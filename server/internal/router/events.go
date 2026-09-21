package router

import (
	"log/slog"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/convert"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

// EventSink is the reliable delivery target for a single player. The network
// layer's *network.Connection satisfies it (Send). Unlike snapshots, events
// are not lossy-tolerant: a full reliable queue means the connection is
// already saturated and the room is on its way to event_backpressure.
type EventSink interface {
	Send(frame []byte) bool
}

// EventDispatcher 是 Room 可靠事件 channel 的唯一消费者，把伤害、死亡、关卡切换等
// 离散事件广播给所有玩家。它和快照分发的关键区别是：事件不允许 latest-wins，
// 因为漏掉一次伤害或结算会让客户端状态机失步。
//
// 若可靠队列塞满，Send 返回 false，由 closingSink 关闭慢连接并记录背压；其他玩家
// 仍继续收事件。这样不会为了照顾一个慢客户端而阻塞整个房间，也不会静默丢关键消息。
type EventDispatcher struct {
	mu     sync.RWMutex
	sinks  map[entity.ID]EventSink
	logger *slog.Logger
	roomID room.ID
}

// NewEventDispatcher returns a dispatcher with no subscribers.
func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{sinks: make(map[entity.ID]EventSink), logger: slog.Default()}
}

// SetLogger installs the logger used to report reliable-queue saturation
// (Send returning false). The default is slog.Default(); a nil logger silences
// saturation reporting entirely.
func (d *EventDispatcher) SetLogger(logger *slog.Logger) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logger = logger
}

// Subscribe registers (or replaces) the sink for a player. Passing nil
// unregisters.
func (d *EventDispatcher) Subscribe(playerID entity.ID, sink EventSink) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sink == nil {
		delete(d.sinks, playerID)
		return
	}
	d.sinks[playerID] = sink
}

// Unsubscribe removes a player's sink (called on leave/disconnect).
func (d *EventDispatcher) Unsubscribe(playerID entity.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sinks, playerID)
}

// Subscribers returns the current player count (for metrics/health).
func (d *EventDispatcher) Subscribers() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.sinks)
}

// Run drains rm.Events() until the channel closes (room closed). It blocks;
// run it in its own goroutine. Stage-scoped events carry their authoritative
// stage index, so dispatch does not depend on a potentially older snapshot.
// It records the room identity once so dispatch and saturation logs can be
// correlated to the owning room (D5 observability).
func (d *EventDispatcher) Run(rm *room.Room) {
	d.mu.Lock()
	d.roomID = rm.Stats().RoomID
	d.mu.Unlock()
	for batch := range rm.Events() {
		d.Dispatch(batch)
	}
}

// Dispatch fans a single event batch out to its subscribers. It is separated
// from Run for testability.
func (d *EventDispatcher) Dispatch(batch game.EventBatch) {
	for _, e := range batch.Events {
		mt, msg, err := convert.Event(e)
		if err != nil {
			// An unknown kind is a programming error, not a recoverable network
			// condition. Skip it but keep draining (do not wedge the stream),
			// and surface it with full correlation fields so a bad event is
			// traceable to its room/stage/tick/entity (D5 observability).
			d.logBadEvent(e, err)
			continue
		}
		body, err := proto.Marshal(msg)
		if err != nil {
			d.logBadEvent(e, err)
			continue
		}
		frame, err := network.EncodeFrame(network.Header{
			Magic:       network.Magic,
			Version:     network.VersionV1,
			MessageType: mt,
		}, body)
		if err != nil {
			d.logBadEvent(e, err)
			continue
		}
		d.broadcast(frame)
	}
}

// logBadEvent reports a dropped event (unknown kind, marshal, or encode
// failure) with room/stage/tick/entity correlation. These are never network
// conditions — they indicate a server-side contract violation.
func (d *EventDispatcher) logBadEvent(e game.Event, err error) {
	d.mu.RLock()
	roomID := d.roomID
	logger := d.logger
	d.mu.RUnlock()
	if logger == nil {
		return
	}
	logger.Error("dropped invalid event",
		"room_id", roomID,
		"kind", int(e.Kind),
		"stage_index", e.StageIndex,
		"server_tick", e.ServerTick,
		"entity_id", e.EntityID,
		"source_id", e.SourceID,
		"err", err,
	)
}

// broadcast delivers one already-encoded frame to every subscriber. It takes
// the lock once per event (not per sink) so a slow sink cannot block the room
// tick indirectly; Send itself is non-blocking.
//
// A sink returning false means its reliable queue is saturated. The sink is
// responsible for the disconnect (the closingSink closes the slow connection);
// the dispatcher's job here is to surface the event — not to silently drop it
// — so saturation is observable as an event_backpressure signal. Delivery to
// other subscribers is unaffected.
func (d *EventDispatcher) broadcast(frame []byte) {
	// Send 只做有界 channel 入队，不做真实网络 I/O，因此在读锁内遍历不会被慢 socket
	// 长时间卡住；写 socket 的工作由每条 Connection 自己的 Writer goroutine 完成。
	d.mu.RLock()
	defer d.mu.RUnlock()
	for playerID, sink := range d.sinks {
		if !sink.Send(frame) {
			if d.logger != nil {
				d.logger.Warn("reliable queue saturated, event delivery rejected",
					"room_id", d.roomID, "player_id", playerID)
			}
		}
	}
}
