#include "core/Frame.h"

#include <algorithm>
#include <stdexcept>

namespace odyssey::client::core {

namespace {

std::uint32_t LoadBe32(const std::uint8_t* p) {
    return (static_cast<std::uint32_t>(p[0]) << 24) |
           (static_cast<std::uint32_t>(p[1]) << 16) |
           (static_cast<std::uint32_t>(p[2]) << 8) |
           static_cast<std::uint32_t>(p[3]);
}

void StoreBe32(std::uint8_t* p, std::uint32_t v) {
    p[0] = static_cast<std::uint8_t>((v >> 24) & 0xFF);
    p[1] = static_cast<std::uint8_t>((v >> 16) & 0xFF);
    p[2] = static_cast<std::uint8_t>((v >> 8) & 0xFF);
    p[3] = static_cast<std::uint8_t>(v & 0xFF);
}

}  // namespace

void EncodeHeader(const FrameHeader& header, std::uint8_t* out) {
    if (out == nullptr) {
        throw std::invalid_argument("EncodeHeader: null output buffer");
    }
    out[0] = static_cast<std::uint8_t>((header.magic >> 8) & 0xFF);
    out[1] = static_cast<std::uint8_t>(header.magic & 0xFF);
    out[2] = header.version;
    out[3] = header.flags;
    out[4] = static_cast<std::uint8_t>((header.message_type >> 8) & 0xFF);
    out[5] = static_cast<std::uint8_t>(header.message_type & 0xFF);
    out[6] = static_cast<std::uint8_t>((header.reserved >> 8) & 0xFF);
    out[7] = static_cast<std::uint8_t>(header.reserved & 0xFF);
    StoreBe32(out + 8, header.body_length);
    StoreBe32(out + 12, header.sequence);
}

bool DecodeHeader(const std::uint8_t* data, std::size_t size, FrameHeader& header) {
    if (data == nullptr || size < kFrameHeaderSize) {
        return false;
    }
    header.magic = static_cast<std::uint16_t>((static_cast<std::uint16_t>(data[0]) << 8) | data[1]);
    header.version = data[2];
    header.flags = data[3];
    header.message_type = static_cast<std::uint16_t>((static_cast<std::uint16_t>(data[4]) << 8) | data[5]);
    header.reserved = static_cast<std::uint16_t>((static_cast<std::uint16_t>(data[6]) << 8) | data[7]);
    header.body_length = LoadBe32(data + 8);
    header.sequence = LoadBe32(data + 12);
    return true;
}

std::vector<std::uint8_t> EncodeFrame(const FrameHeader& header,
                                      const std::uint8_t* body,
                                      std::size_t body_size) {
    if (body_size > kMaxFrameBodyBytes) {
        throw std::invalid_argument("EncodeFrame: body exceeds kMaxFrameBodyBytes");
    }
    if (body_size > 0 && body == nullptr) {
        throw std::invalid_argument("EncodeFrame: null body with non-zero size");
    }
    // The wire body_length is derived from body_size so a caller-supplied
    // header can never produce a frame whose declared length diverges from
    // the bytes actually serialized.
    FrameHeader effective = header;
    effective.magic = kFrameMagic;
    effective.version = kFrameVersion;
    effective.body_length = static_cast<std::uint32_t>(body_size);
    std::vector<std::uint8_t> out(kFrameHeaderSize + body_size);
    EncodeHeader(effective, out.data());
    if (body_size > 0) {
        // body_size <= kMaxFrameBodyBytes is guaranteed to fit in uint32.
        std::copy(body, body + body_size, out.begin() + static_cast<std::ptrdiff_t>(kFrameHeaderSize));
    }
    return out;
}

}  // namespace odyssey::client::core
