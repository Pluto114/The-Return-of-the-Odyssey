package router

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/session"
)

// startRoom 在 synctest 假时钟下启动房间，并等待所属 goroutine 就绪。
func startRoom(t *testing.T, id room.ID) *room.Room {
	t.Helper()
	r, err := room.Start(context.Background(), id, room.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	return r
}

// newMatchingSession 使用给定 ID 构造已登录且处于 Matching 的 Session，准备加入房间。
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
		// 房间应用控制时发送回执，Stats 在 Tick 末发布，因此等待房间完成本帧。
		synctest.Wait()
		if r.Stats().Players != 1 {
			t.Fatalf("room players = %d, want 1", r.Stats().Players)
		}
	})
}

func TestJoinDoesNotTransitionWhenAdmissionFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startRoom(t, 1)
		// 关闭房间，使 Join 入队返回 room.ErrClosed。
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
		synctest.Wait()
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
		// 已离开 Session 再次离开不能报错。
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

		// 第一个 Session 建立 session 11 -> player 101 绑定。
		s1 := newMatchingSession(11, 101)
		if err := Join(s1, r, 1); err != nil {
			t.Fatalf("Join(s1) error = %v", err)
		}
		// 第二个 Session 使用相同 sessionID 但不同 playerID，必须因已绑定而被拒绝。
		s2 := newMatchingSession(11, 202)
		if err := Join(s2, r, 1); !errors.Is(err, room.ErrSessionBound) {
			t.Fatalf("Join(s2) error = %v, want room.ErrSessionBound", err)
		}
		if got := s2.State(); got != session.StateMatching {
			t.Fatalf("s2 State = %v, want Matching (unchanged)", got)
		}
	})
}
