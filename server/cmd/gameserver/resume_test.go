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

// TestResumeRebindsSameIdentity drives the full D8 recovery path over real TCP
// connections: login, match into a room, drop the connection, then resume with
// the issued token onto a new connection. The resumed connection must receive a
// ResumeResponse with the SAME session/player IDs and a full authoritative
// snapshot, and must be able to continue sending input.
func TestResumeRebindsSameIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Chdir("../../..")
	app, err := newGameApplication(ctx, logger, metrics.New(), 2*time.Second)
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

	dial := func() appPeer {
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return appPeer{conn: conn, reader: bufio.NewReader(conn)}
	}

	// Player 1 logs in and matches (two players are needed for a room).
	p1 := dial()
	appSend(t, p1, pb.MessageType_MSG_LOGIN_REQUEST, 1, &pb.LoginRequest{ProtocolVersion: 1})
	var login1 pb.LoginResponse
	appRead(t, p1, pb.MessageType_MSG_LOGIN_RESPONSE, &login1)
	if login1.Reason != pb.ReasonCode_REASON_OK || len(login1.ResumeToken) == 0 {
		t.Fatalf("login failed or no resume token: %v", login1.Reason)
	}
	p1.id = login1.PlayerId

	p2 := dial()
	appSend(t, p2, pb.MessageType_MSG_LOGIN_REQUEST, 1, &pb.LoginRequest{ProtocolVersion: 1})
	var login2 pb.LoginResponse
	appRead(t, p2, pb.MessageType_MSG_LOGIN_RESPONSE, &login2)
	if login2.Reason != pb.ReasonCode_REASON_OK {
		t.Fatalf("peer2 login failed: %v", login2.Reason)
	}
	p2.id = login2.PlayerId

	appSend(t, p1, pb.MessageType_MSG_MATCH_REQUEST, 2, &pb.MatchRequest{})
	appSend(t, p2, pb.MessageType_MSG_MATCH_REQUEST, 2, &pb.MatchRequest{})
	var found1, found2 pb.MatchFound
	appRead(t, p1, pb.MessageType_MSG_MATCH_FOUND, &found1)
	appRead(t, p2, pb.MessageType_MSG_MATCH_FOUND, &found2)
	if found1.RoomId == 0 || found1.RoomId != found2.RoomId {
		t.Fatalf("peers not in same room: %d vs %d", found1.RoomId, found2.RoomId)
	}

	// Move once so there is authoritative state, then drop p1's connection.
	appSend(t, p1, pb.MessageType_MSG_PLAYER_INPUT, 3, &pb.PlayerInput{InputSeq: 1, Move: &pb.Vec2{X: 1}, Aim: &pb.Vec2{Y: 1}})
	readUntilAck(t, p1, p1.id, 1)
	p1.conn.Close()
	// Give the disconnect callback time to run before resuming.
	time.Sleep(50 * time.Millisecond)

	// Resume onto a fresh connection with the issued token.
	p1r := dial()
	appSend(t, p1r, pb.MessageType_MSG_RESUME_REQUEST, 5, &pb.ResumeRequest{
		ResumeToken:     login1.ResumeToken,
		ProtocolVersion: 1,
	})
	var resume pb.ResumeResponse
	appRead(t, p1r, pb.MessageType_MSG_RESUME_RESPONSE, &resume)
	if resume.Reason != pb.ReasonCode_REASON_OK {
		t.Fatalf("resume failed: %v", resume.Reason)
	}
	if resume.SessionId != login1.SessionId || resume.PlayerId != login1.PlayerId {
		t.Fatalf("resume changed identity: session %d->%d player %d->%d",
			login1.SessionId, resume.SessionId, login1.PlayerId, resume.PlayerId)
	}

	// The resumed connection must receive a full authoritative snapshot.
	var snap pb.WorldSnapshot
	readUntilSnapshot(t, p1r, &snap)
	if snap.Self == nil || snap.Self.PlayerId != p1.id {
		t.Fatalf("resume snapshot missing self (player %d)", p1.id)
	}

	// The resumed connection can continue sending input.
	appSend(t, p1r, pb.MessageType_MSG_PLAYER_INPUT, 6, &pb.PlayerInput{InputSeq: 2, Move: &pb.Vec2{Y: 1}, Aim: &pb.Vec2{X: 1}})
	readUntilAck(t, p1r, p1.id, 2)

	// Clean up.
	p2.conn.Close()
	p1r.conn.Close()
}

// TestResumeRejectsForgedToken verifies that a token the server never issued is
// rejected with REASON_RESUME_TOKEN_INVALID and does not create a session.
func TestResumeRejectsForgedToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Chdir("../../..")
	app, err := newGameApplication(ctx, logger, metrics.New(), time.Second)
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

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	peer := appPeer{conn: conn, reader: bufio.NewReader(conn)}

	appSend(t, peer, pb.MessageType_MSG_RESUME_REQUEST, 1, &pb.ResumeRequest{
		ResumeToken:     []byte("this-token-was-never-issued"),
		ProtocolVersion: 1,
	})
	var resp pb.ResumeResponse
	appRead(t, peer, pb.MessageType_MSG_RESUME_RESPONSE, &resp)
	if resp.Reason != pb.ReasonCode_REASON_RESUME_TOKEN_INVALID {
		t.Fatalf("forged token rejected with %v, want REASON_RESUME_TOKEN_INVALID", resp.Reason)
	}

	// The connection must not have gained a session identity.
	app.mu.Lock()
	connectionCount := len(app.connections)
	app.mu.Unlock()
	if connectionCount != 0 {
		t.Fatalf("forged resume created a tracked connection: %d", connectionCount)
	}
	peer.conn.Close()
}

// readUntilAck reads snapshots until this player's input seq is acknowledged.
func readUntilAck(t *testing.T, peer appPeer, selfID uint64, want uint32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var snap pb.WorldSnapshot
		readUntilSnapshot(t, peer, &snap)
		if snap.Self != nil && snap.Self.PlayerId == selfID && snap.LastProcessedInput >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("input %d was not acknowledged for player %d", want, selfID)
		}
	}
}

// readUntilSnapshot reads frames until a WorldSnapshot arrives, skipping
// reliable combat/reward events.
func readUntilSnapshot(t *testing.T, peer appPeer, snap *pb.WorldSnapshot) {
	t.Helper()
	if err := peer.conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		header, body, err := network.ReadFrame(peer.reader)
		if err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		mt := pb.MessageType(header.MessageType)
		if mt == pb.MessageType_MSG_WORLD_SNAPSHOT {
			if err := proto.Unmarshal(body, snap); err != nil {
				t.Fatal(err)
			}
			return
		}
		// Skip any non-snapshot message (events, reward, etc.).
	}
}
