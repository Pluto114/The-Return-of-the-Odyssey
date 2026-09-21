package game

import (
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

type PickupKind uint8

const (
	HealthPickup PickupKind = iota + 1
	WeaponPickup
)

const (
	pickupRadius    = 0.72
	stagePickupHeal = 30.0
)

// PickupView is full-set snapshot state. EquipmentID is set only for weapons;
// Value is set only for immediate-health pickups.
type PickupView struct {
	ID          entity.ID
	Kind        PickupKind
	Position    entity.Vec2
	EquipmentID equipment.ID
	Value       float64
}

func (w *World) spawnStagePickups(stageIndex uint32) {
	clear(w.pickups)
	width, height := w.config.Max.X-w.config.Min.X, w.config.Max.Y-w.config.Min.Y
	flip := stageIndex%2 == 0
	healthY, weaponY := 0.24, 0.76
	if flip {
		healthY, weaponY = weaponY, healthY
	}
	healthPosition := w.clearSpawnFromCover(entity.Vec2{
		X: w.config.Min.X + width*0.20,
		Y: w.config.Min.Y + height*healthY,
	}, pickupRadius)
	healthID := w.allocateID()
	w.pickups[healthID] = PickupView{ID: healthID, Kind: HealthPickup, Position: healthPosition, Value: stagePickupHeal}

	weaponIDs := make([]equipment.ID, 0, 2)
	for _, id := range w.rewardCatalog.IDs() {
		definition, ok := w.rewardCatalog.Lookup(id)
		if ok && definition.Slot == equipment.Weapon {
			weaponIDs = append(weaponIDs, id)
		}
	}
	if len(weaponIDs) == 0 {
		return
	}
	weaponPosition := w.clearSpawnFromCover(entity.Vec2{
		X: w.config.Min.X + width*0.80,
		Y: w.config.Min.Y + height*weaponY,
	}, pickupRadius)
	weaponID := w.allocateID()
	selected := weaponIDs[(stageIndex-1)%uint32(len(weaponIDs))]
	w.pickups[weaponID] = PickupView{ID: weaponID, Kind: WeaponPickup, Position: weaponPosition, EquipmentID: selected}
}

func (w *World) collectPickups() {
	if w.stage.State != stage.Playing || len(w.pickups) == 0 {
		return
	}
	for _, playerID := range orderedIDs(w.players) {
		player := w.players[playerID]
		if !player.player.Alive {
			continue
		}
		for _, pickupID := range orderedIDs(w.pickups) {
			pickup := w.pickups[pickupID]
			if math.Hypot(player.player.Position.X-pickup.Position.X,
				player.player.Position.Y-pickup.Position.Y) > pickupRadius {
				continue
			}
			switch pickup.Kind {
			case HealthPickup:
				if player.player.Health >= player.player.CurrentStats.MaxHealth {
					continue
				}
				player.player.Health = math.Min(player.player.CurrentStats.MaxHealth,
					player.player.Health+pickup.Value)
			case WeaponPickup:
				loadout, stats, health, err := equipment.Apply(player.player.BaseStats,
					player.player.Health, player.loadout, pickup.EquipmentID,
					w.rewardCatalog, TickRate)
				if err != nil {
					continue
				}
				player.loadout, player.player.CurrentStats, player.player.Health = loadout, stats, health
				player.syncEquipment()
				w.syncMagazine(player)
			default:
				continue
			}
			delete(w.pickups, pickupID)
		}
	}
}
