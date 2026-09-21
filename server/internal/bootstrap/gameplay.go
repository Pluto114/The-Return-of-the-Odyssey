// Package bootstrap 在服务端接收连接前组装已校验的不可变依赖，不参与 Room Tick。
package bootstrap

import (
	"fmt"
	"os"
	"strings"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/config"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
)

const EquipmentCatalogVersion uint32 = 1

// Gameplay 是交给 application 生命周期的玩法配置；字段私有，调用方不能原地替换已校验值。
type Gameplay struct {
	catalog             equipment.Catalog
	rewardDurationTicks uint64
	stageLimit          uint32
	firstStageSeedBase  int64
	planner             director.RuleBasedPlanner
	valid               bool
}

// LoadGameplay 一次性加载装备目录，并按 World 与导演规则校验全部玩法参数；失败必须中止启动。
func LoadGameplay(cfg *config.Config, world game.Config) (Gameplay, error) {
	if cfg == nil {
		return Gameplay{}, fmt.Errorf("bootstrap gameplay: nil config")
	}
	if err := cfg.Validate(); err != nil {
		return Gameplay{}, err
	}
	if err := world.Validate(); err != nil {
		return Gameplay{}, fmt.Errorf("bootstrap gameplay: world config: %w", err)
	}

	file, err := os.Open(cfg.EquipmentCatalogPath)
	if err != nil {
		return Gameplay{}, fmt.Errorf("bootstrap gameplay: open equipment catalog %q: %w", cfg.EquipmentCatalogPath, err)
	}
	catalog, parseErr := equipment.Parse(file)
	closeErr := file.Close()
	if parseErr != nil {
		return Gameplay{}, fmt.Errorf("bootstrap gameplay: parse equipment catalog %q: %w", cfg.EquipmentCatalogPath, parseErr)
	}
	if closeErr != nil {
		return Gameplay{}, fmt.Errorf("bootstrap gameplay: close equipment catalog %q: %w", cfg.EquipmentCatalogPath, closeErr)
	}
	if catalog.Version() != EquipmentCatalogVersion {
		return Gameplay{}, fmt.Errorf("bootstrap gameplay: equipment catalog version %d, want %d", catalog.Version(), EquipmentCatalogVersion)
	}
	for _, id := range catalog.IDs() {
		definition, ok := catalog.Lookup(id)
		if !ok {
			return Gameplay{}, fmt.Errorf("bootstrap gameplay: equipment %d disappeared from catalog", id)
		}
		if strings.TrimSpace(definition.Description) == "" {
			return Gameplay{}, fmt.Errorf("bootstrap gameplay: equipment %d description is required", id)
		}
		if strings.ContainsAny(definition.Name, "\t\r\n") || strings.ContainsAny(string(definition.Slot), "\t\r\n") || strings.ContainsAny(definition.Description, "\t\r\n") {
			return Gameplay{}, fmt.Errorf("bootstrap gameplay: equipment %d display fields contain tabs or newlines", id)
		}
	}

	ruleConfig := director.DefaultRuleConfig(world.Min, world.Max, world.Spawn, world.Combat.MaxMonsters)
	ruleConfig.MinDifficulty = cfg.DirectorMinDifficulty
	ruleConfig.MaxDifficulty = cfg.DirectorMaxDifficulty
	ruleConfig.TargetClearTimeSeconds = cfg.DirectorTargetClearTimeSec
	ruleConfig.TargetDPS = cfg.DirectorTargetDPS
	ruleConfig.TargetDamageTaken = cfg.DirectorTargetDamageTaken
	planner, err := director.NewRuleBasedPlanner(ruleConfig)
	if err != nil {
		return Gameplay{}, fmt.Errorf("bootstrap gameplay: director config: %w", err)
	}

	return Gameplay{
		catalog:             catalog,
		rewardDurationTicks: uint64(cfg.RewardDurationSec) * uint64(game.TickRate),
		stageLimit:          uint32(cfg.StageLimit),
		firstStageSeedBase:  cfg.FirstStageSeedBase,
		planner:             planner,
		valid:               true,
	}, nil
}

func (g Gameplay) Valid() bool { return g.valid }

func (g Gameplay) Catalog() equipment.Catalog { return g.catalog }

func (g Gameplay) RewardDurationTicks() uint64 { return g.rewardDurationTicks }

func (g Gameplay) StageLimit() uint32 { return g.stageLimit }

func (g Gameplay) FirstStageSeed(roomID uint64) int64 {
	return g.firstStageSeedBase + int64(roomID)
}

func (g Gameplay) Director() director.RuleBasedPlanner { return g.planner }
