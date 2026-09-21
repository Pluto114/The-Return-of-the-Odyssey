package game

import (
	"slices"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

// PlayerResumeState 由 Room goroutine 从实时 World 构建；客户端必须丢弃旧预测并采用完整快照。
type PlayerResumeState struct {
	Snapshot Snapshot
	Reward   *RewardUpdate
}

func (s PlayerResumeState) Clone() PlayerResumeState {
	s.Snapshot = s.Snapshot.Clone()
	if s.Reward != nil {
		copy := s.Reward.Clone()
		s.Reward = &copy
	}
	return s
}

// ResumeState 返回当前权威状态，不重放输入也不恢复旧副本；同时重建待选或已完成私人奖励，
// 避免奖励阶段重连丢失选项或应用结果。
func (w *World) ResumeState(playerID entity.ID) (PlayerResumeState, error) {
	if _, exists := w.players[playerID]; !exists {
		return PlayerResumeState{}, ErrPlayerMissing
	}
	result := PlayerResumeState{Snapshot: w.Snapshot()}
	if offer, ok := w.rewardRound.Offer(playerID); ok {
		update := RewardUpdate{StageIndex: w.stage.Index, ServerTick: w.tick, PlayerID: playerID, DeadlineTick: offer.DeadlineTick}
		if offer.SelectedID == 0 {
			update.Kind = RewardOptionsAvailable
			update.EquipmentIDs = slices.Clone(offer.EquipmentIDs)
		} else {
			update.Kind = RewardSelectionApplied
			update.EquipmentID = offer.SelectedID
			update.Defaulted = offer.Defaulted
		}
		result.Reward = &update
	}
	return result, nil
}
