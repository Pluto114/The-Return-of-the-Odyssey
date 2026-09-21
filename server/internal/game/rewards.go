package game

import (
	"errors"
	"fmt"
	"slices"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/reward"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

type RewardUpdateKind uint8

const (
	RewardOptionsAvailable RewardUpdateKind = iota + 1
	RewardSelectionApplied
)

var ErrRewardState = errors.New("reward action is not valid in the current stage state")

// RewardUpdate is a targeted reliable domain message. A must deliver it only
// to PlayerID; combat events remain safe to broadcast to the whole room.
type RewardUpdate struct {
	Kind         RewardUpdateKind
	StageIndex   uint32
	ServerTick   uint64
	PlayerID     entity.ID
	EquipmentIDs []equipment.ID
	DeadlineTick uint64
	EquipmentID  equipment.ID
	Defaulted    bool
}

func (u RewardUpdate) Clone() RewardUpdate {
	u.EquipmentIDs = slices.Clone(u.EquipmentIDs)
	return u
}

type RewardUpdateBatch struct {
	Updates  []RewardUpdate
	Overflow bool
}

// StartReward validates every catalog item against every current player before
// changing state. That makes every offered choice and timeout default safe to
// apply later without partially mutating player state.
func (w *World) StartReward(catalog equipment.Catalog, seed int64, durationTicks uint64) error {
	if w.stage.State != stage.StageClear || w.rewardRound != nil {
		return ErrRewardState
	}
	playerIDs := orderedIDs(w.players)
	ids := catalog.IDs()
	if len(ids) == 0 {
		return fmt.Errorf("start reward: empty equipment catalog")
	}
	for _, playerID := range playerIDs {
		player := w.players[playerID]
		for _, id := range ids {
			if _, _, _, err := equipment.Apply(player.player.BaseStats, player.player.Health, player.loadout, id, catalog, TickRate); err != nil {
				return fmt.Errorf("start reward: player %d cannot apply equipment %d: %w", playerID, id, err)
			}
		}
	}
	optionCount := min(reward.MaxOptions, len(ids))
	round, err := reward.NewRound(w.stage.Index, seed, w.tick, durationTicks, playerIDs, catalog, optionCount)
	if err != nil {
		return fmt.Errorf("start reward: %w", err)
	}
	w.rewardCatalog = catalog
	w.rewardRound = round
	w.stage.State = stage.Reward
	for _, offer := range round.Offers() {
		w.emitRewardUpdate(RewardUpdate{Kind: RewardOptionsAvailable, StageIndex: w.stage.Index, ServerTick: w.tick + 1,
			PlayerID: offer.PlayerID, EquipmentIDs: offer.EquipmentIDs, DeadlineTick: offer.DeadlineTick})
	}
	return nil
}

func (w *World) ChooseReward(playerID entity.ID, equipmentID equipment.ID) error {
	if w.stage.State != stage.Reward || w.rewardRound == nil {
		return ErrRewardState
	}
	selection, err := w.rewardRound.ValidateChoice(playerID, equipmentID, w.tick)
	if err != nil {
		return err
	}
	return w.applyRewardSelection(selection, w.tick+1)
}

func (w *World) applyRewardSelection(selection reward.Selection, eventTick uint64) error {
	player := w.players[selection.PlayerID()]
	if player == nil {
		return ErrPlayerMissing
	}
	loadout, stats, health, err := equipment.Apply(player.player.BaseStats, player.player.Health, player.loadout, selection.EquipmentID(), w.rewardCatalog, TickRate)
	if err != nil {
		return err
	}
	if err := w.rewardRound.Commit(selection); err != nil {
		return err
	}
	player.loadout = loadout
	player.player.CurrentStats = stats
	player.player.Health = health
	player.syncEquipment()
	w.syncMagazine(player)
	w.emitRewardUpdate(RewardUpdate{Kind: RewardSelectionApplied, StageIndex: w.stage.Index, ServerTick: eventTick,
		PlayerID: selection.PlayerID(), EquipmentID: selection.EquipmentID(), Defaulted: selection.Defaulted()})
	if w.rewardRound.Complete() {
		w.stage.State = stage.PreparingNextStage
	}
	return nil
}

func (w *World) stepReward() {
	if w.stage.State != stage.Reward || w.rewardRound == nil {
		return
	}
	for _, selection := range w.rewardRound.DueDefaults(w.tick) {
		if err := w.applyRewardSelection(selection, w.tick); err != nil {
			panic(fmt.Sprintf("validated reward default failed: %v", err))
		}
	}
}

func (w *World) usePotion(player *playerState) {
	loadout, health, _, err := equipment.UsePotion(player.loadout, player.player.Health, player.player.CurrentStats.MaxHealth, w.rewardCatalog)
	if err != nil {
		return
	}
	player.loadout = loadout
	player.player.Health = health
	player.syncEquipment()
}

func (p *playerState) syncEquipment() {
	p.player.Equipment = entity.EquipmentState{WeaponID: uint32(p.loadout.WeaponID), RelicID: uint32(p.loadout.RelicID), PotionID: uint32(p.loadout.PotionID)}
}

func (w *World) emitRewardUpdate(update RewardUpdate) {
	if len(w.rewardUpdates) >= maxPendingEvents {
		w.rewardOverflow = true
		return
	}
	w.rewardUpdates = append(w.rewardUpdates, update.Clone())
}

// TakeRewardUpdates transfers one targeted batch to the Room owner. It must be
// consumed every tick just like TakeEvents.
func (w *World) TakeRewardUpdates() RewardUpdateBatch {
	batch := RewardUpdateBatch{Updates: w.rewardUpdates, Overflow: w.rewardOverflow}
	w.rewardUpdates = nil
	w.rewardOverflow = false
	return batch
}
