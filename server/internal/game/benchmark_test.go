package game_test

import (
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func BenchmarkGameCore(b *testing.B) {
	b.Run("AI64", func(b *testing.B) {
		w := benchmarkWorld(b, false)
		now := time.Unix(100, 0)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			w.Step(now)
			w.TakeEvents()
		}
	})

	b.Run("Collision64x256", func(b *testing.B) {
		w := benchmarkWorld(b, true)
		now := time.Unix(100, 0)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			w.Step(now)
			w.TakeEvents()
		}
	})

	b.Run("Snapshot2x64", func(b *testing.B) {
		w := benchmarkWorld(b, false)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_ = w.Snapshot()
		}
	})

	b.Run("Director64", func(b *testing.B) {
		config := game.DefaultConfig()
		planner, err := director.NewRuleBasedPlanner(director.DefaultRuleConfig(config.Min, config.Max, config.Spawn, config.Combat.MaxMonsters))
		if err != nil {
			b.Fatal(err)
		}
		previous := benchmarkPlan(config.Combat.MaxMonsters)
		metrics := director.PerformanceMetrics{ClearTimeSeconds: 45, TeamHPPercent: 0.6, AverageDPS: 30, DamageTaken: 50, EquipmentPower: 1}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if _, _, err := planner.Decide(previous, metrics); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchmarkWorld(b *testing.B, withProjectiles bool) *game.World {
	b.Helper()
	config := game.DefaultConfig()
	config.Combat.PlayerStats.AttackCooldownTicks = 1
	config.Combat.ProjectileSpeed = 0.01
	config.Combat.ProjectileLifetimeTicks = 18000
	w, err := game.NewWorld(config)
	if err != nil {
		b.Fatal(err)
	}
	for _, id := range []entity.ID{1, 2} {
		if err := w.AddPlayer(id); err != nil {
			b.Fatal(err)
		}
	}
	if err := w.StartStage(benchmarkPlan(config.Combat.MaxMonsters)); err != nil {
		b.Fatal(err)
	}
	if withProjectiles {
		now := time.Unix(100, 0)
		for _, id := range []entity.ID{1, 2} {
			if err := w.ApplyInput(id, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}, now); err != nil {
				b.Fatal(err)
			}
		}
		for range config.Combat.MaxProjectiles / 2 {
			w.Step(now)
			w.TakeEvents()
		}
	}
	return w
}

func benchmarkPlan(count int) stage.Plan {
	monsters := make([]stage.Spawn, count)
	for i := range monsters {
		monsters[i] = stage.Spawn{
			Position: entity.Vec2{X: 19 - float64(i%8)*0.01, Y: 19 - float64(i/8)*0.01},
			Stats:    entity.CombatStats{MaxHealth: 10000, AttackCooldownTicks: 30},
			Radius:   0.1, AttackRange: 0,
		}
	}
	return stage.Plan{Index: 1, Seed: 42, DifficultyScore: 1, Monsters: monsters}
}
