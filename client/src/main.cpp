// odyssey_client entry point - Phase 1 D1/D2 wiring to A's v0 protocol.
//
// Flow: connect -> auto LoginRequest(dev) -> show Session/Player ID ->
// 1Hz Ping with real payload (Pong echo displayed) -> auto matchmaking ->
// 30Hz PlayerInput -> authoritative WorldSnapshot. ESC / close
// stops the Network Thread cleanly. 'R' retries a failed connect; a failed
// match can be restarted with the on-screen button (or N).
#include "core/BoundedQueue.h"
#include "input/InputSample.h"
#include "input/InputSampler.h"
#include "network/NetClient.h"
#include "network/NetMessage.h"
#include "network/PayloadCodec.h"
#include "network/ProtocolIds.h"
#include "raylib.h"
#include "rlgl.h"
#include "sync/CombatView.h"
#include "sync/GameView.h"
#include "sync/Interpolation.h"
#include "sync/Prediction.h"
#include "sync/RecoveryState.h"
#include "sync/RewardView.h"
#include "ui/ChineseLabels.h"
#include "ui/RewardChoiceInput.h"

#include <cmath>
#include <charconv>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <map>
#include <set>
#include <sstream>
#include <string>
#include <utility>
#include <vector>

#if defined(_WIN32)
#include <process.h>
#endif

namespace {

constexpr int kScreenWidth = 1280;
constexpr int kScreenHeight = 720;
constexpr int kFps = 60;

// World [0,20]^2 arena mapped into this screen rectangle (shared by the aim
// inverse mapping and the drawing code).
constexpr float kWorldSize = 20.0f;
constexpr float kArenaX = 28.0f;
constexpr float kArenaY = 112.0f;
constexpr float kArenaW = 1224.0f;
constexpr float kArenaH = 508.0f;
constexpr Rectangle kNewRunButton{490.0f, 390.0f, 300.0f, 54.0f};

// "Tactical viewport" palette: a quiet deep-space frame with navigation-cyan
// information, solar-gold actions and coral danger. Debug telemetry uses its
// own neutral overlay and never competes with the player HUD.
constexpr Color kVoid{7, 12, 24, 255};
constexpr Color kPanel{15, 27, 49, 255};
constexpr Color kPanelRaised{23, 39, 67, 255};
constexpr Color kGrid{37, 65, 94, 255};
constexpr Color kStarlight{232, 241, 255, 255};
constexpr Color kMuted{133, 155, 181, 255};
constexpr Color kCyan{84, 214, 232, 255};
constexpr Color kGold{244, 201, 93, 255};
namespace ui = odyssey::client::ui;
Font gChineseFont{};

bool HasChinese(const char* text) {
    for (const unsigned char* cursor = reinterpret_cast<const unsigned char*>(text); *cursor; ++cursor) {
        if (*cursor >= 0x80) return true;
    }
    return false;
}

void DrawUiText(const char* text, int x, int y, int size, Color color) {
    if (HasChinese(text) && IsFontValid(gChineseFont)) {
        DrawTextEx(gChineseFont, text, Vector2{static_cast<float>(x), static_cast<float>(y)},
                   static_cast<float>(size), 1.0f, color);
    } else {
        DrawText(text, x, y, size, color);
    }
}

int MeasureUiText(const char* text, int size) {
    if (HasChinese(text) && IsFontValid(gChineseFont)) {
        return static_cast<int>(MeasureTextEx(gChineseFont, text, static_cast<float>(size), 1.0f).x);
    }
    return MeasureText(text, size);
}

void LoadChineseFont(const odyssey::client::sync::EquipmentTable& equipment_table) {
    std::string corpus;
    for (const char* label : ui::kPlayerLabels) { corpus += label; }
    for (const auto& [id, display] : equipment_table) {
        (void)id;
        corpus += display.name + display.slot + display.description;
    }
    int count = 0;
    int* decoded = LoadCodepoints(corpus.c_str(), &count);
    std::set<int> glyphs;
    glyphs.insert(' ');
    glyphs.insert('?');  // Fallback for an unanticipated server-provided label.
    for (int codepoint = '0'; codepoint <= '9'; ++codepoint) { glyphs.insert(codepoint); }
    for (int index = 0; index < count; ++index) { glyphs.insert(decoded[index]); }
    UnloadCodepoints(decoded);
    const std::vector<int> codepoints(glyphs.begin(), glyphs.end());
    for (const char* candidate : {"C:/Windows/Fonts/simhei.ttf", "C:/Windows/Fonts/msyh.ttf",
                                  "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttf"}) {
        if (!FileExists(candidate)) continue;
        gChineseFont = LoadFontEx(candidate, 48, codepoints.data(),
                                  static_cast<int>(codepoints.size()));
        if (IsFontValid(gChineseFont)) {
            SetTextureFilter(gChineseFont.texture, TEXTURE_FILTER_BILINEAR);
            std::printf("main: Chinese UI font loaded %s glyphs=%zu\n", candidate, codepoints.size());
            std::fflush(stdout);
            return;
        }
    }
    std::fprintf(stderr, "main: no Chinese UI font found; install SimHei on Windows\n");
}
constexpr Color kDanger{255, 107, 114, 255};
constexpr Color kAlly{145, 126, 255, 255};

// The development server listens on loopback. Override for LAN testing via
// ODYSSEY_SERVER_HOST/ODYSSEY_SERVER_PORT; never embed a teammate's IP.
constexpr const char* kDefaultServerHost = "127.0.0.1";
constexpr std::uint16_t kDefaultServerPort = 7777;

// Development-mode login (Phase 1 has no real auth; server assigns identity).
constexpr const char* kDevToken = "dev";
constexpr const char* kDevDisplayName = "odyssey-c";
constexpr std::uint32_t kClientProtocolVersion = 1;

constexpr float kPingIntervalSeconds = 1.0f;
constexpr double kFrameSeconds = 1.0 / 60.0;

struct CanvasViewport {
    float scale = 1.0f;
    float offset_x = 0.0f;
    float offset_y = 0.0f;
};

CanvasViewport CurrentCanvasViewport() {
    const float screen_width = static_cast<float>(GetScreenWidth());
    const float screen_height = static_cast<float>(GetScreenHeight());
    const float horizontal = screen_width / static_cast<float>(kScreenWidth);
    const float vertical = screen_height / static_cast<float>(kScreenHeight);
    const float scale = horizontal < vertical ? horizontal : vertical;
    if (scale <= 0.0f) return {};
    return CanvasViewport{scale,
                          (screen_width - static_cast<float>(kScreenWidth) * scale) * 0.5f,
                          (screen_height - static_cast<float>(kScreenHeight) * scale) * 0.5f};
}

Vector2 CanvasMousePosition(const CanvasViewport& viewport) {
    const Vector2 mouse = GetMousePosition();
    return Vector2{(mouse.x - viewport.offset_x) / viewport.scale,
                   (mouse.y - viewport.offset_y) / viewport.scale};
}

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

const char* PlayerStageName(std::uint32_t state) {
    switch (state) {
        case 0: return ui::kStageWaiting;
        case 1: return ui::kStagePlaying;
        case 2: return ui::kStageClear;
        case 3: return ui::kStageReward;
        case 4: return ui::kStagePreparing;
        case 5: return ui::kStageFailed;
        case 6: return ui::kStageClosed;
        default: return ui::kStageStandby;
    }
}

float Clamp01(float value) {
    if (value < 0.0f) return 0.0f;
    if (value > 1.0f) return 1.0f;
    return value;
}

void DrawPanel(Rectangle bounds, Color fill, Color border) {
    DrawRectangleRec(bounds, fill);
    DrawRectangleLinesEx(bounds, 1.0f, border);
}

void DrawCentered(const std::string& text, float center_x, int y, int size, Color color) {
    DrawUiText(text.c_str(), static_cast<int>(center_x) - MeasureUiText(text.c_str(), size) / 2,
               y, size, color);
}

void DrawMeter(Rectangle bounds, float ratio, Color fill) {
    DrawRectangleRec(bounds, Fade(kStarlight, 0.12f));
    const Rectangle value{bounds.x, bounds.y, bounds.width * Clamp01(ratio), bounds.height};
    DrawRectangleRec(value, fill);
}

std::string ShortText(const std::string& value, std::size_t limit) {
    if (value.size() <= limit) return value;
    return value.substr(0, limit - 3) + "...";
}

void ConfigureDiagnostics() {
#if defined(_WIN32) && defined(ODYSSEY_PLAYER_CLIENT)
    // A player build has no console. Preserve diagnostics beside the
    // executable for bug reports without putting protocol logs on screen.
    // Each process owns its files so two clients may share one working folder.
    FILE* output = nullptr;
    FILE* errors = nullptr;
    const std::string process_id = std::to_string(_getpid());
    const std::string output_path = "odyssey-client-" + process_id + ".log";
    const std::string error_path = "odyssey-client-error-" + process_id + ".log";
    (void)freopen_s(&output, output_path.c_str(), "a", stdout);
    (void)freopen_s(&errors, error_path.c_str(), "a", stderr);
#endif
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
using odyssey::client::sync::PlayerView;
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
    double stage_clear_started_at = 0.0;
};

}  // namespace

int main() {
    ConfigureDiagnostics();
    const char* configured_host = std::getenv("ODYSSEY_SERVER_HOST");
    const std::string server_host = configured_host && *configured_host
                                        ? configured_host : kDefaultServerHost;
    std::uint16_t server_port = kDefaultServerPort;
    if (const char* configured_port = std::getenv("ODYSSEY_SERVER_PORT");
        configured_port && *configured_port) {
        unsigned int value = 0;
        const char* end = configured_port + std::char_traits<char>::length(configured_port);
        const auto result = std::from_chars(configured_port, end, value);
        if (result.ec != std::errc{} || result.ptr != end || value == 0 || value > 65535) {
            std::fprintf(stderr, "main: ODYSSEY_SERVER_PORT must be 1..65535\n");
            return 1;
        }
        server_port = static_cast<std::uint16_t>(value);
    }
    SetConfigFlags(FLAG_WINDOW_RESIZABLE);
    std::printf("main: before InitWindow\n"); fflush(stdout);
    InitWindow(kScreenWidth, kScreenHeight, "奥德赛归途 · 中文测试版");
    std::printf("main: after InitWindow\n"); fflush(stdout);
    SetWindowMinSize(800, 450);
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
    bool rematch_pending = false;
    bool awaiting_new_stage = false;
    bool show_debug = false;
    std::int64_t old_stage_seed = 0;
    double rematch_requested_at = 0.0;
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
    const std::string localized_equipment =
        std::string(GetApplicationDirectory()) + "equipment.zh-CN.tsv";
    std::ifstream localized_file(localized_equipment);
    if (localized_file) {
        std::stringstream buffer;
        buffer << localized_file.rdbuf();
        std::printf("main: loaded %zu Chinese equipment labels\n",
                    odyssey::client::sync::ParseEquipmentTable(buffer.str(), equipment_table));
    } else {
        std::fprintf(stderr, "main: missing Chinese equipment labels: %s\n", localized_equipment.c_str());
    }
    LoadChineseFont(equipment_table);


    client.SetEventCallback([&inbox](NetEvent&& event) { inbox.Push(std::move(event)); });
    std::printf("main: starting net thread\n"); fflush(stdout);
    client.Start(server_host, server_port);
    std::printf("main: net thread started, entering loop\n"); fflush(stdout);

    double last_ping_sent = 0.0;

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
        const CanvasViewport viewport = CurrentCanvasViewport();
        const Vector2 canvas_mouse = CanvasMousePosition(viewport);
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
        if (IsKeyPressed(KEY_ESCAPE)) {
            std::printf("main: ESC pressed, exiting loop\n"); fflush(stdout);
            break;
        }
        if (IsKeyPressed(KEY_F3)) {
            show_debug = !show_debug;
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
            client.Connect(server_host, server_port);
        }

        // This is a new *match*, not a TCP reconnect. The server admits the
        // request only after defeat and moves both connected players together.
        if (demo.state == ConnectionState::kConnected && demo.in_room &&
            demo.stage_state == 5 && !rematch_pending &&
            (IsKeyPressed(KEY_N) ||
             (IsMouseButtonPressed(MOUSE_BUTTON_LEFT) &&
              CheckCollisionPointRec(canvas_mouse, kNewRunButton)))) {
            SendPayload(kMatchRequest, payload::EncodeMatchRequest());
            rematch_pending = true;
            rematch_requested_at = GetTime();
            demo.match_note = "starting new run for both players";
            std::printf("main: requested a new two-player match\n");
            std::fflush(stdout);
        }
        if (rematch_pending && GetTime() - rematch_requested_at > 6.0) {
            rematch_pending = false;
            awaiting_new_stage = false;
            demo.match_note = "new run timed out; check both players are online";
        }

        // Automatic reconnect with bounded backoff after a transient outage.
        // The window stays responsive: this only initiates an async connect.
        if (demo.state != ConnectionState::kConnected && recovery.ShouldRetry(GetTime())) {
            recovery.MarkRetryStarted(GetTime());
            demo.state = ConnectionState::kIdle;
            demo.state_detail = recovery.Note();
            std::printf("main: %s\n", recovery.Note().c_str());
            std::fflush(stdout);
            client.Connect(server_host, server_port);
        }

        // Reward choice: click a card or press 1..3 (including the numpad).
        // Only a candidate equipment_id is sent; the server validates it.
        if (demo.in_room && reward_view.State() == RewardState::kOffered) {
            const int number_keys[3] = {KEY_ONE, KEY_TWO, KEY_THREE};
            const int numpad_keys[3] = {KEY_KP_1, KEY_KP_2, KEY_KP_3};
            int pressed_digit = 0;
            for (int index = 0; index < 3; ++index) {
                if (IsKeyPressed(number_keys[index]) || IsKeyPressed(numpad_keys[index])) {
                    pressed_digit = index + 1;
                    break;
                }
            }
            const auto selected = odyssey::client::ui::SelectRewardOption(
                pressed_digit, IsMouseButtonPressed(MOUSE_BUTTON_LEFT),
                canvas_mouse.x, canvas_mouse.y, reward_view.Options().size());
            std::uint32_t equipment_id = 0;
            if (selected && reward_view.ChooseByIndex(*selected, equipment_id)) {
                SendPayload(kRewardChoice, payload::EncodeRewardChoice(equipment_id));
                std::printf("main: reward choice sent id=%u\n", equipment_id);
                std::fflush(stdout);
            }
        }

        // Sample and transmit intent at a fixed 30Hz after MatchFound. The
        // client sends direction only; position always comes from snapshots.
        if (demo.state == ConnectionState::kConnected) {
            const double now = GetTime();
            if (now - last_input_time >= 1.0 / 30.0) {
                last_input_time = now;
                last_sample = input_sampler.SampleNow();
                if (demo.in_room && (demo.stage_state == 0 || demo.stage_state == 1)) {
                    last_report = input_sequencer.Tick(last_sample);
                    // Aim heading: mouse position mapped back to world space,
                    // relative to our own authoritative position. The client
                    // never sends positions or hit results.
                    const float mx = (canvas_mouse.x - kArenaX) / kArenaW * kWorldSize;
                    const float mz = (canvas_mouse.y - kArenaY) / kArenaH * kWorldSize;
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
                            if (demo.room_id != 0 && demo.room_id != match.room_id) {
                                // Do not display the old defeat or replay old
                                // prediction after the room changes. Keep the
                                // InputSeq monotonic within the same session:
                                // in-flight old inputs may reach the new room.
                                predictor.Reset();
                                remote_interp.Clear();
                                monster_interp.Clear();
                                old_stage_seed = combat_view.Stage().seed;
                                game_view = GameView{};
                                combat_view.Clear();
                                reward_view.Clear();
                                demo.stage_index = 0;
                                demo.stage_state = 0;
                                demo.prev_stage_index = 0;
                                demo.monsters_remaining = 0;
                                demo.self_hp = 0.0f;
                                demo.banner.clear();
                                demo.banner_ttl = 0.0f;
                                demo.ready_sent = false;
                                awaiting_new_stage = true;
                            }
                            rematch_pending = false;
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
                            // The snapshot wire format has no room ID. A final
                            // queued snapshot from the failed room can arrive
                            // after MatchFound; accept only the new Playing
                            // stage while switching rooms.
                            if (awaiting_new_stage &&
                                (snap.stage.state != 1 || snap.stage.seed == old_stage_seed)) {
                                continue;
                            }
                            awaiting_new_stage = false;
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
                            if (snap.stage.state == 2 && demo.stage_state != 2) {
                                demo.stage_clear_started_at = GetTime();
                            } else if (snap.stage.state != 2) {
                                demo.stage_clear_started_at = 0.0;
                            }
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
                        if (awaiting_new_stage && event->message.message_type == kTeamDefeatedEvent) {
                            continue;
                        }
                        StageEventData stage_event;
                        if (payload::DecodeStageEvent(event->message.payload, stage_event)) {
                            const char* kind = event->message.message_type == kStageStartedEvent
                                                   ? "stage started"
                                                   : (event->message.message_type == kStageClearedEvent
                                                          ? "stage cleared"
                                                          : "team defeated");
                            demo.last_event_note = std::string(kind) + " index=" +
                                                   std::to_string(stage_event.stage_index);
                            demo.banner = event->message.message_type == kTeamDefeatedEvent
                                              ? ui::kStageDefeatedBanner
                                              : std::string(ui::kStagePrefix) + " " +
                                                    std::to_string(stage_event.stage_index) + " " +
                                                    (event->message.message_type == kStageStartedEvent
                                                         ? ui::kStageStartedBanner : ui::kStageClearedBanner);
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
            if (demo.in_room && (demo.stage_state == 3 || demo.stage_state == 4) &&
                reward_view.State() == RewardState::kApplied && IsKeyPressed(KEY_ENTER)) {
                SendPayload(kNextStageRequest, payload::EncodeNextStageRequest());
                demo.ready_sent = true;
                demo.last_event_note = "next stage ready sent";
                std::printf("main: next stage ready sent\n");
                std::fflush(stdout);
            }
        }

        BeginDrawing();
        ClearBackground(Color{3, 6, 12, 255});
        rlPushMatrix();
        rlTranslatef(viewport.offset_x, viewport.offset_y, 0.0f);
        rlScalef(viewport.scale, viewport.scale, 1.0f);
        DrawRectangle(0, 0, kScreenWidth, kScreenHeight, kVoid);

        // The player HUD stays intentionally sparse. Raw transport and world
        // diagnostics are rendered only in the F3 developer overlay below.
        DrawRectangle(0, 0, kScreenWidth, 96, kPanel);
        DrawLine(0, 95, kScreenWidth, 95, Fade(kCyan, 0.35f));
        DrawUiText(ui::kTitle, 28, 22, 23, kStarlight);
        DrawUiText(ui::kSubtitle, 29, 55, 14, kMuted);

        const Rectangle self_card{340.0f, 18.0f, 215.0f, 60.0f};
        DrawPanel(self_card, kPanelRaised, Fade(kCyan, 0.3f));
        DrawUiText(ui::kPilot, 354, 27, 13, kMuted);
        const std::string self_hp = std::to_string(static_cast<int>(demo.self_hp)) + " / " +
                                    std::to_string(static_cast<int>(demo.self_max_hp));
        DrawText(self_hp.c_str(), 354, 45, 19, kStarlight);
        DrawMeter(Rectangle{438.0f, 49.0f, 100.0f, 7.0f},
                  demo.self_max_hp > 0.0f ? demo.self_hp / demo.self_max_hp : 0.0f,
                  demo.self_hp > demo.self_max_hp * 0.3f ? kCyan : kDanger);

        const Rectangle stage_card{570.0f, 18.0f, 198.0f, 60.0f};
        DrawPanel(stage_card, kPanelRaised, Fade(kGold, 0.3f));
        DrawUiText((std::string(ui::kStage) + " " + std::to_string(demo.stage_index)).c_str(), 584, 27, 13, kMuted);
        DrawUiText(PlayerStageName(demo.stage_state), 584, 46, 17,
                 demo.stage_state == 5 ? kDanger : kGold);

        const std::vector<PlayerView> player_views = game_view.Players();
        const PlayerView* teammate = nullptr;
        for (const auto& player : player_views) {
            if (player.id != demo.player_id) {
                teammate = &player;
                break;
            }
        }
        const Rectangle team_card{783.0f, 18.0f, 214.0f, 60.0f};
        DrawPanel(team_card, kPanelRaised, Fade(kAlly, 0.3f));
        DrawUiText(ui::kAlly, 797, 27, 13, kMuted);
        if (teammate != nullptr) {
            const std::string ally_hp = std::to_string(static_cast<int>(teammate->hp)) + " / " +
                                        std::to_string(static_cast<int>(teammate->max_hp));
            DrawUiText(teammate->alive ? ally_hp.c_str() : ui::kDown, 797, 45, 19,
                     teammate->alive ? kStarlight : kDanger);
            DrawMeter(Rectangle{881.0f, 49.0f, 99.0f, 7.0f},
                      teammate->max_hp > 0.0f ? teammate->hp / teammate->max_hp : 0.0f,
                      teammate->alive ? kAlly : kDanger);
        } else {
            DrawUiText(ui::kWaiting, 797, 45, 19, kMuted);
        }

        const Rectangle link_card{1012.0f, 18.0f, 240.0f, 60.0f};
        DrawPanel(link_card, kPanelRaised, Fade(kCyan, 0.2f));
        const bool connected = demo.state == ConnectionState::kConnected;
        DrawCircle(1029, 38, 5.0f, connected ? kCyan : kDanger);
        DrawUiText(connected ? ui::kOnline : ui::kOffline, 1043, 29, 15,
                 connected ? kStarlight : kDanger);
        const std::string hostile_count = std::to_string(demo.monsters_remaining) + " " + ui::kHostiles;
        DrawUiText(hostile_count.c_str(), 1043, 51, 14, kMuted);

        // The arena is a navigational chart rather than a generic rectangle:
        // grid, orbital rings and fixed stars establish the Odyssey identity.
        DrawPanel(Rectangle{kArenaX, kArenaY, kArenaW, kArenaH}, kPanel, Fade(kCyan, 0.4f));
        for (int index = 1; index < 10; ++index) {
            const int x = static_cast<int>(kArenaX + kArenaW * index / 10.0f);
            const int y = static_cast<int>(kArenaY + kArenaH * index / 10.0f);
            DrawLine(x, static_cast<int>(kArenaY), x, static_cast<int>(kArenaY + kArenaH),
                     Fade(kGrid, 0.58f));
            DrawLine(static_cast<int>(kArenaX), y, static_cast<int>(kArenaX + kArenaW), y,
                     Fade(kGrid, 0.58f));
        }
        const Vector2 chart_center{kArenaX + kArenaW * 0.5f, kArenaY + kArenaH * 0.5f};
        DrawCircleLines(static_cast<int>(chart_center.x), static_cast<int>(chart_center.y),
                        90.0f, Fade(kGrid, 0.62f));
        DrawCircleLines(static_cast<int>(chart_center.x), static_cast<int>(chart_center.y),
                        185.0f, Fade(kGrid, 0.48f));
        for (int index = 0; index < 34; ++index) {
            const int x = static_cast<int>(kArenaX) + 12 + (index * 97) % 1190;
            const int y = static_cast<int>(kArenaY) + 12 + (index * 53) % 480;
            DrawCircle(x, y, index % 5 == 0 ? 1.5f : 1.0f, Fade(kStarlight, 0.28f));
        }
        DrawUiText(ui::kMission, 44, 128, 15, kMuted);

        const auto to_screen_x = [](float wx) { return kArenaX + (wx / kWorldSize) * kArenaW; };
        const auto to_screen_y = [](float wz) { return kArenaY + (wz / kWorldSize) * kArenaH; };

        for (const auto& [id, projectile] : combat_view.Projectiles()) {
            (void)id;
            const Vector2 point{to_screen_x(projectile.x), to_screen_y(projectile.z)};
            DrawCircleV(point, 6.0f, Fade(kGold, 0.18f));
            DrawCircleV(point, 3.0f, kGold);
        }

        for (const auto& [id, monster] : combat_view.Monsters()) {
            // D9: render monsters from the interpolated 10Hz buffer.
            float mx = monster.x;
            float mz = monster.z;
            monster_interp.SampleEntity(id, mx, mz);
            const float sx = to_screen_x(mx);
            const float sy = to_screen_y(mz);
            const bool dead = combat_view.IsDead(id);
            DrawCircleV(Vector2{sx, sy}, 19.0f, Fade(dead ? kMuted : kDanger, 0.10f));
            DrawPoly(Vector2{sx, sy}, 4, 12.0f, 45.0f, dead ? kMuted : kDanger);
            if (dead) {
                DrawLine(static_cast<int>(sx) - 9, static_cast<int>(sy) - 9,
                         static_cast<int>(sx) + 9, static_cast<int>(sy) + 9, kVoid);
                DrawLine(static_cast<int>(sx) - 9, static_cast<int>(sy) + 9,
                         static_cast<int>(sx) + 9, static_cast<int>(sy) - 9, kVoid);
            }
            const float ratio = monster.max_hp > 0.0f ? (monster.hp / monster.max_hp) : 0.0f;
            DrawMeter(Rectangle{sx - 20.0f, sy - 25.0f, 40.0f, 5.0f}, ratio, kDanger);
            if (combat_view.IsHitFlashing(id)) {
                DrawCircleLines(static_cast<int>(sx), static_cast<int>(sy), 22.0f, kGold);
            }
            if (show_debug) {
                DrawText(std::to_string(id).c_str(), static_cast<int>(sx) + 16,
                         static_cast<int>(sy) - 8, 13, kMuted);
            }
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
            const Color player_color = !player.alive ? kMuted : (is_self ? kCyan : kAlly);
            DrawCircleV(Vector2{px, pz}, 23.0f, Fade(player_color, 0.10f));
            if (is_self) {
                DrawCircleV(Vector2{px, pz}, 12.0f, player_color);
                DrawCircleLines(static_cast<int>(px), static_cast<int>(pz), 16.0f, kStarlight);
            } else {
                DrawPoly(Vector2{px, pz}, 6, 13.0f, 30.0f, player_color);
            }
            if (combat_view.IsHitFlashing(player.id)) {
                DrawCircleLines(static_cast<int>(px), static_cast<int>(pz), 25.0f, kGold);
            }
            // HP bar above every player (authoritative hp/max_hp from snapshot).
            const float hp_ratio = player.max_hp > 0.0f ? (player.hp / player.max_hp) : 0.0f;
            DrawMeter(Rectangle{px - 24.0f, pz - 31.0f, 48.0f, 6.0f}, hp_ratio,
                      player.alive ? player_color : kDanger);
            if (is_self) {
                // Aim heading we are sending to the server.
                DrawLineV(Vector2{px, pz},
                          Vector2{px + last_aim_x * 34.0f, pz + last_aim_z * 34.0f}, kStarlight);
            }
            DrawUiText(is_self ? ui::kYou : ui::kAlly, static_cast<int>(px + 20),
                       static_cast<int>(pz - 9), 14, player_color);
            if (show_debug) {
                DrawText(std::to_string(player.id).c_str(), static_cast<int>(px + 20),
                         static_cast<int>(pz + 8), 12, kMuted);
            }
        }

        if (!demo.banner.empty()) {
            const std::string banner = ShortText(demo.banner, 46);
            const int width = MeasureUiText(banner.c_str(), 20) + 42;
            const Rectangle toast{static_cast<float>((kScreenWidth - width) / 2), 124.0f,
                                  static_cast<float>(width), 42.0f};
            DrawPanel(toast, Fade(kPanelRaised, 0.96f), Fade(kGold, 0.65f));
            DrawCentered(banner, kScreenWidth * 0.5f, 135, 20, kGold);
        }

        if (reward_view.State() != RewardState::kNone) {
            const Rectangle reward_panel{70.0f, 350.0f, 1140.0f, 238.0f};
            DrawPanel(reward_panel, Fade(kVoid, 0.96f), Fade(kGold, 0.75f));
            DrawUiText(ui::kSalvage, 94, 370, 24, kGold);
            std::string reward_instruction = ui::kChoose;
            if (reward_view.State() == RewardState::kChosen) {
                reward_instruction = ui::kInstalling;
            } else if (reward_view.State() == RewardState::kApplied) {
                reward_instruction = demo.ready_sent
                                         ? ui::kReadyWaiting
                                         : ui::kInstalled;
            } else if (reward_view.State() == RewardState::kTimedOut) {
                reward_instruction = ui::kExpired;
            } else if (reward_view.State() == RewardState::kRejected) {
                reward_instruction = ui::kRejected;
            }
            DrawUiText(reward_instruction.c_str(), 94, 402, 16,
                       reward_view.State() == RewardState::kApplied ? kCyan : kMuted);
            const auto& options = reward_view.Options();
            for (std::size_t i = 0; i < options.size(); ++i) {
                const auto bounds = odyssey::client::ui::CardBounds(i);
                const Rectangle card{bounds.x, bounds.y, bounds.width, bounds.height};
                const bool hovered = reward_view.State() == RewardState::kOffered &&
                                     CheckCollisionPointRec(canvas_mouse, card);
                DrawPanel(card, hovered ? Color{38, 59, 82, 255} : kPanelRaised,
                          Fade(hovered ? kCyan : kGold, hovered ? 0.85f : 0.32f));
                DrawText(("[" + std::to_string(i + 1) + "]").c_str(),
                         static_cast<int>(card.x + 16), 450, 20, kGold);
                DrawUiText(ShortText(options[i].display.name, 26).c_str(),
                           static_cast<int>(card.x + 58), 450, 20, kStarlight);
                DrawUiText(options[i].display.slot.c_str(), static_cast<int>(card.x + 16), 482, 14, kCyan);
                DrawUiText(ShortText(options[i].display.description, 38).c_str(),
                           static_cast<int>(card.x + 16), 516, 15, kMuted);
            }
        }

        if (demo.in_room && demo.stage_state == 2 && reward_view.State() == RewardState::kNone) {
            const bool expedition_complete = demo.stage_clear_started_at > 0.0 &&
                                             GetTime() - demo.stage_clear_started_at > 1.5;
            const Rectangle panel{430.0f, 258.0f, 420.0f, 164.0f};
            DrawPanel(panel, Fade(kVoid, 0.96f), Fade(kGold, 0.78f));
            DrawCentered(expedition_complete ? ui::kExpeditionComplete : ui::kStageClear,
                         kScreenWidth * 0.5f, 288, 27, kGold);
            DrawCentered(expedition_complete ? ui::kAllSecure
                                             : ui::kScanning,
                         kScreenWidth * 0.5f, 337, 16, kMuted);
            if (!expedition_complete) {
                DrawCentered(ui::kIncoming, kScreenWidth * 0.5f, 369, 14, kCyan);
            }
        }

        if (demo.state == ConnectionState::kConnected && demo.in_room &&
            (demo.stage_state == 5 || rematch_pending || awaiting_new_stage)) {
            const Rectangle panel{430.0f, 228.0f, 420.0f, 248.0f};
            DrawPanel(panel, Fade(kVoid, 0.97f), Fade(kDanger, 0.82f));
            DrawRectangle(430, 228, 7, 248, kDanger);
            DrawCentered(awaiting_new_stage ? ui::kNewExpedition : ui::kCrewDefeated,
                         kScreenWidth * 0.5f, 256, 28, kStarlight);
            DrawCentered(awaiting_new_stage ? ui::kPreparingSector : ui::kRestartTogether,
                         kScreenWidth * 0.5f, 300, 17, kMuted);
            DrawCentered(ui::kStayOnline, kScreenWidth * 0.5f, 330, 15, kMuted);
            const bool hover = CheckCollisionPointRec(canvas_mouse, kNewRunButton);
            const bool busy = rematch_pending || awaiting_new_stage;
            DrawRectangleRec(kNewRunButton, busy ? Fade(kMuted, 0.45f) : (hover ? kGold : Fade(kGold, 0.82f)));
            DrawRectangleLinesEx(kNewRunButton, hover && !busy ? 2.0f : 1.0f,
                                 hover && !busy ? kStarlight : Fade(kGold, 0.4f));
            DrawCentered(busy ? ui::kStarting : ui::kStartNewRun, kScreenWidth * 0.5f, 406, 20,
                         busy ? kStarlight : kVoid);
            DrawCentered(ui::kClickOrN, kScreenWidth * 0.5f, 454, 14, kMuted);
        }

        if (!demo.in_room && reward_view.State() == RewardState::kNone) {
            const Rectangle panel{430.0f, 246.0f, 420.0f, 190.0f};
            DrawPanel(panel, Fade(kVoid, 0.95f), Fade(connected ? kCyan : kDanger, 0.7f));
            const std::string title = connected ? ui::kAssembling : ui::kLinking;
            DrawCentered(title, kScreenWidth * 0.5f, 279, 26, kStarlight);
            DrawCentered(connected ? ui::kSecondPilot : ui::kKeepServer,
                         kScreenWidth * 0.5f, 325, 17, kMuted);
            DrawCentered(connected ? ui::kAutoMatch : ui::kRetryLink,
                         kScreenWidth * 0.5f, 357, 15, connected ? kCyan : kGold);
        }

        DrawRectangle(0, 638, kScreenWidth, 82, kPanel);
        DrawLine(0, 638, kScreenWidth, 638, Fade(kCyan, 0.3f));
        if (reward_view.State() != RewardState::kNone) {
            DrawUiText(ui::kUpgrade, 32, 656, 13, kMuted);
            DrawUiText(reward_view.State() == RewardState::kOffered ? ui::kClickOrDigits :
                       reward_view.State() == RewardState::kChosen ? ui::kWaitInstall :
                       reward_view.State() == RewardState::kApplied ? ui::kInstalled : ui::kWaitServer,
                       32, 678, 18, kGold);
            DrawUiText(ui::kNextSector, 492, 656, 13, kMuted);
            DrawUiText(reward_view.State() == RewardState::kApplied ?
                           (demo.ready_sent ? ui::kWaitAlly : ui::kPressReady) :
                           ui::kWaitUpgrade, 492, 678, 18, kStarlight);
        } else {
            DrawUiText(ui::kMove, 32, 656, 13, kMuted);
            DrawText("WASD", 32, 678, 18, kStarlight);
            DrawUiText(ui::kAim, 160, 656, 13, kMuted);
            DrawUiText(ui::kMouse, 160, 678, 18, kStarlight);
            DrawUiText(ui::kFire, 300, 656, 13, kMuted);
            DrawUiText(ui::kHoldSpace, 300, 678, 18, kGold);
            DrawUiText(ui::kRestart, 492, 656, 13, kMuted);
            DrawUiText(ui::kNAfterDefeat, 492, 678, 18, kStarlight);
        }
        DrawUiText(ui::kExit, 735, 656, 13, kMuted);
        DrawText("ESC", 735, 678, 18, kStarlight);
        DrawUiText(ui::kDeveloper, 1014, 656, 13, kMuted);
        DrawText("F3", 1014, 678, 18, show_debug ? kCyan : kStarlight);

        if (show_debug) {
            const char* recovery_phase = "idle";
            switch (recovery.Phase()) {
                case RecoveryPhase::kIdle: recovery_phase = "idle"; break;
                case RecoveryPhase::kWaitingToRetry: recovery_phase = "waiting"; break;
                case RecoveryPhase::kConnecting: recovery_phase = "connecting"; break;
                case RecoveryPhase::kResuming: recovery_phase = "resuming"; break;
                case RecoveryPhase::kRestored: recovery_phase = "restored"; break;
                case RecoveryPhase::kFailed: recovery_phase = "failed"; break;
            }
            char correction_text[32] = {0};
            std::snprintf(correction_text, sizeof(correction_text), "%.3f",
                          predictor.LastCorrectionDistance());
            std::vector<std::string> debug_lines{
                std::string("Connection  ") + ToString(demo.state) + "  " + demo.state_detail,
                std::string("Recovery    ") + recovery_phase + "  attempts=" +
                    std::to_string(recovery.Attempts()) + "  token=" +
                    std::to_string(demo.resume_token.size()) + "B  " + recovery.Note(),
                "Identity    session=" + std::to_string(demo.session_id) + " player=" +
                    std::to_string(demo.player_id) + " room=" + std::to_string(demo.room_id),
                "Inbound     type=" + std::to_string(demo.last_type) + " seq=" +
                    std::to_string(demo.last_sequence) + " bytes=" +
                    std::to_string(demo.last_payload_bytes),
                "Heartbeat   sent=" + std::to_string(demo.pings_sent) + " pong=" +
                    std::to_string(demo.pong_nonce) + " server_ms=" +
                    std::to_string(demo.pong_server_time_ms),
                "Transport   outbound_drops=" + std::to_string(demo.outbound_drops),
                "Input       dx=" + std::to_string(last_sample.dx) + " dz=" +
                    std::to_string(last_sample.dz) + " seq=" +
                    std::to_string(last_report.sequence) + " shoot=" +
                    (last_shoot ? "yes" : "no"),
                "View        players=" + std::to_string(game_view.PlayerCount()) + " tick=" +
                    std::to_string(game_view.ServerTick()) + " snapshots=" +
                    std::to_string(demo.snapshots_received),
                "Stage       index=" + std::to_string(demo.stage_index) + " state=" +
                    StageStateName(demo.stage_state) + " remain=" +
                    std::to_string(demo.monsters_remaining),
                "Combat      monsters=" + std::to_string(combat_view.MonsterCount()) +
                    " projectiles=" + std::to_string(combat_view.ProjectileCount()) +
                    " events=" + std::to_string(demo.spawns) + "/" +
                    std::to_string(demo.destroys) + "/" + std::to_string(demo.damages) +
                    "/" + std::to_string(demo.deaths),
                "Stats       hp=" + std::to_string(static_cast<int>(demo.self_hp)) + "/" +
                    std::to_string(static_cast<int>(demo.self_max_hp)) + " atk=" +
                    std::to_string(static_cast<int>(demo.self_attack)) + " def=" +
                    std::to_string(static_cast<int>(demo.self_defense)) + " speed=" +
                    std::to_string(static_cast<int>(demo.self_move_speed)),
                std::string("Netcode     pending=") + std::to_string(predictor.PendingCount()) +
                    " correction=" + correction_text + " delay=" +
                    std::to_string(static_cast<int>(remote_interp.DelayTicks())) + "t tracks=" +
                    std::to_string(remote_interp.Count()) + "/" +
                    std::to_string(monster_interp.Count()),
                "Last event  " + demo.last_event_note,
            };
            if (!demo.server_note.empty()) debug_lines.push_back("Server      " + demo.server_note);

            const Rectangle debug_panel{20.0f, 110.0f, 720.0f, 514.0f};
            DrawPanel(debug_panel, Fade(kVoid, 0.97f), Fade(kCyan, 0.85f));
            DrawText("DEVELOPER TELEMETRY", 40, 130, 21, kCyan);
            DrawText("F3  CLOSE", 616, 134, 14, kMuted);
            int debug_y = 174;
            for (const auto& line : debug_lines) {
                DrawText(ShortText(line, 78).c_str(), 40, debug_y, 17, kStarlight);
                debug_y += 29;
            }
            DrawFPS(640, 582);
        }

        rlPopMatrix();

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
    if (IsFontValid(gChineseFont)) UnloadFont(gChineseFont);
    CloseWindow();
    std::printf("main: exit\n"); fflush(stdout);
    return 0;
}
