package main

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
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
		{Kind: game.ProjectileSpawned, EntityID: 9}, // duplicate must not inflate the Gauge
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
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		peers[i] = appPeer{conn: conn, reader: bufio.NewReader(conn)}
		appSend(t, peers[i], pb.MessageType_MSG_LOGIN_REQUEST, 1, &pb.LoginRequest{ProtocolVersion: 1})
		var login pb.LoginResponse
		appRead(t, peers[i], pb.MessageType_MSG_LOGIN_RESPONSE, &login)
		if login.Reason != pb.ReasonCode_REASON_OK {
			t.Fatalf("peer %d login failed: %v", i, login.Reason)
		}
		peers[i].id = login.PlayerId
	}

	appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 2, &pb.MatchRequest{})
	appSend(t, peers[0], pb.MessageType_MSG_MATCH_REQUEST, 3, &pb.MatchRequest{}) // idempotent
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
				snapshot.Self.PlayerId == peers[i].id &&
				(i != 0 || (snapshot.LastProcessedInput == 1 && snapshot.Self.Position.X > 10)) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("peer %d did not receive authoritative two-player movement", i)
			}
		}
	}

	// EOF must remove the disconnected player from the surviving peer's full
	// snapshot and the final disconnect must allow the empty room to expire.
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
			if pb.MessageType(header.MessageType) == pb.MessageType_MSG_WORLD_SNAPSHOT {
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
