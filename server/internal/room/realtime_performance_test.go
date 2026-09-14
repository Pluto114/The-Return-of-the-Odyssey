package room_test

import (
	"context"
	"math"
	"os"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

// This real-time gate is opt-in because it intentionally occupies ten seconds
// and measures the host scheduler. The D9 benchmark script enables it.
func TestRealtimeTickCadenceUnder50RoomCombat(t *testing.T) {
	if os.Getenv("ODYSSEY_D9_REALTIME") != "1" {
		t.Skip("set ODYSSEY_D9_REALTIME=1 to run the D9 scheduler gate")
	}
	const (
		roomCount = 50
		duration  = 10 * time.Second
	)

	rooms := make([]*room.Room, roomCount)
	for i := range rooms {
		r, err := room.Start(context.Background(), room.ID(i+1), room.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		rooms[i] = r
	}
	t.Cleanup(func() {
		for _, r := range rooms {
			if r != nil {
				r.Close()
			}
		}
	})

	type pending struct {
		receipt <-chan error
	}
	joins := make([]pending, 0, roomCount*2)
	for i, r := range rooms {
		for player := range 2 {
			receipt, err := r.Join(room.SessionID(10000+i*2+player), entity.ID(20000+i*2+player))
			if err != nil {
				t.Fatal(err)
			}
			joins = append(joins, pending{receipt: receipt})
		}
	}
	for _, command := range joins {
		awaitRealtimeReceipt(t, command.receipt)
	}
	starts := make([]pending, 0, roomCount)
	for i, r := range rooms {
		receipt, err := r.StartStage(stressPlan(int64(50000 + i)))
		if err != nil {
			t.Fatal(err)
		}
		starts = append(starts, pending{receipt: receipt})
	}
	for _, command := range starts {
		awaitRealtimeReceipt(t, command.receipt)
	}

	type cadence struct {
		first, last time.Time
		count       int
	}
	cadences := make([]cadence, roomCount)
	durations := make([]time.Duration, 0, roomCount*int(duration/game.TickInterval))
	var mu sync.Mutex
	var consumers sync.WaitGroup
	for i, r := range rooms {
		consumers.Go(func() {
			for sample := range r.TickSamples() {
				if sample.RoomID != r.Stats().RoomID {
					t.Errorf("foreign tick sample: room=%d sample=%d", r.Stats().RoomID, sample.RoomID)
				}
				mu.Lock()
				entry := &cadences[i]
				if entry.count == 0 {
					entry.first = sample.StartedAt
				}
				entry.last = sample.StartedAt
				entry.count++
				durations = append(durations, sample.WorkDuration)
				mu.Unlock()
			}
		})
		consumers.Go(func() {
			for range r.Snapshots() {
			}
		})
		consumers.Go(func() {
			for range r.Events() {
			}
		})
		consumers.Go(func() {
			for range r.RewardUpdates() {
			}
		})
	}
	time.Sleep(duration)
	for _, r := range rooms {
		r.Close()
	}
	consumers.Wait()

	minHz, maxHz := math.Inf(1), 0.0
	for i, entry := range cadences {
		if entry.count < 2 || !entry.last.After(entry.first) {
			t.Fatalf("room %d has insufficient samples: %+v", i+1, entry)
		}
		hz := float64(entry.count-1) / entry.last.Sub(entry.first).Seconds()
		minHz, maxHz = math.Min(minHz, hz), math.Max(maxHz, hz)
		if hz < 29 || hz > 31 {
			t.Errorf("room %d tick frequency %.3fHz outside 29-31Hz", i+1, hz)
		}
	}
	if len(durations) == 0 {
		t.Fatal("no tick work samples")
	}
	slices.Sort(durations)
	p99 := durations[int(math.Ceil(float64(len(durations))*0.99))-1]
	if p99 >= game.TickInterval {
		t.Errorf("tick work p99 %s exceeds %s budget", p99, game.TickInterval)
	}
	for i, r := range rooms {
		stats := r.Stats()
		if !stats.Closed || stats.Players != 0 || stats.DroppedTickSamples != 0 {
			t.Errorf("room %d did not cleanly finish: %+v", i+1, stats)
		}
	}
	t.Logf("runtime=%s %s/%s rooms=%d samples=%d min_hz=%.3f max_hz=%.3f tick_work_p99=%s", runtime.Version(), runtime.GOOS, runtime.GOARCH, roomCount, len(durations), minHz, maxHz, p99)
}

func awaitRealtimeReceipt(t *testing.T, receipt <-chan error) {
	t.Helper()
	select {
	case err, ok := <-receipt:
		if !ok || err != nil {
			t.Fatalf("command receipt: err=%v open=%v", err, ok)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for room command")
	}
}

func stressPlan(seed int64) stage.Plan {
	monsters := make([]stage.Spawn, 64)
	for i := range monsters {
		monsters[i] = stage.Spawn{
			Position: entity.Vec2{X: 19 - float64(i%8)*0.01, Y: 19 - float64(i/8)*0.01},
			Stats:    entity.CombatStats{MaxHealth: 10000, AttackCooldownTicks: 30},
			Radius:   0.1,
		}
	}
	return stage.Plan{Index: 1, Seed: seed, DifficultyScore: 1, Monsters: monsters}
}
