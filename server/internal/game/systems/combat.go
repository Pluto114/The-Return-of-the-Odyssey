package systems

import (
	"errors"
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

// DamageRequest 由碰撞或 AI 产生；这两个子系统都不直接修改生命值。
type DamageRequest struct {
	SourceID, TargetID entity.ID
	Attack             float64
	TargetPlayer       bool
}

type DamageResolved struct {
	Amount, RemainingHealth float64
	Killed                  bool
}

func ResolveDamage(attack, defense, health float64) (DamageResolved, error) {
	for _, v := range []float64{attack, defense, health} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return DamageResolved{}, errors.New("invalid combat value")
		}
	}
	// 等价于 Attack * 100 / (100 + Defense)，但可避免 Attack*100 溢出。
	amount := math.Min(health, attack/(1+defense/100))
	return DamageResolved{Amount: amount, RemainingHealth: health - amount, Killed: health > 0 && amount >= health}, nil
}

// SegmentCircle 返回扫掠投射物与圆形目标首次接触的 [0,1] 比例，包含初始重叠与相切；
// 调用方需提供有限且有界的世界几何数据。
func SegmentCircle(from, to, center entity.Vec2, radius float64) (float64, bool) {
	dx, dy := to.X-from.X, to.Y-from.Y
	fx, fy := from.X-center.X, from.Y-center.Y
	c := fx*fx + fy*fy - radius*radius
	if c <= 0 {
		return 0, true
	}
	a := dx*dx + dy*dy
	if a == 0 {
		return 0, false
	}
	b := fx*dx + fy*dy
	discriminant := b*b - a*c
	if discriminant < 0 {
		return 0, false
	}
	t := (-b - math.Sqrt(discriminant)) / a
	return t, t >= 0 && t <= 1
}
