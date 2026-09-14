// Package metrics owns metric names, labels, and Prometheus exposition.
package metrics

import (
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	ErrInvalidSnapshot        = errors.New("metric snapshot values must not be negative")
	ErrInvalidCombatSnapshot  = errors.New("combat metric snapshot values must not be negative")
	ErrInvalidDamageAmount    = errors.New("damage amount must be finite and positive")
	ErrInvalidMatchDuration   = errors.New("match duration must not be negative")
	ErrInvalidReconnectResult = errors.New("invalid reconnect result")
	ErrInvalidStageResult     = errors.New("invalid stage result")
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

// CombatSnapshot contains current combat entity counts supplied by the Room
// metrics adapter. Values must come from authoritative Room state.
type CombatSnapshot struct {
	ActiveMonsters    int
	ActiveProjectiles int
}

// StageResult is a bounded label value for terminal stage outcomes.
type StageResult string

const (
	StageResultCleared  StageResult = "cleared"
	StageResultDefeated StageResult = "defeated"
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
	activeMonsters    prometheus.Gauge
	activeProjectiles prometheus.Gauge
	damageDealt       prometheus.Counter
	stageResults      *prometheus.CounterVec
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
		activeMonsters: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "odyssey",
			Name:      "active_monsters",
			Help:      "Current number of authoritative monsters across active rooms.",
		}),
		activeProjectiles: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "odyssey",
			Name:      "active_projectiles",
			Help:      "Current number of authoritative projectiles across active rooms.",
		}),
		damageDealt: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "damage_dealt_total",
			Help:      "Total authoritative hit points of damage applied.",
		}),
		stageResults: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "odyssey",
			Name:      "stage_results_total",
			Help:      "Total authoritative terminal stage outcomes by bounded result.",
		}, []string{"result"}),
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
		result.activeMonsters,
		result.activeProjectiles,
		result.damageDealt,
		result.stageResults,
	)
	result.stageResults.WithLabelValues(string(StageResultCleared))
	result.stageResults.WithLabelValues(string(StageResultDefeated))
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

// SetCombatSnapshot publishes current authoritative combat entity counts.
func (m *Metrics) SetCombatSnapshot(snapshot CombatSnapshot) error {
	if snapshot.ActiveMonsters < 0 || snapshot.ActiveProjectiles < 0 {
		return ErrInvalidCombatSnapshot
	}
	m.activeMonsters.Set(float64(snapshot.ActiveMonsters))
	m.activeProjectiles.Set(float64(snapshot.ActiveProjectiles))
	return nil
}

// ObserveDamage records damage after the authoritative World applies it.
func (m *Metrics) ObserveDamage(amount float64) error {
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return ErrInvalidDamageAmount
	}
	m.damageDealt.Add(amount)
	return nil
}

// ObserveStageResult records one authoritative terminal stage outcome.
func (m *Metrics) ObserveStageResult(result StageResult) error {
	if !validStageResult(result) {
		return ErrInvalidStageResult
	}
	m.stageResults.WithLabelValues(string(result)).Inc()
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

func validStageResult(result StageResult) bool {
	switch result {
	case StageResultCleared, StageResultDefeated:
		return true
	default:
		return false
	}
}
