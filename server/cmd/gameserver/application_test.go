package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/bootstrap"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/config"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/router"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/session"
)

type appPeer struct {
	conn   net.Conn
	reader *bufio.Reader
	id     uint64
}

func TestApplicationRecordsAuthoritativeCombatMetrics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	metricSet := metrics.New()
	app, err := newGameApplication(context.Background(), logger, metricSet)
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.rooms[1] = &activeRoom{projectiles: make(map[entity.ID]struct{})}
	app.mu.Unlock()

	app.recordSnapshotMetrics(1, room.Snapshot{RoomID: 1, Snapshot: game.Snapshot{
		Monsters: []game.MonsterView{{ID: 7}, {ID: 8}},
	}})
	app.recordEventMetrics(1, game.EventBatch{Events: []game.Event{
		{Kind: game.ProjectileSpawned, EntityID: 9},
		{Kind: game.ProjectileSpawned, EntityID: 9}, // 重复事件不能抬高 Gauge
		{Kind: game.DamageDealt, Amount: 12.5},
		{Kind: game.StageCleared, StageIndex: 1},
	}})

	body := scrapeApplicationMetrics(t, metricSet)
	for _, sample := range []string{
		"odyssey_active_monsters 2",
		"odyssey_active_projectiles 1",
		"odyssey_damage_dealt_total 12.5",
		`odyssey_stage_results_total{result="cleared"} 1`,
	} {
		if !strings.Contains(body, sample) {
			t.Errorf("metrics do not contain %q", sample)
		}
	}

	app.recordEventMetrics(1, game.EventBatch{Events: []game.Event{
		{Kind: game.ProjectileDestroyed, EntityID: 9},
		{Kind: game.TeamDefeated, StageIndex: 2},
	}})
	app.mu.Lock()
	delete(app.rooms, 1)
	app.publishMetricsLocked()
	app.mu.Unlock()

	body = scrapeApplicationMetrics(t, metricSet)
	for _, sample := range []string{
		"odyssey_active_monsters 0",
		"odyssey_active_projectiles 0",
		`odyssey_stage_results_total{result="defeated"} 1`,
	} {
		if !strings.Contains(body, sample) {
			t.Errorf("metrics after cleanup do not contain %q", sample)
		}
	}
}

func TestApplicationMatchMoveAndDisconnectLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	catalogPath, err := filepath.Abs(filepath.Join("..", "..", "..", "data", "equipment", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.EquipmentCatalogPath = catalogPath
	gameplay, err := bootstrap.LoadGameplay(cfg, room.DefaultConfig().World)
	if err != nil {
		t.Fatal(err)
	}
	app, err := newConfiguredGameApplication(ctx, logger, metrics.New(), gameplay)
	if err != nil {
		t.Fatal(err)
	}
	app.roomConfig.EmptyTimeout = 150 * time.Millisecond
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
		if err := <-serveDone; err != nil {
			t.Error(err)
		}
	}()

	legacyConn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	legacy := appPeer{conn: legacyConn, reader: bufio.NewReader(legacyConn)}
	appSend(t, legacy, pb.MessageType_MSG_LOGIN_REQUEST, 1, &pb.LoginRequest{ProtocolVersion: 1})
	var rejected pb.LoginResponse
	appRead(t, legacy, pb.MessageType_MSG_LOGIN_RESPONSE, &rejected)
	if rejected.Reason != pb.ReasonCode_REASON_INVALID_VERSION ||
		rejected.ProtocolVersion != gameplayProtocolVersion || rejected.SessionId != 0 {
		t.Fatalf("legacy client was not rejected: %+v", &rejected)
	}
	legacyConn.Close()

	peers := make([]appPeer, 2)
	for i := range peers {
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		peers[i] = appPeer{conn: conn, reader: bufio.NewReader(conn)}
		appSend(t, peers[i], pb.MessageType_MSG_LOGIN_REQUEST, 1, &pb.LoginRequest{ProtocolVersion: 2})
		var login pb.LoginResponse
		appRead(t, peers[i], pb.MessageType_MSG_LOGIN_RESPONSE, &login)
		if login.Reason != pb.ReasonCode_REASON_OK {
			t.Fatalf("peer %d login failed: %v", i, login.Reason)
		}
		peers[i].id = login.PlayerId
	}

	appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 2, &pb.MatchRequest{})
	appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 3, &pb.MatchRequest{}) // 验证幂等
	appSend(t, peers[1], pb.MessageType_MSG_MATCH_REQUEST, 2, &pb.MatchRequest{})
	var roomID uint64
	for i := range peers {
		var found pb.MatchFound
		appRead(t, peers[i], pb.MessageType_MSG_MATCH_FOUND, &found)
		if found.RoomId == 0 || len(found.Teammates) != 1 {
			t.Fatalf("peer %d invalid MatchFound: room=%d teammates=%d", i, found.RoomId, len(found.Teammates))
		}
		if roomID == 0 {
			roomID = found.RoomId
		} else if found.RoomId != roomID {
			t.Fatalf("peers matched into different rooms: %d and %d", roomID, found.RoomId)
		}
	}
	for i := range peers {
		var started pb.StageStartedEvent
		appRead(t, peers[i], pb.MessageType_MSG_STAGE_STARTED_EVENT, &started)
		if started.StageIndex != 1 {
			t.Fatalf("peer %d initial stage start = %d, want 1", i, started.StageIndex)
		}
	}

	appSend(t, peers[0], pb.MessageType_MSG_PLAYER_INPUT, 4, &pb.PlayerInput{
		InputSeq: 1,
		Move:     &pb.Vec2{X: 1},
	})
	for i := range peers {
		deadline := time.Now().Add(2 * time.Second)
		for {
			var snapshot pb.WorldSnapshot
			appRead(t, peers[i], pb.MessageType_MSG_WORLD_SNAPSHOT, &snapshot)
			if snapshot.Self != nil && len(snapshot.Players) == 1 &&
				len(snapshot.Pickups) == 2 &&
				snapshot.Self.PlayerId == peers[i].id &&
				(i != 0 || (snapshot.LastProcessedInput == 1 && snapshot.Self.Position.X > 10)) {
				kinds := map[pb.PickupKind]bool{}
				for _, pickup := range snapshot.Pickups {
					kinds[pickup.Kind] = true
				}
				if !kinds[pb.PickupKind_PICKUP_KIND_HEALTH] || !kinds[pb.PickupKind_PICKUP_KIND_WEAPON] {
					t.Fatalf("peer %d pickup kinds = %v, want health + weapon", i, kinds)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("peer %d did not receive movement plus two stage pickups", i)
			}
		}
	}

	// EOF 必须从另一名玩家的完整快照中移除断线者，最后一次断线应允许空房间过期。
	if err := peers[0].conn.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var snapshot pb.WorldSnapshot
		appRead(t, peers[1], pb.MessageType_MSG_WORLD_SNAPSHOT, &snapshot)
		if snapshot.Self != nil && snapshot.Self.PlayerId == peers[1].id && len(snapshot.Players) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected player remained in full snapshots")
		}
	}
	if err := peers[1].conn.Close(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		rooms := len(app.rooms)
		connections := len(app.connections)
		app.mu.Unlock()
		if rooms == 0 && connections == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("connections or empty room were not reclaimed")
}

func TestApplicationFirstStageFailureDoesNotAnnounceMatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := newGameApplication(ctx, logger, metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	app.openingStageStarter = func(*room.Room, stage.Plan) error {
		return errors.New("injected first-stage receipt failure")
	}
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
		if err := <-serveDone; err != nil {
			t.Error(err)
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
		if login.Reason != pb.ReasonCode_REASON_OK {
			t.Fatalf("peer %d login failed: %v", i, login.Reason)
		}
	}
	for i := range peers {
		appSend(t, peers[i], pb.MessageType_MSG_MATCH_REQUEST, 2, &pb.MatchRequest{})
	}
	for i := range peers {
		var disconnected pb.Disconnect
		// 若注入失败前错误发送 MatchFound，appRead 会立即失败。
		appRead(t, peers[i], pb.MessageType_MSG_DISCONNECT, &disconnected)
		if disconnected.Reason != pb.ReasonCode_REASON_ROOM_CLOSED {
			t.Fatalf("peer %d unexpected first-stage failure reason: %+v", i, &disconnected)
		}
	}
}

func TestApplicationClearRewardReadyAndAdvanceLifecycle(t *testing.T) {
	for _, opening := range []uint32{1, 3, 11} {
		t.Run(fmt.Sprintf("stage_%d", opening), func(t *testing.T) { testApplicationAdvance(t, opening) })
	}
}

func testApplicationAdvance(t *testing.T, opening uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	catalogPath, err := filepath.Abs(filepath.Join("..", "..", "..", "data", "equipment", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.EquipmentCatalogPath = catalogPath
	gameplay, err := bootstrap.LoadGameplay(cfg, room.DefaultConfig().World)
	if err != nil {
		t.Fatal(err)
	}
	app, err := newConfiguredGameApplication(ctx, logger, metrics.New(), gameplay)
	if err != nil {
		t.Fatal(err)
	}
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
		if err := <-serveDone; err != nil {
			t.Error(err)
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
		if login.Reason != pb.ReasonCode_REASON_OK {
			t.Fatalf("peer %d login failed: %v", i, login.Reason)
		}
		peers[i].id = login.PlayerId
	}

	roomID, active, err := app.newActiveRoom()
	if err != nil {
		t.Fatal(err)
	}
	for i := range peers {
		var member *session.Session
		var serverConn *network.Connection
		app.mu.Lock()
		for connection, candidate := range app.connections {
			_, playerID := candidate.Identity()
			if playerID == peers[i].id {
				member, serverConn = candidate, connection
				break
			}
		}
		app.mu.Unlock()
		if member == nil || !member.Transition(session.StateMatching) {
			t.Fatal("peer did not enter matching")
		}
		if err := router.Join(member, active.room, uint64(roomID)); err != nil {
			t.Fatal(err)
		}
		active.snapshots.Subscribe(entity.ID(peers[i].id), serverConn)
		active.events.Subscribe(entity.ID(peers[i].id), closingSink{connection: serverConn})
		active.close.Subscribe(entity.ID(peers[i].id), closingSink{connection: serverConn})
	}

	plan := stage.Plan{Index: opening, Seed: 42, DifficultyScore: 1, Monsters: []stage.Spawn{{
		Position: entity.Vec2{X: 13, Y: 10}, Radius: 0.4, AttackRange: 1,
		Stats: entity.CombatStats{MaxHealth: 1, MoveSpeed: 0, AttackCooldownTicks: 30},
	}}}
	receipt, err := active.room.StartStage(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-receipt; err != nil {
		t.Fatal(err)
	}
	for i := range peers {
		var started pb.StageStartedEvent
		appRead(t, peers[i], pb.MessageType_MSG_STAGE_STARTED_EVENT, &started)
		if started.StageIndex != opening {
			t.Fatalf("peer %d opening stage = %d, want %d", i, started.StageIndex, opening)
		}
	}
	appSend(t, peers[0], pb.MessageType_MSG_PLAYER_INPUT, 2,
		&pb.PlayerInput{InputSeq: 1, Aim: &pb.Vec2{X: 1}, Shoot: true})

	offers := make([]pb.RewardOptions, len(peers))
	for i := range peers {
		appRead(t, peers[i], pb.MessageType_MSG_REWARD_OPTIONS, &offers[i])
		if offers[i].StageIndex != opening || len(offers[i].EquipmentIds) == 0 {
			t.Fatalf("peer %d invalid private reward offer: %+v", i, &offers[i])
		}
		if i == 0 {
			// 服务端进入 Reward 时 TCP 中可能已有 30Hz 战斗包，应忽略而不是断开玩家。
			appSend(t, peers[i], pb.MessageType_MSG_PLAYER_INPUT, 3,
				&pb.PlayerInput{InputSeq: 2, Move: &pb.Vec2{X: 1}, Aim: &pb.Vec2{X: 1}})
		}
		appSend(t, peers[i], pb.MessageType_MSG_REWARD_CHOICE, 3,
			&pb.RewardChoice{EquipmentId: offers[i].EquipmentIds[0]})
		if i == 0 {
			appSend(t, peers[i], pb.MessageType_MSG_REWARD_CHOICE, 4,
				&pb.RewardChoice{EquipmentId: offers[i].EquipmentIds[0]})
		}
	}
	for i := range peers {
		var applied pb.RewardApplied
		appRead(t, peers[i], pb.MessageType_MSG_REWARD_APPLIED, &applied)
		if applied.Reason != pb.ReasonCode_REASON_OK || applied.EquipmentId != offers[i].EquipmentIds[0] {
			t.Fatalf("peer %d reward not applied: %+v", i, &applied)
		}
		if i == 0 {
			var duplicate pb.RewardApplied
			appRead(t, peers[i], pb.MessageType_MSG_REWARD_APPLIED, &duplicate)
			if duplicate.Reason == pb.ReasonCode_REASON_OK || duplicate.EquipmentId != offers[i].EquipmentIds[0] {
				t.Fatalf("duplicate reward response out of order or accepted: %+v", &duplicate)
			}
		}
		appSend(t, peers[i], pb.MessageType_MSG_NEXT_STAGE_REQUEST, 4, &pb.NextStageRequest{})
		if i == 0 {
			appSend(t, peers[i], pb.MessageType_MSG_NEXT_STAGE_REQUEST, 5, &pb.NextStageRequest{})
		}
	}
	for i := range peers {
		var started pb.StageStartedEvent
		appRead(t, peers[i], pb.MessageType_MSG_STAGE_STARTED_EVENT, &started)
		if started.StageIndex != opening+1 {
			t.Fatalf("peer %d next stage = %d, want %d", i, started.StageIndex, opening+1)
		}
		var snapshot pb.WorldSnapshot
		appRead(t, peers[i], pb.MessageType_MSG_WORLD_SNAPSHOT, &snapshot)
		if snapshot.Stage.StageLimit != 12 || snapshot.Stage.DifficultyScore <= 1 || snapshot.Self.MagazineCapacity != snapshot.Self.Ammo {
			t.Fatalf("stage limit/director/refill not synchronized: %v", &snapshot)
		}
	}
}

func TestApplicationOneClickRematchMovesBothPlayersAndRestoresShooting(t *testing.T) {
	testApplicationReplay(t, false)
}

func TestApplicationVictoryReplayMovesBothPlayersAndRestoresShooting(t *testing.T) {
	testApplicationReplay(t, true)
}

func testApplicationReplay(t *testing.T, victory bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := newGameApplication(ctx, logger, metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	app.roomConfig.EmptyTimeout = 150 * time.Millisecond
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
		if err := <-serveDone; err != nil {
			t.Error(err)
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
		if login.Reason != pb.ReasonCode_REASON_OK {
			t.Fatalf("login failed: %v", login.Reason)
		}
		peers[i].id = login.PlayerId
	}
	oldID, old, err := app.newActiveRoom()
	if err != nil {
		t.Fatal(err)
	}
	for i := range peers {
		var member *session.Session
		var serverConn *network.Connection
		app.mu.Lock()
		for conn, candidate := range app.connections {
			_, playerID := candidate.Identity()
			if playerID == peers[i].id {
				member, serverConn = candidate, conn
				break
			}
		}
		app.mu.Unlock()
		if member == nil || !member.Transition(session.StateMatching) {
			t.Fatal("peer did not enter matching")
		}
		if err := router.Join(member, old.room, uint64(oldID)); err != nil {
			t.Fatal(err)
		}
		old.snapshots.Subscribe(entity.ID(peers[i].id), serverConn)
		old.events.Subscribe(entity.ID(peers[i].id), closingSink{connection: serverConn})
		old.close.Subscribe(entity.ID(peers[i].id), closingSink{connection: serverConn})
	}
	// 可信战斗把真实房间推进到终局；胜利分支像最终关 beginRewardStage 一样标记完成。
	spawn := stage.Spawn{Position: app.roomConfig.World.Spawn, Radius: 0.4,
		AttackRange: 1, Stats: entity.CombatStats{MaxHealth: 100, Attack: 1000, AttackCooldownTicks: 1}}
	if victory {
		spawn.Position = entity.Vec2{X: 13, Y: 10}
		spawn.Stats = entity.CombatStats{MaxHealth: 1, AttackCooldownTicks: 30}
	}
	receipt, err := old.room.StartStage(stage.Plan{Index: 1, Seed: 42, DifficultyScore: 1, Monsters: []stage.Spawn{spawn}})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-receipt; err != nil {
		t.Fatal(err)
	}
	if victory {
		// 仍存活房间和普通通关阶段必须拒绝重赛。
		appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 2, &pb.MatchRequest{})
		appSend(t, peers[0], pb.MessageType_MSG_PLAYER_INPUT, 3,
			&pb.PlayerInput{InputSeq: 1, Aim: &pb.Vec2{X: 1}, Shoot: true})
	}
	want := stage.Failed
	if victory {
		want = stage.StageClear
	}
	deadline := time.Now().Add(2 * time.Second)
	for old.room.LatestSnapshot().Stage.State != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if old.room.LatestSnapshot().Stage.State != want {
		t.Fatalf("old match did not reach %v", want)
	}
	if victory {
		appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 4, &pb.MatchRequest{})
		// 同一连接上的 Pong 用来确认前一个 MatchRequest 已按序处理，再模拟最终关完成回调。
		appSend(t, peers[0], pb.MessageType_MSG_PING, 5, &pb.Ping{})
		var pong pb.Pong
		appRead(t, peers[0], pb.MessageType_MSG_PONG, &pong)
		app.mu.Lock()
		if old.rematching || len(app.rooms) != 1 {
			t.Error("non-final cleared room admitted replay")
		}
		old.completed = true
		app.mu.Unlock()
	}
	appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 5, &pb.MatchRequest{})
	appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 6, &pb.MatchRequest{}) // 重复点击保持幂等
	var newID uint64
	for i := range peers {
		var found pb.MatchFound
		appRead(t, peers[i], pb.MessageType_MSG_MATCH_FOUND, &found)
		if found.RoomId == uint64(oldID) || found.RoomId == 0 || len(found.Teammates) != 1 {
			t.Fatalf("invalid rematch for peer %d: %+v", i, &found)
		}
		if newID == 0 {
			newID = found.RoomId
		} else if newID != found.RoomId {
			t.Fatalf("team split across new rooms: %d and %d", newID, found.RoomId)
		}
	}
	for i := range peers {
		var started pb.StageStartedEvent
		appRead(t, peers[i], pb.MessageType_MSG_STAGE_STARTED_EVENT, &started)
		if started.StageIndex != 1 {
			t.Fatalf("peer %d rematch stage start = %d, want 1", i, started.StageIndex)
		}
	}
	for i := range peers {
		deadline = time.Now().Add(3 * time.Second)
		for {
			var snap pb.WorldSnapshot
			appRead(t, peers[i], pb.MessageType_MSG_WORLD_SNAPSHOT, &snap)
			if snap.Stage != nil && snap.Stage.State == uint32(stage.Playing) && snap.Self != nil &&
				snap.Self.PlayerId == peers[i].id && snap.Self.Alive && snap.Self.Hp > 0 && len(snap.Players) == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("peer %d did not receive a healthy new match", i)
			}
		}
	}
	appSend(t, peers[0], pb.MessageType_MSG_PLAYER_INPUT, 7,
		&pb.PlayerInput{InputSeq: 2, Aim: &pb.Vec2{X: 1}, Shoot: true})
	for i := range peers {
		var spawned pb.ProjectileSpawnEvent
		appRead(t, peers[i], pb.MessageType_MSG_PROJECTILE_SPAWN, &spawned)
		if spawned.OwnerId != peers[0].id {
			t.Fatalf("peer %d saw another shooter's projectile: %+v", i, &spawned)
		}
	}
	app.mu.Lock()
	if len(app.rooms) > 2 || app.rooms[room.ID(newID)] == nil {
		t.Errorf("duplicate click created extra rooms: %d", len(app.rooms))
	}
	app.mu.Unlock()
}

func TestApplicationRematchStartFailureKeepsTeamInFailedRoom(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := newGameApplication(ctx, logger, metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	app.roomConfig.EmptyTimeout = 150 * time.Millisecond
	startAttempted := make(chan struct{}, 1)
	app.openingStageStarter = func(*room.Room, stage.Plan) error {
		startAttempted <- struct{}{}
		return errors.New("injected stage receipt failure")
	}
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
		if err := <-serveDone; err != nil {
			t.Error(err)
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
		if login.Reason != pb.ReasonCode_REASON_OK {
			t.Fatalf("login failed: %v", login.Reason)
		}
		peers[i].id = login.PlayerId
	}
	oldID, old, err := app.newActiveRoom()
	if err != nil {
		t.Fatal(err)
	}
	for i := range peers {
		var member *session.Session
		var serverConn *network.Connection
		app.mu.Lock()
		for conn, candidate := range app.connections {
			_, playerID := candidate.Identity()
			if playerID == peers[i].id {
				member, serverConn = candidate, conn
				break
			}
		}
		app.mu.Unlock()
		if member == nil || !member.Transition(session.StateMatching) {
			t.Fatal("peer did not enter matching")
		}
		if err := router.Join(member, old.room, uint64(oldID)); err != nil {
			t.Fatal(err)
		}
		old.snapshots.Subscribe(entity.ID(peers[i].id), serverConn)
		old.events.Subscribe(entity.ID(peers[i].id), closingSink{connection: serverConn})
		old.close.Subscribe(entity.ID(peers[i].id), closingSink{connection: serverConn})
	}
	spawn := stage.Spawn{Position: app.roomConfig.World.Spawn, Radius: 0.4,
		AttackRange: 1, Stats: entity.CombatStats{MaxHealth: 100, Attack: 1000, AttackCooldownTicks: 1}}
	receipt, err := old.room.StartStage(stage.Plan{Index: 1, Seed: 42, DifficultyScore: 1, Monsters: []stage.Spawn{spawn}})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-receipt; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for old.room.LatestSnapshot().Stage.State != stage.Failed && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if old.room.LatestSnapshot().Stage.State != stage.Failed {
		t.Fatal("old match did not fail")
	}
	appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 2, &pb.MatchRequest{})
	select {
	case <-startAttempted:
	case <-time.After(2 * time.Second):
		t.Fatal("rematch did not attempt to start its first stage")
	}
	deadline = time.Now().Add(time.Second)
	ready := false
	for time.Now().Before(deadline) {
		app.mu.Lock()
		ready = !old.rematching && len(app.rooms) == 1
		app.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("failed rematch did not release the new room and reset the retry guard")
	}
	for i := range peers {
		app.mu.Lock()
		var boundRoom uint64
		for _, member := range app.connections {
			_, playerID := member.Identity()
			if playerID == peers[i].id {
				boundRoom = member.RoomID()
			}
		}
		app.mu.Unlock()
		if boundRoom != uint64(oldID) {
			t.Errorf("peer %d moved to room %d after start failed; want old room %d", i, boundRoom, oldID)
		}
		if err := peers[i].conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		for {
			header, _, readErr := network.ReadFrame(peers[i].reader)
			if timeout, ok := readErr.(net.Error); ok && timeout.Timeout() {
				break
			}
			if readErr != nil {
				t.Fatalf("peer %d disconnected after rematch start failed: %v", i, readErr)
			}
			if header.MessageType == uint16(pb.MessageType_MSG_MATCH_FOUND) {
				t.Errorf("peer %d received MatchFound before stage start succeeded", i)
			}
		}
	}
	app.openingStageStarter = nil // 同两名玩家可以再次尝试
	appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 3, &pb.MatchRequest{})
	for i := range peers {
		var found pb.MatchFound
		appRead(t, peers[i], pb.MessageType_MSG_MATCH_FOUND, &found)
		if found.RoomId == uint64(oldID) || found.RoomId == 0 {
			t.Fatalf("peer %d could not retry after failed rematch: %+v", i, &found)
		}
		var started pb.StageStartedEvent
		appRead(t, peers[i], pb.MessageType_MSG_STAGE_STARTED_EVENT, &started)
		if started.StageIndex != 1 {
			t.Fatalf("peer %d retry did not start stage 1: %+v", i, &started)
		}
	}
}

func appSend(t *testing.T, peer appPeer, messageType pb.MessageType, sequence uint32, message proto.Message) {
	t.Helper()
	body, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := network.EncodeFrame(network.Header{
		Magic: network.Magic, Version: network.VersionV1,
		MessageType: uint16(messageType), Sequence: sequence,
	}, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.conn.Write(frame); err != nil {
		t.Fatal(err)
	}
}

func appRead(t *testing.T, peer appPeer, want pb.MessageType, message proto.Message) {
	t.Helper()
	if err := peer.conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		header, body, err := network.ReadFrame(peer.reader)
		if err != nil {
			t.Fatalf("read %s: %v", want, err)
		}
		if pb.MessageType(header.MessageType) != want {
			if pb.MessageType(header.MessageType) == pb.MessageType_MSG_WORLD_SNAPSHOT ||
				(header.MessageType >= 320 && header.MessageType <= 327) {
				continue
			}
			t.Fatalf("message type = %d, want %s", header.MessageType, want)
		}
		if err := proto.Unmarshal(body, message); err != nil {
			t.Fatal(err)
		}
		return
	}
}

func scrapeApplicationMetrics(t *testing.T, metricSet *metrics.Metrics) string {
	t.Helper()
	request := httptest.NewRequest("GET", "http://metrics.local/metrics", nil)
	response := httptest.NewRecorder()
	metricSet.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("metrics status = %d, want 200", response.Code)
	}
	return response.Body.String()
}
