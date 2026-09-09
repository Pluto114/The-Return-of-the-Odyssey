package convert

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
)

// Event converts a domain game.Event into a wire message. It returns the
// MessageType and the concrete protobuf message so the caller can marshal and
// frame it exactly once. The caller is responsible for the header's
// MessageType field.
//
// An unknown EventKind is a programming error: it returns an error rather than
// silently dropping, so a newly added domain kind fails loudly at the boundary.
func Event(e game.Event) (uint16, proto.Message, error) {
	switch e.Kind {
	case game.StageStarted:
		return uint16(protocol.MessageType_MSG_STAGE_STARTED_EVENT), &protocol.StageStartedEvent{
			StageIndex: e.StageIndex,
			ServerTick: e.ServerTick,
		}, nil

	case game.ProjectileSpawned:
		return uint16(protocol.MessageType_MSG_PROJECTILE_SPAWN), &protocol.ProjectileSpawnEvent{
			ProjectileId:  uint64(e.EntityID),
			OwnerId:       uint64(e.SourceID),
			Position:      vec2f(e.Position),
			Velocity:      vec2f(e.Velocity),
			ExpiresAtTick: e.ExpiresAtTick,
			ServerTick:    e.ServerTick,
		}, nil

	case game.ProjectileDestroyed:
		return uint16(protocol.MessageType_MSG_PROJECTILE_DESTROY), &protocol.ProjectileDestroyEvent{
			ProjectileId: uint64(e.EntityID),
			OwnerId:      uint64(e.SourceID),
			Position:     vec2f(e.Position),
			ServerTick:   e.ServerTick,
		}, nil

	case game.DamageDealt:
		return uint16(protocol.MessageType_MSG_DAMAGE_EVENT), &protocol.DamageEvent{
			SourceId:        uint64(e.SourceID),
			TargetId:        uint64(e.TargetID),
			Amount:          float32(e.Amount),
			RemainingHealth: float32(e.Health),
			ServerTick:      e.ServerTick,
		}, nil

	case game.EntityDied:
		return uint16(protocol.MessageType_MSG_DEATH_EVENT), &protocol.DeathEvent{
			EntityId:   uint64(e.EntityID),
			KillerId:   uint64(e.SourceID),
			ServerTick: e.ServerTick,
		}, nil

	case game.StageCleared:
		return uint16(protocol.MessageType_MSG_STAGE_CLEARED_EVENT), &protocol.StageClearedEvent{
			StageIndex: e.StageIndex,
			ServerTick: e.ServerTick,
		}, nil

	case game.TeamDefeated:
		return uint16(protocol.MessageType_MSG_TEAM_DEFEATED_EVENT), &protocol.TeamDefeatedEvent{
			StageIndex: e.StageIndex,
			ServerTick: e.ServerTick,
		}, nil

	default:
		return 0, nil, fmt.Errorf("convert: unknown event kind %d", e.Kind)
	}
}
