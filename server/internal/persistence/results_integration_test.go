//go:build integration

package persistence

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
)

func TestResultStoreMySQLIdempotency(t *testing.T) {
	if os.Getenv("ODYSSEY_MYSQL_INTEGRATION") != "1" {
		t.Skip("set ODYSSEY_MYSQL_INTEGRATION=1 to use a real MySQL instance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := OpenResultStore(ctx, ResultStoreOptions{
		DSN: os.Getenv("ODYSSEY_MYSQL_DSN"), OperationTimeout: 3 * time.Second, ApplyMigrations: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	prefix := "integration-" + time.Now().UTC().Format("20060102T150405.000000000")
	envelope := validResultEnvelope()
	envelope.MatchID = prefix + "-victory"
	defeat := validResultEnvelope()
	defeat.MatchID, defeat.Result.Outcome = prefix+"-defeat", game.GameDefeat
	defeat.Result.Players[0].Alive, defeat.Result.Players[0].Health = false, 0
	abandoned := validResultEnvelope()
	abandoned.MatchID, abandoned.Result.Outcome = prefix+"-abandoned", game.GameAbandoned
	for _, candidate := range []ResultEnvelope{envelope, defeat, abandoned} {
		candidate := candidate
		defer store.database.ExecContext(context.Background(), "DELETE FROM match_results WHERE match_id = ?", candidate.MatchID)
		if got, err := store.Persist(ctx, candidate); err != nil || got != PersistInserted {
			t.Fatalf("first Persist(%s) = %v, %v", candidate.Result.Outcome, got, err)
		}
	}

	if got, err := store.Persist(ctx, envelope); err != nil || got != PersistIdempotent {
		t.Fatalf("duplicate Persist = %v, %v", got, err)
	}
	conflict := envelope.Clone()
	conflict.RoomID++
	if _, err := store.Persist(ctx, conflict); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("conflicting Persist error = %v", err)
	}
	loaded, err := store.Load(ctx, envelope.MatchID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MatchID != envelope.MatchID || loaded.RoomID != envelope.RoomID || !reflect.DeepEqual(loaded.Result, envelope.Result) {
		t.Fatalf("loaded result differs: %+v", loaded)
	}
	var results, players int
	if err := store.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM match_results WHERE match_id = ?", envelope.MatchID).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if err := store.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM match_players WHERE match_id = ?", envelope.MatchID).Scan(&players); err != nil {
		t.Fatal(err)
	}
	if results != 1 || players != len(envelope.Result.Players) {
		t.Fatalf("stored rows result/player = %d/%d", results, players)
	}
}

func TestResultWriterSurvivesMySQLFailureOffCallerPath(t *testing.T) {
	if os.Getenv("ODYSSEY_MYSQL_INTEGRATION") != "1" {
		t.Skip("real MySQL disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := OpenResultStore(ctx, ResultStoreOptions{DSN: os.Getenv("ODYSSEY_MYSQL_DSN"), OperationTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	failure := &memoryFailureSink{}
	writer, err := NewResultWriter(store, ResultWriterOptions{QueueCapacity: 1, MaxAttempts: 2,
		AttemptTimeout: 200 * time.Millisecond, RetryBackoff: time.Millisecond, FailureSink: failure})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := writer.Submit(validResultEnvelope()); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 50*time.Millisecond {
		t.Fatal("Submit waited for failed MySQL")
	}
	if err := writer.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	stats := writer.Stats()
	if stats.Failed != 1 || stats.Retries != 1 {
		t.Fatalf("failure stats = %+v", stats)
	}
	failure.mu.Lock()
	defer failure.mu.Unlock()
	if len(failure.failures) != 1 || failure.failures[0].Kind != "backend" {
		t.Fatalf("dead letters = %+v", failure.failures)
	}
}
