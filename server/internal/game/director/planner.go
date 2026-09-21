// Package director 实现“AI 导演”：它不控制单只怪物，而是在一关结束后读取团队表现，
// 生成下一关的难度、怪物数量、属性和出生点。
//
// 导演是纯规划器，不直接修改 World。相同配置、上一关 Plan 和表现指标一定得到相同结果；
// 生成的新 Plan 还要通过 Room 命令进入权威模拟。这样既便于测试，也避免导演 goroutine
// 与房间 Tick 并发写世界状态。
package director

import (
	"errors"
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

type PerformanceMetrics struct {
	// ClearTimeSeconds：通关耗时；TeamHPPercent：通关时全队剩余生命比例；
	// AverageDPS：全队平均秒伤；DeathCount/DamageTaken：容错表现；
	// EquipmentPower：装备带来的综合倍率，用来区分“装备强”与“操作强”。
	ClearTimeSeconds, TeamHPPercent, AverageDPS float64
	DeathCount                                  int
	DamageTaken, EquipmentPower                 float64
}

func (m PerformanceMetrics) Validate() error {
	for _, value := range []float64{m.ClearTimeSeconds, m.TeamHPPercent, m.AverageDPS, m.DamageTaken, m.EquipmentPower} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("performance metrics must be finite")
		}
	}
	if m.ClearTimeSeconds <= 0 || m.TeamHPPercent < 0 || m.TeamHPPercent > 1 || m.AverageDPS < 0 || m.DeathCount < 0 || m.DeathCount > 128 || m.DamageTaken < 0 || m.EquipmentPower <= 0 {
		return errors.New("performance metrics are out of range")
	}
	return nil
}

type Planner interface {
	// Generate 只负责计算并返回不可变关卡方案，不产生网络或游戏状态副作用。
	Generate(previous stage.Plan, performance PerformanceMetrics) (stage.Plan, error)
}
