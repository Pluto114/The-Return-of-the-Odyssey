package game

import (
	"fmt"
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

const firstStageMonsterCount = 3

// NewFirstStagePlan builds the server-owned opening encounter. The supplied
// seed selects and orders positions from a fixed arena-relative layout, so a
// room can reproduce the exact plan without using process-global randomness.
func NewFirstStagePlan(config Config, seed int64) (stage.Plan, error) {
	if err := config.Validate(); err != nil {
		return stage.Plan{}, err
	}
	if config.Combat.MaxMonsters < firstStageMonsterCount {
		return stage.Plan{}, fmt.Errorf("first stage requires capacity for %d monsters", firstStageMonsterCount)
	}

	width, height := config.Max.X-config.Min.X, config.Max.Y-config.Min.Y
	fractions := [...]entity.Vec2{
		{X: 0.15, Y: 0.15},
		{X: 0.50, Y: 0.15},
		{X: 0.85, Y: 0.15},
		{X: 0.85, Y: 0.50},
		{X: 0.85, Y: 0.85},
		{X: 0.50, Y: 0.85},
		{X: 0.15, Y: 0.85},
		{X: 0.15, Y: 0.50},
	}
	candidates := make([]entity.Vec2, len(fractions))
	for i, point := range fractions {
		candidates[i] = entity.Vec2{X: config.Min.X + width*point.X, Y: config.Min.Y + height*point.Y}
	}

	state := uint64(seed)
	start := int(nextSplitMix64(&state) % uint64(len(candidates)))
	step := 3
	if nextSplitMix64(&state)&1 != 0 {
		step = 5
	}

	// Prefer positions away from the shared player spawn. The fallback keeps
	// the builder valid for unusually small or custom arenas.
	minimumDistance := math.Min(width, height) * 0.20
	ordered := make([]entity.Vec2, 0, len(candidates))
	deferred := make([]entity.Vec2, 0, len(candidates))
	for i := range candidates {
		position := candidates[(start+i*step)%len(candidates)]
		if math.Hypot(position.X-config.Spawn.X, position.Y-config.Spawn.Y) >= minimumDistance {
			ordered = append(ordered, position)
		} else {
			deferred = append(deferred, position)
		}
	}
	ordered = append(ordered, deferred...)

	plan := stage.Plan{Index: 1, Seed: seed, DifficultyScore: 1, Monsters: make([]stage.Spawn, firstStageMonsterCount)}
	monsterStats := entity.CombatStats{Attack: 8, MaxHealth: 40, MoveSpeed: 1.5, AttackCooldownTicks: TickRate}
	for i := range plan.Monsters {
		plan.Monsters[i] = stage.Spawn{
			Position:    ordered[i],
			Stats:       monsterStats,
			Radius:      0.4,
			AttackRange: 1,
		}
	}
	if err := ValidateStage(plan, config); err != nil {
		return stage.Plan{}, fmt.Errorf("generated first stage: %w", err)
	}
	return plan, nil
}

func nextSplitMix64(state *uint64) uint64 {
	*state += 0x9e3779b97f4a7c15
	z := *state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
