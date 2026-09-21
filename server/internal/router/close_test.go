package router

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"google.golang.org/protobuf/proto"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
)

func TestCloseReasonCode(t *testing.T) {
	cases := []struct {
		reason string
		want   protocol.ReasonCode
	}{
		{"requested", protocol.ReasonCode_REASON_ROOM_CLOSED},
		{"idle", protocol.ReasonCode_REASON_ROOM_IDLE},
		{"event_backpressure", protocol.ReasonCode_REASON_EVENT_BACKPRESSURE},
		{"", protocol.ReasonCode_REASON_ROOM_CLOSED},      // 未知原因映射为通用关闭
		{"bogus", protocol.ReasonCode_REASON_ROOM_CLOSED}, // 未知原因映射为通用关闭
	}
	for _, c := range cases {
		if got := CloseReasonCode(c.reason); got != c.want {
			t.Errorf("CloseReasonCode(%q) = %v, want %v", c.reason, got, c.want)
		}
	}
}

func TestDisconnectFrame(t *testing.T) {
	frame, err := DisconnectFrame(protocol.ReasonCode_REASON_EVENT_BACKPRESSURE, "event_backpressure")
	if err != nil {
		t.Fatalf("DisconnectFrame() error = %v", err)
	}
	hdr, body, err := network.ReadFrame(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if hdr.MessageType != uint16(protocol.MessageType_MSG_DISCONNECT) {
		t.Errorf("MessageType = %d, want MSG_DISCONNECT", hdr.MessageType)
	}
	var d protocol.Disconnect
	if err := proto.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal Disconnect: %v", err)
	}
	if d.Reason != protocol.ReasonCode_REASON_EVENT_BACKPRESSURE {
		t.Errorf("Reason = %v, want REASON_EVENT_BACKPRESSURE", d.Reason)
	}
	if d.Message != "event_backpressure" {
		t.Errorf("Message = %q, want event_backpressure", d.Message)
	}
}

func TestCloseWatcherNotifiesOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, err := room.Start(context.Background(), 7, room.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		w := NewCloseWatcher()
		sink := &eventRecordingSink{}
		w.Subscribe(101, sink)

		var (
			mu          sync.Mutex
			gotRoomID   room.ID
			gotReason   string
			callbackHit bool
		)
		w.OnClose(func(roomID room.ID, reason string) {
			mu.Lock()
			defer mu.Unlock()
			gotRoomID, gotReason, callbackHit = roomID, reason, true
		})

		done := make(chan struct{})
		go func() {
			w.Run(r)
			close(done)
		}()

		// 关闭房间，应以 requested 原因停机。
		r.Close()
		<-done

		// 订阅者应收到 Disconnect 帧。
		if len(sink.frames) != 1 {
			t.Fatalf("frames = %d, want 1", len(sink.frames))
		}
		hdr, body, err := network.ReadFrame(bufio.NewReader(bytes.NewReader(sink.frames[0])))
		if err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		if hdr.MessageType != uint16(protocol.MessageType_MSG_DISCONNECT) {
			t.Errorf("MessageType = %d, want MSG_DISCONNECT", hdr.MessageType)
		}
		var d protocol.Disconnect
		if err := proto.Unmarshal(body, &d); err != nil {
			t.Fatalf("unmarshal Disconnect: %v", err)
		}
		if d.Reason != protocol.ReasonCode_REASON_ROOM_CLOSED {
			t.Errorf("Reason = %v, want REASON_ROOM_CLOSED (requested)", d.Reason)
		}

		// 回调应携带 roomID 与原因触发。
		mu.Lock()
		if !callbackHit {
			t.Error("onClose callback not fired")
		}
		if gotRoomID != 7 || gotReason != "requested" {
			t.Errorf("callback = (%v, %q), want (7, requested)", gotRoomID, gotReason)
		}
		mu.Unlock()
	})
}

func TestCloseWatcherNoSinkStillFiresCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, err := room.Start(context.Background(), 8, room.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		w := NewCloseWatcher()
		var fired bool
		w.OnClose(func(room.ID, string) { fired = true })

		done := make(chan struct{})
		go func() { w.Run(r); close(done) }()
		r.Close()
		<-done

		if !fired {
			t.Error("onClose callback not fired with zero sinks")
		}
	})
}

// TestCloseWatcherSaturationLogsCorrelation 验证：玩家可靠队列拒绝最终 Disconnect 时，
// 观察器记录所属房间和受影响玩家，而不是静默丢弃。
func TestCloseWatcherSaturationLogsCorrelation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, err := room.Start(context.Background(), 9, room.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		var buf bytes.Buffer
		w := NewCloseWatcher()
		w.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
		w.Subscribe(101, &eventRecordingSink{}) // 健康连接
		w.Subscribe(102, &rejectingSink{})      // 饱和并返回 false

		done := make(chan struct{})
		go func() { w.Run(r); close(done) }()
		r.Close()
		<-done

		out := buf.String()
		for _, want := range []string{"reliable queue saturated", "room_id=9", "player_id=102"} {
			if !strings.Contains(out, want) {
				t.Errorf("log output missing %q; got:\n%s", want, out)
			}
		}
	})
}
