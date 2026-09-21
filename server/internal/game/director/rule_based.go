package director

import (
	"errors"
	"fmt"
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

const (
	// 单关难度最多上调 20%、下调 15%，避免一次异常发挥造成体验骤变。
	MaxDifficultyIncrease = 0.20
	MaxDifficultyDecrease = 0.15
)

var ErrInvalidRuleConfig = errors.New("invalid rule-based director configuration")

type RuleConfig struct {
	Min, Max               entity.Vec2
	PlayerSpawn            entity.Vec2
	MaxMonsters            int
	MinDifficulty          float64
	MaxDifficulty          float64
	TargetClearTimeSeconds float64
	TargetDPS              float64
	TargetDamageTaken      float64
}

func DefaultRuleConfig(min, max, playerSpawn entity.Vec2, maxMonsters int) RuleConfig {
	return RuleConfig{
		Min: min, Max: max, PlayerSpawn: playerSpawn, MaxMonsters: maxMonsters,
		MinDifficulty: 0.5, MaxDifficulty: 10,
		TargetClearTimeSeconds: 45, TargetDPS: 30, TargetDamageTaken: 50,
	}
}

func (c RuleConfig) validate() error {
	for _, value := range []float64{c.MinDifficulty, c.MaxDifficulty, c.TargetClearTimeSeconds, c.TargetDPS, c.TargetDamageTaken} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			return ErrInvalidRuleConfig
		}
	}
	if !c.Min.Finite() || !c.Max.Finite() || !c.PlayerSpawn.Finite() || c.Min.X >= c.Max.X || c.Min.Y >= c.Max.Y ||
		c.PlayerSpawn.X < c.Min.X || c.PlayerSpawn.X > c.Max.X || c.PlayerSpawn.Y < c.Min.Y || c.PlayerSpawn.Y > c.Max.Y ||
		c.MaxMonsters < 1 || c.MaxMonsters > 128 || c.MinDifficulty > c.MaxDifficulty {
		return ErrInvalidRuleConfig
	}
	return nil
}

type RuleBasedPlanner struct{ config RuleConfig }

func NewRuleBasedPlanner(config RuleConfig) (RuleBasedPlanner, error) {
	if err := config.validate(); err != nil {
		return RuleBasedPlanner{}, err
	}
	return RuleBasedPlanner{config: config}, nil
}

type Decision struct {
	// Decision 是给监控面板/答辩解释用的“决策记录”；真正进入 World 的是 stage.Plan。
	// Reasons 保留每条规则的贡献，例如“快速通关 +6%”“有人死亡 -3%”。
	PreviousDifficulty float64
	NewDifficulty      float64
	Adjustment         float64
	MonsterCount       int
	Seed               int64
	Reasons            []string
}

func (p RuleBasedPlanner) Generate(previous stage.Plan, performance PerformanceMetrics) (stage.Plan, error) {
	plan, _, err := p.Decide(previous, performance)
	return plan, err
}

// Decide 按可解释、有限幅度的规则生成下一关。流程如下：
//  1. 校验上一关方案和表现指标，拒绝 NaN/越界数据；
//  2. adjustment 汇总通关速度、血量、死亡、装备归一化 DPS、承伤；
//  3. 将总调整限制在 [-15%, +20%]，再限制到全局难度上下限；
//  4. 按难度平方根调整怪物数量，剩余强度分摊到单只怪物属性；
//  5. 从上一关 seed 确定性地产生新 seed 与出生点，并再次校验完整 Plan。
//
// 这里没有随机全局状态：输入相同就能重现完全相同的关卡，便于定位线上问题。
func (p RuleBasedPlanner) Decide(previous stage.Plan, performance PerformanceMetrics) (stage.Plan, Decision, error) {
	if err := p.config.validate(); err != nil {
		return stage.Plan{}, Decision{}, err
	}
	if err := previous.Validate(p.config.Min, p.config.Max, p.config.MaxMonsters); err != nil {
		return stage.Plan{}, Decision{}, fmt.Errorf("director previous plan: %w", err)
	}
	if previous.DifficultyScore < p.config.MinDifficulty || previous.DifficultyScore > p.config.MaxDifficulty {
		return stage.Plan{}, Decision{}, errors.New("previous difficulty is outside director limits")
	}
	if err := performance.Validate(); err != nil {
		return stage.Plan{}, Decision{}, err
	}

	adjustment, reasons := p.adjustment(performance)
	newDifficulty := previous.DifficultyScore * (1 + adjustment)
	newDifficulty = math.Max(p.config.MinDifficulty, math.Min(p.config.MaxDifficulty, newDifficulty))
	adjustment = newDifficulty/previous.DifficultyScore - 1

	// 怪物数量按难度比的平方根变化，另一半强度交给单体属性承担。
	// 这样不会只靠“堆怪”，也不会只靠“血厚”，关卡压力变化更平滑。
	count := int(math.Round(float64(len(previous.Monsters)) * math.Sqrt(newDifficulty/previous.DifficultyScore)))
	count = max(1, min(p.config.MaxMonsters, count))
	nextIndex := previous.Index + 1
	seedState := uint64(previous.Seed) ^ uint64(nextIndex)*0x9e3779b97f4a7c15
	nextSeed := int64(nextRandom(&seedState))
	positions := p.positions(count, nextSeed)
	perMonsterScale := math.Sqrt((newDifficulty / previous.DifficultyScore) * float64(len(previous.Monsters)) / float64(count))

	plan := stage.Plan{Index: nextIndex, Seed: nextSeed, DifficultyScore: newDifficulty, Monsters: make([]stage.Spawn, count)}
	plan.DifficultyAdjustment = adjustment
	plan.PreviousClearSeconds = performance.ClearTimeSeconds
	plan.PreviousTeamHPPercent = performance.TeamHPPercent
	for i := range plan.Monsters {
		spawn := previous.Monsters[i%len(previous.Monsters)]
		spawn.Position = positions[i]
		spawn.Stats.Attack *= perMonsterScale
		spawn.Stats.MaxHealth *= perMonsterScale
		spawn.Stats.Defense = math.Max(0, (100+spawn.Stats.Defense)*perMonsterScale-100)
		spawn.Stats.MoveSpeed *= math.Pow(perMonsterScale, 0.2)
		plan.Monsters[i] = spawn
	}
	if err := plan.Validate(p.config.Min, p.config.Max, p.config.MaxMonsters); err != nil {
		return stage.Plan{}, Decision{}, fmt.Errorf("director generated plan: %w", err)
	}
	decision := Decision{PreviousDifficulty: previous.DifficultyScore, NewDifficulty: newDifficulty, Adjustment: adjustment,
		MonsterCount: count, Seed: nextSeed, Reasons: reasons}
	return plan, decision, nil
}

func (p RuleBasedPlanner) adjustment(metrics PerformanceMetrics) (float64, []string) {
	// 每关先自然增长 3%，再由玩家表现加减：强队增加压力，困难队降低压力。
	// DPS 会除以 EquipmentPower，避免玩家仅因拿到好装备就被导演过度惩罚。
	adjustment := 0.03
	reasons := []string{"base progression +3%"}
	if metrics.ClearTimeSeconds <= p.config.TargetClearTimeSeconds*0.75 {
		adjustment += 0.06
		reasons = append(reasons, "fast clear +6%")
	} else if metrics.ClearTimeSeconds >= p.config.TargetClearTimeSeconds*1.5 {
		adjustment -= 0.08
		reasons = append(reasons, "slow clear -8%")
	}
	if metrics.TeamHPPercent >= 0.75 {
		adjustment += 0.04
		reasons = append(reasons, "healthy team +4%")
	} else if metrics.TeamHPPercent <= 0.35 {
		adjustment -= 0.06
		reasons = append(reasons, "low team health -6%")
	}
	if metrics.DeathCount > 0 {
		penalty := math.Min(0.06, float64(metrics.DeathCount)*0.03)
		adjustment -= penalty
		reasons = append(reasons, fmt.Sprintf("%d deaths -%.0f%%", metrics.DeathCount, penalty*100))
	}
	normalizedDPS := metrics.AverageDPS / math.Max(0.25, metrics.EquipmentPower)
	if normalizedDPS >= p.config.TargetDPS*1.25 {
		adjustment += 0.04
		reasons = append(reasons, "high equipment-normalized DPS +4%")
	} else if normalizedDPS <= p.config.TargetDPS*0.65 {
		adjustment -= 0.04
		reasons = append(reasons, "low equipment-normalized DPS -4%")
	}
	if metrics.DamageTaken <= p.config.TargetDamageTaken*0.5 {
		adjustment += 0.02
		reasons = append(reasons, "low damage taken +2%")
	} else if metrics.DamageTaken >= p.config.TargetDamageTaken*1.5 {
		adjustment -= 0.03
		reasons = append(reasons, "high damage taken -3%")
	}
	return math.Max(-MaxDifficultyDecrease, math.Min(MaxDifficultyIncrease, adjustment)), reasons
}

func (p RuleBasedPlanner) positions(count int, seed int64) []entity.Vec2 {
	// 出生点由 seed 驱动，不使用并发共享的 math/rand。先随机尝试远离玩家出生点且不重复
	// 的位置；极端小地图下尝试失败，则使用黄金比例构造的确定性后备位置。
	width, height := p.config.Max.X-p.config.Min.X, p.config.Max.Y-p.config.Min.Y
	marginX, marginY := width*0.08, height*0.08
	usableWidth, usableHeight := width-2*marginX, height-2*marginY
	minimumSpawnDistance := math.Min(width, height) * 0.15
	state := uint64(seed)
	positions := make([]entity.Vec2, 0, count)
	seen := make(map[[2]uint64]bool, count)
	for i := 0; i < count; i++ {
		var position entity.Vec2
		accepted := false
		for range 1024 {
			position = entity.Vec2{X: p.config.Min.X + marginX + randomUnit(&state)*usableWidth, Y: p.config.Min.Y + marginY + randomUnit(&state)*usableHeight}
			key := [2]uint64{math.Float64bits(position.X), math.Float64bits(position.Y)}
			if !seen[key] && math.Hypot(position.X-p.config.PlayerSpawn.X, position.Y-p.config.PlayerSpawn.Y) >= minimumSpawnDistance {
				seen[key] = true
				accepted = true
				break
			}
		}
		if !accepted {
			// X 比例唯一，因此即使地图很窄，确定性后备点也不会彼此重复。
			fraction := float64(i+1) / float64(count+1)
			position = entity.Vec2{X: p.config.Min.X + marginX + fraction*usableWidth, Y: p.config.Min.Y + marginY + math.Mod(fraction*0.6180339887498949, 1)*usableHeight}
		}
		positions = append(positions, position)
	}
	return positions
}

func randomUnit(state *uint64) float64 {
	return float64(nextRandom(state)>>11) * (1.0 / (1 << 53))
}

func nextRandom(state *uint64) uint64 {
	*state += 0x9e3779b97f4a7c15
	z := *state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
