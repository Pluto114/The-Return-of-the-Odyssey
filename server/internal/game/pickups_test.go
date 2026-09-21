package game

import (
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
)

func pickupCatalog(t *testing.T) equipment.Catalog {
	t.Helper()
	catalog, err := equipment.NewCatalog(1, []equipment.Definition{
		{ID: 1001, Key: "iron", Name: "Iron", Slot: equipment.Weapon,
			Modifiers: []equipment.Modifier{{Stat: equipment.Attack, Operation: equipment.Add, Value: 5}}},
		{ID: 1002, Key: "rapid", Name: "Rapid", Slot: equipment.Weapon,
			Modifiers: []equipment.Modifier{{Stat: equipment.AttackSpeed, Operation: equipment.Multiply, Value: 1.2}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestStagePickupsHealAndEquipWeaponAuthoritatively(t *testing.T) {
	config := DefaultConfig()
	w, err := NewWorld(config, pickupCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	plan, err := NewFirstStagePlan(config, 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.StartStage(plan); err != nil {
		t.Fatal(err)
	}

	before := w.Snapshot()
	if len(before.Pickups) != 2 {
		t.Fatalf("stage pickups = %d, want health + weapon", len(before.Pickups))
	}
	var health, weapon PickupView
	for _, pickup := range before.Pickups {
		switch pickup.Kind {
		case HealthPickup:
			health = pickup
		case WeaponPickup:
			weapon = pickup
		}
	}
	if health.ID == 0 || health.Value != 30 || weapon.ID == 0 || weapon.EquipmentID != 1001 {
		t.Fatalf("unexpected pickups: %+v", before.Pickups)
	}

	player := w.players[entity.ID(1)]
	player.player.Health = 50
	player.player.Position = health.Position
	w.Step(time.Unix(1, 0))
	afterHeal := w.Snapshot()
	if afterHeal.Players[0].Health != 80 || len(afterHeal.Pickups) != 1 {
		t.Fatalf("health pickup result: player=%+v pickups=%+v", afterHeal.Players[0], afterHeal.Pickups)
	}

	player.player.Position = weapon.Position
	w.Step(time.Unix(1, int64(time.Second/30)))
	afterWeapon := w.Snapshot()
	if afterWeapon.Players[0].Equipment.WeaponID != 1001 || afterWeapon.Players[0].CurrentStats.Attack != 25 || len(afterWeapon.Pickups) != 0 {
		t.Fatalf("weapon pickup result: player=%+v pickups=%+v", afterWeapon.Players[0], afterWeapon.Pickups)
	}
}

func TestFullHealthDoesNotConsumeHealthPickupAndNextStageRefreshes(t *testing.T) {
	w, err := NewWorld(DefaultConfig(), pickupCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	w.spawnStagePickups(1)
	var healthID entity.ID
	for id, pickup := range w.pickups {
		if pickup.Kind == HealthPickup {
			healthID = id
			w.players[1].player.Position = pickup.Position
		}
	}
	w.stage.State = 1
	w.collectPickups()
	if _, ok := w.pickups[healthID]; !ok {
		t.Fatal("full-health player consumed health pickup")
	}

	w.spawnStagePickups(2)
	if len(w.pickups) != 2 {
		t.Fatalf("stage 2 pickup count = %d", len(w.pickups))
	}
	if _, oldStillPresent := w.pickups[healthID]; oldStillPresent {
		t.Fatal("stage refresh retained an old pickup")
	}
}
