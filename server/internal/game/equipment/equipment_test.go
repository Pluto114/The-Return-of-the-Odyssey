package equipment_test

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
)

func defaultCatalog(t *testing.T) equipment.Catalog {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "..", "..", "data", "equipment", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	catalog, err := equipment.Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func baseStats() entity.CombatStats {
	return entity.CombatStats{Attack: 20, Defense: 5, MaxHealth: 100, MoveSpeed: 5, AttackCooldownTicks: 6}
}

func TestDefaultCatalogLoadsAsStableStaticData(t *testing.T) {
	catalog := defaultCatalog(t)
	if catalog.Version() != 1 {
		t.Fatalf("version = %d, want 1", catalog.Version())
	}
	want := []equipment.ID{1001, 1002, 1003, 2001, 2002, 3001, 3002}
	if got := catalog.IDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	wantDisplay := map[equipment.ID]struct {
		name, slot, description string
	}{
		1001: {"Iron Sidearm", "weapon", "Attack +5; magazine +4"},
		1002: {"Rapid Sidearm", "weapon", "Attack speed x1.2; magazine +6"},
		1003: {"Drum Sidearm", "weapon", "Attack +2; magazine +12"},
		2001: {"Vitality Relic", "relic", "Max health +25"},
		2002: {"Wind Relic", "relic", "Move speed x1.1"},
		3001: {"Healing Potion", "potion", "Restore 30 health"},
		3002: {"Greater Healing Potion", "potion", "Restore 60 health"},
	}
	for id, want := range wantDisplay {
		definition, ok := catalog.Lookup(id)
		if !ok {
			t.Errorf("display equipment %d missing", id)
			continue
		}
		if definition.Name != want.name || string(definition.Slot) != want.slot || definition.Description != want.description {
			t.Errorf("display equipment %d = %q/%q/%q, want %q/%q/%q", id,
				definition.Name, definition.Slot, definition.Description, want.name, want.slot, want.description)
		}
	}
	definition, ok := catalog.Lookup(1001)
	if !ok {
		t.Fatal("known equipment missing")
	}
	definition.Modifiers[0].Value = 999
	again, _ := catalog.Lookup(1001)
	if again.Modifiers[0].Value != 5 {
		t.Fatal("catalog definition leaked mutable modifier storage")
	}
	ids := catalog.IDs()
	ids[0] = 999
	if catalog.IDs()[0] != 1001 {
		t.Fatal("catalog leaked mutable ID storage")
	}
}

func TestCatalogRejectsMalformedOrUnsafeData(t *testing.T) {
	valid := `{"version":1,"items":[{"id":1,"key":"w","name":"W","slot":"weapon","modifiers":[{"stat":"attack","operation":"add","value":1}]}]}`
	cases := []string{
		`{"version":0,"items":[]}`,
		`{"version":1,"items":[{"id":1,"key":"w","name":"W","slot":"weapon","modifiers":[{"stat":"attack","operation":"add","value":1}]},{"id":1,"key":"x","name":"X","slot":"relic","modifiers":[{"stat":"defense","operation":"add","value":1}]}]}`,
		`{"version":1,"items":[{"id":1,"key":"w","name":"W","slot":"weapon","modifiers":[{"stat":"attack","operation":"multiply","value":0}]}]}`,
		`{"version":1,"items":[{"id":1,"key":"p","name":"P","slot":"potion","heal":10,"modifiers":[{"stat":"attack","operation":"add","value":1}]}]}`,
		strings.TrimSuffix(valid, "}") + `,"unknown":true}`,
		valid + valid,
	}
	for i, input := range cases {
		if _, err := equipment.Parse(strings.NewReader(input)); err == nil {
			t.Fatalf("case %d accepted malformed catalog", i)
		}
	}
}

func TestResolveRebuildsStatsAndUsesFixedModifierOrder(t *testing.T) {
	catalog, err := equipment.NewCatalog(1, []equipment.Definition{
		{ID: 1, Key: "weapon", Name: "Weapon", Slot: equipment.Weapon, Modifiers: []equipment.Modifier{
			{Stat: equipment.Attack, Operation: equipment.Add, Value: 5},
			{Stat: equipment.Attack, Operation: equipment.Multiply, Value: 2},
			{Stat: equipment.AttackSpeed, Operation: equipment.Multiply, Value: 1.2},
		}},
		{ID: 2, Key: "relic", Name: "Relic", Slot: equipment.Relic, Modifiers: []equipment.Modifier{
			{Stat: equipment.Attack, Operation: equipment.Add, Value: 10},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := baseStats()
	got, err := equipment.Resolve(base, equipment.Loadout{WeaponID: 1, RelicID: 2}, catalog, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attack != 60 || got.AttackCooldownTicks != 5 {
		t.Fatalf("resolved stats = %+v, want attack 60 and cooldown 5", got)
	}
	if base != baseStats() {
		t.Fatal("resolving equipment mutated BaseStats")
	}
}

func TestApplyReplacesSlotsAndDoesNotHealMaxHealthChanges(t *testing.T) {
	catalog, err := equipment.NewCatalog(1, []equipment.Definition{
		{ID: 1, Key: "low", Name: "Low", Slot: equipment.Relic, Modifiers: []equipment.Modifier{{Stat: equipment.MaxHealth, Operation: equipment.Multiply, Value: 0.5}}},
		{ID: 2, Key: "high", Name: "High", Slot: equipment.Relic, Modifiers: []equipment.Modifier{{Stat: equipment.MaxHealth, Operation: equipment.Add, Value: 50}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := baseStats()
	loadout, stats, health, err := equipment.Apply(base, 90, equipment.Loadout{}, 1, catalog, 30)
	if err != nil {
		t.Fatal(err)
	}
	if loadout.RelicID != 1 || stats.MaxHealth != 50 || health != 50 {
		t.Fatalf("lower max-health apply = %+v %+v %v", loadout, stats, health)
	}
	loadout, stats, health, err = equipment.Apply(base, health, loadout, 2, catalog, 30)
	if err != nil {
		t.Fatal(err)
	}
	if loadout.RelicID != 2 || stats.MaxHealth != 150 || health != 50 {
		t.Fatalf("higher max-health replacement = %+v %+v %v", loadout, stats, health)
	}
}

func TestApplyRejectsInvalidResultWithoutChangingInputs(t *testing.T) {
	catalog, err := equipment.NewCatalog(1, []equipment.Definition{{
		ID: 1, Key: "bad", Name: "Bad", Slot: equipment.Weapon,
		Modifiers: []equipment.Modifier{{Stat: equipment.Attack, Operation: equipment.Add, Value: -100}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	before := equipment.Loadout{}
	got, _, health, err := equipment.Apply(baseStats(), 70, before, 1, catalog, 30)
	if !errors.Is(err, equipment.ErrInvalidStats) || got != before || health != 70 {
		t.Fatalf("invalid apply leaked state: loadout=%+v health=%v err=%v", got, health, err)
	}
	valid := defaultCatalog(t)
	if _, _, _, err := equipment.Apply(baseStats(), math.NaN(), before, 1001, valid, 30); !errors.Is(err, equipment.ErrInvalidHealth) {
		t.Fatalf("invalid health returned %v, want ErrInvalidHealth", err)
	}
}

func TestPotionHealsToCapAndCanOnlyBeConsumedOnce(t *testing.T) {
	catalog := defaultCatalog(t)
	loadout := equipment.Loadout{PotionID: 3001}
	next, health, consumed, err := equipment.UsePotion(loadout, 80, 100, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if next.PotionID != 0 || health != 100 || consumed != 3001 {
		t.Fatalf("potion result = %+v health=%v consumed=%v", next, health, consumed)
	}
	if _, _, _, err := equipment.UsePotion(next, health, 100, catalog); !errors.Is(err, equipment.ErrNoPotion) {
		t.Fatalf("second potion use returned %v, want ErrNoPotion", err)
	}
}
