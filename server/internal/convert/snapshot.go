package convert

import (
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

// 本文件负责出站“领域对象 -> 线路对象”的快照映射。快照是完整状态，允许 latest-wins
// 丢弃旧帧，与 events.go 中必须可靠送达的事件不同。

// vec2f 把领域 float64 坐标收窄为线路 float32。不能精确表示的值会损失精度，这是协议
// 接受的特性（客户端本就以 float32 渲染）；发布前领域层已校验数值有限性。
func vec2f(v entity.Vec2) *protocol.Vec2 {
	return &protocol.Vec2{X: float32(v.X), Y: float32(v.Y)}
}

// PlayerSnapshot 把领域玩家转成线路记录，包含 HUD 与预测所需字段。CurrentStats 已合并
// 修正，用于攻击/防御/移速；Health 用于当前与最大生命显示。
func PlayerSnapshot(p entity.Player) *protocol.PlayerSnapshot {
	return &protocol.PlayerSnapshot{
		PlayerId:             uint64(p.ID),
		Position:             vec2f(p.Position),
		Velocity:             vec2f(p.Velocity),
		Aim:                  vec2f(p.Aim),
		Hp:                   float32(p.Health),
		MaxHp:                float32(p.CurrentStats.MaxHealth),
		Attack:               float32(p.CurrentStats.Attack),
		Defense:              float32(p.CurrentStats.Defense),
		MoveSpeed:            float32(p.CurrentStats.MoveSpeed),
		Alive:                p.Alive,
		WeaponId:             p.Equipment.WeaponID,
		RelicId:              p.Equipment.RelicID,
		PotionId:             p.Equipment.PotionID,
		Ammo:                 p.Ammo,
		MagazineCapacity:     p.MagazineCapacity,
		ReloadTicksRemaining: p.ReloadTicksRemaining,
		ReloadDurationTicks:  p.ReloadDurationTicks,
	}
}

func PickupSnapshot(p game.PickupView) *protocol.PickupSnapshot {
	kind := protocol.PickupKind_PICKUP_KIND_UNSPECIFIED
	if p.Kind == game.HealthPickup {
		kind = protocol.PickupKind_PICKUP_KIND_HEALTH
	} else if p.Kind == game.WeaponPickup {
		kind = protocol.PickupKind_PICKUP_KIND_WEAPON
	}
	return &protocol.PickupSnapshot{PickupId: uint64(p.ID), Kind: kind,
		Position: vec2f(p.Position), EquipmentId: uint32(p.EquipmentID), Value: float32(p.Value)}
}

// MonsterSnapshot 把 MonsterView 转成线路记录，只暴露渲染和生命字段，不泄露目标或 AI
// 内部数据；State 沿用 entity.MonsterState 原始枚举。
func MonsterSnapshot(m game.MonsterView) *protocol.MonsterSnapshot {
	return &protocol.MonsterSnapshot{
		MonsterId: uint64(m.ID),
		Position:  vec2f(m.Position),
		Velocity:  vec2f(m.Velocity),
		Hp:        float32(m.Health),
		MaxHp:     float32(m.MaxHealth),
		State:     uint32(m.State),
	}
}

// StageState 把领域 stage.View 转成线路记录，State 沿用 stage.State 原始枚举。
func StageState(v stage.View) *protocol.StageState {
	return &protocol.StageState{
		Index:                 v.Index,
		Seed:                  v.Seed,
		State:                 uint32(v.State),
		MonstersRemaining:     uint32(v.MonstersRemaining),
		StageLimit:            v.StageLimit,
		DifficultyScore:       float32(v.DifficultyScore),
		DifficultyAdjustment:  float32(v.DifficultyAdjustment),
		PreviousClearSeconds:  float32(v.PreviousClearSeconds),
		PreviousTeamHpPercent: float32(v.PreviousTeamHPPercent),
	}
}

// WorldSnapshot 把完整领域快照转换成面向单个接收者的线路快照。selfID 指定接收者本人，
// 其记录放入 Self，LastProcessedInputSeq 提升为顶层 LastProcessedInput 供客户端校正；
// 其他玩家进入 Players，顺序已经由 World 按 ID 排好。
//
// 若快照中不存在 selfID（例如玩家在 Tick 中途离开），Self 为 nil 且确认序号为 0；
// 分发器决定是否丢弃该帧，客户端看到无 Self 时应认为房间已失效。
func WorldSnapshot(s game.Snapshot, selfID entity.ID) *protocol.WorldSnapshot {
	out := &protocol.WorldSnapshot{
		ServerTick: s.ServerTick,
		Stage:      StageState(s.Stage),
		Monsters:   make([]*protocol.MonsterSnapshot, 0, len(s.Monsters)),
		Players:    make([]*protocol.PlayerSnapshot, 0, len(s.Players)),
		Pickups:    make([]*protocol.PickupSnapshot, 0, len(s.Pickups)),
	}
	for _, m := range s.Monsters {
		out.Monsters = append(out.Monsters, MonsterSnapshot(m))
	}
	for _, pickup := range s.Pickups {
		out.Pickups = append(out.Pickups, PickupSnapshot(pickup))
	}
	for _, p := range s.Players {
		row := PlayerSnapshot(p)
		if p.ID == selfID {
			out.Self = row
			out.LastProcessedInput = p.LastProcessedInputSeq
			continue
		}
		out.Players = append(out.Players, row)
	}
	return out
}
