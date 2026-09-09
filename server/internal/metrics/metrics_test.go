package metrics

import (
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMetricsExposeApplicationState(t *testing.T) {
	t.Parallel()

	metrics := New()
	if err := metrics.SetSnapshot(Snapshot{
		OnlinePlayers:     12,
		ActiveRooms:       3,
		MatchQueuePlayers: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveMatch(250 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveReconnect(ReconnectSucceeded); err != nil {
		t.Fatal(err)
	}
	metrics.ObserveTickWork(750 * time.Microsecond)

	body := scrape(t, metrics)
	for _, sample := range []string{
		"odyssey_online_players 12",
		"odyssey_active_rooms 3",
		"odyssey_match_queue_players 2",
		"odyssey_matches_total 1",
		"odyssey_match_duration_seconds_count 1",
		"odyssey_room_tick_work_duration_seconds_count 1",
		`odyssey_reconnect_attempts_total{result="success"} 1`,
	} {
		if !strings.Contains(body, sample) {
			t.Errorf("scrape does not contain %q", sample)
		}
	}
}

func TestMetricsRejectInvalidObservations(t *testing.T) {
	t.Parallel()

	metrics := New()
	valid := Snapshot{OnlinePlayers: 4, ActiveRooms: 1, MatchQueuePlayers: 2}
	if err := metrics.SetSnapshot(valid); err != nil {
		t.Fatal(err)
	}

	invalidSnapshots := []Snapshot{
		{OnlinePlayers: -1},
		{ActiveRooms: -1},
		{MatchQueuePlayers: -1},
	}
	for _, snapshot := range invalidSnapshots {
		if err := metrics.SetSnapshot(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
			t.Errorf("SetSnapshot(%+v) error = %v, want %v", snapshot, err, ErrInvalidSnapshot)
		}
	}
	if err := metrics.ObserveMatch(-time.Nanosecond); !errors.Is(err, ErrInvalidMatchDuration) {
		t.Errorf("negative duration error = %v, want %v", err, ErrInvalidMatchDuration)
	}
	if err := metrics.ObserveReconnect("token-from-client"); !errors.Is(err, ErrInvalidReconnectResult) {
		t.Errorf("unbounded reconnect result error = %v, want %v", err, ErrInvalidReconnectResult)
	}

	body := scrape(t, metrics)
	for _, sample := range []string{
		"odyssey_online_players 4",
		"odyssey_active_rooms 1",
		"odyssey_match_queue_players 2",
		"odyssey_matches_total 0",
	} {
		if !strings.Contains(body, sample) {
			t.Errorf("scrape after rejected observation does not contain %q", sample)
		}
	}
}

func TestMetricsAllowConcurrentUpdatesAndScrapes(t *testing.T) {
	t.Parallel()

	metrics := New()
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func(value int) {
			defer workers.Done()
			if err := metrics.SetSnapshot(Snapshot{
				OnlinePlayers:     value,
				ActiveRooms:       value / 4,
				MatchQueuePlayers: value % 4,
			}); err != nil {
				t.Errorf("SetSnapshot() error = %v", err)
			}
			if err := metrics.ObserveMatch(time.Duration(value) * time.Millisecond); err != nil {
				t.Errorf("ObserveMatch() error = %v", err)
			}
			if err := metrics.ObserveReconnect(ReconnectSucceeded); err != nil {
				t.Errorf("ObserveReconnect() error = %v", err)
			}
			if _, err := scrapeMetrics(metrics); err != nil {
				t.Errorf("concurrent scrape error = %v", err)
			}
		}(i)
	}
	workers.Wait()

	body := scrape(t, metrics)
	if !strings.Contains(body, "odyssey_matches_total 20") {
		t.Errorf("concurrent scrape does not contain final match count")
	}
	if !strings.Contains(body, `odyssey_reconnect_attempts_total{result="success"} 20`) {
		t.Errorf("concurrent scrape does not contain final reconnect count")
	}
}

func scrape(t *testing.T, metrics *Metrics) string {
	t.Helper()
	body, err := scrapeMetrics(metrics)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func scrapeMetrics(metrics *Metrics) (string, error) {
	request := httptest.NewRequest("GET", "http://metrics.local/metrics", nil)
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		return "", fmt.Errorf("metrics status = %d, want 200", response.Code)
	}
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
