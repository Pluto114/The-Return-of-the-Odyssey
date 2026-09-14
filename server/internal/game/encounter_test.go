package game_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func TestFirstStagePlanIsDeterministicAndValid(t *testing.T) {
	config := game.DefaultConfig()
	a, err := game.NewFirstStagePlan(config, 42)
	if err != nil {
		t.Fatal(err)
	}
	b, err := game.NewFirstStagePlan(config, 42)
	if err != nil {
		t.Fatal(err)
	}
	c, err := game.NewFirstStagePlan(config, 43)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed produced different first-stage plans")
	}
	if reflect.DeepEqual(a, c) {
		t.Fatal("different seeds produced the same first-stage plan")
	}
	if err := game.ValidateStage(a, config); err != nil {
		t.Fatalf("generated plan failed validation: %v", err)
	}
	if a.Index != 1 || a.DifficultyScore != 1 || len(a.Monsters) != 3 {
		t.Fatalf("unexpected first-stage shape: %+v", a)
	}
	seen := make(map[entity.Vec2]bool)
	for _, monster := range a.Monsters {
		if seen[monster.Position] {
			t.Fatalf("duplicate monster position: %+v", monster.Position)
		}
		seen[monster.Position] = true
		if monster.Position == config.Spawn {
			t.Fatal("monster spawned on the player spawn")
		}
	}
}

func TestFirstStagePlanRejectsInsufficientCapacity(t *testing.T) {
	config := game.DefaultConfig()
	config.Combat.MaxMonsters = 2
	if _, err := game.NewFirstStagePlan(config, 1); err == nil {
		t.Fatal("first-stage plan ignored monster capacity")
	}
}

func TestFirstStagePlanStartsExactlyOnce(t *testing.T) {
	config := game.DefaultConfig()
	plan, err := game.NewFirstStagePlan(config, 99)
	if err != nil {
		t.Fatal(err)
	}
	w, err := game.NewWorld(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []entity.ID{1, 2} {
		if err := w.AddPlayer(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.StartStage(plan); err != nil {
		t.Fatal(err)
	}
	if err := w.StartStage(plan); !errors.Is(err, game.ErrStageState) {
		t.Fatalf("second start returned %v, want ErrStageState", err)
	}
	w.Step(time.Unix(100, 0))
	events := w.TakeEvents().Events
	if got := countEvents(events, game.StageStarted); got != 1 {
		t.Fatalf("got %d StageStarted events, want 1", got)
	}
	snapshot := w.Snapshot()
	if snapshot.Stage.State != stage.Playing || snapshot.Stage.Seed != 99 || snapshot.Stage.MonstersRemaining != 3 || len(snapshot.Monsters) != 3 {
		t.Fatalf("unexpected started stage: %+v", snapshot)
	}
}

func TestMonsterRetargetsOnlyOnTenHertzDecisionTick(t *testing.T) {
	config := game.DefaultConfig()
	w, err := game.NewWorld(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []entity.ID{1, 2} {
		if err := w.AddPlayer(id); err != nil {
			t.Fatal(err)
		}
	}
	monster := target(5, 10, 100)
	monster.Stats.MoveSpeed = 3
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{monster}}); err != nil {
		t.Fatal(err)
	}

	w.Step(time.Unix(100, 0)) // tick 1: select player 1
	w.TakeEvents()
	w.Step(time.Unix(100, 0)) // tick 2: retain the selected target
	w.TakeEvents()
	w.RemovePlayer(1)

	before := w.Snapshot().Monsters[0].Position
	w.Step(time.Unix(100, 0)) // tick 3: target is gone, but no decision yet
	w.TakeEvents()
	idle := w.Snapshot().Monsters[0]
	if idle.State != entity.MonsterIdle || idle.Position != before {
		t.Fatalf("monster changed target between AI decision ticks: %+v", idle)
	}

	w.Step(time.Unix(100, 0)) // tick 4: next 10 Hz decision
	w.TakeEvents()
	retargeted := w.Snapshot().Monsters[0]
	if retargeted.State != entity.MonsterChase || retargeted.Position == idle.Position {
		t.Fatalf("monster did not retarget on AI decision tick: %+v", retargeted)
	}
}
