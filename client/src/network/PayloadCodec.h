// Protocol payload encode/decode helpers for the client (Phase 1 movement set).
//
// Bridges A's generated protobuf messages to small POD "view" structs so the
// game/render code never touches protobuf directly. Only messages the client
// actually sends/consumes in Phase 1 (Ping/Pong, Login) are covered here;
// PlayerInput/WorldSnapshot join in the following slices.
#pragma once

#include "game.pb.h"
#include "session.pb.h"
#include "system.pb.h"

#include <cstdint>
#include <string>
#include <vector>

namespace odyssey::client::network::payload {

// ---- Ping / Pong -----------------------------------------------------------

struct PingData {
    std::uint64_t client_time_ms = 0;
    std::uint64_t nonce = 0;
};

struct PongData {
    std::uint64_t client_time_ms = 0;
    std::uint64_t server_time_ms = 0;
    std::uint64_t nonce = 0;
};

inline std::vector<std::uint8_t> EncodePing(const PingData& ping) {
    odyssey::protocol::v1::Ping proto;
    proto.set_client_time_ms(ping.client_time_ms);
    proto.set_nonce(ping.nonce);
    std::vector<std::uint8_t> out(proto.ByteSizeLong());
    proto.SerializeToArray(out.data(), static_cast<int>(out.size()));
    return out;
}

inline bool DecodePong(const std::vector<std::uint8_t>& payload, PongData& out) {
    odyssey::protocol::v1::Pong proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.client_time_ms = proto.client_time_ms();
    out.server_time_ms = proto.server_time_ms();
    out.nonce = proto.nonce();
    return true;
}

// ---- PlayerInput (C -> S, 30Hz intent) ------------------------------------

struct PlayerInputData {
    std::uint32_t input_seq = 0;  // client-side 30Hz seq (independent of Frame Seq)
    float dir_x = 0.0f;           // world x (server x); normalized
    float dir_z = 0.0f;           // world z (server y); normalized
    bool shoot = false;
    std::uint64_t client_tick_ms = 0;
};

inline std::vector<std::uint8_t> EncodePlayerInput(const PlayerInputData& data) {
    odyssey::protocol::v1::PlayerInput proto;
    proto.set_input_seq(data.input_seq);
    proto.set_client_tick_ms(data.client_tick_ms);
    auto* move = proto.mutable_move();
    move->set_x(data.dir_x);
    move->set_y(data.dir_z);
    proto.set_shoot(data.shoot);
    std::vector<std::uint8_t> out(proto.ByteSizeLong());
    proto.SerializeToArray(out.data(), static_cast<int>(out.size()));
    return out;
}

// ---- WorldSnapshot (S -> C, 10Hz authoritative) ----------------------------

struct SnapshotPlayerView {
    std::uint64_t id = 0;
    float pos_x = 0.0f;  // server x -> client x
    float pos_z = 0.0f;  // server y -> client z
    float vel_x = 0.0f;
    float vel_z = 0.0f;
    float hp = 0.0f;
    float max_hp = 0.0f;
    bool alive = true;
};

struct WorldSnapshotView {
    std::uint64_t server_tick = 0;
    std::uint32_t last_processed_input = 0;  // self ack (server-applied input_seq)
    bool has_self = false;
    SnapshotPlayerView self;
    std::vector<SnapshotPlayerView> others;  // ascending by id
    std::size_t monster_count = 0;           // D2: decoded later with combat UI
};

inline SnapshotPlayerView MapPlayer(const odyssey::protocol::v1::PlayerSnapshot& p) {
    SnapshotPlayerView out;
    out.id = p.player_id();
    if (p.has_position()) {
        out.pos_x = p.position().x();
        out.pos_z = p.position().y();
    }
    if (p.has_velocity()) {
        out.vel_x = p.velocity().x();
        out.vel_z = p.velocity().y();
    }
    out.hp = p.hp();
    out.max_hp = p.max_hp();
    out.alive = p.alive();
    return out;
}

inline bool DecodeWorldSnapshot(const std::vector<std::uint8_t>& payload,
                                WorldSnapshotView& out) {
    odyssey::protocol::v1::WorldSnapshot proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.server_tick = proto.server_tick();
    out.last_processed_input = proto.last_processed_input();
    out.has_self = proto.has_self();
    if (proto.has_self()) {
        out.self = MapPlayer(proto.self());
    }
    out.others.clear();
    out.others.reserve(proto.players_size());
    for (int i = 0; i < proto.players_size(); ++i) {
        out.others.push_back(MapPlayer(proto.players(i)));
    }
    out.monster_count = static_cast<std::size_t>(proto.monsters_size());
    return true;
}

// ---- Disconnect ------------------------------------------------------------

struct DisconnectData {
    std::uint32_t reason = 0;  // odyssey::protocol::v1::ReasonCode value
    std::string message;
};

inline bool DecodeDisconnect(const std::vector<std::uint8_t>& payload, DisconnectData& out) {
    odyssey::protocol::v1::Disconnect proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.reason = static_cast<std::uint32_t>(proto.reason());
    out.message = proto.message();
    return true;
}

// ---- Login -----------------------------------------------------------------

struct LoginRequestData {
    std::uint32_t protocol_version = 1;
    std::string token;          // v1: any non-empty string
    std::string display_name;   // optional, <= 32 bytes UTF-8
};

struct LoginResponseData {
    std::uint32_t protocol_version = 0;
    std::uint32_t reason = 0;   // odyssey::protocol::v1::ReasonCode value
    std::string message;
    std::uint64_t session_id = 0;
    std::uint64_t player_id = 0;
    std::vector<std::uint8_t> resume_token;
    bool ok = false;            // reason == REASON_OK
};

inline std::vector<std::uint8_t> EncodeLoginRequest(const LoginRequestData& login) {
    odyssey::protocol::v1::LoginRequest proto;
    proto.set_protocol_version(login.protocol_version);
    proto.set_token(login.token);
    if (!login.display_name.empty()) {
        proto.set_display_name(login.display_name);
    }
    std::vector<std::uint8_t> out(proto.ByteSizeLong());
    proto.SerializeToArray(out.data(), static_cast<int>(out.size()));
    return out;
}

inline bool DecodeLoginResponse(const std::vector<std::uint8_t>& payload, LoginResponseData& out) {
    odyssey::protocol::v1::LoginResponse proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.protocol_version = proto.protocol_version();
    out.reason = static_cast<std::uint32_t>(proto.reason());
    out.message = proto.message();
    out.session_id = proto.session_id();
    out.player_id = proto.player_id();
    out.resume_token.assign(proto.resume_token().begin(), proto.resume_token().end());
    out.ok = (proto.reason() == odyssey::protocol::v1::REASON_OK);
    return true;
}

}  // namespace odyssey::client::network::payload