// Command gameserver 是游戏服务端入口。
//
// 启动顺序：加载配置与玩法数据 -> 创建业务编排层 -> 连接 Redis/MySQL（按配置启用）->
// 启动 TCP、监控、管理后台和 pprof。所有长期 goroutine 共享根 context；收到 Ctrl+C 或
// SIGTERM 后先停止接收连接，再关闭在线连接、排空持久化队列并关闭 HTTP 服务。
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/admin"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/bootstrap"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/config"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/persistence"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/session"
)

// errClosing 是 handler 返回的哨兵错误，表示优雅拒绝帧已排队，Reader 应退出；
// Writer 会先刷新 Disconnect，再关闭 socket。
var errClosing = errors.New("gameserver: closing connection after rejection")
var errSendRejected = errors.New("gameserver: reliable send queue rejected frame")

// idAllocator 用原子自增分配进程内唯一的 session/player ID。
// 登录可能来自多条 Connection Reader goroutine，因此不能使用普通整数自增。
type idAllocator struct {
	session atomic.Uint64
	player  atomic.Uint64
}

func (a *idAllocator) next() (sessionID, playerID uint64) {
	sessionID = a.session.Add(1)
	playerID = a.player.Add(1)
	return
}

func main() {
	envPath := flag.String("env", "", "path to .env file (default: configs/.env)")
	flag.Parse()

	cfg, err := config.Load(*envPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)
	gameplay, err := bootstrap.LoadGameplay(cfg, game.DefaultConfig())
	if err != nil {
		logger.Error("failed to load gameplay configuration", "err", err)
		os.Exit(1)
	}
	logger.Info("gameserver starting", "env", cfg.Env, "tcp", cfg.TCPAddr, "tick_hz", cfg.TickHz,
		"equipment_version", gameplay.Catalog().Version(), "reward_ticks", gameplay.RewardDurationTicks(), "stage_limit", gameplay.StageLimit())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	metricSet := metrics.New()
	// application 是“业务大脑”，负责把连接、匹配、Room、导演和持久化编排起来；
	// 真正的逐帧战斗状态仍由每个 Room 独占维护。
	app, err := newConfiguredGameApplication(ctx, logger, metricSet, gameplay)
	if err != nil {
		logger.Error("failed to initialize application", "err", err)
		os.Exit(1)
	}
	app.setEnvironment(cfg.Env)
	var resumeService *persistence.ResumeService
	if cfg.ResumeEnabled {
		resumeTTL := time.Duration(cfg.ResumeTTLSeconds) * time.Second
		resumeService, err = persistence.OpenResumeService(ctx, persistence.ResumeServiceOptions{
			Addr: cfg.RedisAddr, Password: cfg.RedisPass, DB: cfg.RedisDB,
			TokenTTL:         resumeTTL,
			OperationTimeout: time.Duration(cfg.RedisOperationMS) * time.Millisecond,
		})
		if err != nil {
			logger.Error("failed to initialize Resume storage", "err", err)
			os.Exit(1)
		}
		app.setResumeTokenStore(resumeService.Store(), resumeTTL)
		logger.Info("Resume storage ready", "ttl_seconds", cfg.ResumeTTLSeconds)
	}
	var resultService *persistence.ResultService
	if cfg.ResultsEnabled {
		resultService, err = persistence.OpenResultService(ctx, persistence.ResultServiceOptions{
			Store: persistence.ResultStoreOptions{DSN: cfg.MySQLDSN, OperationTimeout: time.Duration(cfg.ResultAttemptTimeoutMS) * time.Millisecond, ApplyMigrations: true},
			Writer: persistence.ResultWriterOptions{QueueCapacity: cfg.ResultQueueCapacity, MaxAttempts: cfg.ResultMaxAttempts,
				AttemptTimeout: time.Duration(cfg.ResultAttemptTimeoutMS) * time.Millisecond, RetryBackoff: time.Duration(cfg.ResultRetryBackoffMS) * time.Millisecond},
			DeadLetterPath: cfg.ResultDeadLetterPath,
		})
		if err != nil {
			_ = resumeService.Close()
			logger.Error("failed to initialize result persistence", "err", err)
			os.Exit(1)
		}
		app.setResultWriter(resultService.Writer())
		logger.Info("result persistence ready", "queue_capacity", cfg.ResultQueueCapacity)
		go observeResultMetrics(ctx, resultService.Writer(), metricSet)
	}
	srv := network.NewServer(app.handle, logger)
	srv.OnDisconnect(app.disconnected)
	ln, err := net.Listen("tcp", cfg.TCPAddr)
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ResultShutdownTimeoutSec)*time.Second)
		_ = resultService.Shutdown(shutdownContext)
		cancel()
		_ = resumeService.Close()
		logger.Error("failed to listen", "addr", cfg.TCPAddr, "err", err)
		os.Exit(1)
	}
	logger.Info("listening", "addr", cfg.TCPAddr)

	metricsServer := &http.Server{Addr: cfg.MetricsAddr, Handler: metricSet.Handler(), ReadHeaderTimeout: 2 * time.Second}
	adminAPI, err := admin.New(admin.ProviderFunc(func() admin.Snapshot {
		snapshot := app.adminSnapshot()
		networkStats := srv.Stats()
		snapshot.Network = admin.NetworkStatus{
			ActiveConnections: networkStats.ActiveConnections, ReliableQueueDepth: networkStats.ReliableQueueDepth,
			ReliableQueueCapacity: networkStats.ReliableQueueCapacity, SnapshotsPending: networkStats.SnapshotsPending,
			ReliableSendRejections: networkStats.ReliableSendRejections, SnapshotReplacements: networkStats.SnapshotReplacements,
		}
		return snapshot
	}), time.Second)
	if err != nil {
		_ = ln.Close()
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ResultShutdownTimeoutSec)*time.Second)
		_ = resultService.Shutdown(shutdownContext)
		cancel()
		_ = resumeService.Close()
		logger.Error("failed to initialize Admin API", "err", err)
		os.Exit(1)
	}
	adminHTTPServer := &http.Server{Addr: cfg.AdminAddr, Handler: adminAPI.Handler(), ReadHeaderTimeout: 2 * time.Second}
	pprofServer := &http.Server{Addr: cfg.PprofAddr, Handler: http.DefaultServeMux, ReadHeaderTimeout: 2 * time.Second}
	go serveHTTP(metricsServer, "metrics", logger, stop)
	go serveHTTP(adminHTTPServer, "admin", logger, stop)
	go serveHTTP(pprofServer, "pprof", logger, stop)
	go observeNetworkMetrics(ctx, srv, metricSet)

	go func() {
		if err := srv.Serve(ctx, ln); err != nil {
			logger.Error("serve stopped", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down", "active_conns", srv.ActiveConns())
	srv.CloseConnections()
	_ = adminAPI.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = metricsServer.Shutdown(shutdownCtx)
	_ = adminHTTPServer.Shutdown(shutdownCtx)
	_ = pprofServer.Shutdown(shutdownCtx)
	resultShutdownCtx, resultCancel := context.WithTimeout(context.Background(), time.Duration(cfg.ResultShutdownTimeoutSec)*time.Second)
	if err := resultService.Shutdown(resultShutdownCtx); err != nil {
		logger.Error("result persistence shutdown incomplete", "err", err, "stats", resultService.Writer().Stats())
	} else if resultService != nil {
		logger.Info("result persistence drained", "stats", resultService.Writer().Stats())
	}
	resultCancel()
	_ = resumeService.Close()
}

func observeResultMetrics(ctx context.Context, writer *persistence.ResultWriter, metricSet *metrics.Metrics) {
	var previous metrics.ResultWriterSnapshot
	publish := func() {
		stats := writer.Stats()
		current := metrics.ResultWriterSnapshot{QueueDepth: stats.QueueDepth, InFlight: stats.InFlight, Persisted: stats.Persisted,
			Idempotent: stats.Idempotent, Retries: stats.Retries, Failed: stats.Failed, Rejected: stats.Rejected,
			DeadLetterFailures: stats.DeadLetterFailures}
		_ = metricSet.SetResultWriterSnapshot(current, previous)
		previous = current
	}
	publish()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			publish()
			return
		case <-ticker.C:
			publish()
		}
	}
}

func observeNetworkMetrics(ctx context.Context, server *network.Server, metricSet *metrics.Metrics) {
	var previous network.Stats
	publish := func() {
		current := server.Stats()
		_ = metricSet.SetNetworkQueueDepth(current.ReliableQueueDepth)
		metricSet.ObserveQueueDelta(metrics.QueueDelta{
			NetworkReliableRejected: counterDelta(current.ReliableSendRejections, previous.ReliableSendRejections),
			NetworkSnapshotReplaced: counterDelta(current.SnapshotReplacements, previous.SnapshotReplacements),
		})
		previous = current
	}
	publish()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			publish()
			return
		case <-ticker.C:
			publish()
		}
	}
}

func serveHTTP(server *http.Server, name string, logger *slog.Logger, stop context.CancelFunc) {
	logger.Info(name+" listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(name+" server stopped", "err", err)
		stop()
	}
}

// routeMessage 处理所有连接都通用的基础消息（Ping/Login）：先按 MessageType 解码，
// 再通过 Session 状态机校验，最后生成回复。匹配、输入、奖励等登录后业务由
// gameApplication.handle 继续路由到对应模块。
func routeMessage(c *network.Connection, h network.Header, payload []byte, ids *idAllocator, logger *slog.Logger) error {
	mt := protocol.MessageType(h.MessageType)

	// Session 作为连接上下文保存；网络层只存 interface{}，并不知道业务状态含义，
	// 从而保持“拆包传输”和“会话规则”解耦。
	var sess *session.Session
	if v := c.Context(); v != nil {
		sess = v.(*session.Session)
	} else {
		sess = session.New()
		c.SetContext(sess)
	}

	// 所有消息先过状态机。例如未登录不能发玩家输入，奖励阶段不能再次请求匹配。
	// 把合法性集中在一张状态迁移表里，比在每个 handler 中零散判断更不容易漏。
	if ok, reason := sess.Accept(mt); !ok {
		logger.Debug("message rejected by state machine", "state", sess.State(), "mt", mt, "reason", reason)
		return sendDisconnect(c, h, reason, "invalid message for session state")
	}

	switch mt {
	case protocol.MessageType_MSG_PING:
		return handlePing(c, h, payload)
	case protocol.MessageType_MSG_LOGIN_REQUEST:
		return handleLogin(c, h, payload, ids, sess)
	case protocol.MessageType_MSG_PLAYER_INPUT:
		// 该分支仅保留给基础路由单测；生产路径中的玩家输入由
		// gameApplication.handle 解析并送入对应 Room。
		return nil
	default:
		// 生产业务消息由 gameApplication.handle 处理；这里不重复实现。
		logger.Debug("message accepted but unhandled", "mt", mt)
		return nil
	}
}

func handlePing(c *network.Connection, h network.Header, payload []byte) error {
	var ping protocol.Ping
	if err := proto.Unmarshal(payload, &ping); err != nil {
		return err
	}
	pong := &protocol.Pong{
		ClientTimeMs: ping.ClientTimeMs,
		ServerTimeMs: 0, // 当前不提供时钟源，回显 nonce 即可
		Nonce:        ping.Nonce,
	}
	return sendMessage(c, h, protocol.MessageType_MSG_PONG, pong)
}

const gameplayProtocolVersion uint32 = 2

func handleLogin(c *network.Connection, h network.Header, payload []byte, ids *idAllocator, sess *session.Session) error {
	var req protocol.LoginRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		return err
	}

	// 帧编码仍为 v1，玩法 v2 表示动态权威地图。拒绝旧版或未声明版本的客户端，避免
	// 碰撞布局不一致导致预测抖动和视觉穿墙。
	if req.ProtocolVersion != gameplayProtocolVersion {
		resp := &protocol.LoginResponse{
			ProtocolVersion: gameplayProtocolVersion,
			Reason:          protocol.ReasonCode_REASON_INVALID_VERSION,
			Message:         "unsupported protocol version",
		}
		return sendMessage(c, h, protocol.MessageType_MSG_LOGIN_RESPONSE, resp)
	}

	sessionID, playerID := ids.next()
	sess.AssignIdentity(sessionID, playerID)
	sess.Transition(session.StateLobby)

	resp := &protocol.LoginResponse{
		ProtocolVersion: gameplayProtocolVersion,
		Reason:          protocol.ReasonCode_REASON_OK,
		Message:         "ok",
		SessionId:       sessionID,
		PlayerId:        playerID,
		ResumeToken:     nil, // 当前基础登录路径把重连视为新会话
	}
	return sendMessage(c, h, protocol.MessageType_MSG_LOGIN_RESPONSE, resp)
}

// sendDisconnect 排入带 ReasonCode 的 Disconnect，并返回哨兵错误让 Reader 退出；
// Writer 排空终止帧后再关闭 socket，实现优雅断开。
func sendDisconnect(c *network.Connection, h network.Header, reason protocol.ReasonCode, msg string) error {
	d := &protocol.Disconnect{Reason: reason, Message: msg}
	if err := sendMessage(c, h, protocol.MessageType_MSG_DISCONNECT, d); err != nil {
		return err
	}
	// 终止帧入队后让 Reader 退出；CloseAfterFlush 关闭 out，使 Writer 排空后再断开。
	c.CloseAfterFlush()
	return errClosing
}

// sendMessage 序列化消息并把完整帧加入连接可靠队列。
func sendMessage(c *network.Connection, h network.Header, mt protocol.MessageType, m proto.Message) error {
	body, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	frame, err := network.EncodeFrame(network.Header{
		Magic:       network.Magic,
		Version:     network.VersionV1,
		MessageType: uint16(mt),
		Sequence:    h.Sequence, // 回显帧序号便于关联请求与响应
	}, body)
	if err != nil {
		return err
	}
	if !c.Send(frame) {
		return errSendRejected
	}
	return nil
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "info":
		l = slog.LevelInfo
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
