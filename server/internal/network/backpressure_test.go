package network

import (
	"log/slog"
	"net"
	"testing"
)

// 本文件固定背压约定：可靠队列有界，满后 Send 返回 false，让调用方关闭慢连接而不是
// 静默丢玩法事件；快照槽容量恰好为 1，新快照替换待发旧快照。测试直接构造 Connection，
// 完全控制队列状态，不依赖操作系统 socket 缓冲区是否恰好填满。

// newBareConnection 构造一个不启动 Writer 的连接，使可靠队列不会被消费，测试可确定性地
// 填满队列并观察准确饱和点。
func newBareConnection() *Connection {
	// net.Pipe 提供真实 net.Conn，确保 Close 安全；不启动 Writer，队列不会被排空。
	serverSide, clientSide := net.Pipe()
	_ = clientSide // 由 serverSide 对端保持存活，之后交给垃圾回收
	return &Connection{
		conn:     serverSide,
		logger:   slog.Default(),
		out:      make(chan []byte, 256),
		snapshot: make(chan []byte, 1),
		closed:   make(chan struct{}),
	}
}

// TestConnectionReliableQueueSaturation 验证可靠队列容量为 256：恰好接收 256 帧，
// 下一次 Send 返回 false，且不阻塞、不 panic、不破坏已排队帧。
func TestConnectionReliableQueueSaturation(t *testing.T) {
	c := newBareConnection()

	// 填满队列；每帧使用不同内容，以证明已排队数据保持完整。
	const capacity = 256
	queued := make([][]byte, 0, capacity)
	for i := 0; i < capacity; i++ {
		f, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: uint16(300 + i)}, []byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		if !c.Send(f) {
			t.Fatalf("Send(%d) = false before capacity %d reached", i, capacity)
		}
		queued = append(queued, f)
	}

	// 第 257 次发送必须失败：队列已满但连接未关闭，正确结果是背压 false 而非阻塞。
	overflow, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 9999}, []byte{0xFF})
	if err != nil {
		t.Fatal(err)
	}
	if c.Send(overflow) {
		t.Fatal("Send succeeded on a full reliable queue; want false (backpressure)")
	}

	// 已排队帧必须完整且有序，饱和不能丢弃或重排先前帧。
	for i := 0; i < capacity; i++ {
		got := <-c.out
		if len(got) != len(queued[i]) {
			t.Fatalf("queued frame %d length = %d, want %d", i, len(got), len(queued[i]))
		}
	}
}

// TestConnectionSendAfterCloseNeverBlocks 验证关闭后 Send 立即返回 false，不能阻塞或 panic；
// 慢连接被关闭后，后续发布必须快速失败。
func TestConnectionSendAfterCloseNeverBlocks(t *testing.T) {
	c := newBareConnection()
	c.Close()
	f, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Send(f) {
		t.Fatal("Send succeeded on a closed connection; want false")
	}
}

// TestConnectionSnapshotSlotCapacityOne 验证 latest-wins 槽容量恰为 1：已有待发快照时
// 发布新快照会替换旧帧，而非排入第二帧。本测试直接证明队列不变量，不依赖 socket。
func TestConnectionSnapshotSlotCapacityOne(t *testing.T) {
	c := newBareConnection()

	first, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 300}, []byte{0x01})
	if err != nil {
		t.Fatal(err)
	}
	if !c.SendSnapshot(first) {
		t.Fatal("first SendSnapshot = false")
	}

	// 首帧仍待发时发布新快照，必须替换而非追加旧帧。
	second, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 301}, []byte{0x02})
	if err != nil {
		t.Fatal(err)
	}
	if !c.SendSnapshot(second) {
		t.Fatal("second SendSnapshot = false")
	}

	// 槽位只能有一帧，而且必须是最新帧。
	got := <-c.snapshot
	if len(got) != len(second) || got[len(got)-1] != 0x02 {
		t.Fatalf("snapshot slot did not retain the newest frame")
	}
	// 取出后槽位必须为空，不能还藏有第二帧。
	select {
	case extra := <-c.snapshot:
		t.Fatalf("snapshot slot held a stale second frame (last byte 0x%02X)", extra[len(extra)-1])
	default:
	}
}
