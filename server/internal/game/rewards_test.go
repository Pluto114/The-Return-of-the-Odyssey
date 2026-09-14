package game_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/reward"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func rewardCatalog(t *testing.T) equipment.Catalog {
	t.Helper()
	catalog, err := equipment.NewCatalog(1, []equipment.Definition{
		{ID: 1, Key: "weapon", Name: "Weapon", Slot: equipment.Weapon, Modifiers: []equipment.Modifier{{Stat: equipment.Attack, Operation: equipment.Add, Value: 5}}},
		{ID: 2, Key: "relic", Name: "Relic", Slot: equipment.Relic, Modifiers: []equipment.Modifier{{Stat: equipment.MaxHealth, Operation: equipment.Add, Value: 25}}},
		{ID: 3, Key: "potion", Name: "Potion", Slot: equipment.Potion, Heal: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func clearedWorld(t *testing.T, players ...entity.ID) *game.World {
	t.Helper()
	w, err := game.NewWorld(game.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range players {
		if err := w.AddPlayer(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.StartStage(stage.Plan{Index: 1, Seed: 10, DifficultyScore: 1, Monsters: []stage.Spawn{target(11, 10, 20)}}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	if err := w.ApplyInput(players[0], game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	w.TakeEvents()
	if w.Snapshot().Stage.State != stage.StageClear {
		t.Fatal("test setup did not clear the stage")
	}
	return w
}

func TestWorldRewardChoiceAppliesStatsAndUnlocksNextStage(t *testing.T) {
	w := clearedWorld(t, 1, 2)
	if err := w.StartReward(rewardCatalog(t), 77, 30); err != nil {
		t.Fatal(err)
	}
	updates := w.TakeRewardUpdates()
	if updates.Overflow || len(updates.Updates) != 2 {
		t.Fatalf("reward options update = %+v", updates)
	}
	for i, update := range updates.Updates {
		if update.Kind != game.RewardOptionsAvailable || update.PlayerID != entity.ID(i+1) || len(update.EquipmentIDs) != 3 || update.DeadlineTick != 31 {
			t.Fatalf("option update %d = %+v", i, update)
		}
	}
	if w.Snapshot().Stage.State != stage.Reward {
		t.Fatal("world did not enter Reward")
	}
	if err := w.ChooseReward(1, 99); !errors.Is(err, reward.ErrChoiceNotOffered) {
		t.Fatalf("unoffered reward returned %v", err)
	}
	if err := w.ChooseReward(1, 1); err != nil {
		t.Fatal(err)
	}
	first := w.TakeRewardUpdates()
	if len(first.Updates) != 1 || first.Updates[0].Kind != game.RewardSelectionApplied || first.Updates[0].EquipmentID != 1 || first.Updates[0].Defaulted {
		t.Fatalf("first apply update = %+v", first)
	}
	snapshot := w.Snapshot()
	if snapshot.Stage.State != stage.Reward || snapshot.Players[0].Equipment.WeaponID != 1 || snapshot.Players[0].CurrentStats.Attack != 25 {
		t.Fatalf("first reward did not apply: %+v", snapshot)
	}
	if err := w.ChooseReward(1, 2); !errors.Is(err, reward.ErrChoiceAlreadyMade) {
		t.Fatalf("duplicate reward returned %v", err)
	}
	if err := w.ChooseReward(2, 2); err != nil {
		t.Fatal(err)
	}
	snapshot = w.Snapshot()
	if snapshot.Stage.State != stage.PreparingNextStage || snapshot.Players[1].Equipment.RelicID != 2 || snapshot.Players[1].CurrentStats.MaxHealth != 125 || snapshot.Players[1].Health != 100 {
		t.Fatalf("reward completion = %+v", snapshot)
	}
	if err := w.StartReward(rewardCatalog(t), 77, 30); !errors.Is(err, game.ErrRewardState) {
		t.Fatalf("duplicate reward start returned %v", err)
	}
	nextPlan := stage.Plan{Index: 1, Seed: 11, DifficultyScore: 1, Monsters: []stage.Spawn{target(19, 19, 100)}}
	if err := w.StartStage(nextPlan); !errors.Is(err, game.ErrStageIndex) {
		t.Fatalf("non-incrementing stage returned %v", err)
	}
	nextPlan.Index = 2
	if err := w.StartStage(nextPlan); err != nil {
		t.Fatal(err)
	}
	if snapshot = w.Snapshot(); snapshot.Stage.State != stage.Playing || snapshot.Stage.Index != 2 || snapshot.Players[0].Equipment.WeaponID != 1 {
		t.Fatalf("next stage did not preserve equipment: %+v", snapshot)
	}
}

func TestWorldRewardTimeoutDefaultsAfterInclusiveDeadline(t *testing.T) {
	w := clearedWorld(t, 1)
	if err := w.StartReward(rewardCatalog(t), 9, 2); err != nil {
		t.Fatal(err)
	}
	offer := w.TakeRewardUpdates().Updates[0]
	for wantTick := uint64(2); wantTick <= 3; wantTick++ {
		w.Step(time.Unix(100, 0))
		if w.Tick() != wantTick || len(w.TakeRewardUpdates().Updates) != 0 || w.Snapshot().Stage.State != stage.Reward {
			t.Fatalf("reward defaulted before deadline at tick %d", w.Tick())
		}
	}
	w.Step(time.Unix(100, 0))
	updates := w.TakeRewardUpdates()
	if len(updates.Updates) != 1 || !updates.Updates[0].Defaulted || updates.Updates[0].EquipmentID != offer.EquipmentIDs[0] {
		t.Fatalf("timeout update = %+v, offer = %+v", updates, offer)
	}
	if w.Snapshot().Stage.State != stage.PreparingNextStage {
		t.Fatal("default selection did not finish reward round")
	}
}

func TestWorldRejectsUnsafeRewardCatalogWithoutMutation(t *testing.T) {
	w := clearedWorld(t, 1)
	unsafe, err := equipment.NewCatalog(1, []equipment.Definition{{
		ID: 1, Key: "unsafe", Name: "Unsafe", Slot: equipment.Weapon,
		Modifiers: []equipment.Modifier{{Stat: equipment.Attack, Operation: equipment.Add, Value: -100}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.StartReward(unsafe, 1, 30); !errors.Is(err, equipment.ErrInvalidStats) {
		t.Fatalf("unsafe catalog returned %v", err)
	}
	if w.Snapshot().Stage.State != stage.StageClear || len(w.TakeRewardUpdates().Updates) != 0 {
		t.Fatal("failed reward start mutated world")
	}
}

func TestPotionInputConsumesOnceOnNextStage(t *testing.T) {
	w, err := game.NewWorld(game.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	monster := target(10, 10, 20)
	monster.Stats.Attack = 30
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{monster}}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	if err := w.ApplyInput(1, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	w.TakeEvents()
	if got := w.Snapshot().Players[0].Health; got != 70 {
		t.Fatalf("test setup health = %v, want 70", got)
	}
	if err := w.StartReward(rewardCatalog(t), 3, 30); err != nil {
		t.Fatal(err)
	}
	w.TakeRewardUpdates()
	if err := w.ChooseReward(1, 3); err != nil {
		t.Fatal(err)
	}
	w.TakeRewardUpdates()
	if err := w.StartStage(stage.Plan{Index: 2, DifficultyScore: 1, Monsters: []stage.Spawn{target(19, 19, 100)}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := w.ApplyInput(1, game.Input{Seq: 2, UsePotion: true}, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	player := w.Snapshot().Players[0]
	if player.Health != 100 || player.Equipment.PotionID != 0 {
		t.Fatalf("potion input result = %+v", player)
	}
	if err := w.ApplyInput(1, game.Input{Seq: 3, UsePotion: true}, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	w.Step(now.Add(time.Millisecond))
	player = w.Snapshot().Players[0]
	if player.Health != 100 || player.Equipment.PotionID != 0 {
		t.Fatal("second potion input changed state")
	}
}

func TestNextStageRevivesDeadTeammateAtHalfHealth(t *testing.T) {
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
	monster.Stats.Attack = 100
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{monster}}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	if err := w.ApplyInput(2, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	w.TakeEvents()
	snapshot := w.Snapshot()
	if snapshot.Stage.State != stage.StageClear || snapshot.Players[0].Alive || !snapshot.Players[1].Alive {
		t.Fatalf("test setup did not leave one dead teammate: %+v", snapshot)
	}
	if err := w.StartReward(rewardCatalog(t), 5, 30); err != nil {
		t.Fatal(err)
	}
	w.TakeRewardUpdates()
	if err := w.ChooseReward(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := w.ChooseReward(2, 2); err != nil {
		t.Fatal(err)
	}
	w.TakeRewardUpdates()
	if err := w.StartStage(stage.Plan{Index: 2, DifficultyScore: 1, Monsters: []stage.Spawn{target(19, 19, 100)}}); err != nil {
		t.Fatal(err)
	}
	snapshot = w.Snapshot()
	if !snapshot.Players[0].Alive || snapshot.Players[0].Health != 50 || snapshot.Players[0].Position != game.DefaultConfig().Spawn {
		t.Fatalf("dead teammate revive = %+v", snapshot.Players[0])
	}
	if snapshot.Players[1].Health != 100 || snapshot.Players[1].Position != game.DefaultConfig().Spawn {
		t.Fatalf("surviving teammate reset = %+v", snapshot.Players[1])
	}
}

func TestLeavingRewardPlayerDoesNotBlockRemainingTeam(t *testing.T) {
	w := clearedWorld(t, 1, 2)
	if err := w.StartReward(rewardCatalog(t), 4, 30); err != nil {
		t.Fatal(err)
	}
	w.TakeRewardUpdates()
	if err := w.ChooseReward(1, 1); err != nil {
		t.Fatal(err)
	}
	w.RemovePlayer(2)
	if snapshot := w.Snapshot(); snapshot.Stage.State != stage.PreparingNextStage || len(snapshot.Players) != 1 || snapshot.Players[0].ID != 1 {
		t.Fatalf("removed reward player blocked progression: %+v", snapshot)
	}
}
