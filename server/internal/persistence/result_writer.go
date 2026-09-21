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
	ErrResultDeadLetter    = errors.New("result writer could not save accepted results to dead-letter storage")
)

// ResultSink 实现必须在 ctx 取消后返回。
type ResultSink interface {
	Persist(context.Context, ResultEnvelope) (PersistDisposition, error)
}

// FailureSink 应在底层 I/O 支持时响应 ctx 取消。Shutdown 会等待 StoreFailure 返回；
// 文件写入与 Sync 无法被 context 截止时间强制中断。
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
	// 强制排空使用一个独立于 worker/调用方 context 的总预算；由 mu 保护并在取消前设置。
	cleanupContext context.Context
	cleanupCancel  context.CancelFunc
	// 只由 run 写入，并且只在 done 关闭后读取。
	firstDeadLetterError error

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

// Submit 无阻塞地校验、复制并入队；不会把可变 slice 所有权转移给 writer。
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

// Shutdown 停止接收新结果并排空已接收队列。ctx 到期后停止数据库尝试和重试等待；
// 尚未持久化的结果使用独立的 AttemptTimeout 总预算写入死信存储。Shutdown 会等待清理，
// 但无法取消的 sink I/O 必须自然结束，因此并非绝对墙钟上限。任何未保存结果都会计数，
// 并以 ErrResultDeadLetter 返回。
func (w *ResultWriter) Shutdown(ctx context.Context) error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		return w.shutdownError()
	case <-ctx.Done():
		w.mu.Lock()
		if w.cleanupContext == nil {
			w.cleanupContext, w.cleanupCancel = context.WithTimeout(context.Background(), w.options.AttemptTimeout)
		}
		w.cancel()
		w.mu.Unlock()
		<-w.done
		return errors.Join(ctx.Err(), w.shutdownError())
	}
}

func (w *ResultWriter) run() {
	defer close(w.done)
	defer w.cancel()
	defer func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.cleanupCancel != nil {
			w.cleanupCancel()
		}
	}()
	// Shutdown 先关闭队列再取消；即使已取消仍继续排空，保证已接收结果不静默消失。
	for envelope := range w.queue {
		w.inFlight.Add(1)
		attempts, err := w.persist(envelope)
		if err != nil {
			w.storeFailure(envelope, attempts, err)
		}
		w.inFlight.Add(-1)
	}
}

func (w *ResultWriter) persist(envelope ResultEnvelope) (int, error) {
	var lastErr error
	attemptsUsed := 0
	for attempt := 1; attempt <= w.options.MaxAttempts; attempt++ {
		if err := w.ctx.Err(); err != nil {
			return attemptsUsed, err
		}
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
			return attemptsUsed, nil
		}
		lastErr = err
		if errors.Is(err, ErrInvalidResultEnvelope) || errors.Is(err, ErrResultConflict) || attempt == w.options.MaxAttempts {
			break
		}
		w.retries.Add(1)
		if !waitContext(w.ctx, w.options.RetryBackoff) {
			return attemptsUsed, w.ctx.Err()
		}
	}
	return attemptsUsed, lastErr
}

func (w *ResultWriter) storeFailure(envelope ResultEnvelope, attempts int, cause error) {
	w.failed.Add(1)
	failure := ResultFailure{FailedAt: time.Now().UTC(), Attempts: attempts, Kind: resultFailureKind(cause), Envelope: envelope.Clone()}
	// 普通失败也有截止时间；若停机打断本次尝试，改用独立强制排空预算重试该结果。
	err := w.ctx.Err()
	if err == nil {
		failureContext, cancel := context.WithTimeout(w.ctx, w.options.AttemptTimeout)
		err = w.failure.StoreFailure(failureContext, failure)
		cancel()
	}
	if err != nil && w.ctx.Err() != nil {
		w.mu.RLock()
		cleanupContext := w.cleanupContext
		w.mu.RUnlock()
		err = w.failure.StoreFailure(cleanupContext, failure)
	}
	if err != nil {
		w.deadLetterFailures.Add(1)
		if w.firstDeadLetterError == nil {
			w.firstDeadLetterError = err
		}
	}
}

func (w *ResultWriter) shutdownError() error {
	if failures := w.deadLetterFailures.Load(); failures != 0 {
		return fmt.Errorf("%w: %d envelope(s); first error: %w", ErrResultDeadLetter, failures, w.firstDeadLetterError)
	}
	return nil
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
	case errors.Is(err, context.Canceled):
		return "shutdown"
	case errors.Is(err, ErrResultConflict):
		return "conflict"
	case errors.Is(err, ErrInvalidResultEnvelope):
		return "invalid"
	default:
		return "backend"
	}
}

// FileFailureSink 在 Room Tick 外追加可恢复的 JSON Lines；每条失败记录 Sync 后才报告成功。
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
	if err := ctx.Err(); err != nil {
		return err
	}
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
