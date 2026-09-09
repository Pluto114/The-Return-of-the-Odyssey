package network

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"net"
	"testing"
	"time"
)

// TestSendSnapshotLatestWins verifies that a slow consumer only ever sees the
// newest snapshot: an unread snapshot is replaced by a newer one rather than
// queued. The reliable queue is empty here, so the writer immediately drains
// the snapshot slot and the final frame on the wire must be the last snapshot.
func TestSendSnapshotLatestWins(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	var got *Connection
	ready := make(chan struct{})
	handler := func(c *Connection, h Header, payload []byte) error {
		got = c
		close(ready)
		return nil
	}
	srv := NewServer(handler, logger)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(context.Background(), ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Drive the handler once so we can capture the server-side *Connection.
	ping, _ := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 1}, []byte{0x00})
	if _, err := conn.Write(ping); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never ran")
	}

	// Publish a burst of snapshots faster than the writer drains. Each frame
	// carries a distinct MessageType so we can identify which one survives.
	for i := 0; i < 20; i++ {
		f, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: uint16(300 + i)}, []byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		if !got.SendSnapshot(f) {
			t.Fatalf("SendSnapshot(%d) = false", i)
		}
	}

	// The last snapshot (MessageType 319) must be the one the peer reads. Give
	// the writer a moment to flush.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	var last uint16
	for {
		h, _, err := ReadFrame(r)
		if err != nil {
			break
		}
		last = h.MessageType
	}
	// The peer sees the ping echo (nothing) then a snapshot; the final frame
	// must be the newest snapshot, never an older one.
	if last != 319 {
		t.Fatalf("last snapshot MessageType = %d, want 319 (newest wins)", last)
	}
}
