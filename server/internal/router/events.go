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

// EventDispatcher consumes a room's event stream and fans each batch out to
// every subscribed player. Events are broadcast (there is no per-player self
// view): every recipient gets the same reliable messages.
//
// It is the single consumer of room.Events() (B's contract requires exactly
// one, and a slow or absent consumer drives the room's event_backpressure
// shutdown). It performs no network I/O directly — it encodes each event and
// hands the frame to each player's EventSink.
type EventDispatcher struct {
	mu     sync.RWMutex
	sinks  map[entity.ID]EventSink
	logger *slog.Logger
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
func (d *EventDispatcher) Run(rm *room.Room) {
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
			// Unknown kind is a programming error, not a recoverable network
			// condition; skip it but keep draining (do not wedge the stream).
			continue
		}
		body, err := proto.Marshal(msg)
		if err != nil {
			continue
		}
		frame, err := network.EncodeFrame(network.Header{
			Magic:       network.Magic,
			Version:     network.VersionV1,
			MessageType: mt,
		}, body)
		if err != nil {
			continue
		}
		d.broadcast(frame)
	}
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
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, sink := range d.sinks {
		if !sink.Send(frame) {
			if d.logger != nil {
				d.logger.Warn("reliable queue saturated, event delivery rejected")
			}
		}
	}
}
