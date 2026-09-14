package main

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
)

type appPeer struct {
	conn   net.Conn
	reader *bufio.Reader
	id     uint64
}

func TestApplicationMatchMoveAndDisconnectLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The application loads the versioned equipment catalog from the repo root
	// (data/equipment/catalog.json) relative to the process working directory.
	t.Chdir("../../..")
	app, err := newGameApplication(ctx, logger, metrics.New(), time.Second)
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
			// Skip unrelated authoritative messages that arrive while the test
			// waits for a specific type: periodic snapshots and B's reliable
			// combat/stage events (320..327) that fire once the room starts.
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
