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

// ResultService 统一拥有 MySQL 连接池、异步 writer 和可恢复死信文件，保证启停顺序正确。
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
	// 即使要返回超时或持久化错误，Shutdown 仍等待 worker 与有界死信清理结束，
	// 之后关闭依赖才不会和未完成写入竞争。
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
