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

// SnapshotSink is the latest-wins delivery target for a single player. The
// network layer's *network.Connection satisfies it (SendSnapshot). The
// dispatcher never blocks on a sink: a slow sink drops stale snapshots.
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

// NewSnapshotDispatcher returns a dispatcher with no subscribers.
func NewSnapshotDispatcher() *SnapshotDispatcher {
	return &SnapshotDispatcher{sinks: make(map[entity.ID]SnapshotSink)}
}

// Subscribe registers (or replaces) the sink for a player. playerID must match
// the entity.ID the room assigned at Join. Passing nil unregisters.
func (d *SnapshotDispatcher) Subscribe(playerID entity.ID, sink SnapshotSink) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sink == nil {
		delete(d.sinks, playerID)
		return
	}
	d.sinks[playerID] = sink
}

// Unsubscribe removes a player's sink (called on leave/disconnect).
func (d *SnapshotDispatcher) Unsubscribe(playerID entity.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sinks, playerID)
}

// Subscribers returns the current player count (for metrics/health).
func (d *SnapshotDispatcher) Subscribers() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.sinks)
}

// Run drains rm.Snapshots() until the channel closes (room closed). It blocks;
// run it in its own goroutine. Each snapshot is fanned out to every subscribed
// player; a player absent from the snapshot (already left) is skipped.
func (d *SnapshotDispatcher) Run(rm *room.Room) {
	for snap := range rm.Snapshots() {
		d.Dispatch(snap)
	}
}

// Dispatch fans a single snapshot out to its subscribers. It is separated from
// Run for testability and for the room-close path (a final Closed snapshot can
// be dispatched explicitly before the channel closes).
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

// deliver encodes a personalized WorldSnapshot for one player and hands the
// wire frame to its sink. Marshal/encode errors are dropped (a malformed local
// snapshot is a server bug, not a recoverable network condition).
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
