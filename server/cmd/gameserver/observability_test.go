package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

func TestRewardAndDirectorObservationAdapters(t *testing.T) {
	metricSet := metrics.New()
	application, err := newGameApplication(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), metricSet)
	if err != nil {
		t.Fatal(err)
	}
	application.recordRewardMetrics(7, game.RewardUpdateBatch{Updates: []game.RewardUpdate{
		{Kind: game.RewardOptionsAvailable, EquipmentIDs: []equipment.ID{1001, 2001}},
		{Kind: game.RewardSelectionApplied, EquipmentID: 1001},
		{Kind: game.RewardSelectionApplied, EquipmentID: 2001, Defaulted: true},
	}})
	application.recordInvalidRewardChoice(7, game.ErrRewardState)

	input := director.PerformanceMetrics{ClearTimeSeconds: 12, TeamHPPercent: 0.8, AverageDPS: 40,
		DeathCount: 1, DamageTaken: 20, EquipmentPower: 1.1}
	output := director.Decision{PreviousDifficulty: 1, NewDifficulty: 1.1, Adjustment: 0.1,
		MonsterCount: 4, Seed: -42, Reasons: []string{"base progression +3%"}}
	if err := application.recordDirectorDecision(7, 2, input, output, 75*time.Microsecond); err != nil {
		t.Fatal(err)
	}
	output.Reasons[0] = "mutated"

	body := scrapeApplicationMetrics(t, metricSet)
	for _, sample := range []string{
		`odyssey_rewards_total{result="offered"} 1`,
		`odyssey_rewards_total{result="chosen"} 1`,
		`odyssey_rewards_total{result="defaulted"} 1`,
		`odyssey_rewards_total{result="invalid"} 1`,
		"odyssey_director_decisions_total 1",
	} {
		if !strings.Contains(body, sample) {
			t.Errorf("metrics do not contain %q", sample)
		}
	}

	snapshot := application.adminSnapshot()
	if len(snapshot.RecentDirectorDecisions) != 1 || snapshot.RecentDirectorDecisions[0].RoomID != 7 ||
		snapshot.RecentDirectorDecisions[0].StageIndex != 2 || snapshot.RecentDirectorDecisions[0].Seed != -42 ||
		snapshot.RecentDirectorDecisions[0].Reasons[0] != "base progression +3%" {
		t.Fatalf("unexpected Admin Director history: %+v", snapshot.RecentDirectorDecisions)
	}
}

func TestAdminSnapshotUsesAuthoritativeRoomState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	metricSet := metrics.New()
	application, err := newGameApplication(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), metricSet)
	if err != nil {
		t.Fatal(err)
	}
	application.setEnvironment("integration")
	config := room.DefaultConfig()
	config.EmptyTimeout = time.Second
	rm, err := room.Start(ctx, 9, config)
	if err != nil {
		t.Fatal(err)
	}
	defer rm.Close()
	receipt, err := rm.Join(11, entity.ID(101))
	if err != nil {
		t.Fatal(err)
	}
	if err := <-receipt; err != nil {
		t.Fatal(err)
	}
	plan := stage.Plan{Index: 1, Seed: -7, DifficultyScore: 1, Monsters: []stage.Spawn{{
		Position: entity.Vec2{X: 12, Y: 10}, Radius: 0.4, AttackRange: 1,
		Stats: entity.CombatStats{MaxHealth: 10, AttackCooldownTicks: 30},
	}}}
	started, err := rm.StartStage(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for rm.LatestSnapshot().Stage.State != stage.Playing && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	application.mu.Lock()
	application.rooms[9] = &activeRoom{room: rm, projectiles: map[entity.ID]struct{}{201: {}}}
	application.mu.Unlock()

	snapshot := application.adminSnapshot()
	if snapshot.Environment != "integration" || snapshot.ActiveRooms != 1 || snapshot.ActiveMonsters != 1 ||
		snapshot.ActiveProjectiles != 1 || len(snapshot.Rooms) != 1 {
		t.Fatalf("unexpected Admin snapshot: %+v", snapshot)
	}
	status := snapshot.Rooms[0]
	if status.RoomID != 9 || status.StageIndex != 1 || status.StageSeed != -7 || status.Phase != "playing" || status.Players != 1 {
		t.Fatalf("unexpected Admin room: %+v", status)
	}
}
