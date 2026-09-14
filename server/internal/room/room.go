// Package room owns each world's single writer and exposes bounded, nonblocking
// command admission to session/lobby callers. It never performs network I/O.
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

// TickSample measures command consumption, simulation and snapshot publication;
// waiting for the next tick and delivery of this sample are excluded.
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
	ready        bool
	readiness    chan ReadinessReceipt
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

// ReadinessReceipt reports the next-stage barrier state on the owner
// goroutine. Online counts the currently bound sessions; Ready counts how many
// of them have signalled readiness for the next stage.
type ReadinessReceipt struct {
	Online int
	Ready  int
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

// Room must not be copied. Only run mutates world, members and the actor's stats.
// mu protects admission, binding lookup and shutdown; callers never access World.
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
	ready      map[SessionID]struct{}
	generation uint64
	emptyTimer *time.Timer
	stats      Stats
}

// Start immediately starts one owner goroutine. Call Close or cancel the parent
// context on teardown. A never-joined room also expires after EmptyTimeout.
func Start(ctx context.Context, id ID, config Config) (*Room, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid room ID")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := game.NewWorld(config.World)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &Room{id: id, config: config, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		controls: make(chan control, config.ControlCapacity), inputs: make(chan movement, config.InputCapacity),
		updates: make(chan Snapshot, 1), samples: make(chan TickSample, config.TickSampleCapacity),
		events:  make(chan game.EventBatch, config.EventCapacity),
		rewards: make(chan game.RewardUpdateBatch, config.EventCapacity),
		world:   w, members: make(map[SessionID]binding), ready: make(map[SessionID]struct{}), emptyTimer: time.NewTimer(config.EmptyTimeout), stats: Stats{RoomID: id}}
	s := Snapshot{RoomID: id, Snapshot: w.Snapshot()}
	r.latest.Store(&s)
	r.storeStats()
	go r.run()
	return r, nil
}

// Join enqueues a trusted Session -> Player binding. The returned receipt yields
// exactly one result and closes. Only a nil RECEIPT result means the player has
// joined; a nil admission error merely means the command was queued.
// Repeating the same binding is idempotent. IDs are assigned by the server.
func (r *Room) Join(sessionID SessionID, playerID entity.ID) (<-chan error, error) {
	if sessionID == 0 {
		return nil, ErrInvalidSession
	}
	if playerID == 0 {
		return nil, game.ErrInvalidPlayer
	}
	return r.submit(control{join: true, sessionID: sessionID, playerID: playerID})
}

// Leave is idempotent and uses the lifecycle queue, independently of input load.
// If admission returns ErrQueueFull, session cleanup must retry until accepted
// or Done closes; it must not silently discard the disconnect command.
func (r *Room) Leave(sessionID SessionID) (<-chan error, error) {
	if sessionID == 0 {
		return nil, ErrInvalidSession
	}
	return r.submit(control{sessionID: sessionID})
}

// StartStage is a trusted server orchestration command, never a direct client
// request. The plan is copied before enqueue, and success is reported by receipt.
func (r *Room) StartStage(plan stage.Plan) (<-chan error, error) {
	if err := game.ValidateStage(plan, r.config.World); err != nil {
		return nil, err
	}
	plan = plan.Clone()
	return r.submit(control{stagePlan: &plan})
}

// StartReward moves a cleared stage into its server-owned reward round. The
// catalog must have been loaded and validated outside the Room tick.
func (r *Room) StartReward(catalog equipment.Catalog, seed int64, durationTicks uint64) (<-chan error, error) {
	return r.submit(control{rewardStart: &rewardStart{catalog: catalog, seed: seed, durationTicks: durationTicks}})
}

// ChooseReward resolves player identity from the trusted Session binding. A
// client can submit only an equipment ID; World validates its private offer.
func (r *Room) ChooseReward(sessionID SessionID, equipmentID equipment.ID) (<-chan error, error) {
	if sessionID == 0 {
		return nil, ErrInvalidSession
	}
	if equipmentID == 0 {
		return nil, equipment.ErrUnknownEquipment
	}
	return r.submit(control{sessionID: sessionID, rewardChoice: equipmentID})
}

// Ready marks a session as ready for the next stage. It is idempotent: a
// duplicate Ready from the same session is a no-op. The server advances only
// when every bound session is ready and the reward round is complete; the
// barrier is reset whenever a new stage starts.
func (r *Room) Ready(sessionID SessionID) (<-chan error, error) {
	if sessionID == 0 {
		return nil, ErrInvalidSession
	}
	return r.submit(control{sessionID: sessionID, ready: true})
}

// Readiness snapshots the next-stage barrier on the owner goroutine: how many
// sessions are bound and how many of them have signalled readiness. It is a
// non-mutating receipt query for the stage orchestration layer.
func (r *Room) Readiness() (<-chan ReadinessReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return nil, ErrClosed
	}
	receipt := make(chan ReadinessReceipt, 1)
	select {
	case r.controls <- control{readiness: receipt}:
		return receipt, nil
	default:
		r.rejected.Add(1)
		return nil, ErrQueueFull
	}
}

// CompletedStage reads the immutable plan and frozen metrics through the Room
// owner. Director orchestration can use the result without accessing World.
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

// ResumeState reconstructs a full authoritative snapshot and this player's
// private reward state on the owner goroutine. Token validation and connection
// replacement happen before this call in A/D; the SessionID itself is retained.
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

// GameResult returns a detached terminal value for asynchronous persistence.
// The World validates the trusted outcome against its current stage state.
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

// Input validates shape and enqueues intent without blocking for a tick. The
// actor resolves player identity from the session binding and validates order.
// A successful enqueue is NOT an acknowledgement; inspect snapshots for ack.
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

// Snapshots has one consumer (A's replication dispatcher). Each value is an
// owned copy. A slow consumer loses old snapshots, never stalls simulation.
func (r *Room) Snapshots() <-chan Snapshot { return r.updates }
func (r *Room) LatestSnapshot() Snapshot   { return r.latest.Load().clone() }

// TickSamples has one consumer (D's metrics adapter). It is lossy and bounded;
// consumers must report DroppedTickSamples when interpreting percentiles.
func (r *Room) TickSamples() <-chan TickSample { return r.samples }

// Events is for one reliable-event dispatcher. Saturation closes the room and
// records event_backpressure; combat events are never silently replaced.
func (r *Room) Events() <-chan game.EventBatch { return r.events }

// RewardUpdates has one consumer and carries targeted reliable updates. A
// must deliver each row only to its PlayerID rather than broadcasting it.
func (r *Room) RewardUpdates() <-chan game.RewardUpdateBatch { return r.rewards }
func (r *Room) Done() <-chan struct{}                        { return r.done }

func (r *Room) Stats() Stats {
	s := *r.status.Load()
	s.ControlQueueDepth, s.InputQueueDepth = len(r.controls), len(r.inputs)
	s.QueueRejections = r.rejected.Load()
	return s
}

// Close is idempotent and waits for owner cleanup, including pending receipts.
func (r *Room) Close() { r.cancel(); <-r.done }

func (r *Room) run() {
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
			// Use actual server time for expiry if scheduling was delayed. The
			// ticker may drop missed ticks; movement never catches up unboundedly.
			now := time.Now()
			r.tick(now)
			if r.stats.CloseReason != "" {
				return
			}
		}
	}
}

func (r *Room) tick(now time.Time) {
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
			case c.readiness != nil:
				c.readiness <- ReadinessReceipt{Online: len(r.members), Ready: len(r.ready)}
				close(c.readiness)
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
		// A new stage begins: reset the next-stage ready barrier so players
		// must signal readiness afresh for the following stage.
		clear(r.ready)
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
	if c.ready {
		if _, joined := r.members[c.sessionID]; !joined {
			return ErrNotJoined
		}
		r.ready[c.sessionID] = struct{}{}
		return nil
	}
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
		delete(r.ready, c.sessionID)
		if r.world.PlayerCount() == 0 {
			r.emptyTimer.Reset(r.config.EmptyTimeout)
		}
	}
	return nil
}

func (r *Room) publish(closed bool) {
	s := Snapshot{RoomID: r.id, Closed: closed, Snapshot: r.world.Snapshot()}
	r.latest.Store(&s)
	// Only this goroutine sends. A consumer may drain between the two selects,
	// but after the drain there is always room for the replacement.
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
	r.cancel()
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	// Admission is now closed, so no command can be stranded after this drain.
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
		case c.readiness != nil:
			c.readiness <- ReadinessReceipt{}
			close(c.readiness)
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
