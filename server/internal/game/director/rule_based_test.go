package director_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func rulePlanner(t *testing.T) (director.RuleBasedPlanner, game.Config) {
	t.Helper()
	world := game.DefaultConfig()
	planner, err := director.NewRuleBasedPlanner(director.DefaultRuleConfig(world.Min, world.Max, world.Spawn, world.Combat.MaxMonsters))
	if err != nil {
		t.Fatal(err)
	}
	return planner, world
}

func firstPlan(t *testing.T, config game.Config) stage.Plan {
	t.Helper()
	plan, err := game.NewFirstStagePlan(config, 42)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestRuleBasedDirectorIsDeterministicPureAndBounded(t *testing.T) {
	planner, config := rulePlanner(t)
	previous := firstPlan(t, config)
	copyBefore := previous.Clone()
	high := director.PerformanceMetrics{ClearTimeSeconds: 20, TeamHPPercent: 0.9, AverageDPS: 60, DamageTaken: 10, EquipmentPower: 1}
	a, decisionA, err := planner.Decide(previous, high)
	if err != nil {
		t.Fatal(err)
	}
	b, decisionB, err := planner.Decide(previous, high)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(decisionA, decisionB) {
		t.Fatal("same director input produced different output")
	}
	if !reflect.DeepEqual(previous, copyBefore) {
		t.Fatal("director mutated previous plan")
	}
	if decisionA.Adjustment <= 0 || decisionA.Adjustment > director.MaxDifficultyIncrease+1e-12 {
		t.Fatalf("high-performance adjustment = %v", decisionA.Adjustment)
	}
	if a.Index != previous.Index+1 || a.Seed == previous.Seed || len(a.Monsters) != decisionA.MonsterCount {
		t.Fatalf("next plan metadata = %+v, decision = %+v", a, decisionA)
	}
	if err := a.Validate(config.Min, config.Max, config.Combat.MaxMonsters); err != nil {
		t.Fatal(err)
	}
	for _, monster := range a.Monsters {
		if monster.Position == config.Spawn {
			t.Fatal("director spawned a monster on the player spawn")
		}
	}

	low := director.PerformanceMetrics{ClearTimeSeconds: 90, TeamHPPercent: 0.2, AverageDPS: 5, DeathCount: 2, DamageTaken: 100, EquipmentPower: 1}
	_, lowDecision, err := planner.Decide(previous, low)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(lowDecision.Adjustment+director.MaxDifficultyDecrease) > 1e-12 {
		t.Fatalf("low-performance adjustment = %v, want -%v", lowDecision.Adjustment, director.MaxDifficultyDecrease)
	}
}

func TestRuleBasedDirectorGeneratesThreeValidStages(t *testing.T) {
	planner, config := rulePlanner(t)
	plan := firstPlan(t, config)
	metrics := director.PerformanceMetrics{ClearTimeSeconds: 45, TeamHPPercent: 0.6, AverageDPS: 30, DamageTaken: 50, EquipmentPower: 1}
	seenSeeds := map[int64]bool{plan.Seed: true}
	for wantIndex := uint32(2); wantIndex <= 4; wantIndex++ {
		next, err := planner.Generate(plan, metrics)
		if err != nil {
			t.Fatal(err)
		}
		if next.Index != wantIndex || seenSeeds[next.Seed] {
			t.Fatalf("stage %d metadata = index %d seed %d", wantIndex, next.Index, next.Seed)
		}
		seenSeeds[next.Seed] = true
		if err := next.Validate(config.Min, config.Max, config.Combat.MaxMonsters); err != nil {
			t.Fatal(err)
		}
		change := next.DifficultyScore/plan.DifficultyScore - 1
		if change > director.MaxDifficultyIncrease+1e-12 || change < -director.MaxDifficultyDecrease-1e-12 {
			t.Fatalf("stage %d difficulty change = %v", wantIndex, change)
		}
		plan = next
	}
}

func TestRuleBasedDirectorRejectsInvalidInputs(t *testing.T) {
	planner, config := rulePlanner(t)
	plan := firstPlan(t, config)
	valid := director.PerformanceMetrics{ClearTimeSeconds: 45, TeamHPPercent: 0.5, AverageDPS: 30, DamageTaken: 50, EquipmentPower: 1}
	for _, metrics := range []director.PerformanceMetrics{
		{},
		{ClearTimeSeconds: math.NaN(), TeamHPPercent: 0.5, EquipmentPower: 1},
		{ClearTimeSeconds: 1, TeamHPPercent: 2, EquipmentPower: 1},
		{ClearTimeSeconds: 1, TeamHPPercent: 0.5, DeathCount: -1, EquipmentPower: 1},
	} {
		if _, err := planner.Generate(plan, metrics); err == nil {
			t.Fatalf("invalid metrics accepted: %+v", metrics)
		}
	}
	badPlan := plan.Clone()
	badPlan.Monsters[0].Position = entity.Vec2{X: 99, Y: 99}
	if _, err := planner.Generate(badPlan, valid); err == nil {
		t.Fatal("invalid previous plan accepted")
	}
	badConfig := director.DefaultRuleConfig(config.Min, config.Max, config.Spawn, 0)
	if _, err := director.NewRuleBasedPlanner(badConfig); err == nil {
		t.Fatal("invalid director config accepted")
	}
}
