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
}

func DefaultConfig() Config {
	return Config{World: game.DefaultConfig(), ControlCapacity: 64, InputCapacity: 256,
		ControlsPerTick: 16, InputsPerTick: 128, TickSampleCapacity: 128, EmptyTimeout: 5 * time.Second}
}

func (c Config) validate() error {
	if err := c.World.Validate(); err != nil {
		return err
	}
	if c.ControlCapacity < 1 || c.InputCapacity < 1 || c.ControlsPerTick < 1 || c.InputsPerTick < 1 ||
		c.TickSampleCapacity < 1 || c.EmptyTimeout <= 0 {
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
}

type control struct {
	join      bool
	sessionID SessionID
	playerID  entity.ID
	result    chan error
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
	latest   atomic.Pointer[Snapshot]
	status   atomic.Pointer[Stats]
	rejected atomic.Uint64

	world      *game.World
	members    map[SessionID]binding
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
		world: w, members: make(map[SessionID]binding), emptyTimer: time.NewTimer(config.EmptyTimeout), stats: Stats{RoomID: id}}
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
func (r *Room) Done() <-chan struct{}          { return r.done }

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
			return
		case <-r.emptyTimer.C:
			return
		case <-ticker.C:
			if r.ctx.Err() != nil {
				return
			}
			// Use actual server time for expiry if scheduling was delayed. The
			// ticker may drop missed ticks; movement never catches up unboundedly.
			now := time.Now()
			r.tick(now)
		}
	}
}

func (r *Room) tick(now time.Time) {
	sample := TickSample{RoomID: r.id, StartedAt: now}
	for range r.config.ControlsPerTick {
		select {
		case c := <-r.controls:
			err := r.applyControl(c)
			c.result <- err
			close(c.result)
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
			c.result <- ErrClosed
			close(c.result)
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
	r.world.Clear()
	clear(r.members)
	r.stats.Closed = true
	r.publish(true)
	r.storeStats()
	close(r.updates)
	close(r.samples)
	close(r.done)
}
