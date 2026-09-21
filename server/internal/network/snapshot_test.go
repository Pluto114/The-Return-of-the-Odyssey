package network

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"net"
	"testing"
	"time"
)

// TestSendSnapshotLatestWins 验证慢消费者最终只看到最新快照：未读旧帧会被替换而非排队。
// 此处可靠队列为空，线路上的最终帧必须是最后发布的快照。
func TestSendSnapshotLatestWins(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	var got *Connection
	ready := make(chan struct{})
	handler := func(c *Connection, h Header, payload []byte) error {
		got = c
		close(ready)
		return nil
	}
	srv := NewServer(handler, logger)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(context.Background(), ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// 驱动一次 handler 以取得服务端 Connection。
	ping, _ := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 1}, []byte{0x00})
	if _, err := conn.Write(ping); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never ran")
	}

	// 以高于 Writer 排空速度发布一批快照，每帧使用不同 MessageType 识别最终保留项。
	for i := 0; i < 20; i++ {
		f, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: uint16(300 + i)}, []byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		if !got.SendSnapshot(f) {
			t.Fatalf("SendSnapshot(%d) = false", i)
		}
	}

	// 对端必须读到最后的 MessageType 319，给 Writer 少量时间刷新。
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	var last uint16
	for {
		h, _, err := ReadFrame(r)
		if err != nil {
			break
		}
		last = h.MessageType
	}
	// 对端经过 ping 回显后看到快照；最终帧必须最新，不能是旧帧。
	if last != 319 {
		t.Fatalf("last snapshot MessageType = %d, want 319 (newest wins)", last)
	}
}
