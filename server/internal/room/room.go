// Package room 是“房间 Actor”层：每个房间启动一个独立 goroutine，且只有它能修改
// game.World。网络、匹配、断线处理等其他 goroutine 不能直接碰世界状态，只能通过
// 有界 channel 投递命令。这样把复杂的“多人同时写地图”问题，转换成房间内的顺序执行，
// 是服务端避免竞态、保证同一输入序列可复现的核心设计。本包不做任何网络 I/O。
package room

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

type ID uint64
type SessionID uint64

var (
	ErrClosed         = errors.New("room closed")
	ErrQueueFull      = errors.New("room command queue full")
	ErrInvalidSession = errors.New("invalid session ID")
	ErrSessionBound   = errors.New("session already bound to another player")
	ErrNotJoined      = errors.New("session not joined")
)

type Config struct {
	World              game.Config
	ControlCapacity    int
	InputCapacity      int
	ControlsPerTick    int
	InputsPerTick      int
	TickSampleCapacity int
	EmptyTimeout       time.Duration
	EventCapacity      int
}

func DefaultConfig() Config {
	return Config{World: game.DefaultConfig(), ControlCapacity: 64, InputCapacity: 256,
		ControlsPerTick: 16, InputsPerTick: 128, TickSampleCapacity: 128, EmptyTimeout: 5 * time.Second, EventCapacity: 64}
}

func (c Config) validate() error {
	if err := c.World.Validate(); err != nil {
		return err
	}
	if c.ControlCapacity < 1 || c.InputCapacity < 1 || c.ControlsPerTick < 1 || c.InputsPerTick < 1 ||
		c.TickSampleCapacity < 1 || c.EmptyTimeout <= 0 || c.EventCapacity < 1 || c.EventCapacity > 1024 {
		return fmt.Errorf("invalid room configuration")
	}
	return nil
}

type Snapshot struct {
	RoomID ID
	Closed bool
	game.Snapshot
}

func (s Snapshot) clone() Snapshot { s.Snapshot = s.Snapshot.Clone(); return s }

// TickSample 统计命令消费、模拟与快照发布耗时；不包含等待下个 Tick 和发送样本的时间。
type TickSample struct {
	RoomID       ID
	ServerTick   uint64
	StartedAt    time.Time
	WorkDuration time.Duration
	Controls     int
	Inputs       int
	Players      int
}

type Stats struct {
	RoomID             ID
	ServerTick         uint64
	Players            int
	ControlQueueDepth  int
	InputQueueDepth    int
	QueueRejections    uint64
	RejectedInputs     uint64
	DroppedSnapshots   uint64
	DroppedTickSamples uint64
	LastTick           TickSample
	Closed             bool
	CloseReason        string
}

type control struct {
	join         bool
	sessionID    SessionID
	playerID     entity.ID
	result       chan error
	stagePlan    *stage.Plan
	rewardStart  *rewardStart
	rewardChoice equipment.ID
	stageResult  chan StageResultReceipt
	resumeState  chan ResumeStateReceipt
	gameOutcome  game.GameOutcome
	gameResult   chan GameResultReceipt
}

type rewardStart struct {
	catalog       equipment.Catalog
	seed          int64
	durationTicks uint64
}

type StageResultReceipt struct {
	Result game.StageResult
	Err    error
}

type ResumeState struct {
	Snapshot Snapshot
	Reward   *game.RewardUpdate
}

func (s ResumeState) clone() ResumeState {
	s.Snapshot = s.Snapshot.clone()
	if s.Reward != nil {
		copy := s.Reward.Clone()
		s.Reward = &copy
	}
	return s
}

type ResumeStateReceipt struct {
	State ResumeState
	Err   error
}

type GameResultReceipt struct {
	Result game.GameResult
	Err    error
}

type movement struct {
	sessionID  SessionID
	generation uint64
	input      game.Input
	received   time.Time
}

type binding struct {
	playerID   entity.ID
	generation uint64
}

// Room 不能被复制。
//
// 并发边界可以简单记成三层：
//   - run 所在的房间 goroutine 是“唯一写者”，负责 world、members 和逐帧统计；
//   - mu 只保护外部 goroutine 的入队、会话绑定查询与关闭过程，不用于包住游戏 Tick；
//   - latest/status 使用 atomic.Pointer 发布只读快照，监控线程读取时无需阻塞房间。
//
// controls 与 inputs 分队列，是为了让加入、离开、开关卡等生命周期命令不会被大量
// 30Hz 玩家输入淹没。两个队列都有容量上限，满时立即返回 ErrQueueFull 形成背压。
type Room struct {
	id       ID
	config   Config
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	closed   bool
	controls chan control
	inputs   chan movement
	updates  chan Snapshot
	samples  chan TickSample
	events   chan game.EventBatch
	rewards  chan game.RewardUpdateBatch
	latest   atomic.Pointer[Snapshot]
	status   atomic.Pointer[Stats]
	rejected atomic.Uint64

	world      *game.World
	members    map[SessionID]binding
	generation uint64
	emptyTimer *time.Timer
	stats      Stats
}

// Start 创建 World 后立即启动唯一的房间 goroutine。
// 上层可调用 Close 或取消父 context；从未有人加入或已经空掉的房间也会在
// EmptyTimeout 后自动回收，避免泄漏 goroutine 和内存。
func Start(ctx context.Context, id ID, config Config, catalogs ...equipment.Catalog) (*Room, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid room ID")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := game.NewWorld(config.World, catalogs...)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &Room{id: id, config: config, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		controls: make(chan control, config.ControlCapacity), inputs: make(chan movement, config.InputCapacity),
		updates: make(chan Snapshot, 1), samples: make(chan TickSample, config.TickSampleCapacity),
		events:  make(chan game.EventBatch, config.EventCapacity),
		rewards: make(chan game.RewardUpdateBatch, config.EventCapacity),
		world:   w, members: make(map[SessionID]binding), emptyTimer: time.NewTimer(config.EmptyTimeout), stats: Stats{RoomID: id}}
	s := Snapshot{RoomID: id, Snapshot: w.Snapshot()}
	r.latest.Store(&s)
	r.storeStats()
	go r.run()
	return r, nil
}

// Join 把可信的 Session -> Player 绑定加入队列。回执只返回一次后关闭；只有回执值为 nil
// 才表示玩家已加入，入队时无错误仅表示命令已排队。重复提交相同绑定是幂等的，ID 由服务端分配。
func (r *Room) Join(sessionID SessionID, playerID entity.ID) (<-chan error, error) {
	if sessionID == 0 {
		return nil, ErrInvalidSession
	}
	if playerID == 0 {
		return nil, game.ErrInvalidPlayer
	}
	return r.submit(control{join: true, sessionID: sessionID, playerID: playerID})
}

// Leave 使用独立于输入负载的生命周期队列且操作幂等。若返回 ErrQueueFull，会话清理必须
// 重试到命令被接收或 Done 关闭，不能静默丢弃断线命令。
func (r *Room) Leave(sessionID SessionID) (<-chan error, error) {
	if sessionID == 0 {
		return nil, ErrInvalidSession
	}
	return r.submit(control{sessionID: sessionID})
}

// StartStage 是可信的服务端编排命令，客户端不能直接调用。Plan 入队前会复制，结果通过回执返回。
func (r *Room) StartStage(plan stage.Plan) (<-chan error, error) {
	if err := game.ValidateStage(plan, r.config.World); err != nil {
		return nil, err
	}
	plan = plan.Clone()
	return r.submit(control{stagePlan: &plan})
}

// StartReward 把已通关阶段切入服务端控制的奖励轮；目录必须在 Room Tick 外加载并校验。
func (r *Room) StartReward(catalog equipment.Catalog, seed int64, durationTicks uint64) (<-chan error, error) {
	return r.submit(control{rewardStart: &rewardStart{catalog: catalog, seed: seed, durationTicks: durationTicks}})
}

// ChooseReward 从可信 Session 绑定解析玩家身份；客户端只能提交装备 ID，World 会校验私人选项。
func (r *Room) ChooseReward(sessionID SessionID, equipmentID equipment.ID) (<-chan error, error) {
	if sessionID == 0 {
		return nil, ErrInvalidSession
	}
	if equipmentID == 0 {
		return nil, equipment.ErrUnknownEquipment
	}
	return r.submit(control{sessionID: sessionID, rewardChoice: equipmentID})
}

// CompletedStage 通过 Room 唯一写者读取不可变 Plan 与冻结指标，导演无需直接访问 World。
func (r *Room) CompletedStage() (<-chan StageResultReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return nil, ErrClosed
	}
	receipt := make(chan StageResultReceipt, 1)
	select {
	case r.controls <- control{stageResult: receipt}:
		return receipt, nil
	default:
		r.rejected.Add(1)
		return nil, ErrQueueFull
	}
}

// ResumeState 在房间 goroutine 中重建完整权威快照和该玩家的私人奖励状态。调用前已完成
// 令牌校验与连接替换，原 SessionID 保持不变。
func (r *Room) ResumeState(sessionID SessionID) (<-chan ResumeStateReceipt, error) {
	if sessionID == 0 {
		return nil, ErrInvalidSession
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return nil, ErrClosed
	}
	receipt := make(chan ResumeStateReceipt, 1)
	select {
	case r.controls <- control{sessionID: sessionID, resumeState: receipt}:
		return receipt, nil
	default:
		r.rejected.Add(1)
		return nil, ErrQueueFull
	}
}

// GameResult 返回可异步持久化的独立终局值；World 会按当前阶段校验可信结果。
func (r *Room) GameResult(outcome game.GameOutcome) (<-chan GameResultReceipt, error) {
	if !outcome.Valid() {
		return nil, game.ErrInvalidGameOutcome
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return nil, ErrClosed
	}
	receipt := make(chan GameResultReceipt, 1)
	select {
	case r.controls <- control{gameOutcome: outcome, gameResult: receipt}:
		return receipt, nil
	default:
		r.rejected.Add(1)
		return nil, ErrQueueFull
	}
}

func (r *Room) submit(c control) (<-chan error, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return nil, ErrClosed
	}
	c.result = make(chan error, 1)
	select {
	case r.controls <- c:
		return c.result, nil
	default:
		r.rejected.Add(1)
		return nil, ErrQueueFull
	}
}

// Input 只校验并入队“操作意图”，不会在网络 goroutine 中等待下一次 Tick。
// 真正的 playerID 由服务端保存的 Session 绑定解析，客户端不能冒充其他玩家。
//
// generation 用于拦截旧连接遗留的输入：玩家离开再加入后 generation 会变化，队列里
// 旧 generation 的 movement 即使稍后出队也会被丢弃。入队成功不等于已经执行；客户端
// 应以快照中的 LastProcessedInputSeq 作为权威确认。
func (r *Room) Input(sessionID SessionID, input game.Input) error {
	if sessionID == 0 {
		return ErrInvalidSession
	}
	if err := input.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return ErrClosed
	}
	member, joined := r.members[sessionID]
	if !joined {
		return ErrNotJoined
	}
	select {
	case r.inputs <- movement{sessionID: sessionID, generation: member.generation, input: input, received: time.Now()}:
		return nil
	default:
		r.rejected.Add(1)
		return ErrQueueFull
	}
}

// Snapshots 只有一个复制分发器消费者；每个值都是独立副本，慢消费者只丢旧快照而不阻塞模拟。
func (r *Room) Snapshots() <-chan Snapshot { return r.updates }
func (r *Room) LatestSnapshot() Snapshot   { return r.latest.Load().clone() }

// TickSamples 只有一个指标适配器消费者，队列有界且允许丢失；解释分位数时需同时报告丢弃量。
func (r *Room) TickSamples() <-chan TickSample { return r.samples }

// Events 供唯一的可靠事件分发器消费；饱和会关闭房间并记录 event_backpressure，
// 战斗事件绝不被静默替换。
func (r *Room) Events() <-chan game.EventBatch { return r.events }

// RewardUpdates 只有一个消费者，承载定向可靠更新；每条记录只能发给对应 PlayerID，不能广播。
func (r *Room) RewardUpdates() <-chan game.RewardUpdateBatch { return r.rewards }
func (r *Room) Done() <-chan struct{}                        { return r.done }

func (r *Room) Stats() Stats {
	s := *r.status.Load()
	s.ControlQueueDepth, s.InputQueueDepth = len(r.controls), len(r.inputs)
	s.QueueRejections = r.rejected.Load()
	return s
}

// Close 幂等，并等待房间 goroutine 完成清理及所有待处理回执。
func (r *Room) Close() { r.cancel(); <-r.done }

func (r *Room) run() {
	// 房间使用固定 30Hz 节拍。Ticker 在系统繁忙时会合并/丢弃过期信号，服务端不会为了
	// “补帧”连续执行很多次物理运算，因此一次调度抖动不会造成模拟突然快进。
	ticker := time.NewTicker(game.TickInterval)
	defer ticker.Stop()
	defer r.emptyTimer.Stop()
	defer r.finish()
	for {
		select {
		case <-r.ctx.Done():
			r.stats.CloseReason = "requested"
			return
		case <-r.emptyTimer.C:
			r.stats.CloseReason = "idle"
			return
		case <-ticker.C:
			if r.ctx.Err() != nil {
				r.stats.CloseReason = "requested"
				return
			}
			// 调度延迟时使用真实服务端时间判断过期。Ticker 可丢失错过的节拍，移动不会无界补帧。
			now := time.Now()
			r.tick(now)
			if r.stats.CloseReason != "" {
				return
			}
		}
	}
}

func (r *Room) tick(now time.Time) {
	// 一个 Tick 固定分四步，答辩时可概括为：
	// 1. 先处理少量控制命令；2. 再消费玩家输入；3. 推进一步权威模拟；
	// 4. 发布事件、快照和监控采样。
	// ControlsPerTick/InputsPerTick 限制单帧工作量，防止请求洪峰拖垮 30Hz 主循环。
	sample := TickSample{RoomID: r.id, StartedAt: now}
	for range r.config.ControlsPerTick {
		select {
		case c := <-r.controls:
			switch {
			case c.stageResult != nil:
				completed, err := r.world.CompletedStage()
				c.stageResult <- StageResultReceipt{Result: completed.Clone(), Err: err}
				close(c.stageResult)
			case c.resumeState != nil:
				r.mu.Lock()
				member, joined := r.members[c.sessionID]
				r.mu.Unlock()
				if !joined {
					c.resumeState <- ResumeStateReceipt{Err: ErrNotJoined}
				} else {
					state, err := r.world.ResumeState(member.playerID)
					roomState := ResumeState{Snapshot: Snapshot{RoomID: r.id, Snapshot: state.Snapshot}, Reward: state.Reward}
					c.resumeState <- ResumeStateReceipt{State: roomState.clone(), Err: err}
				}
				close(c.resumeState)
			case c.gameResult != nil:
				result, err := r.world.GameResult(c.gameOutcome)
				c.gameResult <- GameResultReceipt{Result: result.Clone(), Err: err}
				close(c.gameResult)
			default:
				err := r.applyControl(c)
				c.result <- err
				close(c.result)
			}
			sample.Controls++
		default:
			goto inputs
		}
	}
inputs:
	for range r.config.InputsPerTick {
		select {
		case input := <-r.inputs:
			sample.Inputs++
			member, joined := r.members[input.sessionID]
			if !joined || member.generation != input.generation || now.Sub(input.received) >= r.config.World.InputTimeout {
				r.stats.RejectedInputs++
				continue
			}
			if err := r.world.ApplyInput(member.playerID, input.input, input.received); err != nil {
				r.stats.RejectedInputs++
			}
		default:
			goto simulate
		}
	}
simulate:
	// 到这里开始真正修改 World。本文件之外的 goroutine 永远不会调用 World.Step，
	// 所以移动、伤害、掉落、关卡状态天然按一个全序发生，无需给每个实体单独加锁。
	r.world.Step(time.Now())
	batch := r.world.TakeEvents()
	if batch.Overflow {
		r.stats.CloseReason = "event_backpressure"
	} else if len(batch.Events) > 0 {
		select {
		case r.events <- batch:
		default:
			r.stats.CloseReason = "event_backpressure"
		}
	}
	rewardBatch := r.world.TakeRewardUpdates()
	if rewardBatch.Overflow {
		r.stats.CloseReason = "event_backpressure"
	} else if len(rewardBatch.Updates) > 0 {
		select {
		case r.rewards <- rewardBatch:
		default:
			r.stats.CloseReason = "event_backpressure"
		}
	}
	if r.world.Tick()%game.SnapshotEvery == 0 {
		r.publish(false)
	}
	sample.ServerTick, sample.Players = r.world.Tick(), r.world.PlayerCount()
	sample.WorkDuration = time.Since(now)
	select {
	case r.samples <- sample:
	default:
		r.stats.DroppedTickSamples++
	}
	r.stats.LastTick = sample
	r.storeStats()
}

func (r *Room) applyControl(c control) error {
	if c.stagePlan != nil {
		return r.world.StartStage(*c.stagePlan)
	}
	if c.rewardStart != nil {
		return r.world.StartReward(c.rewardStart.catalog, c.rewardStart.seed, c.rewardStart.durationTicks)
	}
	if c.rewardChoice != 0 {
		r.mu.Lock()
		member, joined := r.members[c.sessionID]
		r.mu.Unlock()
		if !joined {
			return ErrNotJoined
		}
		return r.world.ChooseReward(member.playerID, c.rewardChoice)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.join {
		if member, exists := r.members[c.sessionID]; exists {
			if member.playerID == c.playerID {
				return nil
			}
			return ErrSessionBound
		}
		if err := r.world.AddPlayer(c.playerID); err != nil {
			return err
		}
		r.generation++
		r.members[c.sessionID] = binding{playerID: c.playerID, generation: r.generation}
		r.emptyTimer.Stop()
		return nil
	}
	if member, exists := r.members[c.sessionID]; exists {
		r.world.RemovePlayer(member.playerID)
		delete(r.members, c.sessionID)
		if r.world.PlayerCount() == 0 {
			r.emptyTimer.Reset(r.config.EmptyTimeout)
		}
	}
	return nil
}

func (r *Room) publish(closed bool) {
	s := Snapshot{RoomID: r.id, Closed: closed, Snapshot: r.world.Snapshot()}
	// atomic 发布给 LatestSnapshot/Admin 等旁路读者；Snapshot 内部已经深拷贝，
	// 读者修改自己的副本不会污染下一帧权威状态。
	r.latest.Store(&s)
	// updates 容量为 1，采用 latest-wins：消费者慢时先移除旧快照，再放入新快照。
	// 位置等连续状态允许跳过旧帧，不能反过来阻塞房间 Tick。
	select {
	case <-r.updates:
		r.stats.DroppedSnapshots++
	default:
	}
	r.updates <- s.clone()
}

func (r *Room) storeStats() {
	r.stats.ServerTick, r.stats.Players = r.world.Tick(), r.world.PlayerCount()
	s := r.stats
	r.status.Store(&s)
}

func (r *Room) finish() {
	// 先禁止新的命令入队，再清空队列并给每个 receipt 返回 ErrClosed。
	// 这一步很重要：否则等待“加入/开关/结算结果”的 goroutine 会永久卡住。
	r.cancel()
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	// 此时已禁止新命令入队，因此排空后不会再有命令被遗留。
	for {
		select {
		case c := <-r.controls:
			switch {
			case c.stageResult != nil:
				c.stageResult <- StageResultReceipt{Err: ErrClosed}
				close(c.stageResult)
			case c.resumeState != nil:
				c.resumeState <- ResumeStateReceipt{Err: ErrClosed}
				close(c.resumeState)
			case c.gameResult != nil:
				c.gameResult <- GameResultReceipt{Err: ErrClosed}
				close(c.gameResult)
			default:
				c.result <- ErrClosed
				close(c.result)
			}
		default:
			goto drainInputs
		}
	}
drainInputs:
	for {
		select {
		case <-r.inputs:
		default:
			goto cleared
		}
	}
cleared:
	r.world.Close()
	clear(r.members)
	r.stats.Closed = true
	r.publish(true)
	r.storeStats()
	close(r.updates)
	close(r.samples)
	close(r.events)
	close(r.rewards)
	close(r.done)
}
