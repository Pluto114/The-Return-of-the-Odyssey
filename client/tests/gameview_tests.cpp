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

#include <cmath>
#include <cstdint>
#include <cstdio>
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
using odyssey::client::sync::StepMovement;

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
    const std::string text =
        "# client display table\n"
        "1,Blade of the Odyssey,Weapon,\"+10 Attack\"\n"
        "\n"
        "2,Glass Cannon,Relic,\"+20 Attack, -10 Defense\"\n"
        "bogus line without id,name\n";
    const std::size_t loaded = ParseEquipmentTable(text, table);
    CHECK(loaded == 2);
    CHECK(table.size() == 2);
    const auto it = table.find(1);
    CHECK(it != table.end());
    if (it != table.end()) {
        CHECK(it->second.name == "Blade of the Odyssey");
        CHECK(it->second.slot == "Weapon");
        CHECK(it->second.stats == "\"+10 Attack\"");
    }
    const auto second = table.find(2);
    CHECK(second != table.end());
    if (second != table.end()) {
        // The stats column may itself contain commas.
        CHECK(second->second.stats == "\"+20 Attack, -10 Defense\"");
    }
}

void TestRewardViewFlow() {
    EquipmentTable table;
    ParseEquipmentTable("5,Vitality Relic,Relic,\"+25 Max HP\"\n", table);

    RewardView view;
    view.SetOptions({5, 99}, 4000, table);
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
    CHECK(chosen == 5);
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
    TestEnsureGreaterThan();
    TestSettleAfterAuthoritativeEnd();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
