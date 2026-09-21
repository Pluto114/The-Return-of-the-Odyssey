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

// EventSink 是单个玩家的可靠发送目标，*network.Connection 通过 Send 实现它。
// 事件与快照不同，不允许丢失；可靠队列满说明连接已饱和，需要执行背压处理。
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

// NewEventDispatcher 创建没有订阅者的事件分发器。
func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{sinks: make(map[entity.ID]EventSink), logger: slog.Default()}
}

// SetLogger 设置记录可靠队列饱和的日志器；默认使用 slog.Default，nil 表示完全静默。
func (d *EventDispatcher) SetLogger(logger *slog.Logger) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logger = logger
}

// Subscribe 注册或替换玩家 sink；传 nil 表示取消注册。
func (d *EventDispatcher) Subscribe(playerID entity.ID, sink EventSink) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sink == nil {
		delete(d.sinks, playerID)
		return
	}
	d.sinks[playerID] = sink
}

// Unsubscribe 在离开或断线时移除玩家 sink。
func (d *EventDispatcher) Unsubscribe(playerID entity.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sinks, playerID)
}

// Subscribers 返回当前订阅玩家数，供指标和健康检查使用。
func (d *EventDispatcher) Subscribers() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.sinks)
}

// Run 持续消费 rm.Events() 到房间关闭，必须单独运行。关卡事件自带权威 stage index，
// 分发不依赖可能滞后的快照；同时记录 roomID，便于关联分发和队列饱和日志。
func (d *EventDispatcher) Run(rm *room.Room) {
	d.mu.Lock()
	d.roomID = rm.Stats().RoomID
	d.mu.Unlock()
	for batch := range rm.Events() {
		d.Dispatch(batch)
	}
}

// Dispatch 向所有订阅者分发一个事件批次；与 Run 分离便于测试。
func (d *EventDispatcher) Dispatch(batch game.EventBatch) {
	for _, e := range batch.Events {
		mt, msg, err := convert.Event(e)
		if err != nil {
			// 未知事件类型属于程序错误而非网络故障。跳过该项但继续消费，避免堵死事件流；
			// 同时记录 room/stage/tick/entity 关联字段以便追踪。
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

// logBadEvent 记录因未知类型、序列化或编码失败而丢弃的事件，并附带关联字段；
// 这些都表示服务端契约错误，不是可恢复网络问题。
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

// broadcast 把已编码帧发给所有订阅者。每个事件只获取一次锁，Send 本身无阻塞，
// 慢 sink 不会间接阻塞 Room Tick。
//
// sink 返回 false 表示可靠队列饱和；closingSink 负责关闭慢连接，分发器负责记录而不
// 静默丢弃，其他订阅者的投递不受影响。
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
