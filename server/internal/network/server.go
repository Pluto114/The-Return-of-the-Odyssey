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

// DisconnectHandler 在连接移出在线集合后调用一次。它必须快速返回；可能阻塞的业务清理
// 应另起 goroutine 执行。
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

// Stats 是进程级网络队列的瞬时样本。队列深度只统计在线连接；累计计数在连接关闭后仍
// 保持单调，避免指标采样器漏掉连接结束前的最终数据。
type Stats struct {
	ActiveConnections      int
	ReliableQueueDepth     int
	ReliableQueueCapacity  int
	SnapshotsPending       int
	ReliableSendRejections uint64
	SnapshotReplacements   uint64
}

// OnDisconnect 设置业务层断线回调，应在 Serve 启动前配置。
func (s *Server) OnDisconnect(handler DisconnectHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDisconnect = handler
}

// NewServer 创建服务端，并把每条连接解码后的消息交给 handler。
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
				return nil // 预期的停机路径
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

// Stats 返回队列深度以及单调递增的拒绝/替换计数。
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

// ActiveConns 返回当前连接数，供指标和健康检查使用。
func (s *Server) ActiveConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// CloseConnections 终止全部在线连接，用于停机时确保监听器关闭后不残留连接 goroutine。
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

	closeOnce sync.Once // 保证 socket 与 onClose 只处理一次
	queueOnce sync.Once // 保证 c.out 只关闭一次
	deadOnce  sync.Once // 保证 c.closed 只关闭一次
	closed    chan struct{}
	onClose   func()
	counters  *serverCounters

	// ctx 是调用方拥有的连接级不透明上下文（通常为 Session）。网络层只保存不解释，
	// 从而把连接传输与会话业务解耦。
	ctxMu sync.Mutex
	ctx   interface{}
}

// SetContext 保存连接级不透明值，通常是 Session；网络层不解释其内容。
func (c *Connection) SetContext(v interface{}) {
	c.ctxMu.Lock()
	defer c.ctxMu.Unlock()
	c.ctx = v
}

// Context 返回连接级上下文，未设置时为 nil。
func (c *Connection) Context() interface{} {
	c.ctxMu.Lock()
	defer c.ctxMu.Unlock()
	return c.ctx
}

// IsClosed 表示连接清理是否已经开始。
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
			// 槽位已满：淘汰旧快照后重试。容量固定为 1，因此不会无界自旋。
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

// Close 立即强制关闭连接，可由任意 goroutine 幂等调用。socket 关闭会唤醒 Reader，
// 随后结束发送队列并释放资源。
func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		c.deadOnce.Do(func() { close(c.closed) })
		c.conn.Close()
		if c.onClose != nil {
			c.onClose()
		}
	})
}

// CloseAfterFlush 先关闭发送队列，让 Writer 排空已排队帧（例如最终 Disconnect）后再按
// run 的正常路径断开。用于必须让对端先收到终止帧的优雅拒绝，操作安全且幂等。
func (c *Connection) CloseAfterFlush() {
	c.closeQueue()
}

func (c *Connection) closeQueue() {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.deadOnce.Do(func() { close(c.closed) })
	c.queueOnce.Do(func() { close(c.out) })
}

// SetReadDeadline 用于登录超时：连接必须在 LOGIN_TIMEOUT_MS 内完成 LoginRequest。
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
	c.Close() // 幂等：只关闭一次 socket 并只移出在线集合一次
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
				// 可靠队列已关闭：排空待发快照后退出。
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

// writeFrame 写入并刷新一个完整帧，避免对端观察到半帧；发生任何错误均返回 false。
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
