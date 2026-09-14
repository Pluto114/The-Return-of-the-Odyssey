// Command gameserver is the entry point for the Odyssey game server.
//
// It accepts TCP connections, speaks the 16-byte frame protocol, validates
// each message against the session state machine, and drives the full server
// lifecycle: development-mode login, two-player FIFO matchmaking, authoritative
// Room simulation (30Hz) with per-player snapshots (10Hz) and reliable combat
// events, reward selection, the AI-Director multi-stage loop, and disconnect
// recovery via single-use resume tokens.
//
// Metrics are exposed at cfg.MetricsAddr (Prometheus text format) and pprof at
// cfg.PprofAddr; both shut down cleanly on SIGINT/SIGTERM, closing every live
// connection so no goroutine leaks.
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
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/config"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
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
	logger.Info("gameserver starting", "env", cfg.Env, "tcp", cfg.TCPAddr, "tick_hz", cfg.TickHz)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	metricSet := metrics.New()
	app, err := newGameApplication(ctx, logger, metricSet, time.Duration(cfg.ResumeGraceSec)*time.Second)
	if err != nil {
		logger.Error("failed to initialize application", "err", err)
		os.Exit(1)
	}
	srv := network.NewServer(app.handle, logger)
	srv.OnDisconnect(app.disconnected)
	srv.SetObserver(newMetricsObserver(metricSet))
	ln, err := net.Listen("tcp", cfg.TCPAddr)
	if err != nil {
		logger.Error("failed to listen", "addr", cfg.TCPAddr, "err", err)
		os.Exit(1)
	}
	logger.Info("listening", "addr", cfg.TCPAddr)

	metricsServer := &http.Server{Addr: cfg.MetricsAddr, Handler: metricSet.Handler(), ReadHeaderTimeout: 2 * time.Second}
	pprofServer := &http.Server{Addr: cfg.PprofAddr, Handler: http.DefaultServeMux, ReadHeaderTimeout: 2 * time.Second}
	go serveHTTP(metricsServer, "metrics", logger, stop)
	go serveHTTP(pprofServer, "pprof", logger, stop)

	go func() {
		if err := srv.Serve(ctx, ln); err != nil {
			logger.Error("serve stopped", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down", "active_conns", srv.ActiveConns())
	srv.CloseConnections()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = metricsServer.Shutdown(shutdownCtx)
	_ = pprofServer.Shutdown(shutdownCtx)
}

func serveHTTP(server *http.Server, name string, logger *slog.Logger, stop context.CancelFunc) {
	logger.Info(name+" listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(name+" server stopped", "err", err)
		stop()
	}
}

// routeMessage handles the stateless protocol messages that need no session
// or registry context (Ping). Login and resume are handled by the
// gameApplication, which owns the session registry and room bindings.
func routeMessage(c *network.Connection, h network.Header, payload []byte) error {
	mt := protocol.MessageType(h.MessageType)

	switch mt {
	case protocol.MessageType_MSG_PING:
		return handlePing(c, h, payload)
	default:
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
