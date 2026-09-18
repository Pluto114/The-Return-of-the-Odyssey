package game

import (
	"math"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func TestOpeningEncounterNavigatesCover(t *testing.T) {
	config := DefaultConfig()
	w, err := NewWorld(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	plan, err := NewFirstStagePlan(config, 42)
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Monsters {
		plan.Monsters[i].Stats.Attack = 0
	}
	if err := w.StartStage(plan); err != nil {
		t.Fatal(err)
	}
	for range 300 {
		w.Step(time.Unix(100, 0))
		w.TakeEvents()
	}
	for _, monster := range w.Snapshot().Monsters {
		if distance := math.Hypot(monster.Position.X-10, monster.Position.Y-10); distance > 2 {
			t.Fatalf("monster stuck behind cover at %+v (distance %.2f)", monster.Position, distance)
		}
	}
}

func TestMonsterRoutesAroundCover(t *testing.T) {
	w, err := NewWorld(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	spawn := stage.Spawn{Position: entity.Vec2{X: 17, Y: 8.5},
		Stats:  entity.CombatStats{Attack: 1, MaxHealth: 100, MoveSpeed: 2, AttackCooldownTicks: TickRate},
		Radius: 0.4, AttackRange: 1}
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{spawn}}); err != nil {
		t.Fatal(err)
	}
	for range 150 {
		w.Step(time.Unix(100, 0))
		w.TakeEvents()
	}
	monster := w.Snapshot().Monsters[0]
	if monster.Position.X >= 16 || monster.Position.Y <= 8.5 {
		t.Fatalf("monster stuck behind cover: %+v", monster)
	}
}

func TestCoverStopsProjectileBeforeMonster(t *testing.T) {
	w, err := NewWorld(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	spawn := stage.Spawn{Position: entity.Vec2{X: 17, Y: 8.5},
		Stats:  entity.CombatStats{MaxHealth: 40, AttackCooldownTicks: TickRate},
		Radius: 0.4, AttackRange: 0}
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{spawn}}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	for tick := uint32(1); tick <= 20; tick++ {
		if err := w.ApplyInput(1, Input{Seq: tick, Aim: entity.Vec2{X: 7, Y: -1.5}, Shoot: true}, now); err != nil {
			t.Fatal(err)
		}
		w.Step(now)
		w.TakeEvents()
	}
	if got := w.Snapshot().Monsters[0].Health; got != 40 {
		t.Fatalf("projectile crossed cover, monster health = %v", got)
	}
}

func TestMonstersSplitTargetsAtEqualDistance(t *testing.T) {
	w, err := NewWorld(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []entity.ID{1, 2} {
		if err := w.AddPlayer(id); err != nil {
			t.Fatal(err)
		}
	}
	monsters := make([]stage.Spawn, 8)
	for i := range monsters {
		monsters[i] = stage.Spawn{Position: entity.Vec2{X: 10, Y: 10},
			Stats:  entity.CombatStats{Attack: 5, MaxHealth: 10, AttackCooldownTicks: TickRate},
			Radius: 0.4, AttackRange: 1}
	}
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: monsters}); err != nil {
		t.Fatal(err)
	}
	w.Step(time.Unix(100, 0))
	players := w.Snapshot().Players
	if players[0].Health != 80 || players[1].Health != 80 {
		t.Fatalf("monsters did not divide targets: %+v", players)
	}
}

func TestCoverBlocksMovementAndProjectiles(t *testing.T) {
	w, err := NewWorld(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	from := entity.Vec2{X: 15, Y: 8.9}
	to := w.moveWithCover(from, entity.Vec2{X: -2}, 0.32)
	if to.X < 14.72 {
		t.Fatalf("player passed through cover: %+v", to)
	}
	if _, hit := w.firstCoverHit(from, entity.Vec2{X: 10, Y: 8.9}, 0.1); !hit {
		t.Fatal("projectile path missed cover")
	}
}

func TestMonsterSpawnInsideCoverMovesToOpenGround(t *testing.T) {
	w, err := NewWorld(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	spawn := stage.Spawn{Position: entity.Vec2{X: 6.3, Y: 11.9},
		Stats:  entity.CombatStats{MaxHealth: 10, AttackCooldownTicks: TickRate},
		Radius: 0.4, AttackRange: 1}
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{spawn}}); err != nil {
		t.Fatal(err)
	}
	position := w.Snapshot().Monsters[0].Position
	for _, cover := range w.coverBlocks() {
		if pointInBlock(position, cover, spawn.Radius) {
			t.Fatalf("monster spawned inside cover at %+v", position)
		}
	}
}
