package router

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
)

// TestRewardDispatcherTargetsSinglePlayer verifies the D6 contract that reward
// updates are single-recipient: a RewardOptions update addressed to player 1 is
// delivered only to player 1's sink, never broadcast to player 2.
func TestRewardDispatcherTargetsSinglePlayer(t *testing.T) {
	d := NewRewardDispatcher()
	s1 := &eventRecordingSink{}
	s2 := &eventRecordingSink{}
	d.Subscribe(1, s1)
	d.Subscribe(2, s2)

	d.Dispatch(game.RewardUpdateBatch{Updates: []game.RewardUpdate{
		{Kind: game.RewardOptionsAvailable, PlayerID: 1, StageIndex: 1, EquipmentIDs: []equipment.ID{1001}, DeadlineTick: 100},
	}})

	if len(s1.frames) != 1 {
		t.Fatalf("player 1 frames = %d, want 1", len(s1.frames))
	}
	if len(s2.frames) != 0 {
		t.Fatalf("player 2 frames = %d, want 0 (reward is targeted, not broadcast)", len(s2.frames))
	}

	hdr, _, err := network.ReadFrame(bufio.NewReader(bytes.NewReader(s1.frames[0])))
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if hdr.MessageType != uint16(protocol.MessageType_MSG_REWARD_OPTIONS) {
		t.Errorf("MessageType = %d, want MSG_REWARD_OPTIONS", hdr.MessageType)
	}
}

// TestRewardDispatcherAppliedIsTargeted verifies RewardApplied is also routed
// to the single selected player only.
func TestRewardDispatcherAppliedIsTargeted(t *testing.T) {
	d := NewRewardDispatcher()
	s1 := &eventRecordingSink{}
	s2 := &eventRecordingSink{}
	d.Subscribe(1, s1)
	d.Subscribe(2, s2)

	d.Dispatch(game.RewardUpdateBatch{Updates: []game.RewardUpdate{
		{Kind: game.RewardSelectionApplied, PlayerID: 2, EquipmentID: equipment.ID(2001)},
	}})

	if len(s1.frames) != 0 {
		t.Errorf("player 1 frames = %d, want 0", len(s1.frames))
	}
	if len(s2.frames) != 1 {
		t.Fatalf("player 2 frames = %d, want 1", len(s2.frames))
	}
	hdr, _, err := network.ReadFrame(bufio.NewReader(bytes.NewReader(s2.frames[0])))
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if hdr.MessageType != uint16(protocol.MessageType_MSG_REWARD_APPLIED) {
		t.Errorf("MessageType = %d, want MSG_REWARD_APPLIED", hdr.MessageType)
	}
}

// TestRewardDispatcherSkipsUnknownPlayer verifies a reward update addressed to
// a player who already left is dropped without panicking or affecting others.
func TestRewardDispatcherSkipsUnknownPlayer(t *testing.T) {
	d := NewRewardDispatcher()
	s := &eventRecordingSink{}
	d.Subscribe(1, s)

	d.Dispatch(game.RewardUpdateBatch{Updates: []game.RewardUpdate{
		{Kind: game.RewardOptionsAvailable, PlayerID: 999, StageIndex: 1, EquipmentIDs: []equipment.ID{1001}},
		{Kind: game.RewardOptionsAvailable, PlayerID: 1, StageIndex: 1, EquipmentIDs: []equipment.ID{1001}},
	}})

	if len(s.frames) != 1 {
		t.Fatalf("player 1 frames = %d, want 1 (unknown player skipped)", len(s.frames))
	}
}
