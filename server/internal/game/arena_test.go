package game

import (
	"fmt"
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
	w.covers = buildCoverBlocks(w.config, 1, 0)
	block := w.covers[0]
	from := entity.Vec2{X: block.max.X + .5, Y: (block.min.Y + block.max.Y) / 2}
	to := w.moveWithCover(from, entity.Vec2{X: -.4}, 0.32)
	if to != from {
		t.Fatalf("player passed through cover: %+v", to)
	}
	if _, hit := w.firstCoverHit(from, entity.Vec2{X: block.min.X - 1, Y: from.Y}, 0.1); !hit {
		t.Fatal("projectile path missed cover")
	}
}

func TestEveryStageBuildsDistinctTacticalCover(t *testing.T) {
	config := DefaultConfig()
	seen := make(map[string]bool)
	for index := uint32(1); index <= 12; index++ {
		blocks := buildCoverBlocks(config, index, int64(index)*4242)
		if len(blocks) < 6 {
			t.Fatalf("stage %d has only %d cover pieces", index, len(blocks))
		}
		for _, block := range blocks {
			if pointInBlock(config.Spawn, block, .5) {
				t.Fatalf("stage %d blocks the player spawn", index)
			}
		}
		signature := ""
		for _, block := range blocks {
			if block.min.X < config.Min.X || block.min.Y < config.Min.Y ||
				block.max.X > config.Max.X || block.max.Y > config.Max.Y {
				t.Fatalf("stage %d cover is outside arena: %+v", index, block)
			}
			signature += fmt.Sprintf("%.2f,%.2f,%.2f,%.2f;", block.min.X, block.min.Y, block.max.X, block.max.Y)
		}
		if seen[signature] {
			t.Fatalf("stage %d repeated an earlier geometry", index)
		}
		seen[signature] = true
	}
}

func TestMonstersNavigateEveryArenaTopology(t *testing.T) {
	for index := uint32(1); index <= 12; index++ {
		config := DefaultConfig()
		w, err := NewWorld(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.AddPlayer(1); err != nil {
			t.Fatal(err)
		}
		spawn := stage.Spawn{Position: entity.Vec2{X: 1.5, Y: 1.5},
			Stats:  entity.CombatStats{MaxHealth: 100, MoveSpeed: 3, AttackCooldownTicks: TickRate},
			Radius: .4, AttackRange: 1}
		if err := w.StartStage(stage.Plan{Index: index, Seed: int64(index) * 4242,
			DifficultyScore: 1, Monsters: []stage.Spawn{spawn}}); err != nil {
			t.Fatal(err)
		}
		for range 450 {
			w.Step(time.Unix(100, 0))
			w.TakeEvents()
		}
		monster := w.Snapshot().Monsters[0]
		if distance := math.Hypot(monster.Position.X-config.Spawn.X, monster.Position.Y-config.Spawn.Y); distance > 1.1 {
			t.Errorf("stage %d monster stuck at %+v (distance %.2f)", index, monster.Position, distance)
		}
		if _, blocked := w.firstCoverHit(monster.Position, config.Spawn, 0); blocked {
			t.Errorf("stage %d monster stopped without line of sight at %+v", index, monster.Position)
		}
	}
}

func TestMonsterPathfindingCoversFlanksAndSpawnSectors(t *testing.T) {
	starts := []entity.Vec2{{X: 2, Y: 2}, {X: 10, Y: 2}, {X: 18, Y: 2},
		{X: 18, Y: 10}, {X: 18, Y: 18}, {X: 10, Y: 18}, {X: 2, Y: 18}, {X: 2, Y: 10}}
	goals := []entity.Vec2{{X: 1, Y: 10}, {X: 19, Y: 10}}
	for index := uint32(1); index <= 12; index++ {
		for _, goal := range goals {
			for _, start := range starts {
				config := DefaultConfig()
				w, err := NewWorld(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := w.AddPlayer(1); err != nil {
					t.Fatal(err)
				}
				spawn := stage.Spawn{Position: start,
					Stats:  entity.CombatStats{MaxHealth: 100, MoveSpeed: 3, AttackCooldownTicks: TickRate},
					Radius: .4, AttackRange: 1}
				if err := w.StartStage(stage.Plan{Index: index, Seed: int64(index) * 4242,
					DifficultyScore: 1, Monsters: []stage.Spawn{spawn}}); err != nil {
					t.Fatal(err)
				}
				w.players[1].player.Position = goal
				for range 600 {
					w.Step(time.Unix(100, 0))
					w.TakeEvents()
				}
				monster := w.Snapshot().Monsters[0]
				if distance := math.Hypot(monster.Position.X-goal.X, monster.Position.Y-goal.Y); distance > 1.1 {
					t.Fatalf("stage %d path %v -> %v stuck at %+v (distance %.2f)",
						index, start, goal, monster.Position, distance)
				}
				if _, blocked := w.firstCoverHit(monster.Position, goal, 0); blocked {
					t.Fatalf("stage %d path %v -> %v stopped without line of sight", index, start, goal)
				}
			}
		}
	}
}

func TestMonsterSpawnInsideCoverMovesToOpenGround(t *testing.T) {
	config := DefaultConfig()
	w, err := NewWorld(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	block := buildCoverBlocks(config, 1, 0)[0]
	spawn := stage.Spawn{Position: entity.Vec2{X: (block.min.X + block.max.X) / 2, Y: (block.min.Y + block.max.Y) / 2},
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
