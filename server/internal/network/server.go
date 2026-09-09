package network

import (
	"bufio"
	"context"
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

// Server accepts TCP connections and dispatches each to its own Connection.
type Server struct {
	handler Handler
	logger  *slog.Logger

	mu    sync.Mutex
	conns map[*Connection]struct{}
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
	}
	c.onClose = func() { s.untrack(c) }
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
		return true
	default:
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
			return
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
			if !c.writeFrame(w, frame) {
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
						if !c.writeFrame(w, f) {
							return
						}
					default:
						return
					}
				}
			}
			if !c.writeFrame(w, frame) {
				return
			}
		case frame := <-c.snapshot:
			if !c.writeFrame(w, frame) {
				return
			}
		}
	}
}

// writeFrame writes a single frame and flushes it so a partial frame is never
// observed by the peer. It reports false on any error, signalling the writer
// to unwind.
func (c *Connection) writeFrame(w *bufio.Writer, frame []byte) bool {
	if _, err := w.Write(frame); err != nil {
		c.logger.Debug("connection write failed", "err", err)
		return false
	}
	if err := w.Flush(); err != nil {
		c.logger.Debug("connection flush failed", "err", err)
		return false
	}
	return true
}
