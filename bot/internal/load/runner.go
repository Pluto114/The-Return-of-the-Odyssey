// Package load coordinates concurrent bot lifecycles independently of their
// network and protocol implementation.
package load

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrInvalidClients  = errors.New("client count must be positive")
	ErrInvalidRampUp   = errors.New("ramp-up duration must not be negative")
	ErrInvalidDuration = errors.New("run duration must be positive")
	ErrNilWorker       = errors.New("worker must not be nil")
)

// Config controls one load run. RampUp is the time between the first and last
// planned client start; Duration is measured from the beginning of Run.
type Config struct {
	Clients  int
	RampUp   time.Duration
	Duration time.Duration
}

// Worker runs one numbered bot until completion or context cancellation. A
// Worker must return promptly after ctx is done. Run treats ctx cancellation
// caused by the configured Duration as a normal bot shutdown.
type Worker func(ctx context.Context, clientID int) error

// Report summarizes one run without retaining an error per bot, so memory use
// remains bounded when testing thousands of clients.
type Report struct {
	Planned   int
	Started   int
	Succeeded int
	Failed    int
	Elapsed   time.Duration
}

type workerResult struct {
	err              error
	expectedShutdown bool
}

// WorkerError reports aggregated bot failures while retaining the first cause
// for diagnostics and errors.Is/errors.As.
type WorkerError struct {
	Count int
	First error
}

func (e *WorkerError) Error() string {
	return fmt.Sprintf("%d bot workers failed; first error: %v", e.Count, e.First)
}

func (e *WorkerError) Unwrap() error {
	return e.First
}

// Run starts bots according to Config, waits for every started Worker, and
// returns aggregate results. Configuration errors are returned before any
// Worker starts.
func Run(ctx context.Context, config Config, worker Worker) (Report, error) {
	if err := validate(config, worker); err != nil {
		return Report{}, err
	}

	startedAt := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, config.Duration)
	defer cancel()

	report := Report{Planned: config.Clients}
	if err := runCtx.Err(); err != nil {
		report.Elapsed = time.Since(startedAt)
		return report, err
	}

	results := make(chan workerResult, config.Clients)
	var workers sync.WaitGroup

	startWorker := func(clientID int) {
		report.Started++
		workers.Add(1)
		go func() {
			defer workers.Done()
			workerErr := worker(runCtx, clientID)
			results <- workerResult{
				err:              workerErr,
				expectedShutdown: expectedShutdown(workerErr, runCtx.Err()),
			}
		}()
	}

	startWorker(0)
	interval := rampInterval(config)
	for clientID := 1; clientID < config.Clients; clientID++ {
		if interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-runCtx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				clientID = config.Clients
				continue
			case <-timer.C:
			}
		} else {
			select {
			case <-runCtx.Done():
				clientID = config.Clients
				continue
			default:
			}
		}
		startWorker(clientID)
	}

	workers.Wait()
	close(results)

	var firstFailure error
	for result := range results {
		if result.err == nil || result.expectedShutdown {
			report.Succeeded++
			continue
		}
		report.Failed++
		if firstFailure == nil {
			firstFailure = result.err
		}
	}
	report.Elapsed = time.Since(startedAt)

	if report.Failed > 0 {
		return report, &WorkerError{Count: report.Failed, First: firstFailure}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, nil
}

func validate(config Config, worker Worker) error {
	if config.Clients <= 0 {
		return ErrInvalidClients
	}
	if config.RampUp < 0 {
		return ErrInvalidRampUp
	}
	if config.Duration <= 0 {
		return ErrInvalidDuration
	}
	if worker == nil {
		return ErrNilWorker
	}
	return nil
}

func rampInterval(config Config) time.Duration {
	if config.Clients == 1 {
		return 0
	}
	return config.RampUp / time.Duration(config.Clients-1)
}

func expectedShutdown(workerErr, runErr error) bool {
	return runErr != nil && (errors.Is(workerErr, context.Canceled) || errors.Is(workerErr, context.DeadlineExceeded))
}
