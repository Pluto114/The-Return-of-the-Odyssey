package convert

import (
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func stats(attack, defense, maxHP, moveSpeed float64) entity.CombatStats {
	return entity.CombatStats{Attack: attack, Defense: defense, MaxHealth: maxHP, MoveSpeed: moveSpeed, AttackCooldownTicks: 6}
}

func TestWorldSnapshotSeparatesSelfFromOthers(t *testing.T) {
	s := game.Snapshot{
		ServerTick: 42,
		Players: []entity.Player{
			{ID: 1, Position: entity.Vec2{X: 1, Y: 2}, Velocity: entity.Vec2{X: 0.1, Y: 0.2},
				LastProcessedInputSeq: 7, BaseStats: stats(20, 5, 100, 5), CurrentStats: stats(20, 5, 100, 5),
				Health: 80, Alive: true, Aim: entity.Vec2{X: 1}},
			{ID: 2, Position: entity.Vec2{X: 3, Y: 4}, CurrentStats: stats(20, 5, 100, 5),
				Health: 100, Alive: true, Aim: entity.Vec2{X: 1}},
		},
		Monsters: []game.MonsterView{
			{ID: 1<<63 | 1, Position: entity.Vec2{X: 5, Y: 6}, Velocity: entity.Vec2{}, Health: 50, MaxHealth: 100, State: entity.MonsterChase},
		},
		Stage: stage.View{Index: 1, Seed: 99, State: stage.Playing, MonstersRemaining: 1},
	}

	out := WorldSnapshot(s, 1)

	if out.ServerTick != 42 {
		t.Errorf("ServerTick = %d, want 42", out.ServerTick)
	}
	if out.LastProcessedInput != 7 {
		t.Errorf("LastProcessedInput = %d, want 7 (self's ack)", out.LastProcessedInput)
	}
	// Self must be player 1.
	if out.Self == nil || out.Self.PlayerId != 1 {
		t.Fatalf("Self = %v, want player 1", out.Self)
	}
	if out.Self.Hp != 80 || out.Self.Alive != true {
		t.Errorf("Self hp/alive = %v/%v, want 80/true", out.Self.Hp, out.Self.Alive)
	}
	// Others must contain only player 2.
	if len(out.Players) != 1 || out.Players[0].PlayerId != 2 {
		t.Fatalf("Players = %v, want only player 2", out.Players)
	}
	// Monsters and stage.
	if len(out.Monsters) != 1 || out.Monsters[0].State != uint32(entity.MonsterChase) {
		t.Fatalf("Monsters = %v, want 1 chasing monster", out.Monsters)
	}
	if out.Stage == nil || out.Stage.Index != 1 || out.Stage.State != uint32(stage.Playing) {
		t.Fatalf("Stage = %v, want index 1 playing", out.Stage)
	}
}

func TestWorldSnapshotAbsentSelf(t *testing.T) {
	s := game.Snapshot{
		ServerTick: 1,
		Players:    []entity.Player{{ID: 2, CurrentStats: stats(1, 1, 1, 1)}},
	}
	out := WorldSnapshot(s, 99) // self not present
	if out.Self != nil {
		t.Errorf("Self = %v, want nil when absent", out.Self)
	}
	if out.LastProcessedInput != 0 {
		t.Errorf("LastProcessedInput = %d, want 0 when self absent", out.LastProcessedInput)
	}
	if len(out.Players) != 1 || out.Players[0].PlayerId != 2 {
		t.Errorf("Players = %v, want player 2 still listed as other", out.Players)
	}
}

func TestPlayerSnapshotFieldMapping(t *testing.T) {
	p := entity.Player{
		ID:           5,
		Position:     entity.Vec2{X: 1.5, Y: -2.5},
		Velocity:     entity.Vec2{X: 0.5, Y: 0.25},
		Aim:          entity.Vec2{X: 0.5, Y: 0.5},
		BaseStats:    stats(30, 10, 200, 6),
		CurrentStats: stats(35, 12, 200, 6.5),
		Health:       150,
		Alive:        true,
		Ammo:         3, MagazineCapacity: 24, ReloadTicksRemaining: 30, ReloadDurationTicks: 45,
	}
	out := PlayerSnapshot(p)
	if out.Ammo != 3 || out.MagazineCapacity != 24 || out.ReloadTicksRemaining != 30 || out.ReloadDurationTicks != 45 {
		t.Fatalf("ammo state lost in snapshot: %v", out)
	}
	if out.PlayerId != 5 {
		t.Errorf("PlayerId = %d, want 5", out.PlayerId)
	}
	if out.Position.X != 1.5 || out.Position.Y != -2.5 {
		t.Errorf("Position = %+v, want {1.5 -2.5}", out.Position)
	}
	if out.Hp != 150 || out.MaxHp != 200 {
		t.Errorf("Hp/MaxHp = %v/%v, want 150/200", out.Hp, out.MaxHp)
	}
	// CurrentStats (resolved), not BaseStats, drive the combat row.
	if out.Attack != 35 || out.Defense != 12 || out.MoveSpeed != 6.5 {
		t.Errorf("stats = %v/%v/%v, want CurrentStats 35/12/6.5", out.Attack, out.Defense, out.MoveSpeed)
	}
}

func TestMonsterSnapshotStateMapping(t *testing.T) {
	cases := []struct {
		in   entity.MonsterState
		want uint32
	}{
		{entity.MonsterIdle, 0},
		{entity.MonsterChase, 1},
		{entity.MonsterAttack, 2},
		{entity.MonsterDead, 3},
	}
	for _, tc := range cases {
		out := MonsterSnapshot(game.MonsterView{ID: 9, State: tc.in})
		if out.State != tc.want {
			t.Errorf("State(%v) = %d, want %d", tc.in, out.State, tc.want)
		}
	}
}
