// Package protocolbridge adapts A's wire contract to B's domain values. It
// intentionally lives outside game/room so World never imports protobuf.
package protocolbridge

import (
	"errors"
	"fmt"
	"math"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

var (
	ErrInputGap          = errors.New("input gap exceeds protocol limit 64")
	ErrPotionUnsupported = errors.New("potion is not implemented")
	ErrLossyHealth       = errors.New("fractional or out-of-range HP cannot be represented by v0 int32 fields")
	ErrMissingArchetype  = errors.New("monster archetype mapping is required")
)

// Input uses the last successfully enqueued input sequence, not Frame Sequence
// or the older snapshot acknowledgement. The caller advances its cursor only
// after Room.Input succeeds. Gap rejection must NOT disconnect the client.
func Input(m *pb.PlayerInput, lastAccepted uint64) (game.Input, error) {
	if m == nil || m.InputSeq == 0 {
		return game.Input{}, game.ErrInvalidInput
	}
	if m.InputSeq <= lastAccepted {
		return game.Input{}, game.ErrStaleInput
	}
	if m.InputSeq-lastAccepted > 64 {
		return game.Input{}, ErrInputGap
	}
	if m.UsePotion {
		return game.Input{}, ErrPotionUnsupported
	}
	move := entity.Vec2{X: float64(m.GetMove().GetX()), Y: float64(m.GetMove().GetY())}
	angle := float64(m.AimDeg)
	if !move.Finite() || math.Hypot(move.X, move.Y) > float64(float32(1.05)) || math.IsNaN(angle) || math.IsInf(angle, 0) {
		return game.Input{}, game.ErrInvalidInput
	}
	angle = math.Mod(angle, 360) * math.Pi / 180
	input := game.Input{Seq: m.InputSeq, Direction: move, Aim: entity.Vec2{X: math.Cos(angle), Y: math.Sin(angle)}, Shoot: m.Shoot}
	return input, input.Validate()
}

func vec(v entity.Vec2) *pb.Vec2 { return &pb.Vec2{X: float32(v.X), Y: float32(v.Y)} }
func heading(v entity.Vec2) float32 {
	a := math.Atan2(v.Y, v.X) * 180 / math.Pi
	if a < 0 {
		a += 360
	}
	return float32(a)
}

// Do not silently round fractional authoritative health or turn a live player
// into a displayed zero-HP entity. A/B must agree on a wire representation first.
func integerHealth(v float64) (int32, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > math.MaxInt32 || math.Trunc(v) != v {
		return 0, ErrLossyHealth
	}
	return int32(v), nil
}

// Snapshot is per-recipient: self is excluded from Entities and the ack belongs
// to self. room_id/Stage are absent from A's v0 snapshot; callers must retain
// room context. Archetype IDs are supplied by server configuration, never guessed.
// ALIVE=1 follows A's current example; final flag definitions still need A/C review.
func Snapshot(s room.Snapshot, self entity.ID, archetypes map[entity.ID]uint32) (*pb.WorldSnapshot, error) {
	out := &pb.WorldSnapshot{ServerTick: s.ServerTick}
	for _, p := range s.Players {
		hp, err := integerHealth(p.Health)
		if err != nil {
			return nil, err
		}
		maximum, err := integerHealth(p.CurrentStats.MaxHealth)
		if err != nil {
			return nil, err
		}
		flags := uint32(0)
		if p.Alive {
			flags = 1
		}
		if p.ID == self {
			out.LastProcessedInput = p.LastProcessedInputSeq
			out.Self = &pb.PlayerSnapshot{PlayerId: uint64(p.ID), Position: vec(p.Position), Velocity: vec(p.Velocity), AimDeg: heading(p.Aim), Hp: hp, MaxHp: maximum, StateFlags: flags}
		} else {
			out.Entities = append(out.Entities, &pb.EntitySnapshot{EntityId: uint64(p.ID), Position: vec(p.Position), Velocity: vec(p.Velocity), AimDeg: heading(p.Aim), Hp: hp, MaxHp: maximum, StateFlags: flags})
		}
	}
	if out.Self == nil {
		return nil, fmt.Errorf("%w: self %d", game.ErrPlayerMissing, self)
	}
	for _, m := range s.Monsters {
		archetype := archetypes[m.ID]
		if archetype == 0 {
			return nil, fmt.Errorf("%w: %d", ErrMissingArchetype, m.ID)
		}
		hp, err := integerHealth(m.Health)
		if err != nil {
			return nil, err
		}
		maximum, err := integerHealth(m.MaxHealth)
		if err != nil {
			return nil, err
		}
		flags := uint32(0)
		if m.Health > 0 {
			flags = 1
		}
		out.Entities = append(out.Entities, &pb.EntitySnapshot{EntityId: uint64(m.ID), ArchetypeId: archetype, Position: vec(m.Position), Velocity: vec(m.Velocity), Hp: hp, MaxHp: maximum, StateFlags: flags})
	}
	return out, nil
}
