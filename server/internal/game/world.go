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
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/reward"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/systems"
)

const (
	TickRate        = 30
	SnapshotEvery   = 3
	AIDecisionRate  = 10
	AIDecisionEvery = TickRate / AIDecisionRate
	StepSeconds     = 1.0 / TickRate
	TickInterval    = time.Second / TickRate
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
	Combat       CombatConfig
}

func DefaultConfig() Config {
	return Config{Capacity: 2, Max: entity.Vec2{X: 20, Y: 20}, Spawn: entity.Vec2{X: 10, Y: 10},
		MoveSpeed: 5, InputTimeout: 200 * time.Millisecond, Combat: DefaultCombatConfig()}
}

func (c Config) Validate() error {
	if c.Capacity < 1 || !c.Min.Finite() || !c.Max.Finite() || !c.Spawn.Finite() ||
		c.Min.X >= c.Max.X || c.Min.Y >= c.Max.Y ||
		math.Abs(c.Min.X) > 1e6 || math.Abs(c.Min.Y) > 1e6 || math.Abs(c.Max.X) > 1e6 || math.Abs(c.Max.Y) > 1e6 ||
		c.Spawn.X < c.Min.X || c.Spawn.X > c.Max.X || c.Spawn.Y < c.Min.Y || c.Spawn.Y > c.Max.Y ||
		math.IsNaN(c.MoveSpeed) || math.IsInf(c.MoveSpeed, 0) || c.MoveSpeed <= 0 || c.MoveSpeed > 1e6 || c.InputTimeout <= 0 || !c.Combat.valid() {
		return fmt.Errorf("invalid world configuration")
	}
	return nil
}

// Input contains intent only. Session supplies the player identity and the
// server stamps arrival time; neither comes from a client-controlled position.
type Input struct {
	Seq       uint32
	Direction entity.Vec2
	Aim       entity.Vec2
	Shoot     bool
	UsePotion bool
}

func (i Input) Validate() error {
	if i.Seq == 0 || !i.Direction.Finite() || !i.Aim.Finite() || (i.Shoot && i.Aim.X == 0 && i.Aim.Y == 0) {
		return ErrInvalidInput
	}
	return nil
}

type Snapshot struct {
	ServerTick uint64
	Players    []entity.Player
	Monsters   []MonsterView
	Stage      stage.View
}

func (s Snapshot) Clone() Snapshot {
	s.Players = slices.Clone(s.Players)
	s.Monsters = slices.Clone(s.Monsters)
	return s
}

type playerState struct {
	player     entity.Player
	input      Input
	receivedAt time.Time
	pending    bool
	nextShot   uint64
	firing     bool
	loadout    equipment.Loadout
}

// World is deliberately not concurrent. Only its Room goroutine may call its
// methods. Snapshot returns detached values suitable for publication.
type World struct {
	config         Config
	tick           uint64
	players        map[entity.ID]*playerState
	monsters       map[entity.ID]*monsterState
	projectiles    map[entity.ID]entity.Projectile
	nextEntity     entity.ID
	stage          stage.View
	events         []Event
	eventOverflow  bool
	rewardRound    *reward.Round
	rewardCatalog  equipment.Catalog
	rewardUpdates  []RewardUpdate
	rewardOverflow bool
}

func NewWorld(config Config) (*World, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &World{config: config, players: make(map[entity.ID]*playerState), monsters: make(map[entity.ID]*monsterState), projectiles: make(map[entity.ID]entity.Projectile)}, nil
}

func (w *World) AddPlayer(id entity.ID) error {
	if id == 0 || id >= FirstWorldEntityID {
		return ErrInvalidPlayer
	}
	if _, exists := w.players[id]; exists {
		return ErrPlayerExists
	}
	if len(w.players) >= w.config.Capacity {
		return ErrWorldFull
	}
	if w.stage.State != stage.Waiting {
		return ErrStageState
	}
	stats := w.config.Combat.PlayerStats
	stats.MoveSpeed = w.config.MoveSpeed
	w.players[id] = &playerState{player: entity.Player{ID: id, Position: w.config.Spawn, BaseStats: stats, CurrentStats: stats, Health: stats.MaxHealth, Alive: true, Aim: entity.Vec2{X: 1}}}
	return nil
}

func (w *World) RemovePlayer(id entity.ID) {
	delete(w.players, id)
	if w.rewardRound != nil && w.rewardRound.RemovePlayer(id) && w.stage.State == stage.Reward && w.PlayerCount() > 0 && w.rewardRound.Complete() {
		w.stage.State = stage.PreparingNextStage
	}
}
func (w *World) Clear() {
	clear(w.players)
	clear(w.monsters)
	clear(w.projectiles)
	w.events = nil
	w.eventOverflow = false
	w.rewardRound = nil
	w.rewardCatalog = equipment.Catalog{}
	w.rewardUpdates = nil
	w.rewardOverflow = false
	w.stage = stage.View{}
}
func (w *World) Close()           { w.Clear(); w.stage.State = stage.Closed }
func (w *World) PlayerCount() int { return len(w.players) }
func (w *World) Tick() uint64     { return w.tick }

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
		p.firing = false
		if !p.player.Alive {
			p.player.Velocity = entity.Vec2{}
			continue
		}
		direction := entity.Vec2{}
		fresh := p.input.Seq != 0 && !now.Before(p.receivedAt) && now.Sub(p.receivedAt) < w.config.InputTimeout
		if fresh {
			if w.stage.State == stage.Waiting || w.stage.State == stage.Playing {
				direction = p.input.Direction
			}
			p.firing = w.stage.State == stage.Playing && p.input.Shoot
			if p.input.Aim.X != 0 || p.input.Aim.Y != 0 {
				p.player.Aim = systems.UnitDirection(p.input.Aim)
			}
			if p.pending {
				p.player.LastProcessedInputSeq = p.input.Seq
				if w.stage.State == stage.Playing && p.input.UsePotion {
					w.usePotion(p)
				}
			}
		}
		if !now.Before(p.receivedAt) {
			p.pending = false
		}
		systems.Move(&p.player, direction, p.player.CurrentStats.MoveSpeed, StepSeconds, w.config.Min, w.config.Max)
	}
	w.tick++
	w.stepCombat()
	w.stepReward()
}

// Snapshot is full-state and deterministically ordered by player ID. Callers
// can mutate the returned slice without modifying the live world.
func (w *World) Snapshot() Snapshot {
	s := Snapshot{ServerTick: w.tick, Players: make([]entity.Player, 0, len(w.players)), Stage: w.stage}
	for _, id := range orderedIDs(w.monsters) {
		m := w.monsters[id].monster
		s.Monsters = append(s.Monsters, MonsterView{ID: m.ID, Position: m.Position, Velocity: m.Velocity, Health: m.Health, MaxHealth: m.CurrentStats.MaxHealth, State: m.State})
	}
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
