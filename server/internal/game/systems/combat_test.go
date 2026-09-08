package systems_test

import (
	"math"
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/systems"
)

func TestResolveDamage(t *testing.T) {
	for _, tc := range []struct {
		attack, defense, health, amount float64
		killed                          bool
	}{
		{20, 0, 100, 20, false}, {20, 100, 100, 10, false}, {200, 0, 25, 25, true}, {0, 0, 10, 0, false}, {20, 0, 0, 0, false},
	} {
		r, err := systems.ResolveDamage(tc.attack, tc.defense, tc.health)
		if err != nil || r.Amount != tc.amount || r.RemainingHealth != tc.health-tc.amount || r.Killed != tc.killed {
			t.Fatalf("resolve %+v: %+v %v", tc, r, err)
		}
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1)} {
		for _, args := range [][3]float64{{v, 0, 100}, {20, v, 100}, {20, 0, v}} {
			if _, err := systems.ResolveDamage(args[0], args[1], args[2]); err == nil {
				t.Fatal("invalid damage accepted")
			}
		}
	}
	if r, err := systems.ResolveDamage(math.MaxFloat64, 0, math.MaxFloat64); err != nil || !r.Killed || math.IsInf(r.Amount, 0) {
		t.Fatal("overflow in damage formula")
	}
}

func TestSweptCollision(t *testing.T) {
	for _, tc := range []struct {
		name             string
		from, to, center entity.Vec2
		radius, want     float64
		hit              bool
	}{
		{"tunneling", entity.Vec2{}, entity.Vec2{X: 10}, entity.Vec2{X: 5}, 1, 0.4, true},
		{"overlap", entity.Vec2{X: 5}, entity.Vec2{X: 10}, entity.Vec2{X: 5}, 1, 0, true},
		{"tangent", entity.Vec2{Y: 1}, entity.Vec2{X: 10, Y: 1}, entity.Vec2{X: 5}, 1, 0.5, true},
		{"miss", entity.Vec2{}, entity.Vec2{X: 10}, entity.Vec2{X: 5, Y: 2}, 1, 0, false},
		{"behind", entity.Vec2{}, entity.Vec2{X: 10}, entity.Vec2{X: -5}, 1, 0, false},
		{"stationary", entity.Vec2{}, entity.Vec2{}, entity.Vec2{X: 5}, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, hit := systems.SegmentCircle(tc.from, tc.to, tc.center, tc.radius)
			if hit != tc.hit || hit && math.Abs(f-tc.want) > 1e-9 {
				t.Fatalf("contact %v %v", f, hit)
			}
		})
	}
}

func TestUnitAimExtremeMagnitudes(t *testing.T) {
	for _, v := range []float64{math.SmallestNonzeroFloat64, 0.1, 1, math.MaxFloat64} {
		d := systems.UnitDirection(entity.Vec2{X: v, Y: v})
		if !d.Finite() || math.Abs(math.Hypot(d.X, d.Y)-1) > 1e-12 {
			t.Fatalf("invalid aim: %+v", d)
		}
	}
}
