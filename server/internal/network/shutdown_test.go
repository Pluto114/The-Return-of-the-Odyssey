package network

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"runtime"
	"testing"
	"time"
)

// TestServerShutdownReleasesGoroutines verifies the D9 completion criterion
// "停服后连接和 goroutine 回收": after Serve is cancelled and every connection
// is closed, ActiveConns returns to 0 and the connection goroutine count
// returns to (approximately) its pre-server baseline. It is deliberately
// tolerant of scheduler noise by waiting for a quiescent sample.
func TestServerShutdownReleasesGoroutines(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	srv := NewServer(echoHandler, logger)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	// Baseline goroutine count before the server and any client connect.
	baseline := runtime.NumGoroutine()

	go srv.Serve(ctx, ln)

	// Open several live connections.
	const n = 8
	conns := make([]net.Conn, 0, n)
	for i := 0; i < n; i++ {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}

	// Wait until all are tracked and their reader/writer goroutines are up.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && srv.ActiveConns() < n {
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.ActiveConns(); got != n {
		t.Fatalf("ActiveConns = %d, want %d", got, n)
	}

	// Shut down: cancel Serve (closes the listener) then force-close every
	// connection so their goroutines unwind.
	cancel()
	srv.CloseConnections()
	for _, c := range conns {
		c.Close()
	}

	// Both the connection count and the goroutine count must recover.
	connDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(connDeadline) {
		if srv.ActiveConns() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.ActiveConns(); got != 0 {
		t.Fatalf("ActiveConns = %d after shutdown, want 0", got)
	}

	// Goroutines: allow time for reader/writer goroutines to observe the closed
	// socket and exit, then require the count to settle back near baseline.
	gorDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(gorDeadline) {
		if runtime.NumGoroutine() <= baseline+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines = %d after shutdown, baseline = %d; connection goroutines leaked",
		runtime.NumGoroutine(), baseline)
}
