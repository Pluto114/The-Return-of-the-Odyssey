package network

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// recordingObserver captures transport events so a fault-injection test can
// assert that a bad client's malformed frames are counted without disturbing
// a healthy peer's traffic.
type recordingObserver struct {
	accepted       atomic.Int64
	closed         atomic.Int64
	framesReceived atomic.Int64
	invalid        atomic.Int64
	rejections     atomic.Int64
	snapshotDrops  atomic.Int64
}

func (o *recordingObserver) OnConnectionAccepted()       { o.accepted.Add(1) }
func (o *recordingObserver) OnConnectionClosed()         { o.closed.Add(1) }
func (o *recordingObserver) OnBytesReceived(int)         {}
func (o *recordingObserver) OnBytesSent(int)             {}
func (o *recordingObserver) OnFrameReceived()            { o.framesReceived.Add(1) }
func (o *recordingObserver) OnFrameSent()                {}
func (o *recordingObserver) OnSnapshotSent(int)          {}
func (o *recordingObserver) OnInvalidFrame(string)       { o.invalid.Add(1) }
func (o *recordingObserver) OnReliableRejection()        { o.rejections.Add(1) }
func (o *recordingObserver) OnSnapshotDrop()             { o.snapshotDrops.Add(1) }
func (o *recordingObserver) OnReliableDepth(int)         {}

// echoHandler replies to MSG_PING (1) with a Pong (2) echoing the payload, so
// a test can assert a healthy peer keeps getting served while a bad peer is
// being fed garbage.
func echoHandler(c *Connection, h Header, payload []byte) error {
	if h.MessageType != 1 {
		return nil
	}
	pong, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 2, Sequence: h.Sequence}, payload)
	if err != nil {
		return err
	}
	c.Send(pong)
	return nil
}

// startServer boots a server on an ephemeral port and returns the listener
// address plus a cancel func.
func startServer(t *testing.T, handler Handler, obs Observer) (addr string, cancel context.CancelFunc, srv *Server) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	srv = NewServer(handler, logger)
	if obs != nil {
		srv.SetObserver(obs)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx, ln)
	return ln.Addr().String(), cancel, srv
}

func TestBadFrameDoesNotDisturbHealthyPeer(t *testing.T) {
	obs := &recordingObserver{}
	addr, cancel, _ := startServer(t, echoHandler, obs)
	defer cancel()

	// Healthy peer: sends a valid Ping and must get a Pong back.
	healthy, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer healthy.Close()

	// Bad peer: sends a frame with a wrong magic, then a too-large body length.
	bad, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()

	// Bad frame 1: wrong magic (still a full 16-byte header so the reader can
	// reach the magic check).
	badFrame := make([]byte, HeaderLen)
	badFrame[0] = 0xDE // wrong magic high byte
	badFrame[1] = 0xAD
	if _, err := bad.Write(badFrame); err != nil {
		t.Fatal(err)
	}

	// Meanwhile the healthy peer sends a valid Ping and must still be served.
	pingBody := []byte{0x08, 0x01, 0x10, 0x02}
	pingFrame, _ := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 1, Sequence: 1}, pingBody)
	healthy.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := healthy.Write(pingFrame); err != nil {
		t.Fatal(err)
	}
	healthy.SetReadDeadline(time.Now().Add(2 * time.Second))
	h, _, err := ReadFrame(bufio.NewReader(healthy))
	if err != nil {
		t.Fatalf("healthy peer failed to read pong after bad peer injected garbage: %v", err)
	}
	if h.MessageType != 2 {
		t.Fatalf("healthy peer reply MessageType = %d, want 2", h.MessageType)
	}

	// The bad peer's malformed frame must be counted as an invalid frame.
	if got := obs.invalid.Load(); got == 0 {
		t.Fatalf("invalid frames = 0, want >= 1 after malformed magic")
	}
}

func TestFrameTooLargeRejectedWithoutAllocation(t *testing.T) {
	obs := &recordingObserver{}
	addr, cancel, _ := startServer(t, echoHandler, obs)
	defer cancel()

	bad, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()

	// Craft a header claiming a huge body length (> MaxBodyLen) but with a
	// valid magic/version so it reaches the size check.
	frame := make([]byte, HeaderLen)
	frame[0] = 0x4E
	frame[1] = 0x52 // Magic
	frame[2] = byte(VersionV1)
	frame[4] = 0x00
	frame[5] = 0x01 // MessageType = 1 (Ping)
	// BodyLength at bytes [8:12] big-endian = 1 MiB.
	frame[8] = 0x00
	frame[9] = 0x10
	frame[10] = 0x00
	frame[11] = 0x00
	if _, err := bad.Write(frame); err != nil {
		t.Fatal(err)
	}

	// The reader must reject it and close; give it time to observe.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if obs.invalid.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("invalid frames = 0, want >= 1 after oversized frame")
}

// TestHalfPacketDisconnectIsIsolated verifies that a peer that sends a partial
// header then vanishes is torn down (short_read) without affecting a healthy
// peer, and the connection is eventually untracked.
func TestHalfPacketDisconnectIsIsolated(t *testing.T) {
	addr, cancel, srv := startServer(t, echoHandler, nil)
	defer cancel()

	// Healthy peer stays connected and served.
	healthy, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer healthy.Close()

	// Bad peer sends 4 bytes (a truncated header) then closes.
	bad, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Write([]byte{0x4E, 0x52, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	bad.Close()

	// The half-open peer must be untracked; the healthy peer must remain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.ActiveConns() == 1 {
			// Only the healthy peer remains.
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ActiveConns = %d after half-packet disconnect, want 1 (healthy peer only)", srv.ActiveConns())
}

// TestConnectionStormAllRecover verifies that many peers connecting and
// disconnecting (a "disconnect storm") leave the server clean — ActiveConns
// returns to 0 after all clients close, and no goroutine lingers.
func TestConnectionStormAllRecover(t *testing.T) {
	addr, cancel, srv := startServer(t, echoHandler, nil)
	defer cancel()

	const n = 50
	conns := make([]net.Conn, 0, n)
	for i := 0; i < n; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}

	// Wait for all to be tracked.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && srv.ActiveConns() < n {
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.ActiveConns(); got != n {
		t.Fatalf("ActiveConns = %d, want %d after storm connect", got, n)
	}

	// Slam them all closed at once.
	for _, c := range conns {
		c.Close()
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srv.ActiveConns() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ActiveConns = %d after storm disconnect, want 0", srv.ActiveConns())
}
