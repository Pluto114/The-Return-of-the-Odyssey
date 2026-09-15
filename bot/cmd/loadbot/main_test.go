package main

import (
	"context"
	"errors"
	"testing"

	botclient "github.com/Pluto114/The-Return-of-the-Odyssey/bot/internal/client"
)

func TestFunctionalWorkerRunsExactlyOneMatch(t *testing.T) {
	summary := newLifecycleSummary()
	calls := 0
	worker := workerForMode(modeFunctional, func(context.Context, int) (botclient.MatchResult, error) {
		calls++
		return botclient.MatchResult{Phase: botclient.PhaseComplete, SessionID: 1, RoomID: 2, StagesCleared: 3}, nil
	}, summary)
	if err := worker(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || summary.MatchesCompleted != 1 || summary.StagesCleared != 3 {
		t.Fatalf("calls/summary = %d/%+v", calls, summary)
	}
}

func TestSustainedWorkerRepeatsUntilCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	summary := newLifecycleSummary()
	calls := 0
	worker := workerForMode(modeSustained, func(context.Context, int) (botclient.MatchResult, error) {
		calls++
		if calls == 3 {
			cancel()
		}
		return botclient.MatchResult{Phase: botclient.PhaseComplete, SessionID: 1, RoomID: uint64(calls), StagesCleared: 3}, nil
	}, summary)
	if err := worker(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("worker error = %v, want context cancellation", err)
	}
	if calls != 3 || summary.MatchesCompleted != 3 || len(summary.PhaseFailed) != 0 {
		t.Fatalf("calls/summary = %d/%+v", calls, summary)
	}
}

func TestSummaryRecordsFailurePhaseAndLastProgress(t *testing.T) {
	summary := newLifecycleSummary()
	want := errors.New("reward failed")
	progress := botclient.MatchResult{ClientID: 4, Phase: botclient.PhaseReward, RoomID: 8, StageIndex: 2, LastServerTick: 99}
	summary.record(progress, want, false)
	if summary.PhaseFailed[string(botclient.PhaseReward)] != 1 || summary.FirstFailure != want.Error() || summary.LastProgress != progress {
		t.Fatalf("summary = %+v", summary)
	}
}
