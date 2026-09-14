package room_test

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

// readinessSnapshot drains the readiness receipt and returns it.
func readinessSnapshot(t *testing.T, r *room.Room) room.ReadinessReceipt {
	t.Helper()
	receipt, err := r.Readiness()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := <-receipt
	if !ok {
		t.Fatal("readiness receipt closed without a value")
	}
	if _, ok := <-receipt; ok {
		t.Fatal("readiness receipt must close after one result")
	}
	return got
}

func TestReadyBarrierRequiresEveryBoundSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		join(t, r, 12, 102)

		if got := readinessSnapshot(t, r); got.Online != 2 || got.Ready != 0 {
			t.Fatalf("initial readiness = %+v, want online=2 ready=0", got)
		}

		receipt, err := r.Ready(11)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		if got := readinessSnapshot(t, r); got.Online != 2 || got.Ready != 1 {
			t.Fatalf("after one ready = %+v, want online=2 ready=1", got)
		}

		// Duplicate Ready must be idempotent: it does not double-count.
		receipt, err = r.Ready(11)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		if got := readinessSnapshot(t, r); got.Ready != 1 {
			t.Fatalf("duplicate ready counted twice: %+v", got)
		}

		receipt, err = r.Ready(12)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		if got := readinessSnapshot(t, r); got.Online != 2 || got.Ready != 2 {
			t.Fatalf("after both ready = %+v, want online=2 ready=2", got)
		}
	})
}

func TestReadyRejectsForeignSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)

		if _, err := r.Ready(0); !errors.Is(err, room.ErrInvalidSession) {
			t.Fatalf("zero session returned %v", err)
		}
		receipt, err := r.Ready(99)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, room.ErrNotJoined)
		if got := readinessSnapshot(t, r); got.Ready != 0 {
			t.Fatalf("foreign session marked ready: %+v", got)
		}
	})
}

func TestReadyBarrierResetsOnNewStage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		join(t, r, 12, 102)

		receipt, err := r.Ready(11)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		if got := readinessSnapshot(t, r); got.Ready != 1 {
			t.Fatalf("pre-stage readiness = %+v", got)
		}

		// Starting a stage resets the barrier; players must ready afresh.
		startReceipt, err := r.StartStage(battlePlan())
		if err != nil {
			t.Fatal(err)
		}
		result(t, startReceipt, nil)
		if got := readinessSnapshot(t, r); got.Ready != 0 {
			t.Fatalf("ready barrier not reset on new stage: %+v", got)
		}
	})
}

func TestLeaveRemovesReadyEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := start(t, 1, room.DefaultConfig())
		defer r.Close()
		join(t, r, 11, 101)
		join(t, r, 12, 102)

		receipt, err := r.Ready(11)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		receipt, err = r.Ready(12)
		if err != nil {
			t.Fatal(err)
		}
		result(t, receipt, nil)
		if got := readinessSnapshot(t, r); got.Online != 2 || got.Ready != 2 {
			t.Fatalf("pre-leave readiness = %+v", got)
		}

		// A player disconnecting during the reward phase must be removed from
		// the barrier so the remaining online players can still advance.
		leaveReceipt, err := r.Leave(11)
		if err != nil {
			t.Fatal(err)
		}
		result(t, leaveReceipt, nil)
		if got := readinessSnapshot(t, r); got.Online != 1 || got.Ready != 1 {
			t.Fatalf("leave did not shrink the barrier: %+v", got)
		}
	})
}

// TestPendingReadinessDrainsOnClose ensures a readiness query that is still in
// the control queue when the room closes is drained by finish() rather than
// deadlocking the owner goroutine.
func TestPendingReadinessDrainsOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := room.DefaultConfig()
		config.ControlsPerTick = 1
		r := start(t, 1, config)
		join(t, r, 11, 101)

		// Occupy the control budget so the readiness query parks in the queue.
		block, err := r.Join(11, 101) // idempotent; consumes one control slot per tick
		if err != nil {
			t.Fatal(err)
		}
		query, err := r.Readiness()
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
		<-r.Done()

		// The blocking join resolves with ErrClosed; the readiness query is
		// drained with an empty (zero) receipt by finish().
		if err := <-block; !errors.Is(err, room.ErrClosed) {
			t.Fatalf("blocking join = %v, want ErrClosed", err)
		}
		got, ok := <-query
		if !ok {
			t.Fatal("readiness query was not drained on close")
		}
		if got.Online != 0 || got.Ready != 0 {
			t.Fatalf("drained readiness = %+v", got)
		}
	})
}
