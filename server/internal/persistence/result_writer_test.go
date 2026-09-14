package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type scriptedResultSink struct {
	mu       sync.Mutex
	results  []PersistDisposition
	errors   []error
	received []ResultEnvelope
	started  chan struct{}
	release  chan struct{}
}

func (s *scriptedResultSink) Persist(ctx context.Context, envelope ResultEnvelope) (PersistDisposition, error) {
	if s.started != nil {
		select {
		case s.started <- struct{}{}:
		default:
		}
	}
	if s.release != nil {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-s.release:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, envelope.Clone())
	index := len(s.received) - 1
	if index < len(s.errors) && s.errors[index] != nil {
		return 0, s.errors[index]
	}
	if index < len(s.results) {
		return s.results[index], nil
	}
	return PersistInserted, nil
}

type memoryFailureSink struct {
	mu       sync.Mutex
	failures []ResultFailure
	err      error
}

func (s *memoryFailureSink) StoreFailure(_ context.Context, failure ResultFailure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure)
	return s.err
}

func writerOptions(failure FailureSink) ResultWriterOptions {
	return ResultWriterOptions{QueueCapacity: 2, MaxAttempts: 3, AttemptTimeout: time.Second, RetryBackoff: 0, FailureSink: failure}
}

func TestResultWriterRetriesClonesAndDrains(t *testing.T) {
	sink := &scriptedResultSink{errors: []error{ErrResultBackend, ErrResultBackend}, results: []PersistDisposition{0, 0, PersistInserted}}
	failures := &memoryFailureSink{}
	writer, err := NewResultWriter(sink, writerOptions(failures))
	if err != nil {
		t.Fatal(err)
	}
	envelope := validResultEnvelope()
	if err := writer.Submit(envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Result.Players[0].Health = 1
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	stats := writer.Stats()
	if stats.Submitted != 1 || stats.Persisted != 1 || stats.Retries != 2 || stats.Failed != 0 || stats.QueueDepth != 0 {
		t.Fatalf("unexpected writer stats: %+v", stats)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.received) != 3 || sink.received[0].Result.Players[0].Health != 80 {
		t.Fatalf("writer did not retain cloned envelope: %+v", sink.received)
	}
}

func TestResultWriterIsNonBlockingAndBounded(t *testing.T) {
	sink := &scriptedResultSink{started: make(chan struct{}, 1), release: make(chan struct{})}
	failures := &memoryFailureSink{}
	options := writerOptions(failures)
	options.QueueCapacity = 1
	writer, err := NewResultWriter(sink, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Submit(validResultEnvelope()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start persistence")
	}
	second := validResultEnvelope()
	second.MatchID = "match-002"
	if err := writer.Submit(second); err != nil {
		t.Fatal(err)
	}
	third := validResultEnvelope()
	third.MatchID = "match-003"
	startedAt := time.Now()
	if err := writer.Submit(third); !errors.Is(err, ErrResultQueueFull) {
		t.Fatalf("full queue error = %v", err)
	}
	if time.Since(startedAt) > 50*time.Millisecond {
		t.Fatal("full queue admission blocked caller")
	}
	close(sink.release)
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	if stats := writer.Stats(); stats.Submitted != 2 || stats.Persisted != 2 || stats.Rejected != 1 {
		t.Fatalf("unexpected bounded writer stats: %+v", stats)
	}
	if err := writer.Submit(validResultEnvelope()); !errors.Is(err, ErrResultWriterClosed) {
		t.Fatalf("closed writer error = %v", err)
	}
}

func TestResultWriterDeadLettersPermanentAndExhaustedFailures(t *testing.T) {
	tests := []struct {
		name         string
		errors       []error
		wantAttempts int
		wantRetries  uint64
		wantKind     string
	}{
		{"conflict", []error{ErrResultConflict}, 1, 0, "conflict"},
		{"backend", []error{ErrResultBackend, ErrResultBackend, ErrResultBackend}, 3, 2, "backend"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sink := &scriptedResultSink{errors: test.errors}
			failures := &memoryFailureSink{}
			writer, err := NewResultWriter(sink, writerOptions(failures))
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.Submit(validResultEnvelope()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := writer.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			stats := writer.Stats()
			if stats.Failed != 1 || stats.Retries != test.wantRetries {
				t.Fatalf("stats = %+v", stats)
			}
			failures.mu.Lock()
			defer failures.mu.Unlock()
			if len(failures.failures) != 1 || failures.failures[0].Attempts != test.wantAttempts || failures.failures[0].Kind != test.wantKind {
				t.Fatalf("failures = %+v", failures.failures)
			}
		})
	}
}

func TestFileFailureSinkWritesRecoverableJSONLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "results.jsonl")
	sink, err := OpenFileFailureSink(path)
	if err != nil {
		t.Fatal(err)
	}
	failure := ResultFailure{FailedAt: time.Unix(100, 0).UTC(), Attempts: 3, Kind: "backend", Envelope: validResultEnvelope()}
	if err := sink.StoreFailure(context.Background(), failure); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var restored ResultFailure
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Kind != "backend" || restored.Attempts != 3 || restored.Envelope.MatchID != failure.Envelope.MatchID {
		t.Fatalf("restored failure = %+v", restored)
	}
}
