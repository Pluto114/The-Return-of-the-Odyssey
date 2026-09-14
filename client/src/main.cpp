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
#include "sync/Interpolation.h"
#include "sync/Prediction.h"
#include "sync/RecoveryState.h"
#include "sync/RewardView.h"

#include <cmath>
#include <cstdint>
#include <cstdio>
#include <fstream>
#include <map>
#include <sstream>
#include <string>
#include <utility>
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

const char* StageStateName(std::uint32_t state) {
    switch (state) {
        case 0: return "waiting";
        case 1: return "playing";
        case 2: return "clear";
        case 3: return "reward";
        case 4: return "preparing";
        case 5: return "failed";
        case 6: return "closed";
        default: return "?";
    }
}

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
using odyssey::client::network::payload::RewardAppliedData;
using odyssey::client::network::payload::RewardOptionsData;
using odyssey::client::network::payload::ResumeResponseData;
using odyssey::client::network::payload::StageEventData;
using odyssey::client::network::payload::SnapshotPlayerView;
using odyssey::client::network::payload::WorldSnapshotView;
using odyssey::client::sync::CombatView;
using odyssey::client::sync::EquipmentTable;
using odyssey::client::sync::GameView;
using odyssey::client::sync::InputCommand;
using odyssey::client::sync::MonsterEntity;
using odyssey::client::sync::MovementPredictor;
using odyssey::client::sync::ProjectileVisual;
using odyssey::client::sync::RecoveryPhase;
using odyssey::client::sync::RecoveryState;
using odyssey::client::sync::RewardState;
using odyssey::client::sync::RewardView;
using odyssey::client::sync::SnapshotInterpolator;
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
    std::vector<std::uint8_t> resume_token;  // from LoginResponse; enables resume
    bool resumed = false;                    // this session was re-attached

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
    std::uint32_t prev_stage_index = 0;
    std::string banner;       // transient stage outcome message
    float banner_ttl = 0.0f;
    std::uint32_t spawns = 0;
    std::uint32_t destroys = 0;
    std::uint32_t damages = 0;
    std::uint32_t deaths = 0;
    std::string last_event_note = "(none)";

    // Self base stats from the newest snapshot (reward effects show up here).
    float self_attack = 0.0f;
    float self_defense = 0.0f;
    float self_move_speed = 0.0f;

    // D7 readiness (client-side echo; the server owns the ready barrier).
    bool ready_sent = false;
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
    RewardView reward_view;       // treasure chest options / choice state
    RecoveryState recovery;       // reconnect/resume state machine (D8)
    MovementPredictor predictor;  // local prediction + reconciliation (D9)
    SnapshotInterpolator remote_interp;   // other players (10Hz -> smooth)
    SnapshotInterpolator monster_interp;  // monsters (10Hz -> smooth)
    EquipmentTable equipment_table;

    // This table is generated from data/equipment/catalog.json by CMake and
    // copied beside the executable. Missing data degrades to placeholders.
    const std::string executable_equipment = std::string(GetApplicationDirectory()) + "equipment.tsv";
    for (const std::string& candidate : {executable_equipment, std::string("equipment.tsv"),
                                         std::string("assets/data/equipment.tsv")}) {
        std::ifstream file(candidate);
        if (file) {
            std::stringstream buffer;
            buffer << file.rdbuf();
            const std::size_t loaded = odyssey::client::sync::ParseEquipmentTable(buffer.str(), equipment_table);
            std::printf("main: loaded %zu equipment entries from %s\n", loaded, candidate.c_str());
            std::fflush(stdout);
            break;
        }
    }

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
        // Decay transient combat feedback (hit flashes, banner).
        const float frame_dt = GetFrameTime();
        combat_view.Tick(frame_dt);
        if (demo.banner_ttl > 0.0f) {
            demo.banner_ttl -= frame_dt;
            if (demo.banner_ttl <= 0.0f) {
                demo.banner.clear();
            }
        }
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
            recovery.Reset();
            predictor.Reset();
            remote_interp.Clear();
            monster_interp.Clear();
            game_view = GameView{};
            combat_view.Clear();
            reward_view.Clear();
            demo.prev_stage_index = 0;
            demo.banner.clear();
            demo.banner_ttl = 0.0f;
            demo.ready_sent = false;
            client.Connect(kServerHost, kServerPort);
        }

        // Automatic reconnect with bounded backoff after a transient outage.
        // The window stays responsive: this only initiates an async connect.
        if (demo.state != ConnectionState::kConnected && recovery.ShouldRetry(GetTime())) {
            recovery.MarkRetryStarted(GetTime());
            demo.state = ConnectionState::kIdle;
            demo.state_detail = recovery.Note();
            std::printf("main: %s\n", recovery.Note().c_str());
            std::fflush(stdout);
            client.Connect(kServerHost, kServerPort);
        }

        // Reward choice: keys 1..3 pick one of the offered options. Only a
        // candidate equipment_id is sent; the server validates and applies it.
        if (demo.in_room && reward_view.State() == RewardState::kOffered) {
            const int keys[3] = {KEY_ONE, KEY_TWO, KEY_THREE};
            for (int index = 0; index < 3 &&
                                index < static_cast<int>(reward_view.Options().size());
                 ++index) {
                if (IsKeyPressed(keys[index])) {
                    std::uint32_t equipment_id = 0;
                    if (reward_view.ChooseByIndex(static_cast<std::size_t>(index), equipment_id)) {
                        SendPayload(kRewardChoice, payload::EncodeRewardChoice(equipment_id));
                        std::printf("main: reward choice sent id=%u\n", equipment_id);
                        std::fflush(stdout);
                    }
                }
            }
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
                    // Predict immediately and remember the input for replay
                    // until the server confirms it via last_processed_input.
                    predictor.RecordInput(InputCommand{last_report.sequence,
                                                       last_report.vector.x,
                                                       last_report.vector.z});
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
                        // InputSeq is deliberately NOT reset here: a resumed
                        // session must keep its sequence so the server never
                        // sees a replayed/stale range.
                        const bool had_session = demo.login_ok || !demo.resume_token.empty();
                        demo.login_sent = false;
                        demo.login_ok = false;
                        demo.session_id = 0;
                        demo.player_id = 0;
                        demo.login_note = "not sent";
                        demo.match_sent = false;
                        demo.in_room = false;
                        demo.room_id = 0;
                        demo.match_note = "not sent";
                        predictor.Reset();
                        remote_interp.Clear();
                        monster_interp.Clear();
                        game_view = GameView{};
                        combat_view.Clear();
                        reward_view.Clear();
                        demo.prev_stage_index = 0;
                        demo.banner.clear();
                        demo.banner_ttl = 0.0f;
                        demo.ready_sent = false;
                        if (had_session) {
                            recovery.OnDisconnect(GetTime());
                            std::printf("main: connection lost -> recovery (%s)\n",
                                        recovery.Note().c_str());
                            std::fflush(stdout);
                        } else {
                            recovery.Reset();
                        }
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
                            if (login.ok) {
                                // Fresh session: InputSeq restarts at 1 and the
                                // new resume token enables a later reconnect.
                                demo.resume_token = login.resume_token;
                                recovery.SetToken(login.resume_token);
                                recovery.OnFreshLoginOk();
                                demo.resumed = false;
                                input_sequencer.Reset();
                            }
                        } else {
                            demo.login_ok = false;
                            demo.login_note = "LoginResponse decode failed";
                        }
                    } else if (event->message.message_type == kResumeResponse) {
                        ResumeResponseData resume;
                        if (payload::DecodeResumeResponse(event->message.payload, resume)) {
                            recovery.OnResumeResult(resume.ok);
                            if (resume.ok) {
                                demo.login_ok = true;
                                demo.session_id = resume.session_id;
                                demo.player_id = resume.player_id;
                                demo.resumed = true;
                                demo.in_room = true;
                                demo.login_note = "resumed session";
                                std::printf("main: session resumed session=%llu player=%llu\n",
                                            static_cast<unsigned long long>(resume.session_id),
                                            static_cast<unsigned long long>(resume.player_id));
                                std::fflush(stdout);
                            } else {
                                // Refused (expired/forged/replayed). Never replay
                                // old inputs: drop identity and log in fresh.
                                demo.login_ok = false;
                                demo.in_room = false;
                                demo.login_sent = false;
                                demo.match_sent = false;
                                demo.resume_token.clear();
                                demo.login_note = "resume refused reason=" +
                                                  std::to_string(resume.reason) + " " +
                                                  resume.message;
                                std::printf("main: resume refused reason=%u\n", resume.reason);
                                std::fflush(stdout);
                            }
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
                                v.hp = p.hp;
                                v.max_hp = p.max_hp;
                                v.alive = p.alive;
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
                            // New stage: old projectiles must not leak across
                            // the transition (they are event-driven only).
                            if (demo.prev_stage_index != 0 &&
                                snap.stage.index != demo.prev_stage_index) {
                                combat_view.ClearProjectiles();
                                demo.last_event_note = "stage index changed -> projectiles cleared";
                            }
                            demo.prev_stage_index = snap.stage.index;
                            demo.stage_index = snap.stage.index;
                            demo.stage_state = snap.stage.state;
                            demo.monsters_remaining = snap.stage.monsters_remaining;
                            if (snap.has_self) {
                                demo.self_hp = snap.self.hp;
                                demo.self_max_hp = snap.self.max_hp;
                                demo.self_attack = snap.self.attack;
                                demo.self_defense = snap.self.defense;
                                demo.self_move_speed = snap.self.move_speed;
                                // D9: snap to the authoritative position and
                                // replay only the inputs the server has not
                                // confirmed yet.
                                predictor.ApplyAuthoritative(snap.self.pos_x, snap.self.pos_z,
                                                             snap.last_processed_input);
                            }

                            // D9: remote entities are rendered from an
                            // interpolated 10Hz buffer (full-set semantics).
                            std::map<std::uint64_t, std::pair<float, float>> others_positions;
                            for (const auto& other : snap.others) {
                                others_positions[other.id] = {other.pos_x, other.pos_z};
                            }
                            remote_interp.ApplyEntities(others_positions, snap.server_tick);

                            std::map<std::uint64_t, std::pair<float, float>> monster_positions;
                            for (const auto& m : snap.monsters) {
                                monster_positions[m.id] = {m.pos_x, m.pos_z};
                            }
                            monster_interp.ApplyEntities(monster_positions, snap.server_tick);
                            // Local deadline guard: stop accepting choices once
                            // the authoritative tick passes the deadline. The
                            // server still applies its default.
                            if (reward_view.State() == RewardState::kOffered &&
                                reward_view.DeadlineTick() != 0 &&
                                snap.server_tick > reward_view.DeadlineTick()) {
                                reward_view.Timeout();
                                std::printf("main: reward deadline passed (tick=%llu)\n",
                                            static_cast<unsigned long long>(snap.server_tick));
                                std::fflush(stdout);
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
                            combat_view.ApplyDamageFx(damage.target_id);
                            demo.last_event_note = "damage target=" +
                                                   std::to_string(damage.target_id) + " amount=" +
                                                   std::to_string(damage.amount) + " hp=" +
                                                   std::to_string(damage.remaining_health);
                        }
                    } else if (event->message.message_type == kDeathEvent) {
                        DeathEventData death;
                        if (payload::DecodeDeathEvent(event->message.payload, death)) {
                            ++demo.deaths;
                            combat_view.ApplyDeath(death.entity_id);
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
                            demo.banner = std::string(kind) + "  stage " +
                                          std::to_string(stage_event.stage_index);
                            demo.banner_ttl = 2.5f;
                            if (event->message.message_type == kStageStartedEvent) {
                                // A new stage begins: drop event-driven bullets
                                // from the previous wave and any reward panel.
                                combat_view.ClearProjectiles();
                                reward_view.Clear();
                                demo.ready_sent = false;
                            }
                            std::printf("main: %s stage=%u tick=%llu\n", kind,
                                        stage_event.stage_index,
                                        static_cast<unsigned long long>(stage_event.server_tick));
                            std::fflush(stdout);
                        }
                    } else if (event->message.message_type == kRewardOptions) {
                        RewardOptionsData options;
                        if (payload::DecodeRewardOptions(event->message.payload, options)) {
                            reward_view.SetOptions(options.equipment_ids,
                                                   options.deadline_server_tick,
                                                   equipment_table);
                            demo.last_event_note = "reward options=" +
                                                   std::to_string(options.equipment_ids.size());
                            std::printf("main: reward options stage=%u count=%zu deadline=%llu\n",
                                        options.stage_index, options.equipment_ids.size(),
                                        static_cast<unsigned long long>(options.deadline_server_tick));
                            std::fflush(stdout);
                        }
                    } else if (event->message.message_type == kRewardApplied) {
                        RewardAppliedData applied;
                        if (payload::DecodeRewardApplied(event->message.payload, applied)) {
                            reward_view.ApplyResult(applied.ok, applied.equipment_id, applied.reason);
                            demo.last_event_note = std::string("reward applied id=") +
                                                   std::to_string(applied.equipment_id) +
                                                   (applied.ok ? " ok" : " refused");
                            std::printf("main: reward applied id=%u ok=%d reason=%u\n",
                                        applied.equipment_id, applied.ok ? 1 : 0, applied.reason);
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

            // Resume first when we still hold a token (D8); otherwise perform a
            // fresh development login.
            if (recovery.WantsResumeRequest()) {
                recovery.MarkResumeSent();
                demo.login_sent = true;
                demo.login_note = "sending ResumeRequest";
                SendPayload(kResumeRequest,
                            payload::EncodeResumeRequest(recovery.Token(), kClientProtocolVersion));
                std::printf("main: sent ResumeRequest token_bytes=%zu\n", recovery.Token().size());
                std::fflush(stdout);
            } else if (!demo.login_sent && recovery.Phase() != RecoveryPhase::kResuming) {
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

            // D7: while in the Reward state, ENTER reports "ready for the next
            // stage". The server applies the ready barrier; repeat presses are
            // idempotent server-side.
            if (demo.in_room && demo.stage_state == 3 && IsKeyPressed(KEY_ENTER)) {
                SendPayload(kNextStageRequest, payload::EncodeNextStageRequest());
                demo.ready_sent = true;
                demo.last_event_note = "next stage ready sent";
                std::printf("main: next stage ready sent\n");
                std::fflush(stdout);
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

        const char* recovery_phase = "idle";
        switch (recovery.Phase()) {
            case RecoveryPhase::kIdle: recovery_phase = "idle"; break;
            case RecoveryPhase::kWaitingToRetry: recovery_phase = "waiting"; break;
            case RecoveryPhase::kConnecting: recovery_phase = "connecting"; break;
            case RecoveryPhase::kResuming: recovery_phase = "resuming"; break;
            case RecoveryPhase::kRestored: recovery_phase = "restored"; break;
            case RecoveryPhase::kFailed: recovery_phase = "failed"; break;
        }
        const std::string recovery_line =
            std::string("Recovery: ") + recovery_phase + " attempts=" +
            std::to_string(recovery.Attempts()) + " token_bytes=" +
            std::to_string(demo.resume_token.size()) +
            (demo.resumed ? " (resumed session)" : "") + "  " + recovery.Note();
        DrawText(recovery_line.c_str(), 24, 115, 18, GRAY);

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
            " state=" + StageStateName(demo.stage_state) +
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

        const std::string stats_line =
            "Stats(snapshot): ATK=" + std::to_string(static_cast<int>(demo.self_attack)) +
            " DEF=" + std::to_string(static_cast<int>(demo.self_defense)) +
            " SPD=" + std::to_string(static_cast<int>(demo.self_move_speed)) +
            "  Ready: " + (demo.ready_sent ? "sent" : "no") +
            "  seed=" + std::to_string(combat_view.Stage().seed);
        DrawText(stats_line.c_str(), 24, 400, 20, GRAY);
        DrawText(("Last event: " + demo.last_event_note).c_str(), 470, 400, 18, MAROON);

        char correction_text[32] = {0};
        std::snprintf(correction_text, sizeof(correction_text), "%.3f",
                      predictor.LastCorrectionDistance());
        const std::string netcode_line =
            "Netcode: pending=" + std::to_string(predictor.PendingCount()) +
            " corr=" + correction_text +
            " interpDelay=" + std::to_string(static_cast<int>(remote_interp.DelayTicks())) +
            "t tracks=" + std::to_string(remote_interp.Count()) + "/" +
            std::to_string(monster_interp.Count());
        DrawText(netcode_line.c_str(), 24, 430, 20, GRAY);

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
            // D9: render monsters from the interpolated 10Hz buffer.
            float mx = monster.x;
            float mz = monster.z;
            monster_interp.SampleEntity(id, mx, mz);
            const float sx = to_screen_x(mx);
            const float sy = to_screen_y(mz);
            const bool dead = combat_view.IsDead(id);
            DrawRectangle(static_cast<int>(sx) - 7, static_cast<int>(sy) - 7, 14, 14,
                          dead ? DARKGRAY : ORANGE);
            if (dead) {
                DrawLine(static_cast<int>(sx) - 7, static_cast<int>(sy) - 7,
                         static_cast<int>(sx) + 7, static_cast<int>(sy) + 7, BLACK);
                DrawLine(static_cast<int>(sx) - 7, static_cast<int>(sy) + 7,
                         static_cast<int>(sx) + 7, static_cast<int>(sy) - 7, BLACK);
            }
            const float ratio = monster.max_hp > 0.0f ? (monster.hp / monster.max_hp) : 0.0f;
            DrawRectangle(static_cast<int>(sx) - 10, static_cast<int>(sy) - 16, 20, 4, Fade(RED, 0.25f));
            DrawRectangle(static_cast<int>(sx) - 10, static_cast<int>(sy) - 16,
                          static_cast<int>(20.0f * ratio), 4, LIME);
            if (combat_view.IsHitFlashing(id)) {
                DrawCircleLines(static_cast<int>(sx), static_cast<int>(sy), 13.0f, GOLD);
            }
            DrawText(std::to_string(id).c_str(), static_cast<int>(sx) + 9,
                     static_cast<int>(sy) - 8, 12, DARKGRAY);
        }

        for (const auto& player : game_view.Players()) {
            const bool is_self = (player.id == demo.player_id);
            float px = player.x;
            float pz = player.z;
            if (is_self) {
                // D9: draw our predicted position (reconciled each snapshot).
                if (predictor.HasPrediction()) {
                    px = predictor.X();
                    pz = predictor.Z();
                }
            } else {
                // D9: remote players come from the interpolated buffer.
                remote_interp.SampleEntity(player.id, px, pz);
            }
            px = to_screen_x(px);
            pz = to_screen_y(pz);
            DrawCircleV(Vector2{px, pz}, 9.0f,
                        !player.alive ? DARKGRAY : (is_self ? BLUE : RED));
            if (combat_view.IsHitFlashing(player.id)) {
                DrawCircleLines(static_cast<int>(px), static_cast<int>(pz), 13.0f, GOLD);
            }
            // HP bar above every player (authoritative hp/max_hp from snapshot).
            const float hp_ratio = player.max_hp > 0.0f ? (player.hp / player.max_hp) : 0.0f;
            DrawRectangle(static_cast<int>(px) - 12, static_cast<int>(pz) - 20, 24, 4, Fade(RED, 0.25f));
            DrawRectangle(static_cast<int>(px) - 12, static_cast<int>(pz) - 20,
                          static_cast<int>(24.0f * hp_ratio), 4, player.alive ? GREEN : GRAY);
            if (is_self) {
                // Aim heading we are sending to the server.
                DrawLineV(Vector2{px, pz},
                          Vector2{px + last_aim_x * 26.0f, pz + last_aim_z * 26.0f}, DARKBLUE);
            }
            DrawText(std::to_string(player.id).c_str(), static_cast<int>(px + 12),
                     static_cast<int>(pz - 8), 16, DARKGRAY);
        }

        if (!demo.banner.empty()) {
            DrawText(demo.banner.c_str(), 300, 20, 32, MAROON);
        }

        if (reward_view.State() != RewardState::kNone) {
            // Treasure chest panel: options come from the server; display text
            // comes from the local static table (ids travel on the wire).
            DrawRectangle(20, 452, 920, 72, Fade(LIGHTGRAY, 0.45f));
            DrawText(("REWARD - " + reward_view.Note() + "   (keys 1-3 choose)").c_str(),
                     30, 456, 20, MAROON);
            std::string row;
            const auto& options = reward_view.Options();
            for (std::size_t i = 0; i < options.size(); ++i) {
                row += "[" + std::to_string(i + 1) + "] " + options[i].display.name + " (" +
                       options[i].display.slot + ") " + options[i].display.description + "   ";
            }
            DrawText(row.c_str(), 30, 486, 18, DARKGRAY);
        } else {
            DrawText("WASD move | mouse aim | SPACE shoot | ENTER ready (reward) | R retry | ESC quit",
                     24, kScreenHeight - 60, 20, LIGHTGRAY);
        }
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
