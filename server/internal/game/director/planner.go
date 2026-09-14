// Package director defines the pure planning boundary. A planner never mutates
// World; callers submit its fully validated StagePlan through Room commands.
package director

import (
	"errors"
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

type PerformanceMetrics struct {
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
	Generate(previous stage.Plan, performance PerformanceMetrics) (stage.Plan, error)
}
