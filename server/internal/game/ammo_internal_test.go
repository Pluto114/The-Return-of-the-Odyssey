package game

import (
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"testing"
	"time"
)

func TestMagazineReplacementAndDeathDuringReload(t *testing.T) {
	w, err := NewWorld(DefaultConfig(), pickupCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	p := w.players[1]
	p.loadout = equipment.Loadout{WeaponID: 1002}
	w.syncMagazine(p)
	if p.player.MagazineCapacity != 18 || p.player.Ammo != 12 {
		t.Fatal("capacity not derived from equipment")
	}
	startReload(p)
	p.loadout.WeaponID = 1001
	w.syncMagazine(p)
	if p.player.MagazineCapacity != 16 || p.player.ReloadTicksRemaining != 45 {
		t.Fatal("replacement reset reload")
	}
	p.player.Ammo = 16
	p.loadout.WeaponID = 0
	w.syncMagazine(p)
	if p.player.MagazineCapacity != 12 || p.player.Ammo != 12 {
		t.Fatal("shrinking magazine overflow")
	}
	p.player.Alive = false
	w.Step(time.Unix(100, 0))
	if p.player.ReloadTicksRemaining != 0 {
		t.Fatal("death did not cancel reload")
	}
	p.player.Ammo = 0
	startReload(p)
	if p.player.ReloadTicksRemaining != 0 {
		t.Fatal("dead player can reload")
	}
}
