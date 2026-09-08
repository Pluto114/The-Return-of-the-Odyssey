// Package game implements deterministic authoritative movement without sockets,
// protobuf, database access, or background goroutines.
package game

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/systems"
)

const (
	TickRate      = 30
	SnapshotEvery = 3
	StepSeconds   = 1.0 / TickRate
	TickInterval  = time.Second / TickRate
)

var (
	ErrInvalidPlayer = errors.New("invalid player ID")
	ErrPlayerExists  = errors.New("player already exists")
	ErrPlayerMissing = errors.New("player not in world")
	ErrWorldFull     = errors.New("world is full")
	ErrInvalidInput  = errors.New("invalid movement input")
	ErrStaleInput    = errors.New("stale movement input")
)

type Config struct {
	Capacity     int
	Min, Max     entity.Vec2
	Spawn        entity.Vec2
	MoveSpeed    float64
	InputTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{Capacity: 2, Max: entity.Vec2{X: 20, Y: 20}, Spawn: entity.Vec2{X: 10, Y: 10},
		MoveSpeed: 5, InputTimeout: 200 * time.Millisecond}
}

func (c Config) Validate() error {
	if c.Capacity < 1 || !c.Min.Finite() || !c.Max.Finite() || !c.Spawn.Finite() ||
		c.Min.X >= c.Max.X || c.Min.Y >= c.Max.Y ||
		c.Spawn.X < c.Min.X || c.Spawn.X > c.Max.X || c.Spawn.Y < c.Min.Y || c.Spawn.Y > c.Max.Y ||
		math.IsNaN(c.MoveSpeed) || math.IsInf(c.MoveSpeed, 0) || c.MoveSpeed <= 0 || c.InputTimeout <= 0 {
		return fmt.Errorf("invalid world configuration")
	}
	return nil
}

// Input contains intent only. Session supplies the player identity and the
// server stamps arrival time; neither comes from a client-controlled position.
type Input struct {
	Seq       uint32
	Direction entity.Vec2
}

func (i Input) Validate() error {
	if i.Seq == 0 || !i.Direction.Finite() {
		return ErrInvalidInput
	}
	return nil
}

type Snapshot struct {
	ServerTick uint64
	Players    []entity.Player
}

func (s Snapshot) Clone() Snapshot {
	s.Players = slices.Clone(s.Players)
	return s
}

type playerState struct {
	player     entity.Player
	input      Input
	receivedAt time.Time
	pending    bool
}

// World is deliberately not concurrent. Only its Room goroutine may call its
// methods. Snapshot returns detached values suitable for publication.
type World struct {
	config  Config
	tick    uint64
	players map[entity.ID]*playerState
}

func NewWorld(config Config) (*World, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &World{config: config, players: make(map[entity.ID]*playerState)}, nil
}

func (w *World) AddPlayer(id entity.ID) error {
	if id == 0 {
		return ErrInvalidPlayer
	}
	if _, exists := w.players[id]; exists {
		return ErrPlayerExists
	}
	if len(w.players) >= w.config.Capacity {
		return ErrWorldFull
	}
	w.players[id] = &playerState{player: entity.Player{ID: id, Position: w.config.Spawn}}
	return nil
}

func (w *World) RemovePlayer(id entity.ID) { delete(w.players, id) }
func (w *World) Clear()                    { clear(w.players) }
func (w *World) PlayerCount() int          { return len(w.players) }
func (w *World) Tick() uint64              { return w.tick }

// ApplyInput stages the newest intent; it does not advance position or ack.
// receivedAt must be the trusted server arrival time, not a client timestamp.
func (w *World) ApplyInput(id entity.ID, input Input, receivedAt time.Time) error {
	if err := input.Validate(); err != nil {
		return err
	}
	p, exists := w.players[id]
	if !exists {
		return ErrPlayerMissing
	}
	if input.Seq <= p.input.Seq || receivedAt.Before(p.receivedAt) {
		return ErrStaleInput
	}
	input.Direction = systems.NormalizeDirection(input.Direction)
	p.input, p.receivedAt, p.pending = input, receivedAt, true
	return nil
}

// Step advances simulation exactly once, regardless of the number of received
// packets. now is used only for input expiry, never for movement dt.
func (w *World) Step(now time.Time) {
	for _, p := range w.players {
		direction := entity.Vec2{}
		fresh := p.input.Seq != 0 && !now.Before(p.receivedAt) && now.Sub(p.receivedAt) < w.config.InputTimeout
		if fresh {
			direction = p.input.Direction
			if p.pending {
				p.player.LastProcessedInputSeq = p.input.Seq
			}
		}
		if !now.Before(p.receivedAt) {
			p.pending = false
		}
		systems.Move(&p.player, direction, w.config.MoveSpeed, StepSeconds, w.config.Min, w.config.Max)
	}
	w.tick++
}

// Snapshot is full-state and deterministically ordered by player ID. Callers
// can mutate the returned slice without modifying the live world.
func (w *World) Snapshot() Snapshot {
	s := Snapshot{ServerTick: w.tick, Players: make([]entity.Player, 0, len(w.players))}
	for _, p := range w.players {
		s.Players = append(s.Players, p.player)
	}
	slices.SortFunc(s.Players, func(a, b entity.Player) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return s
}
