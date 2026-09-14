package convert

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
)

// Reward converts a targeted domain reward update into its wire message. It
// returns the MessageType and the concrete protobuf message so the caller can
// marshal and frame it exactly once.
//
// Reward updates are targeted (single recipient), unlike combat events which
// are broadcast: RewardUpdate.PlayerID names the only player who should receive
// the message. The caller (the reward dispatcher) must honour that routing.
//
// An unknown RewardUpdateKind is a programming error and returns an error
// rather than silently dropping, so a newly added kind fails loudly.
func Reward(u game.RewardUpdate) (uint16, proto.Message, error) {
	switch u.Kind {
	case game.RewardOptionsAvailable:
		ids := make([]uint32, len(u.EquipmentIDs))
		for i, id := range u.EquipmentIDs {
			ids[i] = uint32(id)
		}
		return uint16(protocol.MessageType_MSG_REWARD_OPTIONS), &protocol.RewardOptions{
			StageIndex:          u.StageIndex,
			EquipmentIds:        ids,
			DeadlineServerTick:  u.DeadlineTick,
		}, nil

	case game.RewardSelectionApplied:
		return uint16(protocol.MessageType_MSG_REWARD_APPLIED), &protocol.RewardApplied{
			Reason:      protocol.ReasonCode_REASON_OK,
			EquipmentId: uint32(u.EquipmentID),
		}, nil

	default:
		return 0, nil, fmt.Errorf("convert: unknown reward update kind %d", u.Kind)
	}
}
