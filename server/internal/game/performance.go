package game

import (
	"errors"
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

var ErrPerformanceUnavailable = errors.New("performance metrics are available only after stage clear")

type StageResult struct {
	Plan        stage.Plan
	Performance director.PerformanceMetrics
}

func (r StageResult) Clone() StageResult {
	r.Plan = r.Plan.Clone()
	return r
}

type stagePerformance struct {
	// 本关进行时逐项累加；只有 StageClear 时才 freeze，之后导演读取的值不再变化。
	startedAtTick  uint64
	damageDealt    float64
	damageTaken    float64
	deathCount     int
	equipmentPower float64
	frozen         director.PerformanceMetrics
	ready          bool
}

func (w *World) freezePerformance() {
	// 使用权威 tick 计算耗时，而不是客户端上报的时钟；伤害、死亡、装备强度也都来自
	// World 内部结算，因此客户端无法伪造表现来操纵下一关难度。
	elapsedTicks := w.tick - w.performance.startedAtTick
	clearTime := float64(elapsedTicks) / TickRate
	if clearTime <= 0 {
		clearTime = StepSeconds
	}
	var health, maxHealth float64
	for _, player := range w.players {
		health += player.player.Health
		maxHealth += player.player.CurrentStats.MaxHealth
	}
	teamHP := 0.0
	if maxHealth > 0 {
		teamHP = health / maxHealth
	}
	w.performance.frozen = director.PerformanceMetrics{
		ClearTimeSeconds: clearTime,
		TeamHPPercent:    teamHP,
		AverageDPS:       w.performance.damageDealt / clearTime,
		DeathCount:       w.performance.deathCount,
		DamageTaken:      w.performance.damageTaken,
		EquipmentPower:   w.performance.equipmentPower,
	}
	w.performance.ready = true
	w.completedStages = append(w.completedStages, StageSummary{Index: w.currentPlan.Index, Seed: w.currentPlan.Seed,
		DifficultyScore: w.currentPlan.DifficultyScore, ClearTick: w.tick, Performance: w.performance.frozen})
}

func (w *World) PerformanceMetrics() (director.PerformanceMetrics, error) {
	// 只允许在本关已经明确结束的阶段读取，避免导演拿到仍在变化的“半成品”指标。
	if !w.performance.ready || (w.stage.State != stage.StageClear && w.stage.State != stage.Reward && w.stage.State != stage.PreparingNextStage) {
		return director.PerformanceMetrics{}, ErrPerformanceUnavailable
	}
	return w.performance.frozen, nil
}

func (w *World) CompletedStage() (StageResult, error) {
	performance, err := w.PerformanceMetrics()
	if err != nil {
		return StageResult{}, err
	}
	return StageResult{Plan: w.currentPlan.Clone(), Performance: performance}, nil
}

func (w *World) equipmentPower() float64 {
	// 将攻击、防御、生命、移速、攻速分别换算成相对基础属性的倍率，再取团队平均值。
	// 导演用它归一化 DPS：装备提升仍有爽感，但不会被误判成玩家操作突然变强。
	if len(w.players) == 0 {
		return 1
	}
	total := 0.0
	for _, player := range w.players {
		base, current := player.player.BaseStats, player.player.CurrentStats
		ratios := [...]float64{
			statRatio(current.Attack, base.Attack),
			(100 + current.Defense) / (100 + base.Defense),
			statRatio(current.MaxHealth, base.MaxHealth),
			statRatio(current.MoveSpeed, base.MoveSpeed),
			float64(base.AttackCooldownTicks) / float64(current.AttackCooldownTicks),
		}
		for _, ratio := range ratios {
			total += ratio
		}
	}
	return total / float64(len(w.players)*5)
}

func statRatio(current, base float64) float64 {
	if math.Abs(base) < 1e-12 {
		if math.Abs(current) < 1e-12 {
			return 1
		}
		return 1 + current
	}
	return current / base
}
