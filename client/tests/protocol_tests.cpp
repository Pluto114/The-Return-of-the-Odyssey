// Headless tests for the protocol payload codec (Ping/Pong, Login, Disconnect).
// Uses A's generated protobuf classes as the "other side" to prove encode and
// decode agree with the wire schema.
#include "network/PayloadCodec.h"

#include "common.pb.h"
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

using odyssey::client::network::payload::DecodeDisconnect;
using odyssey::client::network::payload::DecodeLoginResponse;
using odyssey::client::network::payload::DecodePong;
using odyssey::client::network::payload::DecodeWorldSnapshot;
using odyssey::client::network::payload::EncodeLoginRequest;
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

void TestPlayerInputWire() {
    odyssey::client::network::payload::PlayerInputData data;
    data.input_seq = 77;
    data.dir_x = 0.70710678f;
    data.dir_z = -0.70710678f;
    data.shoot = false;
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
    CHECK(!parsed.shoot());
    // Zero-intent (release) encodes fine with seq and no direction magnitude.
    odyssey::client::network::payload::PlayerInputData idle;
    idle.input_seq = 78;
    const auto idle_bytes = EncodePlayerInput(idle);
    odyssey::protocol::v1::PlayerInput idle_parsed;
    CHECK(idle_parsed.ParseFromArray(idle_bytes.data(), static_cast<int>(idle_bytes.size())));
    CHECK(idle_parsed.input_seq() == 78);
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
    self->set_alive(true);

    auto* other1 = proto.add_players();
    other1->set_player_id(1);
    other1->mutable_position()->set_x(5.0f);
    other1->mutable_position()->set_y(6.0f);
    auto* other2 = proto.add_players();
    other2->set_player_id(2);
    other2->mutable_position()->set_x(-1.0f);
    other2->mutable_position()->set_y(-2.0f);

    proto.add_monsters()->set_monster_id(900);
    const auto bytes = Serialize(proto);

    odyssey::client::network::payload::WorldSnapshotView view;
    CHECK(DecodeWorldSnapshot(bytes, view));
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
        CHECK(view.self.alive);
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
    CHECK(view.monster_count == 1);
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
    TestPlayerInputWire();
    TestWorldSnapshotDecode();
    TestDisconnectDecode();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
