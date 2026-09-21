package convert

import (
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

// This file owns the outbound (domain -> wire) snapshot mapping. Snapshots are
// full-state and lossy-tolerant (Latest Wins on the session queue), unlike the
// reliable events in events.go.

// vec2f narrows a domain entity.Vec2 (float64) to the wire Vec2 (float32). The
// narrowing is lossy for values not exactly representable in float32; this is
// an accepted protocol property (the client renders at float32 precision). The
// domain layer has already validated finiteness before publication.
func vec2f(v entity.Vec2) *protocol.Vec2 {
	return &protocol.Vec2{X: float32(v.X), Y: float32(v.Y)}
}

// PlayerSnapshot converts a domain player into its wire row. It includes
// everything the HUD and prediction engine need. CurrentStats (already resolved
// with modifiers) drives attack/defense/move-speed; Health drives hp/max_hp.
func PlayerSnapshot(p entity.Player) *protocol.PlayerSnapshot {
	return &protocol.PlayerSnapshot{
		PlayerId:             uint64(p.ID),
		Position:             vec2f(p.Position),
		Velocity:             vec2f(p.Velocity),
		Aim:                  vec2f(p.Aim),
		Hp:                   float32(p.Health),
		MaxHp:                float32(p.CurrentStats.MaxHealth),
		Attack:               float32(p.CurrentStats.Attack),
		Defense:              float32(p.CurrentStats.Defense),
		MoveSpeed:            float32(p.CurrentStats.MoveSpeed),
		Alive:                p.Alive,
		WeaponId:             p.Equipment.WeaponID,
		RelicId:              p.Equipment.RelicID,
		PotionId:             p.Equipment.PotionID,
		Ammo:                 p.Ammo,
		MagazineCapacity:     p.MagazineCapacity,
		ReloadTicksRemaining: p.ReloadTicksRemaining,
		ReloadDurationTicks:  p.ReloadDurationTicks,
	}
}

func PickupSnapshot(p game.PickupView) *protocol.PickupSnapshot {
	kind := protocol.PickupKind_PICKUP_KIND_UNSPECIFIED
	if p.Kind == game.HealthPickup {
		kind = protocol.PickupKind_PICKUP_KIND_HEALTH
	} else if p.Kind == game.WeaponPickup {
		kind = protocol.PickupKind_PICKUP_KIND_WEAPON
	}
	return &protocol.PickupSnapshot{PickupId: uint64(p.ID), Kind: kind,
		Position: vec2f(p.Position), EquipmentId: uint32(p.EquipmentID), Value: float32(p.Value)}
}

// MonsterSnapshot converts a domain MonsterView into its wire row. Only render
// and HP fields; no target/AI internals. State is the raw entity.MonsterState
// (0 idle / 1 chase / 2 attack / 3 dead), aligned with the proto comment.
func MonsterSnapshot(m game.MonsterView) *protocol.MonsterSnapshot {
	return &protocol.MonsterSnapshot{
		MonsterId: uint64(m.ID),
		Position:  vec2f(m.Position),
		Velocity:  vec2f(m.Velocity),
		Hp:        float32(m.Health),
		MaxHp:     float32(m.MaxHealth),
		State:     uint32(m.State),
	}
}

// StageState converts a domain stage.View into its wire row. State is the raw
// stage.State (0 waiting / 1 playing / 2 clear / 3 reward / 4 preparing /
// 5 failed / 6 closed), aligned with the proto comment.
func StageState(v stage.View) *protocol.StageState {
	return &protocol.StageState{
		Index:                 v.Index,
		Seed:                  v.Seed,
		State:                 uint32(v.State),
		MonstersRemaining:     uint32(v.MonstersRemaining),
		StageLimit:            v.StageLimit,
		DifficultyScore:       float32(v.DifficultyScore),
		DifficultyAdjustment:  float32(v.DifficultyAdjustment),
		PreviousClearSeconds:  float32(v.PreviousClearSeconds),
		PreviousTeamHpPercent: float32(v.PreviousTeamHPPercent),
	}
}

// WorldSnapshot converts a full domain snapshot into a per-recipient wire
// snapshot. selfID selects which player is the recipient's "self": that player
// is placed in Self and its LastProcessedInputSeq becomes the top-level
// LastProcessedInput for client reconciliation. All other players go into
// Players (already sorted by player ID ascending by the world).
//
// If selfID is absent from the snapshot (e.g. the player left mid-tick), Self
// is nil and LastProcessedInput is 0; the caller may still want to send the
// remaining world state, but a client with no self row should treat the room
// as gone. The dispatcher decides whether to drop such a frame.
func WorldSnapshot(s game.Snapshot, selfID entity.ID) *protocol.WorldSnapshot {
	out := &protocol.WorldSnapshot{
		ServerTick: s.ServerTick,
		Stage:      StageState(s.Stage),
		Monsters:   make([]*protocol.MonsterSnapshot, 0, len(s.Monsters)),
		Players:    make([]*protocol.PlayerSnapshot, 0, len(s.Players)),
		Pickups:    make([]*protocol.PickupSnapshot, 0, len(s.Pickups)),
	}
	for _, m := range s.Monsters {
		out.Monsters = append(out.Monsters, MonsterSnapshot(m))
	}
	for _, pickup := range s.Pickups {
		out.Pickups = append(out.Pickups, PickupSnapshot(pickup))
	}
	for _, p := range s.Players {
		row := PlayerSnapshot(p)
		if p.ID == selfID {
			out.Self = row
			out.LastProcessedInput = p.LastProcessedInputSeq
			continue
		}
		out.Players = append(out.Players, row)
	}
	return out
}
