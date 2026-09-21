// Package session implements the per-connection session state machine that
// sits between the network layer (Role A) and the Room business logic (Role B).
//
// Every decoded inbound message must pass through the state machine BEFORE
// being routed to the Room. The legality matrix is frozen in
// docs/protocol/message-routing.md; do not diverge from it.
//
// Resume/Redis token ownership belongs to Role D; this package only consumes
// the ResumeRequest/ResumeResponse messages, it does not manage tokens.
package session

import (
	"sync"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
)

// State mirrors protocol.SessionState but is the in-process domain type.
// We intentionally do NOT reuse the protobuf enum directly as domain state
// (ARCHITECTURE.md §10: protobuf must not carry domain state); we map to it
// only when producing wire messages.
type State uint8

const (
	StateConnected State = iota + 1
	StateLobby
	StateMatching
	StateInRoom
	StateReward
	StateDisconnected
	StateClosed
)

func (s State) String() string {
	switch s {
	case StateConnected:
		return "connected"
	case StateLobby:
		return "lobby"
	case StateMatching:
		return "matching"
	case StateInRoom:
		return "in_room"
	case StateReward:
		return "reward"
	case StateDisconnected:
		return "disconnected"
	case StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// ToProto maps the domain state to the wire enum.
func (s State) ToProto() protocol.SessionState {
	switch s {
	case StateConnected:
		return protocol.SessionState_SESSION_STATE_CONNECTED
	case StateLobby:
		return protocol.SessionState_SESSION_STATE_LOBBY
	case StateMatching:
		return protocol.SessionState_SESSION_STATE_MATCHING
	case StateInRoom:
		return protocol.SessionState_SESSION_STATE_IN_ROOM
	case StateReward:
		return protocol.SessionState_SESSION_STATE_REWARD
	case StateDisconnected:
		return protocol.SessionState_SESSION_STATE_DISCONNECTED
	case StateClosed:
		return protocol.SessionState_SESSION_STATE_CLOSED
	default:
		return protocol.SessionState_SESSION_STATE_UNSPECIFIED
	}
}

// legalityMatrix encodes the message x state table from
// docs/protocol/message-routing.md. A message type present in a state's set is
// legal in that state.
//
// Build note: this is indexed by State; the three system messages (Ping/Pong)
// are legal in every pre-DISCONNECTED state and are handled separately in
// Accept to avoid repeating them in every row.
var legalityMatrix = map[State]map[protocol.MessageType]bool{
	StateConnected: {
		protocol.MessageType_MSG_LOGIN_REQUEST:  true,
		protocol.MessageType_MSG_RESUME_REQUEST: true,
	},
	StateLobby: {
		protocol.MessageType_MSG_MATCH_REQUEST: true,
		protocol.MessageType_MSG_MATCH_CANCEL:  true,
	},
	StateMatching: {
		protocol.MessageType_MSG_MATCH_REQUEST: true, // no-op (idempotent)
		protocol.MessageType_MSG_MATCH_CANCEL:  true,
	},
	StateInRoom: {
		protocol.MessageType_MSG_MATCH_REQUEST:      true, // replay only after defeat or final clear
		protocol.MessageType_MSG_PLAYER_INPUT:       true,
		protocol.MessageType_MSG_NEXT_STAGE_REQUEST: true,
	},
	StateReward: {
		// A client can have one or two 30 Hz inputs already in flight when the
		// authoritative room crosses StageClear -> Reward. The application
		// decodes and drops them; treating that normal hand-off as a protocol
		// violation would disconnect healthy players.
		protocol.MessageType_MSG_PLAYER_INPUT:       true,
		protocol.MessageType_MSG_REWARD_CHOICE:      true,
		protocol.MessageType_MSG_NEXT_STAGE_REQUEST: true,
	},
	StateDisconnected: {},
	StateClosed:       {},
}

// Session 是每条连接的权威状态机。Connection Reader 会推进状态；断线、重赛、奖励编排
// 等其他 goroutine 也可能读写，因此使用 RWMutex。状态机先于业务 handler 校验消息，
// 可阻止“未登录先移动”“战斗中重复匹配”等跨阶段请求进入 Room。
type Session struct {
	mu        sync.RWMutex
	state     State
	sessionID uint64
	playerID  uint64
	roomID    uint64
}

// New returns a Session in the Connected state with no identity.
func New() *Session {
	return &Session{state: StateConnected}
}

// State returns the current state (read-only).
func (s *Session) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Identity returns the assigned session/player IDs (both 0 until login).
func (s *Session) Identity() (sessionID, playerID uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID, s.playerID
}

// RoomID returns the room the session is bound to, or 0 when not in a room.
// The router (not this package) is the sole writer; it stores the raw room.ID
// as uint64 to avoid importing the room package into the state machine.
func (s *Session) RoomID() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.roomID
}

// BindRoom records the room binding. It is called by the router after a
// successful Join receipt (nil result). A non-zero id marks the session as
// in-room; zero clears the binding on leave.
func (s *Session) BindRoom(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roomID = id
}

// Accept validates whether mt is legal in the current state, returning the
// rejection ReasonCode when it is not. It returns true only when the message
// may proceed.
//
// Ping/Pong are legal in every state before DISCONNECTED/CLOSED, matching the
// routing matrix.
func (s *Session) Accept(mt protocol.MessageType) (ok bool, reason protocol.ReasonCode) {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()

	if state == StateClosed {
		return false, protocol.ReasonCode_REASON_INVALID_STATE
	}
	if state == StateDisconnected {
		return false, protocol.ReasonCode_REASON_INVALID_STATE
	}

	if mt == protocol.MessageType_MSG_PING || mt == protocol.MessageType_MSG_PONG {
		return true, protocol.ReasonCode_REASON_OK
	}

	if legal := legalityMatrix[state][mt]; legal {
		return true, protocol.ReasonCode_REASON_OK
	}
	return false, protocol.ReasonCode_REASON_INVALID_STATE
}

// Transition performs an explicit state change. It is intended for lifecycle
// events that originate from outside inbound message validation (login
// success, match found, TCP loss, close). It returns false if the transition
// is not allowed from the current state.
func (s *Session) Transition(to State) bool {
	// “检查是否合法”和“真正赋值”必须放在同一写锁内，否则两个并发状态切换都可能
	// 基于同一个旧状态通过检查，最终得到不可预测的结果。
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.canTransition(s.state, to) {
		return false
	}
	s.state = to
	return true
}

// canTransition encodes the arrow diagram from message-routing.md. CLOSED is
// a one-way terminal state.
func (s *Session) canTransition(from, to State) bool {
	if from == StateClosed {
		return false // terminal
	}
	switch to {
	case StateClosed:
		// any live state may close (disconnect grace expiry, server shutdown).
		return true
	case StateDisconnected:
		// TCP loss from any non-terminal state.
		return from != StateClosed
	case StateLobby:
		// login ok (connected) or match cancelled (matching -> lobby).
		return from == StateConnected || from == StateMatching
	case StateMatching:
		return from == StateLobby
	case StateInRoom:
		return from == StateMatching || from == StateReward || from == StateDisconnected
	case StateReward:
		return from == StateInRoom
	case StateConnected:
		return from == StateDisconnected // rebind before resume completes
	}
	return false
}

// AssignIdentity records the server-issued session/player IDs after a
// successful login. Only valid in the Connected state; the caller transitions
// to Lobby separately.
func (s *Session) AssignIdentity(sessionID, playerID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = sessionID
	s.playerID = playerID
}
