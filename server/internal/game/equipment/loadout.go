package equipment

import (
	"errors"
	"fmt"
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

var (
	ErrInvalidLoadout = errors.New("invalid equipment loadout")
	ErrInvalidStats   = errors.New("equipment produced invalid stats")
	ErrNoPotion       = errors.New("no potion equipped")
	ErrInvalidHealth  = errors.New("invalid player health")
)

// Loadout 的每个逻辑槽位最多有一件可替换物品，0 表示空槽。
type Loadout struct {
	WeaponID ID
	RelicID  ID
	PotionID ID
}

// MagazineCapacity 只从静态装备数据推导，绝不采用客户端输入。
func (c Catalog) MagazineCapacity(loadout Loadout, base uint32) uint32 {
	capacity := uint64(base)
	for _, id := range []ID{loadout.WeaponID, loadout.RelicID} {
		if definition, ok := c.Lookup(id); ok {
			capacity += uint64(definition.MagazineBonus)
		}
	}
	return uint32(min(capacity, 120))
}

func (l Loadout) Equip(definition Definition) (Loadout, error) {
	if err := definition.validate(); err != nil {
		return l, err
	}
	switch definition.Slot {
	case Weapon:
		l.WeaponID = definition.ID
	case Relic:
		l.RelicID = definition.ID
	case Potion:
		l.PotionID = definition.ID
	default:
		return l, ErrInvalidLoadout
	}
	return l, nil
}

// Resolve 始终从 BaseStats 重建有效属性；槽位顺序为武器、遗物，每件装备的修正按文件顺序执行。
func Resolve(base entity.CombatStats, loadout Loadout, catalog Catalog, tickRate uint32) (entity.CombatStats, error) {
	if !base.Valid() || tickRate == 0 {
		return entity.CombatStats{}, ErrInvalidStats
	}
	resolved := base
	attackSpeed := float64(tickRate) / float64(base.AttackCooldownTicks)
	for _, slot := range []struct {
		id   ID
		want Slot
	}{{loadout.WeaponID, Weapon}, {loadout.RelicID, Relic}} {
		if slot.id == 0 {
			continue
		}
		definition, err := catalog.Require(slot.id)
		if err != nil || definition.Slot != slot.want {
			return entity.CombatStats{}, fmt.Errorf("%w: %d is not %s", ErrInvalidLoadout, slot.id, slot.want)
		}
		for _, modifier := range definition.Modifiers {
			var target *float64
			switch modifier.Stat {
			case Attack:
				target = &resolved.Attack
			case Defense:
				target = &resolved.Defense
			case MaxHealth:
				target = &resolved.MaxHealth
			case MoveSpeed:
				target = &resolved.MoveSpeed
			case AttackSpeed:
				target = &attackSpeed
			}
			if modifier.Operation == Add {
				*target += modifier.Value
			} else {
				*target *= modifier.Value
			}
		}
	}
	if !finitePositive(attackSpeed) {
		return entity.CombatStats{}, ErrInvalidStats
	}
	cooldown := math.Round(float64(tickRate) / attackSpeed)
	if cooldown < 1 {
		cooldown = 1
	}
	if cooldown > math.MaxUint32 {
		return entity.CombatStats{}, ErrInvalidStats
	}
	resolved.AttackCooldownTicks = uint32(cooldown)
	if !resolved.Valid() {
		return entity.CombatStats{}, ErrInvalidStats
	}
	return resolved, nil
}

// Apply 替换目标槽位并校验完整新配装。最大生命提高不会回血，降低时当前生命会限制到新上限。
func Apply(base entity.CombatStats, currentHealth float64, loadout Loadout, selected ID, catalog Catalog, tickRate uint32) (Loadout, entity.CombatStats, float64, error) {
	definition, err := catalog.Require(selected)
	if err != nil {
		return loadout, entity.CombatStats{}, currentHealth, err
	}
	next, err := loadout.Equip(definition)
	if err != nil {
		return loadout, entity.CombatStats{}, currentHealth, err
	}
	stats, err := Resolve(base, next, catalog, tickRate)
	if err != nil {
		return loadout, entity.CombatStats{}, currentHealth, err
	}
	if math.IsNaN(currentHealth) || math.IsInf(currentHealth, 0) || currentHealth < 0 {
		return loadout, entity.CombatStats{}, currentHealth, ErrInvalidHealth
	}
	return next, stats, math.Min(currentHealth, stats.MaxHealth), nil
}

// UsePotion 恰好消耗一次已装备药剂，治疗不超过当前有效上限；满血使用仍合法且会消耗。
func UsePotion(loadout Loadout, health, maxHealth float64, catalog Catalog) (Loadout, float64, ID, error) {
	if loadout.PotionID == 0 {
		return loadout, health, 0, ErrNoPotion
	}
	if math.IsNaN(health) || math.IsInf(health, 0) || math.IsNaN(maxHealth) || math.IsInf(maxHealth, 0) || health < 0 || maxHealth <= 0 || health > maxHealth {
		return loadout, health, 0, ErrInvalidHealth
	}
	definition, err := catalog.Require(loadout.PotionID)
	if err != nil || definition.Slot != Potion {
		return loadout, health, 0, ErrInvalidLoadout
	}
	consumed := loadout.PotionID
	loadout.PotionID = 0
	return loadout, math.Min(maxHealth, health+definition.Heal), consumed, nil
}
