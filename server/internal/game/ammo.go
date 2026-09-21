package game

const (
	BaseMagazineCapacity uint32 = 12
	ReloadDurationTicks  uint32 = 45 // 30Hz 下为 1.5 秒；备用弹匣数量不限。
)

func startReload(p *playerState) {
	if p.player.Alive && p.player.Ammo < p.player.MagazineCapacity && p.player.ReloadTicksRemaining == 0 {
		p.player.ReloadTicksRemaining = ReloadDurationTicks
	}
}

func (w *World) syncMagazine(p *playerState) {
	p.player.MagazineCapacity = w.rewardCatalog.MagazineCapacity(p.loadout, BaseMagazineCapacity)
	// 装备只改变容量，不免费补弹；正在进行的换弹要等服务端计时结束后才装满新容量。
	p.player.Ammo = min(p.player.Ammo, p.player.MagazineCapacity)
}
