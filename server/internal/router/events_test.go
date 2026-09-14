package router

import (
	"bufio"
	"bytes"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
)

// eventRecordingSink captures every reliable event frame delivered to a
// player, preserving order (events must not be lossy).
type eventRecordingSink struct {
	mu     sync.Mutex
	frames [][]byte
}

func (s *eventRecordingSink) Send(frame []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy: the caller may reuse the slice.
	cp := make([]byte, len(frame))
	copy(cp, frame)
	s.frames = append(s.frames, cp)
	return true
}

// rejectingSink always reports a saturated reliable queue (Send=false),
// simulating a slow connection whose outbound queue is full.
type rejectingSink struct {
	mu    sync.Mutex
	calls int
}

func (s *rejectingSink) Send(frame []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return false
}

// decodeEvent reads a single frame and returns its MessageType + the raw body.
func decodeEvent(t *testing.T, frame []byte) (uint16, []byte) {
	t.Helper()
	hdr, body, err := network.ReadFrame(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	return hdr.MessageType, body
}

func TestEventDispatcherBroadcastsToAll(t *testing.T) {
	d := NewEventDispatcher()
	s1 := &eventRecordingSink{}
	s2 := &eventRecordingSink{}
	d.Subscribe(1, s1)
	d.Subscribe(2, s2)

	d.Dispatch(game.EventBatch{Events: []game.Event{
		{Kind: game.DamageDealt, SourceID: 1, TargetID: 1<<63 | 1, Amount: 10, Health: 20, ServerTick: 5},
		{Kind: game.EntityDied, EntityID: 1<<63 | 1, SourceID: 1, ServerTick: 5},
	}})

	if len(s1.frames) != 2 || len(s2.frames) != 2 {
		t.Fatalf("frames = %d/%d, want 2/2 (events are broadcast)", len(s1.frames), len(s2.frames))
	}

	// Both sinks receive identical MessageTypes in order.
	mt1, _ := decodeEvent(t, s1.frames[0])
	mt2, _ := decodeEvent(t, s1.frames[1])
	if mt1 != uint16(protocol.MessageType_MSG_DAMAGE_EVENT) {
		t.Errorf("first event type = %d, want MSG_DAMAGE_EVENT", mt1)
	}
	if mt2 != uint16(protocol.MessageType_MSG_DEATH_EVENT) {
		t.Errorf("second event type = %d, want MSG_DEATH_EVENT", mt2)
	}

	mtS2, _ := decodeEvent(t, s2.frames[0])
	if mtS2 != mt1 {
		t.Errorf("s2 first event type = %d, want %d (broadcast must match)", mtS2, mt1)
	}
}

func TestEventDispatcherEncodesStageScopedEvents(t *testing.T) {
	d := NewEventDispatcher()
	s := &eventRecordingSink{}
	d.Subscribe(1, s)

	d.Dispatch(game.EventBatch{Events: []game.Event{
		{Kind: game.StageStarted, StageIndex: 2, ServerTick: 1},
	}})

	if len(s.frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(s.frames))
	}
	mt, body := decodeEvent(t, s.frames[0])
	if mt != uint16(protocol.MessageType_MSG_STAGE_STARTED_EVENT) {
		t.Fatalf("type = %d", mt)
	}
	var ev protocol.StageStartedEvent
	if err := proto.Unmarshal(body, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.StageIndex != 2 {
		t.Errorf("stage_index = %d, want 2 (from dispatcher stageIndex)", ev.StageIndex)
	}
}

func TestEventDispatcherSkipsUnknownKind(t *testing.T) {
	d := NewEventDispatcher()
	s := &eventRecordingSink{}
	d.Subscribe(1, s)

	// Unknown kind first, then a valid event: the valid one must still arrive
	// (dispatcher must not wedge the stream on a bad kind).
	d.Dispatch(game.EventBatch{Events: []game.Event{
		{Kind: game.EventKind(255)},
		{Kind: game.TeamDefeated, ServerTick: 9},
	}})

	if len(s.frames) != 1 {
		t.Fatalf("frames = %d, want 1 (unknown kind skipped)", len(s.frames))
	}
	mt, _ := decodeEvent(t, s.frames[0])
	if mt != uint16(protocol.MessageType_MSG_TEAM_DEFEATED_EVENT) {
		t.Errorf("type = %d, want MSG_TEAM_DEFEATED_EVENT", mt)
	}
}

func TestEventDispatcherUnsubscribeStopsDelivery(t *testing.T) {
	d := NewEventDispatcher()
	s := &eventRecordingSink{}
	d.Subscribe(1, s)

	d.Dispatch(game.EventBatch{Events: []game.Event{{Kind: game.TeamDefeated, StageIndex: 1}}})
	if len(s.frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(s.frames))
	}

	d.Unsubscribe(1)
	d.Dispatch(game.EventBatch{Events: []game.Event{{Kind: game.StageCleared, StageIndex: 1}}})
	if len(s.frames) != 1 {
		t.Errorf("frames = %d after unsubscribe, want 1 (no new delivery)", len(s.frames))
	}
	if d.Subscribers() != 0 {
		t.Errorf("Subscribers = %d, want 0", d.Subscribers())
	}
}

// TestEventDispatcherSaturationDoesNotAffectOthers verifies the T10 contract:
// when one subscriber's reliable queue is saturated (Send=false), the event is
// rejected for that sink but still delivered to every other subscriber — a slow
// connection must not cause silent loss for healthy peers, and the dispatcher
// must still fan the event out to them.
func TestEventDispatcherSaturationDoesNotAffectOthers(t *testing.T) {
	d := NewEventDispatcher()
	healthy := &eventRecordingSink{}
	slow := &rejectingSink{}
	d.Subscribe(1, healthy)
	d.Subscribe(2, slow)

	d.Dispatch(game.EventBatch{Events: []game.Event{
		{Kind: game.TeamDefeated, StageIndex: 1, ServerTick: 7},
	}})

	// The healthy sink receives the event exactly once.
	if len(healthy.frames) != 1 {
		t.Fatalf("healthy frames = %d, want 1 (saturation of a peer must not drop delivery)", len(healthy.frames))
	}
	mt, _ := decodeEvent(t, healthy.frames[0])
	if mt != uint16(protocol.MessageType_MSG_TEAM_DEFEATED_EVENT) {
		t.Errorf("healthy type = %d, want MSG_TEAM_DEFEATED_EVENT", mt)
	}

	// The saturated sink was still offered the frame (its Send was invoked and
	// returned false), which is the surface that drives its disconnect.
	slow.mu.Lock()
	calls := slow.calls
	slow.mu.Unlock()
	if calls != 1 {
		t.Fatalf("saturated sink Send calls = %d, want 1 (rejection must be surfaced, not silently skipped)", calls)
	}
}
