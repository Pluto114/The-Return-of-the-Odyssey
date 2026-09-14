package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrInvalidResultWriter = errors.New("invalid result writer options")
	ErrResultQueueFull     = errors.New("result writer queue is full")
	ErrResultWriterClosed  = errors.New("result writer is closed")
	ErrInvalidDeadLetter   = errors.New("invalid result dead-letter path")
)

type ResultSink interface {
	Persist(context.Context, ResultEnvelope) (PersistDisposition, error)
}

type FailureSink interface {
	StoreFailure(context.Context, ResultFailure) error
}

type ResultFailure struct {
	FailedAt time.Time      `json:"failed_at"`
	Attempts int            `json:"attempts"`
	Kind     string         `json:"kind"`
	Envelope ResultEnvelope `json:"envelope"`
}

type ResultWriterOptions struct {
	QueueCapacity  int
	MaxAttempts    int
	AttemptTimeout time.Duration
	RetryBackoff   time.Duration
	FailureSink    FailureSink
}

func (o ResultWriterOptions) Validate() error {
	if o.QueueCapacity < 1 || o.QueueCapacity > 100000 || o.MaxAttempts < 1 || o.MaxAttempts > 100 ||
		o.AttemptTimeout <= 0 || o.RetryBackoff < 0 || o.FailureSink == nil {
		return ErrInvalidResultWriter
	}
	return nil
}

type ResultWriterStats struct {
	QueueDepth         int
	InFlight           int64
	Submitted          uint64
	Persisted          uint64
	Idempotent         uint64
	Retries            uint64
	Failed             uint64
	Rejected           uint64
	DeadLetterFailures uint64
}

type ResultWriter struct {
	sink    ResultSink
	failure FailureSink
	options ResultWriterOptions
	queue   chan ResultEnvelope
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	mu     sync.RWMutex
	closed bool

	inFlight           atomic.Int64
	submitted          atomic.Uint64
	persisted          atomic.Uint64
	idempotent         atomic.Uint64
	retries            atomic.Uint64
	failed             atomic.Uint64
	rejected           atomic.Uint64
	deadLetterFailures atomic.Uint64
}

func NewResultWriter(sink ResultSink, options ResultWriterOptions) (*ResultWriter, error) {
	if sink == nil {
		return nil, ErrInvalidResultWriter
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	writer := &ResultWriter{
		sink: sink, failure: options.FailureSink, options: options,
		queue: make(chan ResultEnvelope, options.QueueCapacity), ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	go writer.run()
	return writer, nil
}

// Submit validates, clones and admits without blocking. It is safe for A's
// lifecycle goroutine and cannot transfer mutable slices to the writer.
func (w *ResultWriter) Submit(envelope ResultEnvelope) error {
	if err := envelope.Validate(); err != nil {
		w.rejected.Add(1)
		return err
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.rejected.Add(1)
		return ErrResultWriterClosed
	}
	select {
	case w.queue <- envelope.Clone():
		w.submitted.Add(1)
		return nil
	default:
		w.rejected.Add(1)
		return ErrResultQueueFull
	}
}

func (w *ResultWriter) Stats() ResultWriterStats {
	return ResultWriterStats{
		QueueDepth: len(w.queue), InFlight: w.inFlight.Load(), Submitted: w.submitted.Load(),
		Persisted: w.persisted.Load(), Idempotent: w.idempotent.Load(), Retries: w.retries.Load(),
		Failed: w.failed.Load(), Rejected: w.rejected.Load(), DeadLetterFailures: w.deadLetterFailures.Load(),
	}
}

// Shutdown stops admission and drains accepted envelopes. If the caller's
// deadline expires, in-flight work is cancelled and remaining queue depth is
// retained in Stats for the shutdown log.
func (w *ResultWriter) Shutdown(ctx context.Context) error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		w.cancel()
		return ctx.Err()
	}
}

func (w *ResultWriter) run() {
	defer close(w.done)
	defer w.cancel()
	for {
		select {
		case <-w.ctx.Done():
			return
		case envelope, open := <-w.queue:
			if !open {
				return
			}
			w.persist(envelope)
		}
	}
}

func (w *ResultWriter) persist(envelope ResultEnvelope) {
	w.inFlight.Add(1)
	defer w.inFlight.Add(-1)
	var lastErr error
	attemptsUsed := 0
	for attempt := 1; attempt <= w.options.MaxAttempts; attempt++ {
		attemptsUsed = attempt
		attemptContext, cancel := context.WithTimeout(w.ctx, w.options.AttemptTimeout)
		disposition, err := w.sink.Persist(attemptContext, envelope)
		cancel()
		if err == nil {
			if disposition == PersistIdempotent {
				w.idempotent.Add(1)
			} else {
				w.persisted.Add(1)
			}
			return
		}
		lastErr = err
		if errors.Is(err, ErrInvalidResultEnvelope) || errors.Is(err, ErrResultConflict) || attempt == w.options.MaxAttempts {
			break
		}
		w.retries.Add(1)
		if !waitContext(w.ctx, w.options.RetryBackoff) {
			return
		}
	}
	w.failed.Add(1)
	failure := ResultFailure{FailedAt: time.Now().UTC(), Attempts: attemptsUsed, Kind: resultFailureKind(lastErr), Envelope: envelope.Clone()}
	if err := w.failure.StoreFailure(w.ctx, failure); err != nil {
		w.deadLetterFailures.Add(1)
	}
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	if duration == 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func resultFailureKind(err error) string {
	switch {
	case errors.Is(err, ErrResultConflict):
		return "conflict"
	case errors.Is(err, ErrInvalidResultEnvelope):
		return "invalid"
	default:
		return "backend"
	}
}

// FileFailureSink appends recoverable JSON Lines outside the Room Tick. Each
// accepted failure is synced before success is reported to the writer.
type FileFailureSink struct {
	mu   sync.Mutex
	file *os.File
}

func OpenFileFailureSink(path string) (*FileFailureSink, error) {
	if filepath.Clean(path) == "." || path == "" {
		return nil, ErrInvalidDeadLetter
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create result dead-letter directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open result dead-letter: %w", err)
	}
	return &FileFailureSink{file: file}, nil
}

func (s *FileFailureSink) StoreFailure(ctx context.Context, failure ResultFailure) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := json.NewEncoder(s.file).Encode(failure); err != nil {
		return fmt.Errorf("write result dead-letter: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("sync result dead-letter: %w", err)
	}
	return nil
}

func (s *FileFailureSink) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}
