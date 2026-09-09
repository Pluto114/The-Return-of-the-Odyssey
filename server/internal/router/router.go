// Package router bridges the session state machine (Role A) and the room world
// (Role B). It owns the Join/Leave orchestration: it submits the trusted
// session<->player binding to the room, waits for the room's single-shot
// receipt, and only then mutates session state.
//
// It contains no room-registry logic (which session belongs to which room) —
// that is Role D's matchmaking concern. It also performs no network I/O; the
// caller (cmd/gameserver) runs Join/Leave off the connection Reader goroutine
// because they block until the room tick applies the command.
//
// Contract (see B's CURRENT-COLLABORATION.md):
//   - Join: only a nil receipt result means the player has joined; a nil
//     admission error merely means the command was queued.
//   - Leave: idempotent; admission returning room.ErrQueueFull must be retried
//     by the caller (it must not silently discard a disconnect).
package router

import (
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/session"
)

// Join binds a session into a room and transitions the session to InRoom only
// after the room reports a successful (nil) join receipt. roomID is supplied
// by the caller (the match registry) because Room does not expose its ID.
//
// It blocks until the room tick applies the join command and yields the
// receipt, so it must not run on the connection Reader goroutine.
//
// On admission failure (queue full / room closed) or a non-nil receipt, the
// session state and room binding are left untouched and the error is returned.
func Join(sess *session.Session, rm *room.Room, roomID uint64) error {
	sessionID, playerID := sess.Identity()

	receipt, err := rm.Join(room.SessionID(sessionID), entity.ID(playerID))
	if err != nil {
		// Admission rejected synchronously: queue full, room closed, or an
		// invalid ID. No state change; the caller decides retry/teardown.
		return err
	}

	// The receipt yields exactly one value and closes. nil means joined.
	if err := <-receipt; err != nil {
		// Joined rejected by the room (e.g. session already bound to another
		// player, world full, stage no longer Waiting). No state change.
		return err
	}

	// Joined: bind the room and move to InRoom.
	sess.BindRoom(roomID)
	sess.Transition(session.StateInRoom)
	return nil
}

// Leave unbinds a session from its room. It is idempotent: leaving a session
// that was never joined is not an error at the room layer. It clears the
// session's room binding regardless of the room's receipt result, because the
// session must never retain a stale room reference after a leave attempt.
//
// It blocks until the room tick applies the leave command, so it must not run
// on the connection Reader goroutine.
//
// The session state transition after a leave is intentionally left to the
// caller: a voluntary leave returns to Lobby, a TCP loss goes to Disconnected.
// This package only tears down the room binding.
func Leave(sess *session.Session, rm *room.Room) error {
	sessionID, _ := sess.Identity()

	receipt, err := rm.Leave(room.SessionID(sessionID))
	sess.BindRoom(0)
	if err != nil {
		// Admission rejected (queue full / closed). The binding is cleared;
		// the caller must retry the leave until accepted or Done closes.
		return err
	}
	// Consume the single receipt. Even a non-nil result (e.g. ErrClosed) must
	// not resurrect the binding, so we clear before returning.
	if err := <-receipt; err != nil {
		return err
	}
	return nil
}
