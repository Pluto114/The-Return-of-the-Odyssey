// Package entity defines protocol-independent game state. A room owns its live
// entities; anything published to another goroutine must be copied.
package entity

import "math"

type ID uint64

// Vec2 uses server X/Y coordinates (client X/Z).
type Vec2 struct {
	X, Y float64
}

func (v Vec2) Finite() bool {
	return !math.IsNaN(v.X) && !math.IsNaN(v.Y) && !math.IsInf(v.X, 0) && !math.IsInf(v.Y, 0)
}

// Player is a domain value, not a network DTO. Zero acknowledgement means that
// no input has been simulated yet. Input sequences start at 1 and do not wrap.
type Player struct {
	ID                    ID
	Position              Vec2
	Velocity              Vec2
	LastProcessedInputSeq uint32
}
