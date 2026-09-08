package game

import (
	"errors"
	"math"
	"slices"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/systems"
)

// Player IDs use the low half; World allocates monsters/projectiles in the high
// half, monotonically and without reuse, to keep event references unambiguous.
const FirstWorldEntityID entity.ID = 1 << 63
const maxPendingEvents = 4096

var ErrStageState = errors.New("stage can only start in Waiting with living players")

type CombatConfig struct {
	PlayerStats                       entity.CombatStats
	ProjectileSpeed, ProjectileRadius float64
	ProjectileLifetimeTicks           uint32
	MaxMonsters, MaxProjectiles       int
}

func DefaultCombatConfig() CombatConfig {
	return CombatConfig{PlayerStats: entity.CombatStats{Attack: 20, MaxHealth: 100, MoveSpeed: 5, AttackCooldownTicks: 6},
		ProjectileSpeed: 20, ProjectileRadius: 0.1, ProjectileLifetimeTicks: 60, MaxMonsters: 64, MaxProjectiles: 256}
}

func (c CombatConfig) valid() bool {
	return c.PlayerStats.Valid() && !math.IsNaN(c.ProjectileSpeed) && !math.IsInf(c.ProjectileSpeed, 0) && c.ProjectileSpeed > 0 && c.ProjectileSpeed <= 1e4 &&
		!math.IsNaN(c.ProjectileRadius) && !math.IsInf(c.ProjectileRadius, 0) && c.ProjectileRadius > 0 && c.ProjectileRadius <= 10 &&
		c.ProjectileLifetimeTicks > 0 && c.ProjectileLifetimeTicks <= 18000 && c.MaxMonsters > 0 && c.MaxMonsters <= 128 && c.MaxProjectiles > 0 && c.MaxProjectiles <= 1024
}

type EventKind uint8

const (
	StageStarted EventKind = iota + 1
	ProjectileSpawned
	ProjectileDestroyed
	DamageDealt
	EntityDied
	StageCleared
	TeamDefeated
)

// Event is a domain value for A to map into reliable messages; it contains no
// protobuf references. Projectile state intentionally stays out of snapshots.
type Event struct {
	Kind                         EventKind
	ServerTick                   uint64
	EntityID, SourceID, TargetID entity.ID
	Position, Velocity           entity.Vec2
	Amount, Health               float64
	ExpiresAtTick                uint64
}

type EventBatch struct {
	Events   []Event
	Overflow bool
}
type MonsterView struct {
	ID                 entity.ID
	Position, Velocity entity.Vec2
	Health, MaxHealth  float64
	State              entity.MonsterState
}
type monsterState struct {
	monster    entity.Monster
	previous   entity.Vec2
	target     entity.ID
	nextAttack uint64
}

func orderedIDs[V any](m map[entity.ID]V) []entity.ID {
	ids := make([]entity.ID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// ValidateStage checks plan shape without accessing mutable World state, so
// Room may use it before copying and admitting the trusted server command.
func ValidateStage(plan stage.Plan, config Config) error {
	return plan.Validate(config.Min, config.Max, config.Combat.MaxMonsters)
}

func (w *World) StartStage(plan stage.Plan) error {
	if err := ValidateStage(plan, w.config); err != nil {
		return err
	}
	if w.stage.State != stage.Waiting || w.livingPlayers() == 0 {
		return ErrStageState
	}
	w.stage = stage.View{Index: plan.Index, Seed: plan.Seed, State: stage.Playing, MonstersRemaining: len(plan.Monsters)}
	for _, spawn := range plan.Monsters {
		id := w.allocateID()
		w.monsters[id] = &monsterState{monster: entity.Monster{ID: id, Position: spawn.Position, BaseStats: spawn.Stats, CurrentStats: spawn.Stats,
			Health: spawn.Stats.MaxHealth, Radius: spawn.Radius, AttackRange: spawn.AttackRange, State: entity.MonsterIdle}}
	}
	w.emit(Event{Kind: StageStarted, ServerTick: w.tick + 1})
	return nil
}

func (w *World) allocateID() entity.ID { w.nextEntity++; return FirstWorldEntityID | w.nextEntity }
func (w *World) emit(e Event) {
	if e.ServerTick == 0 {
		e.ServerTick = w.tick
	}
	if len(w.events) >= maxPendingEvents {
		w.eventOverflow = true
		return
	}
	w.events = append(w.events, e)
}

// TakeEvents transfers ownership of one batch. Failure to consume events is
// detectable and bounded; reliable transport must never silently drop Overflow.
func (w *World) TakeEvents() EventBatch {
	b := EventBatch{Events: w.events, Overflow: w.eventOverflow}
	w.events = nil
	w.eventOverflow = false
	return b
}

func (w *World) livingPlayers() int {
	n := 0
	for _, p := range w.players {
		if p.player.Alive {
			n++
		}
	}
	return n
}

func (w *World) stepCombat() {
	if w.stage.State != stage.Playing {
		return
	}
	playerIDs, monsterIDs := orderedIDs(w.players), orderedIDs(w.monsters)
	for _, id := range playerIDs {
		p := w.players[id]
		if !p.player.Alive || !p.firing || w.tick < p.nextShot || len(w.projectiles) >= w.config.Combat.MaxProjectiles {
			continue
		}
		p.nextShot = w.tick + uint64(p.player.CurrentStats.AttackCooldownTicks)
		c := w.config.Combat
		projectile := entity.Projectile{ID: w.allocateID(), OwnerID: id, Position: p.player.Position,
			Velocity: entity.Vec2{X: p.player.Aim.X * c.ProjectileSpeed, Y: p.player.Aim.Y * c.ProjectileSpeed},
			Attack:   p.player.CurrentStats.Attack, Radius: c.ProjectileRadius, ExpiresAtTick: w.tick + uint64(c.ProjectileLifetimeTicks)}
		w.projectiles[projectile.ID] = projectile
		w.emit(Event{Kind: ProjectileSpawned, EntityID: projectile.ID, SourceID: id, Position: projectile.Position, Velocity: projectile.Velocity, ExpiresAtTick: projectile.ExpiresAtTick})
	}
	var requests []systems.DamageRequest
	for _, id := range monsterIDs {
		m := w.monsters[id]
		m.previous = m.monster.Position
		if (w.tick-1)%SnapshotEvery == 0 {
			m.target = 0
			nearest := math.Inf(1)
			for _, pid := range playerIDs {
				p := w.players[pid].player
				if !p.Alive {
					continue
				}
				d := math.Hypot(p.Position.X-m.monster.Position.X, p.Position.Y-m.monster.Position.Y)
				if d < nearest {
					nearest = d
					m.target = pid
				}
			}
		}
		m.monster.Velocity = entity.Vec2{}
		p := w.players[m.target]
		if p == nil || !p.player.Alive {
			m.monster.State = entity.MonsterIdle
			continue
		}
		dx, dy := p.player.Position.X-m.monster.Position.X, p.player.Position.Y-m.monster.Position.Y
		distance := math.Hypot(dx, dy)
		if distance > m.monster.AttackRange {
			m.monster.State = entity.MonsterChase
			step := math.Min(m.monster.CurrentStats.MoveSpeed*StepSeconds, distance-m.monster.AttackRange)
			m.monster.Velocity = entity.Vec2{X: dx / distance * step / StepSeconds, Y: dy / distance * step / StepSeconds}
			m.monster.Position.X += m.monster.Velocity.X * StepSeconds
			m.monster.Position.Y += m.monster.Velocity.Y * StepSeconds
		} else {
			m.monster.State = entity.MonsterAttack
			if w.tick >= m.nextAttack {
				m.nextAttack = w.tick + uint64(m.monster.CurrentStats.AttackCooldownTicks)
				requests = append(requests, systems.DamageRequest{SourceID: id, TargetID: m.target, Attack: m.monster.CurrentStats.Attack, TargetPlayer: true})
			}
		}
	}
	// Swept collision tests the whole segment, so fast bullets cannot tunnel.
	for _, id := range orderedIDs(w.projectiles) {
		p := w.projectiles[id]
		if w.tick >= p.ExpiresAtTick {
			w.destroyProjectile(p)
			continue
		}
		end := entity.Vec2{X: p.Position.X + p.Velocity.X*StepSeconds, Y: p.Position.Y + p.Velocity.Y*StepSeconds}
		// Clip at the map exit before testing targets so an off-map segment
		// cannot hit an entity after the projectile should have disappeared.
		fraction := 1.0
		for _, axis := range [][4]float64{{p.Position.X, end.X, w.config.Min.X, w.config.Max.X}, {p.Position.Y, end.Y, w.config.Min.Y, w.config.Max.Y}} {
			if axis[1] < axis[2] {
				fraction = math.Min(fraction, (axis[2]-axis[0])/(axis[1]-axis[0]))
			}
			if axis[1] > axis[3] {
				fraction = math.Min(fraction, (axis[3]-axis[0])/(axis[1]-axis[0]))
			}
		}
		end = entity.Vec2{X: p.Position.X + (end.X-p.Position.X)*fraction, Y: p.Position.Y + (end.Y-p.Position.Y)*fraction}
		closest := math.Inf(1)
		var target entity.ID
		for _, mid := range monsterIDs {
			m := w.monsters[mid]
			centerEnd := entity.Vec2{X: m.previous.X + (m.monster.Position.X-m.previous.X)*fraction, Y: m.previous.Y + (m.monster.Position.Y-m.previous.Y)*fraction}
			relativeStart := entity.Vec2{X: p.Position.X - m.previous.X, Y: p.Position.Y - m.previous.Y}
			relativeEnd := entity.Vec2{X: end.X - centerEnd.X, Y: end.Y - centerEnd.Y}
			if hit, ok := systems.SegmentCircle(relativeStart, relativeEnd, entity.Vec2{}, p.Radius+m.monster.Radius); ok && hit < closest {
				closest = hit
				target = mid
			}
		}
		if target != 0 {
			p.Position = entity.Vec2{X: p.Position.X + (end.X-p.Position.X)*closest, Y: p.Position.Y + (end.Y-p.Position.Y)*closest}
			requests = append(requests, systems.DamageRequest{SourceID: p.OwnerID, TargetID: target, Attack: p.Attack})
			w.destroyProjectile(p)
		} else if fraction < 1 {
			p.Position = end
			w.destroyProjectile(p)
		} else {
			p.Position = end
			w.projectiles[id] = p
		}
	}
	for _, request := range requests {
		w.applyDamage(request)
	}
	for _, id := range monsterIDs {
		if w.monsters[id].monster.Health == 0 {
			delete(w.monsters, id)
		}
	}
	w.stage.MonstersRemaining = len(w.monsters)
	// A simultaneous final kill and team wipe is defeat; no rewards are granted.
	if w.livingPlayers() == 0 {
		w.stage.State = stage.Failed
		w.emit(Event{Kind: TeamDefeated})
	} else if len(w.monsters) == 0 {
		w.stage.State = stage.StageClear
		w.emit(Event{Kind: StageCleared})
	}
	if w.stage.State != stage.Playing {
		for _, m := range w.monsters {
			m.monster.Velocity = entity.Vec2{}
		}
		for _, id := range orderedIDs(w.projectiles) {
			w.destroyProjectile(w.projectiles[id])
		}
	}
}

func (w *World) destroyProjectile(p entity.Projectile) {
	delete(w.projectiles, p.ID)
	w.emit(Event{Kind: ProjectileDestroyed, EntityID: p.ID, SourceID: p.OwnerID, Position: p.Position})
}

func (w *World) applyDamage(request systems.DamageRequest) {
	var health, defense float64
	if request.TargetPlayer {
		p := w.players[request.TargetID]
		if p == nil || !p.player.Alive {
			return
		}
		health, defense = p.player.Health, p.player.CurrentStats.Defense
	} else {
		m := w.monsters[request.TargetID]
		if m == nil || m.monster.Health == 0 {
			return
		}
		health, defense = m.monster.Health, m.monster.CurrentStats.Defense
	}
	resolved, err := systems.ResolveDamage(request.Attack, defense, health)
	if err != nil {
		panic("validated combat state produced invalid damage")
	}
	if resolved.Amount == 0 {
		return
	}
	if request.TargetPlayer {
		p := w.players[request.TargetID]
		p.player.Health = resolved.RemainingHealth
		p.player.Alive = !resolved.Killed
		if resolved.Killed {
			p.player.Velocity = entity.Vec2{}
			p.firing = false
		}
	} else {
		m := w.monsters[request.TargetID]
		m.monster.Health = resolved.RemainingHealth
		if resolved.Killed {
			m.monster.State = entity.MonsterDead
		}
	}
	w.emit(Event{Kind: DamageDealt, SourceID: request.SourceID, TargetID: request.TargetID, Amount: resolved.Amount, Health: resolved.RemainingHealth})
	if resolved.Killed {
		w.emit(Event{Kind: EntityDied, EntityID: request.TargetID, SourceID: request.SourceID})
	}
}
