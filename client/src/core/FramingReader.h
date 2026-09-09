// Incremental TCP framing: consumes a raw byte stream and emits complete
// Frames. Handles both split frames (a frame arriving across several reads)
// and coalesced frames (several frames inside one read) without any
// assumption about socket read sizes.
#pragma once

#include "core/Frame.h"

#include <cstddef>
#include <cstdint>
#include <functional>
#include <string>
#include <vector>

namespace odyssey::client::core {

enum class FramingError {
    kNone,
    kBadMagic,      // header magic differs from kFrameMagic
    kBadVersion,    // header version differs from kFrameVersion
    kOversizeBody,  // body_length exceeds the configured cap (checked before allocation)
    kHeaderTruncated,  // EOF left a partial header
    kBodyTruncated,    // EOF left a partial body
};

class FramingReader {
public:
    // max_body caps how large a single frame body may be. Values above
    // kMaxFrameBodyBytes are rejected immediately, before buffering/allocating
    // that much memory, matching the "check BodyLength before allocation" rule.
    explicit FramingReader(std::size_t max_body = kMaxFrameBodyBytes);

    // Feeds raw socket bytes. Completed frames are handed to the callback set
    // with SetFrameCallback. Returns true while the stream is healthy; on a
    // fatal framing error it records error()/error_message() and returns false
    // (subsequent Appends are ignored until Reset()).
    bool Append(const std::uint8_t* data, std::size_t size);

    // Sets the receiver for each complete, validated frame.
    void SetFrameCallback(std::function<void(Frame&&)> callback);

    // Bytes buffered but not yet part of a complete frame.
    std::size_t BufferedBytes() const;

    // Call when the transport signals an orderly EOF. A leftover partial
    // header/body is reported via error()/error_message() and returns false;
    // a clean stream boundary returns true.
    bool Finish(std::string& error_message);

    // Drops all state, clearing any error; buffered partial bytes are lost.
    void Reset();

    FramingError error() const { return error_; }
    const std::string& error_message() const { return error_message_; }

private:
    enum class Phase { kHeader, kBody, kFailed };

    void Fail(FramingError error, const std::string& message);
    bool Drain();

    std::size_t max_body_;
    Phase phase_ = Phase::kHeader;
    std::vector<std::uint8_t> buffer_;
    FrameHeader pending_header_;
    std::function<void(Frame&&)> on_frame_;
    FramingError error_ = FramingError::kNone;
    std::string error_message_;
};

}  // namespace odyssey::client::core
