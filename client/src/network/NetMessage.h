// Network -> Main handoff types.
//
// Thread model (ARCHITECTURE.md section 27): the Network Thread owns the
// socket and frame decoding; it must never touch the client GameWorld. It
// hands decoded inbound messages and connection state changes to the Main
// Thread through NetEvent items queued in a bounded queue.
#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace odyssey::client::network {

// A decoded inbound frame, delivered as opaque payload bytes. Decoding the
// payload into concrete protobuf messages is protocol-layer work that waits
// for A's reviewed message set (feature/network); the client never defines a
// second protocol schema.
struct NetMessage {
    std::uint16_t message_type = 0;
    std::uint32_t sequence = 0;
    std::vector<std::uint8_t> payload;
};

enum class ConnectionState {
    kIdle,          // client created, not started
    kConnecting,    // async connect in flight
    kConnected,     // socket established, frames flowing
    kDisconnected,  // clean EOF or explicit close
    kFailed,        // connect/IO/framing error, reason in detail
};

const char* ToString(ConnectionState state);

// One item produced on the Network Thread and consumed on the Main Thread.
struct NetEvent {
    enum class Kind {
        kStateChanged,    // state + detail
        kMessage,         // message: an inbound frame
        kOutboundDropped  // detail: an outbound frame was dropped (queue full)
    };

    Kind kind = Kind::kStateChanged;
    ConnectionState state = ConnectionState::kIdle;  // meaningful for kStateChanged
    std::string detail;                              // human-readable reason/note
    NetMessage message;                              // meaningful for kMessage
};

}  // namespace odyssey::client::network
