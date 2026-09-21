package router

import (
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/convert"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

// SnapshotSink 是单玩家 latest-wins 发送目标，*network.Connection 通过 SendSnapshot
// 实现它；分发器不会被 sink 阻塞，慢连接只会丢弃旧快照。
type SnapshotSink interface {
	SendSnapshot(frame []byte) bool
}

// SnapshotDispatcher 是 Room 快照 channel 的唯一消费者，再把同一份权威状态转换为
// 每个玩家自己的 WorldSnapshot：接收者本人放在 Self 中，并带上该玩家的输入确认序号。
//
// dispatcher 本身不直接写 socket，只编码后交给 SnapshotSink。sinks 使用 RWMutex，
// 允许断线/重连 goroutine 修改订阅关系，同时快照分发 goroutine安全读取。最终发送采用
// latest-wins，因此这里即使遇到慢客户端也不会阻塞房间模拟。
type SnapshotDispatcher struct {
	mu    sync.RWMutex
	sinks map[entity.ID]SnapshotSink
}

// NewSnapshotDispatcher 创建没有订阅者的快照分发器。
func NewSnapshotDispatcher() *SnapshotDispatcher {
	return &SnapshotDispatcher{sinks: make(map[entity.ID]SnapshotSink)}
}

// Subscribe 注册或替换玩家 sink；playerID 必须与 Room.Join 分配的 entity.ID 一致，
// 传 nil 表示取消注册。
func (d *SnapshotDispatcher) Subscribe(playerID entity.ID, sink SnapshotSink) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sink == nil {
		delete(d.sinks, playerID)
		return
	}
	d.sinks[playerID] = sink
}

// Unsubscribe 在离开或断线时移除玩家 sink。
func (d *SnapshotDispatcher) Unsubscribe(playerID entity.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sinks, playerID)
}

// Subscribers 返回当前订阅玩家数，供指标和健康检查使用。
func (d *SnapshotDispatcher) Subscribers() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.sinks)
}

// Run 持续消费 rm.Snapshots() 到房间关闭，必须单独运行。每份快照分发给所有订阅玩家；
// 已离开且不在快照中的玩家会被跳过。
func (d *SnapshotDispatcher) Run(rm *room.Room) {
	for snap := range rm.Snapshots() {
		d.Dispatch(snap)
	}
}

// Dispatch 分发单份快照；与 Run 分离便于测试，也允许关闭 channel 前显式发送最终快照。
func (d *SnapshotDispatcher) Dispatch(snap room.Snapshot) {
	// 整次遍历持有读锁，保证某个 sink 不会在“查到后、调用前”被并发替换。
	// SendSnapshot 是无阻塞操作，所以读锁持有时间有明确上界。
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, p := range snap.Players {
		sink, ok := d.sinks[p.ID]
		if !ok {
			continue
		}
		d.deliver(sink, snap.Snapshot, p.ID)
	}
}

// deliver 为单个玩家编码个性化 WorldSnapshot 并交给 sink。序列化/编码失败表示服务端
// 本地快照错误，不属于可恢复网络故障。
func (d *SnapshotDispatcher) deliver(sink SnapshotSink, s game.Snapshot, selfID entity.ID) {
	ws := convert.WorldSnapshot(s, selfID)
	body, err := proto.Marshal(ws)
	if err != nil {
		return
	}
	frame, err := network.EncodeFrame(network.Header{
		Magic:       network.Magic,
		Version:     network.VersionV1,
		MessageType: uint16(protocol.MessageType_MSG_WORLD_SNAPSHOT),
	}, body)
	if err != nil {
		return
	}
	sink.SendSnapshot(frame)
}
