// Package stage 定义服务端拥有的关卡方案而非线路消息；奖励、导演和下一关编排位于类型之外。
package stage

import (
	"errors"
	"math"
	"slices"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

type State uint8

const (
	Waiting State = iota
	Playing
	StageClear
	Reward
	PreparingNextStage
	Failed
	Closed
)

type Spawn struct {
	Position            entity.Vec2
	Stats               entity.CombatStats
	Radius, AttackRange float64
}

type Plan struct {
	Index                 uint32
	Seed                  int64
	DifficultyScore       float64
	Monsters              []Spawn
	DifficultyAdjustment  float64
	PreviousClearSeconds  float64
	PreviousTeamHPPercent float64
}

func (p Plan) Clone() Plan { p.Monsters = slices.Clone(p.Monsters); return p }

func (p Plan) Validate(min, max entity.Vec2, maxMonsters int) error {
	if p.Index == 0 || len(p.Monsters) == 0 || len(p.Monsters) > maxMonsters ||
		math.IsNaN(p.DifficultyScore) || math.IsInf(p.DifficultyScore, 0) || p.DifficultyScore <= 0 {
		return errors.New("invalid stage plan")
	}
	for _, m := range p.Monsters {
		if !m.Position.Finite() || !m.Stats.Valid() || m.Position.X < min.X || m.Position.X > max.X ||
			m.Position.Y < min.Y || m.Position.Y > max.Y || math.IsNaN(m.Radius) || math.IsInf(m.Radius, 0) ||
			m.Radius <= 0 || m.Radius > 10 || math.IsNaN(m.AttackRange) || math.IsInf(m.AttackRange, 0) || m.AttackRange < 0 || m.AttackRange > 100 {
			return errors.New("invalid monster spawn")
		}
	}
	return nil
}

type View struct {
	Index                 uint32
	Seed                  int64
	State                 State
	MonstersRemaining     int
	StageLimit            uint32
	DifficultyScore       float64
	DifficultyAdjustment  float64
	PreviousClearSeconds  float64
	PreviousTeamHPPercent float64
}
