// Package session 实现每条连接独立的会话状态机，位于网络层与 Room 业务层之间。
//
// 每条已解码消息在路由到 Room 前都必须先通过状态机校验。合法性矩阵以
// docs/protocol/message-routing.md 为准，修改时必须保持一致。
//
// 断线恢复使用的 Redis 令牌由持久化层管理；本包只处理恢复相关的会话状态，不管理令牌。
package session

import (
	"sync"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
)

// State 是进程内的领域状态，与 protocol.SessionState 对应，但不直接复用 protobuf 枚举。
// 只有生成网络消息时才转换，避免把传输结构当作服务端内部状态。
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

// ToProto 将进程内状态转换成网络协议枚举。
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

// legalityMatrix 实现“消息类型 × 会话状态”合法性表；某消息出现在对应集合中，
// 才允许在该状态继续处理。
//
// Ping/Pong 在断线前的所有状态都合法，因此在 Accept 中统一处理，避免每行重复配置。
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
		protocol.MessageType_MSG_MATCH_REQUEST: true, // 不执行额外操作，保持幂等
		protocol.MessageType_MSG_MATCH_CANCEL:  true,
	},
	StateInRoom: {
		protocol.MessageType_MSG_MATCH_REQUEST:      true, // 仅团灭或最终通关后允许重赛
		protocol.MessageType_MSG_PLAYER_INPUT:       true,
		protocol.MessageType_MSG_NEXT_STAGE_REQUEST: true,
	},
	StateReward: {
		// 权威房间从 StageClear 切到 Reward 时，网络中可能仍有一两个 30Hz 输入包。
		// 编排层会解码后丢弃；若把正常的在途包视为违规，会错误断开健康玩家。
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

// New 创建处于 Connected 状态、尚未分配身份的 Session。
func New() *Session {
	return &Session{state: StateConnected}
}

// State 返回当前会话状态。
func (s *Session) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Identity 返回服务端分配的 sessionID/playerID；登录前二者均为 0。
func (s *Session) Identity() (sessionID, playerID uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID, s.playerID
}

// RoomID 返回当前绑定的房间，未进房时为 0。绑定只由 router 写入；这里用 uint64
// 保存原始 room.ID，以免状态机反向依赖 room 包。
func (s *Session) RoomID() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.roomID
}

// BindRoom 记录房间绑定。router 只有在 Join 回执成功后才调用；非零表示已进房，
// 传入 0 表示离开并清除绑定。
func (s *Session) BindRoom(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roomID = id
}

// Accept 判断消息 mt 在当前状态是否允许继续；拒绝时同时返回协议原因码。
//
// 按路由矩阵约定，Ping/Pong 在 DISCONNECTED/CLOSED 之前的所有状态都合法。
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

// Transition 执行显式状态切换，用于登录成功、匹配完成、TCP 断开和关闭等生命周期事件。
// 若当前状态不允许切到目标状态则返回 false。
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

// canTransition 实现 message-routing.md 中的状态迁移图；CLOSED 是不可离开的终态。
func (s *Session) canTransition(from, to State) bool {
	if from == StateClosed {
		return false // 终态
	}
	switch to {
	case StateClosed:
		// 任意存活状态都可关闭，例如重连宽限期结束或服务端停机。
		return true
	case StateDisconnected:
		// 任意非终态都可能因 TCP 中断进入断线状态。
		return from != StateClosed
	case StateLobby:
		// 登录成功，或玩家取消匹配时回到大厅。
		return from == StateConnected || from == StateMatching
	case StateMatching:
		return from == StateLobby
	case StateInRoom:
		return from == StateMatching || from == StateReward || from == StateDisconnected
	case StateReward:
		return from == StateInRoom
	case StateConnected:
		return from == StateDisconnected // 恢复完成前先重新绑定
	}
	return false
}

// AssignIdentity 在登录成功后记录服务端分配的 session/player ID；调用方随后另行切到 Lobby。
func (s *Session) AssignIdentity(sessionID, playerID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = sessionID
	s.playerID = playerID
}
