package convert

import (
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

func TestInput(t *testing.T) {
	t.Run("full mapping", func(t *testing.T) {
		in := &protocol.PlayerInput{
			InputSeq:  42,
			Move:      &protocol.Vec2{X: 1, Y: 0},
			Aim:       &protocol.Vec2{X: 0.5, Y: 0.25},
			Shoot:     true,
			UsePotion: true,
			Reload:    true,
		}
		got, err := Input(in)
		if err != nil {
			t.Fatalf("Input() error = %v", err)
		}
		if got.Seq != 42 {
			t.Errorf("Seq = %d, want 42", got.Seq)
		}
		if got.Direction != (entity.Vec2{X: 1, Y: 0}) {
			t.Errorf("Direction = %+v, want {1 0}", got.Direction)
		}
		if got.Aim != (entity.Vec2{X: 0.5, Y: 0.25}) {
			t.Errorf("Aim = %+v, want {0.5 0.25}", got.Aim)
		}
		if !got.Shoot {
			t.Errorf("Shoot = false, want true")
		}
		if !got.UsePotion {
			t.Errorf("UsePotion = false, want true")
		}
		if !got.Reload {
			t.Error("reload intent lost")
		}
	})

	t.Run("nil move and aim become zero vectors", func(t *testing.T) {
		in := &protocol.PlayerInput{InputSeq: 1, Shoot: false}
		got, err := Input(in)
		if err != nil {
			t.Fatalf("Input() error = %v", err)
		}
		if got.Direction != (entity.Vec2{}) {
			t.Errorf("Direction = %+v, want zero", got.Direction)
		}
		if got.Aim != (entity.Vec2{}) {
			t.Errorf("Aim = %+v, want zero", got.Aim)
		}
	})

	t.Run("float32 widened to float64 exactly", func(t *testing.T) {
		in := &protocol.PlayerInput{
			InputSeq: 7,
			Move:     &protocol.Vec2{X: -0.5, Y: 0.25},
		}
		got, err := Input(in)
		if err != nil {
			t.Fatalf("Input() error = %v", err)
		}
		// 0.5 and 0.25 are exactly representable in both float32 and float64.
		if got.Direction.X != -0.5 || got.Direction.Y != 0.25 {
			t.Errorf("Direction = %+v, want {-0.5 0.25}", got.Direction)
		}
	})

	t.Run("nil input returns error", func(t *testing.T) {
		if _, err := Input(nil); err != ErrNilInput {
			t.Errorf("Input(nil) error = %v, want ErrNilInput", err)
		}
	})

}
