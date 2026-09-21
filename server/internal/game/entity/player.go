// Package entity 定义与协议无关的游戏状态。Room 拥有实时实体；发布给其他 goroutine 前必须复制。
package entity

import "math"

type ID uint64

// Vec2 使用服务端 X/Y 坐标，对应客户端 X/Z。
type Vec2 struct {
	X, Y float64
}

func (v Vec2) Finite() bool {
	return !math.IsNaN(v.X) && !math.IsNaN(v.Y) && !math.IsInf(v.X, 0) && !math.IsInf(v.Y, 0)
}

// Player 是领域值而非网络 DTO。确认序号为 0 表示尚未模拟任何输入；输入序号从 1 开始且不回绕。
type Player struct {
	ID                      ID
	Position                Vec2
	Velocity                Vec2
	LastProcessedInputSeq   uint32
	BaseStats, CurrentStats CombatStats
	Health                  float64
	Alive                   bool
	Aim                     Vec2
	Equipment               EquipmentState
	Ammo                    uint32
	MagazineCapacity        uint32
	ReloadTicksRemaining    uint32
	ReloadDurationTicks     uint32
}

// EquipmentState 在权威快照中保存稳定静态数据 ID；0 表示对应逻辑槽为空。
type EquipmentState struct {
	WeaponID uint32
	RelicID  uint32
	PotionID uint32
}
