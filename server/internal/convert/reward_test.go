package convert

import (
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
)

func TestRewardOptionsAvailable(t *testing.T) {
	mt, msg, err := Reward(game.RewardUpdate{
		Kind:         game.RewardOptionsAvailable,
		StageIndex:   1,
		ServerTick:   10,
		PlayerID:     7,
		EquipmentIDs: []equipment.ID{1001, 2001, 3001},
		DeadlineTick: 460,
	})
	if err != nil {
		t.Fatalf("Reward() error = %v", err)
	}
	if mt != uint16(protocol.MessageType_MSG_REWARD_OPTIONS) {
		t.Errorf("MessageType = %d, want MSG_REWARD_OPTIONS", mt)
	}
	got := msg.(*protocol.RewardOptions)
	if got.StageIndex != 1 || got.DeadlineServerTick != 460 {
		t.Errorf("RewardOptions = %+v, want stage_index=1 deadline=460", got)
	}
	if len(got.EquipmentIds) != 3 || got.EquipmentIds[0] != 1001 || got.EquipmentIds[2] != 3001 {
		t.Errorf("EquipmentIds = %v, want [1001 2001 3001]", got.EquipmentIds)
	}
}

func TestRewardSelectionApplied(t *testing.T) {
	mt, msg, err := Reward(game.RewardUpdate{
		Kind:        game.RewardSelectionApplied,
		StageIndex:  1,
		ServerTick:  20,
		PlayerID:    7,
		EquipmentID: equipment.ID(2001),
	})
	if err != nil {
		t.Fatalf("Reward() error = %v", err)
	}
	if mt != uint16(protocol.MessageType_MSG_REWARD_APPLIED) {
		t.Errorf("MessageType = %d, want MSG_REWARD_APPLIED", mt)
	}
	got := msg.(*protocol.RewardApplied)
	if got.Reason != protocol.ReasonCode_REASON_OK {
		t.Errorf("Reason = %v, want REASON_OK", got.Reason)
	}
	if got.EquipmentId != 2001 {
		t.Errorf("EquipmentId = %d, want 2001", got.EquipmentId)
	}
}

func TestRewardUnknownKind(t *testing.T) {
	if _, _, err := Reward(game.RewardUpdate{Kind: game.RewardUpdateKind(255)}); err == nil {
		t.Error("Reward() with unknown kind returned nil error, want error")
	}
}
