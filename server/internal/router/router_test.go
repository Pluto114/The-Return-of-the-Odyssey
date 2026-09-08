package router

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/session"
)

// startRoom starts a room under synctest's fake clock and waits for its owner
// goroutine to come up.
func startRoom(t *testing.T, id room.ID) *room.Room {
	t.Helper()
	r, err := room.Start(context.Background(), id, room.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	return r
}

// newMatchingSession builds a logged-in session in the Matching state with the
// given IDs, ready to join a room.
func newMatchingSession(sessionID, playerID uint64) *session.Session {
	s := session.New()
	s.AssignIdentity(sessionID, playerID)
	s.Transition(session.StateLobby)
	s.Transition(session.StateMatching)
	return s
}

func TestJoinTransitionsToInRoomOnNilReceipt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startRoom(t, 1)
		defer r.Close()

		s := newMatchingSession(11, 101)
		if err := Join(s, r, 1); err != nil {
			t.Fatalf("Join() error = %v", err)
		}
		if got := s.State(); got != session.StateInRoom {
			t.Fatalf("State = %v, want InRoom", got)
		}
		if got := s.RoomID(); got != 1 {
			t.Fatalf("RoomID = %d, want 1", got)
		}
		if r.Stats().Players != 1 {
			t.Fatalf("room players = %d, want 1", r.Stats().Players)
		}
	})
}

func TestJoinDoesNotTransitionWhenAdmissionFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startRoom(t, 1)
		// Close the room so Join admission returns room.ErrClosed.
		r.Close()

		s := newMatchingSession(11, 101)
		if err := Join(s, r, 1); !errors.Is(err, room.ErrClosed) {
			t.Fatalf("Join() error = %v, want room.ErrClosed", err)
		}
		if got := s.State(); got != session.StateMatching {
			t.Fatalf("State = %v, want Matching (unchanged)", got)
		}
		if got := s.RoomID(); got != 0 {
			t.Fatalf("RoomID = %d, want 0 (unbound)", got)
		}
	})
}

func TestLeaveClearsBindingAndRemovesPlayer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startRoom(t, 1)
		defer r.Close()

		s := newMatchingSession(11, 101)
		if err := Join(s, r, 1); err != nil {
			t.Fatalf("Join() error = %v", err)
		}

		if err := Leave(s, r); err != nil {
			t.Fatalf("Leave() error = %v", err)
		}
		if got := s.RoomID(); got != 0 {
			t.Fatalf("RoomID = %d, want 0 after leave", got)
		}
		if r.Stats().Players != 0 {
			t.Fatalf("room players = %d, want 0 after leave", r.Stats().Players)
		}
	})
}

func TestLeaveIsIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startRoom(t, 1)
		defer r.Close()

		s := newMatchingSession(11, 101)
		if err := Join(s, r, 1); err != nil {
			t.Fatalf("Join() error = %v", err)
		}
		// Second leave on an already-left session must not error.
		if err := Leave(s, r); err != nil {
			t.Fatalf("first Leave() error = %v", err)
		}
		if err := Leave(s, r); err != nil {
			t.Fatalf("second Leave() error = %v, want nil (idempotent)", err)
		}
		if got := s.RoomID(); got != 0 {
			t.Fatalf("RoomID = %d, want 0", got)
		}
	})
}

func TestJoinDuplicateSessionIsRejectedByRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startRoom(t, 1)
		defer r.Close()

		// First session binds session 11 -> player 101.
		s1 := newMatchingSession(11, 101)
		if err := Join(s1, r, 1); err != nil {
			t.Fatalf("Join(s1) error = %v", err)
		}
		// A second session with the same sessionID but a different playerID
		// must be rejected by the room (session already bound).
		s2 := newMatchingSession(11, 202)
		if err := Join(s2, r, 1); !errors.Is(err, room.ErrSessionBound) {
			t.Fatalf("Join(s2) error = %v, want room.ErrSessionBound", err)
		}
		if got := s2.State(); got != session.StateMatching {
			t.Fatalf("s2 State = %v, want Matching (unchanged)", got)
		}
	})
}
