package reward_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/reward"
)

func catalog(t *testing.T) equipment.Catalog {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "..", "..", "data", "equipment", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result, err := equipment.Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRoundOffersAreDeterministicAndDetached(t *testing.T) {
	items := catalog(t)
	a, err := reward.NewRound(2, 42, 100, 300, []entity.ID{2, 1}, items, 3)
	if err != nil {
		t.Fatal(err)
	}
	b, err := reward.NewRound(2, 42, 100, 300, []entity.ID{1, 2}, items, 3)
	if err != nil {
		t.Fatal(err)
	}
	c, err := reward.NewRound(2, 43, 100, 300, []entity.ID{1, 2}, items, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Offers(), b.Offers()) {
		t.Fatal("same stage, seed and players produced different offers")
	}
	if reflect.DeepEqual(a.Offers(), c.Offers()) {
		t.Fatal("different seed produced identical offers")
	}
	if a.StageIndex() != 2 || a.Seed() != 42 || a.DeadlineTick() != 400 {
		t.Fatalf("round metadata = %d/%d/%d", a.StageIndex(), a.Seed(), a.DeadlineTick())
	}
	offers := a.Offers()
	if len(offers) != 2 || offers[0].PlayerID != 1 || offers[1].PlayerID != 2 {
		t.Fatalf("offers are not player-ID ordered: %+v", offers)
	}
	for _, offer := range offers {
		seen := map[equipment.ID]bool{}
		for _, id := range offer.EquipmentIDs {
			if seen[id] {
				t.Fatalf("duplicate option for player %d: %v", offer.PlayerID, offer.EquipmentIDs)
			}
			seen[id] = true
			if _, ok := items.Lookup(id); !ok {
				t.Fatalf("unknown option %d", id)
			}
		}
	}
	offers[0].EquipmentIDs[0] = 999
	if a.Offers()[0].EquipmentIDs[0] == 999 {
		t.Fatal("Offers leaked mutable option storage")
	}
}

func TestChoiceDeadlineValidationAndDuplicateProtection(t *testing.T) {
	round, err := reward.NewRound(1, 7, 10, 5, []entity.ID{1}, catalog(t), 3)
	if err != nil {
		t.Fatal(err)
	}
	offer := round.Offers()[0]
	if _, err := round.ValidateChoice(2, offer.EquipmentIDs[0], 15); !errors.Is(err, reward.ErrUnknownPlayer) {
		t.Fatalf("unknown player returned %v", err)
	}
	if _, err := round.ValidateChoice(1, 999, 15); !errors.Is(err, reward.ErrChoiceNotOffered) {
		t.Fatalf("unoffered choice returned %v", err)
	}
	selection, err := round.ValidateChoice(1, offer.EquipmentIDs[1], 15)
	if err != nil {
		t.Fatalf("choice at deadline failed: %v", err)
	}
	if err := round.Commit(selection); err != nil {
		t.Fatal(err)
	}
	if !round.Complete() {
		t.Fatal("single-player round did not complete")
	}
	if _, err := round.ValidateChoice(1, offer.EquipmentIDs[0], 15); !errors.Is(err, reward.ErrChoiceAlreadyMade) {
		t.Fatalf("duplicate choice returned %v", err)
	}
	foreign, err := reward.NewRound(1, 7, 10, 5, []entity.ID{1}, catalog(t), 3)
	if err != nil {
		t.Fatal(err)
	}
	foreignSelection, err := foreign.ValidateChoice(1, foreign.Offers()[0].EquipmentIDs[0], 15)
	if err != nil {
		t.Fatal(err)
	}
	if err := round.Commit(foreignSelection); !errors.Is(err, reward.ErrInvalidRound) {
		t.Fatalf("foreign selection returned %v", err)
	}

	expired, err := reward.NewRound(1, 7, 10, 5, []entity.ID{1}, catalog(t), 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expired.ValidateChoice(1, expired.Offers()[0].EquipmentIDs[0], 16); !errors.Is(err, reward.ErrChoiceExpired) {
		t.Fatalf("late choice returned %v", err)
	}
}

func TestExpiredPlayersDefaultToFirstOption(t *testing.T) {
	round, err := reward.NewRound(3, 11, 100, 10, []entity.ID{2, 1}, catalog(t), 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := round.DueDefaults(110); got != nil {
		t.Fatalf("defaults triggered at inclusive deadline: %+v", got)
	}
	first := round.Offers()[0]
	chosen, err := round.ValidateChoice(first.PlayerID, first.EquipmentIDs[2], 110)
	if err != nil {
		t.Fatal(err)
	}
	if err := round.Commit(chosen); err != nil {
		t.Fatal(err)
	}
	defaults := round.DueDefaults(111)
	if len(defaults) != 1 || defaults[0].PlayerID() != 2 || !defaults[0].Defaulted() {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
	second := round.Offers()[1]
	if defaults[0].EquipmentID() != second.EquipmentIDs[0] {
		t.Fatal("timeout did not select the first option")
	}
	if err := round.Commit(defaults[0]); err != nil {
		t.Fatal(err)
	}
	if !round.Complete() || !round.Offers()[1].Defaulted {
		t.Fatal("default selection did not complete the round")
	}
}

func TestRoundRejectsInvalidShape(t *testing.T) {
	items := catalog(t)
	for _, test := range []struct {
		stage, options   int
		opened, duration uint64
		players          []entity.ID
	}{
		{stage: 0, options: 3, duration: 1, players: []entity.ID{1}},
		{stage: 1, options: 0, duration: 1, players: []entity.ID{1}},
		{stage: 1, options: 4, duration: 1, players: []entity.ID{1}},
		{stage: 1, options: 3, duration: 0, players: []entity.ID{1}},
		{stage: 1, options: 3, opened: ^uint64(0), duration: 1, players: []entity.ID{1}},
		{stage: 1, options: 3, duration: 1, players: []entity.ID{1, 1}},
		{stage: 1, options: 3, duration: 1, players: []entity.ID{0}},
	} {
		if _, err := reward.NewRound(uint32(test.stage), 1, test.opened, test.duration, test.players, items, test.options); !errors.Is(err, reward.ErrInvalidRound) {
			t.Fatalf("invalid round %+v returned %v", test, err)
		}
	}
}

func TestRemovePlayerDropsPendingOffer(t *testing.T) {
	round, err := reward.NewRound(1, 1, 1, 10, []entity.ID{1, 2}, catalog(t), 3)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := round.ValidateChoice(1, round.Offers()[0].EquipmentIDs[0], 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := round.Commit(selection); err != nil {
		t.Fatal(err)
	}
	if !round.RemovePlayer(2) || round.RemovePlayer(2) || !round.Complete() {
		t.Fatal("removing pending player did not complete remaining offers")
	}
}
