package metrics

import (
	"errors"
	"fmt"
	"io"
	"math"
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
	if err := metrics.SetCombatSnapshot(CombatSnapshot{ActiveMonsters: 7, ActiveProjectiles: 4}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveDamage(12.5); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveStageResult(StageResultCleared); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveReward(RewardOffered); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveReward(RewardDefaulted); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveDirector(DirectorSample{
		Duration: 50 * time.Microsecond, ClearTimeSeconds: 12, TeamHPPercent: 0.75,
		AverageDPS: 42, DeathCount: 1, DamageTaken: 25, EquipmentPower: 1.2,
		PreviousDifficulty: 1, NewDifficulty: 1.1, Adjustment: 0.1, MonsterCount: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.SetQueueSnapshot(QueueSnapshot{RoomControlDepth: 2, RoomInputDepth: 5, NetworkReliableDepth: 3}); err != nil {
		t.Fatal(err)
	}
	metrics.ObserveQueueDelta(QueueDelta{RoomRejections: 1, RejectedInputs: 2, DroppedSnapshots: 3,
		DroppedTickSamples: 4, NetworkReliableRejected: 5, NetworkSnapshotReplaced: 6})
	if err := metrics.SetResultWriterSnapshot(ResultWriterSnapshot{QueueDepth: 2, InFlight: 1, Persisted: 3,
		Idempotent: 1, Retries: 2, Failed: 1, Rejected: 4}, ResultWriterSnapshot{}); err != nil {
		t.Fatal(err)
	}

	body := scrape(t, metrics)
	for _, sample := range []string{
		"odyssey_online_players 12",
		"odyssey_active_rooms 3",
		"odyssey_match_queue_players 2",
		"odyssey_matches_total 1",
		"odyssey_match_duration_seconds_count 1",
		"odyssey_room_tick_work_duration_seconds_count 1",
		`odyssey_reconnect_attempts_total{result="success"} 1`,
		"odyssey_active_monsters 7",
		"odyssey_active_projectiles 4",
		"odyssey_damage_dealt_total 12.5",
		`odyssey_stage_results_total{result="cleared"} 1`,
		`odyssey_stage_results_total{result="defeated"} 0`,
		`odyssey_rewards_total{result="offered"} 1`,
		`odyssey_rewards_total{result="defaulted"} 1`,
		"odyssey_director_decisions_total 1",
		"odyssey_director_decision_duration_seconds_count 1",
		"odyssey_director_input_clear_time_seconds 12",
		"odyssey_director_input_team_hp_ratio 0.75",
		"odyssey_director_output_difficulty 1.1",
		"odyssey_director_output_monster_count 4",
		"odyssey_room_control_queue_depth 2",
		"odyssey_room_input_queue_depth 5",
		"odyssey_network_reliable_queue_depth 3",
		"odyssey_room_queue_rejections_total 1",
		"odyssey_room_rejected_inputs_total 2",
		"odyssey_room_dropped_snapshots_total 3",
		"odyssey_room_dropped_tick_samples_total 4",
		"odyssey_network_reliable_queue_rejections_total 5",
		"odyssey_network_snapshot_replacements_total 6",
		"odyssey_result_queue_depth 2",
		"odyssey_result_writes_in_flight 1",
		`odyssey_result_writes_total{result="persisted"} 3`,
		`odyssey_result_writes_total{result="retry"} 2`,
		`odyssey_result_writes_total{result="failed"} 1`,
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
	for _, snapshot := range []CombatSnapshot{{ActiveMonsters: -1}, {ActiveProjectiles: -1}} {
		if err := metrics.SetCombatSnapshot(snapshot); !errors.Is(err, ErrInvalidCombatSnapshot) {
			t.Errorf("SetCombatSnapshot(%+v) error = %v, want %v", snapshot, err, ErrInvalidCombatSnapshot)
		}
	}
	for _, amount := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if err := metrics.ObserveDamage(amount); !errors.Is(err, ErrInvalidDamageAmount) {
			t.Errorf("ObserveDamage(%v) error = %v, want %v", amount, err, ErrInvalidDamageAmount)
		}
	}
	if err := metrics.ObserveStageResult("room-id-from-client"); !errors.Is(err, ErrInvalidStageResult) {
		t.Errorf("unbounded stage result error = %v, want %v", err, ErrInvalidStageResult)
	}
	if err := metrics.ObserveReward("equipment-id-from-client"); !errors.Is(err, ErrInvalidRewardResult) {
		t.Errorf("unbounded reward result error = %v, want %v", err, ErrInvalidRewardResult)
	}
	invalidDirector := DirectorSample{Duration: time.Microsecond, ClearTimeSeconds: 1, TeamHPPercent: 0.5,
		EquipmentPower: 1, PreviousDifficulty: 1, NewDifficulty: 1, MonsterCount: 1}
	invalidDirector.TeamHPPercent = math.NaN()
	if err := metrics.ObserveDirector(invalidDirector); !errors.Is(err, ErrInvalidDirectorSample) {
		t.Errorf("invalid director sample error = %v, want %v", err, ErrInvalidDirectorSample)
	}
	if err := metrics.SetQueueSnapshot(QueueSnapshot{RoomInputDepth: -1}); !errors.Is(err, ErrInvalidQueueSnapshot) {
		t.Errorf("invalid queue snapshot error = %v, want %v", err, ErrInvalidQueueSnapshot)
	}

	body := scrape(t, metrics)
	for _, sample := range []string{
		"odyssey_online_players 4",
		"odyssey_active_rooms 1",
		"odyssey_match_queue_players 2",
		"odyssey_matches_total 0",
		"odyssey_active_monsters 0",
		"odyssey_active_projectiles 0",
		"odyssey_damage_dealt_total 0",
		`odyssey_rewards_total{result="invalid"} 0`,
		"odyssey_director_decisions_total 0",
		"odyssey_room_control_queue_depth 0",
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
