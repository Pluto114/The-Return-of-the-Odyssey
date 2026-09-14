package network

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Handler is the callback the network layer invokes with each fully-decoded
// inbound message. It runs on the connection's Reader goroutine.
//
// The Handler receives the owning *Connection, the decoded header and the
// payload. Implementations (the session router in cmd/gameserver) must NOT
// block the Reader goroutine for long; they should only validate and enqueue
// commands, never perform blocking I/O or write to the Room world.
//
// An error return signals that the connection should be closed.
type Handler func(c *Connection, h Header, payload []byte) error

// DisconnectHandler is invoked once after a connection has been untracked.
// It must return quickly; application cleanup that may block should run in a
// separate goroutine.
type DisconnectHandler func(c *Connection)

// Observer receives transport-level events so the application can surface
// network throughput, queue depth, and rejection metrics (D9 observability)
// without the network package depending on the metrics package. Every method
// must return quickly and never block the Reader/Writer goroutines.
type Observer interface {
	// OnConnectionAccepted is called once when a TCP connection is accepted.
	OnConnectionAccepted()
	// OnConnectionClosed is called once after a connection fully tears down.
	OnConnectionClosed()
	// OnBytesReceived is called with each inbound read size.
	OnBytesReceived(n int)
	// OnBytesSent is called with each outbound write size.
	OnBytesSent(n int)
	// OnFrameReceived is called after a frame is successfully decoded.
	OnFrameReceived()
	// OnFrameSent is called after a frame is successfully written.
	OnFrameSent()
	// OnSnapshotSent is called after a latest-wins snapshot frame is written,
	// with its full byte size. This separates the 10Hz snapshot bandwidth from
	// reliable traffic (D9).
	OnSnapshotSent(bytes int)
	// OnInvalidFrame is called when ReadFrame rejects a frame. reason is a
	// bounded string (see metrics.FrameResult); it must never be client text.
	OnInvalidFrame(reason string)
	// OnReliableRejection is called when a reliable Send is rejected because
	// the queue is full (backpressure).
	OnReliableRejection()
	// OnSnapshotDrop is called when a stale snapshot is evicted (latest-wins).
	OnSnapshotDrop()
	// OnReliableDepth is called whenever the reliable queue length changes.
	OnReliableDepth(depth int)
}

// Server accepts TCP connections and dispatches each to its own Connection.
type Server struct {
	handler Handler
	logger  *slog.Logger

	mu           sync.Mutex
	conns        map[*Connection]struct{}
	onDisconnect DisconnectHandler
	observer     Observer
}

// OnDisconnect installs the application lifecycle callback. Configure it
// before Serve starts.
func (s *Server) OnDisconnect(handler DisconnectHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDisconnect = handler
}

// SetObserver installs the transport observer. Configure it before Serve
// starts; it is read by each new connection at accept time.
func (s *Server) SetObserver(o Observer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observer = o
}

// NewServer creates a server that will route every connection's decoded
// messages to handler.
func NewServer(handler Handler, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		handler: handler,
		logger:  logger,
		conns:   make(map[*Connection]struct{}),
	}
}

// Serve listens on ln and accepts until ctx is cancelled or ln errors.
// It blocks; run it in its own goroutine.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil // expected shutdown
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		c := s.newConnection(conn)
		s.track(c)
		if s.observer != nil {
			s.observer.OnConnectionAccepted()
		}
		go c.run()
	}
}

func (s *Server) newConnection(conn net.Conn) *Connection {
	c := &Connection{
		conn:     conn,
		handler:  s.handler,
		logger:   s.logger,
		out:      make(chan []byte, 256),
		snapshot: make(chan []byte, 1),
		closed:   make(chan struct{}),
		observer: s.observer,
	}
	c.onClose = func() {
		s.untrack(c)
		if s.observer != nil {
			s.observer.OnConnectionClosed()
		}
		s.mu.Lock()
		handler := s.onDisconnect
		s.mu.Unlock()
		if handler != nil {
			handler(c)
		}
	}
	return c
}

func (s *Server) track(c *Connection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[c] = struct{}{}
}

func (s *Server) untrack(c *Connection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
}

// ActiveConns returns the current connection count (for metrics/health).
func (s *Server) ActiveConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// CloseConnections terminates every active connection. It is used during
// server shutdown so Serve cancellation cannot leave connection goroutines
// behind after the listener is closed.
func (s *Server) CloseConnections() {
	s.mu.Lock()
	connections := make([]*Connection, 0, len(s.conns))
	for c := range s.conns {
		connections = append(connections, c)
	}
	s.mu.Unlock()
	for _, c := range connections {
		c.Close()
	}
}

// Connection represents a single client socket. It owns two goroutines:
//   - Reader: reads frames and invokes Handler (the run loop's read side).
//   - Writer: drains the outbound channel and writes frames.
//
// Only the Writer goroutine performs socket writes, so the Reader (and thus
// the Room Tick via Handler) can never be blocked by a slow client.
//
// Outbound traffic is split into two queues with different loss policies:
//   - out (reliable): bounded FIFO, every frame must reach the peer in order.
//   - snapshot (latest-wins): capacity 1, a new snapshot replaces a pending
//     stale one. Used for 10Hz world snapshots where only the newest state
//     matters (docs/protocol/snapshots.md).
type Connection struct {
	conn    net.Conn
	handler Handler
	logger  *slog.Logger

	// out is the bounded outbound queue drained by the Writer goroutine.
	out    chan []byte
	sendMu sync.Mutex // serializes reliable queue admission with queue closure
	// snapshot is the latest-wins slot (capacity 1). A slow consumer drops
	// stale snapshots, never blocks the publisher (the room tick).
	snapshot chan []byte

	closeOnce sync.Once // protects socket close + onClose
	queueOnce sync.Once // protects close(c.out)
	deadOnce  sync.Once // protects close(c.closed)
	closed    chan struct{}
	onClose   func()

	observer Observer

	// ctx is opaque per-connection context owned by the caller (the session
	// router). The network layer stores it without interpreting it, keeping
	// network and session concerns separate.
	ctxMu sync.Mutex
	ctx   interface{}
}

// SetContext stores an opaque per-connection value (typically the session
// state). The network layer does not interpret it.
func (c *Connection) SetContext(v interface{}) {
	c.ctxMu.Lock()
	defer c.ctxMu.Unlock()
	c.ctx = v
}

// Context returns the opaque per-connection value, or nil.
func (c *Connection) Context() interface{} {
	c.ctxMu.Lock()
	defer c.ctxMu.Unlock()
	return c.ctx
}

// IsClosed reports whether teardown has started.
func (c *Connection) IsClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// Send queues an already-encoded frame (header + body) for writing. It is
// non-blocking up to the queue capacity; when full it returns false so the
// caller can apply backpressure policy (drop for snapshots, close for
// reliable overflow).
//
// The byte slice must not be mutated after Send returns. Send is safe to call
// after the connection has closed (it returns false rather than panicking).
func (c *Connection) Send(frame []byte) bool {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	select {
	case <-c.closed:
		return false
	default:
	}
	select {
	case c.out <- frame:
		if c.observer != nil {
			c.observer.OnReliableDepth(len(c.out))
		}
		return true
	default:
		if c.observer != nil {
			c.observer.OnReliableRejection()
		}
		return false
	}
}

// SendSnapshot queues a latest-wins snapshot frame. If a previous snapshot is
// still pending, it is replaced by the new one (stale world state is never
// worth sending once a newer snapshot exists). It never blocks the caller
// beyond the replace, so a slow consumer cannot stall the room tick.
//
// The byte slice must not be mutated after SendSnapshot returns. It returns
// false only when the connection has already closed.
func (c *Connection) SendSnapshot(frame []byte) bool {
	select {
	case <-c.closed:
		return false
	default:
	}
	for {
		select {
		case c.snapshot <- frame:
			return true
		default:
			// Slot full: evict the stale snapshot, then retry the send. The
			// eviction loop is bounded by capacity 1, so this never spins.
			select {
			case <-c.snapshot:
				if c.observer != nil {
					c.observer.OnSnapshotDrop()
				}
			default:
			}
		}
	}
}

// Close forces the connection down immediately. It is safe to call from any
// goroutine and is idempotent. Closing the socket causes the reader to observe
// an error and unwind, which in turn closes the out channel and releases
// resources.
func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		c.deadOnce.Do(func() { close(c.closed) })
		c.conn.Close()
		if c.onClose != nil {
			c.onClose()
		}
	})
}

// CloseAfterFlush closes the outbound queue so the writer drains any queued
// frames (e.g. a final Disconnect) and then the connection tears down via
// run()'s normal path. It is used for graceful rejection where the peer must
// receive the terminal frame before the socket closes. Safe and idempotent.
func (c *Connection) CloseAfterFlush() {
	c.closeQueue()
}

func (c *Connection) closeQueue() {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.deadOnce.Do(func() { close(c.closed) })
	c.queueOnce.Do(func() { close(c.out) })
}

// SetReadDeadline is exposed for the login-timeout path (a connection must
// complete LoginRequest within LOGIN_TIMEOUT_MS or be closed).
func (c *Connection) SetReadDeadline(d time.Time) error { return c.conn.SetReadDeadline(d) }

func (c *Connection) run() {
	// Reader and writer run concurrently. The connection ends when EITHER
	// side finishes; whoever finishes last releases the socket.
	//
	//   - Normal EOF: reader ends -> close(out) -> writer drains + exits.
	//   - CloseAfterFlush: out closed -> writer drains + exits -> close socket
	//     -> reader unblocks and ends.
	//   - Close (hard): socket closed -> reader + writer both error out.
	readerDone := make(chan struct{})
	go c.readLoop(readerDone)
	writerDone := make(chan struct{})
	go c.writeLoop(writerDone)

	<-readerDone
	c.closeQueue()
	<-writerDone
	c.Close() // idempotent: closes socket + untracks exactly once
}

func (c *Connection) readLoop(done chan<- struct{}) {
	defer close(done)
	r := bufio.NewReader(c.conn)
	for {
		h, payload, err := ReadFrame(r)
		if err != nil {
			c.logger.Debug("connection read ended", "err", err)
			c.observeReadError(err)
			return
		}
		if c.observer != nil {
			c.observer.OnFrameReceived()
			c.observer.OnBytesReceived(HeaderLen + len(payload))
		}
		if err := c.handler(c, h, payload); err != nil {
			c.logger.Debug("handler rejected message, closing", "err", err)
			return
		}
	}
}

func (c *Connection) writeLoop(done chan<- struct{}) {
	defer close(done)
	w := bufio.NewWriter(c.conn)
	for {
		// Prefer the latest snapshot (non-blocking) so stale world state never
		// lingers behind the reliable queue.
		select {
		case frame := <-c.snapshot:
			if !c.writeSnapshotFrame(w, frame) {
				return
			}
			continue
		default:
		}

		select {
		case frame, ok := <-c.out:
			if !ok {
				// Reliable queue closed: drain any pending snapshot, then exit.
				for {
					select {
					case f := <-c.snapshot:
						if !c.writeSnapshotFrame(w, f) {
							return
						}
					default:
						return
					}
				}
			}
			if c.observer != nil {
				c.observer.OnReliableDepth(len(c.out))
			}
			if !c.writeFrame(w, frame) {
				return
			}
		case frame := <-c.snapshot:
			if !c.writeSnapshotFrame(w, frame) {
				return
			}
		}
	}
}

// writeFrame writes a single reliable frame and flushes it so a partial frame
// is never observed by the peer. It reports false on any error, signalling the
// writer to unwind.
func (c *Connection) writeFrame(w *bufio.Writer, frame []byte) bool {
	if _, err := w.Write(frame); err != nil {
		c.logger.Debug("connection write failed", "err", err)
		return false
	}
	if err := w.Flush(); err != nil {
		c.logger.Debug("connection flush failed", "err", err)
		return false
	}
	if c.observer != nil {
		c.observer.OnFrameSent()
		c.observer.OnBytesSent(len(frame))
	}
	return true
}

// writeSnapshotFrame writes a latest-wins snapshot frame and reports it to the
// observer as snapshot bandwidth (separate from reliable traffic) in addition
// to the generic sent counters.
func (c *Connection) writeSnapshotFrame(w *bufio.Writer, frame []byte) bool {
	if !c.writeFrame(w, frame) {
		return false
	}
	if c.observer != nil {
		c.observer.OnSnapshotSent(len(frame))
	}
	return true
}

// observeReadError maps a ReadFrame error to a bounded reason for the observer.
// It deliberately swallows the raw error text so no client-controlled string
// ever becomes a metric label value.
func (c *Connection) observeReadError(err error) {
	if c.observer == nil || err == nil {
		return
	}
	switch {
	case errors.Is(err, ErrInvalidMagic):
		c.observer.OnInvalidFrame("invalid_magic")
	case errors.Is(err, ErrInvalidVersion):
		c.observer.OnInvalidFrame("invalid_version")
	case errors.Is(err, ErrFrameTooLarge):
		c.observer.OnInvalidFrame("too_large")
	case errors.Is(err, ErrMessageTypeZero):
		c.observer.OnInvalidFrame("message_type_zero")
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		// Clean close or truncated final frame: not a malformed frame, so no
		// invalid-frame count; a clean EOF is normal teardown.
	default:
		// Transport error or partial header read (half-packet disconnect).
		c.observer.OnInvalidFrame("short_read")
	}
}
