package main

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/persistence"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

type captureResultWriter struct {
	mu       sync.Mutex
	attempts []persistence.ResultEnvelope
	fullFor  int
	accepted chan persistence.ResultEnvelope
}

func (w *captureResultWriter) Submit(envelope persistence.ResultEnvelope) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.attempts = append(w.attempts, envelope.Clone())
	if len(w.attempts) <= w.fullFor {
		return persistence.ErrResultQueueFull
	}
	if w.accepted != nil {
		w.accepted <- envelope.Clone()
	}
	return nil
}

func startResultTestRoom(t *testing.T, app *gameApplication, spawn stage.Spawn) (room.ID, *activeRoom) {
	t.Helper()
	roomID, active, err := app.newActiveRoom()
	if err != nil {
		t.Fatal(err)
	}
	for id := uint64(1); id <= 2; id++ {
		receipt, joinErr := active.room.Join(room.SessionID(id), entity.ID(id))
		if joinErr != nil {
			t.Fatal(joinErr)
		}
		if joinErr = <-receipt; joinErr != nil {
			t.Fatal(joinErr)
		}
	}
	receipt, err := active.room.StartStage(stage.Plan{Index: 1, Seed: 42, DifficultyScore: 1, Monsters: []stage.Spawn{spawn}})
	if err != nil {
		t.Fatal(err)
	}
	if err = <-receipt; err != nil {
		t.Fatal(err)
	}
	return roomID, active
}

func TestApplicationQueuesOneVictoryResultWithStableRetryID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := newGameApplication(ctx, logger, metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	writer := &captureResultWriter{fullFor: 2}
	app.setResultWriter(writer)
	spawn := stage.Spawn{Position: entity.Vec2{X: 13, Y: 10}, Radius: 0.4, AttackRange: 1,
		Stats: entity.CombatStats{MaxHealth: 1, MoveSpeed: 0, AttackCooldownTicks: 30}}
	roomID, active := startResultTestRoom(t, app, spawn)
	if err := active.room.Input(1, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for active.room.LatestSnapshot().Stage.State != stage.StageClear && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active.room.LatestSnapshot().Stage.State != stage.StageClear {
		t.Fatal("room did not clear its first stage")
	}
	app.submitGameResult(roomID, game.GameVictory)
	app.submitGameResult(roomID, game.GameVictory)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.attempts) != 3 {
		t.Fatalf("result attempts = %d, want 2 queue-full and 1 accepted", len(writer.attempts))
	}
	for _, envelope := range writer.attempts {
		if envelope.MatchID != active.matchID || envelope.RoomID != uint64(roomID) ||
			envelope.Result.Outcome != game.GameVictory || envelope.Result.FinalStageIndex != 1 {
			t.Fatalf("invalid stable victory envelope: %+v", envelope)
		}
		if err := envelope.Validate(); err != nil {
			t.Fatalf("invalid result: %v", err)
		}
	}
	otherID, other, err := app.newActiveRoom()
	if err != nil {
		t.Fatal(err)
	}
	if otherID == roomID || other.matchID == active.matchID {
		t.Fatal("different rooms shared a result identity")
	}
}

func TestApplicationTeamDefeatQueuesAuthoritativeResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := newGameApplication(ctx, logger, metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	writer := &captureResultWriter{accepted: make(chan persistence.ResultEnvelope, 2)}
	app.setResultWriter(writer)
	spawn := stage.Spawn{Position: app.roomConfig.World.Spawn, Radius: 0.4, AttackRange: 1,
		Stats: entity.CombatStats{MaxHealth: 100, Attack: 1000, AttackCooldownTicks: 1}}
	roomID, active := startResultTestRoom(t, app, spawn)
	select {
	case envelope := <-writer.accepted:
		if envelope.MatchID != active.matchID || envelope.RoomID != uint64(roomID) || envelope.Result.Outcome != game.GameDefeat {
			t.Fatalf("unexpected defeat result: %+v", envelope)
		}
		if err := envelope.Validate(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authoritative TeamDefeated did not enqueue a result")
	}
	app.submitGameResult(roomID, game.GameDefeat)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.attempts) != 1 {
		t.Fatalf("duplicate defeat result attempts: %d", len(writer.attempts))
	}
}

func TestApplicationFinalClearDisconnectKeepsVictoryResult(t *testing.T) {
	result := game.GameResult{Outcome: game.GameAbandoned, StartedAtTick: 1, EndedAtTick: 400,
		FinalStageIndex: 3, ClearedStages: []game.StageSummary{{Index: 1, ClearTick: 100},
			{Index: 2, ClearTick: 200}, {Index: 3, ClearTick: 300}}}
	if got := terminalResultOnDeparture(result, 3); got.Outcome != game.GameVictory || got.EndedAtTick != 300 {
		t.Fatalf("final clear should remain a victory despite disconnect: %+v", got)
	}
	if got := terminalResultOnDeparture(result, 4); got.Outcome != game.GameAbandoned || got.EndedAtTick != 400 {
		t.Fatalf("non-final clear should still be abandoned: %+v", got)
	}
}

func TestApplicationLastDisconnectQueuesAbandonedResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := newGameApplication(ctx, logger, metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	app.roomConfig.EmptyTimeout = 200 * time.Millisecond
	writer := &captureResultWriter{accepted: make(chan persistence.ResultEnvelope, 2)}
	app.setResultWriter(writer)
	srv := network.NewServer(app.handle, logger)
	srv.OnDisconnect(app.disconnected)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx, listener) }()
	defer func() {
		cancel()
		srv.CloseConnections()
		if serveErr := <-serveDone; serveErr != nil {
			t.Error(serveErr)
		}
	}()

	peers := make([]appPeer, 2)
	for i := range peers {
		conn, dialErr := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if dialErr != nil {
			t.Fatal(dialErr)
		}
		peers[i] = appPeer{conn: conn, reader: bufio.NewReader(conn)}
		defer conn.Close()
		appSend(t, peers[i], pb.MessageType_MSG_LOGIN_REQUEST, 1, &pb.LoginRequest{ProtocolVersion: 2})
		var login pb.LoginResponse
		appRead(t, peers[i], pb.MessageType_MSG_LOGIN_RESPONSE, &login)
		appSend(t, peers[i], pb.MessageType_MSG_MATCH_REQUEST, 2, &pb.MatchRequest{})
	}
	for i := range peers {
		var match pb.MatchFound
		appRead(t, peers[i], pb.MessageType_MSG_MATCH_FOUND, &match)
		var started pb.StageStartedEvent
		appRead(t, peers[i], pb.MessageType_MSG_STAGE_STARTED_EVENT, &started)
	}
	if err := peers[0].conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-writer.accepted:
		t.Fatal("first teammate departure prematurely abandoned the expedition")
	case <-time.After(100 * time.Millisecond):
	}
	if err := peers[1].conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case envelope := <-writer.accepted:
		if envelope.Result.Outcome != game.GameAbandoned || envelope.Result.FinalStageIndex != 1 ||
			envelope.MatchID == "" || len(envelope.Result.Players) != 2 {
			t.Fatalf("unexpected abandoned result: %+v", envelope)
		}
		if err := envelope.Validate(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("last teammate departure did not enqueue abandonment")
	}
	select {
	case duplicate := <-writer.accepted:
		t.Fatalf("duplicate terminal result: %+v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestApplicationFullResultQueueDoesNotKeepAbandonedRoomAlive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, err := newGameApplication(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	app.roomConfig.EmptyTimeout = 100 * time.Millisecond
	writer := &captureResultWriter{fullFor: 1 << 30}
	app.setResultWriter(writer)
	spawn := stage.Spawn{Position: entity.Vec2{X: 19, Y: 19}, Radius: 0.4, AttackRange: 1,
		Stats: entity.CombatStats{MaxHealth: 100, MoveSpeed: 0, AttackCooldownTicks: 30}}
	roomID, active := startResultTestRoom(t, app, spawn)
	done := make(chan struct{})
	go func() {
		app.submitGameResultAfterCapture(roomID, game.GameAbandoned, func() {
			// 结果独立后才模拟两名玩家的最终 Leave 回执。
			for id := uint64(1); id <= 2; id++ {
				receipt, leaveErr := active.room.Leave(room.SessionID(id))
				if leaveErr == nil {
					leaveErr = <-receipt
				}
				if leaveErr != nil {
					t.Errorf("leave %d: %v", id, leaveErr)
				}
			}
		})
		close(done)
	}()
	select {
	case <-active.room.Done():
		// 即使每次 writer 尝试都返回队列满，房间仍能回收自身。
	case <-time.After(2 * time.Second):
		t.Fatal("full result queue kept the abandoned room alive")
	}
	writer.mu.Lock()
	attempts := len(writer.attempts)
	writer.mu.Unlock()
	if attempts == 0 {
		t.Fatal("room closed without trying to preserve its result")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("result delivery ignored application cancellation")
	}
}
