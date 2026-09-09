// odyssey_client entry point - Phase 1 D1/D2 wiring to A's v0 protocol.
//
// Flow: connect -> auto LoginRequest(dev) -> show Session/Player ID ->
// 1Hz Ping with real payload (Pong echo displayed) -> 30Hz input intent
// (transmission of PlayerInput lands with the snapshot slice). ESC / close
// stops the Network Thread cleanly. 'R' retries a failed connect.
#include "core/BoundedQueue.h"
#include "input/InputSample.h"
#include "input/InputSampler.h"
#include "network/NetClient.h"
#include "network/NetMessage.h"
#include "network/PayloadCodec.h"
#include "network/ProtocolIds.h"
#include "raylib.h"
#include "sync/GameView.h"

#include <cstdint>
#include <cstdio>
#include <string>
#include <vector>

namespace {

constexpr int kScreenWidth = 960;
constexpr int kScreenHeight = 540;
constexpr int kFps = 60;

// Gameserver endpoint reserved in the infra docs; read from config later.
constexpr const char* kServerHost = "127.0.0.1";
constexpr std::uint16_t kServerPort = 7777;

// Development-mode login (Phase 1 has no real auth; server assigns identity).
constexpr const char* kDevToken = "dev";
constexpr const char* kDevDisplayName = "odyssey-c";
constexpr std::uint32_t kClientProtocolVersion = 1;

constexpr float kPingIntervalSeconds = 1.0f;

using namespace odyssey::client::network::ids;
namespace payload = odyssey::client::network::payload;
using odyssey::client::core::BoundedQueue;
using odyssey::client::input::InputReport;
using odyssey::client::input::InputSample;
using odyssey::client::input::InputSampler;
using odyssey::client::input::InputSequencer;
using odyssey::client::network::ConnectionState;
using odyssey::client::network::NetClient;
using odyssey::client::network::NetEvent;
using odyssey::client::network::ToString;
using odyssey::client::network::payload::DisconnectData;
using odyssey::client::network::payload::LoginRequestData;
using odyssey::client::network::payload::LoginResponseData;
using odyssey::client::network::payload::PingData;
using odyssey::client::network::payload::PongData;
using odyssey::client::sync::GameView;

struct DemoState {
    ConnectionState state = ConnectionState::kIdle;
    std::string state_detail = "not started";

    // Frame Sequence for outbound frames (independent from InputSeq).
    std::uint32_t frame_seq = 0;

    // Login handshake.
    bool login_sent = false;
    bool login_ok = false;
    std::uint64_t session_id = 0;
    std::uint64_t player_id = 0;
    std::string login_note = "not sent";

    // Heartbeat.
    std::uint32_t pings_sent = 0;
    std::uint64_t ping_nonce = 0;
    std::uint64_t pong_server_time_ms = 0;
    std::uint64_t pong_nonce = 0;

    // Last inbound message (generic HUD line).
    bool received_any = false;
    std::uint16_t last_type = 0;
    std::uint32_t last_sequence = 0;
    std::size_t last_payload_bytes = 0;

    int outbound_drops = 0;

    // Server-pushed note (Disconnect reason etc.).
    std::string server_note;
};

}  // namespace

int main() {
    InitWindow(kScreenWidth, kScreenHeight, "The Return of the Odyssey - Client");
    SetTargetFPS(kFps);

    BoundedQueue<NetEvent> inbox(256);
    NetClient client;
    DemoState demo;

    InputSampler input_sampler;
    InputSequencer input_sequencer;
    InputSample last_sample;
    InputReport last_report;      // normalized vector + sequence (connected ticks)
    double last_input_time = 0.0;
    GameView game_view;           // filled once the WorldSnapshot decode slice lands

    client.SetEventCallback([&inbox](NetEvent&& event) { inbox.Push(std::move(event)); });
    client.Start(kServerHost, kServerPort);

    double last_ping_sent = 0.0;

    auto SendPayload = [&client, &demo](std::uint16_t message_type,
                                        const std::vector<std::uint8_t>& payload) {
        client.SendFrame(message_type, ++demo.frame_seq, payload.data(), payload.size());
    };

    while (!WindowShouldClose()) {
        if (IsKeyPressed(KEY_ESCAPE)) {
            break;
        }
        if (IsKeyPressed(KEY_R)) {
            // Retry after a failed/disconnected connect attempt.
            demo.state = ConnectionState::kIdle;
            demo.state_detail = "retrying";
            demo.login_sent = false;
            demo.login_ok = false;
            demo.login_note = "not sent";
            demo.server_note.clear();
            client.Connect(kServerHost, kServerPort);
        }

        // Sample input at a fixed 30Hz while connected. Wire transmission is
        // gated on login (and later room join) once PlayerInput lands.
        if (demo.state == ConnectionState::kConnected) {
            const double now = GetTime();
            if (now - last_input_time >= 1.0 / 30.0) {
                last_input_time = now;
                last_sample = input_sampler.SampleNow();
                last_report = input_sequencer.Tick(last_sample);
            }
        }

        // Drain the Network -> Main inbox (Main Thread consumes events only).
        while (auto event = inbox.TryPop()) {
            switch (event->kind) {
                case NetEvent::Kind::kStateChanged:
                    demo.state = event->state;
                    demo.state_detail = event->detail;
                    if (demo.state != ConnectionState::kConnected) {
                        // Fresh session on every reconnect; never reuse identity.
                        demo.login_sent = false;
                        demo.login_ok = false;
                        demo.session_id = 0;
                        demo.player_id = 0;
                        demo.login_note = "not sent";
                    }
                    break;
                case NetEvent::Kind::kMessage:
                    demo.received_any = true;
                    demo.last_type = event->message.message_type;
                    demo.last_sequence = event->message.sequence;
                    demo.last_payload_bytes = event->message.payload.size();
                    if (event->message.message_type == kLoginResponse) {
                        LoginResponseData login;
                        if (payload::DecodeLoginResponse(event->message.payload, login)) {
                            demo.login_ok = login.ok;
                            demo.session_id = login.session_id;
                            demo.player_id = login.player_id;
                            demo.login_note = login.ok
                                                  ? "ok"
                                                  : ("reason=" + std::to_string(login.reason) +
                                                     " " + login.message);
                        } else {
                            demo.login_ok = false;
                            demo.login_note = "LoginResponse decode failed";
                        }
                    } else if (event->message.message_type == kPong) {
                        PongData pong;
                        if (payload::DecodePong(event->message.payload, pong)) {
                            demo.pong_server_time_ms = pong.server_time_ms;
                            demo.pong_nonce = pong.nonce;
                        }
                    } else if (event->message.message_type == kDisconnect) {
                        DisconnectData disc;
                        if (payload::DecodeDisconnect(event->message.payload, disc)) {
                            demo.server_note = "server disconnect: reason=" +
                                               std::to_string(disc.reason) + " " + disc.message;
                        } else {
                            demo.server_note = "server disconnect (payload decode failed)";
                        }
                    }
                    break;
                case NetEvent::Kind::kOutboundDropped:
                    ++demo.outbound_drops;
                    break;
            }
        }

        if (demo.state == ConnectionState::kConnected) {
            const double now = GetTime();

            // Auto-login once per connection (dev mode: token accepted as-is).
            if (!demo.login_sent) {
                demo.login_sent = true;
                demo.login_note = "sent, awaiting response";
                LoginRequestData login;
                login.protocol_version = kClientProtocolVersion;
                login.token = kDevToken;
                login.display_name = kDevDisplayName;
                SendPayload(kLoginRequest, payload::EncodeLoginRequest(login));
            }

            // Heartbeat with a real Ping payload; Pong echoes nonce back.
            if (now - last_ping_sent >= kPingIntervalSeconds) {
                last_ping_sent = now;
                PingData ping;
                ping.client_time_ms = static_cast<std::uint64_t>(now * 1000.0);
                ping.nonce = ++demo.ping_nonce;
                ++demo.pings_sent;
                SendPayload(kPing, payload::EncodePing(ping));
            }
        }

        BeginDrawing();
        ClearBackground(RAYWHITE);

        DrawText("The Return of the Odyssey", 24, 24, 32, DARKGRAY);
        DrawText("Phase 1 - client + A's v0 protocol (login slice)", 24, 64, 20, GRAY);

        const std::string state_line =
            std::string("Connection: ") + ToString(demo.state) + "  (" + demo.state_detail + ")";
        DrawText(state_line.c_str(), 24, 100, 20,
                 demo.state == ConnectionState::kConnected ? DARKGREEN : DARKGRAY);

        const std::string login_line =
            "Login: " + demo.login_note +
            (demo.login_ok ? ("  session=" + std::to_string(demo.session_id) +
                              " player=" + std::to_string(demo.player_id))
                           : "");
        DrawText(login_line.c_str(), 24, 130, 20, demo.login_ok ? DARKGREEN : GRAY);

        if (demo.received_any) {
            const std::string msg = "Inbound: type=" + std::to_string(demo.last_type) +
                                    " seq=" + std::to_string(demo.last_sequence) +
                                    " bytes=" + std::to_string(demo.last_payload_bytes);
            DrawText(msg.c_str(), 24, 160, 20, GRAY);
        } else {
            DrawText("Inbound: (none yet)", 24, 160, 20, GRAY);
        }

        const std::string hb_line =
            "Ping sent: " + std::to_string(demo.pings_sent) +
            "   Pong: nonce=" + std::to_string(demo.pong_nonce) +
            " server_time_ms=" + std::to_string(demo.pong_server_time_ms);
        DrawText(hb_line.c_str(), 24, 190, 20, GRAY);
        DrawText(("Outbound drops: " + std::to_string(demo.outbound_drops)).c_str(), 24, 220, 20, GRAY);
        if (!demo.server_note.empty()) {
            DrawText(demo.server_note.c_str(), 24, 250, 20, MAROON);
        }

        const std::string input_line =
            "Input intent: keys(dx=" + std::to_string(last_sample.dx) +
            ", dz=" + std::to_string(last_sample.dz) + ") vec(" +
            std::to_string(last_report.vector.x) + ", " + std::to_string(last_report.vector.z) +
            ") seq=" + std::to_string(last_report.sequence) + " @30Hz";
        DrawText(input_line.c_str(), 24, 280, 20, GRAY);

        const std::string view_line =
            "View: players=" + std::to_string(game_view.PlayerCount()) +
            " room=" + std::to_string(game_view.RoomId()) +
            " tick=" + std::to_string(game_view.ServerTick()) + " (awaiting snapshot slice)";
        DrawText(view_line.c_str(), 24, 310, 20, GRAY);

        DrawText("R: retry connect   |   ESC / close window: quit", 24, kScreenHeight - 60, 20, LIGHTGRAY);
        DrawFPS(kScreenWidth - 90, 12);

        EndDrawing();
    }

    // Always stop the Network Thread before tearing the process down.
    client.Stop();
    CloseWindow();
    return 0;
}
