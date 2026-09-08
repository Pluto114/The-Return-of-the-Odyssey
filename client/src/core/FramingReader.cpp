#include "core/FramingReader.h"

#include <algorithm>
#include <cstring>
#include <utility>

namespace odyssey::client::core {

FramingReader::FramingReader(std::size_t max_body) : max_body_(max_body) {
    if (max_body_ == 0) {
        max_body_ = kMaxFrameBodyBytes;
    }
}

void FramingReader::SetFrameCallback(std::function<void(Frame&&)> callback) {
    on_frame_ = std::move(callback);
}

std::size_t FramingReader::BufferedBytes() const {
    return buffer_.size();
}

void FramingReader::Fail(FramingError error, const std::string& message) {
    error_ = error;
    error_message_ = message;
    phase_ = Phase::kFailed;
    buffer_.clear();
}

void FramingReader::Reset() {
    phase_ = Phase::kHeader;
    buffer_.clear();
    error_ = FramingError::kNone;
    error_message_.clear();
    pending_header_ = FrameHeader{};
}

bool FramingReader::Append(const std::uint8_t* data, std::size_t size) {
    if (phase_ == Phase::kFailed) {
        return false;
    }
    if (data == nullptr || size == 0) {
        return true;
    }
    buffer_.insert(buffer_.end(), data, data + size);
    return Drain();
}

bool FramingReader::Drain() {
    while (phase_ != Phase::kFailed) {
        if (phase_ == Phase::kHeader) {
            if (buffer_.size() < kFrameHeaderSize) {
                return true;  // wait for a full header
            }
            FrameHeader header;
            if (!DecodeHeader(buffer_.data(), kFrameHeaderSize, header)) {
                Fail(FramingError::kBadMagic, "cannot decode 16-byte header");
                return false;
            }
            if (header.magic != kFrameMagic) {
                Fail(FramingError::kBadMagic, "frame magic mismatch");
                return false;
            }
            if (header.version != kFrameVersion) {
                Fail(FramingError::kBadVersion, "frame version mismatch");
                return false;
            }
            if (header.body_length > max_body_) {
                Fail(FramingError::kOversizeBody, "frame body_length exceeds cap before allocation");
                return false;
            }
            pending_header_ = header;
            buffer_.erase(buffer_.begin(), buffer_.begin() + static_cast<std::ptrdiff_t>(kFrameHeaderSize));
            phase_ = Phase::kBody;
        } else {  // Phase::kBody
            const std::size_t need = pending_header_.body_length;
            if (buffer_.size() < need) {
                return true;  // wait for the rest of the body
            }
            Frame frame;
            frame.header = pending_header_;
            if (need > 0) {
                frame.body.assign(buffer_.begin(),
                                  buffer_.begin() + static_cast<std::ptrdiff_t>(need));
            }
            buffer_.erase(buffer_.begin(), buffer_.begin() + static_cast<std::ptrdiff_t>(need));
            phase_ = Phase::kHeader;
            if (on_frame_) {
                on_frame_(std::move(frame));
            }
        }
    }
    return false;
}

bool FramingReader::Finish(std::string& error_message) {
    if (phase_ == Phase::kFailed) {
        error_message = error_message_;
        return false;
    }
    if (phase_ == Phase::kBody) {
        const std::string msg =
            "EOF in the middle of a frame body (" + std::to_string(buffer_.size()) +
            " of " + std::to_string(pending_header_.body_length) + " bytes received)";
        Fail(FramingError::kBodyTruncated, msg);
        error_message = msg;
        return false;
    }
    if (!buffer_.empty()) {
        const std::string msg = "EOF with a partial frame header buffered";
        Fail(FramingError::kHeaderTruncated, msg);
        error_message = msg;
        return false;
    }
    error_message.clear();
    return true;
}

}  // namespace odyssey::client::core
