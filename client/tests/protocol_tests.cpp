// Headless tests for the protocol payload codec (Ping/Pong, Login, Disconnect).
// Uses A's generated protobuf classes as the "other side" to prove encode and
// decode agree with the wire schema.
#include "network/PayloadCodec.h"

#include "common.pb.h"
#include "lobby.pb.h"
#include "session.pb.h"
#include "system.pb.h"

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <string>
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

using odyssey::client::network::payload::DecodeDamageEvent;
using odyssey::client::network::payload::DecodeDeathEvent;
using odyssey::client::network::payload::DecodeDisconnect;
using odyssey::client::network::payload::DecodeLoginResponse;
using odyssey::client::network::payload::DecodeMatchFound;
using odyssey::client::network::payload::DecodePong;
using odyssey::client::network::payload::DecodeProjectileDestroy;
using odyssey::client::network::payload::DecodeProjectileSpawn;
using odyssey::client::network::payload::DecodeRewardApplied;
using odyssey::client::network::payload::DecodeRewardOptions;
using odyssey::client::network::payload::EncodeRewardChoice;
using odyssey::client::network::payload::DecodeResumeResponse;
using odyssey::client::network::payload::EncodeResumeRequest;
using odyssey::client::network::payload::DecodeStageEvent;
using odyssey::client::network::payload::DecodeWorldSnapshot;
using odyssey::client::network::payload::EncodeLoginRequest;
using odyssey::client::network::payload::EncodeMatchRequest;
using odyssey::client::network::payload::EncodePing;
using odyssey::client::network::payload::EncodePlayerInput;
using odyssey::client::network::payload::LoginRequestData;
using odyssey::client::network::payload::LoginResponseData;
using odyssey::client::network::payload::PingData;
using odyssey::client::network::payload::PongData;

std::vector<std::uint8_t> Serialize(const google::protobuf::MessageLite& msg) {
    std::vector<std::uint8_t> out(msg.ByteSizeLong());
    msg.SerializeToArray(out.data(), static_cast<int>(out.size()));
    return out;
}

void TestPingWireRoundTrip() {
    PingData ping;
    ping.client_time_ms = 1234567890ULL;
    ping.nonce = 42;
    const auto bytes = EncodePing(ping);
    CHECK(!bytes.empty());

    // The "server side": parse with the generated proto class.
    odyssey::protocol::v1::Ping parsed;
    CHECK(parsed.ParseFromArray(bytes.data(), static_cast<int>(bytes.size())));
    CHECK(parsed.client_time_ms() == 1234567890ULL);
    CHECK(parsed.nonce() == 42);
}

void TestPongDecodeFromServer() {
    odyssey::protocol::v1::Pong proto;
    proto.set_client_time_ms(111);
    proto.set_server_time_ms(222);
    proto.set_nonce(7);
    const auto bytes = Serialize(proto);

    PongData pong;
    CHECK(DecodePong(bytes, pong));
    CHECK(pong.client_time_ms == 111);
    CHECK(pong.server_time_ms == 222);
    CHECK(pong.nonce == 7);

    // Truncated payload must fail, not half-decode.
    std::vector<std::uint8_t> truncated(bytes.begin(), bytes.begin() + 1);
    PongData bad;
    CHECK(!DecodePong(truncated, bad));
}

void TestLoginRequestWire() {
    LoginRequestData login;
    login.protocol_version = 1;
    login.token = "dev";
    login.display_name = "odyssey-c";
    const auto bytes = EncodeLoginRequest(login);

    odyssey::protocol::v1::LoginRequest parsed;
    CHECK(parsed.ParseFromArray(bytes.data(), static_cast<int>(bytes.size())));
    CHECK(parsed.protocol_version() == 1);
    CHECK(parsed.token() == "dev");
    CHECK(parsed.display_name() == "odyssey-c");
}

void TestLoginResponseOk() {
    odyssey::protocol::v1::LoginResponse proto;
    proto.set_protocol_version(1);
    proto.set_reason(odyssey::protocol::v1::REASON_OK);
    proto.set_message("ok");
    proto.set_session_id(0x1122334455667788ULL);
    proto.set_player_id(99);
    proto.set_resume_token("tok-bytes", 9);
    const auto bytes = Serialize(proto);

    LoginResponseData login;
    CHECK(DecodeLoginResponse(bytes, login));
    CHECK(login.ok);
    CHECK(login.protocol_version == 1);
    CHECK(login.session_id == 0x1122334455667788ULL);
    CHECK(login.player_id == 99);
    CHECK(login.resume_token.size() == 9);
    CHECK(std::memcmp(login.resume_token.data(), "tok-bytes", 9) == 0);
}

void TestLoginResponseRejected() {
    odyssey::protocol::v1::LoginResponse proto;
    proto.set_protocol_version(1);
    proto.set_reason(odyssey::protocol::v1::REASON_INVALID_VERSION);
    proto.set_message("version mismatch");
    const auto bytes = Serialize(proto);

    LoginResponseData login;
    CHECK(DecodeLoginResponse(bytes, login));
    CHECK(!login.ok);
    CHECK(login.reason == static_cast<std::uint32_t>(odyssey::protocol::v1::REASON_INVALID_VERSION));
    CHECK(login.message == "version mismatch");
    CHECK(login.session_id == 0);
    CHECK(login.player_id == 0);
}

void TestMatchmakingWire() {
    const auto request_bytes = EncodeMatchRequest();
    odyssey::protocol::v1::MatchRequest request;
    CHECK(request.ParseFromArray(request_bytes.data(), static_cast<int>(request_bytes.size())));

    odyssey::protocol::v1::MatchFound found;
    found.set_room_id(77);
    found.set_room_token("room-77");
    found.add_teammates(9);
    odyssey::client::network::payload::MatchFoundData decoded;
    CHECK(DecodeMatchFound(Serialize(found), decoded));
    CHECK(decoded.room_id == 77);
    CHECK(decoded.room_token == "room-77");
    CHECK(decoded.teammates.size() == 1);
    CHECK(decoded.teammates[0] == 9);
}

void TestPlayerInputWire() {
    odyssey::client::network::payload::PlayerInputData data;
    data.input_seq = 77;
    data.dir_x = 0.70710678f;
    data.dir_z = -0.70710678f;
    data.aim_x = 0.0f;
    data.aim_z = 1.0f;
    data.shoot = true;
    data.use_potion = true;
    data.reload = true;
    data.client_tick_ms = 12345;
    const auto bytes = EncodePlayerInput(data);

    odyssey::protocol::v1::PlayerInput parsed;
    CHECK(parsed.ParseFromArray(bytes.data(), static_cast<int>(bytes.size())));
    CHECK(parsed.input_seq() == 77);
    CHECK(parsed.client_tick_ms() == 12345);
    CHECK(parsed.has_move());
    if (parsed.has_move()) {
        CHECK(parsed.move().x() == data.dir_x);
        CHECK(parsed.move().y() == data.dir_z);
    }
    CHECK(parsed.has_aim());
    if (parsed.has_aim()) {
        CHECK(parsed.aim().x() == data.aim_x);
        CHECK(parsed.aim().y() == data.aim_z);
    }
    CHECK(parsed.shoot());
    CHECK(parsed.use_potion());
    CHECK(parsed.reload());
    // Zero-intent (release) encodes fine with seq and no direction magnitude.
    odyssey::client::network::payload::PlayerInputData idle;
    idle.input_seq = 78;
    const auto idle_bytes = EncodePlayerInput(idle);
    odyssey::protocol::v1::PlayerInput idle_parsed;
    CHECK(idle_parsed.ParseFromArray(idle_bytes.data(), static_cast<int>(idle_bytes.size())));
    CHECK(idle_parsed.input_seq() == 78);
    CHECK(!idle_parsed.use_potion());
    CHECK(!idle_parsed.reload());
    if (idle_parsed.has_move()) {
        CHECK(idle_parsed.move().x() == 0.0f);
        CHECK(idle_parsed.move().y() == 0.0f);
    }
}

void TestWorldSnapshotDecode() {
    odyssey::protocol::v1::WorldSnapshot proto;
    proto.set_server_tick(1234);
    proto.set_last_processed_input(55);

    auto* self = proto.mutable_self();
    self->set_player_id(10);
    self->mutable_position()->set_x(2.0f);
    self->mutable_position()->set_y(3.0f);
    self->mutable_velocity()->set_x(0.5f);
    self->set_hp(90.0f);
    self->set_max_hp(100.0f);
    self->set_attack(12.0f);
    self->set_defense(4.0f);
    self->set_move_speed(5.0f);
    self->set_alive(true);
    self->set_weapon_id(1001);
    self->set_relic_id(2002);
    self->set_potion_id(3001);
    self->set_ammo(3);
    self->set_magazine_capacity(24);
    self->set_reload_ticks_remaining(30);
    self->set_reload_duration_ticks(45);
    proto.mutable_stage()->set_stage_limit(12);
    proto.mutable_stage()->set_difficulty_score(1.5f);
    proto.mutable_stage()->set_difficulty_adjustment(-0.1f);
    proto.mutable_stage()->set_previous_clear_seconds(55);
    proto.mutable_stage()->set_previous_team_hp_percent(0.4f);

    auto* other1 = proto.add_players();
    other1->set_player_id(1);
    other1->mutable_position()->set_x(5.0f);
    other1->mutable_position()->set_y(6.0f);
    auto* other2 = proto.add_players();
    other2->set_player_id(2);
    other2->mutable_position()->set_x(-1.0f);
    other2->mutable_position()->set_y(-2.0f);

    auto* monster = proto.add_monsters();
    monster->set_monster_id(900);
    monster->mutable_position()->set_x(7.0f);
    monster->mutable_position()->set_y(8.0f);
    monster->set_hp(30.0f);
    monster->set_max_hp(50.0f);
    monster->set_state(1);
    auto* pickup = proto.add_pickups();
    pickup->set_pickup_id(990);
    pickup->set_kind(odyssey::protocol::v1::PICKUP_KIND_WEAPON);
    pickup->mutable_position()->set_x(4.0f);
    pickup->mutable_position()->set_y(16.0f);
    pickup->set_equipment_id(1001);
    proto.mutable_stage()->set_index(2);
    proto.mutable_stage()->set_seed(4242);
    proto.mutable_stage()->set_state(1);
    proto.mutable_stage()->set_monsters_remaining(3);
    const auto bytes = Serialize(proto);

    odyssey::client::network::payload::WorldSnapshotView view;
    CHECK(DecodeWorldSnapshot(bytes, view));
    CHECK(view.self.ammo == 3);
    CHECK(view.self.magazine_capacity == 24);
    CHECK(view.self.reload_ticks_remaining == 30);
    CHECK(view.self.reload_duration_ticks == 45);
    CHECK(view.stage.stage_limit == 12);
    CHECK(view.stage.difficulty_score == 1.5f);
    CHECK(view.stage.difficulty_adjustment == -0.1f);
    CHECK(view.stage.previous_clear_seconds == 55);
    CHECK(view.stage.previous_team_hp_percent == 0.4f);
    CHECK(view.server_tick == 1234);
    CHECK(view.last_processed_input == 55);
    CHECK(view.has_self);
    if (view.has_self) {
        CHECK(view.self.id == 10);
        CHECK(view.self.pos_x == 2.0f);
        CHECK(view.self.pos_z == 3.0f);
        CHECK(view.self.vel_x == 0.5f);
        CHECK(view.self.hp == 90.0f);
        CHECK(view.self.max_hp == 100.0f);
        CHECK(view.self.attack == 12.0f);
        CHECK(view.self.defense == 4.0f);
        CHECK(view.self.move_speed == 5.0f);
        CHECK(view.self.alive);
        CHECK(view.self.weapon_id == 1001);
        CHECK(view.self.relic_id == 2002);
        CHECK(view.self.potion_id == 3001);
    }
    CHECK(view.pickups.size() == 1);
    if (view.pickups.size() == 1) {
        CHECK(view.pickups[0].id == 990);
        CHECK(view.pickups[0].kind == 2);
        CHECK(view.pickups[0].pos_x == 4.0f);
        CHECK(view.pickups[0].pos_z == 16.0f);
        CHECK(view.pickups[0].equipment_id == 1001);
    }
    CHECK(view.others.size() == 2);
    if (view.others.size() == 2) {
        CHECK(view.others[0].id == 1);
        CHECK(view.others[0].pos_x == 5.0f);
        CHECK(view.others[0].pos_z == 6.0f);
        CHECK(view.others[1].id == 2);
        CHECK(view.others[1].pos_x == -1.0f);
        CHECK(view.others[1].pos_z == -2.0f);
    }
    CHECK(view.monsters.size() == 1);
    if (view.monsters.size() == 1) {
        CHECK(view.monsters[0].id == 900);
        CHECK(view.monsters[0].pos_x == 7.0f);
        CHECK(view.monsters[0].pos_z == 8.0f);
        CHECK(view.monsters[0].hp == 30.0f);
        CHECK(view.monsters[0].max_hp == 50.0f);
        CHECK(view.monsters[0].state == 1);
    }
    CHECK(view.stage.index == 2);
    CHECK(view.stage.seed == 4242);
    CHECK(view.stage.state == 1);
    CHECK(view.stage.monsters_remaining == 3);
}

void TestCombatEventsDecode() {
    // ProjectileSpawnEvent
    odyssey::protocol::v1::ProjectileSpawnEvent spawn;
    spawn.set_projectile_id(700);
    spawn.set_owner_id(10);
    spawn.mutable_position()->set_x(3.0f);
    spawn.mutable_position()->set_y(4.0f);
    spawn.mutable_velocity()->set_x(5.0f);
    spawn.mutable_velocity()->set_y(6.0f);
    spawn.set_expires_at_tick(999);
    spawn.set_server_tick(123);
    odyssey::client::network::payload::ProjectileSpawnData spawn_view;
    CHECK(DecodeProjectileSpawn(Serialize(spawn), spawn_view));
    CHECK(spawn_view.projectile_id == 700);
    CHECK(spawn_view.owner_id == 10);
    CHECK(spawn_view.pos_x == 3.0f);
    CHECK(spawn_view.pos_z == 4.0f);
    CHECK(spawn_view.vel_x == 5.0f);
    CHECK(spawn_view.vel_z == 6.0f);
    CHECK(spawn_view.expires_at_tick == 999);
    CHECK(spawn_view.server_tick == 123);

    // ProjectileDestroyEvent
    odyssey::protocol::v1::ProjectileDestroyEvent destroy;
    destroy.set_projectile_id(700);
    destroy.set_owner_id(10);
    destroy.mutable_position()->set_x(9.0f);
    destroy.mutable_position()->set_y(9.0f);
    destroy.set_server_tick(130);
    odyssey::client::network::payload::ProjectileDestroyData destroy_view;
    CHECK(DecodeProjectileDestroy(Serialize(destroy), destroy_view));
    CHECK(destroy_view.projectile_id == 700);
    CHECK(destroy_view.pos_x == 9.0f);
    CHECK(destroy_view.pos_z == 9.0f);

    // DamageEvent
    odyssey::protocol::v1::DamageEvent damage;
    damage.set_source_id(10);
    damage.set_target_id(900);
    damage.set_amount(12.5f);
    damage.set_remaining_health(37.5f);
    damage.set_server_tick(131);
    odyssey::client::network::payload::DamageEventData damage_view;
    CHECK(DecodeDamageEvent(Serialize(damage), damage_view));
    CHECK(damage_view.source_id == 10);
    CHECK(damage_view.target_id == 900);
    CHECK(damage_view.amount == 12.5f);
    CHECK(damage_view.remaining_health == 37.5f);

    // DeathEvent
    odyssey::protocol::v1::DeathEvent death;
    death.set_entity_id(900);
    death.set_killer_id(10);
    death.set_server_tick(132);
    odyssey::client::network::payload::DeathEventData death_view;
    CHECK(DecodeDeathEvent(Serialize(death), death_view));
    CHECK(death_view.entity_id == 900);
    CHECK(death_view.killer_id == 10);

    // StageStartedEvent / StageClearedEvent / TeamDefeatedEvent share a shape.
    odyssey::protocol::v1::StageStartedEvent stage;
    stage.set_stage_index(2);
    stage.set_server_tick(200);
    odyssey::client::network::payload::StageEventData stage_view;
    CHECK(DecodeStageEvent(Serialize(stage), stage_view));
    CHECK(stage_view.stage_index == 2);
    CHECK(stage_view.server_tick == 200);
}

void TestRewardWire() {
    // RewardOptions (S -> C)
    odyssey::protocol::v1::RewardOptions options;
    options.set_stage_index(2);
    options.add_equipment_ids(11);
    options.add_equipment_ids(12);
    options.add_equipment_ids(13);
    options.set_deadline_server_tick(5000);
    odyssey::client::network::payload::RewardOptionsData options_view;
    CHECK(DecodeRewardOptions(Serialize(options), options_view));
    CHECK(options_view.stage_index == 2);
    CHECK(options_view.equipment_ids.size() == 3);
    CHECK(options_view.equipment_ids[1] == 12);
    CHECK(options_view.deadline_server_tick == 5000);

    // RewardChoice (C -> S): only the candidate id travels.
    const auto choice_bytes = EncodeRewardChoice(12);
    odyssey::protocol::v1::RewardChoice choice;
    CHECK(choice.ParseFromArray(choice_bytes.data(), static_cast<int>(choice_bytes.size())));
    CHECK(choice.equipment_id() == 12);

    // RewardApplied ok + refused.
    odyssey::protocol::v1::RewardApplied applied;
    applied.set_reason(odyssey::protocol::v1::REASON_OK);
    applied.set_equipment_id(12);
    odyssey::client::network::payload::RewardAppliedData applied_view;
    CHECK(DecodeRewardApplied(Serialize(applied), applied_view));
    CHECK(applied_view.ok);
    CHECK(applied_view.equipment_id == 12);

    odyssey::protocol::v1::RewardApplied refused;
    refused.set_reason(odyssey::protocol::v1::REASON_INVALID_STATE);
    refused.set_equipment_id(99);
    odyssey::client::network::payload::RewardAppliedData refused_view;
    CHECK(DecodeRewardApplied(Serialize(refused), refused_view));
    CHECK(!refused_view.ok);
    CHECK(refused_view.reason ==
          static_cast<std::uint32_t>(odyssey::protocol::v1::REASON_INVALID_STATE));
}

void TestResumeWire() {
    const std::vector<std::uint8_t> token = {0xDE, 0xAD, 0xBE, 0xEF};
    const auto bytes = EncodeResumeRequest(token, 1);
    odyssey::protocol::v1::ResumeRequest parsed;
    CHECK(parsed.ParseFromArray(bytes.data(), static_cast<int>(bytes.size())));
    CHECK(parsed.protocol_version() == 1);
    CHECK(parsed.resume_token().size() == token.size());
    CHECK(static_cast<std::uint8_t>(parsed.resume_token()[0]) == 0xDE);
    CHECK(static_cast<std::uint8_t>(parsed.resume_token()[3]) == 0xEF);

    odyssey::protocol::v1::ResumeResponse ok;
    ok.set_reason(odyssey::protocol::v1::REASON_OK);
    ok.set_session_id(77);
    ok.set_player_id(9);
    odyssey::client::network::payload::ResumeResponseData ok_view;
    CHECK(DecodeResumeResponse(Serialize(ok), ok_view));
    CHECK(ok_view.ok);
    CHECK(ok_view.session_id == 77);
    CHECK(ok_view.player_id == 9);

    odyssey::protocol::v1::ResumeResponse expired;
    expired.set_reason(odyssey::protocol::v1::REASON_RESUME_TOKEN_EXPIRED);
    expired.set_message("token expired");
    odyssey::client::network::payload::ResumeResponseData expired_view;
    CHECK(DecodeResumeResponse(Serialize(expired), expired_view));
    CHECK(!expired_view.ok);
    CHECK(expired_view.reason ==
          static_cast<std::uint32_t>(odyssey::protocol::v1::REASON_RESUME_TOKEN_EXPIRED));
    CHECK(expired_view.message == "token expired");
}

void TestDisconnectDecode() {
    odyssey::protocol::v1::Disconnect proto;
    proto.set_reason(odyssey::protocol::v1::REASON_EVENT_BACKPRESSURE);
    proto.set_message("event queue saturated");
    const auto bytes = Serialize(proto);

    odyssey::client::network::payload::DisconnectData disc;
    CHECK(DecodeDisconnect(bytes, disc));
    CHECK(disc.reason == static_cast<std::uint32_t>(odyssey::protocol::v1::REASON_EVENT_BACKPRESSURE));
    CHECK(disc.message == "event queue saturated");
}

}  // namespace

int main() {
    TestPingWireRoundTrip();
    TestPongDecodeFromServer();
    TestLoginRequestWire();
    TestLoginResponseOk();
    TestLoginResponseRejected();
    TestMatchmakingWire();
    TestPlayerInputWire();
    TestWorldSnapshotDecode();
    TestCombatEventsDecode();
    TestRewardWire();
    TestResumeWire();
    TestDisconnectDecode();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
