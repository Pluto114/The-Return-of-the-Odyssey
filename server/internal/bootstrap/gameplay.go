// Package bootstrap assembles validated, immutable dependencies before the
// gameserver starts accepting connections. It performs no work on Room ticks.
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

// Gameplay is the D-owned configuration handoff to A's application lifecycle.
// Its fields are private so callers cannot replace validated values in place.
type Gameplay struct {
	catalog             equipment.Catalog
	rewardDurationTicks uint64
	stageLimit          uint32
	firstStageSeedBase  int64
	planner             director.RuleBasedPlanner
	valid               bool
}

// LoadGameplay loads the equipment catalog once and validates every gameplay
// knob against B's world and Director rules. Failure must abort server startup.
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
