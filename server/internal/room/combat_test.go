package room_test

import (
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

func TestConcurrentCombatConsumers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		join(t, r, 11, 101)
		plan := battlePlan()
		plan.Monsters[0].Stats.MaxHealth = 1e6
		receipt, err := r.StartStage(plan)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		var workers sync.WaitGroup
		workers.Go(func() {
			for seq := uint32(1); seq <= 40; seq++ {
				err := r.Input(11, game.Input{Seq: seq, Aim: entity.Vec2{X: 1}, Shoot: true})
				if err != nil && !errors.Is(err, room.ErrClosed) {
					t.Errorf("input: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
		workers.Go(func() {
			for batch := range r.Events() {
				for i := range batch.Events {
					batch.Events[i].Health = -99
				}
			}
		})
		workers.Go(func() {
			for s := range r.Snapshots() {
				for i := range s.Monsters {
					s.Monsters[i].Health = -99
				}
			}
		})
		workers.Go(func() {
			for range 30 {
				s := r.LatestSnapshot()
				for i := range s.Monsters {
					if s.Monsters[i].Health < 0 {
						t.Error("consumer corrupted world")
					}
					s.Monsters[i].Health = -99
				}
				_ = r.Stats()
				time.Sleep(10 * time.Millisecond)
			}
		})
		time.Sleep(300 * time.Millisecond)
		r.Close()
		workers.Wait()
		if r.Stats().CloseReason != "requested" {
			t.Fatal("unexpected combat shutdown")
		}
	})
}

func battlePlan() stage.Plan {
	return stage.Plan{Index: 1, DifficultyScore: 1, Monsters: []stage.Spawn{{Position: entity.Vec2{X: 11, Y: 10},
		Stats: entity.CombatStats{MaxHealth: 20, AttackCooldownTicks: 30}, Radius: 0.4, AttackRange: 0.5}}}
}

func TestRoomStageCommandCopiesPlanAndPublishesCombat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		plan := battlePlan()
		receipt, err := r.StartStage(plan)
		if err != nil {
			t.Fatal(err)
		}
		plan.Monsters[0].Position.X = 19
		plan.Monsters[0].Stats.MaxHealth = 999
		result(t, receipt, nil)
		if err := r.Input(11, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
			t.Fatal(err)
		}
		advance(1)
		s := r.LatestSnapshot()
		if s.Stage.State != stage.StageClear || len(s.Monsters) != 0 {
			t.Fatalf("plan aliased caller or combat failed: %+v", s)
		}
		kinds := map[game.EventKind]int{}
		for len(r.Events()) > 0 {
			batch := <-r.Events()
			for _, e := range batch.Events {
				kinds[e.Kind]++
			}
		}
		if kinds[game.StageStarted] != 1 || kinds[game.DamageDealt] != 1 || kinds[game.EntityDied] != 1 || kinds[game.StageCleared] != 1 {
			t.Fatalf("events: %v", kinds)
		}
		if s.Players[0].Health != 100 {
			t.Fatal("unexpected friendly damage")
		}
	})
}

func TestReliableEventBackpressureClosesRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := room.DefaultConfig()
		c.EventCapacity = 1
		r := start(t, 1, c)
		defer r.Close()
		join(t, r, 11, 101)
		receipt, err := r.StartStage(battlePlan())
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		if len(r.Events()) != 1 {
			t.Fatal("test did not fill event queue")
		}
		if err := r.Input(11, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
			t.Fatal(err)
		}
		advance(1)
		select {
		case <-r.Done():
		default:
			t.Fatal("full reliable queue did not close room")
		}
		if r.Stats().CloseReason != "event_backpressure" || !r.LatestSnapshot().Closed || r.LatestSnapshot().Stage.State != stage.Closed {
			t.Fatal("missing close status")
		}
		if _, err := r.StartStage(battlePlan()); !errors.Is(err, room.ErrClosed) {
			t.Fatal(err)
		}
	})
}

func TestStageAdmissionAndRoomIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := start(t, 1, room.DefaultConfig())
		b := start(t, 2, room.DefaultConfig())
		defer a.Close()
		defer b.Close()
		bad := battlePlan()
		bad.Monsters = nil
		if _, err := a.StartStage(bad); err == nil {
			t.Fatal("invalid plan admitted")
		}
		receipt, err := a.StartStage(battlePlan())
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, game.ErrStageState)
		join(t, a, 11, 101)
		join(t, b, 21, 201)
		receipt, err = a.StartStage(battlePlan())
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		if err := a.Input(11, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
			t.Fatal(err)
		}
		advance(3)
		if b.LatestSnapshot().Stage.State != stage.Waiting || len(b.LatestSnapshot().Monsters) != 0 || len(b.Events()) != 0 {
			t.Fatal("combat leaked to another room")
		}
	})
}
