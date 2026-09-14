package persistence

import (
	"context"
	"errors"
	"time"
)

type ResultServiceOptions struct {
	Store          ResultStoreOptions
	Writer         ResultWriterOptions
	DeadLetterPath string
}

// ResultService owns the MySQL pool, asynchronous writer, and recoverable
// dead-letter file so startup and shutdown cannot close them out of order.
type ResultService struct {
	store   *ResultStore
	writer  *ResultWriter
	failure *FileFailureSink
}

func OpenResultService(ctx context.Context, options ResultServiceOptions) (*ResultService, error) {
	failure, err := OpenFileFailureSink(options.DeadLetterPath)
	if err != nil {
		return nil, err
	}
	store, err := OpenResultStore(ctx, options.Store)
	if err != nil {
		_ = failure.Close()
		return nil, err
	}
	options.Writer.FailureSink = failure
	writer, err := NewResultWriter(store, options.Writer)
	if err != nil {
		_ = store.Close()
		_ = failure.Close()
		return nil, err
	}
	return &ResultService{store: store, writer: writer, failure: failure}, nil
}

func (s *ResultService) Writer() *ResultWriter {
	if s == nil {
		return nil
	}
	return s.writer
}

func (s *ResultService) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var result error
	if s.writer != nil {
		result = s.writer.Shutdown(ctx)
	}
	// If the drain deadline elapsed, the writer may still be unwinding a sink
	// call. Leave its dependencies open until process exit instead of racing a
	// database/file close against that goroutine.
	if result != nil {
		return result
	}
	if s.store != nil {
		result = errors.Join(result, s.store.Close())
	}
	if s.failure != nil {
		result = errors.Join(result, s.failure.Close())
	}
	return result
}

func DefaultResultWriterOptions() ResultWriterOptions {
	return ResultWriterOptions{QueueCapacity: 256, MaxAttempts: 3, AttemptTimeout: 2 * time.Second, RetryBackoff: 100 * time.Millisecond}
}
