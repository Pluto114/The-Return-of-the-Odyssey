package entity

import "math"

// CombatStats keeps baseline and effective attributes separate. Equipment and
// modifiers can later derive CurrentStats without modifying BaseStats.
type CombatStats struct {
	Attack, Defense, MaxHealth, MoveSpeed float64
	AttackCooldownTicks                   uint32
}

func (s CombatStats) Valid() bool {
	for _, v := range []float64{s.Attack, s.Defense, s.MaxHealth, s.MoveSpeed} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e6 {
			return false
		}
	}
	return s.MaxHealth > 0 && s.AttackCooldownTicks > 0
}

type MonsterState uint8

const (
	MonsterIdle MonsterState = iota
	MonsterChase
	MonsterAttack
	MonsterDead
)

type Monster struct {
	ID                          ID
	Position, Velocity          Vec2
	BaseStats, CurrentStats     CombatStats
	Health, Radius, AttackRange float64
	State                       MonsterState
}

type Projectile struct {
	ID, OwnerID        ID
	Position, Velocity Vec2
	Attack, Radius     float64
	ExpiresAtTick      uint64
}
