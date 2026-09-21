package network

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Handler 在一条连接的 Reader goroutine 中执行，接收已经拆包完成的消息。
// 它只应该做协议校验、会话校验和命令入队，不能执行耗时 I/O，更不能直接修改 Room
// 的 World；否则一个慢请求会堵住该玩家后续所有消息。返回 error 表示应关闭连接。
type Handler func(c *Connection, h Header, payload []byte) error

// DisconnectHandler is invoked once after a connection has been untracked.
// It must return quickly; application cleanup that may block should run in a
// separate goroutine.
type DisconnectHandler func(c *Connection)

// Server 负责监听 TCP，并为每个客户端创建独立 Connection。
// mu 只保护在线连接集合与断线回调；全局计数器用 atomic 累加，采集指标时无需阻塞写路径。
type Server struct {
	handler Handler
	logger  *slog.Logger

	mu           sync.Mutex
	conns        map[*Connection]struct{}
	onDisconnect DisconnectHandler
	counters     serverCounters
}

type serverCounters struct {
	reliableRejected atomic.Uint64
	snapshotReplaced atomic.Uint64
}

// Stats is a point-in-time, process-wide network queue sample. Queue depths
// include live connections only; counters remain monotonic after connections
// close so a metrics sampler cannot lose their final values.
type Stats struct {
	ActiveConnections      int
	ReliableQueueDepth     int
	ReliableQueueCapacity  int
	SnapshotsPending       int
	ReliableSendRejections uint64
	SnapshotReplacements   uint64
}

// OnDisconnect installs the application lifecycle callback. Configure it
// before Serve starts.
func (s *Server) OnDisconnect(handler DisconnectHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDisconnect = handler
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

// Serve 持续 Accept，直到 context 取消或监听器出错。每个连接都会启动自己的 run，
// 因此某个客户端收发变慢不会阻塞其他客户端。context 取消时主动关闭 listener，
// 用于唤醒正在阻塞的 Accept，从而完成优雅停机。
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
		counters: &s.counters,
	}
	c.onClose = func() {
		s.untrack(c)
		s.mu.Lock()
		handler := s.onDisconnect
		s.mu.Unlock()
		if handler != nil {
			handler(c)
		}
	}
	return c
}

// Stats returns queue depths and monotonic rejection/replacement counters.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := Stats{
		ActiveConnections:      len(s.conns),
		ReliableSendRejections: s.counters.reliableRejected.Load(),
		SnapshotReplacements:   s.counters.snapshotReplaced.Load(),
	}
	for connection := range s.conns {
		stats.ReliableQueueDepth += len(connection.out)
		stats.ReliableQueueCapacity += cap(connection.out)
		stats.SnapshotsPending += len(connection.snapshot)
	}
	return stats
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

// Connection 表示一个客户端连接，内部拆成两个 goroutine：
//   - Reader：从 socket 读帧并调用 Handler；
//   - Writer：从发送队列取帧，且它是唯一允许写 socket 的 goroutine。
//
// “单写者”可避免两个 goroutine 同时写导致协议帧交叉。发送再按语义拆成两条队列：
//   - out：可靠、有界 FIFO，用于登录结果、伤害、关卡事件等，必须保序；
//   - snapshot：容量 1 的 latest-wins 槽位，用于 10Hz 世界快照，新帧替换未发送旧帧。
//
// 因此慢客户端只会少看几张过期快照，不会反向卡住房间 Tick；若可靠队列也塞满，
// 则说明客户端已无法跟上关键事件，应触发背压策略并断开，而不是悄悄丢事件。
type Connection struct {
	conn    net.Conn
	handler Handler
	logger  *slog.Logger

	// out 只由 Writer 消费。sendMu 不是用来保护 socket，而是保证“入队”和“关闭队列”
	// 互斥，避免向已关闭 channel 发送而 panic。
	out    chan []byte
	sendMu sync.Mutex
	// snapshot 是容量 1 的最新快照槽位；慢消费者会丢旧帧，不会阻塞发布者。
	snapshot chan []byte

	closeOnce sync.Once // protects socket close + onClose
	queueOnce sync.Once // protects close(c.out)
	deadOnce  sync.Once // protects close(c.closed)
	closed    chan struct{}
	onClose   func()
	counters  *serverCounters

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

// Send 把可靠帧无阻塞地放入 FIFO。队列已满或连接已关闭时返回 false，让上层执行
// “可靠消息积压即断开”的背压策略。传入切片在返回后不可再修改。
func (c *Connection) Send(frame []byte) bool {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	select {
	case <-c.closed:
		c.recordReliableRejection()
		return false
	default:
	}
	select {
	case c.out <- frame:
		return true
	default:
		c.recordReliableRejection()
		return false
	}
}

// SendSnapshot 发布最新快照。如果槽位中已有未发送快照，就先淘汰旧帧再重试；
// 因为容量固定为 1，这个循环不会无界增长。它只有在连接已关闭时才返回 false。
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
				if c.counters != nil {
					c.counters.snapshotReplaced.Add(1)
				}
			default:
			}
		}
	}
}

func (c *Connection) recordReliableRejection() {
	if c.counters != nil {
		c.counters.reliableRejected.Add(1)
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
	// Reader/Writer 并行运行，但关闭顺序是确定的：
	//   - 正常 EOF：Reader 结束 -> 关闭 out -> Writer 排空后退出；
	//   - CloseAfterFlush：Writer 排空后关闭 socket -> Reader 被唤醒；
	//   - 强制 Close：socket 立即关闭，两侧都从 I/O 中返回。
	// sync.Once 保证无论哪个路径先到，socket、channel 和 onClose 都只关闭一次。
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
		// 先非阻塞检查最新快照，减少画面延迟；若没有，再同时等待可靠帧或快照。
		// socket 写入始终只发生在本 goroutine，因此不需要给每次 Write 额外加锁。
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
