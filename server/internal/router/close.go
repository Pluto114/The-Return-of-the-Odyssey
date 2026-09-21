package router

import (
	"log/slog"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

// CloseReasonCode 把 room.CloseReason 映射为线路 ReasonCode。Room 在 Done 关闭前只会设置
// requested、idle 或 event_backpressure；未知/空值属于程序错误，统一回退为 ROOM_CLOSED，
// 不向客户端发送 UNSPECIFIED。
func CloseReasonCode(reason string) protocol.ReasonCode {
	switch reason {
	case "requested":
		return protocol.ReasonCode_REASON_ROOM_CLOSED
	case "idle":
		return protocol.ReasonCode_REASON_ROOM_IDLE
	case "event_backpressure":
		return protocol.ReasonCode_REASON_EVENT_BACKPRESSURE
	default:
		return protocol.ReasonCode_REASON_ROOM_CLOSED
	}
}

// DisconnectFrame 构造带原因的可靠 Disconnect 帧，可直接交给 EventSink；连接关闭本身由
// 调用方执行 CloseAfterFlush，因为 sink 抽象不暴露 socket 生命周期。
func DisconnectFrame(reason protocol.ReasonCode, msg string) ([]byte, error) {
	body, err := proto.Marshal(&protocol.Disconnect{Reason: reason, Message: msg})
	if err != nil {
		return nil, err
	}
	return network.EncodeFrame(network.Header{
		Magic:       network.Magic,
		Version:     network.VersionV1,
		MessageType: uint16(protocol.MessageType_MSG_DISCONNECT),
	}, body)
}

// CloseWatcher 观察房间生命周期。房间关闭时向所有订阅玩家发送 Disconnect，并调用
// onClose，让上层房间注册表移除该房间。
//
// 它是 rm.Done() 的唯一观察者，Run 会阻塞到房间结束，必须单独运行。event_backpressure
// 不会被当成正常通关/团灭，而是作为带原因的房间关闭上报，便于注册表区分告警。
type CloseWatcher struct {
	mu      sync.RWMutex
	sinks   map[entity.ID]EventSink
	onClose func(roomID room.ID, reason string)
	logger  *slog.Logger
	roomID  room.ID
}

// NewCloseWatcher 创建没有订阅者和回调的观察器。
func NewCloseWatcher() *CloseWatcher {
	return &CloseWatcher{sinks: make(map[entity.ID]EventSink), logger: slog.Default()}
}

// SetLogger 设置广播 Disconnect 时记录可靠队列饱和的日志器；传 nil 表示不记录。
func (w *CloseWatcher) SetLogger(logger *slog.Logger) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.logger = logger
}

// OnClose 设置或替换房间关闭回调。房间结束并广播 Disconnect 后只调用一次；nil 表示清除。
func (w *CloseWatcher) OnClose(fn func(roomID room.ID, reason string)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onClose = fn
}

// Subscribe 注册或替换玩家的 Disconnect 接收端；传 nil 表示取消。
func (w *CloseWatcher) Subscribe(playerID entity.ID, sink EventSink) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if sink == nil {
		delete(w.sinks, playerID)
		return
	}
	w.sinks[playerID] = sink
}

// Unsubscribe 移除玩家的接收端。
func (w *CloseWatcher) Unsubscribe(playerID entity.ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.sinks, playerID)
}

// Run 阻塞到 rm.Done() 关闭，随后向所有订阅者广播带 CloseReason 的 Disconnect 并触发回调。
func (w *CloseWatcher) Run(rm *room.Room) {
	<-rm.Done()
	stats := rm.Stats()
	w.mu.Lock()
	w.roomID = stats.RoomID
	w.mu.Unlock()
	reason := CloseReasonCode(stats.CloseReason)
	frame, err := DisconnectFrame(reason, stats.CloseReason)
	if err != nil {
		// 序列化/编码失败属于本地程序错误，但仍要通知注册表，避免房间泄漏。
		w.fireCallback(stats.RoomID, stats.CloseReason)
		return
	}
	w.broadcast(frame)
	w.fireCallback(stats.RoomID, stats.CloseReason)
}

// broadcast 向所有订阅者发送 Disconnect。sink 返回 false 表示可靠队列已饱和；
// sink 负责断开连接，观察器负责记录，而不是静默丢弃终止通知。
func (w *CloseWatcher) broadcast(frame []byte) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for playerID, sink := range w.sinks {
		if !sink.Send(frame) {
			if w.logger != nil {
				w.logger.Warn("reliable queue saturated, disconnect delivery rejected",
					"room_id", w.roomID, "player_id", playerID)
			}
		}
	}
}

// fireCallback 在不持锁时调用 onClose，使回调可以安全重入观察器（例如清空 sinks）。
func (w *CloseWatcher) fireCallback(roomID room.ID, reason string) {
	w.mu.RLock()
	fn := w.onClose
	w.mu.RUnlock()
	if fn != nil {
		fn(roomID, reason)
	}
}
