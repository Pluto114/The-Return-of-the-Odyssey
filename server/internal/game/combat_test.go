package game_test

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
)

func target(x, y, hp float64) stage.Spawn {
	return stage.Spawn{Position: entity.Vec2{X: x, Y: y}, Stats: entity.CombatStats{MaxHealth: hp, AttackCooldownTicks: 30}, Radius: 0.4, AttackRange: 1}
}
func encounter(t *testing.T, c game.Config, spawns ...stage.Spawn) *game.World {
	t.Helper()
	w, err := game.NewWorld(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	if err := w.StartStage(stage.Plan{Index: 1, Seed: 42, DifficultyScore: 1, Monsters: spawns}); err != nil {
		t.Fatal(err)
	}
	return w
}
func stepInput(t *testing.T, w *game.World, i game.Input) []game.Event {
	t.Helper()
	now := time.Unix(100, 0).Add(time.Duration(w.Tick()) * game.TickInterval)
	if err := w.ApplyInput(1, i, now); err != nil {
		t.Fatal(err)
	}
	w.Step(now)
	b := w.TakeEvents()
	if b.Overflow {
		t.Fatal("unexpected event overflow")
	}
	return b.Events
}
func countEvents(events []game.Event, kind game.EventKind) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func TestProjectileDamageDeathAndStageClear(t *testing.T) {
	w := encounter(t, game.DefaultConfig(), target(13, 10, 40))
	var events []game.Event
	for seq := uint32(1); seq <= 30; seq++ {
		events = append(events, stepInput(t, w, game.Input{Seq: seq, Aim: entity.Vec2{X: 1}, Shoot: true})...)
	}
	s := w.Snapshot()
	if s.Stage.State != stage.StageClear || len(s.Monsters) != 0 || s.Players[0].Health != 100 {
		t.Fatalf("battle result: %+v", s)
	}
	for kind, want := range map[game.EventKind]int{game.StageStarted: 1, game.DamageDealt: 2, game.EntityDied: 1, game.StageCleared: 1, game.TeamDefeated: 0} {
		if got := countEvents(events, kind); got != want {
			t.Fatalf("event %d: %d, want %d", kind, got, want)
		}
	}
	for _, e := range events {
		if (e.Kind == game.StageStarted || e.Kind == game.StageCleared) && e.StageIndex != 1 {
			t.Fatalf("stage event %d has index %d, want 1", e.Kind, e.StageIndex)
		}
	}
	spawns, destroys := map[entity.ID]bool{}, map[entity.ID]bool{}
	var lastTick uint64
	for _, e := range events {
		if e.ServerTick < lastTick {
			t.Fatal("event order regressed")
		}
		lastTick = e.ServerTick
		if e.Kind == game.ProjectileSpawned {
			if spawns[e.EntityID] {
				t.Fatal("reused projectile ID")
			}
			spawns[e.EntityID] = true
		}
		if e.Kind == game.ProjectileDestroyed {
			if destroys[e.EntityID] {
				t.Fatal("double destroy")
			}
			destroys[e.EntityID] = true
		}
	}
	if !reflect.DeepEqual(spawns, destroys) {
		t.Fatal("projectiles leaked after clear")
	}
}

func TestFireCooldownIndependentOfPacketRate(t *testing.T) {
	for _, packets := range []int{1, 10} {
		w := encounter(t, game.DefaultConfig(), target(19, 19, 1e6))
		var seq uint32
		var events []game.Event
		for range 30 {
			now := time.Unix(100, 0).Add(time.Duration(w.Tick()) * game.TickInterval)
			for range packets {
				seq++
				if err := w.ApplyInput(1, game.Input{Seq: seq, Aim: entity.Vec2{X: -1}, Shoot: true}, now); err != nil {
					t.Fatal(err)
				}
			}
			w.Step(now)
			events = append(events, w.TakeEvents().Events...)
		}
		if countEvents(events, game.ProjectileSpawned) != 5 {
			t.Fatalf("%d packets/tick changed fire rate", packets)
		}
	}
}

func TestShootingReleaseTimeoutAndInvalidAim(t *testing.T) {
	for _, release := range []bool{true, false} {
		w := encounter(t, game.DefaultConfig(), target(19, 19, 100))
		now := time.Unix(100, 0)
		if err := w.ApplyInput(1, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}, now); err != nil {
			t.Fatal(err)
		}
		w.Step(now)
		w.TakeEvents()
		if release {
			if err := w.ApplyInput(1, game.Input{Seq: 2}, now.Add(time.Millisecond)); err != nil {
				t.Fatal(err)
			}
		}
		var events []game.Event
		for range 30 {
			w.Step(now.Add(time.Second))
			events = append(events, w.TakeEvents().Events...)
		}
		if countEvents(events, game.ProjectileSpawned) != 0 {
			t.Fatal("stale/released trigger kept firing")
		}
	}
	for _, aim := range []entity.Vec2{{}, {X: math.NaN()}, {Y: math.Inf(1)}} {
		if err := (game.Input{Seq: 1, Aim: aim, Shoot: true}).Validate(); err == nil {
			t.Fatal("invalid aim accepted")
		}
	}
}

func TestFastProjectileHitsNearestOnly(t *testing.T) {
	c := game.DefaultConfig()
	c.Combat.ProjectileSpeed = 300
	// 故意先分配较远目标，ID 顺序不能覆盖距离优先级。
	w := encounter(t, c, target(17, 10, 100), target(13, 10, 100))
	stepInput(t, w, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true})
	m := w.Snapshot().Monsters
	if m[0].Health != 100 || m[1].Health != 80 {
		t.Fatalf("wrong swept collision target: %+v", m)
	}
}

func TestMovingTargetUsesRelativeSweep(t *testing.T) {
	c := game.DefaultConfig()
	c.Combat.ProjectileSpeed = 300
	m := target(10, 11, 100)
	m.Stats.MoveSpeed = 30
	m.AttackRange = 0
	w := encounter(t, c, m)
	stepInput(t, w, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true})
	// 子弹离开后怪物才到达其起点；若只检测怪物最终位置会误报命中。
	if w.Snapshot().Monsters[0].Health != 100 {
		t.Fatal("false hit on a moving target")
	}
}

func TestMonsterChasesAtFixedRateAndRetargets(t *testing.T) {
	config := game.DefaultConfig()
	config.CoverEnabled = false
	w, err := game.NewWorld(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(2); err != nil {
		t.Fatal(err)
	}
	if err := w.AddPlayer(1); err != nil {
		t.Fatal(err)
	}
	m := target(5, 10, 100)
	m.Stats.MoveSpeed = 3
	m.Stats.Attack = 10
	if err := w.StartStage(stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{m}}); err != nil {
		t.Fatal(err)
	}
	for range 30 {
		w.Step(time.Unix(100, 0))
		w.TakeEvents()
	}
	near(t, w.Snapshot().Monsters[0].Position.X, 8)
	for range 35 {
		w.Step(time.Unix(100, 0))
		w.TakeEvents()
	}
	players := w.Snapshot().Players
	if players[0].Health != 90 || players[1].Health != 100 {
		t.Fatalf("nearest tie-break/cooldown: %+v", players)
	}
	w.RemovePlayer(1)
	for range 33 {
		w.Step(time.Unix(100, 0))
		w.TakeEvents()
	}
	if w.Snapshot().Players[0].Health != 90 {
		t.Fatal("monster did not retarget remaining player")
	}
}

func TestTeamDefeatStopsCombatAndDeadPlayerMovement(t *testing.T) {
	m := target(10, 10, 100)
	m.Stats.Attack = 100
	w := encounter(t, game.DefaultConfig(), m)
	events := stepInput(t, w, game.Input{Seq: 1})
	if w.Snapshot().Stage.State != stage.Failed || w.Snapshot().Players[0].Alive || countEvents(events, game.TeamDefeated) != 1 {
		t.Fatal("team wipe missing")
	}
	for _, e := range events {
		if e.Kind == game.TeamDefeated && e.StageIndex != 1 {
			t.Fatalf("team defeat has stage index %d, want 1", e.StageIndex)
		}
	}
	pos := w.Snapshot().Players[0].Position
	for seq := uint32(2); seq < 30; seq++ {
		e := stepInput(t, w, game.Input{Seq: seq, Direction: entity.Vec2{X: 1}, Aim: entity.Vec2{X: 1}, Shoot: true})
		if len(e) != 0 {
			t.Fatal("combat continued after defeat")
		}
	}
	if w.Snapshot().Players[0].Position != pos {
		t.Fatal("dead player moved")
	}
}

func TestSimultaneousFinalKillAndWipeIsDefeat(t *testing.T) {
	m := target(10, 10, 20)
	m.Stats.Attack = 100
	w := encounter(t, game.DefaultConfig(), m)
	e := stepInput(t, w, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true})
	if w.Snapshot().Stage.State != stage.Failed || countEvents(e, game.StageCleared) != 0 || countEvents(e, game.EntityDied) != 2 {
		t.Fatal("simultaneous result is not deterministic defeat")
	}
}

func TestProjectileCapacityAndLifetime(t *testing.T) {
	c := game.DefaultConfig()
	c.Combat.MaxProjectiles = 1
	c.Combat.ProjectileSpeed = 0.01
	c.Combat.ProjectileLifetimeTicks = 10
	c.Combat.PlayerStats.AttackCooldownTicks = 1
	w := encounter(t, c, target(19, 19, 100))
	active, maxActive := 0, 0
	for seq := uint32(1); seq <= 25; seq++ {
		for _, e := range stepInput(t, w, game.Input{Seq: seq, Aim: entity.Vec2{X: 1}, Shoot: true}) {
			if e.Kind == game.ProjectileSpawned {
				active++
				maxActive = max(maxActive, active)
			}
			if e.Kind == game.ProjectileDestroyed {
				active--
			}
		}
	}
	if maxActive != 1 || active != 1 {
		t.Fatal("projectile capacity or expiry violated")
	}
}

func TestStageValidationAndSnapshotCopies(t *testing.T) {
	w := world(t)
	if err := w.AddPlayer(game.FirstWorldEntityID); !errors.Is(err, game.ErrInvalidPlayer) {
		t.Fatal("reserved ID accepted")
	}
	good := stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{target(15, 10, 100)}}
	for _, modify := range []func(*stage.Plan){func(p *stage.Plan) { p.Index = 0 }, func(p *stage.Plan) { p.Monsters = nil }, func(p *stage.Plan) { p.Monsters[0].Position.X = 99 }, func(p *stage.Plan) { p.Monsters[0].Stats.Attack = math.NaN() }, func(p *stage.Plan) { p.Monsters[0].Radius = -1 }} {
		p := good.Clone()
		modify(&p)
		if err := w.StartStage(p); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
	if w.Snapshot().Stage.State != stage.Waiting {
		t.Fatal("failed plan mutated stage")
	}
	if err := w.StartStage(good); err != nil {
		t.Fatal(err)
	}
	good.Monsters[0].Stats.MaxHealth = 999
	s := w.Snapshot()
	s.Monsters[0].Health = 0
	copy := s.Clone()
	copy.Monsters[0].Position.X = 999
	if w.Snapshot().Monsters[0].Health != 100 || w.Snapshot().Monsters[0].Position.X != 15 {
		t.Fatal("monster snapshot aliased world")
	}
	if err := w.StartStage(good); !errors.Is(err, game.ErrStageState) {
		t.Fatal("stage restart accepted")
	}
	if err := w.AddPlayer(2); !errors.Is(err, game.ErrStageState) {
		t.Fatal("mid-fight join accepted without policy")
	}
}

func TestCombatReplayIsDeterministic(t *testing.T) {
	replay := func() ([]game.Event, game.Snapshot) {
		m := target(15, 10, 100)
		m.Stats.MoveSpeed = 2
		m.Stats.Attack = 10
		w := encounter(t, game.DefaultConfig(), m, target(17, 12, 100))
		var events []game.Event
		for seq := uint32(1); seq <= 180; seq++ {
			events = append(events, stepInput(t, w, game.Input{Seq: seq, Aim: entity.Vec2{X: 1}, Shoot: true})...)
		}
		return events, w.Snapshot()
	}
	a, sa := replay()
	b, sb := replay()
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(sa, sb) {
		t.Fatal("same plan and inputs produced different simulation")
	}
}
