package room_test

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/reward"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

func roomRewardCatalog(t *testing.T) equipment.Catalog {
	t.Helper()
	catalog, err := equipment.NewCatalog(1, []equipment.Definition{
		{ID: 1, Key: "weapon", Name: "Weapon", Slot: equipment.Weapon, Modifiers: []equipment.Modifier{{Stat: equipment.Attack, Operation: equipment.Add, Value: 5}}},
		{ID: 2, Key: "relic", Name: "Relic", Slot: equipment.Relic, Modifiers: []equipment.Modifier{{Stat: equipment.Defense, Operation: equipment.Add, Value: 5}}},
		{ID: 3, Key: "potion", Name: "Potion", Slot: equipment.Potion, Heal: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestRoomRewardCommandsResolveSessionAndPublishTargetedUpdates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		join(t, r, 12, 102)

		startReceipt, err := r.StartStage(battlePlan())
		if err != nil {
			t.Fatal(err)
		}
		result(t, startReceipt, nil)
		if err := r.Input(11, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
			t.Fatal(err)
		}
		advance(game.SnapshotEvery)
		if r.LatestSnapshot().Stage.State != stage.StageClear {
			t.Fatal("test room did not clear")
		}

		receipt, err := r.StartReward(roomRewardCatalog(t), 44, 30)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		options := <-r.RewardUpdates()
		if options.Overflow || len(options.Updates) != 2 || options.Updates[0].PlayerID != 101 || options.Updates[1].PlayerID != 102 {
			t.Fatalf("targeted options = %+v", options)
		}

		if _, err := r.ChooseReward(0, 1); !errors.Is(err, room.ErrInvalidSession) {
			t.Fatalf("zero session returned %v", err)
		}
		if _, err := r.ChooseReward(11, 0); !errors.Is(err, equipment.ErrUnknownEquipment) {
			t.Fatalf("zero equipment returned %v", err)
		}
		receipt, err = r.ChooseReward(99, 1)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, room.ErrNotJoined)

		receipt, err = r.ChooseReward(11, 1)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		applied := <-r.RewardUpdates()
		if len(applied.Updates) != 1 || applied.Updates[0].PlayerID != 101 || applied.Updates[0].EquipmentID != 1 {
			t.Fatalf("first applied update = %+v", applied)
		}

		receipt, err = r.ChooseReward(11, 2)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, reward.ErrChoiceAlreadyMade)
		receipt, err = r.ChooseReward(12, 2)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		<-r.RewardUpdates()
		advance(game.SnapshotEvery)
		if snapshot := r.LatestSnapshot(); snapshot.Stage.State != stage.PreparingNextStage || snapshot.Players[0].Equipment.WeaponID != 1 || snapshot.Players[1].Equipment.RelicID != 2 {
			t.Fatalf("room reward state = %+v", snapshot)
		}
	})
}

func TestRewardUpdateBackpressureClosesRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := room.DefaultConfig()
		config.EventCapacity = 1
		r := start(t, 1, config)
		defer r.Close()
		join(t, r, 11, 101)
		receipt, err := r.StartStage(battlePlan())
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		<-r.Events()
		if err := r.Input(11, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
			t.Fatal(err)
		}
		advance(game.SnapshotEvery)
		<-r.Events()
		receipt, err = r.StartReward(roomRewardCatalog(t), 1, 30)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		if len(r.RewardUpdates()) != 1 {
			t.Fatal("test did not fill targeted reward queue")
		}
		receipt, err = r.ChooseReward(11, 1)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		select {
		case <-r.Done():
		default:
			t.Fatal("full reward update queue did not close room")
		}
		if r.Stats().CloseReason != "event_backpressure" || !r.LatestSnapshot().Closed {
			t.Fatalf("reward backpressure status = %+v", r.Stats())
		}
	})
}
