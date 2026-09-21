package game

const (
	BaseMagazineCapacity uint32 = 12
	ReloadDurationTicks  uint32 = 45 // 1.5 seconds at 30 Hz; spare magazines are unlimited.
)

func startReload(p *playerState) {
	if p.player.Alive && p.player.Ammo < p.player.MagazineCapacity && p.player.ReloadTicksRemaining == 0 {
		p.player.ReloadTicksRemaining = ReloadDurationTicks
	}
}

func (w *World) syncMagazine(p *playerState) {
	p.player.MagazineCapacity = w.rewardCatalog.MagazineCapacity(p.loadout, BaseMagazineCapacity)
	// Equipment changes capacity but grants no free ammunition. An active
	// reload fills the new capacity only when its server timer completes.
	p.player.Ammo = min(p.player.Ammo, p.player.MagazineCapacity)
}
