package router

import (
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

// CloseReasonCode maps B's room.CloseReason string to the wire ReasonCode.
// B sets exactly one of "requested", "idle", or "event_backpressure" before
// the room's Done channel closes (see room.run). An unknown/empty value is a
// programming error; it falls back to REASON_ROOM_CLOSED rather than sending
// an UNSPECIFIED reason to clients.
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

// DisconnectFrame builds a reliable Disconnect frame carrying the given
// reason. The frame is ready to hand to an EventSink (reliable delivery); the
// connection teardown itself is the caller's responsibility (CloseAfterFlush),
// because the sink abstraction does not expose the socket lifecycle.
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

// CloseWatcher observes a room's lifecycle and, when the room closes, notifies
// every subscribed player with a Disconnect frame and invokes onClose so the
// caller (D's room registry) can deregister the room.
//
// It is the single observer of rm.Done(). Run blocks until the room closes;
// run it in its own goroutine. It deliberately does NOT treat event_backpressure
// as a normal clear/team-defeat path — it is reported through the same channel
// as any room shutdown, tagged with the reason, so the registry can distinguish
// and alert.
type CloseWatcher struct {
	mu      sync.RWMutex
	sinks   map[entity.ID]EventSink
	onClose func(roomID room.ID, reason string)
}

// NewCloseWatcher returns a watcher with no subscribers and no callback.
func NewCloseWatcher() *CloseWatcher {
	return &CloseWatcher{sinks: make(map[entity.ID]EventSink)}
}

// OnClose registers (or replaces) the room-closed callback. It is invoked once
// when the observed room closes, after the Disconnect frames are fanned out.
// Pass nil to clear it.
func (w *CloseWatcher) OnClose(fn func(roomID room.ID, reason string)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onClose = fn
}

// Subscribe registers (or replaces) a player's Disconnect delivery sink.
// Passing nil unregisters.
func (w *CloseWatcher) Subscribe(playerID entity.ID, sink EventSink) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if sink == nil {
		delete(w.sinks, playerID)
		return
	}
	w.sinks[playerID] = sink
}

// Unsubscribe removes a player's sink.
func (w *CloseWatcher) Unsubscribe(playerID entity.ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.sinks, playerID)
}

// Run blocks until rm.Done() closes, then fans out a Disconnect frame (tagged
// with the room's CloseReason) to every subscriber and fires the callback.
func (w *CloseWatcher) Run(rm *room.Room) {
	<-rm.Done()
	stats := rm.Stats()
	reason := CloseReasonCode(stats.CloseReason)
	frame, err := DisconnectFrame(reason, stats.CloseReason)
	if err != nil {
		// Marshal/encode failure is a local bug; still notify the registry so
		// the room is never leaked.
		w.fireCallback(stats.RoomID, stats.CloseReason)
		return
	}
	w.broadcast(frame)
	w.fireCallback(stats.RoomID, stats.CloseReason)
}

// broadcast delivers the Disconnect frame to every subscriber.
func (w *CloseWatcher) broadcast(frame []byte) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, sink := range w.sinks {
		sink.Send(frame)
	}
}

// fireCallback invokes onClose without holding the lock, so the callback may
// re-enter the watcher (e.g. to clear sinks).
func (w *CloseWatcher) fireCallback(roomID room.ID, reason string) {
	w.mu.RLock()
	fn := w.onClose
	w.mu.RUnlock()
	if fn != nil {
		fn(roomID, reason)
	}
}
