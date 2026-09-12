package game_test

import (
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func TestThreeStageServerCoreLoop(t *testing.T) {
	config := game.DefaultConfig()
	w, err := game.NewWorld(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	planner, err := director.NewRuleBasedPlanner(director.DefaultRuleConfig(config.Min, config.Max, config.Spawn, config.Combat.MaxMonsters))
	if err != nil {
		t.Fatal(err)
	}
	plan := stage.Plan{Index: 1, Seed: 42, DifficultyScore: 1, Monsters: []stage.Spawn{{
		Position: entity.Vec2{X: 11, Y: 10}, Radius: 0.4, AttackRange: 0,
		Stats: entity.CombatStats{MaxHealth: 20, AttackCooldownTicks: 30},
	}}}
	var seq uint32
	for stageNumber := uint32(1); stageNumber <= 3; stageNumber++ {
		if err := w.StartStage(plan); err != nil {
			t.Fatalf("start stage %d: %v", stageNumber, err)
		}
		for ticks := 0; w.Snapshot().Stage.State == stage.Playing && ticks < 600; ticks++ {
			snapshot := w.Snapshot()
			if len(snapshot.Monsters) == 0 {
				t.Fatal("playing stage has no monsters")
			}
			seq++
			aim := entity.Vec2{X: snapshot.Monsters[0].Position.X - snapshot.Players[0].Position.X, Y: snapshot.Monsters[0].Position.Y - snapshot.Players[0].Position.Y}
			now := time.Unix(100, 0).Add(time.Duration(w.Tick()) * game.TickInterval)
			if err := w.ApplyInput(1, game.Input{Seq: seq, Aim: aim, Shoot: true}, now); err != nil {
				t.Fatal(err)
			}
			w.Step(now)
			w.TakeEvents()
		}
		if snapshot := w.Snapshot(); snapshot.Stage.State != stage.StageClear || snapshot.Stage.Index != stageNumber {
			t.Fatalf("stage %d did not clear: %+v", stageNumber, snapshot)
		}
		completed, err := w.CompletedStage()
		if err != nil {
			t.Fatal(err)
		}
		if completed.Plan.Index != stageNumber || completed.Performance.ClearTimeSeconds <= 0 {
			t.Fatalf("stage %d result = %+v", stageNumber, completed)
		}
		if stageNumber == 3 {
			break
		}
		if err := w.StartReward(rewardCatalog(t), int64(100+stageNumber), 30); err != nil {
			t.Fatal(err)
		}
		offer := w.TakeRewardUpdates().Updates[0]
		if err := w.ChooseReward(1, offer.EquipmentIDs[0]); err != nil {
			t.Fatal(err)
		}
		w.TakeRewardUpdates()
		if w.Snapshot().Stage.State != stage.PreparingNextStage {
			t.Fatal("reward did not unlock next stage")
		}
		plan, err = planner.Generate(completed.Plan, completed.Performance)
		if err != nil {
			t.Fatal(err)
		}
	}
}
