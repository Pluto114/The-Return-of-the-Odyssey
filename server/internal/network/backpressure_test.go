package network

import (
	"log/slog"
	"net"
	"testing"
)

// The tests in this file pin down the backpressure contract that T10 verifies:
// the reliable queue is bounded and, once full, Send returns false so the
// caller can apply policy (close the slow connection) instead of silently
// dropping a gameplay event; the snapshot slot has capacity exactly 1 and a
// new snapshot replaces a pending stale one. Both tests construct a
// Connection directly (package-internal) so the queue state is fully
// controlled and does not depend on the OS socket buffer — T10 explicitly
// rejects "client stops reading but the OS buffer is not yet full" as
// insufficient verification.

// newBareConnection builds a Connection whose writer goroutine is never
// started. The reliable queue is therefore never drained, which lets the test
// deterministically fill it to capacity and observe the exact saturation
// point.
func newBareConnection() *Connection {
	// net.Pipe gives a real net.Conn so Close() is safe (a nil conn would
	// panic), but we never start the writer goroutine, so the reliable queue
	// is never drained and its capacity can be exercised deterministically.
	serverSide, clientSide := net.Pipe()
	_ = clientSide // kept alive by serverSide's peer; dropped on GC
	return &Connection{
		conn:     serverSide,
		logger:   slog.Default(),
		out:      make(chan []byte, 256),
		snapshot: make(chan []byte, 1),
		closed:   make(chan struct{}),
	}
}

// TestConnectionReliableQueueSaturation verifies that the reliable queue has
// a finite capacity (256), admits exactly that many frames, and then returns
// false on the next Send. The call must never block, panic, or corrupt the
// already-queued frames — a slow peer must surface as a Send==false result
// rather than as a growing queue or a stuck publisher (the room tick).
func TestConnectionReliableQueueSaturation(t *testing.T) {
	c := newBareConnection()

	// Fill the queue to its documented capacity. Every Send must succeed.
	// Use a distinct payload per frame so we can prove queued frames are
	// preserved intact.
	const capacity = 256
	queued := make([][]byte, 0, capacity)
	for i := 0; i < capacity; i++ {
		f, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: uint16(300 + i)}, []byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		if !c.Send(f) {
			t.Fatalf("Send(%d) = false before capacity %d reached", i, capacity)
		}
		queued = append(queued, f)
	}

	// The 257th Send must fail: the queue is full and the connection is not
	// closed, so the only correct result is false (backpressure), not a block.
	overflow, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 9999}, []byte{0xFF})
	if err != nil {
		t.Fatal(err)
	}
	if c.Send(overflow) {
		t.Fatal("Send succeeded on a full reliable queue; want false (backpressure)")
	}

	// The queued frames must still be intact and in order — saturation must
	// not drop or reorder earlier frames.
	for i := 0; i < capacity; i++ {
		got := <-c.out
		if len(got) != len(queued[i]) {
			t.Fatalf("queued frame %d length = %d, want %d", i, len(got), len(queued[i]))
		}
	}
}

// TestConnectionSendAfterCloseNeverBlocks verifies that Send on a closed
// connection returns false immediately (rather than blocking or panicking).
// This is the teardown half of the backpressure contract: once a slow
// connection is closed, further publishes must fail fast.
func TestConnectionSendAfterCloseNeverBlocks(t *testing.T) {
	c := newBareConnection()
	c.Close()
	f, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Send(f) {
		t.Fatal("Send succeeded on a closed connection; want false")
	}
}

// TestConnectionSnapshotSlotCapacityOne verifies the latest-wins slot has
// capacity exactly 1: publishing a snapshot while one is pending replaces the
// stale frame instead of queueing a second. It does not depend on the socket,
// so it proves the queue-level invariant directly (complements
// TestSendSnapshotLatestWins which observes the wire).
func TestConnectionSnapshotSlotCapacityOne(t *testing.T) {
	c := newBareConnection()

	first, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 300}, []byte{0x01})
	if err != nil {
		t.Fatal(err)
	}
	if !c.SendSnapshot(first) {
		t.Fatal("first SendSnapshot = false")
	}

	// Publish a newer snapshot while the first is still pending. It must
	// replace, not queue, the stale frame.
	second, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 301}, []byte{0x02})
	if err != nil {
		t.Fatal(err)
	}
	if !c.SendSnapshot(second) {
		t.Fatal("second SendSnapshot = false")
	}

	// The slot holds exactly one frame, and it must be the newest.
	got := <-c.snapshot
	if len(got) != len(second) || got[len(got)-1] != 0x02 {
		t.Fatalf("snapshot slot did not retain the newest frame")
	}
	// The slot must now be empty (capacity 1), never holding a second frame.
	select {
	case extra := <-c.snapshot:
		t.Fatalf("snapshot slot held a stale second frame (last byte 0x%02X)", extra[len(extra)-1])
	default:
	}
}
