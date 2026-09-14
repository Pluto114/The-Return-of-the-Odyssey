package game

import (
	"slices"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

// PlayerResumeState is built from the live World on the Room owner goroutine.
// The client must discard its old prediction state and use this full snapshot.
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

// ResumeState returns current authoritative state without replaying input or
// restoring an older copy. A pending or completed private reward is rebuilt so
// reconnecting during Reward cannot lose its options or applied transition.
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
