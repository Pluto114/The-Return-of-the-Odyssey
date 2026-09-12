// odyssey_client entry point - Phase 1 D1/D2 wiring to A's v0 protocol.
//
// Flow: connect -> auto LoginRequest(dev) -> show Session/Player ID ->
// 1Hz Ping with real payload (Pong echo displayed) -> auto matchmaking ->
// 30Hz PlayerInput -> authoritative WorldSnapshot. ESC / close
// stops the Network Thread cleanly. 'R' retries a failed connect.
#include "core/BoundedQueue.h"
#include "input/InputSample.h"
#include "input/InputSampler.h"
#include "network/NetClient.h"
#include "network/NetMessage.h"
#include "network/PayloadCodec.h"
#include "network/ProtocolIds.h"
#include "raylib.h"
#include "sync/CombatView.h"
#include "sync/GameView.h"

#include <cmath>
#include <cstdint>
#include <cstdio>
#include <string>
#include <vector>

namespace {

constexpr int kScreenWidth = 960;
constexpr int kScreenHeight = 540;
constexpr int kFps = 60;

// World [0,20]^2 arena mapped into this screen rectangle (shared by the aim
// inverse mapping and the drawing code).
constexpr float kWorldSize = 20.0f;
constexpr float kArenaX = 560.0f;
constexpr float kArenaY = 190.0f;
constexpr float kArenaW = 340.0f;
constexpr float kArenaH = 280.0f;

// Gameserver endpoint reserved in the infra docs; read from config later.
constexpr const char* kServerHost = "10.22.31.251";
constexpr std::uint16_t kServerPort = 7777;

// Development-mode login (Phase 1 has no real auth; server assigns identity).
constexpr const char* kDevToken = "dev";
constexpr const char* kDevDisplayName = "odyssey-c";
constexpr std::uint32_t kClientProtocolVersion = 1;

constexpr float kPingIntervalSeconds = 1.0f;
constexpr double kFrameSeconds = 1.0 / 60.0;

using namespace odyssey::client::network::ids;
namespace payload = odyssey::client::network::payload;
using odyssey::client::core::BoundedQueue;
using odyssey::client::input::InputReport;
using odyssey::client::input::InputSample;
using odyssey::client::input::InputSampler;
using odyssey::client::input::InputSequencer;
using odyssey::client::input::NormalizeInput;
using odyssey::client::network::ConnectionState;
using odyssey::client::network::NetClient;
using odyssey::client::network::NetEvent;
using odyssey::client::network::ToString;
using odyssey::client::network::payload::DamageEventData;
using odyssey::client::network::payload::DeathEventData;
using odyssey::client::network::payload::DisconnectData;
using odyssey::client::network::payload::LoginRequestData;
using odyssey::client::network::payload::LoginResponseData;
using odyssey::client::network::payload::MatchFoundData;
using odyssey::client::network::payload::PingData;
using odyssey::client::network::payload::PlayerInputData;
using odyssey::client::network::payload::PongData;
using odyssey::client::network::payload::ProjectileDestroyData;
using odyssey::client::network::payload::ProjectileSpawnData;
using odyssey::client::network::payload::StageEventData;
using odyssey::client::network::payload::SnapshotPlayerView;
using odyssey::client::network::payload::WorldSnapshotView;
using odyssey::client::sync::CombatView;
using odyssey::client::sync::GameView;
using odyssey::client::sync::MonsterEntity;
using odyssey::client::sync::ProjectileVisual;
using odyssey::client::sync::StageInfo;

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

    // Matchmaking / room binding.
    bool match_sent = false;
    bool in_room = false;
    std::uint64_t room_id = 0;
    std::string match_note = "not sent";

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

    // WorldSnapshot ingestion stats.
    std::uint64_t snapshots_received = 0;

    // Combat (D4) state.
    float self_hp = 0.0f;
    float self_max_hp = 0.0f;
    std::uint32_t stage_index = 0;
    std::uint32_t stage_state = 0;
    std::uint32_t monsters_remaining = 0;
    std::uint32_t spawns = 0;
    std::uint32_t destroys = 0;
    std::uint32_t damages = 0;
    std::uint32_t deaths = 0;
    std::string last_event_note = "(none)";
};

}  // namespace

int main() {
    std::printf("main: before InitWindow\n"); fflush(stdout);
    InitWindow(kScreenWidth, kScreenHeight, "The Return of the Odyssey - Client");
    std::printf("main: after InitWindow\n"); fflush(stdout);
    SetTargetFPS(kFps);

    BoundedQueue<NetEvent> inbox(256);
    NetClient client;
    DemoState demo;

    InputSampler input_sampler;
    InputSequencer input_sequencer;
    InputSample last_sample;
    InputReport last_report;      // normalized vector + sequence (connected ticks)
    float last_aim_x = 1.0f;      // aim heading sent to the server (for the HUD)
    float last_aim_z = 0.0f;
    bool last_shoot = false;
    double last_input_time = 0.0;
    GameView game_view;           // players from authoritative snapshots
    CombatView combat_view;       // monsters (snapshot) + projectiles (events)

    client.SetEventCallback([&inbox](NetEvent&& event) { inbox.Push(std::move(event)); });
    std::printf("main: starting net thread\n"); fflush(stdout);
    client.Start(kServerHost, kServerPort);
    std::printf("main: net thread started, entering loop\n"); fflush(stdout);

    double last_ping_sent = 0.0;
    int frame_counter = 0;
    const double t_start = GetTime();

    auto SendPayload = [&client, &demo](std::uint16_t message_type,
                                        const std::vector<std::uint8_t>& payload) {
        client.SendFrame(message_type, ++demo.frame_seq, payload.data(), payload.size());
    };

    while (true) {
        // Drive the platform message pump explicitly: in this raylib build
        // EndDrawing()/WindowShouldClose() do NOT dispatch Win32 messages on
        // their own (the window would be "Not Responding"). Poll once per
        // frame, then honour the close flag.
        PollInputEvents();
        if (WindowShouldClose()) {
            std::printf("main: window close requested\n"); fflush(stdout);
            break;
        }
        const double frame_start = GetTime();
        if ((frame_counter % 120) == 0) {
            std::printf("main: frame %d state=%s elapsed=%.1fs fps=%d\n", frame_counter,
                        ToString(demo.state), GetTime() - t_start, GetFPS());
            fflush(stdout);
        }
        ++frame_counter;
        if (IsKeyPressed(KEY_ESCAPE)) {
            std::printf("main: ESC pressed, exiting loop\n"); fflush(stdout);
            break;
        }
        if (IsKeyPressed(KEY_R)) {
            // Retry after a failed/disconnected connect attempt.
            demo.state = ConnectionState::kIdle;
            demo.state_detail = "retrying";
            demo.login_sent = false;
            demo.login_ok = false;
            demo.login_note = "not sent";
            demo.match_sent = false;
            demo.in_room = false;
            demo.room_id = 0;
            demo.match_note = "not sent";
            demo.server_note.clear();
            input_sequencer.Reset();
            game_view = GameView{};
            combat_view.Clear();
            client.Connect(kServerHost, kServerPort);
        }

        // Sample and transmit intent at a fixed 30Hz after MatchFound. The
        // client sends direction only; position always comes from snapshots.
        if (demo.state == ConnectionState::kConnected) {
            const double now = GetTime();
            if (now - last_input_time >= 1.0 / 30.0) {
                last_input_time = now;
                last_sample = input_sampler.SampleNow();
                if (demo.in_room) {
                    last_report = input_sequencer.Tick(last_sample);
                    // Aim heading: mouse position mapped back to world space,
                    // relative to our own authoritative position. The client
                    // never sends positions or hit results.
                    const Vector2 mouse = GetMousePosition();
                    const float mx = (mouse.x - kArenaX) / kArenaW * kWorldSize;
                    const float mz = (mouse.y - kArenaY) / kArenaH * kWorldSize;
                    float aim_x = 1.0f;
                    float aim_z = 0.0f;
                    if (const auto* self = game_view.Find(demo.player_id)) {
                        aim_x = mx - self->x;
                        aim_z = mz - self->z;
                        const float length = std::sqrt(aim_x * aim_x + aim_z * aim_z);
                        if (length > 1e-4f) {
                            aim_x /= length;
                            aim_z /= length;
                        } else {
                            aim_x = 1.0f;
                            aim_z = 0.0f;
                        }
                    }
                    PlayerInputData input;
                    input.input_seq = last_report.sequence;
                    input.dir_x = last_report.vector.x;
                    input.dir_z = last_report.vector.z;
                    input.aim_x = aim_x;
                    input.aim_z = aim_z;
                    input.shoot = IsKeyDown(KEY_SPACE);
                    input.client_tick_ms = static_cast<std::uint64_t>(now * 1000.0);
                    last_aim_x = aim_x;
                    last_aim_z = aim_z;
                    last_shoot = input.shoot;
                    SendPayload(kPlayerInput, payload::EncodePlayerInput(input));
                } else {
                    last_report.sequence = 0;
                    last_report.vector = NormalizeInput(last_sample);
                }
            }
        }

        // Drain the Network -> Main inbox (Main Thread consumes events only).
        while (auto event = inbox.TryPop()) {
            switch (event->kind) {
                case NetEvent::Kind::kStateChanged:
                    demo.state = event->state;
                    demo.state_detail = event->detail;
                    std::printf("main: net state -> %s (%s)\n", ToString(demo.state),
                                demo.state_detail.c_str());
                    fflush(stdout);
                    if (demo.state != ConnectionState::kConnected) {
                        // Fresh session on every reconnect; never reuse identity.
                        demo.login_sent = false;
                        demo.login_ok = false;
                        demo.session_id = 0;
                        demo.player_id = 0;
                        demo.login_note = "not sent";
                        demo.match_sent = false;
                        demo.in_room = false;
                        demo.room_id = 0;
                        demo.match_note = "not sent";
                        input_sequencer.Reset();
                        game_view = GameView{};
                        combat_view.Clear();
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
                    } else if (event->message.message_type == kMatchFound) {
                        MatchFoundData match;
                        if (payload::DecodeMatchFound(event->message.payload, match)) {
                            demo.in_room = true;
                            demo.room_id = match.room_id;
                            demo.match_note = "room=" + std::to_string(match.room_id);
                            std::printf("main: match ready room=%llu teammates=%zu\n",
                                        static_cast<unsigned long long>(match.room_id),
                                        match.teammates.size());
                            std::fflush(stdout);
                        } else {
                            demo.match_note = "MatchFound decode failed";
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
                    } else if (event->message.message_type == kWorldSnapshot) {
                        // Authoritative full snapshot: replace the whole view.
                        WorldSnapshotView snap;
                        if (payload::DecodeWorldSnapshot(event->message.payload, snap)) {
                            ++demo.snapshots_received;
                            if (demo.snapshots_received == 1) {
                                std::printf("main: first world snapshot tick=%llu\n",
                                            static_cast<unsigned long long>(snap.server_tick));
                                std::fflush(stdout);
                            }
                            odyssey::client::sync::SnapshotView sv;
                            sv.server_tick = snap.server_tick;
                            sv.room_id = demo.room_id;
                            sv.closed = false;
                            const auto add = [&sv, &snap](const SnapshotPlayerView& p) {
                                odyssey::client::sync::PlayerView v;
                                v.id = p.id;
                                v.x = p.pos_x;
                                v.z = p.pos_z;
                                v.vx = p.vel_x;
                                v.vz = p.vel_z;
                                if (snap.has_self && p.id == snap.self.id) {
                                    v.last_processed_input_seq = snap.last_processed_input;
                                }
                                sv.players.push_back(v);
                            };
                            if (snap.has_self) {
                                add(snap.self);
                            }
                            for (const auto& other : snap.others) {
                                add(other);
                            }
                            game_view.Apply(sv);

                            // Monsters are a FULL set from the snapshot: a
                            // monster missing from the newest one is removed.
                            std::vector<MonsterEntity> monsters;
                            monsters.reserve(snap.monsters.size());
                            for (const auto& m : snap.monsters) {
                                MonsterEntity entity;
                                entity.id = m.id;
                                entity.x = m.pos_x;
                                entity.z = m.pos_z;
                                entity.vx = m.vel_x;
                                entity.vz = m.vel_z;
                                entity.hp = m.hp;
                                entity.max_hp = m.max_hp;
                                entity.state = m.state;
                                monsters.push_back(entity);
                            }
                            combat_view.ApplyMonsters(monsters);

                            StageInfo stage;
                            stage.index = snap.stage.index;
                            stage.seed = snap.stage.seed;
                            stage.state = snap.stage.state;
                            stage.monsters_remaining = snap.stage.monsters_remaining;
                            combat_view.SetStage(stage);
                            demo.stage_index = snap.stage.index;
                            demo.stage_state = snap.stage.state;
                            demo.monsters_remaining = snap.stage.monsters_remaining;
                            if (snap.has_self) {
                                demo.self_hp = snap.self.hp;
                                demo.self_max_hp = snap.self.max_hp;
                            }
                        }
                    } else if (event->message.message_type == kProjectileSpawn) {
                        ProjectileSpawnData spawn;
                        if (payload::DecodeProjectileSpawn(event->message.payload, spawn)) {
                            ProjectileVisual projectile;
                            projectile.id = spawn.projectile_id;
                            projectile.owner_id = spawn.owner_id;
                            projectile.x = spawn.pos_x;
                            projectile.z = spawn.pos_z;
                            projectile.vx = spawn.vel_x;
                            projectile.vz = spawn.vel_z;
                            projectile.expires_at_tick = spawn.expires_at_tick;
                            projectile.server_tick = spawn.server_tick;
                            combat_view.SpawnProjectile(projectile);
                            ++demo.spawns;
                            demo.last_event_note = "projectile spawn id=" +
                                                   std::to_string(spawn.projectile_id);
                        }
                    } else if (event->message.message_type == kProjectileDestroy) {
                        ProjectileDestroyData destroy;
                        if (payload::DecodeProjectileDestroy(event->message.payload, destroy)) {
                            combat_view.DestroyProjectile(destroy.projectile_id);
                            ++demo.destroys;
                            demo.last_event_note = "projectile destroy id=" +
                                                   std::to_string(destroy.projectile_id);
                        }
                    } else if (event->message.message_type == kDamageEvent) {
                        DamageEventData damage;
                        if (payload::DecodeDamageEvent(event->message.payload, damage)) {
                            ++demo.damages;
                            demo.last_event_note = "damage target=" +
                                                   std::to_string(damage.target_id) + " amount=" +
                                                   std::to_string(damage.amount) + " hp=" +
                                                   std::to_string(damage.remaining_health);
                        }
                    } else if (event->message.message_type == kDeathEvent) {
                        DeathEventData death;
                        if (payload::DecodeDeathEvent(event->message.payload, death)) {
                            ++demo.deaths;
                            demo.last_event_note = "death entity=" +
                                                   std::to_string(death.entity_id) + " killer=" +
                                                   std::to_string(death.killer_id);
                        }
                    } else if (event->message.message_type == kStageStartedEvent ||
                               event->message.message_type == kStageClearedEvent ||
                               event->message.message_type == kTeamDefeatedEvent) {
                        StageEventData stage_event;
                        if (payload::DecodeStageEvent(event->message.payload, stage_event)) {
                            const char* kind = event->message.message_type == kStageStartedEvent
                                                   ? "stage started"
                                                   : (event->message.message_type == kStageClearedEvent
                                                          ? "stage cleared"
                                                          : "team defeated");
                            demo.last_event_note = std::string(kind) + " index=" +
                                                   std::to_string(stage_event.stage_index);
                            std::printf("main: %s stage=%u tick=%llu\n", kind,
                                        stage_event.stage_index,
                                        static_cast<unsigned long long>(stage_event.server_tick));
                            std::fflush(stdout);
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

            if (demo.login_ok && !demo.match_sent) {
                demo.match_sent = true;
                demo.match_note = "queued";
                SendPayload(kMatchRequest, payload::EncodeMatchRequest());
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
        DrawText("Phase 1 - authoritative two-player movement", 24, 64, 20, GRAY);

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

        DrawText(("Match: " + demo.match_note).c_str(), 520, 130, 20,
                 demo.in_room ? DARKGREEN : GRAY);

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
            " tick=" + std::to_string(game_view.ServerTick()) +
            " snaps=" + std::to_string(demo.snapshots_received);
        DrawText(view_line.c_str(), 24, 310, 20, GRAY);

        const std::string combat_line =
            "Stage: idx=" + std::to_string(demo.stage_index) +
            " state=" + std::to_string(demo.stage_state) +
            " remain=" + std::to_string(demo.monsters_remaining) +
            " | monsters=" + std::to_string(combat_view.MonsterCount()) +
            " bullets=" + std::to_string(combat_view.ProjectileCount());
        DrawText(combat_line.c_str(), 24, 340, 20, GRAY);

        const std::string hp_line =
            "HP self=" + std::to_string(static_cast<int>(demo.self_hp)) + "/" +
            std::to_string(static_cast<int>(demo.self_max_hp)) +
            "  shoot=" + std::string(last_shoot ? "yes" : "no") +
            "  events sp/dst/dmg/dth=" + std::to_string(demo.spawns) + "/" +
            std::to_string(demo.destroys) + "/" + std::to_string(demo.damages) + "/" +
            std::to_string(demo.deaths);
        DrawText(hp_line.c_str(), 24, 370, 20, GRAY);
        DrawText(("Last event: " + demo.last_event_note).c_str(), 24, 400, 20, MAROON);

        // Arena: world [0,20]^2. Self blue, peers red, monsters orange,
        // projectiles gold. Projectiles exist only via spawn/destroy events.
        DrawRectangleLines(static_cast<int>(kArenaX), static_cast<int>(kArenaY),
                           static_cast<int>(kArenaW), static_cast<int>(kArenaH), LIGHTGRAY);
        const auto to_screen_x = [](float wx) { return kArenaX + (wx / kWorldSize) * kArenaW; };
        const auto to_screen_y = [](float wz) { return kArenaY + (wz / kWorldSize) * kArenaH; };

        for (const auto& [id, projectile] : combat_view.Projectiles()) {
            (void)id;
            DrawCircleV(Vector2{to_screen_x(projectile.x), to_screen_y(projectile.z)}, 3.0f, GOLD);
        }

        for (const auto& [id, monster] : combat_view.Monsters()) {
            const float sx = to_screen_x(monster.x);
            const float sy = to_screen_y(monster.z);
            DrawRectangle(static_cast<int>(sx) - 7, static_cast<int>(sy) - 7, 14, 14, ORANGE);
            const float ratio = monster.max_hp > 0.0f ? (monster.hp / monster.max_hp) : 0.0f;
            DrawRectangle(static_cast<int>(sx) - 10, static_cast<int>(sy) - 16, 20, 4, Fade(RED, 0.25f));
            DrawRectangle(static_cast<int>(sx) - 10, static_cast<int>(sy) - 16,
                          static_cast<int>(20.0f * ratio), 4, LIME);
            DrawText(std::to_string(id).c_str(), static_cast<int>(sx) + 9,
                     static_cast<int>(sy) - 8, 12, DARKGRAY);
        }

        for (const auto& player : game_view.Players()) {
            const float px = to_screen_x(player.x);
            const float py = to_screen_y(player.z);
            const bool is_self = (player.id == demo.player_id);
            DrawCircleV(Vector2{px, py}, 9.0f, is_self ? BLUE : RED);
            if (is_self) {
                // Aim heading we are sending to the server.
                DrawLineV(Vector2{px, py},
                          Vector2{px + last_aim_x * 26.0f, py + last_aim_z * 26.0f}, DARKBLUE);
            }
            DrawText(std::to_string(player.id).c_str(), static_cast<int>(px + 12),
                     static_cast<int>(py - 8), 16, DARKGRAY);
        }

        DrawText("WASD move | mouse aim | SPACE shoot | R retry | ESC quit", 24, kScreenHeight - 60, 20, LIGHTGRAY);
        DrawFPS(kScreenWidth - 90, 12);

        EndDrawing();
        // This raylib build enables SUPPORT_CUSTOM_FRAME_CONTROL: EndDrawing
        // flushes drawing commands, but presenting the frame is our job.
        SwapScreenBuffer();

        // Manual frame pacing fallback: hold each frame to ~1/60s even when
        // raylib's built-in timing is not applied by the linked build.
        const double frame_elapsed = GetTime() - frame_start;
        if (frame_elapsed < kFrameSeconds) {
            WaitTime(kFrameSeconds - frame_elapsed);
        }
    }

    // Always stop the Network Thread before tearing the process down.
    std::printf("main: loop exited, stopping net thread\n"); fflush(stdout);
    client.Stop();
    std::printf("main: net stopped, closing window\n"); fflush(stdout);
    CloseWindow();
    std::printf("main: exit\n"); fflush(stdout);
    return 0;
}
