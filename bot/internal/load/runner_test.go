package load

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRunRejectsInvalidConfigurationBeforeStartingWorkers(t *testing.T) {
	t.Parallel()

	valid := Config{Clients: 1, Duration: time.Second}
	tests := []struct {
		name   string
		config Config
		worker Worker
		want   error
	}{
		{name: "clients", config: Config{Duration: time.Second}, worker: successfulWorker, want: ErrInvalidClients},
		{name: "ramp up", config: Config{Clients: 1, RampUp: -time.Second, Duration: time.Second}, worker: successfulWorker, want: ErrInvalidRampUp},
		{name: "duration", config: Config{Clients: 1}, worker: successfulWorker, want: ErrInvalidDuration},
		{name: "worker", config: valid, worker: nil, want: ErrNilWorker},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := false
			worker := test.worker
			if worker != nil {
				worker = func(ctx context.Context, clientID int) error {
					started = true
					return test.worker(ctx, clientID)
				}
			}
			if _, err := Run(context.Background(), test.config, worker); !errors.Is(err, test.want) {
				t.Fatalf("Run() error = %v, want %v", err, test.want)
			}
			if started {
				t.Fatal("worker started for invalid configuration")
			}
		})
	}
}

func TestRunStartsEachClientOnce(t *testing.T) {
	t.Parallel()

	const clients = 100
	seen := make(map[int]int, clients)
	var mu sync.Mutex
	worker := func(_ context.Context, clientID int) error {
		mu.Lock()
		defer mu.Unlock()
		seen[clientID]++
		return nil
	}

	report, err := Run(context.Background(), Config{Clients: clients, Duration: time.Second}, worker)
	if err != nil {
		t.Fatal(err)
	}
	if report.Planned != clients || report.Started != clients || report.Succeeded != clients || report.Failed != 0 {
		t.Fatalf("report = %+v, want all %d clients successful", report, clients)
	}
	for clientID := 0; clientID < clients; clientID++ {
		if seen[clientID] != 1 {
			t.Errorf("client %d starts = %d, want 1", clientID, seen[clientID])
		}
	}
}

func TestRunUsesDurationAsNormalWorkerShutdown(t *testing.T) {
	t.Parallel()

	const clients = 10
	worker := func(ctx context.Context, _ int) error {
		<-ctx.Done()
		return ctx.Err()
	}
	report, err := Run(context.Background(), Config{
		Clients:  clients,
		RampUp:   10 * time.Millisecond,
		Duration: 50 * time.Millisecond,
	}, worker)
	if err != nil {
		t.Fatal(err)
	}
	if report.Started != clients || report.Succeeded != clients || report.Failed != 0 {
		t.Fatalf("report = %+v, want duration to stop all clients normally", report)
	}
	if report.Elapsed < 40*time.Millisecond {
		t.Fatalf("elapsed = %s, want approximately configured duration", report.Elapsed)
	}
}

func TestRunAggregatesWorkerFailures(t *testing.T) {
	t.Parallel()

	wantFailure := errors.New("dial failed")
	worker := func(_ context.Context, clientID int) error {
		if clientID%2 == 0 {
			return wantFailure
		}
		return nil
	}
	report, err := Run(context.Background(), Config{Clients: 6, Duration: time.Second}, worker)
	if report.Failed != 3 || report.Succeeded != 3 {
		t.Fatalf("report = %+v, want 3 successes and 3 failures", report)
	}
	var runErr *WorkerError
	if !errors.As(err, &runErr) {
		t.Fatalf("Run() error = %v, want WorkerError", err)
	}
	if runErr.Count != 3 || !errors.Is(runErr, wantFailure) {
		t.Fatalf("WorkerError = %+v, want count 3 wrapping first failure", runErr)
	}
}

func TestRunStopsLaunchingWhenParentIsCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	worker := func(ctx context.Context, clientID int) error {
		if clientID == 0 {
			cancel()
		}
		<-ctx.Done()
		return ctx.Err()
	}
	report, err := Run(ctx, Config{
		Clients:  20,
		RampUp:   time.Second,
		Duration: 2 * time.Second,
	}, worker)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want %v", err, context.Canceled)
	}
	if report.Started != 1 || report.Succeeded != 1 {
		t.Fatalf("report = %+v, want only first client started and stopped", report)
	}
}

func TestRunDoesNotStartWithCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := false
	report, err := Run(ctx, Config{Clients: 10, Duration: time.Second}, func(context.Context, int) error {
		started = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want %v", err, context.Canceled)
	}
	if started || report.Started != 0 {
		t.Fatalf("report = %+v, worker started for canceled context", report)
	}
}

func TestRunDoesNotHideWorkerDeadlineFailure(t *testing.T) {
	t.Parallel()

	report, err := Run(context.Background(), Config{Clients: 1, Duration: time.Second}, func(context.Context, int) error {
		return context.DeadlineExceeded
	})
	if report.Failed != 1 {
		t.Fatalf("report = %+v, want one failed worker", report)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want worker deadline failure", err)
	}
}

func successfulWorker(context.Context, int) error {
	return nil
}
