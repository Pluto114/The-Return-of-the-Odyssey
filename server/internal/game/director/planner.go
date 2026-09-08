// Package director defines the pure planning boundary. No difficulty algorithm
// or reward policy is implemented yet; a planner must never mutate World.
package director

import "github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"

type PerformanceMetrics struct {
	ClearTimeSeconds, TeamHPPercent, AverageDPS float64
	DeathCount                                  int
	DamageTaken, EquipmentPower                 float64
}

type Planner interface {
	Generate(previous stage.Plan, performance PerformanceMetrics) (stage.Plan, error)
}
