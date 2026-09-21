// Package router 连接 Session 状态机与 Room 世界，负责 Join/Leave 编排：
// 把可信的 session 与 player 绑定提交给房间，等待一次性回执成功后才修改 Session。
//
// 本包不维护“哪个 Session 属于哪个房间”的全局注册表，也不做网络 I/O。
// Join/Leave 会等到房间 Tick 应用命令，因此调用方必须放在 Connection Reader 之外执行。
//
// 关键约定：
//   - Join：只有回执值为 nil 才表示真正加入；入队时没有错误只代表命令已排队；
//   - Leave：操作幂等；若入队返回 room.ErrQueueFull，调用方必须重试，不能丢失断线清理。
package router

import (
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/session"
)

// Join 把 Session 绑定进 Room，只有房间返回成功回执后才切换到 InRoom。
// roomID 由上层匹配注册表提供。
//
// 本函数会阻塞到房间 Tick 应用命令，因此不能在 Connection Reader goroutine 中执行。
//
// 入队失败或回执含错误时，Session 状态和房间绑定保持不变。
func Join(sess *session.Session, rm *room.Room, roomID uint64) error {
	sessionID, playerID := sess.Identity()

	receipt, err := rm.Join(room.SessionID(sessionID), entity.ID(playerID))
	if err != nil {
		// 队列满、房间关闭或 ID 非法会被同步拒绝；调用方决定重试还是清理。
		return err
	}

	// 回执只产生一个值后关闭；nil 才表示已经加入。
	if err := <-receipt; err != nil {
		// 房间可能因 Session 已绑定、世界已满或关卡不再等待而拒绝，状态不变。
		return err
	}

	// 房间确认成功后，再绑定 roomID 并切换到 InRoom。
	sess.BindRoom(roomID)
	sess.Transition(session.StateInRoom)
	return nil
}

// Leave 解除 Session 与 Room 的绑定，操作幂等；从未加入的 Session 离开也不算错误。
// 无论房间回执如何都会清除本地 roomID，避免 Session 保留失效房间引用。
//
// 本函数会等待房间 Tick，因此同样不能在 Connection Reader goroutine 中执行。
//
// 离开后的会话状态由调用方决定：主动离开回 Lobby，TCP 中断则进入 Disconnected；
// 本包只负责解除房间绑定。
func Leave(sess *session.Session, rm *room.Room) error {
	sessionID, _ := sess.Identity()

	receipt, err := rm.Leave(room.SessionID(sessionID))
	sess.BindRoom(0)
	if err != nil {
		// 队列满或房间关闭时仍清除本地绑定；调用方负责重试到成功或房间结束。
		return err
	}
	// 消费一次性回执；即使返回 ErrClosed，也不能恢复已经清除的旧绑定。
	if err := <-receipt; err != nil {
		return err
	}
	return nil
}
