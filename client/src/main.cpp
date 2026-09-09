// odyssey_client entry point - Phase 1 D1 slices A + B, now wired to A's v0
// protocol message IDs (see network/ProtocolIds.h). Slice C input/snapshot
// framework is in place; real PlayerInput/WorldSnapshot decode lands next.
#include "core/BoundedQueue.h"
#include "input/InputSampler.h"
#include "input/InputSample.h"
#include "network/NetClient.h"
#include "network/NetMessage.h"
#include "network/ProtocolIds.h"
#include "raylib.h"
#include "sync/GameView.h"

#include <cstdint>
#include <cstdio>
#include <string>

namespace {

constexpr int kScreenWidth = 960;
constexpr int kScreenHeight = 540;
constexpr int kFps = 60;

// Gameserver endpoint reserved in the infra docs; read from config later.
constexpr const char* kServerHost = "127.0.0.1";
constexpr std::uint16_t kServerPort = 7777;

constexpr float kPingIntervalSeconds = 1.0f;

using odyssey::client::core::BoundedQueue;
using odyssey::client::input::InputReport;
using odyssey::client::input::InputSample;
using odyssey::client::input::InputSampler;
using odyssey::client::input::InputSequencer;
using odyssey::client::network::ConnectionState;
using odyssey::client::network::NetClient;
using odyssey::client::network::NetEvent;
using odyssey::client::network::ToString;
using namespace odyssey::client::network::ids;
using odyssey::client::sync::GameView;

struct DemoState {
    ConnectionState state = ConnectionState::kIdle;
    std::string state_detail = "not started";
    std::uint32_t ping_sequence = 0;
    bool received_any = false;
    std::uint16_t last_type = 0;
    std::uint32_t last_sequence = 0;
    std::size_t last_payload_bytes = 0;
    int outbound_drops = 0;
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
    GameView game_view;           // filled once A's snapshot DTO is decoded

    client.SetEventCallback([&inbox](NetEvent&& event) { inbox.Push(std::move(event)); });
    client.Start(kServerHost, kServerPort);

    double last_ping_sent = 0.0;

    while (!WindowShouldClose()) {
        if (IsKeyPressed(KEY_ESCAPE)) {
            break;
        }
        if (IsKeyPressed(KEY_R)) {
            // Retry after a failed/disconnected connect attempt.
            demo.state = ConnectionState::kIdle;
            demo.state_detail = "retrying";
            client.Connect(kServerHost, kServerPort);
        }

        // Sample input at a fixed 30Hz while connected. Sequencing/payload
        // transmission is gated by room/session state once A's PlayerInput
        // lands; for now the HUD shows the intent and tick count.
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
                    break;
                case NetEvent::Kind::kMessage:
                    demo.received_any = true;
                    demo.last_type = event->message.message_type;
                    demo.last_sequence = event->message.sequence;
                    demo.last_payload_bytes = event->message.payload.size();
                    break;
                case NetEvent::Kind::kOutboundDropped:
                    ++demo.outbound_drops;
                    break;
            }
        }

        // While connected, send a Ping every second using A's authoritative
        // message ID. Payload is empty for now; Login/PlayerInput/Snapshot
        // decode lands in the next slices.
        if (demo.state == ConnectionState::kConnected) {
            const double now = GetTime();
            if (now - last_ping_sent >= kPingIntervalSeconds) {
                last_ping_sent = now;
                ++demo.ping_sequence;
                client.SendFrame(kPing, demo.ping_sequence, nullptr, 0);
            }
        }

        BeginDrawing();
        ClearBackground(RAYWHITE);

        DrawText("The Return of the Odyssey", 24, 24, 32, DARKGRAY);
        DrawText("Phase 1 D1 - client window + network thread", 24, 64, 20, GRAY);

        const std::string state_line =
            std::string("Connection: ") + ToString(demo.state) + "  (" + demo.state_detail + ")";
        DrawText(state_line.c_str(), 24, 120, 20, demo.state == ConnectionState::kConnected ? DARKGREEN : DARKGRAY);

        if (demo.received_any) {
            const std::string msg = "Inbound: type=" + std::to_string(demo.last_type) +
                                    " seq=" + std::to_string(demo.last_sequence) +
                                    " payload_bytes=" + std::to_string(demo.last_payload_bytes);
            DrawText(msg.c_str(), 24, 160, 20, GRAY);
        } else {
            DrawText("Inbound: (none yet)", 24, 160, 20, GRAY);
        }
        DrawText(("Pings sent: " + std::to_string(demo.ping_sequence)).c_str(), 24, 200, 20, GRAY);
        DrawText(("Outbound queue drops: " + std::to_string(demo.outbound_drops)).c_str(), 24, 240, 20, GRAY);

        const std::string input_line =
            "Input intent: keys(dx=" + std::to_string(last_sample.dx) +
            ", dz=" + std::to_string(last_sample.dz) + ") vec(" +
            std::to_string(last_report.vector.x) + ", " + std::to_string(last_report.vector.z) +
            ") seq=" + std::to_string(last_report.sequence) + " @30Hz";
        DrawText(input_line.c_str(), 24, 280, 20, GRAY);

        const std::string view_line =
            "View: players=" + std::to_string(game_view.PlayerCount()) +
            " room=" + std::to_string(game_view.RoomId()) +
            " tick=" + std::to_string(game_view.ServerTick()) + " (awaiting protocol)";
        DrawText(view_line.c_str(), 24, 320, 20, GRAY);

        DrawText("R: retry connect   |   ESC / close window: quit", 24, kScreenHeight - 60, 20, LIGHTGRAY);
        DrawFPS(kScreenWidth - 90, 12);

        EndDrawing();
    }

    // Always stop the Network Thread before tearing the process down.
    client.Stop();
    CloseWindow();
    return 0;
}
