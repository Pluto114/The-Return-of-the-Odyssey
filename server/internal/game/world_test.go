package game_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

func world(t *testing.T) *game.World {
	t.Helper()
	w, err := game.NewWorld(game.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	return w
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > 0.000001 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// T05/T06: packet rate does not set simulation speed; diagonal and extreme
// finite vectors must not move faster than axial input.
func TestFixedTickMovement(t *testing.T) {
	for _, tc := range []struct {
		name      string
		direction entity.Vec2
		packets   int
		distance  float64
	}{
		{"axis", entity.Vec2{X: 1}, 1, 5},
		{"diagonal", entity.Vec2{X: 1, Y: 1}, 1, 5},
		{"300Hz", entity.Vec2{X: 1}, 10, 5},
		{"huge finite", entity.Vec2{X: math.MaxFloat64, Y: math.MaxFloat64}, 1, 5},
		{"analog", entity.Vec2{Y: -0.5}, 1, 2.5},
		{"zero", entity.Vec2{}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := world(t)
			now := time.Unix(100, 0)
			var seq uint64
			for range 30 {
				for range tc.packets {
					seq++
					if err := w.ApplyInput(1, game.Input{Seq: seq, Direction: tc.direction}, now); err != nil {
						t.Fatal(err)
					}
				}
				w.Step(now)
				now = now.Add(game.TickInterval)
			}
			s := w.Snapshot()
			p := s.Players[0]
			near(t, math.Hypot(p.Position.X-10, p.Position.Y-10), tc.distance)
			if s.ServerTick != 30 || p.LastProcessedInputSeq != seq {
				t.Fatalf("unexpected tick/ack: %+v", s)
			}
		})
	}
}

func TestAcknowledgesOnlySimulatedInput(t *testing.T) {
	w := world(t)
	now := time.Unix(100, 0)
	for _, input := range []game.Input{{Seq: 1, Direction: entity.Vec2{X: 1}}, {Seq: 3, Direction: entity.Vec2{Y: 1}}} {
		if err := w.ApplyInput(1, input, now); err != nil {
			t.Fatal(err)
		}
	}
	before := w.Snapshot().Players[0]
	near(t, before.Position.X, 10)
	if before.LastProcessedInputSeq != 0 {
		t.Fatal("ack advanced before Step")
	}
	for _, seq := range []uint64{3, 2, 1} {
		if err := w.ApplyInput(1, game.Input{Seq: seq, Direction: entity.Vec2{X: -1}}, now); !errors.Is(err, game.ErrStaleInput) {
			t.Fatalf("old input: %v", err)
		}
	}
	w.Step(now)
	p := w.Snapshot().Players[0]
	near(t, p.Position.X, 10)
	near(t, p.Position.Y, 10+5.0/30)
	if p.LastProcessedInputSeq != 3 {
		t.Fatalf("ack = %d", p.LastProcessedInputSeq)
	}
}

func TestInvalidInputCannotPoisonSequenceOrMovement(t *testing.T) {
	w := world(t)
	now := time.Unix(100, 0)
	for _, input := range []game.Input{
		{Seq: 0}, {Seq: 999, Direction: entity.Vec2{X: math.NaN()}},
		{Seq: 999, Direction: entity.Vec2{Y: math.Inf(1)}}, {Seq: 999, Direction: entity.Vec2{X: math.Inf(-1)}},
	} {
		if err := w.ApplyInput(1, input, now); !errors.Is(err, game.ErrInvalidInput) {
			t.Fatalf("invalid input: %v", err)
		}
	}
	if err := w.ApplyInput(1, game.Input{Seq: 1, Direction: entity.Vec2{X: 1}}, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	near(t, w.Snapshot().Players[0].Position.X, 10+5.0/30)
	if err := w.ApplyInput(99, game.Input{Seq: 1}, now); !errors.Is(err, game.ErrPlayerMissing) {
		t.Fatal(err)
	}
}

func TestReleaseAndInputExpiry(t *testing.T) {
	for _, release := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "release"}[release], func(t *testing.T) {
			w := world(t)
			now := time.Unix(100, 0)
			if err := w.ApplyInput(1, game.Input{Seq: 1, Direction: entity.Vec2{X: 1}}, now); err != nil {
				t.Fatal(err)
			}
			w.Step(now)
			before := w.Snapshot().Players[0]
			if release {
				now = now.Add(game.TickInterval)
				if err := w.ApplyInput(1, game.Input{Seq: 2}, now); err != nil {
					t.Fatal(err)
				}
			} else {
				// A duplicate must not renew the timeout.
				if err := w.ApplyInput(1, game.Input{Seq: 1}, now.Add(199*time.Millisecond)); !errors.Is(err, game.ErrStaleInput) {
					t.Fatal(err)
				}
				now = now.Add(200 * time.Millisecond)
			}
			w.Step(now)
			after := w.Snapshot().Players[0]
			near(t, after.Position.X, before.Position.X)
			near(t, after.Velocity.X, 0)
			wantAck := uint64(1)
			if release {
				wantAck = 2
			}
			if after.LastProcessedInputSeq != wantAck {
				t.Fatalf("ack = %d", after.LastProcessedInputSeq)
			}
		})
	}
}

func TestExpiredPendingInputIsNeverAcknowledged(t *testing.T) {
	w := world(t)
	now := time.Unix(100, 0)
	if err := w.ApplyInput(1, game.Input{Seq: 1, Direction: entity.Vec2{X: 1}}, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now.Add(200 * time.Millisecond))
	p := w.Snapshot().Players[0]
	near(t, p.Position.X, 10)
	if p.LastProcessedInputSeq != 0 {
		t.Fatal("expired input was acknowledged")
	}
}

func TestFutureArrivalRemainsPendingUntilSimulation(t *testing.T) {
	w := world(t)
	now := time.Unix(100, 0)
	if err := w.ApplyInput(1, game.Input{Seq: 1, Direction: entity.Vec2{X: 1}}, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	if w.Snapshot().Players[0].LastProcessedInputSeq != 0 {
		t.Fatal("future input acknowledged")
	}
	w.Step(now.Add(game.TickInterval))
	if w.Snapshot().Players[0].LastProcessedInputSeq != 1 {
		t.Fatal("pending ack lost")
	}
}

func TestBoundsAndActualVelocity(t *testing.T) {
	for _, direction := range []entity.Vec2{{X: 1}, {X: -1}, {Y: 1}, {Y: -1}} {
		w := world(t)
		now := time.Unix(100, 0)
		for seq := uint64(1); seq <= 100; seq++ {
			if err := w.ApplyInput(1, game.Input{Seq: seq, Direction: direction}, now); err != nil {
				t.Fatal(err)
			}
			w.Step(now)
			now = now.Add(game.TickInterval)
		}
		p := w.Snapshot().Players[0]
		near(t, p.Position.X, 10+10*direction.X)
		near(t, p.Position.Y, 10+10*direction.Y)
		near(t, p.Velocity.X, 0)
		near(t, p.Velocity.Y, 0)
	}
}

func TestWorldMembershipAndSnapshotIsolation(t *testing.T) {
	w, err := game.NewWorld(game.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []entity.ID{20, 10} {
		if err := w.AddPlayer(id); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		id   entity.ID
		want error
	}{{0, game.ErrInvalidPlayer}, {10, game.ErrPlayerExists}, {30, game.ErrWorldFull}} {
		if err := w.AddPlayer(tc.id); !errors.Is(err, tc.want) {
			t.Fatalf("AddPlayer(%d): %v", tc.id, err)
		}
	}
	s := w.Snapshot()
	if s.Players[0].ID != 10 || s.Players[1].ID != 20 {
		t.Fatal("unstable entity ordering")
	}
	clone := s.Clone()
	clone.Players[0].Position.X = -999
	near(t, s.Players[0].Position.X, 10)
	s.Players[0].Position.X = 999
	near(t, w.Snapshot().Players[0].Position.X, 10)
	w.RemovePlayer(10)
	w.RemovePlayer(10)
	if w.PlayerCount() != 1 || w.Snapshot().Players[0].ID != 20 {
		t.Fatal("remove did not update full state")
	}
	w.Clear()
	if len(w.Snapshot().Players) != 0 {
		t.Fatal("clear left entities")
	}
}

func TestInvalidWorldConfig(t *testing.T) {
	for name, modify := range map[string]func(*game.Config){
		"capacity": func(c *game.Config) { c.Capacity = 0 },
		"bounds":   func(c *game.Config) { c.Min = c.Max },
		"spawn":    func(c *game.Config) { c.Spawn.X = -1 },
		"speed":    func(c *game.Config) { c.MoveSpeed = math.Inf(1) },
		"nan":      func(c *game.Config) { c.Max.Y = math.NaN() },
		"timeout":  func(c *game.Config) { c.InputTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c := game.DefaultConfig()
			modify(&c)
			if _, err := game.NewWorld(c); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
