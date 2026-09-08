// Package convert owns the DTO <-> Domain boundary for Role A. It is the only
// place that knows both the generated protocol types and B's game domain types.
// It contains no business logic: it maps fields and never mutates state.
//
// Direction rules (ARCHITECTURE.md §10):
//   - Inbound:  protocol DTO  -> game domain (this file).
//   - Outbound: game domain  -> protocol DTO (snapshot.go, events.go).
//
// This package must never import network (frame/connection) or session; those
// layers sit above it and route through convert without being aware of the
// mapping details.
package convert

import (
	"errors"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

// ErrNilInput is returned when the caller passes a nil PlayerInput pointer.
// The room layer never produces a nil intent, but the network boundary must
// defend against a malformed frame that unmarshals to nil.
var ErrNilInput = errors.New("convert: nil PlayerInput")

// Input converts the wire intent (protocol.PlayerInput) into B's domain intent
// (game.Input). It maps fields and widens float32 wire coordinates to float64
// domain coordinates, but does NOT validate shape: game.Input.Validate is the
// single source of truth for that and the room applies it on admission.
//
// Nil sub-messages are treated as their zero values:
//   - a nil move means "stop moving" (zero vector),
//   - a nil aim means "no aim" (zero vector); note that Shoot with a zero aim
//     is rejected later by game.Input.Validate, matching B's contract.
func Input(in *protocol.PlayerInput) (game.Input, error) {
	if in == nil {
		return game.Input{}, ErrNilInput
	}
	var out game.Input
	out.Seq = in.InputSeq
	if in.Move != nil {
		out.Direction = vec2(in.Move)
	}
	if in.Aim != nil {
		out.Aim = vec2(in.Aim)
	}
	out.Shoot = in.Shoot
	// UsePotion is intentionally not mapped: the room does not yet consume it
	// (game.Input has no field for it). Wire it through in a later combat
	// extension rather than silently dropping intent.
	return out, nil
}

// vec2 widens a wire Vec2 (float32) to the domain entity.Vec2 (float64). The
// conversion itself is exact (every float32 is representable as float64), but
// the wire value already carries float32 precision (e.g. 0.6f becomes
// 0.6000000238418579), which is a protocol property, not a conversion artifact.
// No rounding or clamping is applied here; the domain layer validates finiteness
// and range.
func vec2(v *protocol.Vec2) entity.Vec2 {
	return entity.Vec2{X: float64(v.X), Y: float64(v.Y)}
}
