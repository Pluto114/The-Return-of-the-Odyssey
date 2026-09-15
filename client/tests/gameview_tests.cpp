// Headless tests for the D2-direction logic slice: input normalization &
// sequencing, and full-snapshot application semantics. No window, no sockets.
#include "input/InputSample.h"
#include "sync/CombatView.h"
#include "sync/GameView.h"
#include "sync/Interpolation.h"
#include "sync/Prediction.h"
#include "sync/RecoveryState.h"
#include "sync/RewardView.h"
#include "sync/SessionGate.h"
#include "sync/StageSummary.h"
#include "ui/AssetPath.h"
#include "ui/FloaterPool.h"
#include "ui/HealthBar.h"
#include "ui/HudMath.h"
#include "ui/Metrics.h"
#include "ui/PerfCapture.h"
#include "ui/Theme.h"
#include "ui/UiGeometry.h"

#include <cmath>
#include <cstdint>
#include <cstdio>
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

using odyssey::client::input::InputReport;
using odyssey::client::input::InputSample;
using odyssey::client::input::InputSequencer;
using odyssey::client::input::NormalizeInput;
using odyssey::client::sync::CombatView;
using odyssey::client::sync::AuthoritativeRewardPhaseEnded;
using odyssey::client::sync::CanReportReady;
using odyssey::client::sync::CanSendInput;
using odyssey::client::sync::EquipmentDisplay;
using odyssey::client::sync::EquipmentTable;
using odyssey::client::sync::GameView;
using odyssey::client::sync::InputCommand;
using odyssey::client::sync::InputGate;
using odyssey::client::sync::InputSeqFloor;
using odyssey::client::sync::InputBlockReason;
using odyssey::client::sync::IsPreparingNextStage;
using odyssey::client::sync::kArenaMax;
using odyssey::client::sync::kModOpAdd;
using odyssey::client::sync::kModOpMultiply;
using odyssey::client::sync::kModStatAttack;
using odyssey::client::sync::kModStatDefense;
using odyssey::client::sync::kModStatMaxHealth;
using odyssey::client::sync::kModStatMoveSpeed;
using odyssey::client::sync::kModTargetMonster;
using odyssey::client::sync::kModTargetPlayer;
using odyssey::client::sync::kMoveSpeedUnitsPerSecond;
using odyssey::client::sync::kSimulationStepSeconds;
using odyssey::client::sync::MonsterEntity;
using odyssey::client::sync::MovementPredictor;
using odyssey::client::sync::ParseEquipmentTable;
using odyssey::client::sync::PlayerView;
using odyssey::client::sync::ProjectileVisual;
using odyssey::client::sync::ReadyBlockReason;
using odyssey::client::sync::RecoveryPhase;
using odyssey::client::sync::RecoveryState;
using odyssey::client::sync::RewardSettled;
using odyssey::client::sync::RewardState;
using odyssey::client::sync::RewardView;
using odyssey::client::sync::SnapshotView;
using odyssey::client::sync::SnapshotInterpolator;
using odyssey::client::sync::StageInfo;
using odyssey::client::sync::StageState;
using odyssey::client::sync::StageStateName;
using odyssey::client::sync::StageSummary;
using odyssey::client::sync::StepMovement;
using odyssey::client::ui::AccessibilityConfig;
using odyssey::client::ui::AssetRootCandidates;
using odyssey::client::ui::ChooseAssetRoot;
using odyssey::client::ui::ChooseSettingsDirectory;
using odyssey::client::ui::ComputeHealthSegments;
using odyssey::client::ui::ComputeViewportLayout;
using odyssey::client::ui::DamageDedupeTable;
using odyssey::client::ui::Floater;
using odyssey::client::ui::FloaterKey;
using odyssey::client::ui::FloaterPool;
using odyssey::client::ui::IsInsideTarget;
using odyssey::client::ui::JoinPath;using odyssey::client::ui::kDefaultAccessibility;
using odyssey::client::ui::kDefaultTheme;
using odyssey::client::ui::kTargetHeight;
using odyssey::client::ui::kTargetWidth;
using odyssey::client::ui::kWorldSize;
using odyssey::client::ui::Rectf;
using odyssey::client::ui::Rgba;
using odyssey::client::ui::RgbaFromHex;
using odyssey::client::ui::RTToWorld;
using odyssey::client::ui::SegmentWidth;
using odyssey::client::ui::SettingsFilePathIn;
using odyssey::client::ui::Theme;
using odyssey::client::ui::DamageGhost;
using odyssey::client::ui::DamageShakeOffset;
using odyssey::client::ui::Ema;
using odyssey::client::ui::MetricSeries;
using odyssey::client::ui::PerfCapture;
using odyssey::client::ui::PerfSummary;
using odyssey::client::ui::PredictionErrorEstimator;
using odyssey::client::ui::RttEstimator;
using odyssey::client::ui::ServerTickRateEstimator;
using odyssey::client::ui::HexagonCrosshair;
using odyssey::client::ui::HitMarker;
using odyssey::client::ui::HitMarkerFade;
using odyssey::client::ui::kCrosshairPoints;
using odyssey::client::ui::OnHealthFraction;
using odyssey::client::ui::SetHitDirection;
using odyssey::client::ui::UpdateDamageGhost;
using odyssey::client::ui::UpdateHitMarker;
using odyssey::client::ui::Vec2f;
using odyssey::client::ui::WindowToRT;
using odyssey::client::ui::WorldToRT;
using odyssey::client::ui::WorldToWindow;

constexpr float kEps = 1e-5f;

void TestNormalizeIdle() {
    const auto v = NormalizeInput(InputSample{});
    CHECK(v.x == 0.0f);
    CHECK(v.z == 0.0f);
}

void TestNormalizeAxes() {
    const auto east = NormalizeInput(InputSample{1, 0});
    CHECK(std::fabs(east.x - 1.0f) < kEps);
    CHECK(east.z == 0.0f);

    const auto north = NormalizeInput(InputSample{0, -1});
    CHECK(north.x == 0.0f);
    CHECK(std::fabs(north.z + 1.0f) < kEps);
}

void TestNormalizeDiagonalNotFaster() {
    const auto diag = NormalizeInput(InputSample{1, 1});
    const float expected = 1.0f / std::sqrt(2.0f);
    CHECK(std::fabs(diag.x - expected) < kEps);
    CHECK(std::fabs(diag.z - expected) < kEps);
    const float length = std::sqrt(diag.x * diag.x + diag.z * diag.z);
    CHECK(std::fabs(length - 1.0f) < kEps);
}

void TestSequencerCountsEveryTick() {
    InputSequencer sequencer;
    CHECK(!sequencer.HasSent());

    const auto first = sequencer.Tick(InputSample{1, 0});
    CHECK(first.sequence == 1);
    CHECK(std::fabs(first.vector.x - 1.0f) < kEps);

    const auto second = sequencer.Tick(InputSample{});
    CHECK(second.sequence == 2);
    CHECK(second.vector.x == 0.0f);
    CHECK(second.vector.z == 0.0f);
    CHECK(sequencer.LastSequence() == 2);
    sequencer.Reset();
    CHECK(!sequencer.HasSent());
    CHECK(sequencer.Tick(InputSample{}).sequence == 1);
}

void TestGameViewApplyAndRemoveMissing() {
    GameView view;
    SnapshotView snap;
    snap.room_id = 10;
    snap.server_tick = 100;
    snap.players = {
        PlayerView{1, 2.0f, 3.0f, 0.0f, 0.0f, 7},
        PlayerView{2, 5.0f, 1.0f, 0.0f, 0.0f, 0},
    };
    const auto removed = view.Apply(snap);
    CHECK(removed.empty());
    CHECK(view.RoomId() == 10);
    CHECK(view.ServerTick() == 100);
    CHECK(view.PlayerCount() == 2);
    CHECK(view.Players().size() == 2);

    const PlayerView* self = view.Find(1);
    CHECK(self != nullptr);
    if (self) {
        CHECK(self->x == 2.0f);
        CHECK(self->last_processed_input_seq == 7);
    }
    CHECK(view.SelfAck(1).has_value());
    CHECK(view.SelfAck(1).value() == 7);

    // A later full snapshot without player 1 must remove it.
    SnapshotView next;
    next.room_id = 10;
    next.server_tick = 103;
    next.players = {PlayerView{2, 6.0f, 1.5f, 0.0f, 0.0f, 0}};
    const auto removed2 = view.Apply(next);
    CHECK(removed2.size() == 1);
    CHECK(removed2[0] == 1);
    CHECK(view.PlayerCount() == 1);
    CHECK(view.Find(1) == nullptr);
    CHECK(view.SelfAck(1) == std::nullopt);
}

void TestGameViewClosedEmpties() {
    GameView view;
    SnapshotView snap;
    snap.room_id = 3;
    snap.server_tick = 50;
    snap.players = {PlayerView{1, 1.0f, 1.0f, 0.0f, 0.0f, 0},
                    PlayerView{9, 2.0f, 2.0f, 0.0f, 0.0f, 0}};
    view.Apply(snap);
    CHECK(view.PlayerCount() == 2);

    SnapshotView closed;
    closed.room_id = 3;
    closed.server_tick = 60;
    closed.closed = true;
    const auto removed = view.Apply(closed);
    CHECK(removed.size() == 2);
    CHECK(view.PlayerCount() == 0);
    CHECK(view.IsClosed());
}

void TestGameViewDefensiveSort() {
    GameView view;
    SnapshotView snap;
    snap.room_id = 1;
    snap.server_tick = 1;
    // Deliberately unsorted input.
    snap.players = {PlayerView{5, 0.0f, 0.0f, 0.0f, 0.0f, 0},
                    PlayerView{2, 0.0f, 0.0f, 0.0f, 0.0f, 0},
                    PlayerView{7, 0.0f, 0.0f, 0.0f, 0.0f, 0}};
    view.Apply(snap);
    CHECK(view.PlayerCount() == 3);
    // Removing player 2+7 in one go works because ids were sorted internally.
    SnapshotView next;
    next.room_id = 1;
    next.server_tick = 2;
    next.players = {PlayerView{5, 1.0f, 1.0f, 0.0f, 0.0f, 0}};
    const auto removed = view.Apply(next);
    CHECK(removed.size() == 2);
    CHECK(view.PlayerCount() == 1);
}

void TestCombatViewMonstersFullSet() {
    CombatView view;
    std::vector<MonsterEntity> first(2);
    first[0].id = 900;
    first[0].x = 1.0f;
    first[0].hp = 50.0f;
    first[1].id = 901;
    first[1].x = 2.0f;
    CHECK(view.ApplyMonsters(first).empty());
    CHECK(view.MonsterCount() == 2);
    const MonsterEntity* monster = view.FindMonster(900);
    CHECK(monster != nullptr);
    if (monster) {
        CHECK(monster->x == 1.0f);
        CHECK(monster->hp == 50.0f);
    }

    // Newest snapshot is a FULL set: 900 disappears, 902 appears.
    std::vector<MonsterEntity> second(2);
    second[0].id = 901;
    second[1].id = 902;
    const auto removed = view.ApplyMonsters(second);
    CHECK(removed.size() == 1);
    CHECK(removed[0] == 900);
    CHECK(view.MonsterCount() == 2);
    CHECK(view.FindMonster(900) == nullptr);
    CHECK(view.FindMonster(902) != nullptr);

    view.Clear();
    CHECK(view.MonsterCount() == 0);
}

void TestCombatViewProjectilesAndStage() {
    CombatView view;
    ProjectileVisual projectile;
    projectile.id = 42;
    projectile.owner_id = 10;
    projectile.x = 3.0f;
    projectile.z = 4.0f;
    projectile.expires_at_tick = 500;
    view.SpawnProjectile(projectile);
    CHECK(view.ProjectileCount() == 1);
    CHECK(view.Projectiles().at(42).x == 3.0f);

    // Duplicate spawn (should not happen with one dispatcher) overwrites, and
    // destroy of an unknown id is a no-op.
    CHECK(!view.DestroyProjectile(99));
    CHECK(view.DestroyProjectile(42));
    CHECK(view.ProjectileCount() == 0);

    StageInfo stage;
    stage.index = 2;
    stage.state = 1;
    stage.monsters_remaining = 3;
    view.SetStage(stage);
    CHECK(view.Stage().index == 2);
    CHECK(view.Stage().monsters_remaining == 3);
}

void TestGameViewCombatFields() {
    GameView view;
    SnapshotView snap;
    snap.room_id = 1;
    snap.server_tick = 10;
    PlayerView player;
    player.id = 7;
    player.hp = 40.0f;
    player.max_hp = 100.0f;
    player.alive = false;
    snap.players = {player};
    view.Apply(snap);
    const PlayerView* stored = view.Find(7);
    CHECK(stored != nullptr);
    if (stored) {
        CHECK(stored->hp == 40.0f);
        CHECK(stored->max_hp == 100.0f);
        CHECK(!stored->alive);
    }
}

void TestCombatViewFeedback() {
    CombatView view;
    // Damage feedback is transient: it decays away after the flash window.
    view.ApplyDamageFx(900);
    CHECK(view.IsHitFlashing(900));
    CHECK(view.HitFlashCount() == 1);
    view.Tick(0.1f);
    CHECK(view.IsHitFlashing(900));
    view.Tick(0.5f);
    CHECK(!view.IsHitFlashing(900));
    CHECK(view.HitFlashCount() == 0);

    // Death marks persist until Clear (snapshot is authoritative for removal).
    view.ApplyDeath(900);
    CHECK(view.IsDead(900));
    CHECK(!view.IsDead(901));
    view.Clear();
    CHECK(!view.IsDead(900));
}

void TestParseEquipmentTable() {
    EquipmentTable table;
    // Shape of the CMake-generated table: tab-separated, comment header, blanks.
    const std::string text =
        "# Generated from data/equipment/catalog.json; do not edit.\n"
        "# catalog-version=1\n"
        "1001\tIron Sidearm\tweapon\tAttack +5\n"
        "\n"
        "2002\tWind Relic\trelic\tMove speed x1.1, stacks with relics\n"
        "3001\tHealing Potion\tpotion\tRestore 30 health\n"
        // Malformed rows are skipped rather than guessed at: too few fields, too
        // many, a non-numeric id, and a line with no tabs at all.
        "4001\tPlain Blade\tweapon\n"
        "5001\tToo Many\tweapon\tdescription\textra\n"
        "not-a-number\tBroken\tweapon\tnothing\n"
        "no tabs here\n";
    const std::size_t loaded = ParseEquipmentTable(text, table);
    CHECK(loaded == 3);
    CHECK(table.size() == 3);

    const auto first = table.find(1001);
    CHECK(first != table.end());
    if (first != table.end()) {
        CHECK(first->second.name == "Iron Sidearm");
        CHECK(first->second.slot == "weapon");
        CHECK(first->second.description == "Attack +5");
    }

    const auto with_comma = table.find(2002);
    CHECK(with_comma != table.end());
    if (with_comma != table.end()) {
        // Tabs are the separator, so commas inside the description are literal.
        CHECK(with_comma->second.description == "Move speed x1.1, stacks with relics");
    }

    const auto potion = table.find(3001);
    CHECK(potion != table.end());
    if (potion != table.end()) {
        CHECK(potion->second.name == "Healing Potion");
        CHECK(potion->second.description == "Restore 30 health");
    }

    // The malformed rows contributed nothing.
    CHECK(table.find(4001) == table.end());
    CHECK(table.find(5001) == table.end());
}

void TestRewardViewFlow() {
    EquipmentTable table;
    ParseEquipmentTable("2001\tVitality Relic\trelic\tMax health +25\n", table);

    RewardView view;
    view.SetOptions({2001, 99}, 4000, table);
    CHECK(view.State() == RewardState::kOffered);
    CHECK(view.Active());
    CHECK(view.Options().size() == 2);
    CHECK(view.Options()[0].display.name == "Vitality Relic");
    // Unknown id degrades to a placeholder instead of inventing stats.
    CHECK(view.Options()[1].display.name == "equipment#99");
    CHECK(view.Options()[1].display.slot == "(config pending)");
    CHECK(view.DeadlineTick() == 4000);

    std::uint32_t chosen = 0;
    CHECK(!view.ChooseByIndex(2, chosen));  // out of range: still offered
    CHECK(view.State() == RewardState::kOffered);
    CHECK(view.ChooseByIndex(0, chosen));
    CHECK(chosen == 2001u);
    CHECK(view.State() == RewardState::kChosen);
    // A second choice is refused locally (single-shot).
    CHECK(!view.ChooseByIndex(1, chosen));

    // Refusal is reported honestly.
    view.ApplyResult(false, 5, 200);
    CHECK(view.State() == RewardState::kRejected);
    CHECK(view.Note().find("refused") != std::string::npos);

    // Deadline expiry only affects an unanswered offer.
    RewardView timed;
    timed.SetOptions({5}, 100, table);
    timed.Timeout();
    CHECK(timed.State() == RewardState::kTimedOut);
    CHECK(!timed.Active());
    timed.ApplyResult(true, 5, 1);
    CHECK(timed.State() == RewardState::kApplied);

    // Clearing resets everything (stage change / disconnect).
    view.Clear();
    CHECK(view.State() == RewardState::kNone);
    CHECK(view.Options().empty());
    CHECK(!view.Active());
}

void TestRecoveryStateFlow() {
    RecoveryState recovery;
    recovery.SetToken({1, 2, 3});
    CHECK(recovery.HasToken());

    recovery.OnDisconnect(0.0);
    CHECK(recovery.Phase() == RecoveryPhase::kWaitingToRetry);
    CHECK(recovery.ShouldRetry(0.0));         // first retry immediately
    recovery.MarkRetryStarted(0.0);
    CHECK(recovery.Attempts() == 1);
    CHECK(recovery.Phase() == RecoveryPhase::kConnecting);

    // The attempt failed: the next retry honours the backoff (2s -> 4s ...)
    // instead of resetting it, so repeated failures do not hammer the server.
    recovery.OnDisconnect(1.0);
    CHECK(!recovery.ShouldRetry(4.0));        // next attempt at 1.0 + 4.0
    CHECK(recovery.ShouldRetry(5.0));

    recovery.OnConnected();
    CHECK(recovery.Phase() == RecoveryPhase::kResuming);
    CHECK(recovery.WantsResumeRequest());
    CHECK(!recovery.HandshakePending());
    recovery.MarkResumeSent(5.0);
    CHECK(!recovery.WantsResumeRequest());    // exactly one per connection
    CHECK(recovery.HandshakePending());
    CHECK(std::fabs(recovery.HandshakeSecondsLeft(6.0) -
                    (RecoveryState::kHandshakeTimeoutSeconds - 1.0)) < kEps);

    recovery.OnResumeResult(true);
    CHECK(recovery.Phase() == RecoveryPhase::kRestored);
    CHECK(!recovery.Active());
    CHECK(!recovery.HandshakePending());
    CHECK(recovery.Attempts() == 0);

    // Refused token: cleared so the caller performs a fresh login and never
    // replays the old session's inputs.
    RecoveryState refused;
    refused.SetToken({9});
    refused.OnDisconnect(0.0);
    refused.MarkRetryStarted(0.0);
    refused.OnConnected();
    refused.OnResumeResult(false);
    CHECK(refused.Phase() == RecoveryPhase::kFailed);
    CHECK(!refused.HasToken());

    // Retries are bounded: after kMaxAttempts the loop must stop and ask the
    // user (R) instead of hammering the server.
    RecoveryState exhausted;
    exhausted.OnDisconnect(0.0);
    double now = 0.0;
    for (int i = 0; i < RecoveryState::kMaxAttempts; ++i) {
        CHECK(exhausted.ShouldRetry(now));
        exhausted.MarkRetryStarted(now);
        now += RecoveryState::kMaxBackoffSeconds;  // past this attempt's slot
        exhausted.OnDisconnect(now);               // attempt failed
        if (i + 1 < RecoveryState::kMaxAttempts) {
            now += RecoveryState::kMaxBackoffSeconds;  // wait out the backoff
        }
    }
    CHECK(exhausted.Exhausted());
    // A5 item C-f: the bound is a real terminal state, not a silent stall.
    CHECK(exhausted.Phase() == RecoveryPhase::kExhausted);
    CHECK(!exhausted.ShouldRetry(now + 1000.0));
    CHECK(exhausted.Note().find("press R") != std::string::npos);

    recovery.Reset();
    CHECK(recovery.Phase() == RecoveryPhase::kIdle);
    CHECK(!recovery.HandshakePending());
    CHECK(recovery.Attempts() == 0);
}

void TestRecoveryHandshakeTimeout() {
    // A ResumeRequest that is never answered must not leave the client waiting
    // forever on a connection that is silently useless (A5 item C-f).
    RecoveryState recovery;
    recovery.SetToken({1, 2, 3});
    recovery.OnDisconnect(0.0);
    recovery.MarkRetryStarted(0.0);
    recovery.OnConnected();
    recovery.MarkResumeSent(10.0);

    CHECK(!recovery.HandshakeTimedOut(10.0 + RecoveryState::kHandshakeTimeoutSeconds - 0.1));
    CHECK(recovery.HandshakeTimedOut(10.0 + RecoveryState::kHandshakeTimeoutSeconds));
    CHECK(recovery.HandshakeTimedOut(10.0 + RecoveryState::kHandshakeTimeoutSeconds + 0.1));

    recovery.OnHandshakeTimeout(10.0 + RecoveryState::kHandshakeTimeoutSeconds);
    CHECK(recovery.Phase() == RecoveryPhase::kFailed);
    CHECK(!recovery.HasToken());
    CHECK(!recovery.HandshakePending());
    // The connection attempt counted once and the silent handshake counts again:
    // both consume the same bounded budget.
    CHECK(recovery.Attempts() == 2);
    CHECK(recovery.Note().find("logging in again") != std::string::npos);
    // The timeout is no longer reported once handled.
    CHECK(!recovery.HandshakeTimedOut(10.0 + 100.0));

    // A fresh LoginRequest is itself bounded: repeated silence exhausts the same
    // attempt budget instead of looping forever.
    RecoveryState silent_login;
    silent_login.OnConnected();  // no token -> fresh login
    CHECK(silent_login.Phase() == RecoveryPhase::kConnecting);
    double now = 0.0;
    int timeouts = 0;
    while (silent_login.Phase() != RecoveryPhase::kExhausted && timeouts < 20) {
        silent_login.MarkLoginSent(now);
        now += RecoveryState::kHandshakeTimeoutSeconds;
        CHECK(silent_login.HandshakeTimedOut(now));
        silent_login.OnHandshakeTimeout(now);
        ++timeouts;
    }
    CHECK(timeouts == RecoveryState::kMaxAttempts);
    CHECK(silent_login.Exhausted());
    CHECK(!silent_login.HandshakeTimedOut(now + 1000.0));
}

void TestRecoverySecondDropIsRecoverable() {
    // Two outages in a row: after a successful resume the machine must be able to
    // start over (attempts and backoff reset), not stay latched in kRestored.
    RecoveryState recovery;
    recovery.SetToken({7});
    recovery.OnDisconnect(0.0);
    recovery.MarkRetryStarted(0.0);
    recovery.OnConnected();
    recovery.MarkResumeSent(1.0);
    recovery.OnResumeResult(true);
    CHECK(recovery.Phase() == RecoveryPhase::kRestored);

    // Second drop while restored: retry immediately with a fresh attempt budget.
    recovery.OnDisconnect(20.0);
    CHECK(recovery.Phase() == RecoveryPhase::kWaitingToRetry);
    CHECK(recovery.Attempts() == 0);
    CHECK(recovery.ShouldRetry(20.0));
    CHECK(recovery.HasToken());  // token rotation is an A4 dependency; keep trying

    recovery.MarkRetryStarted(20.0);
    recovery.OnConnected();
    CHECK(recovery.Phase() == RecoveryPhase::kResuming);
    CHECK(recovery.WantsResumeRequest());
    recovery.MarkResumeSent(20.5);
    recovery.OnResumeResult(false);
    CHECK(recovery.Phase() == RecoveryPhase::kFailed);
    CHECK(!recovery.HasToken());

    // A third drop from the failed state still retries (fresh login path).
    recovery.OnDisconnect(21.0);
    CHECK(recovery.Phase() == RecoveryPhase::kWaitingToRetry);
    CHECK(recovery.ShouldRetry(21.0));

    // A retry that drops while resuming keeps the attempt budget growing.
    recovery.MarkRetryStarted(21.0);
    recovery.OnConnected();
    recovery.OnDisconnect(22.0);
    CHECK(recovery.Phase() == RecoveryPhase::kWaitingToRetry);
    CHECK(recovery.Attempts() == 1);
    CHECK(!recovery.ShouldRetry(22.0));
    CHECK(recovery.ShouldRetry(22.0 + RecoveryState::kMaxBackoffSeconds));
}

void TestMovementPredictorReconciliation() {
    MovementPredictor predictor;
    // First snapshot anchors the tick timeline and adopts the server pose.
    predictor.ApplyAuthoritative(5.0f, 7.0f, 0, 100, 5.0f, true);
    CHECK(predictor.HasPrediction());
    CHECK(predictor.PredictedTick() == 100);
    CHECK(predictor.AckSeq() == 0);
    CHECK(predictor.PendingCount() == 0);
    CHECK(std::fabs(predictor.MoveSpeed() - 5.0f) < kEps);

    // Two ticks of intent at 5 units/s => 5/30 per tick.
    predictor.RecordInput(InputCommand{1, 1.0f, 0.0f});
    predictor.AdvanceTick();
    predictor.RecordInput(InputCommand{2, 1.0f, 0.0f});
    predictor.AdvanceTick();
    CHECK(predictor.PredictedTick() == 102);
    CHECK(predictor.PendingCount() == 2);
    CHECK(std::fabs(predictor.X() - (5.0f + 10.0f / 30.0f)) < kEps);

    // A snapshot generated at tick 101 acks input 1: one tick of prediction was
    // simulated past it and must be re-applied from the authoritative pose.
    predictor.ApplyAuthoritative(5.0f, 7.0f, 1, 101, 5.0f, true);
    CHECK(predictor.PendingCount() == 1);
    CHECK(predictor.AckSeq() == 1);
    CHECK(predictor.PredictedTick() == 102);
    CHECK(std::fabs(predictor.X() - (5.0f + 5.0f / 30.0f)) < kEps);
    CHECK(std::fabs(predictor.Z() - 7.0f) < kEps);
    CHECK(predictor.LastCorrectionDistance() > 0.0f);

    // Everything acked and the snapshot covers our whole timeline: exact pose.
    predictor.ApplyAuthoritative(5.0f, 7.0f, 2, 102, 5.0f, true);
    CHECK(predictor.PendingCount() == 0);
    CHECK(std::fabs(predictor.X() - 5.0f) < kEps);
    CHECK(std::fabs(predictor.Z() - 7.0f) < kEps);
    CHECK(predictor.PredictedTick() == 102);

    // A snapshot after our timeline moves the anchor forward without replaying.
    predictor.ApplyAuthoritative(6.0f, 7.0f, 2, 105, 5.0f, true);
    CHECK(predictor.PredictedTick() == 105);
    CHECK(std::fabs(predictor.X() - 6.0f) < kEps);

    // Reset (new session / resume) drops predictions entirely.
    predictor.Reset();
    CHECK(!predictor.HasPrediction());
    CHECK(predictor.PendingCount() == 0);
    CHECK(predictor.PredictedTick() == 0);
}

void TestPredictorStepsPerTickNotPerPacket() {
    // A5 item C-d: the server applies one step per tick using the newest intent,
    // so ten inputs inside one tick must still produce exactly one step.
    MovementPredictor predictor;
    predictor.ApplyAuthoritative(0.0f, 0.0f, 0, 100, 5.0f, true);
    for (std::uint32_t seq = 1; seq <= 10; ++seq) {
        predictor.RecordInput(InputCommand{seq, 1.0f, 0.0f});
    }
    predictor.AdvanceTick();
    CHECK(std::fabs(predictor.X() - (5.0f / 30.0f)) < kEps);
    CHECK(predictor.PendingCount() == 10);

    // ... and one tick later the newest intent still applies exactly once.
    predictor.RecordInput(InputCommand{11, 1.0f, 0.0f});
    predictor.AdvanceTick();
    CHECK(std::fabs(predictor.X() - (2.0f * 5.0f / 30.0f)) < kEps);

    // 30 more packets but no tick: the position must not move at all.
    const float before = predictor.X();
    for (std::uint32_t seq = 12; seq <= 41; ++seq) {
        predictor.RecordInput(InputCommand{seq, 1.0f, 0.0f});
    }
    CHECK(std::fabs(predictor.X() - before) < kEps);
}

void TestPredictorUsesServerMoveSpeed() {
    MovementPredictor predictor;
    predictor.ApplyAuthoritative(0.0f, 0.0f, 0, 100, 5.0f, true);
    // Equipment that raises MoveSpeed to 7.5 changes the predicted step with it.
    predictor.ApplyAuthoritative(0.0f, 0.0f, 0, 100, 7.5f, true);
    CHECK(std::fabs(predictor.MoveSpeed() - 7.5f) < kEps);
    predictor.RecordInput(InputCommand{1, 1.0f, 0.0f});
    predictor.AdvanceTick();
    CHECK(std::fabs(predictor.X() - (7.5f / 30.0f)) < kEps);

    // Nonsensical speeds cannot come from a real player state: keep the last
    // valid value instead of freezing or teleporting prediction.
    predictor.ApplyAuthoritative(0.0f, 0.0f, 0, 100, 0.0f, true);
    CHECK(std::fabs(predictor.MoveSpeed() - 7.5f) < kEps);
    predictor.ApplyAuthoritative(0.0f, 0.0f, 0, 100, -3.0f, true);
    CHECK(std::fabs(predictor.MoveSpeed() - 7.5f) < kEps);
}

void TestPredictorStationary() {
    MovementPredictor predictor;
    predictor.ApplyAuthoritative(4.0f, 4.0f, 0, 50, 5.0f, true);
    // Released keys produce a zero-intent report: no movement, no correction.
    predictor.RecordInput(InputCommand{1, 0.0f, 0.0f});
    predictor.AdvanceTick();
    predictor.AdvanceTick();
    CHECK(std::fabs(predictor.X() - 4.0f) < kEps);
    CHECK(std::fabs(predictor.Z() - 4.0f) < kEps);
    predictor.ApplyAuthoritative(4.0f, 4.0f, 1, 52, 5.0f, true);
    CHECK(predictor.LastCorrectionDistance() < kEps);
}

void TestPredictorDeadPlayerDoesNotAdvance() {
    MovementPredictor predictor;
    predictor.ApplyAuthoritative(3.0f, 3.0f, 0, 60, 5.0f, true);
    predictor.RecordInput(InputCommand{1, 1.0f, 0.0f});
    predictor.AdvanceTick();
    CHECK(std::fabs(predictor.X() - (3.0f + 5.0f / 30.0f)) < kEps);

    // Death: the server does not move a corpse, so prediction must stop too.
    predictor.ApplyAuthoritative(3.0f, 3.0f, 1, 61, 5.0f, false);
    CHECK(!predictor.Alive());
    predictor.RecordInput(InputCommand{2, 1.0f, 0.0f});
    predictor.AdvanceTick();
    predictor.AdvanceTick();
    CHECK(std::fabs(predictor.X() - 3.0f) < kEps);
    CHECK(std::fabs(predictor.Z() - 3.0f) < kEps);
    // The tick timeline keeps running even while dead.
    CHECK(predictor.PredictedTick() == 63);

    // Revival resumes prediction from the authoritative pose.
    predictor.ApplyAuthoritative(8.0f, 3.0f, 2, 64, 5.0f, true);
    CHECK(predictor.Alive());
    predictor.RecordInput(InputCommand{3, 1.0f, 0.0f});
    predictor.AdvanceTick();
    CHECK(std::fabs(predictor.X() - (8.0f + 5.0f / 30.0f)) < kEps);
}

void TestPredictorStageChangeJump() {
    MovementPredictor predictor;
    predictor.ApplyAuthoritative(18.0f, 18.0f, 0, 200, 5.0f, true);
    predictor.RecordInput(InputCommand{1, 1.0f, 1.0f});
    predictor.AdvanceTick();

    // A new stage teleports the player to the spawn point: the correction is
    // large, but with nothing owed on the tick timeline the pose is exact.
    predictor.ApplyAuthoritative(1.0f, 1.0f, 1, 201, 5.0f, true);
    CHECK(predictor.LastCorrectionDistance() > 1.0f);
    CHECK(std::fabs(predictor.X() - 1.0f) < kEps);
    CHECK(std::fabs(predictor.Z() - 1.0f) < kEps);
    CHECK(predictor.PredictedTick() == 201);
}

void TestPredictorRejectsAbsurdTickGap() {
    MovementPredictor predictor;
    predictor.ApplyAuthoritative(0.0f, 0.0f, 0, 1000, 5.0f, true);
    for (int i = 0; i < 5; ++i) {
        predictor.AdvanceTick();
    }
    predictor.RecordInput(InputCommand{1, 1.0f, 0.0f});
    CHECK(predictor.PredictedTick() == 1005);

    // A stale snapshot (older than what we already simulated by a lot) must not
    // replay an unbounded number of steps: accept its tick and stay put.
    predictor.ApplyAuthoritative(0.0f, 0.0f, 0, 100, 5.0f, true);
    CHECK(predictor.PredictedTick() == 100);
    CHECK(std::fabs(predictor.X()) < kEps);
    CHECK(std::fabs(predictor.Z()) < kEps);
}

void TestStepMovementRules() {
    // Diagonal is length-limited: 30 ticks diagonal == 30 ticks straight. The
    // speed is passed in (server-authoritative), so the expectation tracks it.
    const float speed = kMoveSpeedUnitsPerSecond;
    float dx = 0.0f;
    float dz = 0.0f;
    {
        auto [x1, z1] = StepMovement(0.0f, 0.0f, 1.0f, 0.0f, speed, 30.0f * kSimulationStepSeconds);
        dx = x1;
        dz = z1;
    }
    const auto [x2, z2] =
        StepMovement(0.0f, 0.0f, 1.0f, 1.0f, speed, 30.0f * kSimulationStepSeconds);
    const float diagonal_length = std::sqrt(x2 * x2 + z2 * z2);
    CHECK(std::fabs(diagonal_length - 5.0f) < 1e-3f);
    CHECK(std::fabs(dx - 5.0f) < 1e-3f);
    CHECK(std::fabs(dz) < kEps);

    // A faster authoritative speed covers proportionally more ground.
    const auto [fast_x, fast_z] =
        StepMovement(0.0f, 0.0f, 1.0f, 0.0f, 2.0f * speed, kSimulationStepSeconds);
    CHECK(std::fabs(fast_x - (2.0f * speed / 30.0f)) < kEps);
    CHECK(fast_z == 0.0f);

    // Zero intent does not move, and the arena clamps.
    const auto [x3, z3] = StepMovement(3.0f, 4.0f, 0.0f, 0.0f, speed, kSimulationStepSeconds);
    CHECK(x3 == 3.0f);
    CHECK(z3 == 4.0f);
    const auto [x4, z4] =
        StepMovement(19.9f, 19.9f, 1.0f, 1.0f, speed, kSimulationStepSeconds);
    CHECK(x4 <= kArenaMax);
    CHECK(z4 <= kArenaMax);
}

void TestSnapshotInterpolation() {
    SnapshotInterpolator interpolator;
    interpolator.SetDelayTicks(1.0);
    CHECK(interpolator.ApplyEntities({{1, {0.0f, 0.0f}}}, 100).empty());
    CHECK(interpolator.ApplyEntities({{1, {10.0f, 0.0f}}}, 110).empty());
    CHECK(interpolator.LatestTick() == 110);
    CHECK(interpolator.Count() == 1);
    CHECK(interpolator.HasEntity(1));

    // Render tick = latest - delay = 109 => one tick before the newest sample.
    float x = 0.0f;
    float z = 0.0f;
    CHECK(interpolator.SampleEntity(1, x, z));
    CHECK(std::fabs(x - 9.0f) < 1e-3f);
    CHECK(std::fabs(z) < kEps);
    CHECK(!interpolator.SampleEntity(42, x, z));

    // Out-of-order samples are ignored instead of rewinding time.
    CHECK(interpolator.ApplyEntities({{1, {1.0f, 0.0f}}}, 105).empty());
    CHECK(interpolator.SampleEntity(1, x, z));
    CHECK(std::fabs(x - 9.0f) < 1e-3f);

    // Full-set semantics: an entity missing from the newest snapshot is gone.
    const auto removed = interpolator.ApplyEntities({{2, {1.0f, 1.0f}}}, 120);
    CHECK(removed.size() == 1);
    CHECK(removed[0] == 1);
    CHECK(!interpolator.HasEntity(1));
    CHECK(interpolator.HasEntity(2));

    interpolator.Clear();
    CHECK(interpolator.Count() == 0);
}

void TestStageStateWireValues() {
    // The enum must stay numerically identical to the server's stage.State iota
    // order (server/internal/game/stage/plan.go), because it is compared against
    // the raw WorldSnapshot.stage.state field. If the server renumbers the
    // states, this table is the tripwire.
    struct Case {
        std::uint32_t wire;
        StageState state;
        const char* name;
    };
    Case cases[] = {
        {0, StageState::kWaiting, "waiting"},
        {1, StageState::kPlaying, "playing"},
        {2, StageState::kStageClear, "clear"},
        {3, StageState::kReward, "reward"},
        {4, StageState::kPreparingNextStage, "preparing"},
        {5, StageState::kFailed, "failed"},
        {6, StageState::kClosed, "closed"},
    };
    for (const Case& item : cases) {
        CHECK(static_cast<std::uint32_t>(item.state) == item.wire);
        CHECK(std::string(StageStateName(item.wire)) == item.name);
    }

    // Unknown wire values are reported as unknown, never mapped onto a state.
    CHECK(std::string(StageStateName(7)) == "?");
    CHECK(std::string(StageStateName(99)) == "?");

    CHECK(IsPreparingNextStage(4));
    CHECK(!IsPreparingNextStage(3));
    CHECK(!IsPreparingNextStage(5));
}

void TestAuthoritativeRewardPhaseEnded() {
    CHECK(AuthoritativeRewardPhaseEnded(4));
    CHECK(AuthoritativeRewardPhaseEnded(5));
    CHECK(AuthoritativeRewardPhaseEnded(6));
    CHECK(!AuthoritativeRewardPhaseEnded(0));
    CHECK(!AuthoritativeRewardPhaseEnded(1));
    CHECK(!AuthoritativeRewardPhaseEnded(2));
    CHECK(!AuthoritativeRewardPhaseEnded(3));
    CHECK(!AuthoritativeRewardPhaseEnded(99));
}

void TestRewardSettledStates() {
    CHECK(RewardSettled(RewardState::kNone));
    CHECK(RewardSettled(RewardState::kApplied));
    CHECK(RewardSettled(RewardState::kRejected));
    CHECK(RewardSettled(RewardState::kTimedOut));
    CHECK(!RewardSettled(RewardState::kOffered));
    CHECK(!RewardSettled(RewardState::kChosen));
}

void TestCanSendInputGating() {
    // Everything satisfied: in a room, with a snapshot, alive, stage playing.
    InputGate gate;
    gate.in_room = true;
    gate.session_has_snapshot = true;
    gate.self_known = true;
    gate.self_alive = true;
    gate.stage_state = static_cast<std::uint32_t>(StageState::kPlaying);
    CHECK(CanSendInput(gate));
    CHECK(std::string(InputBlockReason(gate)) == "(input enabled)");

    // Each blocking condition on its own (A5 item C-b).
    InputGate not_in_room = gate;
    not_in_room.in_room = false;
    CHECK(!CanSendInput(not_in_room));
    CHECK(std::string(InputBlockReason(not_in_room)) == "not in room");

    InputGate recovering = gate;
    recovering.recovery_active = true;
    CHECK(!CanSendInput(recovering));
    CHECK(std::string(InputBlockReason(recovering)) == "session recovery in progress");

    InputGate no_snapshot = gate;
    no_snapshot.session_has_snapshot = false;
    CHECK(!CanSendInput(no_snapshot));
    CHECK(std::string(InputBlockReason(no_snapshot)) ==
          "waiting for the first authoritative snapshot");

    InputGate dead = gate;
    dead.self_alive = false;
    CHECK(!CanSendInput(dead));
    CHECK(std::string(InputBlockReason(dead)) == "player is dead");

    // Aliveness is only gated on once the server has actually said something
    // about this player, so a snapshot without a self entry cannot mute input.
    InputGate unknown_self = gate;
    unknown_self.self_known = false;
    unknown_self.self_alive = false;
    CHECK(CanSendInput(unknown_self));

    // Every non-playing stage state mutes intent; playing is the only allowed one.
    for (std::uint32_t state = 0; state <= 6; ++state) {
        InputGate staged = gate;
        staged.stage_state = state;
        if (state == static_cast<std::uint32_t>(StageState::kPlaying)) {
            CHECK(CanSendInput(staged));
        } else {
            CHECK(!CanSendInput(staged));
            CHECK(std::string(InputBlockReason(staged)) == "stage is not being played");
        }
    }

    // Reason precedence follows the gate order, so the reported cause is the
    // first thing that has to be fixed.
    InputGate everything_wrong = gate;
    everything_wrong.in_room = false;
    everything_wrong.recovery_active = true;
    everything_wrong.session_has_snapshot = false;
    everything_wrong.self_alive = false;
    everything_wrong.stage_state = static_cast<std::uint32_t>(StageState::kReward);
    CHECK(std::string(InputBlockReason(everything_wrong)) == "not in room");
}

void TestClearIntentStopsStaleMovement() {
    MovementPredictor predictor;
    predictor.ApplyAuthoritative(0.0f, 0.0f, 0, 10, 5.0f, true);
    predictor.RecordInput(InputCommand{1, 1.0f, 0.0f});
    predictor.AdvanceTick();
    const float moved = predictor.X();
    CHECK(moved > 0.0f);

    // Input muted (stage ended / death / recovery): the remembered direction is
    // dropped, so the next ticks keep the pose instead of drifting on stale input.
    predictor.ClearIntent();
    predictor.AdvanceTick();
    predictor.AdvanceTick();
    CHECK(std::fabs(predictor.X() - moved) < kEps);
    CHECK(predictor.PredictedTick() == 13);

    // A fresh intent moves again from the frozen pose.
    predictor.RecordInput(InputCommand{2, 0.0f, 1.0f});
    predictor.AdvanceTick();
    CHECK(std::fabs(predictor.Z() - (5.0f / 30.0f)) < kEps);
}

void TestCanReportReadyGating() {
    const std::uint32_t preparing = static_cast<std::uint32_t>(StageState::kPreparingNextStage);
    const std::uint32_t reward = static_cast<std::uint32_t>(StageState::kReward);

    CHECK(CanReportReady(true, preparing, RewardState::kNone, false));
    CHECK(CanReportReady(true, preparing, RewardState::kApplied, false));
    CHECK(CanReportReady(true, preparing, RewardState::kTimedOut, false));
    // Not in a room, wrong stage state, unreported reward, or already reported.
    CHECK(!CanReportReady(false, preparing, RewardState::kNone, false));
    CHECK(!CanReportReady(true, reward, RewardState::kNone, false));
    CHECK(!CanReportReady(true, static_cast<std::uint32_t>(StageState::kPlaying),
                          RewardState::kNone, false));
    CHECK(!CanReportReady(true, preparing, RewardState::kOffered, false));
    CHECK(!CanReportReady(true, preparing, RewardState::kChosen, false));
    CHECK(!CanReportReady(true, preparing, RewardState::kNone, true));
    // The latch is per stage: a settled reward plus a new stage is reportable.
    CHECK(CanReportReady(true, preparing, RewardState::kApplied, false));
}

void TestReadyBlockReasons() {
    const std::uint32_t preparing = static_cast<std::uint32_t>(StageState::kPreparingNextStage);
    const std::uint32_t reward = static_cast<std::uint32_t>(StageState::kReward);

    CHECK(std::string(ReadyBlockReason(false, preparing, RewardState::kNone, false)) ==
          "not in room");
    CHECK(std::string(ReadyBlockReason(true, reward, RewardState::kNone, false)) ==
          "waiting for authoritative preparing state");
    CHECK(std::string(ReadyBlockReason(true, preparing, RewardState::kOffered, false)) ==
          "your reward choice is still pending");
    CHECK(std::string(ReadyBlockReason(true, preparing, RewardState::kTimedOut, true)) ==
          "already reported for this stage");
}

void TestInputSeqFloor() {
    CHECK(InputSeqFloor(0, 0) == 0);
    CHECK(InputSeqFloor(10, 4) == 10);
    CHECK(InputSeqFloor(4, 10) == 10);
    CHECK(InputSeqFloor(7, 7) == 7);
}

void TestEnsureGreaterThan() {
    InputSequencer sequencer;
    // Raising the counter makes the next report strictly greater than the floor,
    // so a resumed session never replays a range the server already processed.
    sequencer.EnsureGreaterThan(7);
    CHECK(sequencer.LastSequence() == 7);
    CHECK(sequencer.HasSent());
    const auto first = sequencer.Tick(InputSample{1, 0});
    CHECK(first.sequence == 8);

    // A lower floor never rewinds the counter.
    sequencer.EnsureGreaterThan(3);
    CHECK(sequencer.Tick(InputSample{}).sequence == 9);

    // Equal floor leaves the next report strictly greater as well.
    sequencer.EnsureGreaterThan(9);
    CHECK(sequencer.Tick(InputSample{}).sequence == 10);

    // A fresh session still restarts at 1 unless a floor is applied.
    sequencer.Reset();
    CHECK(!sequencer.HasSent());
    CHECK(sequencer.Tick(InputSample{}).sequence == 1);
    sequencer.EnsureGreaterThan(0);
    CHECK(sequencer.Tick(InputSample{}).sequence == 2);
}

void TestSettleAfterAuthoritativeEnd() {
    EquipmentTable table;
    table[1001] = EquipmentDisplay{1001, "Blade", "weapon", "+10 Attack"};

    // Offered but the server already ended the phase: the panel must close so it
    // cannot block the ready barrier, and the note says who settled it.
    RewardView offered;
    offered.SetOptions({1001}, 500, table);
    CHECK(offered.State() == RewardState::kOffered);
    offered.SettleAfterAuthoritativeEnd();
    CHECK(offered.State() == RewardState::kTimedOut);
    CHECK(offered.Note().find("server settled") != std::string::npos);
    CHECK(!offered.Active());

    // Same for a choice awaiting its acknowledgement.
    RewardView chosen;
    chosen.SetOptions({1001}, 500, table);
    std::uint32_t id = 0;
    CHECK(chosen.ChooseByIndex(0, id));
    CHECK(chosen.State() == RewardState::kChosen);
    chosen.SettleAfterAuthoritativeEnd();
    CHECK(chosen.State() == RewardState::kTimedOut);

    // Already-settled and never-offered panels are untouched.
    RewardView applied;
    applied.SetOptions({1001}, 500, table);
    applied.ApplyResult(true, 1001, 0);
    const std::string applied_note = applied.Note();
    applied.SettleAfterAuthoritativeEnd();
    CHECK(applied.State() == RewardState::kApplied);
    CHECK(applied.Note() == applied_note);

    RewardView rejected;
    rejected.SetOptions({1001}, 500, table);
    rejected.ApplyResult(false, 1001, 7);
    rejected.SettleAfterAuthoritativeEnd();
    CHECK(rejected.State() == RewardState::kRejected);

    RewardView idle;
    CHECK(idle.State() == RewardState::kNone);
    idle.SettleAfterAuthoritativeEnd();
    CHECK(idle.State() == RewardState::kNone);
}

// ---------------------------------------------------------------------------
// UI infrastructure (Katana Zero refactor, phase P0). All of it is raylib-free.
// ---------------------------------------------------------------------------

void TestViewportLayoutScaleOne() {
    auto native = ComputeViewportLayout(960, 540);
    CHECK(std::fabs(native.scale - 1.0f) < kEps);
    CHECK(native.offset_x == 0.0f);
    CHECK(native.offset_y == 0.0f);

    // Not an integer multiple of the target: scale stays 1 and letterbox bars are
    // centred instead of scaling by a fraction (which would blur the pixel art).
    auto odd = ComputeViewportLayout(1280, 720);
    CHECK(std::fabs(odd.scale - 1.0f) < kEps);
    CHECK(std::fabs(odd.offset_x - 160.0f) < kEps);
    CHECK(std::fabs(odd.offset_y - 90.0f) < kEps);
}

void TestViewportLayoutScaleTwoAndAbove() {
    auto exact = ComputeViewportLayout(1920, 1080);
    CHECK(std::fabs(exact.scale - 2.0f) < kEps);
    CHECK(exact.offset_x == 0.0f);
    CHECK(exact.offset_y == 0.0f);

    // The development monitor: 2.67 fit floors to 2 with balanced bars.
    auto monitor = ComputeViewportLayout(2560, 1440);
    CHECK(std::fabs(monitor.scale - 2.0f) < kEps);
    CHECK(std::fabs(monitor.offset_x - 320.0f) < kEps);
    CHECK(std::fabs(monitor.offset_y - 180.0f) < kEps);

    auto four_x = ComputeViewportLayout(3840, 2160);
    CHECK(std::fabs(four_x.scale - 4.0f) < kEps);
    CHECK(four_x.offset_x == 0.0f);

    // Height is the limiting axis; bars appear only left/right of centre.
    auto wide = ComputeViewportLayout(2560, 1080);
    CHECK(std::fabs(wide.scale - 2.0f) < kEps);
    CHECK(std::fabs(wide.offset_x - 320.0f) < kEps);
    CHECK(wide.offset_y == 0.0f);
}

void TestViewportLayoutDegenerate() {
    // A minimised or not-yet-sized window reports a 1:1 layout, not NaN offsets.
    const int degenerate[][2] = {{0, 0}, {0, 540}, {960, 0}, {-100, -100}};
    for (const auto& size : degenerate) {
        auto layout = ComputeViewportLayout(size[0], size[1]);
        CHECK(std::fabs(layout.scale - 1.0f) < kEps);
        CHECK(layout.offset_x == 0.0f);
        CHECK(layout.offset_y == 0.0f);
    }

    // A 1x1 window is not "unknown", it is simply far smaller than the target, so
    // it is cropped like any other sub-target size.
    auto tiny = ComputeViewportLayout(1, 1);
    CHECK(std::fabs(tiny.scale - 1.0f) < kEps);
    CHECK(tiny.offset_x < 0.0f);
    CHECK(tiny.offset_y < 0.0f);

    // Smaller than the target: clamped to scale 1 and cropped symmetrically, so
    // the offsets go negative rather than shrinking the image.
    auto small = ComputeViewportLayout(800, 600);
    CHECK(std::fabs(small.scale - 1.0f) < kEps);
    CHECK(std::fabs(small.offset_x + 80.0f) < kEps);
    CHECK(std::fabs(small.offset_y - 30.0f) < kEps);
}

void TestWindowToRTAndClamping() {
    // Exactly 2x: window (960,540) is the centre of the RT.
    auto doubled = ComputeViewportLayout(1920, 1080);
    auto centre = WindowToRT(Vec2f{960.0f, 540.0f}, doubled);
    CHECK(std::fabs(centre.x - 480.0f) < kEps);
    CHECK(std::fabs(centre.y - 270.0f) < kEps);
    auto corner = WindowToRT(Vec2f{0.0f, 0.0f}, doubled);
    CHECK(corner.x == 0.0f);
    CHECK(corner.y == 0.0f);
    auto far_corner = WindowToRT(Vec2f{1920.0f, 1080.0f}, doubled);
    CHECK(std::fabs(far_corner.x - kTargetWidth) < kEps);
    CHECK(std::fabs(far_corner.y - kTargetHeight) < kEps);

    // With bars (1280x720 => scale 1, offsets 160/90): the bar area would map to
    // negative RT coordinates, so it is clamped onto the RT edge instead.
    auto barred = ComputeViewportLayout(1280, 720);
    auto left_bar = WindowToRT(Vec2f{10.0f, 450.0f}, barred);
    CHECK(left_bar.x == 0.0f);
    CHECK(std::fabs(left_bar.y - 360.0f) < kEps);
    auto top_bar = WindowToRT(Vec2f{640.0f, 5.0f}, barred);
    CHECK(std::fabs(top_bar.x - 480.0f) < kEps);
    CHECK(top_bar.y == 0.0f);
    auto beyond = WindowToRT(Vec2f{5000.0f, 5000.0f}, barred);
    CHECK(std::fabs(beyond.x - kTargetWidth) < kEps);
    CHECK(std::fabs(beyond.y - kTargetHeight) < kEps);

    // The image area is distinguishable from the bars (used to hold the last aim
    // direction while the pointer is outside).
    CHECK(IsInsideTarget(Vec2f{160.0f, 90.0f}, barred));
    CHECK(IsInsideTarget(Vec2f{1120.0f, 630.0f}, barred));
    CHECK(!IsInsideTarget(Vec2f{159.0f, 300.0f}, barred));
    CHECK(!IsInsideTarget(Vec2f{640.0f, 89.0f}, barred));
}

void TestWorldRTTransforms() {
    const Rectf view{560.0f, 190.0f, 340.0f, 280.0f};

    // The arena centre lands in the middle of the view rectangle.
    auto centre = WorldToRT(Vec2f{10.0f, 10.0f}, view);
    CHECK(std::fabs(centre.x - 730.0f) < kEps);
    CHECK(std::fabs(centre.y - 330.0f) < kEps);

    // Corners map onto the view rectangle's corners.
    auto origin = WorldToRT(Vec2f{0.0f, 0.0f}, view);
    CHECK(std::fabs(origin.x - view.x) < kEps);
    CHECK(std::fabs(origin.y - view.y) < kEps);
    auto max = WorldToRT(Vec2f{kWorldSize, kWorldSize}, view);
    CHECK(std::fabs(max.x - (view.x + view.w)) < kEps);
    CHECK(std::fabs(max.y - (view.y + view.h)) < kEps);

    // Round trips: RT -> world -> RT and world -> RT -> world.
    for (const Vec2f world : {Vec2f{0.0f, 0.0f}, Vec2f{3.5f, 17.25f}, Vec2f{10.0f, 10.0f},
                              Vec2f{kWorldSize, kWorldSize}}) {
        const Vec2f rt = WorldToRT(world, view);
        const Vec2f back = RTToWorld(rt, view);
        CHECK(std::fabs(back.x - world.x) < 1e-3f);
        CHECK(std::fabs(back.y - world.y) < 1e-3f);
    }
    const Vec2f rt_point{700.0f, 300.0f};
    const Vec2f round_trip = WorldToRT(RTToWorld(rt_point, view), view);
    CHECK(std::fabs(round_trip.x - rt_point.x) < 1e-3f);
    CHECK(std::fabs(round_trip.y - rt_point.y) < 1e-3f);

    // Degenerate view rectangles must not divide by zero.
    auto degenerate_world = RTToWorld(Vec2f{10.0f, 10.0f}, Rectf{});
    CHECK(degenerate_world.x == 0.0f);
    CHECK(degenerate_world.y == 0.0f);
    auto degenerate_rt = WorldToRT(Vec2f{5.0f, 5.0f}, Rectf{7.0f, 9.0f, 0.0f, 0.0f});
    CHECK(degenerate_rt.x == 7.0f);
    CHECK(degenerate_rt.y == 9.0f);
}

void TestWorldToWindowUsesLayout() {
    const Rectf view{560.0f, 190.0f, 340.0f, 280.0f};

    // 2x with no bars.
    auto doubled = ComputeViewportLayout(1920, 1080);
    auto window = WorldToWindow(Vec2f{10.0f, 10.0f}, view, doubled);
    CHECK(std::fabs(window.x - 1460.0f) < kEps);
    CHECK(std::fabs(window.y - 660.0f) < kEps);

    // Bars are added on top of the scaled RT position.
    auto barred = ComputeViewportLayout(1280, 720);
    auto in_bars = WorldToWindow(Vec2f{10.0f, 10.0f}, view, barred);
    CHECK(std::fabs(in_bars.x - 890.0f) < kEps);
    CHECK(std::fabs(in_bars.y - 420.0f) < kEps);

    // Composing WorldToWindow == WindowToRT inverse: feeding the window point back
    // through the mouse transform recovers the RT point (the ImGui anchoring case).
    auto back = WindowToRT(in_bars, barred);
    auto rt = WorldToRT(Vec2f{10.0f, 10.0f}, view);
    CHECK(std::fabs(back.x - rt.x) < 1e-3f);
    CHECK(std::fabs(back.y - rt.y) < 1e-3f);
}

void TestHealthSegments() {
    // Full health fills every block with no partial block.
    auto full = ComputeHealthSegments(100.0f, 100.0f, 8);
    CHECK(full.segments == 8);
    CHECK(full.filled == 8);
    CHECK(full.partial == 0.0f);
    CHECK(full.alive);
    CHECK(std::fabs(full.fraction - 1.0f) < kEps);

    // Zero and negative health: nothing filled, not alive.
    for (const float hp : {0.0f, -5.0f}) {
        auto empty = ComputeHealthSegments(hp, 100.0f, 8);
        CHECK(empty.filled == 0);
        CHECK(empty.partial == 0.0f);
        CHECK(!empty.alive);
    }

    // Half health: exactly four blocks, no partial.
    auto half = ComputeHealthSegments(50.0f, 100.0f, 8);
    CHECK(half.filled == 4);
    CHECK(half.partial == 0.0f);
    CHECK(half.alive);

    // 25/100 over 8 blocks = 2 blocks exactly; 30/100 = 2 blocks + 0.4 partial.
    auto quarter = ComputeHealthSegments(25.0f, 100.0f, 8);
    CHECK(quarter.filled == 2);
    CHECK(quarter.partial == 0.0f);
    auto partial = ComputeHealthSegments(30.0f, 100.0f, 8);
    CHECK(partial.filled == 2);
    CHECK(std::fabs(partial.partial - 0.4f) < 1e-4f);

    // Overheal clamps; a single block keeps the whole bar in the partial slot.
    auto overheal = ComputeHealthSegments(180.0f, 100.0f, 8);
    CHECK(overheal.filled == 8);
    CHECK(overheal.partial == 0.0f);
    auto one_block = ComputeHealthSegments(1.0f, 100.0f, 1);
    CHECK(one_block.filled == 0);
    CHECK(std::fabs(one_block.partial - 0.01f) < 1e-4f);
    CHECK(one_block.alive);

    // Non-finite input and a non-positive max are treated as no data.
    auto nan_hp = ComputeHealthSegments(std::nanf(""), 100.0f, 8);
    CHECK(nan_hp.filled == 0);
    CHECK(nan_hp.fraction == 0.0f);
    auto zero_max = ComputeHealthSegments(50.0f, 0.0f, 8);
    CHECK(zero_max.filled == 0);
    auto no_segments = ComputeHealthSegments(50.0f, 100.0f, 0);
    CHECK(no_segments.segments == 0);
    CHECK(no_segments.filled == 0);
}

void TestSegmentWidth() {
    // 8 blocks with 4px gaps inside 340px: (340 - 28) / 8 = 39.
    CHECK(std::fabs(SegmentWidth(340.0f, 8, 4.0f) - 39.0f) < 1e-3f);
    CHECK(std::fabs(SegmentWidth(100.0f, 1, 0.0f) - 100.0f) < 1e-3f);
    // Degenerate inputs report 0 so the renderer can skip drawing.
    CHECK(SegmentWidth(340.0f, 0, 4.0f) == 0.0f);
    CHECK(SegmentWidth(0.0f, 8, 4.0f) == 0.0f);
    CHECK(SegmentWidth(340.0f, 8, -1.0f) == 0.0f);
    CHECK(SegmentWidth(10.0f, 8, 4.0f) == 0.0f);  // gaps alone exceed the width
}

void TestFloaterPoolLifetimeAndOverflow() {
    FloaterPool pool;
    CHECK(pool.ActiveCount() == 0);

    CHECK(pool.Spawn(FloaterKey{100, 1, 2}, 3.0f, 4.0f, 17.0f, 0.0f));
    CHECK(pool.ActiveCount() == 1);
    Floater out;
    CHECK(pool.At(0, out));
    CHECK(out.value == 17.0f);
    CHECK(out.world_x == 3.0f);
    CHECK(out.key.target_id == 2);

    // Lifetime expiry is driven by the injected clock.
    pool.Tick(FloaterPool::kDefaultLifetimeSeconds * 0.5f);
    CHECK(pool.ActiveCount() == 1);
    pool.Tick(FloaterPool::kDefaultLifetimeSeconds);
    CHECK(pool.ActiveCount() == 0);

    // Accessibility switch: the event is consumed but no slot is taken.
    CHECK(!pool.Spawn(FloaterKey{1, 1, 1}, 0.0f, 0.0f, 5.0f, 0.0f, /*enabled=*/false));
    CHECK(pool.ActiveCount() == 0);

    // Filling the whole pool, then overflowing: the ring replaces the oldest slot
    // (FIFO) instead of allocating or growing without bound. Start from empty so
    // the FIFO cursor is back at slot 0.
    pool.Clear();
    std::size_t spawned = 0;
    for (std::size_t i = 0; i < FloaterPool::kCapacity; ++i) {
        if (pool.Spawn(FloaterKey{1000 + i, 1, 1}, 1.0f, 1.0f, 1.0f, 0.0f)) {
            ++spawned;
        }
    }
    CHECK(spawned == FloaterPool::kCapacity);
    CHECK(pool.ActiveCount() == FloaterPool::kCapacity);
    CHECK(pool.ReplacedCount() == 0);
    CHECK(pool.Spawn(FloaterKey{9999, 1, 1}, 2.0f, 2.0f, 3.0f, 0.0f));
    CHECK(pool.ActiveCount() == FloaterPool::kCapacity);
    CHECK(pool.ReplacedCount() == 1);
    CHECK(pool.At(0, out));
    CHECK(out.key.server_tick == 9999);  // slot 0 was the oldest

    // Cross-session clearing empties the ring and resets its diagnostics.
    pool.Clear();
    CHECK(pool.ActiveCount() == 0);
    CHECK(pool.ReplacedCount() == 0);
    CHECK(!pool.At(0, out));
}

void TestDamageDedupeTable() {
    DamageDedupeTable dedupe;
    const FloaterKey hit{500, 7, 9};
    CHECK(dedupe.Accept(hit));   // first time: render it
    CHECK(!dedupe.Accept(hit));  // same tick, same source, same target: redundant
    CHECK(dedupe.Size() == 1);
    CHECK(dedupe.LatestTick() == 500);

    // Any component of the key changing makes it a different hit.
    CHECK(dedupe.Accept(FloaterKey{501, 7, 9}));
    CHECK(dedupe.Accept(FloaterKey{501, 8, 9}));
    CHECK(dedupe.Accept(FloaterKey{501, 8, 10}));
    CHECK(dedupe.Size() == 4);

    // Ageing: a key older than kMaxAgeTicks relative to the newest tick seen is
    // forgotten, so a stale duplicate cannot suppress a new hit forever.
    const std::uint64_t fresh_tick = 501 + DamageDedupeTable::kMaxAgeTicks + 1;
    CHECK(dedupe.Accept(FloaterKey{fresh_tick, 1, 1}));
    CHECK(dedupe.Size() < 5);
    CHECK(dedupe.Accept(hit));

    // Capacity is bounded: the 257th distinct key evicts the oldest entry at the
    // same tick, so the table never grows past kCapacity.
    DamageDedupeTable bounded;
    std::size_t accepted = 0;
    for (std::size_t i = 0; i < DamageDedupeTable::kCapacity; ++i) {
        if (bounded.Accept(FloaterKey{1000, static_cast<std::uint32_t>(i + 1), 1})) {
            ++accepted;
        }
    }
    CHECK(accepted == DamageDedupeTable::kCapacity);
    CHECK(bounded.Size() == DamageDedupeTable::kCapacity);
    CHECK(!bounded.Accept(FloaterKey{1000, 1, 1}));
    CHECK(bounded.Accept(FloaterKey{1000, 9999, 1}));
    CHECK(bounded.Size() == DamageDedupeTable::kCapacity);
    CHECK(bounded.Accept(FloaterKey{1000, 1, 1}));  // evicted, so it is news again

    // Cross-session clearing resets the table and its tick watermark.
    bounded.Clear();
    CHECK(bounded.Size() == 0);
    CHECK(bounded.LatestTick() == 0);
}

void TestAssetRootResolution() {
    // Priority: beside the executable first, then CWD candidates.
    auto candidates = AssetRootCandidates("C:/game/bin", "C:/repo");
    CHECK(candidates.size() == 3);
    CHECK(candidates[0] == "C:/game/bin/assets");
    CHECK(candidates[1] == "C:/repo/assets");
    CHECK(candidates[2] == "C:/repo/client/assets");

    // Duplicates collapse, and an unknown executable directory is skipped.
    auto same_dir = AssetRootCandidates("C:/game", "C:/game");
    CHECK(same_dir.size() == 2);
    CHECK(same_dir[0] == "C:/game/assets");
    CHECK(same_dir[1] == "C:/game/client/assets");
    auto no_exe = AssetRootCandidates("", "C:/repo");
    CHECK(no_exe.size() == 2);
    CHECK(no_exe[0] == "C:/repo/assets");

    // The first existing candidate wins...
    auto picked = ChooseAssetRoot(candidates, [](const std::string& path) {
        return path == "C:/repo/assets";
    });
    CHECK(picked == "C:/repo/assets");
    // ...and nothing existing yields an empty root (caller falls back + warns).
    auto none = ChooseAssetRoot(candidates, [](const std::string&) { return false; });
    CHECK(none.empty());
    CHECK(ChooseAssetRoot({}, [](const std::string&) { return true; }).empty());

    // JoinPath handles the separator cases the candidates rely on.
    CHECK(JoinPath("base", "leaf") == "base/leaf");
    CHECK(JoinPath("base/", "leaf") == "base/leaf");
    CHECK(JoinPath("base\\", "leaf") == "base\\leaf");
    CHECK(JoinPath("", "leaf") == "leaf");
}

void TestSettingsPathSelection() {
    // Windows: %APPDATA% wins, never the source tree.
    auto appdata = ChooseSettingsDirectory("C:/Users/x/AppData/Roaming", nullptr, nullptr, "C:/exe");
    CHECK(appdata == "C:/Users/x/AppData/Roaming/Odyssey");

    // POSIX with XDG_CONFIG_HOME set.
    auto xdg = ChooseSettingsDirectory(nullptr, "/home/x/.config", "/home/x", "C:/exe");
    CHECK(xdg == "/home/x/.config/odyssey");

    // POSIX without it falls back to ~/.config.
    auto home = ChooseSettingsDirectory(nullptr, nullptr, "/home/x", "C:/exe");
    CHECK(home == "/home/x/.config/odyssey");

    // Empty environment values count as unset; the executable directory is last.
    auto exe = ChooseSettingsDirectory("", "", "", "C:/exe");
    CHECK(exe == "C:/exe");

    CHECK(SettingsFilePathIn("C:/Users/x/AppData/Roaming/Odyssey") ==
          "C:/Users/x/AppData/Roaming/Odyssey/settings.ini");
}

void TestThemePaletteAndAccessibility() {
    Theme theme = kDefaultTheme;
    // The spec pins the deep purple-black background and a black letterbox.
    CHECK(theme.background == RgbaFromHex(0x0A0A10));
    CHECK(theme.background.r == 0x0Au);
    CHECK(theme.background.g == 0x0Au);
    CHECK(theme.background.b == 0x10u);
    CHECK(theme.background.a == 255u);
    CHECK(theme.letterbox == RgbaFromHex(0x000000));
    // The play field must be visibly above the mandated clear colour, otherwise the
    // arena reads as "black with a faint grid" (measured: 88% of the play area at 2%
    // luminance before this was added).
    const int background_luma = 299 * theme.background.r + 587 * theme.background.g +
                                114 * theme.background.b;
    const int floor_luma =
        299 * theme.arena_floor.r + 587 * theme.arena_floor.g + 114 * theme.arena_floor.b;
    const int grid_luma = 299 * theme.grid.r + 587 * theme.grid.g + 114 * theme.grid.b;
    const int edge_luma =
        299 * theme.panel_edge.r + 587 * theme.panel_edge.g + 114 * theme.panel_edge.b;
    CHECK(floor_luma > background_luma);
    CHECK(grid_luma > floor_luma);
    CHECK(edge_luma > grid_luma);
    CHECK(theme.text.a == 255u);
    // Panels are translucent so the world shows through.
    CHECK(theme.panel.a == 235u);
    // Accents stay distinct (a copy/paste slip would collapse two neon colours).
    CHECK(theme.neon_cyan != theme.neon_magenta);
    CHECK(theme.neon_cyan != theme.neon_yellow);
    CHECK(theme.bar_fill != theme.bar_empty);

    // Hex packing: checked field by field because a braced initializer inside the
    // CHECK macro would be split on its commas.
    Rgba packed = RgbaFromHex(0x123456);
    CHECK(packed.r == 0x12u);
    CHECK(packed.g == 0x34u);
    CHECK(packed.b == 0x56u);
    CHECK(packed.a == 255u);
    Rgba transparent = RgbaFromHex(0xFFFFFF, 0);
    CHECK(transparent.r == 0xFFu);
    CHECK(transparent.a == 0u);

    // Accessibility defaults: every reduction is off until the user asks for it.
    AccessibilityConfig access = kDefaultAccessibility;
    CHECK(!access.disable_glitch_fx);
    CHECK(!access.disable_screen_shake);
    CHECK(!access.disable_damage_floaters);
}

void TestHitMarkerDirection() {
    HitMarker marker;
    CHECK(!marker.active);
    // Attacker to the east of the player.
    CHECK(SetHitDirection(marker, 10.0f, 10.0f, 13.0f, 14.0f, 5.0f));
    CHECK(marker.active);
    CHECK(std::fabs(marker.dir_x - 0.6f) < 1e-5f);
    CHECK(std::fabs(marker.dir_z - 0.8f) < 1e-5f);
    const float length = std::sqrt(marker.dir_x * marker.dir_x + marker.dir_z * marker.dir_z);
    CHECK(std::fabs(length - 1.0f) < 1e-5f);
    CHECK(std::fabs(HitMarkerFade(marker, 5.0f) - 1.0f) < 1e-5f);
    CHECK(std::fabs(HitMarkerFade(marker, 5.0f + marker.lifetime * 0.5f) - 0.5f) < 1e-4f);

    // Expiry is driven by the injected clock. The epsilon avoids asserting on the
    // exact float boundary (5.0f + 0.7f - 5.0f is not exactly 0.7f).
    UpdateHitMarker(marker, 5.0f + marker.lifetime - 0.01f);
    CHECK(marker.active);
    UpdateHitMarker(marker, 5.0f + marker.lifetime + 0.001f);
    CHECK(!marker.active);
    CHECK(HitMarkerFade(marker, 6.0f) == 0.0f);

    // An attacker on top of the player carries no direction: the old marker is
    // left alone instead of reporting a bogus angle.
    HitMarker kept;
    CHECK(SetHitDirection(kept, 0.0f, 0.0f, 1.0f, 0.0f, 3.0f));
    CHECK(SetHitDirection(kept, 0.0f, 0.0f, 0.0f, 0.0f, 4.0f) == false);
    CHECK(kept.active);
    CHECK(kept.born_seconds == 3.0f);  // untouched by the rejected update
    CHECK(std::fabs(kept.dir_x - 1.0f) < 1e-5f);
}

void TestDamageGhost() {
    DamageGhost ghost;
    CHECK(!ghost.active);

    // Healing (or no change) does not raise a ghost.
    OnHealthFraction(ghost, 0.8f, 0.9f);
    CHECK(!ghost.active);
    OnHealthFraction(ghost, 0.8f, 0.8f);
    CHECK(!ghost.active);

    // A drop holds the pre-hit level, then drains towards the real bar.
    OnHealthFraction(ghost, 1.0f, 0.6f);
    CHECK(ghost.active);
    CHECK(std::fabs(ghost.shown_fraction - 1.0f) < 1e-5f);
    CHECK(std::fabs(ghost.amount - 0.4f) < 1e-5f);
    CHECK(DamageShakeOffset(ghost, 3.0f) == 0.0f);  // shake starts at t=0 exactly

    UpdateDamageGhost(ghost, ghost.hold_seconds * 0.5f);
    CHECK(ghost.active);
    CHECK(std::fabs(ghost.shown_fraction - 1.0f) < 1e-5f);  // still holding

    // Shake is bounded by its amplitude and decays to nothing.
    float max_shake = 0.0f;
    float time = 0.0f;
    while (time < 0.6f) {
        time += 1.0f / 60.0f;
        UpdateDamageGhost(ghost, 1.0f / 60.0f);
        const float offset = DamageShakeOffset(ghost, 3.0f);
        max_shake = std::max(max_shake, std::fabs(offset));
    }
    CHECK(max_shake <= 3.0f);
    CHECK(max_shake > 0.0f);
    CHECK(!ghost.active);  // drained and retired
    CHECK(ghost.amount == 0.0f);
    CHECK(DamageShakeOffset(ghost, 3.0f) == 0.0f);

    // A second hit during the animation restarts it from the new level.
    OnHealthFraction(ghost, 0.6f, 0.2f);
    CHECK(ghost.active);
    CHECK(std::fabs(ghost.shown_fraction - 0.6f) < 1e-5f);
    UpdateDamageGhost(ghost, 0.0f);  // a zero-length frame must not advance it
    CHECK(ghost.since_hit == 0.0f);
    // Non-finite input is ignored rather than poisoning the ghost.
    OnHealthFraction(ghost, std::nanf(""), 0.1f);
    CHECK(std::fabs(ghost.shown_fraction - 0.6f) < 1e-5f);
}

void TestHexagonCrosshair() {
    float points[kCrosshairPoints * 2] = {0};
    CHECK(HexagonCrosshair(100.0f, 50.0f, 8.0f, 0.0f, points, kCrosshairPoints * 2) ==
          kCrosshairPoints * 2);
    // First vertex points right (rotation 0), second is 60 degrees further round.
    CHECK(std::fabs(points[0] - 108.0f) < 1e-3f);
    CHECK(std::fabs(points[1] - 50.0f) < 1e-3f);
    CHECK(std::fabs(points[2] - (100.0f + 8.0f * std::cos(1.04719755f))) < 1e-3f);
    CHECK(std::fabs(points[3] - (50.0f + 8.0f * std::sin(1.04719755f))) < 1e-3f);
    // Every vertex sits exactly `radius` from the centre.
    for (int i = 0; i < kCrosshairPoints; ++i) {
        const float dx = points[i * 2] - 100.0f;
        const float dy = points[i * 2 + 1] - 50.0f;
        CHECK(std::fabs(std::sqrt(dx * dx + dy * dy) - 8.0f) < 1e-3f);
    }
    // Rotation turns the whole outline.
    float rotated[kCrosshairPoints * 2] = {0};
    CHECK(HexagonCrosshair(0.0f, 0.0f, 1.0f, 1.57079633f, rotated, kCrosshairPoints * 2) ==
          kCrosshairPoints * 2);
    CHECK(std::fabs(rotated[0]) < 1e-3f);
    CHECK(std::fabs(rotated[1] - 1.0f) < 1e-3f);

    // Degenerate buffers report 0 instead of writing out of bounds.
    CHECK(HexagonCrosshair(0.0f, 0.0f, 1.0f, 0.0f, points, kCrosshairPoints * 2 - 1) == 0);
    CHECK(HexagonCrosshair(0.0f, 0.0f, 1.0f, 0.0f, nullptr, kCrosshairPoints * 2) == 0);
}

void TestEmaAndRtt() {
    Ema ema(0.5f);
    CHECK(!ema.HasValue());
    ema.Add(100.0f);
    // The first sample seeds the average instead of starting from zero.
    CHECK(std::fabs(ema.Value() - 100.0f) < kEps);
    CHECK(ema.HasValue());
    ema.Add(200.0f);
    CHECK(std::fabs(ema.Value() - 150.0f) < kEps);
    ema.Add(200.0f);
    CHECK(std::fabs(ema.Value() - 175.0f) < kEps);
    // NaN must not poison the average.
    ema.Add(std::nanf(""));
    CHECK(std::fabs(ema.Value() - 175.0f) < kEps);
    ema.Reset();
    CHECK(!ema.HasValue());

    RttEstimator rtt;
    CHECK(!rtt.HasValue());
    rtt.OnPong(1000, 1042);  // 42 ms
    CHECK(std::fabs(rtt.Milliseconds() - 42.0f) < kEps);
    rtt.OnPong(2000, 2058);  // 58 ms, EMA(0.1) -> 42 + 0.1*16 = 43.6
    CHECK(std::fabs(rtt.Milliseconds() - 43.6f) < 1e-3f);
    // A pong that claims to predate its ping is ignored, not reported as negative.
    const float before = rtt.Milliseconds();
    rtt.OnPong(5000, 4000);
    CHECK(std::fabs(rtt.Milliseconds() - before) < kEps);
}

void TestServerTickRateEstimator() {
    // Snapshots arrive at 10Hz but carry the 30Hz tick: the reported rate must be the
    // TICK rate (30), not the snapshot rate (10). This is the exact confusion the
    // plan calls out.
    ServerTickRateEstimator rate(0.5f);
    CHECK(!rate.HasValue());
    rate.OnSnapshot(1000, 0.0);
    CHECK(!rate.HasValue());  // a single snapshot cannot yield a rate
    rate.OnSnapshot(1003, 0.1);  // 3 ticks in 0.1 s
    CHECK(std::fabs(rate.Hertz() - 30.0f) < 1e-3f);
    rate.OnSnapshot(1006, 0.2);  // 3 ticks in 0.1 s again; EMA stays at 30
    CHECK(std::fabs(rate.Hertz() - 30.0f) < 1e-3f);

    // A zero-length interval (duplicate arrival time) is skipped rather than dividing
    // by zero, and a server restart (tick going backwards) adds no sample.
    const float before = rate.Hertz();
    rate.OnSnapshot(1009, 0.2);
    CHECK(std::fabs(rate.Hertz() - before) < kEps);
    rate.OnSnapshot(5, 0.3);
    CHECK(std::fabs(rate.Hertz() - before) < kEps);

    // A slower tick rate is reported as such.
    ServerTickRateEstimator slow(1.0f);
    slow.OnSnapshot(100, 0.0);
    slow.OnSnapshot(102, 0.1);  // 20 Hz
    CHECK(std::fabs(slow.Hertz() - 20.0f) < 1e-3f);
}

void TestPredictionErrorEstimator() {
    PredictionErrorEstimator error(0.5f);
    CHECK(!error.HasValue());
    error.OnSnapshotCorrection(0.0f);
    CHECK(error.HasValue());
    CHECK(error.Distance() == 0.0f);
    error.OnSnapshotCorrection(2.0f);
    CHECK(std::fabs(error.Distance() - 1.0f) < kEps);
    error.Reset();
    CHECK(!error.HasValue());
}

void TestMetricSeries() {
    MetricSeries series;
    CHECK(series.Count() == 0);
    for (int i = 0; i < 5; ++i) {
        series.Add(static_cast<float>(i));
    }
    CHECK(series.Count() == 5);
    CHECK(std::fabs(series.MaxValue() - 4.0f) < kEps);
    CHECK(series.At(0) == 0.0f);  // oldest first, so index 0 is the leftmost point
    CHECK(series.At(4) == 4.0f);
    CHECK(series.At(5) == 0.0f);  // out of range reads as zero, never out of bounds
    series.Add(std::nanf(""));    // a NaN sample is ignored, not plotted
    CHECK(series.Count() == 5);

    // Wrapping past capacity keeps exactly kCapacity samples and drops the oldest.
    for (std::size_t i = 0; i < MetricSeries::kCapacity + 10; ++i) {
        series.Add(static_cast<float>(i));
    }
    CHECK(series.Count() == MetricSeries::kCapacity);
    CHECK(series.At(0) <= series.At(MetricSeries::kCapacity - 1));
    series.Reset();
    CHECK(series.Count() == 0);
    CHECK(series.MaxValue() == 0.0f);
    CHECK(series.At(0) == 0.0f);
}

void TestPerfCapture() {
    PerfCapture capture;
    CHECK(!capture.Armed());
    CHECK(!capture.Sampling());
    CHECK(!capture.Complete());
    capture.Add(99.0);  // ignored before the capture is even armed
    CHECK(capture.Count() == 0);

    capture.Arm(5);
    CHECK(capture.Armed());
    CHECK(!capture.Sampling());
    CHECK(capture.Target() == 5);
    // Armed but not started: frames before the battle begins must not be counted, or the
    // budget would be spent on login/matchmaking instead of the scene under test.
    capture.Add(99.0);
    CHECK(capture.Count() == 0);
    CHECK(!capture.Complete());

    CHECK(capture.BeginSampling());
    CHECK(capture.Sampling());
    CHECK(!capture.BeginSampling());  // starting again is not a second start

    // The first kWarmupFrames frames are dropped: frame 0 pays for the font atlas upload
    // and the first draw setup, which the plan says must not be counted.
    for (std::size_t i = 0; i < PerfCapture::kWarmupFrames; ++i) {
        capture.Add(50.0);
    }
    CHECK(capture.Count() == 0);
    for (const double ms : {10.0, 12.0, 11.0, 13.0, 14.0}) {
        capture.Add(ms);
    }
    CHECK(capture.Count() == 5);
    CHECK(capture.Complete());
    const PerfSummary summary = capture.Summary();
    CHECK(summary.sampled == 5);
    CHECK(std::fabs(summary.mean_ms - 12.0) < 1e-6);
    CHECK(std::fabs(summary.min_ms - 10.0) < 1e-6);
    CHECK(std::fabs(summary.max_ms - 14.0) < 1e-6);
    CHECK(std::fabs(summary.median_ms - 12.0) < 1e-6);
    CHECK(summary.p95_ms >= summary.median_ms);
    CHECK(summary.p95_ms <= summary.max_ms);

    // The plan's floor for a valid measurement, and the capacity clamp: asking for more
    // than the fixed buffer holds truncates instead of growing.
    // Read through a mutable local: comparing two compile-time constants directly trips
    // MSVC C4127 (constant conditional expression) inside the CHECK macro's `if`.
    int plan_minimum_frames = PerfCapture::kMinimumFrames;
    CHECK(plan_minimum_frames == 600);
    PerfCapture clamped;
    clamped.Arm(PerfCapture::kCapacity + 500);
    CHECK(clamped.Target() == PerfCapture::kCapacity);
    clamped.BeginSampling();
    for (std::size_t i = 0; i < PerfCapture::kCapacity + 32; ++i) {
        clamped.Add(1.0);
    }
    CHECK(clamped.Count() == PerfCapture::kCapacity);  // truncated, never overflowed

    // An empty capture reports zeros rather than dividing by zero.
    PerfCapture empty;
    empty.Arm(3);
    empty.BeginSampling();
    const PerfSummary nothing = empty.Summary();
    CHECK(nothing.sampled == 0);
    CHECK(nothing.mean_ms == 0.0);
    CHECK(nothing.max_ms == 0.0);

    // Dumping to a path that cannot be opened fails instead of pretending to work.
    PerfCapture written;
    written.Arm(2);
    written.BeginSampling();
    for (std::size_t i = 0; i < PerfCapture::kWarmupFrames; ++i) {
        written.Add(9.0);
    }
    written.Add(1.0);
    written.Add(0.5);
    written.Add(0.25);
    CHECK(written.Count() == 3);
    CHECK(!written.Dump("Z:/definitely/not/a/directory/perf.csv"));
}

void TestStageSummary() {
    // Helpers: format into a fresh buffer and hand back a string, so the expectations read
    // as the exact text the HUD would draw.
    const auto modifier_text = [](const StageSummary& summary, std::size_t index,
                                  bool with_target = false) {
        char buffer[128] = {0};
        summary.FormatModifier(index, buffer, sizeof(buffer), with_target);
        return std::string(buffer);
    };
    const auto modifiers_text = [](const StageSummary& summary) {
        char buffer[256] = {0};
        summary.FormatModifiers(buffer, sizeof(buffer));
        return std::string(buffer);
    };
    const auto cleared_text = [](const StageSummary& summary) {
        char buffer[128] = {0};
        summary.FormatCleared(buffer, sizeof(buffer));
        return std::string(buffer);
    };
    const auto line_text = [](const StageSummary& summary) {
        char buffer[256] = {0};
        summary.FormatLine(buffer, sizeof(buffer));
        return std::string(buffer);
    };

    StageSummary summary;
    // Nothing has arrived: every formatter reports "nothing to show" so the HUD can skip the
    // draw, and no line renders a zero that was never sent.
    char text[256] = {0};
    CHECK(!summary.has_index());
    CHECK(!summary.has_seed());
    CHECK(!summary.HasDetail());
    CHECK(summary.modifier_count() == 0);
    CHECK(summary.FormatLine(text, sizeof(text)) == 0);
    CHECK(text[0] == '\0');
    CHECK(summary.FormatModifiers(text, sizeof(text)) == 0);
    CHECK(summary.FormatCleared(text, sizeof(text)) == 0);
    CHECK(summary.FormatModifier(0, text, sizeof(text)) == 0);

    summary.BeginStage(2);
    CHECK(summary.has_index());
    CHECK(summary.index() == 2);
    CHECK(!summary.HasDetail());

    // Detail is wiped when the stage index changes...
    summary.AddModifier(kModTargetPlayer, kModOpMultiply, kModStatAttack, 1.2f);
    summary.SetMonsterCount(6);
    summary.SetSeed(4242);
    summary.SetCleared(3, 42500);
    CHECK(summary.HasDetail());
    summary.BeginStage(2);  // same stage: what already arrived for it must survive
    CHECK(summary.modifier_count() == 1);
    CHECK(summary.has_difficulty());
    CHECK(summary.has_monster_count());
    CHECK(summary.has_seed());
    CHECK(summary.seed() == 4242);
    summary.BeginStage(3);  // next stage: the previous stage's detail is gone
    CHECK(summary.index() == 3);
    CHECK(!summary.HasDetail());
    CHECK(!summary.has_monster_count());
    CHECK(!summary.has_difficulty());
    CHECK(!summary.has_clear_time());
    CHECK(summary.seed() == 4242);  // the seed survives (a stage's seed is stable)

    // Formatting follows the server's semantics: add = signed delta, multiply = factor, so
    // 1.2 reads as +20% and 0.9 as -10%.
    StageSummary formatted;
    formatted.AddModifier(kModTargetPlayer, kModOpAdd, kModStatAttack, 5.0f);
    formatted.AddModifier(kModTargetMonster, kModOpMultiply, kModStatMoveSpeed, 1.2f);
    formatted.AddModifier(kModTargetPlayer, kModOpMultiply, kModStatMoveSpeed, 0.9f);
    formatted.AddModifier(kModTargetPlayer, kModOpAdd, kModStatDefense, -3.0f);
    CHECK(formatted.modifier_count() == 4);
    CHECK(modifier_text(formatted, 0) == "ATK +5");
    CHECK(modifier_text(formatted, 1) == "SPD +20%");
    CHECK(modifier_text(formatted, 2) == "SPD -10%");
    CHECK(modifier_text(formatted, 3) == "DEF -3");
    CHECK(modifier_text(formatted, 1, true) == "MONSTER SPD +20%");
    CHECK(modifier_text(formatted, 4).empty());  // out of range formats nothing

    // An unknown stat keeps its raw id and an unknown operation falls back to the plain value,
    // so a newer server stays readable instead of being dropped.
    StageSummary unknown;
    unknown.AddModifier(kModTargetPlayer, kModOpAdd, 7, 2.0f);
    CHECK(modifier_text(unknown, 0) == "STAT7 +2");
    StageSummary unknown_op;
    unknown_op.AddModifier(9, 99, kModStatAttack, 2.0f);
    CHECK(modifier_text(unknown_op, 0) == "ATK +2");
    CHECK(modifier_text(unknown_op, 0, true) == "ATK +2");  // unknown target: no prefix

    // Overflow is reported, not silently dropped: the HUD shows kMaxShown and a count.
    StageSummary many;
    for (int i = 0; i < 6; ++i) {
        many.AddModifier(kModTargetPlayer, kModOpAdd, kModStatAttack, 1.0f);
    }
    CHECK(many.modifier_count() == StageSummary::kMaxShown);
    CHECK(many.modifier_overflow() == 2);
    CHECK(modifiers_text(many) == "ATK +1, ATK +1, ATK +1, ATK +1 (+2 more)");

    // A clipped line reports nothing rather than a truncated label.
    char tiny[4] = {0};
    CHECK(formatted.FormatModifier(0, tiny, sizeof(tiny)) == 0);
    CHECK(tiny[0] == '\0');
    CHECK(formatted.FormatModifiers(tiny, sizeof(tiny)) == 0);

    // Clear summary: only the parts the server actually filled.
    StageSummary cleared;
    cleared.SetCleared(0, 0);  // an unpopulated message must not render as DIFF 0
    CHECK(!cleared.has_difficulty());
    CHECK(!cleared.has_clear_time());
    CHECK(!cleared.HasDetail());
    cleared.SetCleared(3, 0);
    CHECK(cleared.has_difficulty());
    CHECK(!cleared.has_clear_time());
    CHECK(cleared_text(cleared) == "DIFF 3");
    cleared.SetCleared(0, 42500);
    CHECK(cleared.has_difficulty());  // still there: SetCleared only ever adds
    CHECK(cleared.has_clear_time());
    CHECK(cleared_text(cleared) == "DIFF 3   CLEAR 42.5s");

    // The single HUD line joins the cleared summary and the modifier list.
    StageSummary line;
    line.SetCleared(3, 42500);
    line.AddModifier(kModTargetPlayer, kModOpMultiply, kModStatAttack, 1.2f);
    line.AddModifier(kModTargetPlayer, kModOpMultiply, kModStatMoveSpeed, 1.1f);
    CHECK(line_text(line) == "DIFF 3   CLEAR 42.5s   ATK +20%, SPD +10%");
    CHECK(line.FormatLine(tiny, sizeof(tiny)) == 0);
    StageSummary modifiers_only;
    modifiers_only.AddModifier(kModTargetPlayer, kModOpAdd, kModStatMaxHealth, 25.0f);
    CHECK(line_text(modifiers_only) == "HP +25");

    // A new session wipes everything, including the index and the seed.
    line.Clear();
    CHECK(!line.has_index());
    CHECK(!line.has_seed());
    CHECK(!line.HasDetail());
}

}  // namespace

int main() {
    TestNormalizeIdle();
    TestNormalizeAxes();
    TestNormalizeDiagonalNotFaster();
    TestSequencerCountsEveryTick();
    TestGameViewApplyAndRemoveMissing();
    TestGameViewClosedEmpties();
    TestGameViewDefensiveSort();
    TestCombatViewMonstersFullSet();
    TestCombatViewProjectilesAndStage();
    TestGameViewCombatFields();
    TestCombatViewFeedback();
    TestParseEquipmentTable();
    TestRewardViewFlow();
    TestRecoveryStateFlow();
    TestRecoveryHandshakeTimeout();
    TestRecoverySecondDropIsRecoverable();
    TestMovementPredictorReconciliation();
    TestPredictorStepsPerTickNotPerPacket();
    TestPredictorUsesServerMoveSpeed();
    TestPredictorStationary();
    TestPredictorDeadPlayerDoesNotAdvance();
    TestPredictorStageChangeJump();
    TestPredictorRejectsAbsurdTickGap();
    TestStepMovementRules();
    TestSnapshotInterpolation();
    TestStageStateWireValues();
    TestAuthoritativeRewardPhaseEnded();
    TestRewardSettledStates();
    TestCanSendInputGating();
    TestClearIntentStopsStaleMovement();
    TestCanReportReadyGating();
    TestReadyBlockReasons();
    TestInputSeqFloor();
    TestStageSummary();
    TestEnsureGreaterThan();
    TestSettleAfterAuthoritativeEnd();
    TestViewportLayoutScaleOne();
    TestViewportLayoutScaleTwoAndAbove();
    TestViewportLayoutDegenerate();
    TestWindowToRTAndClamping();
    TestWorldRTTransforms();
    TestWorldToWindowUsesLayout();
    TestHealthSegments();
    TestSegmentWidth();
    TestFloaterPoolLifetimeAndOverflow();
    TestDamageDedupeTable();
    TestAssetRootResolution();
    TestSettingsPathSelection();
    TestThemePaletteAndAccessibility();
    TestHitMarkerDirection();
    TestDamageGhost();
    TestHexagonCrosshair();
    TestEmaAndRtt();
    TestServerTickRateEstimator();
    TestPredictionErrorEstimator();
    TestMetricSeries();
    TestPerfCapture();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
