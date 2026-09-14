// Package metrics owns metric names, labels, and Prometheus exposition.
package metrics

import (
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	ErrInvalidSnapshot        = errors.New("metric snapshot values must not be negative")
	ErrInvalidMatchDuration   = errors.New("match duration must not be negative")
	ErrInvalidReconnectResult = errors.New("invalid reconnect result")
	ErrInvalidFrameResult     = errors.New("invalid frame result")
)

// ReconnectResult is a bounded label value. Keeping this set closed prevents
// client-controlled values from creating unbounded Prometheus series.
type ReconnectResult string

const (
	ReconnectSucceeded    ReconnectResult = "success"
	ReconnectInvalidToken ReconnectResult = "invalid_token"
	ReconnectExpired      ReconnectResult = "expired"
	ReconnectBackendError ReconnectResult = "backend_error"
)

// Snapshot contains current server state sampled by the integration layer.
type Snapshot struct {
	OnlinePlayers     int
	ActiveRooms       int
	MatchQueuePlayers int
}

// FrameResult is a bounded label value for inbound frame parsing outcomes.
// Keeping this set closed prevents client-controlled strings (or error text)
// from creating unbounded Prometheus series.
type FrameResult string

const (
	FrameInvalidMagic     FrameResult = "invalid_magic"
	FrameInvalidVersion   FrameResult = "invalid_version"
	FrameTooLarge         FrameResult = "too_large"
	FrameMessageTypeZero  FrameResult = "message_type_zero"
	FrameShortRead        FrameResult = "short_read"
)

// Metrics centralizes the project's collector definitions and registry. Its
// methods are safe for concurrent use through the Prometheus collectors.
type Metrics struct {
	registry *prometheus.Registry

	onlinePlayers     prometheus.Gauge
	activeRooms       prometheus.Gauge
	matchQueuePlayers prometheus.Gauge
	matches           prometheus.Counter
	matchDuration     prometheus.Histogram
	tickWorkDuration  prometheus.Histogram
	reconnectAttempts *prometheus.CounterVec

	// Network transport metrics (D9). Bytes and frames are counters; queue
	// depth is a gauge; rejections/drops are counters surfaced to prove
	// backpressure is observable and that a bad client cannot silently drop
	// another room's traffic.
	connectionsAccepted prometheus.Counter
	connectionsClosed   prometheus.Counter
	bytesReceived       prometheus.Counter
	bytesSent           prometheus.Counter
	framesReceived      prometheus.Counter
	framesSent          prometheus.Counter
	snapshotFramesSent  prometheus.Counter
	snapshotBytesSent   prometheus.Counter
	reliableQueueDepth  prometheus.Gauge
	reliableRejections  prometheus.Counter
	snapshotDrops       prometheus.Counter
	invalidFrames       *prometheus.CounterVec
}

// New creates an isolated registry containing Go/process collectors and the
// Odyssey application metrics. Isolation avoids duplicate registration in
// tests and when multiple gameserver instances share one process.
func New() *Metrics {
	registry := prometheus.NewRegistry()
	result := &Metrics{
		registry: registry,
		onlinePlayers: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "odyssey",
			Name:      "online_players",
			Help:      "Current number of online players.",
		}),
		activeRooms: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "odyssey",
			Name:      "active_rooms",
			Help:      "Current number of active rooms.",
		}),
		matchQueuePlayers: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "odyssey",
			Name:      "match_queue_players",
			Help:      "Current number of players waiting for a match.",
		}),
		matches: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "matches_total",
			Help:      "Total number of matches formed.",
		}),
		matchDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "odyssey",
			Name:      "match_duration_seconds",
			Help:      "Time a completed match spent waiting for enough players.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}),
		tickWorkDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "odyssey",
			Name:      "room_tick_work_duration_seconds",
			Help:      "Room tick work duration, excluding the wait for the next tick.",
			Buckets:   []float64{0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.02, 0.03333},
		}),
		reconnectAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "reconnect_attempts_total",
			Help:      "Total number of reconnect attempts by bounded result.",
		}, []string{"result"}),
		connectionsAccepted: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "connections_accepted_total",
			Help:      "Total number of TCP connections accepted.",
		}),
		connectionsClosed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "connections_closed_total",
			Help:      "Total number of TCP connections fully torn down.",
		}),
		bytesReceived: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "network_bytes_received_total",
			Help:      "Total inbound bytes read from client sockets.",
		}),
		bytesSent: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "network_bytes_sent_total",
			Help:      "Total outbound bytes written to client sockets.",
		}),
		framesReceived: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "network_frames_received_total",
			Help:      "Total inbound frames decoded successfully.",
		}),
		framesSent: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "network_frames_sent_total",
			Help:      "Total outbound frames written.",
		}),
		snapshotFramesSent: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "snapshot_frames_sent_total",
			Help:      "Total world-snapshot frames delivered to sinks.",
		}),
		snapshotBytesSent: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "snapshot_bytes_sent_total",
			Help:      "Total world-snapshot frame bytes delivered to sinks.",
		}),
		reliableQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "odyssey",
			Name:      "reliable_queue_depth",
			Help:      "Current number of frames queued on reliable outbound queues (sum across connections).",
		}),
		reliableRejections: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "reliable_queue_rejections_total",
			Help:      "Total reliable-queue sends rejected because the queue was full.",
		}),
		snapshotDrops: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "snapshot_drops_total",
			Help:      "Total stale world snapshots evicted by latest-wins delivery.",
		}),
		invalidFrames: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "invalid_frames_total",
			Help:      "Total inbound frames rejected at parse time, by bounded reason.",
		}, []string{"reason"}),
	}

	registry.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
		result.onlinePlayers,
		result.activeRooms,
		result.matchQueuePlayers,
		result.matches,
		result.matchDuration,
		result.tickWorkDuration,
		result.reconnectAttempts,
		result.connectionsAccepted,
		result.connectionsClosed,
		result.bytesReceived,
		result.bytesSent,
		result.framesReceived,
		result.framesSent,
		result.snapshotFramesSent,
		result.snapshotBytesSent,
		result.reliableQueueDepth,
		result.reliableRejections,
		result.snapshotDrops,
		result.invalidFrames,
	)
	return result
}

// ObserveTickWork records one room tick's active work duration.
func (m *Metrics) ObserveTickWork(duration time.Duration) {
	if duration >= 0 {
		m.tickWorkDuration.Observe(duration.Seconds())
	}
}

// Handler exposes this module's isolated registry in Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// SetSnapshot validates the entire state sample before updating its gauges.
// Prometheus gauges are individually thread-safe; callers should treat values
// from one call as one logical sample even though a scrape may overlap it.
func (m *Metrics) SetSnapshot(snapshot Snapshot) error {
	if snapshot.OnlinePlayers < 0 || snapshot.ActiveRooms < 0 || snapshot.MatchQueuePlayers < 0 {
		return ErrInvalidSnapshot
	}

	m.onlinePlayers.Set(float64(snapshot.OnlinePlayers))
	m.activeRooms.Set(float64(snapshot.ActiveRooms))
	m.matchQueuePlayers.Set(float64(snapshot.MatchQueuePlayers))
	return nil
}

// ObserveMatch records one completed matchmaking wait.
func (m *Metrics) ObserveMatch(duration time.Duration) error {
	if duration < 0 {
		return ErrInvalidMatchDuration
	}
	m.matches.Inc()
	m.matchDuration.Observe(duration.Seconds())
	return nil
}

// ObserveReconnect records one reconnect attempt using a bounded result label.
func (m *Metrics) ObserveReconnect(result ReconnectResult) error {
	if !validReconnectResult(result) {
		return ErrInvalidReconnectResult
	}
	m.reconnectAttempts.WithLabelValues(string(result)).Inc()
	return nil
}

func validReconnectResult(result ReconnectResult) bool {
	switch result {
	case ReconnectSucceeded, ReconnectInvalidToken, ReconnectExpired, ReconnectBackendError:
		return true
	default:
		return false
	}
}

// ObserveConnectionAccepted records one accepted TCP connection.
func (m *Metrics) ObserveConnectionAccepted() {
	m.connectionsAccepted.Inc()
}

// ObserveConnectionClosed records one fully torn-down TCP connection.
func (m *Metrics) ObserveConnectionClosed() {
	m.connectionsClosed.Inc()
}

// ObserveBytesReceived records n inbound bytes read from a client socket.
func (m *Metrics) ObserveBytesReceived(n int) {
	if n <= 0 {
		return
	}
	m.bytesReceived.Add(float64(n))
}

// ObserveBytesSent records n outbound bytes written to a client socket.
func (m *Metrics) ObserveBytesSent(n int) {
	if n <= 0 {
		return
	}
	m.bytesSent.Add(float64(n))
}

// ObserveFrameReceived records one successfully decoded inbound frame.
func (m *Metrics) ObserveFrameReceived() {
	m.framesReceived.Inc()
}

// ObserveFrameSent records one outbound frame written.
func (m *Metrics) ObserveFrameSent() {
	m.framesSent.Inc()
}

// ObserveSnapshotSent records one world-snapshot frame (and its byte size)
// delivered to a sink. Size is the full encoded frame length.
func (m *Metrics) ObserveSnapshotSent(bytes int) {
	m.snapshotFramesSent.Inc()
	if bytes > 0 {
		m.snapshotBytesSent.Add(float64(bytes))
	}
}

// SetReliableQueueDepth sets the aggregate reliable-queue depth gauge. The
// value is the total frames currently queued across all connections.
func (m *Metrics) SetReliableQueueDepth(depth int) {
	if depth < 0 {
		return
	}
	m.reliableQueueDepth.Set(float64(depth))
}

// ObserveReliableRejection records one reliable-queue send rejected because
// the queue was full (backpressure).
func (m *Metrics) ObserveReliableRejection() {
	m.reliableRejections.Inc()
}

// ObserveSnapshotDrop records one stale world snapshot evicted by latest-wins
// delivery (a slow consumer falling behind is observable, never silent).
func (m *Metrics) ObserveSnapshotDrop() {
	m.snapshotDrops.Inc()
}

// ObserveInvalidFrame records one inbound frame rejected at parse time, using
// a bounded reason label.
func (m *Metrics) ObserveInvalidFrame(reason FrameResult) error {
	if !validFrameResult(reason) {
		return ErrInvalidFrameResult
	}
	m.invalidFrames.WithLabelValues(string(reason)).Inc()
	return nil
}

func validFrameResult(reason FrameResult) bool {
	switch reason {
	case FrameInvalidMagic, FrameInvalidVersion, FrameTooLarge, FrameMessageTypeZero, FrameShortRead:
		return true
	default:
		return false
	}
}
