// Package metrics 统一管理指标名称、标签和 Prometheus 输出。
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
	ErrInvalidRewardResult    = errors.New("invalid reward result")
	ErrInvalidDirectorSample  = errors.New("invalid director sample")
	ErrInvalidQueueSnapshot   = errors.New("queue metric snapshot values must not be negative")
)

// ReconnectResult 是有限集合标签，防止客户端输入制造无限数量的 Prometheus 时间序列。
type ReconnectResult string

const (
	ReconnectSucceeded    ReconnectResult = "success"
	ReconnectInvalidToken ReconnectResult = "invalid_token"
	ReconnectExpired      ReconnectResult = "expired"
	ReconnectBackendError ReconnectResult = "backend_error"
)

// Snapshot 保存集成层采样到的当前服务端状态。
type Snapshot struct {
	OnlinePlayers     int
	ActiveRooms       int
	MatchQueuePlayers int
}

// CombatSnapshot 保存 Room 指标适配器提供的当前战斗实体数量，数值必须来自权威房间状态。
type CombatSnapshot struct {
	ActiveMonsters    int
	ActiveProjectiles int
}

// StageResult 是表示关卡终局结果的有限集合标签。
type StageResult string

const (
	StageResultCleared  StageResult = "cleared"
	StageResultDefeated StageResult = "defeated"
)

// RewardResult 刻意限制为固定集合；装备 ID、玩家 ID 和错误文本只能写日志或管理查询，
// 不能进入 Prometheus 标签。
type RewardResult string

const (
	RewardOffered   RewardResult = "offered"
	RewardChosen    RewardResult = "chosen"
	RewardDefaulted RewardResult = "defaulted"
	RewardInvalid   RewardResult = "invalid"
)

// DirectorSample 保存一次已成功应用的权威导演决策输入与输出。room/stage/seed 等关联信息
// 写入管理事件日志，不作为高基数指标标签。
type DirectorSample struct {
	Duration           time.Duration
	ClearTimeSeconds   float64
	TeamHPPercent      float64
	AverageDPS         float64
	DeathCount         int
	DamageTaken        float64
	EquipmentPower     float64
	PreviousDifficulty float64
	NewDifficulty      float64
	Adjustment         float64
	MonsterCount       int
}

// QueueSnapshot 是所有在线房间和连接的当前聚合队列深度；累计拒绝/丢弃数另行观测。
type QueueSnapshot struct {
	RoomControlDepth     int
	RoomInputDepth       int
	NetworkReliableDepth int
}

// QueueDelta 保存集成层根据 Room 与网络权威计数器计算出的单调增量。
type QueueDelta struct {
	RoomRejections          uint64
	RejectedInputs          uint64
	DroppedSnapshots        uint64
	DroppedTickSamples      uint64
	NetworkReliableRejected uint64
	NetworkSnapshotReplaced uint64
}

type ResultWriterSnapshot struct {
	QueueDepth         int
	InFlight           int64
	Persisted          uint64
	Idempotent         uint64
	Retries            uint64
	Failed             uint64
	Rejected           uint64
	DeadLetterFailures uint64
}

// Metrics 集中保存项目的采集器定义与注册表；底层 Prometheus 采集器保证方法并发安全。
type Metrics struct {
	registry *prometheus.Registry

	onlinePlayers               prometheus.Gauge
	activeRooms                 prometheus.Gauge
	matchQueuePlayers           prometheus.Gauge
	matches                     prometheus.Counter
	matchDuration               prometheus.Histogram
	tickWorkDuration            prometheus.Histogram
	reconnectAttempts           *prometheus.CounterVec
	activeMonsters              prometheus.Gauge
	activeProjectiles           prometheus.Gauge
	damageDealt                 prometheus.Counter
	stageResults                *prometheus.CounterVec
	rewards                     *prometheus.CounterVec
	directorDecisions           prometheus.Counter
	directorDuration            prometheus.Histogram
	directorInputClearTime      prometheus.Gauge
	directorInputTeamHP         prometheus.Gauge
	directorInputDPS            prometheus.Gauge
	directorInputDeaths         prometheus.Gauge
	directorInputDamageTaken    prometheus.Gauge
	directorInputEquipmentPower prometheus.Gauge
	directorOutputDifficulty    prometheus.Gauge
	directorOutputAdjustment    prometheus.Gauge
	directorOutputMonsters      prometheus.Gauge
	roomControlQueueDepth       prometheus.Gauge
	roomInputQueueDepth         prometheus.Gauge
	networkReliableQueueDepth   prometheus.Gauge
	roomQueueRejections         prometheus.Counter
	rejectedInputs              prometheus.Counter
	droppedSnapshots            prometheus.Counter
	droppedTickSamples          prometheus.Counter
	networkReliableRejected     prometheus.Counter
	networkSnapshotReplaced     prometheus.Counter
	resultQueueDepth            prometheus.Gauge
	resultInFlight              prometheus.Gauge
	resultWrites                *prometheus.CounterVec
}

// New 创建包含 Go/进程采集器和游戏指标的独立注册表，避免测试或同进程多实例重复注册。
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
		rewards: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "odyssey", Name: "rewards_total",
			Help: "Total authoritative reward outcomes by bounded result.",
		}, []string{"result"}),
		directorDecisions: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "odyssey", Name: "director_decisions_total",
			Help: "Total successfully applied authoritative Director decisions.",
		}),
		directorDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "odyssey", Name: "director_decision_duration_seconds",
			Help:    "Duration of successfully applied Director decisions.",
			Buckets: []float64{0.00001, 0.000025, 0.00005, 0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01},
		}),
		directorInputClearTime:      newGauge("director_input_clear_time_seconds", "Clear time input of the latest applied Director decision."),
		directorInputTeamHP:         newGauge("director_input_team_hp_ratio", "Team health ratio input of the latest applied Director decision."),
		directorInputDPS:            newGauge("director_input_average_dps", "Average DPS input of the latest applied Director decision."),
		directorInputDeaths:         newGauge("director_input_death_count", "Death count input of the latest applied Director decision."),
		directorInputDamageTaken:    newGauge("director_input_damage_taken", "Damage-taken input of the latest applied Director decision."),
		directorInputEquipmentPower: newGauge("director_input_equipment_power", "Equipment-power input of the latest applied Director decision."),
		directorOutputDifficulty:    newGauge("director_output_difficulty", "Difficulty output of the latest applied Director decision."),
		directorOutputAdjustment:    newGauge("director_output_adjustment_ratio", "Difficulty adjustment output of the latest applied Director decision."),
		directorOutputMonsters:      newGauge("director_output_monster_count", "Monster-count output of the latest applied Director decision."),
		roomControlQueueDepth:       newGauge("room_control_queue_depth", "Current aggregate Room control-queue depth."),
		roomInputQueueDepth:         newGauge("room_input_queue_depth", "Current aggregate Room input-queue depth."),
		networkReliableQueueDepth:   newGauge("network_reliable_queue_depth", "Current aggregate reliable network-queue depth."),
		roomQueueRejections:         newCounter("room_queue_rejections_total", "Total Room command admissions rejected because a queue was full."),
		rejectedInputs:              newCounter("room_rejected_inputs_total", "Total queued player inputs rejected by authoritative Room validation."),
		droppedSnapshots:            newCounter("room_dropped_snapshots_total", "Total stale Room snapshots replaced before integration consumption."),
		droppedTickSamples:          newCounter("room_dropped_tick_samples_total", "Total lossy Room tick samples dropped before metrics consumption."),
		networkReliableRejected:     newCounter("network_reliable_queue_rejections_total", "Total reliable network sends rejected by a full or closed queue."),
		networkSnapshotReplaced:     newCounter("network_snapshot_replacements_total", "Total stale network snapshots replaced by a newer snapshot."),
		resultQueueDepth:            newGauge("result_queue_depth", "Current asynchronous result queue depth."),
		resultInFlight:              newGauge("result_writes_in_flight", "Current asynchronous result writes in flight."),
		resultWrites: prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "odyssey", Name: "result_writes_total",
			Help: "Asynchronous result writer outcomes by bounded result."}, []string{"result"}),
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
		result.rewards,
		result.directorDecisions,
		result.directorDuration,
		result.directorInputClearTime,
		result.directorInputTeamHP,
		result.directorInputDPS,
		result.directorInputDeaths,
		result.directorInputDamageTaken,
		result.directorInputEquipmentPower,
		result.directorOutputDifficulty,
		result.directorOutputAdjustment,
		result.directorOutputMonsters,
		result.roomControlQueueDepth,
		result.roomInputQueueDepth,
		result.networkReliableQueueDepth,
		result.roomQueueRejections,
		result.rejectedInputs,
		result.droppedSnapshots,
		result.droppedTickSamples,
		result.networkReliableRejected,
		result.networkSnapshotReplaced,
		result.resultQueueDepth,
		result.resultInFlight,
		result.resultWrites,
	)
	result.stageResults.WithLabelValues(string(StageResultCleared))
	result.stageResults.WithLabelValues(string(StageResultDefeated))
	for _, reward := range []RewardResult{RewardOffered, RewardChosen, RewardDefaulted, RewardInvalid} {
		result.rewards.WithLabelValues(string(reward))
	}
	for _, outcome := range []string{"persisted", "idempotent", "retry", "failed", "rejected", "dead_letter_failure"} {
		result.resultWrites.WithLabelValues(outcome)
	}
	return result
}

func (m *Metrics) SetResultWriterSnapshot(current, previous ResultWriterSnapshot) error {
	if current.QueueDepth < 0 || current.InFlight < 0 {
		return ErrInvalidQueueSnapshot
	}
	m.resultQueueDepth.Set(float64(current.QueueDepth))
	m.resultInFlight.Set(float64(current.InFlight))
	for result, delta := range map[string]uint64{
		"persisted":           counterDelta(current.Persisted, previous.Persisted),
		"idempotent":          counterDelta(current.Idempotent, previous.Idempotent),
		"retry":               counterDelta(current.Retries, previous.Retries),
		"failed":              counterDelta(current.Failed, previous.Failed),
		"rejected":            counterDelta(current.Rejected, previous.Rejected),
		"dead_letter_failure": counterDelta(current.DeadLetterFailures, previous.DeadLetterFailures),
	} {
		m.resultWrites.WithLabelValues(result).Add(float64(delta))
	}
	return nil
}

func counterDelta(current, previous uint64) uint64 {
	if current < previous {
		return current
	}
	return current - previous
}

func newGauge(name, help string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "odyssey", Name: name, Help: help})
}

func newCounter(name, help string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{Namespace: "odyssey", Name: name, Help: help})
}

// ObserveTickWork 记录一次房间 Tick 的实际工作耗时。
func (m *Metrics) ObserveTickWork(duration time.Duration) {
	if duration >= 0 {
		m.tickWorkDuration.Observe(duration.Seconds())
	}
}

// Handler 以 Prometheus 文本格式暴露本模块的独立注册表。
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// SetSnapshot 先校验完整状态样本，再更新仪表。每个 Prometheus Gauge 都并发安全；
// 即使抓取与更新重叠，调用方仍应把同一次调用中的值视为一个逻辑样本。
func (m *Metrics) SetSnapshot(snapshot Snapshot) error {
	if snapshot.OnlinePlayers < 0 || snapshot.ActiveRooms < 0 || snapshot.MatchQueuePlayers < 0 {
		return ErrInvalidSnapshot
	}

	m.onlinePlayers.Set(float64(snapshot.OnlinePlayers))
	m.activeRooms.Set(float64(snapshot.ActiveRooms))
	m.matchQueuePlayers.Set(float64(snapshot.MatchQueuePlayers))
	return nil
}

// SetCombatSnapshot 发布当前权威战斗实体数量。
func (m *Metrics) SetCombatSnapshot(snapshot CombatSnapshot) error {
	if snapshot.ActiveMonsters < 0 || snapshot.ActiveProjectiles < 0 {
		return ErrInvalidCombatSnapshot
	}
	m.activeMonsters.Set(float64(snapshot.ActiveMonsters))
	m.activeProjectiles.Set(float64(snapshot.ActiveProjectiles))
	return nil
}

// ObserveDamage 记录权威 World 已经实际结算的伤害。
func (m *Metrics) ObserveDamage(amount float64) error {
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return ErrInvalidDamageAmount
	}
	m.damageDealt.Add(amount)
	return nil
}

// ObserveStageResult 记录一次权威关卡终局结果。
func (m *Metrics) ObserveStageResult(result StageResult) error {
	if !validStageResult(result) {
		return ErrInvalidStageResult
	}
	m.stageResults.WithLabelValues(string(result)).Inc()
	return nil
}

// ObserveReward 记录权威奖励选项、已应用选择/默认项或拒绝；非法客户端值不会成为标签。
func (m *Metrics) ObserveReward(result RewardResult) error {
	if !validRewardResult(result) {
		return ErrInvalidRewardResult
	}
	m.rewards.WithLabelValues(string(result)).Inc()
	return nil
}

// ObserveDirector 记录一条已完整校验并成功应用的导演决策；修改采集器前会校验全部字段。
func (m *Metrics) ObserveDirector(sample DirectorSample) error {
	if !validDirectorSample(sample) {
		return ErrInvalidDirectorSample
	}
	m.directorDecisions.Inc()
	m.directorDuration.Observe(sample.Duration.Seconds())
	m.directorInputClearTime.Set(sample.ClearTimeSeconds)
	m.directorInputTeamHP.Set(sample.TeamHPPercent)
	m.directorInputDPS.Set(sample.AverageDPS)
	m.directorInputDeaths.Set(float64(sample.DeathCount))
	m.directorInputDamageTaken.Set(sample.DamageTaken)
	m.directorInputEquipmentPower.Set(sample.EquipmentPower)
	m.directorOutputDifficulty.Set(sample.NewDifficulty)
	m.directorOutputAdjustment.Set(sample.Adjustment)
	m.directorOutputMonsters.Set(float64(sample.MonsterCount))
	return nil
}

// SetQueueSnapshot 发布聚合实时队列深度；数值刻意不带标签，新增房间/玩家不会创建新序列。
func (m *Metrics) SetQueueSnapshot(snapshot QueueSnapshot) error {
	if snapshot.RoomControlDepth < 0 || snapshot.RoomInputDepth < 0 || snapshot.NetworkReliableDepth < 0 {
		return ErrInvalidQueueSnapshot
	}
	m.roomControlQueueDepth.Set(float64(snapshot.RoomControlDepth))
	m.roomInputQueueDepth.Set(float64(snapshot.RoomInputDepth))
	m.networkReliableQueueDepth.Set(float64(snapshot.NetworkReliableDepth))
	return nil
}

// SetRoomQueueSnapshot 只更新 Room 拥有的深度，让网络采样器可独立运行而不互相覆盖。
func (m *Metrics) SetRoomQueueSnapshot(controlDepth, inputDepth int) error {
	if controlDepth < 0 || inputDepth < 0 {
		return ErrInvalidQueueSnapshot
	}
	m.roomControlQueueDepth.Set(float64(controlDepth))
	m.roomInputQueueDepth.Set(float64(inputDepth))
	return nil
}

// SetNetworkQueueDepth 只更新网络可靠队列的聚合深度。
func (m *Metrics) SetNetworkQueueDepth(depth int) error {
	if depth < 0 {
		return ErrInvalidQueueSnapshot
	}
	m.networkReliableQueueDepth.Set(float64(depth))
	return nil
}

// ObserveQueueDelta 累加从源计数器计算的单调增量，避免直接重复采样累计值造成重复计数。
func (m *Metrics) ObserveQueueDelta(delta QueueDelta) {
	m.roomQueueRejections.Add(float64(delta.RoomRejections))
	m.rejectedInputs.Add(float64(delta.RejectedInputs))
	m.droppedSnapshots.Add(float64(delta.DroppedSnapshots))
	m.droppedTickSamples.Add(float64(delta.DroppedTickSamples))
	m.networkReliableRejected.Add(float64(delta.NetworkReliableRejected))
	m.networkSnapshotReplaced.Add(float64(delta.NetworkSnapshotReplaced))
}

// ObserveMatch 记录一次完成的匹配等待。
func (m *Metrics) ObserveMatch(duration time.Duration) error {
	if duration < 0 {
		return ErrInvalidMatchDuration
	}
	m.matches.Inc()
	m.matchDuration.Observe(duration.Seconds())
	return nil
}

// ObserveReconnect 使用有限结果标签记录一次重连尝试。
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

func validRewardResult(result RewardResult) bool {
	switch result {
	case RewardOffered, RewardChosen, RewardDefaulted, RewardInvalid:
		return true
	default:
		return false
	}
}

func validDirectorSample(sample DirectorSample) bool {
	if sample.Duration < 0 || sample.DeathCount < 0 || sample.MonsterCount < 1 || sample.TeamHPPercent < 0 || sample.TeamHPPercent > 1 {
		return false
	}
	values := []float64{sample.ClearTimeSeconds, sample.TeamHPPercent, sample.AverageDPS, sample.DamageTaken,
		sample.EquipmentPower, sample.PreviousDifficulty, sample.NewDifficulty, sample.Adjustment}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return sample.ClearTimeSeconds > 0 && sample.AverageDPS >= 0 && sample.DamageTaken >= 0 &&
		sample.EquipmentPower > 0 && sample.PreviousDifficulty > 0 && sample.NewDifficulty > 0
}
