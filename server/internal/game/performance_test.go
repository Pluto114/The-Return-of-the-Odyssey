package game_test

import (
	"errors"
	"math"
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func TestPerformanceMetricsFreezeAtStageClear(t *testing.T) {
	monster := target(10, 10, 40)
	monster.Stats.Attack = 30
	w := encounter(t, game.DefaultConfig(), monster)
	if _, err := w.PerformanceMetrics(); !errors.Is(err, game.ErrPerformanceUnavailable) {
		t.Fatalf("playing metrics returned %v", err)
	}
	for seq := uint32(1); seq <= 7; seq++ {
		stepInput(t, w, game.Input{Seq: seq, Aim: entity.Vec2{X: 1}, Shoot: true})
	}
	metrics, err := w.PerformanceMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(metrics.ClearTimeSeconds-7.0/30.0) > 1e-12 || math.Abs(metrics.AverageDPS-40/(7.0/30.0)) > 1e-9 ||
		metrics.TeamHPPercent != 0.7 || metrics.DamageTaken != 30 || metrics.DeathCount != 0 || metrics.EquipmentPower != 1 {
		t.Fatalf("performance metrics = %+v", metrics)
	}
	completed, err := w.CompletedStage()
	if err != nil || completed.Plan.Index != 1 || completed.Performance != metrics {
		t.Fatalf("completed stage = %+v err=%v", completed, err)
	}
	completed.Plan.Monsters[0].Stats.MaxHealth = 999
	again, err := w.CompletedStage()
	if err != nil || again.Plan.Monsters[0].Stats.MaxHealth != 40 {
		t.Fatal("completed stage plan aliased World")
	}
	frozen := metrics
	if err := w.StartReward(rewardCatalog(t), 2, 30); err != nil {
		t.Fatal(err)
	}
	w.TakeRewardUpdates()
	if err := w.ChooseReward(1, 1); err != nil {
		t.Fatal(err)
	}
	if metrics, err = w.PerformanceMetrics(); err != nil || metrics != frozen {
		t.Fatalf("reward changed frozen performance: %+v err=%v", metrics, err)
	}
	if err := w.StartStage(stage.Plan{Index: 2, DifficultyScore: 1, Monsters: []stage.Spawn{target(19, 19, 100)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.PerformanceMetrics(); !errors.Is(err, game.ErrPerformanceUnavailable) {
		t.Fatalf("new stage retained old metrics: %v", err)
	}
}

func TestPerformanceCountsDeadTeammateAndResolvedDamage(t *testing.T) {
	w, err := game.NewWorld(game.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []entity.ID{1, 2} {
		if err := w.AddPlayer(id); err != nil {
			t.Fatal(err)
		}
	}
	monster := target(10, 10, 20)
	monster.Stats.Attack = 1000 // 结算伤害限制为玩家的 100 点生命
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{monster}}); err != nil {
		t.Fatal(err)
	}
	stepInput(t, w, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true})
	metrics, err := w.PerformanceMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if metrics.DeathCount != 1 || metrics.DamageTaken != 100 || metrics.TeamHPPercent != 0.5 || metrics.AverageDPS != 600 {
		t.Fatalf("two-player metrics = %+v", metrics)
	}
}
