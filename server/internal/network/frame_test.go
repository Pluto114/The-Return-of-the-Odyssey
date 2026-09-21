package network

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// goldPing 是 docs/protocol/frame.md 样例 1 的标准帧：Ping(client_time_ms=1, nonce=2)。
var goldPing = []byte{
	0x4E, 0x52, // 魔数
	0x01,       // 版本
	0x00,       // 标志位
	0x00, 0x01, // 消息类型为 MSG_PING（1）
	0x00, 0x00, // 保留字段
	0x00, 0x00, 0x00, 0x04, // 消息体长度为 4
	0x00, 0x00, 0x00, 0x01, // 序号为 1
	0x08, 0x01, // 字段 1 的 varint 值为 1
	0x10, 0x02, // 字段 2 的 varint 值为 2
}

func TestGoldenSample(t *testing.T) {
	// 解码标准帧并校验每个帧头字段。
	r := bufio.NewReader(bytes.NewReader(goldPing))
	h, body, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame(golden) = %v", err)
	}
	if h.Magic != Magic {
		t.Errorf("Magic = 0x%04X, want 0x%04X", h.Magic, Magic)
	}
	if h.Version != VersionV1 {
		t.Errorf("Version = %d, want %d", h.Version, VersionV1)
	}
	if h.MessageType != 1 {
		t.Errorf("MessageType = %d, want 1", h.MessageType)
	}
	if h.BodyLength != 4 {
		t.Errorf("BodyLength = %d, want 4", h.BodyLength)
	}
	if h.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", h.Sequence)
	}
	if !bytes.Equal(body, []byte{0x08, 0x01, 0x10, 0x02}) {
		t.Errorf("body = % X, want 08 01 10 02", body)
	}

	// 重新编码并确认与标准样例逐字节一致。
	got, err := EncodeFrame(h, body)
	if err != nil {
		t.Fatalf("EncodeFrame = %v", err)
	}
	if !bytes.Equal(got, goldPing) {
		t.Errorf("re-encoded = % X\nwant golden  = % X", got, goldPing)
	}
}

func TestRoundTrip(t *testing.T) {
	h := Header{
		Magic:       Magic,
		Version:     VersionV1,
		MessageType: 300, // 玩家输入
		Sequence:    7,
	}
	body := []byte{0x0A, 0x02, 0x3D, 0x00} // 任意消息体

	encoded, err := EncodeFrame(h, body)
	if err != nil {
		t.Fatalf("EncodeFrame = %v", err)
	}

	r := bufio.NewReader(bytes.NewReader(encoded))
	h2, body2, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame = %v", err)
	}
	if h2.Magic != h.Magic || h2.Version != h.Version || h2.MessageType != h.MessageType || h2.Sequence != h.Sequence {
		t.Errorf("header mismatch: got %+v want %+v", h2, h)
	}
	if h2.BodyLength != uint32(len(body)) {
		t.Errorf("BodyLength = %d, want %d", h2.BodyLength, len(body))
	}
	if !bytes.Equal(body2, body) {
		t.Errorf("body = % X, want % X", body2, body)
	}
}

// TestPartialRead 每次只提供一个字节，验证 ReadFrame 能正确累积短读。
func TestPartialRead(t *testing.T) {
	encoded, err := EncodeFrame(Header{Magic: Magic, Version: VersionV1, MessageType: 2, Sequence: 9}, []byte{0x01, 0x02, 0x03})
	if err != nil {
		t.Fatal(err)
	}

	r := bufio.NewReader(&oneByteReader{data: encoded})
	h, body, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame(partial) = %v", err)
	}
	if h.MessageType != 2 || h.Sequence != 9 {
		t.Errorf("header mismatch: %+v", h)
	}
	if !bytes.Equal(body, []byte{0x01, 0x02, 0x03}) {
		t.Errorf("body = % X", body)
	}
}

// TestStickyPackets 提供 100 个拼接帧，验证每帧都按序且只解码一次。
func TestStickyPackets(t *testing.T) {
	const n = 100
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		body := []byte{byte(i)}
		if err := WriteFrame(&buf, Header{Magic: Magic, Version: VersionV1, MessageType: 300, Sequence: uint32(i)}, body); err != nil {
			t.Fatal(err)
		}
	}

	r := bufio.NewReader(&buf)
	for i := 0; i < n; i++ {
		h, body, err := ReadFrame(r)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if h.Sequence != uint32(i) {
			t.Errorf("frame %d: Sequence = %d, want %d", i, h.Sequence, i)
		}
		if len(body) != 1 || body[0] != byte(i) {
			t.Errorf("frame %d: body = % X", i, body)
		}
	}

	// 此时应干净 EOF，不能多出一帧。
	if _, _, err := ReadFrame(r); err != io.EOF {
		t.Errorf("after %d frames, want io.EOF, got %v", n, err)
	}
}

func TestInvalidMagic(t *testing.T) {
	bad := make([]byte, len(goldPing))
	copy(bad, goldPing)
	binary.BigEndian.PutUint16(bad[0:2], 0xBEEF)

	_, _, err := ReadFrame(bufio.NewReader(bytes.NewReader(bad)))
	if !errors.Is(err, ErrInvalidMagic) {
		t.Errorf("err = %v, want ErrInvalidMagic", err)
	}
}

func TestInvalidVersion(t *testing.T) {
	bad := make([]byte, len(goldPing))
	copy(bad, goldPing)
	bad[2] = 99

	_, _, err := ReadFrame(bufio.NewReader(bytes.NewReader(bad)))
	if !errors.Is(err, ErrInvalidVersion) {
		t.Errorf("err = %v, want ErrInvalidVersion", err)
	}
}

func TestMessageTypeZero(t *testing.T) {
	bad := make([]byte, len(goldPing))
	copy(bad, goldPing)
	binary.BigEndian.PutUint16(bad[4:6], 0)

	_, _, err := ReadFrame(bufio.NewReader(bytes.NewReader(bad)))
	if !errors.Is(err, ErrMessageTypeZero) {
		t.Errorf("err = %v, want ErrMessageTypeZero", err)
	}
}

func TestFrameTooLarge(t *testing.T) {
	h := Header{Magic: Magic, Version: VersionV1, MessageType: 1}
	big := make([]byte, MaxBodyLen+1)
	_, err := EncodeFrame(h, big)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("EncodeFrame err = %v, want ErrFrameTooLarge", err)
	}

	// 入站帧声明超大 BodyLength，确认 ReadFrame 在尝试读取对应字节前就拒绝。
	var buf bytes.Buffer
	hb := make([]byte, HeaderLen)
	binary.BigEndian.PutUint16(hb[0:2], Magic)
	hb[2] = VersionV1
	binary.BigEndian.PutUint16(hb[4:6], 1)
	binary.BigEndian.PutUint32(hb[8:12], MaxBodyLen+1)
	buf.Write(hb)

	_, _, err = ReadFrame(bufio.NewReader(&buf))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("ReadFrame err = %v, want ErrFrameTooLarge", err)
	}
}

func TestHalfPacketEOF(t *testing.T) {
	// 帧头后只有半个消息体必须返回 io.EOF，不能 panic 或返回半帧。
	r := bufio.NewReader(bytes.NewReader(goldPing[:HeaderLen+2]))
	_, _, err := ReadFrame(r)
	if err == nil {
		t.Fatal("expected error on half packet, got nil")
	}
}

// TestBufferGrowth 在同一 Reader 上重复读取不同大小帧，确认不会保留旧状态。
func TestBufferGrowth(t *testing.T) {
	var buf bytes.Buffer
	sizes := []int{0, 1, 4, 64, 1024, 65536}
	for i, sz := range sizes {
		body := bytes.Repeat([]byte{0xAB}, sz)
		if err := WriteFrame(&buf, Header{Magic: Magic, Version: VersionV1, MessageType: 310, Sequence: uint32(i)}, body); err != nil {
			t.Fatal(err)
		}
	}

	r := bufio.NewReader(&buf)
	for i, sz := range sizes {
		h, body, err := ReadFrame(r)
		if err != nil {
			t.Fatalf("frame %d (size %d): %v", i, sz, err)
		}
		if h.BodyLength != uint32(sz) {
			t.Errorf("frame %d: BodyLength = %d, want %d", i, h.BodyLength, sz)
		}
		if len(body) != sz {
			t.Errorf("frame %d: body len = %d, want %d", i, len(body), sz)
		}
	}
}

// oneByteReader 每次只放出一个字节，强制覆盖半包读取路径。
type oneByteReader struct {
	data []byte
	pos  int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}
