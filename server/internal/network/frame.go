// Package network 实现奥德赛服务端的 TCP 帧格式与连接生命周期。
//
// 16 字节帧头格式在 ARCHITECTURE.md §8 与 docs/protocol/frame.md 中定义。
// 修改字段顺序、宽度或字节序时，必须提升协议版本并同步更新跨语言标准样例。
package network

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// 以下是线路协议常量，必须与 docs/protocol/frame.md 保持一致。
const (
	// HeaderLen 是固定的 16 字节帧头长度。
	HeaderLen = 16

	// Magic 是 2 字节大端魔数，用于快速过滤发给其他服务的错误流量。
	Magic uint16 = 0x4E52

	// VersionV1 是当前线路帧格式版本。
	VersionV1 byte = 1

	// MaxBodyLen 是允许的 protobuf 消息体最大字节数。接收方必须先校验长度再分配内存，
	// 防止恶意长度字段造成超大内存申请。
	MaxBodyLen uint32 = 64 * 1024 // 64 千二进制字节
)

// ReadFrame 返回的哨兵错误，调用方可通过 errors.Is 分类处理。
var (
	ErrInvalidMagic    = errors.New("network: invalid magic")
	ErrInvalidVersion  = errors.New("network: invalid version")
	ErrFrameTooLarge   = errors.New("network: frame too large")
	ErrMessageTypeZero = errors.New("network: message type unspecified")
)

// Header 是解码后的 16 字节帧头；多字节字段在线路上使用网络字节序（大端），
// 进入内存结构后使用本机整数表示。
type Header struct {
	Magic       uint16
	Version     byte
	Flags       byte
	MessageType uint16
	Reserved    uint16
	BodyLength  uint32
	Sequence    uint32
}

// Validate 检查每个入站帧都必须满足的固定条件。BodyLength 由 ReadFrame 单独与
// MaxBodyLen 比较，以便上层区分“帧头非法”和“消息过大”。
func (h Header) Validate() error {
	if h.Magic != Magic {
		return fmt.Errorf("%w: got 0x%04X want 0x%04X", ErrInvalidMagic, h.Magic, Magic)
	}
	if h.Version != VersionV1 {
		return fmt.Errorf("%w: got %d want %d", ErrInvalidVersion, h.Version, VersionV1)
	}
	if h.MessageType == 0 {
		return ErrMessageTypeZero
	}
	return nil
}

// ReadFrame 从 r 精确读取一个完整帧，能处理 TCP 粘包与半包；在帧头和消息体完整前会阻塞。
//
// 成功时返回解码帧头及由调用方持有的消息体；失败时不返回半帧，连接应被关闭。
func ReadFrame(r *bufio.Reader) (Header, []byte, error) {
	var h Header

	// 先完整读取 16 字节帧头。io.ReadFull 明确保证不会把短读误当成完整帧头。
	hb := make([]byte, HeaderLen)
	if _, err := io.ReadFull(r, hb); err != nil {
		return h, nil, err // 正常关闭时为 io.EOF，否则为传输错误
	}

	h.Magic = binary.BigEndian.Uint16(hb[0:2])
	h.Version = hb[2]
	h.Flags = hb[3]
	h.MessageType = binary.BigEndian.Uint16(hb[4:6])
	h.Reserved = binary.BigEndian.Uint16(hb[6:8])
	h.BodyLength = binary.BigEndian.Uint32(hb[8:12])
	h.Sequence = binary.BigEndian.Uint32(hb[12:16])

	// 在分配消息体前先完成低成本校验，非法帧不能先触发超大缓冲区申请。
	if h.Magic != Magic {
		return h, nil, fmt.Errorf("%w: got 0x%04X", ErrInvalidMagic, h.Magic)
	}
	if h.Version != VersionV1 {
		return h, nil, fmt.Errorf("%w: got %d", ErrInvalidVersion, h.Version)
	}
	if h.BodyLength > MaxBodyLen {
		return h, nil, fmt.Errorf("%w: %d bytes exceeds %d", ErrFrameTooLarge, h.BodyLength, MaxBodyLen)
	}
	if h.MessageType == 0 {
		return h, nil, ErrMessageTypeZero
	}

	// BodyLength == 0 合法，例如空 Ping；io.ReadFull 会立即处理零长度读取。
	body := make([]byte, h.BodyLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return h, nil, err
	}

	return h, body, nil
}

// WriteFrame 把 h 与 body 序列化成“16 字节帧头 + 消息体”并写入 w。
//
// 调用方负责外部同步；每条连接只允许 Writer goroutine 写 socket。
func WriteFrame(w io.Writer, h Header, body []byte) error {
	if len(body) > int(MaxBodyLen) {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrFrameTooLarge, len(body), MaxBodyLen)
	}

	// 始终按实际 body 重算 BodyLength，忽略调用方可能传入的旧值，保证线路内容可信。
	h.BodyLength = uint32(len(body))

	buf := make([]byte, HeaderLen+len(body))
	binary.BigEndian.PutUint16(buf[0:2], h.Magic)
	buf[2] = h.Version
	buf[3] = h.Flags
	binary.BigEndian.PutUint16(buf[4:6], h.MessageType)
	binary.BigEndian.PutUint16(buf[6:8], h.Reserved)
	binary.BigEndian.PutUint32(buf[8:12], h.BodyLength)
	binary.BigEndian.PutUint32(buf[12:16], h.Sequence)
	copy(buf[HeaderLen:], body)

	n, err := w.Write(buf)
	if err != nil {
		return err
	}
	if n != len(buf) {
		return io.ErrShortWrite
	}
	return nil
}

// EncodeFrame 是返回完整帧字节的便捷封装，供测试和发送队列使用。
func EncodeFrame(h Header, body []byte) ([]byte, error) {
	var buf bytesBuffer
	if err := WriteFrame(&buf, h, body); err != nil {
		return nil, err
	}
	return buf.b, nil
}

// bytesBuffer 是 EncodeFrame 使用的最小字节切片写入器。
type bytesBuffer struct {
	b []byte
}

func (bb *bytesBuffer) Write(p []byte) (int, error) {
	bb.b = append(bb.b, p...)
	return len(p), nil
}
