package persistence

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

func validResultEnvelope() ResultEnvelope {
	return ResultEnvelope{
		MatchID: "match-001", RoomID: 7, CreatedAt: time.Unix(100, 0).UTC(),
		Result: game.GameResult{
			Outcome: game.GameVictory, StartedAtTick: 10, EndedAtTick: 70, FinalStageIndex: 1,
			ClearedStages: []game.StageSummary{{Index: 1, Seed: -42, DifficultyScore: 1.1, ClearTick: 70,
				Performance: director.PerformanceMetrics{ClearTimeSeconds: 2, TeamHPPercent: 0.8, AverageDPS: 50, DamageTaken: 20, EquipmentPower: 1}}},
			Players: []game.PlayerResult{{PlayerID: 101, Alive: true, Health: 80,
				Stats:     entity.CombatStats{Attack: 20, Defense: 5, MaxHealth: 100, MoveSpeed: 5, AttackCooldownTicks: 10},
				Equipment: entity.EquipmentState{WeaponID: 1001}}},
		},
	}
}

func TestResultEnvelopeValidationAndClone(t *testing.T) {
	valid := validResultEnvelope()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	clone := valid.Clone()
	clone.Result.Players[0].Health = 1
	clone.Result.ClearedStages[0].Seed = 999
	if valid.Result.Players[0].Health != 80 || valid.Result.ClearedStages[0].Seed != -42 {
		t.Fatal("ResultEnvelope.Clone aliased result slices")
	}

	tests := []func(*ResultEnvelope){
		func(envelope *ResultEnvelope) { envelope.MatchID = "unsafe/match" },
		func(envelope *ResultEnvelope) { envelope.RoomID = 0 },
		func(envelope *ResultEnvelope) { envelope.Result.Outcome = 99 },
		func(envelope *ResultEnvelope) { envelope.Result.EndedAtTick = 9 },
		func(envelope *ResultEnvelope) { envelope.Result.FinalStageIndex = 0 },
		func(envelope *ResultEnvelope) { envelope.Result.Players = nil },
		func(envelope *ResultEnvelope) { envelope.Result.Players[0].Health = math.NaN() },
		func(envelope *ResultEnvelope) { envelope.Result.Players[0].Health = 101 },
		func(envelope *ResultEnvelope) {
			envelope.Result.Players = append(envelope.Result.Players, envelope.Result.Players[0])
		},
		func(envelope *ResultEnvelope) { envelope.Result.ClearedStages[0].DifficultyScore = math.Inf(1) },
		func(envelope *ResultEnvelope) { envelope.Result.ClearedStages[0].ClearTick = 71 },
		func(envelope *ResultEnvelope) { envelope.Result.ClearedStages = nil },
	}
	for index, mutate := range tests {
		envelope := valid.Clone()
		mutate(&envelope)
		if err := envelope.Validate(); !errors.Is(err, ErrInvalidResultEnvelope) {
			t.Errorf("case %d error = %v", index, err)
		}
	}
}

func TestResultPayloadHashCoversRoomAndResultButNotRetryTimestamp(t *testing.T) {
	first := validResultEnvelope()
	_, firstHash, err := resultPayload(first)
	if err != nil {
		t.Fatal(err)
	}
	second := first.Clone()
	second.CreatedAt = second.CreatedAt.Add(time.Hour)
	_, secondHash, err := resultPayload(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatal("retry timestamp changed idempotency hash")
	}
	second.RoomID++
	_, secondHash, _ = resultPayload(second)
	if firstHash == secondHash {
		t.Fatal("Room ID did not change idempotency hash")
	}
}

func TestResultStoreOptionsRejectMalformedDSNWithoutEchoingIt(t *testing.T) {
	password := "secret-that-must-not-echo"
	options := ResultStoreOptions{DSN: "user:" + password + "@tcp(broken", OperationTimeout: time.Second}
	err := options.Validate()
	if !errors.Is(err, ErrInvalidResultStore) || strings.Contains(err.Error(), password) {
		t.Fatalf("unsafe options error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = OpenResultStore(ctx, ResultStoreOptions{
		DSN: "user:" + password + "@tcp(127.0.0.1:3306)/odyssey", OperationTimeout: 50 * time.Millisecond,
	})
	if !errors.Is(err, ErrResultBackend) || strings.Contains(err.Error(), password) {
		t.Fatalf("unsafe backend error = %v", err)
	}
}
