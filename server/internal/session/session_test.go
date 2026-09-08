package session

import (
	"testing"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
)

func TestInitialStateIsConnected(t *testing.T) {
	s := New()
	if s.State() != StateConnected {
		t.Fatalf("initial state = %v, want connected", s.State())
	}
}

func TestLoginOnlyInConnected(t *testing.T) {
	s := New()
	// LoginRequest legal in CONNECTED.
	if ok, _ := s.Accept(protocol.MessageType_MSG_LOGIN_REQUEST); !ok {
		t.Fatal("LoginRequest should be accepted in connected state")
	}
	// PlayerInput illegal in CONNECTED (T04).
	if ok, reason := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); ok {
		t.Fatal("PlayerInput should be rejected in connected state")
	} else if reason != protocol.ReasonCode_REASON_INVALID_STATE {
		t.Fatalf("reason = %v, want REASON_INVALID_STATE", reason)
	}
}

func TestInputRejectedBeforeInRoom(t *testing.T) {
	// Lobby: login ok, but input still illegal.
	s := New()
	s.AssignIdentity(1, 100)
	if !s.Transition(StateLobby) {
		t.Fatal("transition connected->lobby failed")
	}
	if ok, _ := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); ok {
		t.Fatal("PlayerInput should be rejected in lobby")
	}

	// Matching: still illegal.
	if !s.Transition(StateMatching) {
		t.Fatal("transition lobby->matching failed")
	}
	if ok, _ := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); ok {
		t.Fatal("PlayerInput should be rejected in matching")
	}

	// InRoom: legal now.
	if !s.Transition(StateInRoom) {
		t.Fatal("transition matching->in_room failed")
	}
	if ok, _ := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); !ok {
		t.Fatal("PlayerInput should be accepted in in_room")
	}
}

func TestPingAlwaysAcceptedUntilDisconnect(t *testing.T) {
	s := New()
	for _, st := range []State{StateConnected, StateLobby, StateMatching, StateInRoom, StateReward} {
		// drive state forward via allowed transitions
		switch st {
		case StateLobby:
			s.Transition(StateLobby)
		case StateMatching:
			s.Transition(StateLobby)
			s.Transition(StateMatching)
		case StateInRoom:
			s.Transition(StateLobby)
			s.Transition(StateMatching)
			s.Transition(StateInRoom)
		case StateReward:
			s.Transition(StateLobby)
			s.Transition(StateMatching)
			s.Transition(StateInRoom)
			s.Transition(StateReward)
		}
		if ok, _ := s.Accept(protocol.MessageType_MSG_PING); !ok {
			t.Errorf("Ping should be accepted in %v", st)
		}
	}
}

func TestDisconnectedRejectsEverything(t *testing.T) {
	s := New()
	if !s.Transition(StateDisconnected) {
		t.Fatal("transition connected->disconnected failed")
	}
	if ok, _ := s.Accept(protocol.MessageType_MSG_PING); ok {
		t.Fatal("Ping should be rejected in disconnected")
	}
	if ok, _ := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); ok {
		t.Fatal("PlayerInput should be rejected in disconnected")
	}
}

func TestClosedIsTerminal(t *testing.T) {
	s := New()
	s.AssignIdentity(1, 100)
	if !s.Transition(StateLobby) {
		t.Fatal("connected->lobby failed")
	}
	if !s.Transition(StateClosed) {
		t.Fatal("lobby->closed failed")
	}
	// No transition out of closed.
	if s.Transition(StateInRoom) {
		t.Fatal("closed->in_room should be impossible")
	}
	if ok, reason := s.Accept(protocol.MessageType_MSG_PING); ok {
		t.Fatal("Ping should be rejected in closed")
	} else if reason != protocol.ReasonCode_REASON_INVALID_STATE {
		t.Fatalf("reason = %v", reason)
	}
}

func TestIllegalTransitions(t *testing.T) {
	s := New()
	if s.Transition(StateMatching) {
		t.Fatal("connected->matching should be illegal (need lobby first)")
	}
	if s.Transition(StateInRoom) {
		t.Fatal("connected->in_room should be illegal")
	}
	if s.Transition(StateReward) {
		t.Fatal("connected->reward should be illegal")
	}
}

func TestMatchCancelBackToLobby(t *testing.T) {
	s := New()
	s.Transition(StateLobby)
	s.Transition(StateMatching)
	if !s.Transition(StateLobby) {
		t.Fatal("matching->lobby (match cancel) should be legal")
	}
	if s.State() != StateLobby {
		t.Fatalf("state = %v, want lobby", s.State())
	}
}

func TestIdentityAssignment(t *testing.T) {
	s := New()
	s.AssignIdentity(42, 7)
	sid, pid := s.Identity()
	if sid != 42 || pid != 7 {
		t.Fatalf("identity = (%d,%d), want (42,7)", sid, pid)
	}
}
