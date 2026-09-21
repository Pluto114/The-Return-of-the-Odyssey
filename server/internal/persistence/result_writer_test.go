package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func (s *memoryFailureSink) StoreFailure(ctx context.Context, failure ResultFailure) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure)
	return s.err
}

type failureSinkFunc func(context.Context, ResultFailure) error

func (f failureSinkFunc) StoreFailure(ctx context.Context, failure ResultFailure) error {
	return f(ctx, failure)
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

func TestResultWriterShutdownDuringRetrySavesInFlightResult(t *testing.T) {
	sink := &scriptedResultSink{errors: []error{ErrResultBackend}}
	failures := &memoryFailureSink{}
	options := writerOptions(failures)
	options.RetryBackoff = time.Hour
	writer, err := NewResultWriter(sink, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = writer.Shutdown(ctx)
	})
	if err := writer.Submit(validResultEnvelope()); err != nil {
		t.Fatal(err)
	}
	// 计数在重试等待前立即发布，因此停机测试无需依赖任意 sleep 即可覆盖该路径。
	deadline := time.Now().Add(time.Second)
	for writer.Stats().Retries == 0 {
		if time.Now().After(deadline) {
			t.Fatal("writer did not reach its retry wait")
		}
		runtime.Gosched()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writer.Shutdown(ctx); !errors.Is(err, context.Canceled) || errors.Is(err, ErrResultDeadLetter) {
		t.Fatalf("Shutdown = %v, want cancellation with successful dead-letter cleanup", err)
	}
	if stats := writer.Stats(); stats.Submitted != 1 || stats.Failed != 1 || stats.DeadLetterFailures != 0 || stats.InFlight != 0 || stats.QueueDepth != 0 {
		t.Fatalf("shutdown stats = %+v", stats)
	}
	if len(failures.failures) != 1 || failures.failures[0].Attempts != 1 || failures.failures[0].Kind != "shutdown" {
		t.Fatalf("in-flight dead letters = %+v", failures.failures)
	}
}

func TestResultWriterShutdownSavesInFlightAndQueuedResults(t *testing.T) {
	sink := &scriptedResultSink{started: make(chan struct{}, 1), release: make(chan struct{})}
	failures := &memoryFailureSink{}
	writer, err := NewResultWriter(sink, writerOptions(failures))
	if err != nil {
		t.Fatal(err)
	}
	submitBlockedResults(t, writer, sink)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writer.Shutdown(ctx); !errors.Is(err, context.Canceled) || errors.Is(err, ErrResultDeadLetter) {
		t.Fatalf("Shutdown = %v", err)
	}
	if stats := writer.Stats(); stats.Submitted != 3 || stats.Failed != 3 || stats.DeadLetterFailures != 0 || stats.InFlight != 0 || stats.QueueDepth != 0 {
		t.Fatalf("shutdown stats = %+v", stats)
	}
	if len(failures.failures) != 3 {
		t.Fatalf("dead letters = %+v", failures.failures)
	}
	for i, failure := range failures.failures {
		wantAttempts := 0
		if i == 0 {
			wantAttempts = 1
		}
		if failure.Envelope.RoomID != uint64(i+1) || failure.Attempts != wantAttempts {
			t.Fatalf("dead letter %d = %+v", i, failure)
		}
	}
}

func TestResultWriterShutdownReportsDeadLetterFailure(t *testing.T) {
	sink := &scriptedResultSink{started: make(chan struct{}, 1), release: make(chan struct{})}
	diskError := errors.New("dead-letter disk unavailable")
	failures := &memoryFailureSink{err: diskError}
	writer, err := NewResultWriter(sink, writerOptions(failures))
	if err != nil {
		t.Fatal(err)
	}
	submitBlockedResults(t, writer, sink)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = writer.Shutdown(ctx)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrResultDeadLetter) || !errors.Is(err, diskError) {
		t.Fatalf("Shutdown = %v, want cancellation and visible dead-letter failure", err)
	}
	if stats := writer.Stats(); stats.Failed != 3 || stats.DeadLetterFailures != 3 || stats.QueueDepth != 0 || stats.InFlight != 0 {
		t.Fatalf("shutdown stats = %+v", stats)
	}
	if err := writer.Shutdown(context.Background()); !errors.Is(err, ErrResultDeadLetter) {
		t.Fatalf("repeated Shutdown lost failure: %v", err)
	}
}

func TestResultWriterShutdownUsesOneDeadLetterDeadline(t *testing.T) {
	sink := &scriptedResultSink{started: make(chan struct{}, 1), release: make(chan struct{})}
	var deadlines []time.Time
	failure := failureSinkFunc(func(ctx context.Context, _ ResultFailure) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("dead-letter cleanup has no deadline")
		}
		deadlines = append(deadlines, deadline)
		<-ctx.Done()
		return ctx.Err()
	})
	options := writerOptions(failure)
	options.AttemptTimeout = 250 * time.Millisecond
	writer, err := NewResultWriter(sink, options)
	if err != nil {
		t.Fatal(err)
	}
	submitBlockedResults(t, writer, sink)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = writer.Shutdown(ctx)
	if !errors.Is(err, ErrResultDeadLetter) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want exhausted cleanup deadline", err)
	}
	if len(deadlines) != 3 || !deadlines[0].Equal(deadlines[1]) || !deadlines[0].Equal(deadlines[2]) {
		t.Fatalf("cleanup deadlines must be shared across queued results: %v", deadlines)
	}
	if stats := writer.Stats(); stats.DeadLetterFailures != 3 || stats.QueueDepth != 0 || stats.InFlight != 0 {
		t.Fatalf("expired cleanup stats = %+v", stats)
	}
}

func submitBlockedResults(t *testing.T, writer *ResultWriter, sink *scriptedResultSink) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = writer.Shutdown(ctx)
	})
	for i := 0; i < 3; i++ {
		envelope := validResultEnvelope()
		envelope.RoomID = uint64(i + 1)
		if err := writer.Submit(envelope); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			select {
			case <-sink.started:
			case <-time.After(time.Second):
				t.Fatal("writer did not start persistence")
			}
		}
	}
}

func TestResultServiceClosesFileAfterForcedDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.jsonl")
	failure, err := OpenFileFailureSink(path)
	if err != nil {
		t.Fatal(err)
	}
	sink := &scriptedResultSink{started: make(chan struct{}, 1), release: make(chan struct{})}
	writer, err := NewResultWriter(sink, writerOptions(failure))
	if err != nil {
		t.Fatal(err)
	}
	submitBlockedResults(t, writer, sink)
	service := &ResultService{writer: writer, failure: failure}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Shutdown(ctx); !errors.Is(err, context.Canceled) || errors.Is(err, ErrResultDeadLetter) {
		t.Fatalf("service Shutdown = %v", err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	for decoder.More() {
		var line json.RawMessage
		if err := decoder.Decode(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if len(lines) != 3 {
		t.Fatalf("saved %d dead-letter records, want 3", len(lines))
	}
	if err := failure.StoreFailure(context.Background(), ResultFailure{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("dead-letter file was not closed after drain: %v", err)
	}
}
