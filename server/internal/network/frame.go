// Package network implements the wire-level TCP framing and connection
// lifecycle for the Odyssey game server. This package is owned by Role A
// (Realtime Network & Protocol).
//
// The 16-byte header layout is frozen in ARCHITECTURE.md §8 and
// docs/protocol/frame.md. Do NOT change field order, width, or byte order
// without bumping the protocol Version and updating the cross-language
// golden samples in docs/protocol/frame.md.
package network

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Wire-level constants. Keep in sync with docs/protocol/frame.md.
const (
	// HeaderLen is the fixed 16-byte header size.
	HeaderLen = 16

	// Magic is the fixed 2-byte big-endian magic that filters out traffic
	// from unrelated services.
	Magic uint16 = 0x4E52

	// VersionV1 is the only supported protocol version at launch.
	VersionV1 byte = 1

	// MaxBodyLen is the maximum accepted protobuf payload size in bytes.
	// PHASE1 draft suggests 64 KiB; this value is finalized at the D1
	// alignment meeting. The receiver MUST validate BodyLength before
	// allocating the body buffer.
	MaxBodyLen uint32 = 64 * 1024 // 64 KiB
)

// Sentinel errors returned by ReadFrame. Callers can use errors.Is to branch.
var (
	ErrInvalidMagic     = errors.New("network: invalid magic")
	ErrInvalidVersion   = errors.New("network: invalid version")
	ErrFrameTooLarge    = errors.New("network: frame too large")
	ErrMessageTypeZero  = errors.New("network: message type unspecified")
)

// Header is the decoded 16-byte frame header. All multi-byte fields are
// network byte order (big endian) on the wire and native order in memory.
type Header struct {
	Magic       uint16
	Version     byte
	Flags       byte
	MessageType uint16
	Reserved    uint16
	BodyLength  uint32
	Sequence    uint32
}

// Validate checks the fixed invariants a server must enforce on every
// inbound frame. It does NOT touch BodyLength (that is checked against
// MaxBodyLen separately by ReadFrame so the caller can report a distinct
// reason code).
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

// ReadFrame reads exactly one frame from r, handling the TCP sticky-packet /
// partial-read cases. It blocks until a full header + body are available.
//
// On success it returns the decoded header and a body slice that is owned by
// the caller. On error the connection should be closed; no partial frame is
// ever returned.
func ReadFrame(r *bufio.Reader) (Header, []byte, error) {
	var h Header

	// Read the full 16-byte header up front. bufio.Reader guarantees the
	// returned slice is filled, but io.ReadFull makes the contract explicit
	// and safe against short reads.
	hb := make([]byte, HeaderLen)
	if _, err := io.ReadFull(r, hb); err != nil {
		return h, nil, err // io.EOF on clean close, or a transport error
	}

	h.Magic = binary.BigEndian.Uint16(hb[0:2])
	h.Version = hb[2]
	h.Flags = hb[3]
	h.MessageType = binary.BigEndian.Uint16(hb[4:6])
	h.Reserved = binary.BigEndian.Uint16(hb[6:8])
	h.BodyLength = binary.BigEndian.Uint32(hb[8:12])
	h.Sequence = binary.BigEndian.Uint32(hb[12:16])

	// Validate cheap invariants BEFORE any body allocation. This is the
	// T03 requirement: an illegal frame must not first allocate an
	// oversized buffer.
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

	// BodyLength == 0 is legal (empty Ping). io.ReadFull handles the n==0
	// case by returning immediately.
	body := make([]byte, h.BodyLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return h, nil, err
	}

	return h, body, nil
}

// WriteFrame serializes h and body into a 16-byte header followed by the
// payload, writing it all to w. It writes header and body in one pass so a
// slow/flaky reader cannot observe a header without its body.
//
// The caller is responsible for external synchronization; a connection's
// writer goroutine is the only writer to its socket.
func WriteFrame(w io.Writer, h Header, body []byte) error {
	if len(body) > int(MaxBodyLen) {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrFrameTooLarge, len(body), MaxBodyLen)
	}

	// Header is always written from canonical field values; ignore whatever
	// stale BodyLength the caller may have passed so the wire stays honest.
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

// EncodeFrame is a convenience wrapper that returns the serialized bytes of a
// frame. It is used by tests and by the golden-sample verifier; production
// code should prefer WriteFrame to avoid an extra allocation.
func EncodeFrame(h Header, body []byte) ([]byte, error) {
	var buf bytesBuffer
	if err := WriteFrame(&buf, h, body); err != nil {
		return nil, err
	}
	return buf.b, nil
}

// bytesBuffer is a minimal io.Writer over a byte slice for EncodeFrame.
type bytesBuffer struct {
	b []byte
}

func (bb *bytesBuffer) Write(p []byte) (int, error) {
	bb.b = append(bb.b, p...)
	return len(p), nil
}
