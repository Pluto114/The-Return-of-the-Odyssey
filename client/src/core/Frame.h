// Frame envelope for the Odyssey TCP transport.
//
// Fixed 16-byte big-endian header (see ARCHITECTURE.md section 8):
//   Magic 2B | Version 1B | Flags 1B | MessageType 2B | Reserved 2B |
//   BodyLength 4B | Sequence 4B
//
// The header layout is architectural; concrete MessageType values and payload
// schemas are owned by the protocol owner (A) and land on feature/network.
// This file deliberately knows nothing about protobuf payloads.
#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

namespace odyssey::client::core {

inline constexpr std::size_t kFrameHeaderSize = 16;
inline constexpr std::uint16_t kFrameMagic = 0x4E52;  // 'NR'
inline constexpr std::uint8_t kFrameVersion = 1;
inline constexpr std::uint32_t kMaxFrameBodyBytes = 64 * 1024;  // 64 KiB plan limit

struct FrameHeader {
    std::uint16_t magic = kFrameMagic;
    std::uint8_t version = kFrameVersion;
    std::uint8_t flags = 0;
    std::uint16_t message_type = 0;
    std::uint16_t reserved = 0;
    std::uint32_t body_length = 0;
    std::uint32_t sequence = 0;
};

// Writes the header into the first kFrameHeaderSize bytes of `out`
// (network byte order). `out` must have room for kFrameHeaderSize bytes.
void EncodeHeader(const FrameHeader& header, std::uint8_t* out);

// Reads a header from `data`/`size`; `size` must be >= kFrameHeaderSize.
// Returns false when the buffer is too short.
bool DecodeHeader(const std::uint8_t* data, std::size_t size, FrameHeader& header);

// A complete decoded frame.
struct Frame {
    FrameHeader header;
    std::vector<std::uint8_t> body;
};

// Serializes header + body into one contiguous buffer.
// Throws std::invalid_argument when body_size exceeds kMaxFrameBodyBytes
// (the limit is enforced before any allocation of that size).
std::vector<std::uint8_t> EncodeFrame(const FrameHeader& header,
                                      const std::uint8_t* body,
                                      std::size_t body_size);

}  // namespace odyssey::client::core
