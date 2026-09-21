package systems

import (
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

// UnitDirection 也能处理次正规瞄准向量而不发生下溢。
func UnitDirection(v entity.Vec2) entity.Vec2 {
	scale := math.Max(math.Abs(v.X), math.Abs(v.Y))
	if scale == 0 {
		return entity.Vec2{}
	}
	v.X /= scale
	v.Y /= scale
	length := math.Hypot(v.X, v.Y)
	return entity.Vec2{X: v.X / length, Y: v.Y / length}
}

// NormalizeDirection 保留长度小于 1 的模拟输入，并限制更大向量。先缩放可避免两个
// MaxFloat64 分量造成溢出；调用前必须拒绝非有限输入。
func NormalizeDirection(v entity.Vec2) entity.Vec2 {
	scale := math.Max(math.Abs(v.X), math.Abs(v.Y))
	if scale > 1 {
		v.X /= scale
		v.Y /= scale
	}
	if length := math.Hypot(v.X, v.Y); length > 1 {
		v.X /= length
		v.Y /= length
	}
	return v
}

// Move 恰好执行一个固定模拟步长。Velocity 反映实际位移，因此地图边界阻挡分量会变成 0。
func Move(p *entity.Player, direction entity.Vec2, speed, dt float64, min, max entity.Vec2) {
	previous := p.Position
	p.Position.X = math.Max(min.X, math.Min(max.X, previous.X+direction.X*speed*dt))
	p.Position.Y = math.Max(min.Y, math.Min(max.Y, previous.Y+direction.Y*speed*dt))
	p.Velocity = entity.Vec2{X: (p.Position.X - previous.X) / dt, Y: (p.Position.Y - previous.Y) / dt}
}
