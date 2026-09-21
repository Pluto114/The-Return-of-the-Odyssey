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

func TestServerEchoPingPong(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	// Handler 收到 Ping 后回复 Pong 并回显 nonce。
	handler := func(c *Connection, h Header, payload []byte) error {
		if h.MessageType != 1 { // 消息类型为 MSG_PING
			return nil
		}
		// 构造消息类型为 2 的 Pong，并回显消息体字节。
		pong, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 2, Sequence: h.Sequence}, payload)
		if err != nil {
			return err
		}
		c.Send(pong)
		return nil
	}

	srv := NewServer(handler, logger)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)

	// 客户端连接并发送 Ping 帧。
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	pingBody := []byte{0x08, 0x01, 0x10, 0x02} // Ping 参数为 client_time_ms=1、nonce=2
	pingFrame, _ := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 1, Sequence: 1}, pingBody)
	if _, err := conn.Write(pingFrame); err != nil {
		t.Fatal(err)
	}

	// 读取 Pong 回复。
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	h, body, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("read pong: %v", err)
	}
	if h.MessageType != 2 {
		t.Fatalf("reply MessageType = %d, want 2 (Pong)", h.MessageType)
	}
	if !bytes.Equal(body, pingBody) {
		t.Fatalf("pong body = % X, want echo of % X", body, pingBody)
	}
}

func TestConnectionCloseOnClientDisconnect(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	handler := func(c *Connection, h Header, payload []byte) error { return nil }

	srv := NewServer(handler, logger)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)

	conn, _ := net.Dial("tcp", ln.Addr().String())
	// 等待连接被接收并加入在线集合。
	time.Sleep(50 * time.Millisecond)
	if srv.ActiveConns() != 1 {
		t.Fatalf("ActiveConns = %d, want 1", srv.ActiveConns())
	}

	// 关闭客户端，服务端最终应将其移出在线集合。
	conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.ActiveConns() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ActiveConns = %d after client close, want 0", srv.ActiveConns())
}

func TestServerStatsAggregateLiveQueuesAndKeepCounters(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	srv := NewServer(func(*Connection, Header, []byte) error { return nil }, logger)
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	connection := srv.newConnection(serverConn)
	srv.track(connection)

	for index := 0; index < cap(connection.out); index++ {
		if !connection.Send([]byte{byte(index)}) {
			t.Fatalf("reliable send %d rejected before capacity", index)
		}
	}
	if connection.Send([]byte("overflow")) {
		t.Fatal("reliable send succeeded after capacity")
	}
	if !connection.SendSnapshot([]byte("old")) || !connection.SendSnapshot([]byte("new")) {
		t.Fatal("latest-wins snapshot send failed")
	}

	stats := srv.Stats()
	if stats.ActiveConnections != 1 || stats.ReliableQueueDepth != 256 || stats.ReliableQueueCapacity != 256 ||
		stats.SnapshotsPending != 1 || stats.ReliableSendRejections != 1 || stats.SnapshotReplacements != 1 {
		t.Fatalf("unexpected network stats: %+v", stats)
	}

	connection.Close()
	stats = srv.Stats()
	if stats.ActiveConnections != 0 || stats.ReliableQueueDepth != 0 ||
		stats.ReliableSendRejections != 1 || stats.SnapshotReplacements != 1 {
		t.Fatalf("closed connection stats lost counters: %+v", stats)
	}
}
