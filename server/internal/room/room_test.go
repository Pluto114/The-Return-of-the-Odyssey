package room_test

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

func start(t *testing.T, id room.ID, config room.Config) *room.Room {
	t.Helper()
	r, err := room.Start(context.Background(), id, config)
	if err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	return r
}

func join(t *testing.T, r *room.Room, session room.SessionID, player entity.ID) {
	t.Helper()
	receipt, err := r.Join(session, player)
	if err != nil {
		t.Fatal(err)
	}
	result(t, receipt, nil)
}

func result(t *testing.T, receipt <-chan error, want error) {
	t.Helper()
	got, ok := <-receipt
	if !ok || !errors.Is(got, want) {
		t.Fatalf("receipt = %v, open=%v, want %v", got, ok, want)
	}
	if _, ok := <-receipt; ok {
		t.Fatal("receipt must close after one result")
	}
	synctest.Wait()
}

func advance(ticks int) {
	time.Sleep(time.Duration(ticks) * game.TickInterval)
	synctest.Wait()
}

func TestJoinReceiptsAndSessionBinding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		receipt, err := r.Join(11, 101)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-receipt:
			t.Fatal("Join completed before a tick")
		default:
		}
		if err := r.Input(11, game.Input{Seq: 1}); !errors.Is(err, room.ErrNotJoined) {
			t.Fatalf("input before join applied: %v", err)
		}
		result(t, receipt, nil)
		join(t, r, 11, 101)
		if r.Stats().Players != 1 {
			t.Fatal("duplicate join created another player")
		}
		for _, tc := range []struct {
			session room.SessionID
			player  entity.ID
			want    error
		}{
			{11, 102, room.ErrSessionBound}, {12, 101, game.ErrPlayerExists}, {12, 102, nil}, {13, 103, game.ErrWorldFull},
		} {
			receipt, err := r.Join(tc.session, tc.player)
			if err != nil {
				t.Fatal(err)
			}
			result(t, receipt, tc.want)
		}
		if err := r.Input(99, game.Input{Seq: 1}); !errors.Is(err, room.ErrNotJoined) {
			t.Fatal(err)
		}
	})
}

func TestSnapshotCadenceIsolationAndSlowConsumer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := room.DefaultConfig()
		config.TickSampleCapacity = 1
		r := start(t, 8, config)
		defer r.Close()
		join(t, r, 11, 101) // tick 1
		if err := r.Input(11, game.Input{Seq: 1, Direction: entity.Vec2{X: 1}}); err != nil {
			t.Fatal(err)
		}
		advance(1)
		if r.LatestSnapshot().ServerTick != 0 {
			t.Fatal("snapshot published before third tick")
		}
		advance(1)
		s := <-r.Snapshots()
		if s.RoomID != 8 || s.ServerTick != 3 || len(s.Players) != 1 || s.Players[0].LastProcessedInputSeq != 1 {
			t.Fatalf("snapshot: %+v", s)
		}
		wantX := s.Players[0].Position.X
		s.Players[0].Position.X = -100
		copy := r.LatestSnapshot()
		if copy.Players[0].Position.X != wantX {
			t.Fatal("consumer mutated stored snapshot")
		}
		copy.Players[0].ID = 999
		if r.LatestSnapshot().Players[0].ID != 101 {
			t.Fatal("polling consumer mutated stored snapshot")
		}
		advance(9) // Leave snapshots and metrics unread until tick 12.
		if len(r.Snapshots()) != 1 || len(r.TickSamples()) != 1 {
			t.Fatal("unbounded output queue")
		}
		latest := <-r.Snapshots()
		stats := r.Stats()
		if latest.ServerTick != 12 || stats.ServerTick != 12 || stats.DroppedSnapshots != 2 || stats.DroppedTickSamples != 11 {
			t.Fatalf("slow consumer stalled room: %+v / %+v", latest, stats)
		}
	})
}

func TestQueueLimitsAndPerTickBudgets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := room.DefaultConfig()
		config.ControlCapacity, config.ControlsPerTick = 2, 1
		config.InputCapacity, config.InputsPerTick = 3, 1
		r := start(t, 1, config)
		defer r.Close()
		join(t, r, 11, 101)
		for seq := uint64(1); seq <= 3; seq++ {
			if err := r.Input(11, game.Input{Seq: seq}); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Input(11, game.Input{Seq: 4}); !errors.Is(err, room.ErrQueueFull) {
			t.Fatal(err)
		}
		first, err := r.Join(11, 101)
		if err != nil {
			t.Fatal(err)
		}
		second, err := r.Join(11, 101)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Leave(11); !errors.Is(err, room.ErrQueueFull) {
			t.Fatal(err)
		}
		advance(1)
		result(t, first, nil)
		select {
		case <-second:
			t.Fatal("control budget exceeded")
		default:
		}
		stats := r.Stats()
		if stats.InputQueueDepth != 2 || stats.ControlQueueDepth != 1 || stats.QueueRejections != 2 || stats.LastTick.Inputs != 1 {
			t.Fatalf("limits: %+v", stats)
		}
		result(t, second, nil)
	})
}

func TestInputFloodCannotStarveLeave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		for seq := uint64(1); seq <= 256; seq++ {
			if err := r.Input(11, game.Input{Seq: seq, Direction: entity.Vec2{X: 1}}); err != nil {
				t.Fatal(err)
			}
		}
		receipt, err := r.Leave(11)
		if err != nil {
			t.Fatalf("input queue blocked Leave: %v", err)
		}
		result(t, receipt, nil)
		if r.Stats().Players != 0 || r.Stats().LastTick.ServerTick != 2 {
			t.Fatal("leave did not run on next tick")
		}
		advance(1)
		if len(r.LatestSnapshot().Players) != 0 || r.Stats().RejectedInputs != 256 {
			t.Fatal("queued input affected removed player")
		}
	})
}

func TestOldMembershipInputCannotAffectRejoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		if err := r.Input(11, game.Input{Seq: 900, Direction: entity.Vec2{X: 1}}); err != nil {
			t.Fatal(err)
		}
		leave, err := r.Leave(11)
		if err != nil {
			t.Fatal(err)
		}
		rejoin, err := r.Join(11, 102)
		if err != nil {
			t.Fatal(err)
		}
		result(t, leave, nil)
		result(t, rejoin, nil)
		advance(1)
		p := r.LatestSnapshot().Players[0]
		if p.ID != 102 || p.LastProcessedInputSeq != 0 || p.Position.X != 10 {
			t.Fatalf("old membership leaked: %+v", p)
		}
	})
}

func TestBackloggedInputUsesArrivalTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := room.DefaultConfig()
		config.InputsPerTick = 1
		config.World.InputTimeout = 50 * time.Millisecond
		r := start(t, 1, config)
		defer r.Close()
		join(t, r, 11, 101)
		for seq := uint64(1); seq <= 3; seq++ {
			if err := r.Input(11, game.Input{Seq: seq, Direction: entity.Vec2{X: 1}}); err != nil {
				t.Fatal(err)
			}
		}
		advance(5)
		p := r.LatestSnapshot().Players[0]
		if p.LastProcessedInputSeq != 1 || math.Abs(p.Position.X-(10+5.0/30)) > 1e-6 || r.Stats().RejectedInputs != 2 {
			t.Fatalf("backlog renewed input lifetime: %+v", p)
		}
	})
}

func TestTwoPlayersAndRoomIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		moving := start(t, 1, room.DefaultConfig())
		still := start(t, 2, room.DefaultConfig())
		defer moving.Close()
		defer still.Close()
		join(t, moving, 11, 101)
		join(t, moving, 12, 102)
		join(t, still, 21, 201)
		join(t, still, 22, 202)
		for seq := uint64(1); seq <= 30; seq++ {
			if err := moving.Input(11, game.Input{Seq: seq, Direction: entity.Vec2{X: 1}}); err != nil {
				t.Fatal(err)
			}
			if err := moving.Input(12, game.Input{Seq: seq, Direction: entity.Vec2{Y: -1}}); err != nil {
				t.Fatal(err)
			}
			advance(1)
		}
		// Advance to the next snapshot while preserving zero input.
		for _, session := range []room.SessionID{11, 12} {
			if err := moving.Input(session, game.Input{Seq: 31}); err != nil {
				t.Fatal(err)
			}
		}
		advance(3)
		m, s := moving.LatestSnapshot(), still.LatestSnapshot()
		if len(m.Players) != 2 || len(s.Players) != 2 {
			t.Fatal("missing players")
		}
		if m.Players[0].ID != 101 || m.Players[1].ID != 102 || s.Players[0].ID != 201 || s.Players[1].ID != 202 {
			t.Fatal("cross-room entity leak")
		}
		if math.Abs(m.Players[0].Position.X-15) > 1e-6 || math.Abs(m.Players[1].Position.Y-5) > 1e-6 {
			t.Fatalf("wrong authoritative movement: %+v", m)
		}
		for _, p := range s.Players {
			if p.Position != (entity.Vec2{X: 10, Y: 10}) {
				t.Fatal("other room moved")
			}
		}
		if err := moving.Input(21, game.Input{Seq: 1}); !errors.Is(err, room.ErrNotJoined) {
			t.Fatal("foreign session admitted")
		}
	})
}

func TestRoomLifecycleAndPendingReceipts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for cycle := 1; cycle <= 20; cycle++ {
			r := start(t, room.ID(cycle), room.DefaultConfig())
			join(t, r, 11, 101)
			join(t, r, 12, 102)
			receipt, err := r.Leave(11)
			if err != nil {
				t.Fatal(err)
			}
			result(t, receipt, nil)
			advance(3)
			if p := r.LatestSnapshot().Players; len(p) != 1 || p[0].ID != 102 {
				t.Fatal("disconnected player still present")
			}
			receipt, err = r.Leave(12)
			if err != nil {
				t.Fatal(err)
			}
			result(t, receipt, nil)
			time.Sleep(5 * time.Second)
			synctest.Wait()
			select {
			case <-r.Done():
			default:
				t.Fatal("empty room exceeded close deadline")
			}
			r.Close()
			if !r.Stats().Closed || r.Stats().Players != 0 || !r.LatestSnapshot().Closed || len(r.LatestSnapshot().Players) != 0 {
				t.Fatal("incomplete owner cleanup")
			}
		}
		r := start(t, 99, room.DefaultConfig())
		receipt, err := r.Join(11, 101)
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
		result(t, receipt, room.ErrClosed)
		if _, err := r.Join(11, 101); !errors.Is(err, room.ErrClosed) {
			t.Fatal(err)
		}
		if _, err := r.Leave(11); !errors.Is(err, room.ErrClosed) {
			t.Fatal(err)
		}
		if err := r.Input(11, game.Input{Seq: 1}); !errors.Is(err, room.ErrClosed) {
			t.Fatal(err)
		}
	})
}

func TestNeverJoinedRoomExpiresAndRejoinResetsTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		time.Sleep(5 * time.Second)
		synctest.Wait()
		select {
		case <-r.Done():
		default:
			t.Fatal("unused room leaked")
		}
		r.Close()
		r = start(t, 2, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		receipt, err := r.Leave(11)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		time.Sleep(4 * time.Second)
		join(t, r, 12, 102)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		select {
		case <-r.Done():
			t.Fatal("old idle timer closed occupied room")
		default:
		}
	})
}

func TestParentCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		r, err := room.Start(ctx, 1, room.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		pending, err := r.Join(11, 101)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		<-r.Done()
		result(t, pending, room.ErrClosed)
		if _, err := room.Start(ctx, 2, room.DefaultConfig()); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

// Exercises actual concurrent producers, snapshot mutation by consumers, owner
// ticks and shutdown. synctest advances virtual time; -race checks shared access.
func TestConcurrentCommandsSnapshotsAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		join(t, r, 11, 101)
		join(t, r, 12, 102)
		var workers sync.WaitGroup
		for worker := range 8 {
			workers.Go(func() {
				for seq := uint64(1); seq <= 60; seq++ {
					err := r.Input(room.SessionID(11+worker%2), game.Input{Seq: seq, Direction: entity.Vec2{X: 1}})
					if err != nil && !errors.Is(err, room.ErrClosed) && !errors.Is(err, room.ErrQueueFull) {
						t.Errorf("input: %v", err)
					}
					s := r.LatestSnapshot()
					for i := range s.Players {
						s.Players[i].Position.X = -999
					}
					_ = r.Stats()
					time.Sleep(10 * time.Millisecond)
				}
			})
		}
		workers.Go(func() {
			for range 30 {
				receipt, err := r.Join(11, 101)
				if err != nil {
					if !errors.Is(err, room.ErrClosed) {
						t.Errorf("join: %v", err)
					}
					return
				}
				if err := <-receipt; err != nil && !errors.Is(err, room.ErrClosed) {
					t.Errorf("join receipt: %v", err)
				}
				time.Sleep(time.Millisecond)
			}
		})
		workers.Go(func() {
			for s := range r.Snapshots() {
				for i := range s.Players {
					s.Players[i].Position.X = -999
				}
			}
		})
		workers.Go(func() {
			for range r.TickSamples() {
			}
		})
		time.Sleep(400 * time.Millisecond)
		r.Close()
		workers.Wait()
		if !r.Stats().Closed || r.Stats().Players != 0 {
			t.Fatal("shutdown failed")
		}
	})
}

func TestInvalidRoomArguments(t *testing.T) {
	for _, c := range []room.Config{{}, {World: game.DefaultConfig()}} {
		if _, err := room.Start(context.Background(), 1, c); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	if _, err := room.Start(context.Background(), 0, room.DefaultConfig()); err == nil {
		t.Fatal("zero room ID accepted")
	}
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		if _, err := r.Join(0, 1); !errors.Is(err, room.ErrInvalidSession) {
			t.Fatal(err)
		}
		if _, err := r.Join(1, 0); !errors.Is(err, game.ErrInvalidPlayer) {
			t.Fatal(err)
		}
		if _, err := r.Leave(0); !errors.Is(err, room.ErrInvalidSession) {
			t.Fatal(err)
		}
		if err := r.Input(0, game.Input{Seq: 1}); !errors.Is(err, room.ErrInvalidSession) {
			t.Fatal(err)
		}
		if err := r.Input(1, game.Input{Seq: 1, Direction: entity.Vec2{X: math.NaN()}}); !errors.Is(err, game.ErrInvalidInput) {
			t.Fatal(err)
		}
	})
}
