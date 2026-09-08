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

// SnapshotDispatcher consumes a room's snapshot stream and fans out a
// personalized WorldSnapshot to each subscribed player. Each recipient sees
// itself as Self and receives the per-player input acknowledgement; other
// players and monsters are shared.
//
// It is the single consumer of room.Snapshots() (B's contract requires exactly
// one). It performs no network I/O directly — it encodes the wire frame and
// hands it to the player's SnapshotSink, which applies latest-wins delivery.
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
