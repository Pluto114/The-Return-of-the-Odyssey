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

// RewardUpdate 是定向可靠领域消息，只能发送给对应 PlayerID；战斗事件仍可全房间广播。
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

// StartReward 在改变状态前先用所有当前玩家校验目录中每件物品，确保之后应用任一选项或
// 超时默认项都安全，不会只修改玩家一半状态。
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

// TakeRewardUpdates 把一个定向批次转移给 Room，必须像 TakeEvents 一样每 Tick 消费。
func (w *World) TakeRewardUpdates() RewardUpdateBatch {
	batch := RewardUpdateBatch{Updates: w.rewardUpdates, Overflow: w.rewardOverflow}
	w.rewardUpdates = nil
	w.rewardOverflow = false
	return batch
}
