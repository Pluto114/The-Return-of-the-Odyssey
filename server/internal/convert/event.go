package convert

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
)

// Event 把领域 game.Event 转成线路消息，返回 MessageType 与具体 protobuf，调用方只需
// 序列化和封帧一次，并负责设置帧头 MessageType。
//
// 未知 EventKind 属于程序错误，本函数明确返回错误而不静默丢弃，让新增领域类型在边界立即暴露。
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
