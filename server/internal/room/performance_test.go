package room_test

import (
	"math"
	"testing"
	"testing/synctest"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

func TestManyRoomsKeepCombatStateIsolated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const roomCount = 24
		rooms := make([]*room.Room, roomCount)
		for i := range rooms {
			r := start(t, room.ID(i+1), room.DefaultConfig())
			rooms[i] = r
			defer r.Close()
			join(t, r, room.SessionID(1000+i), entity.ID(2000+i))
			plan := inertPlan(int64(9000 + i))
			if i == 0 {
				plan.Monsters[0].Position = entity.Vec2{X: 11, Y: 10}
				plan.Monsters[0].Stats.MaxHealth = 20
			}
			receipt, err := r.StartStage(plan)
			if err != nil {
				t.Fatal(err)
			}
			result(t, receipt, nil)
		}

		if err := rooms[0].Input(1000, game.Input{Seq: 1, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
			t.Fatal(err)
		}
		advance(game.SnapshotEvery)

		for i, r := range rooms {
			snapshot := r.LatestSnapshot()
			if snapshot.RoomID != room.ID(i+1) || len(snapshot.Players) != 1 || snapshot.Players[0].ID != entity.ID(2000+i) || snapshot.Stage.Seed != int64(9000+i) {
				t.Fatalf("room %d received foreign snapshot state: %+v", i+1, snapshot)
			}
			if i == 0 {
				if snapshot.Stage.State != stage.StageClear || len(snapshot.Monsters) != 0 {
					t.Fatalf("target room did not clear: %+v", snapshot)
				}
				continue
			}
			if snapshot.Stage.State != stage.Playing || len(snapshot.Monsters) != 1 || snapshot.Monsters[0].Health != 1e6 {
				t.Fatalf("combat leaked into room %d: %+v", i+1, snapshot)
			}
			for len(r.Events()) > 0 {
				for _, event := range (<-r.Events()).Events {
					if event.Kind != game.StageStarted {
						t.Fatalf("room %d received foreign event: %+v", i+1, event)
					}
				}
			}
		}
	})
}

func TestRoomInputRateDoesNotIncreaseMovementOrFireRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		baseline := start(t, 1, room.DefaultConfig())
		burst := start(t, 2, room.DefaultConfig())
		defer baseline.Close()
		defer burst.Close()
		join(t, baseline, 11, 101)
		join(t, burst, 22, 202)
		for _, r := range []*room.Room{baseline, burst} {
			receipt, err := r.StartStage(inertPlan(42))
			if err != nil {
				t.Fatal(err)
			}
			result(t, receipt, nil)
		}

		var baselineSeq, burstSeq uint32
		for range game.TickRate {
			baselineSeq++
			if err := baseline.Input(11, game.Input{Seq: baselineSeq, Direction: entity.Vec2{X: 1}, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
				t.Fatal(err)
			}
			for range 10 { // 10 packets per 30Hz tick models a 300Hz sender.
				burstSeq++
				if err := burst.Input(22, game.Input{Seq: burstSeq, Direction: entity.Vec2{X: 1}, Aim: entity.Vec2{X: 1}, Shoot: true}); err != nil {
					t.Fatal(err)
				}
			}
			advance(1)
		}
		// Observe through the next complete 10Hz snapshot. Both rooms keep the
		// same last intent for these ticks, so the comparison remains symmetric.
		advance(game.SnapshotEvery)

		baselineSnapshot, burstSnapshot := baseline.LatestSnapshot(), burst.LatestSnapshot()
		baselinePlayer, burstPlayer := baselineSnapshot.Players[0], burstSnapshot.Players[0]
		if math.Abs(baselinePlayer.Position.X-burstPlayer.Position.X) > 1e-12 {
			t.Fatalf("packet rate changed movement: baseline=%+v burst=%+v", baselinePlayer, burstPlayer)
		}
		if baselinePlayer.LastProcessedInputSeq != baselineSeq || burstPlayer.LastProcessedInputSeq != burstSeq {
			t.Fatalf("latest intent was not acknowledged: baseline=%d burst=%d", baselinePlayer.LastProcessedInputSeq, burstPlayer.LastProcessedInputSeq)
		}
		baselineShots, burstShots := projectileSpawns(baseline), projectileSpawns(burst)
		if baselineShots == 0 || baselineShots != burstShots {
			t.Fatalf("packet rate changed fire rate: 30Hz=%d 300Hz=%d", baselineShots, burstShots)
		}
	})
}

func inertPlan(seed int64) stage.Plan {
	return stage.Plan{Index: 1, Seed: seed, DifficultyScore: 1, Monsters: []stage.Spawn{{
		Position: entity.Vec2{X: 19, Y: 19}, Radius: 0.4, AttackRange: 0,
		Stats: entity.CombatStats{MaxHealth: 1e6, AttackCooldownTicks: 30},
	}}}
}

func projectileSpawns(r *room.Room) int {
	count := 0
	for len(r.Events()) > 0 {
		for _, event := range (<-r.Events()).Events {
			if event.Kind == game.ProjectileSpawned {
				count++
			}
		}
	}
	return count
}
