// Package reward owns deterministic per-player reward offers and selection
// state. It contains no protocol or network types and does not mutate World.
package reward

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
)

const MaxOptions = 3

var (
	ErrInvalidRound      = errors.New("invalid reward round")
	ErrUnknownPlayer     = errors.New("player has no reward offer")
	ErrChoiceNotOffered  = errors.New("equipment was not offered")
	ErrChoiceAlreadyMade = errors.New("reward choice already made")
	ErrChoiceExpired     = errors.New("reward choice deadline expired")
)

type Offer struct {
	PlayerID     entity.ID
	EquipmentIDs []equipment.ID
	DeadlineTick uint64
	SelectedID   equipment.ID
	Defaulted    bool
}

func (o Offer) Clone() Offer {
	o.EquipmentIDs = slices.Clone(o.EquipmentIDs)
	return o
}

type Selection struct {
	owner       *Round
	playerID    entity.ID
	equipmentID equipment.ID
	defaulted   bool
}

func (s Selection) PlayerID() entity.ID       { return s.playerID }
func (s Selection) EquipmentID() equipment.ID { return s.equipmentID }
func (s Selection) Defaulted() bool           { return s.defaulted }

// Round is mutated only by its Room owner. Offers returns detached views for
// dispatchers and tests.
type Round struct {
	stageIndex uint32
	seed       int64
	deadline   uint64
	offers     map[entity.ID]*Offer
}

func NewRound(stageIndex uint32, seed int64, openedAtTick, durationTicks uint64, players []entity.ID, catalog equipment.Catalog, optionCount int) (*Round, error) {
	if stageIndex == 0 || durationTicks == 0 || openedAtTick > math.MaxUint64-durationTicks || len(players) == 0 || optionCount < 1 || optionCount > MaxOptions {
		return nil, ErrInvalidRound
	}
	ids := catalog.IDs()
	if len(ids) < optionCount {
		return nil, fmt.Errorf("%w: catalog has %d items for %d options", ErrInvalidRound, len(ids), optionCount)
	}
	playerIDs := slices.Clone(players)
	slices.Sort(playerIDs)
	for i, id := range playerIDs {
		if id == 0 || (i > 0 && id == playerIDs[i-1]) {
			return nil, ErrInvalidRound
		}
	}
	round := &Round{stageIndex: stageIndex, seed: seed, deadline: openedAtTick + durationTicks, offers: make(map[entity.ID]*Offer, len(playerIDs))}
	for _, playerID := range playerIDs {
		candidates := slices.Clone(ids)
		state := uint64(seed) ^ uint64(playerID)*0x9e3779b97f4a7c15 ^ uint64(stageIndex)*0xbf58476d1ce4e5b9
		for i := len(candidates) - 1; i > 0; i-- {
			j := int(next(&state) % uint64(i+1))
			candidates[i], candidates[j] = candidates[j], candidates[i]
		}
		round.offers[playerID] = &Offer{PlayerID: playerID, EquipmentIDs: slices.Clone(candidates[:optionCount]), DeadlineTick: round.deadline}
	}
	return round, nil
}

func (r *Round) StageIndex() uint32   { return r.stageIndex }
func (r *Round) Seed() int64          { return r.seed }
func (r *Round) DeadlineTick() uint64 { return r.deadline }

func (r *Round) Offers() []Offer {
	if r == nil {
		return nil
	}
	playerIDs := make([]entity.ID, 0, len(r.offers))
	for id := range r.offers {
		playerIDs = append(playerIDs, id)
	}
	slices.Sort(playerIDs)
	offers := make([]Offer, 0, len(playerIDs))
	for _, id := range playerIDs {
		offers = append(offers, r.offers[id].Clone())
	}
	return offers
}

// Offer returns one detached offer, including its current selection state.
// Resume paths use it to reconstruct only the reconnecting player's private
// reward state without exposing another player's options.
func (r *Round) Offer(playerID entity.ID) (Offer, bool) {
	if r == nil {
		return Offer{}, false
	}
	offer := r.offers[playerID]
	if offer == nil {
		return Offer{}, false
	}
	return offer.Clone(), true
}

// ValidateChoice is read-only. The Room first applies the equipment to a
// temporary value and calls Commit only after that succeeds.
func (r *Round) ValidateChoice(playerID entity.ID, equipmentID equipment.ID, serverTick uint64) (Selection, error) {
	if r == nil {
		return Selection{}, ErrInvalidRound
	}
	offer := r.offers[playerID]
	if offer == nil {
		return Selection{}, ErrUnknownPlayer
	}
	if offer.SelectedID != 0 {
		return Selection{}, ErrChoiceAlreadyMade
	}
	if serverTick > offer.DeadlineTick {
		return Selection{}, ErrChoiceExpired
	}
	if !slices.Contains(offer.EquipmentIDs, equipmentID) {
		return Selection{}, ErrChoiceNotOffered
	}
	return Selection{owner: r, playerID: playerID, equipmentID: equipmentID}, nil
}

// DueDefaults returns the first offered item for every still-pending player
// after the deadline. It does not commit, so failed equipment application
// cannot corrupt the round.
func (r *Round) DueDefaults(serverTick uint64) []Selection {
	if r == nil || serverTick <= r.deadline {
		return nil
	}
	var selections []Selection
	for _, offer := range r.Offers() {
		if offer.SelectedID == 0 {
			selections = append(selections, Selection{owner: r, playerID: offer.PlayerID, equipmentID: offer.EquipmentIDs[0], defaulted: true})
		}
	}
	return selections
}

func (r *Round) Commit(selection Selection) error {
	if r == nil || selection.owner != r || selection.playerID == 0 || selection.equipmentID == 0 {
		return ErrInvalidRound
	}
	offer := r.offers[selection.playerID]
	if offer == nil {
		return ErrUnknownPlayer
	}
	if offer.SelectedID != 0 {
		return ErrChoiceAlreadyMade
	}
	if !slices.Contains(offer.EquipmentIDs, selection.equipmentID) || (selection.defaulted && selection.equipmentID != offer.EquipmentIDs[0]) {
		return ErrChoiceNotOffered
	}
	offer.SelectedID = selection.equipmentID
	offer.Defaulted = selection.defaulted
	return nil
}

func (r *Round) Complete() bool {
	if r == nil || len(r.offers) == 0 {
		return false
	}
	for _, offer := range r.offers {
		if offer.SelectedID == 0 {
			return false
		}
	}
	return true
}

// RemovePlayer drops a pending or completed offer when the Room permanently
// removes that player. Resume flows should keep the player in World instead.
func (r *Round) RemovePlayer(playerID entity.ID) bool {
	if r == nil {
		return false
	}
	if _, exists := r.offers[playerID]; !exists {
		return false
	}
	delete(r.offers, playerID)
	return true
}

func next(state *uint64) uint64 {
	*state += 0x9e3779b97f4a7c15
	z := *state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
