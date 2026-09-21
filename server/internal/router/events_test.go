package router

import (
	"bufio"
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
)

// eventRecordingSink 捕获发给玩家的每个可靠事件帧并保持顺序，事件不能丢失。
type eventRecordingSink struct {
	mu     sync.Mutex
	frames [][]byte
}

func (s *eventRecordingSink) Send(frame []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 调用方可能复用 slice，因此这里复制。
	cp := make([]byte, len(frame))
	copy(cp, frame)
	s.frames = append(s.frames, cp)
	return true
}

// rejectingSink 始终报告可靠队列饱和（Send=false），模拟发送队列已满的慢连接。
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

// decodeEvent 读取单帧并返回 MessageType 与原始消息体。
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

	// 两个 sink 按序收到完全相同的 MessageType。
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

	// 先放未知类型再放合法事件；合法事件仍必须到达，错误类型不能堵住流。
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

// TestEventDispatcherSaturationDoesNotAffectOthers 验证：一个订阅者可靠队列饱和时，
// 该 sink 拒绝事件，但其他订阅者仍正常收到；慢连接不能导致健康连接静默丢事件。
func TestEventDispatcherSaturationDoesNotAffectOthers(t *testing.T) {
	d := NewEventDispatcher()
	healthy := &eventRecordingSink{}
	slow := &rejectingSink{}
	d.Subscribe(1, healthy)
	d.Subscribe(2, slow)

	d.Dispatch(game.EventBatch{Events: []game.Event{
		{Kind: game.TeamDefeated, StageIndex: 1, ServerTick: 7},
	}})

	// 健康 sink 恰好收到一次事件。
	if len(healthy.frames) != 1 {
		t.Fatalf("healthy frames = %d, want 1 (saturation of a peer must not drop delivery)", len(healthy.frames))
	}
	mt, _ := decodeEvent(t, healthy.frames[0])
	if mt != uint16(protocol.MessageType_MSG_TEAM_DEFEATED_EVENT) {
		t.Errorf("healthy type = %d, want MSG_TEAM_DEFEATED_EVENT", mt)
	}

	// 饱和 sink 仍被调用 Send 并返回 false，从而触发其断线路径。
	slow.mu.Lock()
	calls := slow.calls
	slow.mu.Unlock()
	if calls != 1 {
		t.Fatalf("saturated sink Send calls = %d, want 1 (rejection must be surfaced, not silently skipped)", calls)
	}
}

// TestEventDispatcherBadEventLogsCorrelation 验证未知事件被丢弃但不堵流，并记录
// room/stage/tick/entity 关联字段以便追踪。
func TestEventDispatcherBadEventLogsCorrelation(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	d := NewEventDispatcher()
	d.SetLogger(logger)
	d.mu.Lock()
	d.roomID = 42
	d.mu.Unlock()

	d.Dispatch(game.EventBatch{Events: []game.Event{
		{Kind: game.EventKind(255), StageIndex: 3, ServerTick: 99, EntityID: 1<<63 | 7, SourceID: 5},
	}})

	out := buf.String()
	for _, want := range []string{"dropped invalid event", "room_id=42", "stage_index=3", "server_tick=99", "kind=255"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q; got:\n%s", want, out)
		}
	}
}

// TestEventDispatcherSaturationLogsCorrelation 验证饱和 sink 日志包含所属房间和受影响玩家，
// 便于追踪且确认慢连接只影响自身。
func TestEventDispatcherSaturationLogsCorrelation(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	d := NewEventDispatcher()
	d.SetLogger(logger)
	d.mu.Lock()
	d.roomID = 7
	d.mu.Unlock()

	d.Subscribe(1, &eventRecordingSink{})
	d.Subscribe(2, &rejectingSink{})
	d.Dispatch(game.EventBatch{Events: []game.Event{{Kind: game.TeamDefeated, StageIndex: 1, ServerTick: 7}}})

	out := buf.String()
	for _, want := range []string{"reliable queue saturated", "room_id=7", "player_id=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q; got:\n%s", want, out)
		}
	}
}
