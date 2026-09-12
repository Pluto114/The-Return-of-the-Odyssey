package game_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func TestResumeStateRebuildsPrivateRewardState(t *testing.T) {
	w, err := game.NewWorld(game.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []entity.ID{1, 2} {
		if err := w.AddPlayer(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.StartStage(stage.Plan{Index: 1, Seed: 7, DifficultyScore: 1, Monsters: []stage.Spawn{target(11, 10, 20)}}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	if err := w.ApplyInput(1, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	w.TakeEvents()
	if err := w.StartReward(rewardCatalog(t), 44, 2); err != nil {
		t.Fatal(err)
	}
	w.TakeRewardUpdates()

	first, err := w.ResumeState(1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.ResumeState(2)
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot.Stage.State != stage.Reward || first.Reward == nil || first.Reward.Kind != game.RewardOptionsAvailable || first.Reward.PlayerID != 1 ||
		second.Reward == nil || second.Reward.PlayerID != 2 {
		t.Fatalf("private resume rewards = %+v / %+v", first, second)
	}
	original := first.Clone()
	first.Snapshot.Players[0].Health = -1
	first.Reward.EquipmentIDs[0] = 999
	again, err := w.ResumeState(1)
	if err != nil || again.Snapshot.Players[0].Health < 0 || again.Reward.EquipmentIDs[0] == 999 || !reflect.DeepEqual(again, original) {
		t.Fatalf("resume state aliased World: %+v err=%v", again, err)
	}

	selected := again.Reward.EquipmentIDs[0]
	if err := w.ChooseReward(1, selected); err != nil {
		t.Fatal(err)
	}
	applied, err := w.ResumeState(1)
	if err != nil || applied.Reward == nil || applied.Reward.Kind != game.RewardSelectionApplied || applied.Reward.EquipmentID != selected || applied.Reward.Defaulted {
		t.Fatalf("applied resume reward = %+v err=%v", applied, err)
	}
	pending, err := w.ResumeState(2)
	if err != nil || pending.Reward == nil || pending.Reward.Kind != game.RewardOptionsAvailable {
		t.Fatalf("teammate pending reward = %+v err=%v", pending, err)
	}
	for range 3 {
		w.Step(now)
		w.TakeRewardUpdates()
	}
	defaulted, err := w.ResumeState(2)
	if err != nil || defaulted.Reward == nil || defaulted.Reward.Kind != game.RewardSelectionApplied || !defaulted.Reward.Defaulted ||
		defaulted.Snapshot.Stage.State != stage.PreparingNextStage {
		t.Fatalf("defaulted resume reward = %+v err=%v", defaulted, err)
	}
	if _, err := w.ResumeState(99); !errors.Is(err, game.ErrPlayerMissing) {
		t.Fatalf("unknown resume player returned %v", err)
	}
}

func TestResumeStateReflectsDeathWithoutRevival(t *testing.T) {
	w := world(t)
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{{
		Position: entity.Vec2{X: 10, Y: 10}, Radius: 0.4, AttackRange: 1,
		Stats: entity.CombatStats{Attack: 1000, MaxHealth: 100, AttackCooldownTicks: 30},
	}}}); err != nil {
		t.Fatal(err)
	}
	w.Step(time.Unix(100, 0))
	resumed, err := w.ResumeState(1)
	if err != nil || len(resumed.Snapshot.Players) != 1 || resumed.Snapshot.Players[0].Alive || resumed.Snapshot.Players[0].Health != 0 ||
		resumed.Snapshot.Stage.State != stage.Failed {
		t.Fatalf("dead resume state = %+v err=%v", resumed, err)
	}
}

func TestGameResultIsDetachedAndTerminalTicksAreStable(t *testing.T) {
	w := world(t)
	if game.GameVictory.String() != "victory" || game.GameDefeat.String() != "defeat" || game.GameAbandoned.String() != "abandoned" || game.GameOutcome(99).String() != "unknown" {
		t.Fatal("game outcome labels changed")
	}
	if _, err := w.GameResult(game.GameVictory); !errors.Is(err, game.ErrGameResultState) {
		t.Fatalf("pre-run result returned %v", err)
	}
	if _, err := w.GameResult(game.GameOutcome(99)); !errors.Is(err, game.ErrInvalidGameOutcome) {
		t.Fatalf("invalid outcome returned %v", err)
	}
	if err := w.StartStage(stage.Plan{Index: 1, Seed: 7, DifficultyScore: 1, Monsters: []stage.Spawn{target(11, 10, 20)}}); err != nil {
		t.Fatal(err)
	}
	stepInput(t, w, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true})
	victory, err := w.GameResult(game.GameVictory)
	if err != nil {
		t.Fatal(err)
	}
	if victory.Outcome != game.GameVictory || victory.StartedAtTick != 0 || victory.EndedAtTick != 1 || victory.DurationTicks() != 1 ||
		victory.FinalStageIndex != 1 || len(victory.ClearedStages) != 1 || victory.ClearedStages[0].ClearTick != 1 || len(victory.Players) != 1 {
		t.Fatalf("victory result = %+v", victory)
	}
	victory.ClearedStages[0].Index = 999
	victory.Players[0].Health = -1
	again, err := w.GameResult(game.GameVictory)
	if err != nil || again.ClearedStages[0].Index != 1 || again.Players[0].Health < 0 {
		t.Fatalf("game result aliased World: %+v err=%v", again, err)
	}
	if _, err := w.GameResult(game.GameDefeat); !errors.Is(err, game.ErrGameResultState) {
		t.Fatalf("clear accepted defeat: %v", err)
	}

	if err := w.StartReward(rewardCatalog(t), 55, 30); err != nil {
		t.Fatal(err)
	}
	offer := w.TakeRewardUpdates().Updates[0]
	if err := w.ChooseReward(1, offer.EquipmentIDs[0]); err != nil {
		t.Fatal(err)
	}
	if err := w.StartStage(stage.Plan{Index: 2, Seed: 8, DifficultyScore: 1.1, Monsters: []stage.Spawn{{
		Position: entity.Vec2{X: 10, Y: 10}, Radius: 0.4, AttackRange: 1,
		Stats: entity.CombatStats{Attack: 1000, MaxHealth: 100, AttackCooldownTicks: 30},
	}}}); err != nil {
		t.Fatal(err)
	}
	abandoned, err := w.GameResult(game.GameAbandoned)
	if err != nil || abandoned.Outcome != game.GameAbandoned || abandoned.FinalStageIndex != 2 {
		t.Fatalf("active abandonment = %+v err=%v", abandoned, err)
	}
	for steps := 0; w.Snapshot().Stage.State == stage.Playing && steps < game.AIDecisionEvery+1; steps++ {
		w.Step(time.Unix(200, 0))
		w.TakeEvents()
	}
	defeat, err := w.GameResult(game.GameDefeat)
	if err != nil {
		t.Fatal(err)
	}
	if defeat.Outcome != game.GameDefeat || defeat.FinalStageIndex != 2 || defeat.EndedAtTick != w.Tick() || len(defeat.ClearedStages) != 1 ||
		len(defeat.Players) != 1 || defeat.Players[0].Alive || defeat.Players[0].Health != 0 || defeat.Players[0].Equipment == (entity.EquipmentState{}) {
		t.Fatalf("defeat result = %+v", defeat)
	}
	w.Step(time.Unix(300, 0))
	later, err := w.GameResult(game.GameDefeat)
	if err != nil || later.EndedAtTick != defeat.EndedAtTick || later.DurationTicks() != defeat.DurationTicks() {
		t.Fatalf("defeat terminal tick moved: before=%+v after=%+v err=%v", defeat, later, err)
	}
}
