package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/protocolbridge"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/session"
	"google.golang.org/protobuf/proto"
)

// This test-only assembly reuses A's actual TCP codec, login handlers and Session
// with B's actual Room. Fixed room assignment replaces D's missing matchmaker;
// it is NOT a production matchmaking implementation or a C++ client test.
func TestTCPProtocolToRoomAndTwoRecipientSnapshots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r, err := room.Start(ctx, 99, room.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids := &idAllocator{}
	var conns sync.Map
	var cursors sync.Map
	handler := func(c *network.Connection, h network.Header, b []byte) error {
		conns.Store(c, true)
		mt := pb.MessageType(h.MessageType)
		if mt != pb.MessageType_MSG_MATCH_REQUEST && mt != pb.MessageType_MSG_PLAYER_INPUT {
			return routeMessage(c, h, b, ids, logger)
		}
		sess, ok := c.Context().(*session.Session)
		if !ok {
			return routeMessage(c, h, b, ids, logger)
		}
		if accepted, reason := sess.Accept(mt); !accepted {
			return sendDisconnect(c, h, reason, "integration state rejection")
		}
		sid, pid := sess.Identity()
		if mt == pb.MessageType_MSG_MATCH_REQUEST {
			var request pb.MatchRequest
			if err := proto.Unmarshal(b, &request); err != nil {
				return err
			}
			if !sess.Transition(session.StateMatching) {
				return fmt.Errorf("match transition failed")
			}
			joined, err := r.Join(room.SessionID(sid), entity.ID(pid))
			if err != nil {
				return err
			}
			select {
			case err := <-joined:
				if err != nil {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			// Queue MatchFound before exposing InRoom to the snapshot dispatcher.
			if err := sendMessage(c, h, pb.MessageType_MSG_MATCH_FOUND, &pb.MatchFound{RoomId: 99}); err != nil {
				return err
			}
			if !sess.Transition(session.StateInRoom) {
				return fmt.Errorf("join transition failed")
			}
			return nil
		}
		var input pb.PlayerInput
		if err := proto.Unmarshal(b, &input); err != nil {
			return err
		}
		var last uint64
		if old, ok := cursors.Load(c); ok {
			last = old.(uint64)
		}
		intent, err := protocolbridge.Input(&input, last)
		if err != nil {
			return err
		}
		if err := r.Input(room.SessionID(sid), intent); err != nil {
			return err
		}
		cursors.Store(c, intent.Seq)
		return nil
	}
	srv := network.NewServer(handler, logger)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		r.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx, ln) }()
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		for s := range r.Snapshots() {
			conns.Range(func(key, value any) bool {
				c := key.(*network.Connection)
				sess, ok := c.Context().(*session.Session)
				if !ok || sess.State() != session.StateInRoom {
					return true
				}
				_, pid := sess.Identity()
				out, err := protocolbridge.Snapshot(s, entity.ID(pid), nil)
				if err != nil {
					return true
				}
				if err := sendMessage(c, network.Header{}, pb.MessageType_MSG_WORLD_SNAPSHOT, out); err != nil {
					t.Errorf("snapshot send: %v", err)
				}
				return true
			})
		}
	}()
	defer func() {
		cancel()
		r.Close()
		<-pumpDone
		conns.Range(func(key, value any) bool { key.(*network.Connection).Close(); return true })
		select {
		case err := <-serveDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server did not exit")
		}
	}()
	type peer struct {
		conn   net.Conn
		reader *bufio.Reader
		id     uint64
	}
	send := func(p peer, mt pb.MessageType, seq uint32, message proto.Message, fragment bool) {
		t.Helper()
		body, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := network.EncodeFrame(network.Header{Magic: network.Magic, Version: network.VersionV1, MessageType: uint16(mt), Sequence: seq}, body)
		if err != nil {
			t.Fatal(err)
		}
		p.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if fragment {
			for _, b := range frame {
				if _, err := p.conn.Write([]byte{b}); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			if _, err := p.conn.Write(frame); err != nil {
				t.Fatal(err)
			}
		}
	}
	read := func(p peer, want pb.MessageType, message proto.Message) network.Header {
		t.Helper()
		p.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			h, b, err := network.ReadFrame(p.reader)
			if err != nil {
				t.Fatal(err)
			}
			if pb.MessageType(h.MessageType) == want {
				if err := proto.Unmarshal(b, message); err != nil {
					t.Fatal(err)
				}
				return h
			}
			if pb.MessageType(h.MessageType) != pb.MessageType_MSG_WORLD_SNAPSHOT {
				t.Fatalf("unexpected message %d", h.MessageType)
			}
		}
	}
	var peers []peer
	for range 2 {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		p := peer{conn: conn, reader: bufio.NewReader(conn)}
		send(p, pb.MessageType_MSG_PING, 17, &pb.Ping{Nonce: 123}, true)
		var pong pb.Pong
		read(p, pb.MessageType_MSG_PONG, &pong)
		if pong.Nonce != 123 {
			t.Fatal("ping nonce changed")
		}
		send(p, pb.MessageType_MSG_LOGIN_REQUEST, 18, &pb.LoginRequest{ProtocolVersion: 1}, false)
		var login pb.LoginResponse
		read(p, pb.MessageType_MSG_LOGIN_RESPONSE, &login)
		if login.Reason != pb.ReasonCode_REASON_OK {
			t.Fatal("login failed")
		}
		p.id = login.PlayerId
		send(p, pb.MessageType_MSG_MATCH_REQUEST, 19, &pb.MatchRequest{}, false)
		var match pb.MatchFound
		read(p, pb.MessageType_MSG_MATCH_FOUND, &match)
		if match.RoomId != 99 {
			t.Fatal("wrong room")
		}
		peers = append(peers, p)
	}
	send(peers[0], pb.MessageType_MSG_PLAYER_INPUT, 900, &pb.PlayerInput{InputSeq: 1, Move: &pb.Vec2{X: 1}, AimDeg: 90}, false)
	send(peers[1], pb.MessageType_MSG_PLAYER_INPUT, 1000, &pb.PlayerInput{InputSeq: 1, Move: &pb.Vec2{Y: 1}, AimDeg: 180}, false)
	states := []map[uint64]*pb.WorldSnapshot{{}, {}}
	var common uint64
	for attempt := 0; attempt < 15 && common == 0; attempt++ {
		for i, p := range peers {
			var s pb.WorldSnapshot
			read(p, pb.MessageType_MSG_WORLD_SNAPSHOT, &s)
			if s.LastProcessedInput == 1 && len(s.Entities) == 1 {
				states[i][s.ServerTick] = &s
				if states[1-i][s.ServerTick] != nil {
					common = s.ServerTick
					break
				}
			}
		}
	}
	if common == 0 {
		t.Fatal("no common acknowledged two-player snapshot")
	}
	a, b := states[0][common], states[1][common]
	if a.Self.PlayerId != peers[0].id || b.Self.PlayerId != peers[1].id || a.Self.Position.X <= 10 || b.Self.Position.Y <= 10 {
		t.Fatal("authoritative movement did not reach both recipients")
	}
	if !proto.Equal(a.Self.Position, b.Entities[0].Position) || !proto.Equal(b.Self.Position, a.Entities[0].Position) {
		t.Fatal("same-tick views disagree")
	}
	if a.LastProcessedInput != 1 || b.LastProcessedInput != 1 {
		t.Fatal("Frame Sequence contaminated input ack")
	}
}
