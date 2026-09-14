// odyssey_client entry point - Phase 1 D1/D2 wiring to A's v0 protocol.
//
// Flow: connect -> auto LoginRequest(dev) -> show Session/Player ID ->
// 1Hz Ping with real payload (Pong echo displayed) -> auto matchmaking ->
// 30Hz PlayerInput -> authoritative WorldSnapshot. ESC / close
// stops the Network Thread cleanly. 'R' retries a failed connect.
#include "core/BoundedQueue.h"
#include "core/ClientConfig.h"
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
#include "sync/SessionGate.h"

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

// Gameserver endpoint: resolved from the command line or the environment, with
// a loopback default (see core/ClientConfig.h). Never hardcode an address here.

// Development-mode login (Phase 1 has no real auth; server assigns identity).
constexpr const char* kDevToken = "dev";
constexpr const char* kDevDisplayName = "odyssey-c";
constexpr std::uint32_t kClientProtocolVersion = 1;

constexpr float kPingIntervalSeconds = 1.0f;
constexpr double kFrameSeconds = 1.0 / 60.0;

using namespace odyssey::client::network::ids;
namespace payload = odyssey::client::network::payload;
using odyssey::client::core::BoundedQueue;
using odyssey::client::core::ClientEndpoint;
using odyssey::client::core::ClientUsageText;
using odyssey::client::core::ConfigParseResult;
using odyssey::client::core::ParseClientOptions;
using odyssey::client::core::ToString;
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
using odyssey::client::sync::AuthoritativeRewardPhaseEnded;
using odyssey::client::sync::CanReportReady;
using odyssey::client::sync::CanSendInput;
using odyssey::client::sync::EquipmentTable;
using odyssey::client::sync::GameView;
using odyssey::client::sync::InputBlockReason;
using odyssey::client::sync::InputCommand;
using odyssey::client::sync::InputGate;
using odyssey::client::sync::InputSeqFloor;
using odyssey::client::sync::MonsterEntity;
using odyssey::client::sync::MovementPredictor;
using odyssey::client::sync::ProjectileVisual;
using odyssey::client::sync::ReadyBlockReason;
using odyssey::client::sync::RecoveryPhase;
using odyssey::client::sync::RecoveryState;
using odyssey::client::sync::RewardState;
using odyssey::client::sync::RewardView;
using odyssey::client::sync::SnapshotInterpolator;
using odyssey::client::sync::StageInfo;
using odyssey::client::sync::StageStateName;

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
    std::uint64_t snapshots_received = 0;  // lifetime (HUD)
    // Snapshots of the current session/connection. Reset on every disconnect and
    // on a fresh login: a resumed session has no authoritative state at all until
    // its first snapshot arrives (proto/session.proto).
    std::uint64_t session_snapshots = 0;

    // Combat (D4) state.
    float self_hp = 0.0f;
    float self_max_hp = 0.0f;
    // Authoritative aliveness. Gated on only once a self entity has been seen
    // (input will not be muted for a field the server has not sent yet).
    bool self_known = false;
    bool self_alive = false;
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

    // D7 readiness: the server owns the ready barrier. The client only reports
    // ready once the authoritative state is PreparingNextStage (A5 item C-a) and
    // latches it per stage so ENTER cannot spam the request.
    bool ready_sent = false;
    std::uint32_t ready_stage = 0;  // stage index ready_sent applies to
};

// A5 item C-b: everything the input gate depends on, gathered in one place so the
// send path and the HUD always agree on why input is muted.
InputGate MakeInputGate(const DemoState& demo, bool recovery_active) {
    InputGate gate;
    gate.in_room = demo.in_room;
    gate.session_has_snapshot = demo.session_snapshots > 0;
    gate.recovery_active = recovery_active;
    gate.self_known = demo.self_known;
    gate.self_alive = demo.self_alive;
    gate.stage_state = demo.stage_state;
    return gate;
}

}  // namespace

int main(int argc, char** argv) {
    // Resolve the endpoint before creating the window so that --help and
    // invalid arguments both exit without opening one.
    const ConfigParseResult config = ParseClientOptions(argc, argv);
    if (!config.ok) {
        std::fprintf(stderr, "odyssey_client: %s\n", config.error.c_str());
        std::fprintf(stderr, "\n%s", ClientUsageText());
        return 2;
    }
    if (config.options.help_requested) {
        std::printf("%s", ClientUsageText());
        return 0;
    }
    const ClientEndpoint endpoint = config.options.endpoint;

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

    // Optional local display table (static equipment data is never sent on the
    // wire). Missing file simply means "equipment#<id>" placeholders.
    for (const char* candidate : {"equipment.csv", "assets/data/equipment.csv",
                                  "client/assets/data/equipment.csv"}) {
        std::ifstream file(candidate);
        if (file) {
            std::stringstream buffer;
            buffer << file.rdbuf();
            const std::size_t loaded = odyssey::client::sync::ParseEquipmentTable(buffer.str(), equipment_table);
            std::printf("main: loaded %zu equipment entries from %s\n", loaded, candidate);
            std::fflush(stdout);
            break;
        }
    }

    client.SetEventCallback([&inbox](NetEvent&& event) { inbox.Push(std::move(event)); });
    // Prints the effective endpoint and where it came from, so a LAN playtest can
    // tell a mistyped argument from a server that is simply not running.
    std::printf("main: server endpoint %s:%u (source=%s)\n", endpoint.host.c_str(),
                static_cast<unsigned>(endpoint.port), ToString(config.options.source));
    std::printf("main: starting net thread\n"); fflush(stdout);
    client.Start(endpoint.host, endpoint.port);
    std::printf("main: net thread started, entering loop\n"); fflush(stdout);

    double last_ping_sent = 0.0;
    int frame_counter = 0;
    const double t_start = GetTime();
    // Input gate state, recomputed every frame and shared with the HUD. The
    // previous value drives the mute/unmute transitions below.
    InputGate input_gate;
    bool input_enabled = false;
    bool input_enabled_prev = false;

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
            demo.session_snapshots = 0;
            demo.self_known = false;
            demo.self_alive = false;
            client.Connect(endpoint.host, endpoint.port);
        }

        // Automatic reconnect with bounded backoff after a transient outage.
        // The window stays responsive: this only initiates an async connect.
        if (demo.state != ConnectionState::kConnected && recovery.ShouldRetry(GetTime())) {
            recovery.MarkRetryStarted(GetTime());
            demo.state = ConnectionState::kIdle;
            demo.state_detail = recovery.Note();
            std::printf("main: %s\n", recovery.Note().c_str());
            std::fflush(stdout);
            client.Connect(endpoint.host, endpoint.port);
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
        // A5 items C-b/C-e: intent is transmitted only while the room session is
        // live, this session has an authoritative snapshot, no recovery is in
        // progress, the player is alive and the stage is actually being played.
        // Recomputed every frame (before the connection check) so the send path
        // and the HUD can never disagree, and a dropped link reports muted at once.
        input_gate = MakeInputGate(demo, recovery.Active());
        input_enabled = CanSendInput(input_gate);
        if (demo.state == ConnectionState::kConnected) {
            const double now = GetTime();
            if (now - last_input_time >= 1.0 / 30.0) {
                last_input_time = now;
                last_sample = input_sampler.SampleNow();
                if (input_enabled) {
                    if (!input_enabled_prev) {
                        std::printf("main: input enabled stage=%s alive=%s\n",
                                    StageStateName(demo.stage_state),
                                    demo.self_alive ? "yes" : "no");
                        std::fflush(stdout);
                    }
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
                    // Predict with the same rules the server uses: record the
                    // intent, then advance exactly one 1/30 step per 30Hz
                    // boundary. Steps are counted in ticks, never per sent packet
                    // (A5 item C-d), so a faster send rate cannot outrun the
                    // server.
                    predictor.RecordInput(InputCommand{last_report.sequence,
                                                       last_report.vector.x,
                                                       last_report.vector.z});
                    predictor.AdvanceTick();
                    SendPayload(kPlayerInput, payload::EncodePlayerInput(input));
                } else {
                    // Muted: no InputSeq is consumed and no input is transmitted.
                    // Drop the remembered intent on the transition so a stale
                    // direction is not applied for one extra tick when play
                    // resumes (A5 item C-b).
                    if (input_enabled_prev) {
                        predictor.ClearIntent();
                        std::printf("main: input muted (%s) stage=%s alive=%s\n",
                                    InputBlockReason(input_gate),
                                    StageStateName(demo.stage_state),
                                    demo.self_alive ? "yes" : "no");
                        std::fflush(stdout);
                    }
                    last_report.sequence = 0;
                    last_report.vector = NormalizeInput(last_sample);
                }
                input_enabled_prev = input_enabled;
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
                        // No authoritative state belongs to the next connection:
                        // input stays muted until its first snapshot arrives.
                        demo.session_snapshots = 0;
                        demo.self_known = false;
                        demo.self_alive = false;
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
                                demo.session_snapshots = 0;
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
                                // The server re-bound us to the existing room:
                                // matchmaking must NOT run again for this session
                                // (A5 item C-e), and the resume token stays valid.
                                demo.match_sent = true;
                                demo.match_note = "resumed (no new match)";
                                // The resumed session has no authoritative state
                                // until its first snapshot: keep input muted and
                                // do not let the old sequence range be replayed.
                                demo.session_snapshots = 0;
                                std::printf("main: session resumed session=%llu player=%llu\n",
                                            static_cast<unsigned long long>(resume.session_id),
                                            static_cast<unsigned long long>(resume.player_id));
                                std::printf("main: input muted until the first snapshot of the "
                                            "resumed session\n");
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
                            const bool first_of_session = demo.session_snapshots == 0;
                            ++demo.session_snapshots;
                            if (demo.snapshots_received == 1) {
                                std::printf("main: first world snapshot tick=%llu\n",
                                            static_cast<unsigned long long>(snap.server_tick));
                                std::fflush(stdout);
                            }
                            if (first_of_session) {
                                // A5 item C-e: the first snapshot of a resumed
                                // session is the authority on where this session's
                                // input range already stands. Continue strictly
                                // above both the high-water mark sent before the
                                // drop and the server's LastProcessedInputSeq,
                                // never replaying the pre-drop range.
                                if (demo.resumed) {
                                    const std::uint32_t floor = InputSeqFloor(
                                        input_sequencer.LastSequence(), snap.last_processed_input);
                                    input_sequencer.EnsureGreaterThan(floor);
                                    std::printf("main: resumed input floor=%u next_seq=%u\n",
                                                floor, input_sequencer.LastSequence() + 1);
                                }
                                std::printf("main: input enabled after first snapshot%s\n",
                                            demo.resumed ? " (resumed session)" : "");
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
                            // A5 item C-a: the snapshot is the authority on the
                            // reward phase. If it already ended while this client
                            // is still holding an open panel, close it so it
                            // cannot block the ready barrier (the server settled
                            // the round; no outcome is invented here).
                            if (AuthoritativeRewardPhaseEnded(snap.stage.state) &&
                                reward_view.Active()) {
                                reward_view.SettleAfterAuthoritativeEnd();
                                std::printf("main: reward panel settled by authoritative state=%s\n",
                                            StageStateName(snap.stage.state));
                                std::fflush(stdout);
                            }
                            if (snap.has_self) {
                                demo.self_hp = snap.self.hp;
                                demo.self_max_hp = snap.self.max_hp;
                                demo.self_attack = snap.self.attack;
                                demo.self_defense = snap.self.defense;
                                demo.self_move_speed = snap.self.move_speed;
                                demo.self_known = true;
                                demo.self_alive = snap.self.alive;
                                // D9/A5 C-d: snap to the authoritative pose, adopt
                                // the server's move speed and alive flag, then
                                // re-advance only the ticks already simulated past
                                // this snapshot (tick timeline, not packet count).
                                predictor.ApplyAuthoritative(snap.self.pos_x, snap.self.pos_z,
                                                             snap.last_processed_input,
                                                             snap.server_tick,
                                                             snap.self.move_speed,
                                                             snap.self.alive);
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
                                // from the previous wave, any reward panel and the
                                // remembered movement intent (a direction held
                                // during the transition must not carry over).
                                combat_view.ClearProjectiles();
                                reward_view.Clear();
                                predictor.ClearIntent();
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

        // Keyed by stage index rather than a bare flag: if a StageStarted event
        // is ever missed, the next stage still becomes reportable instead of
        // staying latched forever.
        const bool ready_reported = demo.ready_sent && demo.ready_stage == demo.stage_index;

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

            // Matchmaking runs once per fresh session. A resumed session is
            // already bound to its room, so it must never enqueue a new match
            // request (A5 item C-e).
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

            // D7 / A5 item C-a: report "ready for the next stage" only when the
            // authoritative state is PreparingNextStage - the server moves there
            // itself once the reward round is complete - and this client's own
            // reward is settled. Pressing ENTER earlier only produces a request
            // the room cannot use, so it is refused here with a visible reason.
            if (demo.in_room && IsKeyPressed(KEY_ENTER)) {
                if (CanReportReady(demo.in_room, demo.stage_state, reward_view.State(),
                                   ready_reported)) {
                    SendPayload(kNextStageRequest, payload::EncodeNextStageRequest());
                    demo.ready_sent = true;
                    demo.ready_stage = demo.stage_index;
                    demo.last_event_note = "next stage ready sent";
                    std::printf("main: next stage ready sent stage=%u state=%s\n",
                                demo.stage_index, StageStateName(demo.stage_state));
                    std::fflush(stdout);
                } else {
                    const char* reason = ReadyBlockReason(demo.in_room, demo.stage_state,
                                                          reward_view.State(), ready_reported);
                    demo.last_event_note = std::string("ready blocked: ") + reason;
                    std::printf("main: ready blocked (%s) stage=%u state=%s\n", reason,
                                demo.stage_index, StageStateName(demo.stage_state));
                    std::fflush(stdout);
                }
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
            "  input=" + (input_enabled
                              ? std::string("on")
                              : std::string("muted:") + InputBlockReason(input_gate)) +
            "  events sp/dst/dmg/dth=" + std::to_string(demo.spawns) + "/" +
            std::to_string(demo.destroys) + "/" + std::to_string(demo.damages) + "/" +
            std::to_string(demo.deaths);
        DrawText(hp_line.c_str(), 24, 370, 20, GRAY);

        // Ready is gated on the authoritative preparing state (A5 C-a): show why
        // ENTER is unavailable instead of leaving the operator guessing.
        const std::string ready_text =
            ready_reported ? "sent"
                           : (CanReportReady(demo.in_room, demo.stage_state, reward_view.State(),
                                             ready_reported)
                                  ? "ready"
                                  : std::string("blocked: ") +
                                        ReadyBlockReason(demo.in_room, demo.stage_state,
                                                         reward_view.State(), ready_reported));
        const std::string stats_line =
            "Stats(snapshot): ATK=" + std::to_string(static_cast<int>(demo.self_attack)) +
            " DEF=" + std::to_string(static_cast<int>(demo.self_defense)) +
            " SPD=" + std::to_string(static_cast<int>(demo.self_move_speed)) +
            "  Ready: " + ready_text +
            "  seed=" + std::to_string(combat_view.Stage().seed);
        DrawText(stats_line.c_str(), 24, 400, 20, GRAY);
        DrawText(("Last event: " + demo.last_event_note).c_str(), 470, 400, 18, MAROON);

        char correction_text[32] = {0};
        std::snprintf(correction_text, sizeof(correction_text), "%.3f",
                      predictor.LastCorrectionDistance());
        const std::string netcode_line =
            "Netcode: pending=" + std::to_string(predictor.PendingCount()) +
            " corr=" + correction_text +
            " predTick=" + std::to_string(predictor.PredictedTick()) +
            " ack=" + std::to_string(predictor.AckSeq()) +
            " spd=" + std::to_string(static_cast<int>(predictor.MoveSpeed())) +
            " alive=" + std::string(predictor.Alive() ? "y" : "n") +
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
                       options[i].display.slot + ") " + options[i].display.stats + "   ";
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
