package network

import (
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

type blockedWriterConn struct {
	readEnd, writing, release        chan struct{}
	readOnce, writeOnce, releaseOnce sync.Once
}

func (c *blockedWriterConn) Read([]byte) (int, error) { <-c.readEnd; return 0, io.EOF }
func (c *blockedWriterConn) Write(p []byte) (int, error) {
	c.writeOnce.Do(func() { close(c.writing) })
	<-c.release
	return len(p), nil
}
func (c *blockedWriterConn) Close() error {
	c.readOnce.Do(func() { close(c.readEnd) })
	c.releaseOnce.Do(func() { close(c.release) })
	return nil
}
func (*blockedWriterConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*blockedWriterConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*blockedWriterConn) SetDeadline(time.Time) error      { return nil }
func (*blockedWriterConn) SetReadDeadline(time.Time) error  { return nil }
func (*blockedWriterConn) SetWriteDeadline(time.Time) error { return nil }

// 复现复制 Writer 与对端 EOF 的竞争：Reader 已关闭发送队列，而 socket Writer 仍在排空当前帧。
func TestSendAfterReaderEOFWithBlockedWriter(t *testing.T) {
	conn := &blockedWriterConn{readEnd: make(chan struct{}), writing: make(chan struct{}), release: make(chan struct{})}
	s := NewServer(func(*Connection, Header, []byte) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c := s.newConnection(conn)
	done := make(chan struct{})
	defer func() {
		c.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("connection goroutine did not exit")
		}
	}()
	c.Send([]byte{1})
	go func() { c.run(); close(done) }()
	select {
	case <-conn.writing:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	conn.readOnce.Do(func() { close(conn.readEnd) })
	select {
	case _, ok := <-c.out:
		if ok {
			t.Fatal("queue unexpectedly contained data")
		}
	case <-time.After(time.Second):
		t.Fatal("EOF did not close queue")
	}
	defer func() {
		if value := recover(); value != nil {
			t.Errorf("Send panicked after peer EOF: %v", value)
		}
	}()
	if c.Send([]byte{2}) {
		t.Error("Send accepted frame after peer EOF")
	}
}
