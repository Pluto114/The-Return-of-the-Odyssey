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
	// LoginRequest 在 CONNECTED 合法。
	if ok, _ := s.Accept(protocol.MessageType_MSG_LOGIN_REQUEST); !ok {
		t.Fatal("LoginRequest should be accepted in connected state")
	}
	// PlayerInput 在 CONNECTED 非法。
	if ok, reason := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); ok {
		t.Fatal("PlayerInput should be rejected in connected state")
	} else if reason != protocol.ReasonCode_REASON_INVALID_STATE {
		t.Fatalf("reason = %v, want REASON_INVALID_STATE", reason)
	}
}

func TestInputRejectedBeforeInRoom(t *testing.T) {
	// Lobby：已登录，但输入仍非法。
	s := New()
	s.AssignIdentity(1, 100)
	if !s.Transition(StateLobby) {
		t.Fatal("transition connected->lobby failed")
	}
	if ok, _ := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); ok {
		t.Fatal("PlayerInput should be rejected in lobby")
	}

	// Matching：仍然非法。
	if !s.Transition(StateMatching) {
		t.Fatal("transition lobby->matching failed")
	}
	if ok, _ := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); ok {
		t.Fatal("PlayerInput should be rejected in matching")
	}

	// InRoom：此时合法。
	if !s.Transition(StateInRoom) {
		t.Fatal("transition matching->in_room failed")
	}
	if ok, _ := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); !ok {
		t.Fatal("PlayerInput should be accepted in in_room")
	}
}

func TestMatchRequestAcceptedFromInRoomForServerValidatedRematch(t *testing.T) {
	s := New()
	s.AssignIdentity(1, 100)
	s.Transition(StateLobby)
	s.Transition(StateMatching)
	s.Transition(StateInRoom)
	if ok, reason := s.Accept(protocol.MessageType_MSG_MATCH_REQUEST); !ok {
		t.Fatalf("rematch request rejected in room: %v", reason)
	}
	if s.State() != StateInRoom {
		t.Fatal("rematch request must not change session state before new room join")
	}
}

func TestRewardAcceptsInFlightPlayerInputForSafeApplicationDrop(t *testing.T) {
	s := New()
	s.AssignIdentity(1, 100)
	s.Transition(StateLobby)
	s.Transition(StateMatching)
	s.Transition(StateInRoom)
	s.Transition(StateReward)
	if ok, reason := s.Accept(protocol.MessageType_MSG_PLAYER_INPUT); !ok {
		t.Fatalf("in-flight PlayerInput rejected during reward transition: %v", reason)
	}
}

func TestPingAlwaysAcceptedUntilDisconnect(t *testing.T) {
	s := New()
	for _, st := range []State{StateConnected, StateLobby, StateMatching, StateInRoom, StateReward} {
		// 通过允许的迁移向前推进状态
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
	// Closed 之后不允许任何迁移。
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
