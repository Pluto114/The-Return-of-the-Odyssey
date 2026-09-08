package systems

import (
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

// NormalizeDirection preserves analog magnitudes below one and caps larger
// vectors. Scaling first avoids overflow even with two MaxFloat64 components.
// The caller must reject non-finite inputs before invoking this function.
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

// Move performs exactly one fixed simulation step. Velocity reflects actual
// displacement, so the blocked component becomes zero at a map boundary.
func Move(p *entity.Player, direction entity.Vec2, speed, dt float64, min, max entity.Vec2) {
	previous := p.Position
	p.Position.X = math.Max(min.X, math.Min(max.X, previous.X+direction.X*speed*dt))
	p.Position.Y = math.Max(min.Y, math.Min(max.Y, previous.Y+direction.Y*speed*dt))
	p.Velocity = entity.Vec2{X: (p.Position.X - previous.X) / dt, Y: (p.Position.Y - previous.Y) / dt}
}
