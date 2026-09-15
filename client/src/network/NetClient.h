// Asio TCP client owned by the client's Network Thread.
//
// - Connects asynchronously to a gameserver host/port.
// - Runs one io_context on a dedicated thread (the Network Thread).
// - Feeds every received byte into FramingReader and emits one NetEvent
//   per validated frame or state change. The event callback runs on the
//   Network Thread and must be cheap and thread-safe (push into a bounded
//   queue for the Main Thread - see NetMessage.h).
// - Outbound writes are queued on the same thread via a strand; the write
//   queue is bounded and drops the oldest frame when full, reporting the
//   drop as a kOutboundDropped event (plan: bounded queues everywhere).
// - Stop() closes the socket, stops the io_context and joins the thread.
#pragma once

#include "network/NetMessage.h"

#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <string>

namespace odyssey::client::network {

class NetClient {
public:
    using EventCallback = std::function<void(NetEvent&&)>;

    // outbound_queue_capacity overrides the default 64-frame write queue cap;
    // kept injectable so the saturation/drop policy can be tested headlessly.
    explicit NetClient(std::size_t outbound_queue_capacity = 64);
    ~NetClient();

    NetClient(const NetClient&) = delete;
    NetClient& operator=(const NetClient&) = delete;

    // Sets the handler invoked on the Network Thread for every NetEvent.
    // Must be called before Start(); not thread-safe afterwards.
    void SetEventCallback(EventCallback callback);

    // Launches the Network Thread and begins an async connect. Returns false
    // when already running or the callback was not set.
    bool Start(std::string host, std::uint16_t port);

    // Starts another connection attempt (e.g. a retry after a failure).
    // No-op while a connect/connection is active or after Stop().
    void Connect(std::string host, std::uint16_t port);

    bool IsRunning() const;

    // Outbound write-queue depth, published from the Network Thread: the instant
    // depth and the high-water mark since the last reset. Safe from the Main Thread
    // (the debug overlay reads these).
    std::size_t OutboundDepth() const;
    std::size_t OutboundMaxDepth() const;
    void ResetOutboundMaxDepth();

    // Enqueues one outbound frame (header derived from message_type/sequence;
    // payload bytes are opaque here). Safe to call from any thread.
    void SendFrame(std::uint16_t message_type, std::uint32_t sequence,
                   const std::uint8_t* payload, std::size_t payload_size);

    // Closes the connection and joins the Network Thread. Safe to call from
    // the Main Thread (e.g. on window close). Idempotent.
    void Stop();

private:
    class Impl;
    std::shared_ptr<Impl> impl_;
};

}  // namespace odyssey::client::network
