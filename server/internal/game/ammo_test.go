package game_test

import (
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"testing"
	"time"
)

func TestMagazineRequiresManualReload(t *testing.T) {
	w := encounter(t, game.DefaultConfig(), target(18, 18, 10000))
	shots := 0
	for seq := uint32(1); seq <= 100; seq++ {
		shots += countEvents(stepInput(t, w, game.Input{Seq: seq, Aim: entity.Vec2{X: 1}, Shoot: true}), game.ProjectileSpawned)
	}
	if shots != 12 || w.Snapshot().Players[0].Ammo != 0 {
		t.Fatalf("shots/ammo = %d/%d", shots, w.Snapshot().Players[0].Ammo)
	}
	before := w.Snapshot().Players[0].Position
	for tick := uint32(0); tick < game.ReloadDurationTicks; tick++ {
		events := stepInput(t, w, game.Input{Seq: 101 + tick, Direction: entity.Vec2{Y: 1}, Aim: entity.Vec2{X: 1}, Shoot: true, Reload: true})
		if countEvents(events, game.ProjectileSpawned) != 0 {
			t.Fatal("shot during reload")
		}
		p := w.Snapshot().Players[0]
		if p.ReloadTicksRemaining != game.ReloadDurationTicks-tick-1 {
			t.Fatalf("reload timer reset: %+v", p)
		}
		if tick < game.ReloadDurationTicks-1 && p.Ammo != 0 {
			t.Fatal("refilled early")
		}
	}
	p := w.Snapshot().Players[0]
	if p.Ammo != 12 || p.Position == before {
		t.Fatalf("reload/movement failed: %+v", p)
	}
	events := stepInput(t, w, game.Input{Seq: 146, Aim: entity.Vec2{X: 1}, Shoot: true, Reload: true})
	if countEvents(events, game.ProjectileSpawned) != 1 || w.Snapshot().Players[0].Ammo != 11 {
		t.Fatal("full-mag reload must not prevent firing")
	}
}

func TestReloadOneShotSurvivesInputCoalescing(t *testing.T) {
	w := encounter(t, game.DefaultConfig(), target(18, 18, 10000))
	stepInput(t, w, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true})
	now := time.Unix(100, 0).Add(time.Duration(w.Tick()) * game.TickInterval)
	if err := w.ApplyInput(1, game.Input{Seq: 2, Reload: true}, now); err != nil {
		t.Fatal(err)
	}
	if err := w.ApplyInput(1, game.Input{Seq: 3}, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	if p := w.Snapshot().Players[0]; p.ReloadTicksRemaining != 44 || p.LastProcessedInputSeq != 3 {
		t.Fatalf("lost one-shot: %+v", p)
	}
	for i := 0; i < 60; i++ {
		now = now.Add(game.TickInterval)
		w.Step(now)
		w.TakeEvents()
	}
	p := w.Snapshot().Players[0]
	if p.ReloadTicksRemaining != 0 || p.Ammo != 12 {
		t.Fatalf("reload stuck/repeated without fresh input: %+v", p)
	}
}
