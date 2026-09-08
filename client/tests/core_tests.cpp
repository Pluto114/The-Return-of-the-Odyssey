// Headless unit tests for client core: Frame codec, FramingReader and
// BoundedQueue. No window, no sockets, no protobuf - these tests feed T01/T02
// style evidence (fixed byte samples, split/coalesced streams) locally.
#include "core/BoundedQueue.h"
#include "core/Frame.h"
#include "core/FramingReader.h"

#include <atomic>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <string>
#include <thread>
#include <vector>

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                              \
    do {                                                                         \
        ++g_checks;                                                              \
        if (!(cond)) {                                                           \
            ++g_failures;                                                        \
            std::printf("FAIL %s:%d  %s\n", __FILE__, __LINE__, #cond);          \
        }                                                                        \
    } while (0)

using odyssey::client::core::BoundedQueue;
using odyssey::client::core::DecodeHeader;
using odyssey::client::core::EncodeFrame;
using odyssey::client::core::EncodeHeader;
using odyssey::client::core::Frame;
using odyssey::client::core::FrameHeader;
using odyssey::client::core::FramingError;
using odyssey::client::core::FramingReader;
using odyssey::client::core::QueuePushResult;
using odyssey::client::core::kFrameHeaderSize;
using odyssey::client::core::kFrameMagic;
using odyssey::client::core::kFrameVersion;
using odyssey::client::core::kMaxFrameBodyBytes;

std::vector<std::uint8_t> AsBytes(const std::initializer_list<int>& values) {
    std::vector<std::uint8_t> out;
    out.reserve(values.size());
    for (int v : values) {
        out.push_back(static_cast<std::uint8_t>(v));
    }
    return out;
}

void TestHeaderFixedSample() {
    FrameHeader h;
    h.magic = 0x4E52;
    h.version = 1;
    h.flags = 0;
    h.message_type = 1;
    h.reserved = 0;
    h.body_length = 4;
    h.sequence = 7;

    std::uint8_t raw[kFrameHeaderSize] = {0};
    EncodeHeader(h, raw);
    const auto expected = AsBytes({0x4E, 0x52, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00,
                                   0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x07});
    CHECK(std::memcmp(raw, expected.data(), kFrameHeaderSize) == 0);

    FrameHeader back{};
    CHECK(DecodeHeader(raw, kFrameHeaderSize, back));
    CHECK(back.magic == 0x4E52);
    CHECK(back.version == 1);
    CHECK(back.message_type == 1);
    CHECK(back.body_length == 4);
    CHECK(back.sequence == 7);
}

void TestHeaderRoundTrip() {
    FrameHeader h;
    h.flags = 0xAB;
    h.message_type = 0x1234;
    h.reserved = 0x0055;
    h.body_length = 0x00010000;  // 65536 == cap, fits uint32
    h.sequence = 0xDEADBEEF;
    std::uint8_t raw[kFrameHeaderSize] = {0};
    EncodeHeader(h, raw);

    FrameHeader back{};
    CHECK(DecodeHeader(raw, kFrameHeaderSize, back));
    CHECK(back.flags == 0xAB);
    CHECK(back.message_type == 0x1234);
    CHECK(back.reserved == 0x0055);
    CHECK(back.body_length == 0x00010000);
    CHECK(back.sequence == 0xDEADBEEF);
}

void TestDecodeShortBuffer() {
    std::uint8_t raw[4] = {0};
    FrameHeader h{};
    CHECK(!DecodeHeader(raw, 4, h));
    CHECK(!DecodeHeader(nullptr, kFrameHeaderSize, h));
}

void TestEncodeFrameAssembly() {
    FrameHeader h;
    h.message_type = 300;
    h.sequence = 42;
    h.body_length = 3;
    const std::uint8_t body[] = {0x01, 0x02, 0x03};
    const auto bytes = EncodeFrame(h, body, 3);
    CHECK(bytes.size() == kFrameHeaderSize + 3);
    FrameHeader back{};
    CHECK(DecodeHeader(bytes.data(), kFrameHeaderSize, back));
    CHECK(back.message_type == 300);
    CHECK(back.sequence == 42);
    CHECK(back.body_length == 3);
    CHECK(bytes[kFrameHeaderSize + 0] == 0x01);
    CHECK(bytes[kFrameHeaderSize + 1] == 0x02);
    CHECK(bytes[kFrameHeaderSize + 2] == 0x03);
}

void TestEncodeRejectsOversize() {
    FrameHeader h;
    h.body_length = kMaxFrameBodyBytes + 1;
    bool threw = false;
    try {
        (void)EncodeFrame(h, nullptr, h.body_length);
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    CHECK(threw);
}

void Feed(FramingReader& reader, const std::vector<std::uint8_t>& bytes,
          std::vector<Frame>& out) {
    reader.SetFrameCallback([&out](Frame&& f) { out.push_back(std::move(f)); });
    reader.Append(bytes.data(), bytes.size());
}

FrameHeader MakeHeader(std::uint16_t type, std::uint32_t body_len,
                       std::uint32_t seq = 1) {
    FrameHeader h;
    h.magic = kFrameMagic;
    h.version = kFrameVersion;
    h.message_type = type;
    h.body_length = body_len;
    h.sequence = seq;
    return h;
}

std::vector<std::uint8_t> FrameBytes(std::uint16_t type, std::uint32_t seq,
                                     const std::vector<std::uint8_t>& body = {}) {
    return EncodeFrame(MakeHeader(type, static_cast<std::uint32_t>(body.size()), seq),
                       body.data(), body.size());
}

void TestFramingSingleFrame() {
    FramingReader reader;
    std::vector<Frame> frames;
    Feed(reader, FrameBytes(1, 100, {0xAA, 0xBB}), frames);
    CHECK(frames.size() == 1);
    if (frames.size() == 1) {
        CHECK(frames[0].header.message_type == 1);
        CHECK(frames[0].header.sequence == 100);
        CHECK(frames[0].header.body_length == 2);
        CHECK(frames[0].body.size() == 2);
        CHECK(frames[0].body[0] == 0xAA);
        CHECK(frames[0].body[1] == 0xBB);
    }
    CHECK(reader.BufferedBytes() == 0);
}

void TestFramingCoalesced() {
    // Two complete frames delivered inside a single Append.
    FramingReader reader;
    std::vector<Frame> frames;
    auto a = FrameBytes(1, 10, {0x01});
    auto b = FrameBytes(2, 20, {0x02, 0x03, 0x04});
    std::vector<std::uint8_t> combined;
    combined.insert(combined.end(), a.begin(), a.end());
    combined.insert(combined.end(), b.begin(), b.end());
    Feed(reader, combined, frames);
    CHECK(frames.size() == 2);
    if (frames.size() == 2) {
        CHECK(frames[0].header.message_type == 1);
        CHECK(frames[0].header.sequence == 10);
        CHECK(frames[1].header.message_type == 2);
        CHECK(frames[1].header.sequence == 20);
        CHECK(frames[1].body.size() == 3);
    }
    CHECK(reader.BufferedBytes() == 0);
}

void TestFramingByteByByte() {
    // Every possible split point must decode exactly once and in order.
    const auto frame = FrameBytes(7, 5, {0x11, 0x22, 0x33});
    for (std::size_t split = 0; split < frame.size(); ++split) {
        FramingReader reader;
        std::vector<Frame> frames;
        reader.SetFrameCallback([&frames](Frame&& f) { frames.push_back(std::move(f)); });
        for (std::size_t i = 0; i < frame.size(); ++i) {
            if (i == split && split < frame.size()) {
                // Simulates a mid-stream pause; nothing extra should emit.
                CHECK(frames.size() <= 1);
            }
            reader.Append(&frame[i], 1);
        }
        CHECK(frames.size() == 1);
        if (frames.size() == 1) {
            CHECK(frames[0].header.sequence == 5);
            CHECK(frames[0].body.size() == 3);
            CHECK(frames[0].body[2] == 0x33);
        }
        CHECK(reader.BufferedBytes() == 0);
        CHECK(reader.error() == FramingError::kNone);
    }
}

void TestFramingPartialHeaderThenBody() {
    const auto frame = FrameBytes(3, 9, {0x01});
    // Feed only the first 13 bytes of the 16-byte header.
    FramingReader reader;
    std::vector<Frame> frames;
    reader.SetFrameCallback([&frames](Frame&& f) { frames.push_back(std::move(f)); });
    CHECK(reader.Append(frame.data(), 13));
    CHECK(frames.empty());
    CHECK(reader.BufferedBytes() == 13);
    // Remaining 3 header bytes + 1 body byte arrive together.
    CHECK(reader.Append(frame.data() + 13, frame.size() - 13));
    CHECK(frames.size() == 1);
    CHECK(reader.BufferedBytes() == 0);
}

void TestFramingBadMagicAndVersion() {
    {
        FramingReader reader;
        std::vector<Frame> frames;
        Feed(reader, FrameBytes(1, 1), frames);
        (void)frames;
        CHECK(reader.error() == FramingError::kNone);

        // Corrupt magic: feed header where magic != 0x4E52.
        FramingReader bad;
        std::uint8_t hdr[kFrameHeaderSize] = {0};
        FrameHeader h;
        h.magic = 0x1234;
        EncodeHeader(h, hdr);
        CHECK(!bad.Append(hdr, kFrameHeaderSize));
        CHECK(bad.error() == FramingError::kBadMagic);
        CHECK(!bad.error_message().empty());
        // Failed reader ignores further input until Reset.
        CHECK(!bad.Append(hdr, kFrameHeaderSize));
        bad.Reset();
        CHECK(bad.error() == FramingError::kNone);
    }
    {
        FramingReader bad;
        std::uint8_t hdr[kFrameHeaderSize] = {0};
        FrameHeader h;
        h.magic = kFrameMagic;
        h.version = kFrameVersion + 1;
        EncodeHeader(h, hdr);
        CHECK(!bad.Append(hdr, kFrameHeaderSize));
        CHECK(bad.error() == FramingError::kBadVersion);
    }
}

void TestFramingOversizeRejectedEarly() {
    // body_length above the cap must be rejected from the header alone -
    // before any large allocation is attempted.
    FramingReader reader;
    std::vector<Frame> frames;
    reader.SetFrameCallback([&frames](Frame&&) { frames.push_back(Frame{}); });
    std::uint8_t hdr[kFrameHeaderSize] = {0};
    FrameHeader h;
    h.magic = kFrameMagic;
    h.version = kFrameVersion;
    h.body_length = kMaxFrameBodyBytes + 1;
    EncodeHeader(h, hdr);
    CHECK(!reader.Append(hdr, kFrameHeaderSize));
    CHECK(reader.error() == FramingError::kOversizeBody);
    CHECK(frames.empty());
}

void TestFramingMaxBodyAccepted() {
    // A frame whose body is exactly the cap must still round-trip.
    FramingReader reader;
    std::vector<Frame> frames;
    reader.SetFrameCallback([&frames](Frame&& f) { frames.push_back(std::move(f)); });
    const auto body = FrameBytes(4, 2, std::vector<std::uint8_t>(kMaxFrameBodyBytes, 0x5A));
    CHECK(body.size() == kFrameHeaderSize + kMaxFrameBodyBytes);
    reader.Append(body.data(), body.size());
    CHECK(reader.error() == FramingError::kNone);
    CHECK(frames.size() == 1);
    if (frames.size() == 1) {
        CHECK(frames[0].body.size() == kMaxFrameBodyBytes);
        CHECK(frames[0].body.back() == 0x5A);
    }
}

void TestFinishCleanAndTruncated() {
    {
        FramingReader reader;
        std::string err;
        CHECK(reader.Finish(err));
        CHECK(err.empty());
    }
    {
        FramingReader reader;
        std::vector<Frame> frames;
        Feed(reader, FrameBytes(1, 1), frames);
        std::string err;
        CHECK(reader.Finish(err));
        CHECK(frames.size() == 1);
    }
    {
        FramingReader reader;
        const auto frame = FrameBytes(1, 1, {0x01});
        reader.Append(frame.data(), 10);  // partial header
        std::string err;
        CHECK(!reader.Finish(err));
        CHECK(!err.empty());
        CHECK(reader.error() == FramingError::kHeaderTruncated);
    }
    {
        FramingReader reader;
        const auto frame = FrameBytes(1, 1, {0x01, 0x02, 0x03});
        reader.Append(frame.data(), kFrameHeaderSize + 2);  // partial body
        std::string err;
        CHECK(!reader.Finish(err));
        CHECK(!err.empty());
        CHECK(reader.error() == FramingError::kBodyTruncated);
    }
}

void TestQueueFifoAndBounds() {
    BoundedQueue<int> queue(3);
    CHECK(queue.Capacity() == 3);
    CHECK(queue.Push(1) == QueuePushResult::kAccepted);
    CHECK(queue.Push(2) == QueuePushResult::kAccepted);
    CHECK(queue.Push(3) == QueuePushResult::kAccepted);
    // Drop-oldest on overflow.
    CHECK(queue.Push(4) == QueuePushResult::kDroppedOldest);
    CHECK(queue.Size() == 3);
    {
        const auto v = queue.TryPop();
        CHECK(v.has_value());
        if (v) {
            CHECK(*v == 2);
        }
    }
    {
        const auto v = queue.TryPop();
        CHECK(v.has_value());
        if (v) {
            CHECK(*v == 3);
        }
    }
    {
        const auto v = queue.TryPop();
        CHECK(v.has_value());
        if (v) {
            CHECK(*v == 4);
        }
    }
    CHECK(queue.TryPop() == std::nullopt);
}

void TestQueueTryPushReject() {
    BoundedQueue<int> queue(2);
    queue.Push(1);
    queue.Push(2);
    CHECK(queue.TryPush(3) == QueuePushResult::kRejected);
    CHECK(queue.Size() == 2);
    {
        const auto v = queue.TryPop();
        CHECK(v.has_value());
        if (v) {
            CHECK(*v == 1);
        }
    }
    {
        const auto v = queue.TryPop();
        CHECK(v.has_value());
        if (v) {
            CHECK(*v == 2);
        }
    }
}

void TestQueueCloseAndPop() {
    BoundedQueue<int> queue(2);
    queue.Push(1);
    queue.Push(2);
    queue.Close();
    {
        const auto v = queue.Pop();
        CHECK(v.has_value());
        if (v) {
            CHECK(*v == 1);
        }
    }
    {
        const auto v = queue.Pop();
        CHECK(v.has_value());
        if (v) {
            CHECK(*v == 2);
        }
    }
    CHECK(queue.Pop() == std::nullopt);
}

void TestQueueProducerConsumer() {
    constexpr int kTotal = 5000;
    // Capacity above the produced count: no drop-oldest eviction can occur, so
    // the test is deterministic. Drop-oldest semantics have their own test.
    BoundedQueue<int> queue(kTotal + 64);
    std::thread producer([&queue] {
        for (int i = 0; i < kTotal; ++i) {
            queue.Push(i);
        }
    });
    int received = 0;
    long sum = 0;
    while (received < kTotal) {
        auto v = queue.Pop();
        if (v) {
            ++received;
            sum += *v;
        }
    }
    producer.join();
    queue.Close();
    CHECK(received == kTotal);
    CHECK(sum == static_cast<long>(kTotal) * (kTotal - 1) / 2);
    CHECK(queue.Pop() == std::nullopt);
}

void TestQueueDrain() {
    BoundedQueue<int> queue(4);
    queue.Push(10);
    queue.Push(20);
    auto drained = queue.Drain();
    CHECK(drained.size() == 2);
    CHECK(drained.front() == 10);
    CHECK(drained.back() == 20);
    CHECK(queue.Size() == 0);
}

}  // namespace

int main() {
    TestHeaderFixedSample();
    TestHeaderRoundTrip();
    TestDecodeShortBuffer();
    TestEncodeFrameAssembly();
    TestEncodeRejectsOversize();
    TestFramingSingleFrame();
    TestFramingCoalesced();
    TestFramingByteByByte();
    TestFramingPartialHeaderThenBody();
    TestFramingBadMagicAndVersion();
    TestFramingOversizeRejectedEarly();
    TestFramingMaxBodyAccepted();
    TestFinishCleanAndTruncated();
    TestQueueFifoAndBounds();
    TestQueueTryPushReject();
    TestQueueCloseAndPop();
    TestQueueProducerConsumer();
    TestQueueDrain();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
