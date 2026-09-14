// Command gameserver is the entry point for the Odyssey game server.
//
// Phase 1 scope: accept TCP connections, speak the 16-byte frame protocol,
// validate each message against the session state machine, answer Ping with
// Pong, and perform development-mode login (test nickname -> server-issued
// session/player IDs). Room/combat/matchmaking are not wired yet (Role B/D).
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
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/session"
)

// errClosing is a sentinel returned by the handler to signal the reader to
// unwind after a graceful rejection (the Disconnect frame has been queued and
// will be flushed by the writer before the socket closes).
var errClosing = errors.New("gameserver: closing connection after rejection")
var errSendRejected = errors.New("gameserver: reliable send queue rejected frame")

// serverIDAllocator issues sequential session/player IDs for development
// login. Atomic counters are enough for Phase 1 (no persistence).
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
	app, err := newConfiguredGameApplication(ctx, logger, metricSet, gameplay)
	if err != nil {
		logger.Error("failed to initialize application", "err", err)
		os.Exit(1)
	}
	app.setEnvironment(cfg.Env)
	srv := network.NewServer(app.handle, logger)
	srv.OnDisconnect(app.disconnected)
	ln, err := net.Listen("tcp", cfg.TCPAddr)
	if err != nil {
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

// routeMessage decodes the payload by MessageType, validates it against the
// connection's session state machine, and produces the appropriate reply.
func routeMessage(c *network.Connection, h network.Header, payload []byte, ids *idAllocator, logger *slog.Logger) error {
	mt := protocol.MessageType(h.MessageType)

	// Session state is stored per-connection. In Phase 1 the connection IS the
	// session context; a real session registry (resume/redis) lands with D.
	var sess *session.Session
	if v := c.Context(); v != nil {
		sess = v.(*session.Session)
	} else {
		sess = session.New()
		c.SetContext(sess)
	}

	// State machine validation (message-routing.md). Ping/Pong and login are
	// the only accepted messages before login; everything else is rejected.
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
		// Accepted by the state machine only when IN_ROOM. Room is not wired
		// in Phase 1; acknowledge nothing and drop for now (B will consume).
		return nil
	default:
		// Legally accepted but not yet implemented (match, reward, etc.).
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
		ServerTimeMs: 0, // Phase 1: no clock source wired; echo nonce is enough
		Nonce:        ping.Nonce,
	}
	return sendMessage(c, h, protocol.MessageType_MSG_PONG, pong)
}

func handleLogin(c *network.Connection, h network.Header, payload []byte, ids *idAllocator, sess *session.Session) error {
	var req protocol.LoginRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		return err
	}

	// Protocol version check: must match the header Version (both = 1).
	if req.ProtocolVersion != 0 && req.ProtocolVersion != uint32(network.VersionV1) {
		resp := &protocol.LoginResponse{
			ProtocolVersion: uint32(network.VersionV1),
			Reason:          protocol.ReasonCode_REASON_INVALID_VERSION,
			Message:         "unsupported protocol version",
		}
		return sendMessage(c, h, protocol.MessageType_MSG_LOGIN_RESPONSE, resp)
	}

	sessionID, playerID := ids.next()
	sess.AssignIdentity(sessionID, playerID)
	sess.Transition(session.StateLobby)

	resp := &protocol.LoginResponse{
		ProtocolVersion: uint32(network.VersionV1),
		Reason:          protocol.ReasonCode_REASON_OK,
		Message:         "ok",
		SessionId:       sessionID,
		PlayerId:        playerID,
		ResumeToken:     nil, // Phase 1: reconnect treated as a new session
	}
	return sendMessage(c, h, protocol.MessageType_MSG_LOGIN_RESPONSE, resp)
}

// sendDisconnect writes a Disconnect message (ReasonCode), flushes it, and
// returns a sentinel error so the reader unwinds and the connection tears down
// gracefully (the writer drains the queued Disconnect frame before the socket
// closes).
func sendDisconnect(c *network.Connection, h network.Header, reason protocol.ReasonCode, msg string) error {
	d := &protocol.Disconnect{Reason: reason, Message: msg}
	if err := sendMessage(c, h, protocol.MessageType_MSG_DISCONNECT, d); err != nil {
		return err
	}
	// Queue the terminal frame, then let the reader unwind. CloseAfterFlush
	// closes the out channel so the writer drains the Disconnect frame and
	// then the connection closes.
	c.CloseAfterFlush()
	return errClosing
}

// sendMessage marshals msg and queues a complete frame on the connection.
func sendMessage(c *network.Connection, h network.Header, mt protocol.MessageType, m proto.Message) error {
	body, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	frame, err := network.EncodeFrame(network.Header{
		Magic:       network.Magic,
		Version:     network.VersionV1,
		MessageType: uint16(mt),
		Sequence:    h.Sequence, // echo frame sequence for correlation
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
