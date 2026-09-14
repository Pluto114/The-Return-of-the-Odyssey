// Protocol payload encode/decode helpers for the client (Phase 1 movement set).
//
// Bridges A's generated protobuf messages to small POD "view" structs so the
// game/render code never touches protobuf directly. Only messages the client
// actually sends/consumes in Phase 1 (Ping/Pong, Login) are covered here;
// PlayerInput/WorldSnapshot join in the following slices.
#pragma once

#include "game.pb.h"
#include "lobby.pb.h"
#include "session.pb.h"
#include "stage.pb.h"
#include "system.pb.h"

#include <cstdint>
#include <string>
#include <vector>

namespace odyssey::client::network::payload {

// ---- Matchmaking ----------------------------------------------------------

struct MatchFoundData {
    std::uint64_t room_id = 0;
    std::string room_token;
    std::vector<std::uint64_t> teammates;
};

inline std::vector<std::uint8_t> EncodeMatchRequest() {
    odyssey::protocol::v1::MatchRequest proto;
    std::vector<std::uint8_t> out(proto.ByteSizeLong());
    proto.SerializeToArray(out.data(), static_cast<int>(out.size()));
    return out;
}

inline bool DecodeMatchFound(const std::vector<std::uint8_t>& payload, MatchFoundData& out) {
    odyssey::protocol::v1::MatchFound proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.room_id = proto.room_id();
    out.room_token = proto.room_token();
    out.teammates.assign(proto.teammates().begin(), proto.teammates().end());
    return out.room_id != 0;
}

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
    float dir_x = 0.0f;           // move intent, world x (server x); normalized
    float dir_z = 0.0f;           // move intent, world z (server y); normalized
    float aim_x = 0.0f;           // aim heading, world x (finite; required when shoot)
    float aim_z = 0.0f;           // aim heading, world z
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
    auto* aim = proto.mutable_aim();
    aim->set_x(data.aim_x);
    aim->set_y(data.aim_z);
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
    // Base stats resolved by the server (shown in the HUD; reward effects
    // become visible here after the next snapshot).
    float attack = 0.0f;
    float defense = 0.0f;
    float move_speed = 0.0f;
};

struct SnapshotMonsterView {
    std::uint64_t id = 0;
    float pos_x = 0.0f;
    float pos_z = 0.0f;
    float vel_x = 0.0f;
    float vel_z = 0.0f;
    float hp = 0.0f;
    float max_hp = 0.0f;
    std::uint32_t state = 0;  // 0 idle / 1 chase / 2 attack / 3 dead
};

struct StageStateView {
    std::uint32_t index = 0;
    std::int64_t seed = 0;
    std::uint32_t state = 0;  // 0 waiting/1 playing/2 clear/3 reward/4 prep/5 failed/6 closed
    std::uint32_t monsters_remaining = 0;
};

struct WorldSnapshotView {
    std::uint64_t server_tick = 0;
    std::uint32_t last_processed_input = 0;  // self ack (server-applied input_seq)
    bool has_self = false;
    SnapshotPlayerView self;
    std::vector<SnapshotPlayerView> others;    // ascending by id
    std::vector<SnapshotMonsterView> monsters; // full set: missing => removed
    StageStateView stage;
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
    out.attack = p.attack();
    out.defense = p.defense();
    out.move_speed = p.move_speed();
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
    out.monsters.clear();
    out.monsters.reserve(proto.monsters_size());
    for (int i = 0; i < proto.monsters_size(); ++i) {
        const auto& m = proto.monsters(i);
        SnapshotMonsterView view;
        view.id = m.monster_id();
        if (m.has_position()) {
            view.pos_x = m.position().x();
            view.pos_z = m.position().y();
        }
        if (m.has_velocity()) {
            view.vel_x = m.velocity().x();
            view.vel_z = m.velocity().y();
        }
        view.hp = m.hp();
        view.max_hp = m.max_hp();
        view.state = m.state();
        out.monsters.push_back(view);
    }
    if (proto.has_stage()) {
        out.stage.index = proto.stage().index();
        out.stage.seed = proto.stage().seed();
        out.stage.state = proto.stage().state();
        out.stage.monsters_remaining = proto.stage().monsters_remaining();
    } else {
        out.stage = StageStateView{};
    }
    return true;
}

// ---- Reliable combat events (320-327) --------------------------------------

struct ProjectileSpawnData {
    std::uint64_t projectile_id = 0;
    std::uint64_t owner_id = 0;
    float pos_x = 0.0f;
    float pos_z = 0.0f;
    float vel_x = 0.0f;
    float vel_z = 0.0f;
    std::uint64_t expires_at_tick = 0;
    std::uint64_t server_tick = 0;
};

struct ProjectileDestroyData {
    std::uint64_t projectile_id = 0;
    std::uint64_t owner_id = 0;
    float pos_x = 0.0f;
    float pos_z = 0.0f;
    std::uint64_t server_tick = 0;
};

struct DamageEventData {
    std::uint64_t source_id = 0;
    std::uint64_t target_id = 0;
    float amount = 0.0f;
    float remaining_health = 0.0f;
    std::uint64_t server_tick = 0;
};

struct DeathEventData {
    std::uint64_t entity_id = 0;
    std::uint64_t killer_id = 0;
    std::uint64_t server_tick = 0;
};

struct StageEventData {
    std::uint32_t stage_index = 0;
    std::uint64_t server_tick = 0;
};

inline bool DecodeProjectileSpawn(const std::vector<std::uint8_t>& payload,
                                  ProjectileSpawnData& out) {
    odyssey::protocol::v1::ProjectileSpawnEvent proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.projectile_id = proto.projectile_id();
    out.owner_id = proto.owner_id();
    if (proto.has_position()) {
        out.pos_x = proto.position().x();
        out.pos_z = proto.position().y();
    }
    if (proto.has_velocity()) {
        out.vel_x = proto.velocity().x();
        out.vel_z = proto.velocity().y();
    }
    out.expires_at_tick = proto.expires_at_tick();
    out.server_tick = proto.server_tick();
    return true;
}

inline bool DecodeProjectileDestroy(const std::vector<std::uint8_t>& payload,
                                    ProjectileDestroyData& out) {
    odyssey::protocol::v1::ProjectileDestroyEvent proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.projectile_id = proto.projectile_id();
    out.owner_id = proto.owner_id();
    if (proto.has_position()) {
        out.pos_x = proto.position().x();
        out.pos_z = proto.position().y();
    }
    out.server_tick = proto.server_tick();
    return true;
}

inline bool DecodeDamageEvent(const std::vector<std::uint8_t>& payload, DamageEventData& out) {
    odyssey::protocol::v1::DamageEvent proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.source_id = proto.source_id();
    out.target_id = proto.target_id();
    out.amount = proto.amount();
    out.remaining_health = proto.remaining_health();
    out.server_tick = proto.server_tick();
    return true;
}

inline bool DecodeDeathEvent(const std::vector<std::uint8_t>& payload, DeathEventData& out) {
    odyssey::protocol::v1::DeathEvent proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.entity_id = proto.entity_id();
    out.killer_id = proto.killer_id();
    out.server_tick = proto.server_tick();
    return true;
}

// StageStartedEvent / StageClearedEvent / TeamDefeatedEvent share the same
// shape (stage_index + server_tick).
inline bool DecodeStageEvent(const std::vector<std::uint8_t>& payload, StageEventData& out) {
    odyssey::protocol::v1::StageStartedEvent proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.stage_index = proto.stage_index();
    out.server_tick = proto.server_tick();
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

// ---- Resume (reconnect after a transient disconnect) -----------------------

struct ResumeResponseData {
    std::uint32_t reason = 0;  // ReasonCode
    std::string message;
    std::uint64_t session_id = 0;
    std::uint64_t player_id = 0;
    bool ok = false;
};

inline std::vector<std::uint8_t> EncodeResumeRequest(const std::vector<std::uint8_t>& token,
                                                     std::uint32_t protocol_version) {
    odyssey::protocol::v1::ResumeRequest proto;
    proto.set_resume_token(token.data(), token.size());
    proto.set_protocol_version(protocol_version);
    std::vector<std::uint8_t> out(proto.ByteSizeLong());
    proto.SerializeToArray(out.data(), static_cast<int>(out.size()));
    return out;
}

inline bool DecodeResumeResponse(const std::vector<std::uint8_t>& payload,
                                 ResumeResponseData& out) {
    odyssey::protocol::v1::ResumeResponse proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.reason = static_cast<std::uint32_t>(proto.reason());
    out.message = proto.message();
    out.session_id = proto.session_id();
    out.player_id = proto.player_id();
    out.ok = (proto.reason() == odyssey::protocol::v1::REASON_OK);
    return true;
}

// ---- Rewards (410-413) -----------------------------------------------------

struct RewardOptionsData {
    std::uint32_t stage_index = 0;
    std::vector<std::uint32_t> equipment_ids;
    std::uint64_t deadline_server_tick = 0;
};

struct RewardAppliedData {
    std::uint32_t reason = 0;  // ReasonCode; REASON_OK == 1
    std::uint32_t equipment_id = 0;
    bool ok = false;
};

inline bool DecodeRewardOptions(const std::vector<std::uint8_t>& payload,
                                RewardOptionsData& out) {
    odyssey::protocol::v1::RewardOptions proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.stage_index = proto.stage_index();
    out.equipment_ids.assign(proto.equipment_ids().begin(), proto.equipment_ids().end());
    out.deadline_server_tick = proto.deadline_server_tick();
    return true;
}

inline std::vector<std::uint8_t> EncodeRewardChoice(std::uint32_t equipment_id) {
    odyssey::protocol::v1::RewardChoice proto;
    proto.set_equipment_id(equipment_id);
    std::vector<std::uint8_t> out(proto.ByteSizeLong());
    proto.SerializeToArray(out.data(), static_cast<int>(out.size()));
    return out;
}

inline bool DecodeRewardApplied(const std::vector<std::uint8_t>& payload,
                                RewardAppliedData& out) {
    odyssey::protocol::v1::RewardApplied proto;
    if (!proto.ParseFromArray(payload.data(), static_cast<int>(payload.size()))) {
        return false;
    }
    out.reason = static_cast<std::uint32_t>(proto.reason());
    out.equipment_id = proto.equipment_id();
    out.ok = (proto.reason() == odyssey::protocol::v1::REASON_OK);
    return true;
}

inline std::vector<std::uint8_t> EncodeNextStageRequest() {
    odyssey::protocol::v1::NextStageRequest proto;
    std::vector<std::uint8_t> out(proto.ByteSizeLong());
    proto.SerializeToArray(out.data(), static_cast<int>(out.size()));
    return out;
}

}  // namespace odyssey::client::network::payload
