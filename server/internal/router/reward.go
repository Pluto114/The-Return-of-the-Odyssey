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

// RewardDispatcher consumes a room's targeted reward stream and delivers each
// RewardOptions / RewardApplied message to exactly one player. Unlike combat
// events (broadcast) and snapshots (per-player fan-out), reward updates are
// single-recipient: RewardUpdate.PlayerID names the only sink that receives it.
//
// It is the single consumer of room.RewardUpdates() (B's contract requires
// exactly one). It performs no network I/O directly — it encodes each message
// and hands the frame to the named player's EventSink (reliable delivery).
type RewardDispatcher struct {
	mu     sync.RWMutex
	sinks  map[entity.ID]EventSink
	logger *slog.Logger
	roomID room.ID
}

// NewRewardDispatcher returns a dispatcher with no subscribers.
func NewRewardDispatcher() *RewardDispatcher {
	return &RewardDispatcher{sinks: make(map[entity.ID]EventSink), logger: slog.Default()}
}

// SetLogger installs the logger used to report reliable-queue saturation and
// bad reward updates. A nil logger silences reporting.
func (d *RewardDispatcher) SetLogger(logger *slog.Logger) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logger = logger
}

// Subscribe registers (or replaces) the sink for a player. Passing nil
// unregisters.
func (d *RewardDispatcher) Subscribe(playerID entity.ID, sink EventSink) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sink == nil {
		delete(d.sinks, playerID)
		return
	}
	d.sinks[playerID] = sink
}

// Unsubscribe removes a player's sink (called on leave/disconnect).
func (d *RewardDispatcher) Unsubscribe(playerID entity.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sinks, playerID)
}

// Subscribers returns the current player count (for metrics/health).
func (d *RewardDispatcher) Subscribers() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.sinks)
}

// Run drains rm.RewardUpdates() until the channel closes (room closed). It
// blocks; run it in its own goroutine. It records the room identity once so
// saturation and bad-update logs can be correlated (D5/D6 observability).
func (d *RewardDispatcher) Run(rm *room.Room) {
	d.mu.Lock()
	d.roomID = rm.Stats().RoomID
	d.mu.Unlock()
	for batch := range rm.RewardUpdates() {
		d.Dispatch(batch)
	}
}

// Dispatch routes a single reward batch. Each update carries its own
// PlayerID, so every update is delivered to exactly that player's sink.
func (d *RewardDispatcher) Dispatch(batch game.RewardUpdateBatch) {
	for _, u := range batch.Updates {
		mt, msg, err := convert.Reward(u)
		if err != nil {
			d.logBadUpdate(u, err)
			continue
		}
		body, err := proto.Marshal(msg)
		if err != nil {
			d.logBadUpdate(u, err)
			continue
		}
		frame, err := network.EncodeFrame(network.Header{
			Magic:       network.Magic,
			Version:     network.VersionV1,
			MessageType: mt,
		}, body)
		if err != nil {
			d.logBadUpdate(u, err)
			continue
		}
		d.deliver(u.PlayerID, frame)
	}
}

// deliver sends a single already-encoded frame to exactly one player. A sink
// returning false means its reliable queue is saturated; the sink owns the
// disconnect, and the dispatcher surfaces the event with room/player identity
// rather than silently dropping the targeted message.
func (d *RewardDispatcher) deliver(playerID entity.ID, frame []byte) {
	d.mu.RLock()
	sink, ok := d.sinks[playerID]
	roomID := d.roomID
	logger := d.logger
	d.mu.RUnlock()
	if !ok {
		// Player already left mid-reward; the reward was still applied
		// authoritatively, but there is no live sink to notify.
		return
	}
	if !sink.Send(frame) {
		if logger != nil {
			logger.Warn("reliable queue saturated, reward delivery rejected",
				"room_id", roomID, "player_id", playerID)
		}
	}
}

// logBadUpdate reports a dropped reward update (unknown kind, marshal, or
// encode failure) with room/stage/tick/player correlation.
func (d *RewardDispatcher) logBadUpdate(u game.RewardUpdate, err error) {
	d.mu.RLock()
	roomID := d.roomID
	logger := d.logger
	d.mu.RUnlock()
	if logger == nil {
		return
	}
	logger.Error("dropped invalid reward update",
		"room_id", roomID,
		"kind", int(u.Kind),
		"stage_index", u.StageIndex,
		"server_tick", u.ServerTick,
		"player_id", u.PlayerID,
		"err", err,
	)
}
