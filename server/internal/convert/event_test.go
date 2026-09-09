package convert

import (
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

func TestEventStageStarted(t *testing.T) {
	mt, msg, err := Event(game.Event{Kind: game.StageStarted, StageIndex: 2, ServerTick: 7})
	if err != nil {
		t.Fatalf("Event() error = %v", err)
	}
	if mt != uint16(protocol.MessageType_MSG_STAGE_STARTED_EVENT) {
		t.Errorf("MessageType = %d, want %d", mt, protocol.MessageType_MSG_STAGE_STARTED_EVENT)
	}
	got := msg.(*protocol.StageStartedEvent)
	if got.StageIndex != 2 || got.ServerTick != 7 {
		t.Errorf("StageStartedEvent = %+v, want stage_index=2 server_tick=7", got)
	}
}

func TestEventProjectileSpawned(t *testing.T) {
	mt, msg, err := Event(game.Event{
		Kind:          game.ProjectileSpawned,
		EntityID:      entity.ID(101),
		SourceID:      entity.ID(5),
		Position:      entity.Vec2{X: 1, Y: 2},
		Velocity:      entity.Vec2{X: 0.5, Y: 0.5},
		ExpiresAtTick: 99,
		ServerTick:    3,
	})
	if err != nil {
		t.Fatalf("Event() error = %v", err)
	}
	if mt != uint16(protocol.MessageType_MSG_PROJECTILE_SPAWN) {
		t.Errorf("MessageType = %d", mt)
	}
	got := msg.(*protocol.ProjectileSpawnEvent)
	if got.ProjectileId != 101 || got.OwnerId != 5 || got.ExpiresAtTick != 99 || got.ServerTick != 3 {
		t.Errorf("ProjectileSpawnEvent = %+v", got)
	}
	if got.Position.X != 1 || got.Position.Y != 2 {
		t.Errorf("Position = %+v, want {1 2}", got.Position)
	}
	if got.Velocity.X != 0.5 || got.Velocity.Y != 0.5 {
		t.Errorf("Velocity = %+v, want {0.5 0.5}", got.Velocity)
	}
}

func TestEventProjectileDestroyed(t *testing.T) {
	_, msg, err := Event(game.Event{
		Kind:     game.ProjectileDestroyed,
		EntityID: entity.ID(101),
		SourceID: entity.ID(5),
		Position: entity.Vec2{X: 3, Y: 4},
	})
	if err != nil {
		t.Fatalf("Event() error = %v", err)
	}
	got := msg.(*protocol.ProjectileDestroyEvent)
	if got.ProjectileId != 101 || got.OwnerId != 5 {
		t.Errorf("ProjectileDestroyEvent = %+v", got)
	}
}

func TestEventDamageDealt(t *testing.T) {
	_, msg, err := Event(game.Event{
		Kind:     game.DamageDealt,
		SourceID: entity.ID(5),
		TargetID: entity.ID(200),
		Amount:   12.5,
		Health:   87.5,
	})
	if err != nil {
		t.Fatalf("Event() error = %v", err)
	}
	got := msg.(*protocol.DamageEvent)
	if got.SourceId != 5 || got.TargetId != 200 {
		t.Errorf("DamageEvent = %+v", got)
	}
	if got.Amount != 12.5 || got.RemainingHealth != 87.5 {
		t.Errorf("Amount/Health = %v/%v, want 12.5/87.5", got.Amount, got.RemainingHealth)
	}
}

func TestEventEntityDied(t *testing.T) {
	mt, msg, err := Event(game.Event{
		Kind:     game.EntityDied,
		EntityID: entity.ID(200),
		SourceID: entity.ID(5),
	})
	if err != nil {
		t.Fatalf("Event() error = %v", err)
	}
	if mt != uint16(protocol.MessageType_MSG_DEATH_EVENT) {
		t.Errorf("MessageType = %d", mt)
	}
	got := msg.(*protocol.DeathEvent)
	if got.EntityId != 200 || got.KillerId != 5 {
		t.Errorf("DeathEvent = %+v", got)
	}
}

func TestEventStageLifecycle(t *testing.T) {
	// StageCleared and TeamDefeated carry stageIndex.
	for _, tc := range []struct {
		kind     game.EventKind
		mt       protocol.MessageType
		stageIdx uint32
	}{
		{game.StageCleared, protocol.MessageType_MSG_STAGE_CLEARED_EVENT, 3},
		{game.TeamDefeated, protocol.MessageType_MSG_TEAM_DEFEATED_EVENT, 3},
	} {
		gotMT, msg, err := Event(game.Event{Kind: tc.kind, StageIndex: tc.stageIdx, ServerTick: 8})
		if err != nil {
			t.Fatalf("Event(%v) error = %v", tc.kind, err)
		}
		if gotMT != uint16(tc.mt) {
			t.Errorf("MessageType = %d, want %d", gotMT, tc.mt)
		}
		var idx uint32
		var tick uint64
		switch m := msg.(type) {
		case *protocol.StageClearedEvent:
			idx, tick = m.StageIndex, m.ServerTick
		case *protocol.TeamDefeatedEvent:
			idx, tick = m.StageIndex, m.ServerTick
		}
		if idx != tc.stageIdx || tick != 8 {
			t.Errorf("stage event = idx %d tick %d, want %d/8", idx, tick, tc.stageIdx)
		}
	}
}

func TestEventUnknownKind(t *testing.T) {
	_, _, err := Event(game.Event{Kind: game.EventKind(255)})
	if err == nil {
		t.Fatal("Event() expected error for unknown kind, got nil")
	}
}
