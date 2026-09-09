package protocolbridge_test

import (
	"errors"
	"math"
	"testing"
	"time"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/protocolbridge"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
	"google.golang.org/protobuf/proto"
)

func TestInput64BitSequenceAndAngle(t *testing.T) {
	seq := uint64(1) << 40
	m := &pb.PlayerInput{InputSeq: seq, ClientTickMs: 999, AimDeg: 450, Shoot: true, Move: &pb.Vec2{X: 1}}
	body, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.PlayerInput
	if err := proto.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	input, err := protocolbridge.Input(&decoded, seq-1)
	if err != nil {
		t.Fatal(err)
	}
	if input.Seq != seq || math.Abs(input.Aim.X) > 1e-6 || math.Abs(input.Aim.Y-1) > 1e-6 || !input.Shoot {
		t.Fatalf("conversion: %+v", input)
	}
	w, err := game.NewWorld(game.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := w.ApplyInput(1, input, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	if w.Snapshot().Players[0].LastProcessedInputSeq != seq {
		t.Fatal("64-bit acknowledgement truncated")
	}
}

func TestInputValidationAndGap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input *pb.PlayerInput
		last  uint64
		want  error
	}{
		{"nil", nil, 0, game.ErrInvalidInput}, {"zero", &pb.PlayerInput{}, 0, game.ErrInvalidInput},
		{"duplicate", &pb.PlayerInput{InputSeq: 10}, 10, game.ErrStaleInput},
		{"gap", &pb.PlayerInput{InputSeq: 75}, 10, protocolbridge.ErrInputGap},
		{"potion", &pb.PlayerInput{InputSeq: 1, UsePotion: true}, 0, protocolbridge.ErrPotionUnsupported},
		{"nan", &pb.PlayerInput{InputSeq: 1, AimDeg: float32(math.NaN())}, 0, game.ErrInvalidInput},
		{"too long", &pb.PlayerInput{InputSeq: 1, Move: &pb.Vec2{X: 1, Y: 1}}, 0, game.ErrInvalidInput},
		{"infinite", &pb.PlayerInput{InputSeq: 1, Move: &pb.Vec2{Y: float32(math.Inf(1))}}, 0, game.ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := protocolbridge.Input(tc.input, tc.last)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := protocolbridge.Input(&pb.PlayerInput{InputSeq: 64, Move: &pb.Vec2{X: 1.05}}, 0); err != nil {
		t.Fatal("boundary input rejected", err)
	}
}

func fixture() room.Snapshot {
	return room.Snapshot{RoomID: 8, Snapshot: game.Snapshot{ServerTick: 30, Players: []entity.Player{
		{ID: 1, Health: 100, Alive: true, CurrentStats: entity.CombatStats{MaxHealth: 100}, LastProcessedInputSeq: 1 << 40},
		{ID: 2, Health: 80, Alive: true, CurrentStats: entity.CombatStats{MaxHealth: 100}, LastProcessedInputSeq: 7},
	}, Monsters: []game.MonsterView{{ID: game.FirstWorldEntityID + 1, Health: 20, MaxHealth: 40}}}}
}

func TestSnapshotIsPerPlayerAndPreservesEntityIDs(t *testing.T) {
	s := fixture()
	mapping := map[entity.ID]uint32{game.FirstWorldEntityID + 1: 12}
	for _, id := range []entity.ID{1, 2} {
		out, err := protocolbridge.Snapshot(s, id, mapping)
		if err != nil {
			t.Fatal(err)
		}
		b, err := proto.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		var decoded pb.WorldSnapshot
		if err := proto.Unmarshal(b, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Self.PlayerId != uint64(id) || len(decoded.Entities) != 2 || decoded.Entities[0].EntityId == uint64(id) {
			t.Fatal("self/other mapping failed")
		}
		if decoded.LastProcessedInput != s.Players[id-1].LastProcessedInputSeq {
			t.Fatal("ack belongs to another player")
		}
		if decoded.Entities[1].EntityId != uint64(game.FirstWorldEntityID+1) || decoded.Entities[1].ArchetypeId != 12 {
			t.Fatal("monster identity truncated")
		}
	}
}

func TestUnresolvedHealthAndArchetypeAreExplicitErrors(t *testing.T) {
	s := fixture()
	if _, err := protocolbridge.Snapshot(s, 1, nil); !errors.Is(err, protocolbridge.ErrMissingArchetype) {
		t.Fatal(err)
	}
	s.Players[0].Health = 0.25
	if _, err := protocolbridge.Snapshot(s, 1, nil); !errors.Is(err, protocolbridge.ErrLossyHealth) {
		t.Fatal("silently rounded health", err)
	}
	if _, err := protocolbridge.Snapshot(fixture(), 99, nil); !errors.Is(err, game.ErrPlayerMissing) {
		t.Fatal(err)
	}
}
