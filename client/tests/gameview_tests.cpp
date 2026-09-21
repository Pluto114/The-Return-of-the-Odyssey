// Headless tests for the D2-direction logic slice: input normalization &
// sequencing, and full-snapshot application semantics. No window, no sockets.
#include "input/InputSample.h"
#include "input/OneShotAction.h"
#include "scene/StageScene.h"
#include "sync/CombatView.h"
#include "sync/CombatFeedback.h"
#include "sync/GameView.h"
#include "sync/Interpolation.h"
#include "sync/MatchScore.h"
#include "sync/Prediction.h"
#include "sync/RecoveryState.h"
#include "sync/RewardView.h"
#include "ui/ChineseLabels.h"
#include "ui/ReplayInput.h"
#include "ui/RewardChoiceInput.h"
#include "ui/TextUtils.h"

#include <cmath>
#include <cstdint>
#include <cstdio>
#include <fstream>
#include <sstream>
#include <string_view>
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
using odyssey::client::input::OneShotAction;
using odyssey::client::sync::CombatView;
using odyssey::client::sync::EquipmentTable;
using odyssey::client::sync::GameView;
using odyssey::client::sync::InputCommand;
using odyssey::client::sync::kArenaMax;
using odyssey::client::sync::kSimulationStepSeconds;
using odyssey::client::sync::MonsterEntity;
using odyssey::client::sync::MatchScoreboard;
using odyssey::client::sync::MovementPredictor;
using odyssey::client::sync::ParseEquipmentTable;
using odyssey::client::sync::PlayerView;
using odyssey::client::sync::PickupEntity;
using odyssey::client::sync::ProjectileVisual;
using odyssey::client::sync::RecoveryPhase;
using odyssey::client::sync::RecoveryState;
using odyssey::client::sync::RewardState;
using odyssey::client::sync::RewardView;
using odyssey::client::sync::SnapshotView;
using odyssey::client::sync::SnapshotInterpolator;
using odyssey::client::sync::StageInfo;
using odyssey::client::sync::StepMovement;

constexpr float kEps = 1e-5f;

void TestProceduralStageScenes() {
    using odyssey::client::scene::MakeStageScene;
    const auto first = MakeStageScene(1, 4242);
    const auto repeat = MakeStageScene(1, 4242);
    CHECK(first.fingerprint == repeat.fingerprint);
    CHECK(first.biome == repeat.biome);
    CHECK(first.stars[17].x == repeat.stars[17].x);
    CHECK(first.landmarks[5].phase == repeat.landmarks[5].phase);

    const auto next = MakeStageScene(2, 4242);
    CHECK(first.fingerprint != next.fingerprint);
    CHECK(first.biome != next.biome);
    CHECK(first.stars[0].x != next.stars[0].x || first.stars[0].z != next.stars[0].z);

    for (std::uint32_t index = 1; index <= 24; ++index) {
        const auto scene = MakeStageScene(index, 8000 + index);
        CHECK(scene.grid_columns >= 8 && scene.grid_columns <= 13);
        CHECK(scene.grid_rows >= 6 && scene.grid_rows <= 10);
        for (const auto& star : scene.stars) {
            CHECK(star.x >= 0.0f && star.x <= 1.0f);
            CHECK(star.z >= 0.0f && star.z <= 1.0f);
        }
        for (const auto& landmark : scene.landmarks) {
            CHECK(landmark.x >= 0.0f && landmark.x <= 1.0f);
            CHECK(landmark.z >= 0.0f && landmark.z <= 1.0f);
        }
    }
}

void TestDynamicArenaLayouts() {
    using odyssey::client::sync::InsideCover;
    using odyssey::client::sync::MakeArenaLayout;
    std::vector<std::string> signatures;
    for (std::uint32_t index = 1; index <= 12; ++index) {
        const auto layout = MakeArenaLayout(index, static_cast<std::int64_t>(index) * 4242);
        CHECK(layout.block_count >= 6);
        CHECK(!InsideCover(layout, 10.0f, 10.0f, 0.5f));
        std::ostringstream signature;
        signature << static_cast<std::uint32_t>(layout.topology) << ':'
                  << layout.rotation << ':' << layout.mirrored;
        for (std::size_t block_index = 0; block_index < layout.block_count; ++block_index) {
            const auto& block = layout.blocks[block_index];
            CHECK(block.min_x >= 0.0f && block.min_z >= 0.0f);
            CHECK(block.max_x <= 20.0f && block.max_z <= 20.0f);
            signature << ';' << block.min_x << ',' << block.min_z << ','
                      << block.max_x << ',' << block.max_z;
        }
        for (const auto& previous : signatures) CHECK(previous != signature.str());
        signatures.push_back(signature.str());
    }
}

void TestAuthoritativeMatchScoreboard() {
    MatchScoreboard scoreboard;
    constexpr std::uint64_t monster = odyssey::client::sync::kFirstWorldEntityId | 7;
    scoreboard.ObservePlayer(10, 80.0f, 100.0f, true);
    scoreboard.ObservePlayer(20, 50.0f, 100.0f, true);
    scoreboard.ObserveStageStarted(1, 30);
    scoreboard.ObserveDamage(10, monster, 35.6);
    scoreboard.ObserveDamage(monster, 10, 20.0);
    scoreboard.ObserveDamage(20, monster, 9.0);
    scoreboard.ObserveDeath(monster, 10);
    scoreboard.ObserveDeath(20, monster);
    scoreboard.ObserveStageCleared(1, 330);
    scoreboard.ObserveStageCleared(1, 330);  // reliable duplicate is idempotent

    const auto ranking = scoreboard.Rankings();
    CHECK(ranking.size() == 2);
    CHECK(ranking[0].player_id == 10);
    CHECK(ranking[0].kills == 1);
    CHECK(std::fabs(ranking[0].damage_dealt - 35.6) < kEps);
    CHECK(std::fabs(ranking[0].damage_taken - 20.0) < kEps);
    CHECK(ranking[0].score == 396);
    CHECK(ranking[1].player_id == 20);
    CHECK(ranking[1].deaths == 1);
    CHECK(ranking[1].score == 0);
    CHECK(scoreboard.ClearedStages() == 1);
    CHECK(std::fabs(scoreboard.DurationSeconds() - 10.0) < kEps);
    CHECK(scoreboard.TeamScore() == 750);

    scoreboard.Reset();
    CHECK(scoreboard.Rankings().empty());
    CHECK(scoreboard.TeamScore() == 0);
}

void TestReplayAfterTerminalStage() {
    using odyssey::client::ui::CanReplay;
    using odyssey::client::ui::ExpeditionComplete;
    CHECK(CanReplay(1, 5, 0.0, 12));
    CHECK(!CanReplay(1, 1, 99.0, 12));
    CHECK(!CanReplay(1, 2, 99.0, 12));
    CHECK(!CanReplay(3, 2, 99.0, 12));
    CHECK(!CanReplay(11, 2, 99.0, 12));
    CHECK(!CanReplay(12, 2, 1.0, 12));
    CHECK(ExpeditionComplete(12, 2, 1.5, 12));
    CHECK(CanReplay(12, 2, 1.5, 12));
    CHECK(!ExpeditionComplete(12, 3, 99.0, 12));
    CHECK(!ExpeditionComplete(12, 2, 99.0, 0));
    CHECK(ExpeditionComplete(4, 2, 1.5, 4));
}

void TestBoundedCombatFeedback() {
    odyssey::client::sync::CombatFeedback feedback;
    feedback.Track(1, 10, 10);
    feedback.Damage(1, 20, true, true);
    CHECK(feedback.Numbers().size() == 1);
    CHECK(feedback.Particles().size() == 7);
    CHECK(feedback.HitMarker() == 1);
    CHECK(feedback.HurtFlash() == 1);
    for (int i = 0; i < 1000; ++i) {
        feedback.Damage(1, 10, false, true);
        feedback.Death(1);
    }
    CHECK(feedback.Particles().size() <= feedback.kMaxParticles);
    CHECK(feedback.Numbers().size() <= feedback.kMaxNumbers);
    feedback.Tick(1.1f);
    CHECK(feedback.Numbers().empty());
    CHECK(feedback.Particles().empty());
    CHECK(feedback.HitMarker() == 0);
    CHECK(feedback.HurtFlash() == 0);
    feedback.Death(1);
    CHECK(feedback.Particles().empty()); // expired target is not retained forever
    feedback.Track(2, 1, 1);
    feedback.Death(2);
    CHECK(feedback.Particles().size() == 20);
    feedback.Clear();
    CHECK(feedback.Particles().empty());
}

void TestWindowTitleIdentifiesScoreboardBuild() {
    CHECK(std::string_view(odyssey::client::ui::kWindowTitle).find("积分榜") != std::string_view::npos);
    CHECK(std::string_view(odyssey::client::ui::kWindowTitle).find("战术地图 V2") != std::string_view::npos);
}

void TestRewardCardSelection() {
    using odyssey::client::ui::CardBounds;
    using odyssey::client::ui::SelectRewardOption;
    CHECK(SelectRewardOption(1, false, 0, 0, 3) == 0);
    CHECK(SelectRewardOption(3, false, 0, 0, 3) == 2);
    CHECK(!SelectRewardOption(3, false, 0, 0, 2).has_value());
    for (std::size_t i = 0; i < 3; ++i) {
        const auto card = CardBounds(i);
        CHECK(SelectRewardOption(0, true, card.x + 20, card.y + 20, 3) == i);
        CHECK(SelectRewardOption(0, true, card.x, card.y, 3) == i);
        CHECK(!SelectRewardOption(0, true, card.x + card.width, card.y + 20, 3).has_value());
        CHECK(!SelectRewardOption(0, false, card.x + 20, card.y + 20, 3).has_value());
    }
    CHECK(!SelectRewardOption(0, true, 450, 500, 3).has_value());
    CHECK(!SelectRewardOption(0, true, 1140, 500, 2).has_value());
}

void TestChineseEquipmentLabels() {
    std::ifstream file(ODYSSEY_EQUIPMENT_ZH_PATH);
    CHECK(file.good());
    if (!file) return;
    std::stringstream contents;
    contents << file.rdbuf();
    EquipmentTable table;
    CHECK(ParseEquipmentTable(contents.str(), table) == 7);
    CHECK(table.at(1003).name == "弹鼓手枪");
    CHECK(table.at(2002).name == "疾风遗物");
    CHECK(table.at(2002).description == "移动速度 ×1.1");
    CHECK(table.at(3001).slot == "药剂");
    CHECK(table.at(3001).description.find("按 Q 使用") != std::string::npos);
}

void TestShortTextDoesNotSplitChineseUtf8() {
    const std::string description = "装备后按 Q 使用，恢复 60 点生命值";
    CHECK(odyssey::client::ui::ShortText(description, 38) == description);
}

void TestNormalizeIdle() {
    const auto v = NormalizeInput(InputSample{});
    CHECK(v.x == 0.0f);
    CHECK(v.z == 0.0f);
}

void TestOneShotActionSurvivesUntilInputTick() {
    OneShotAction action;
    CHECK(!action.Pending());
    action.Press();
    CHECK(action.Pending());
    action.Press();  // keyboard repeat must still result in one consumable action
    CHECK(action.Consume());
    CHECK(!action.Consume());
    action.Press();
    action.Reset();
    CHECK(!action.Consume());
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

void TestCombatViewPickupsUseFullSetSemantics() {
    CombatView view;
    view.ApplyPickups({PickupEntity{700, 1, 4.0f, 5.0f, 0, 30.0f},
                       PickupEntity{701, 2, 16.0f, 15.0f, 1001, 0.0f}});
    CHECK(view.PickupCount() == 2);
    CHECK(view.Pickups().at(700).value == 30.0f);
    CHECK(view.Pickups().at(701).equipment_id == 1001);

    view.ApplyPickups({PickupEntity{701, 2, 16.0f, 15.0f, 1001, 0.0f}});
    CHECK(view.PickupCount() == 1);
    CHECK(view.Pickups().find(700) == view.Pickups().end());
    view.Clear();
    CHECK(view.PickupCount() == 0);
}

void TestCombatViewProjectilesAndStage() {
    CombatView view;
    ProjectileVisual projectile;
    projectile.id = 42;
    projectile.owner_id = 10;
    projectile.x = 3.0f;
    projectile.z = 4.0f;
    projectile.vx = 20.0f;
    projectile.expires_at_tick = 500;
    view.SpawnProjectile(projectile);
    CHECK(view.ProjectileCount() == 1);
    CHECK(view.Projectiles().at(42).x == 3.0f);
    view.Tick(0.05f);
    CHECK(std::fabs(view.Projectiles().at(42).x - 4.0f) < kEps);

    // Duplicate spawn (should not happen with one dispatcher) overwrites, and
    // destroy of an unknown id is a no-op.
    CHECK(!view.DestroyProjectile(99));
    CHECK(view.DestroyProjectile(42, 6.0f, 4.0f));
    CHECK(view.ProjectileCount() == 1);  // animate the authoritative final segment
    CHECK(view.Projectiles().at(42).finishing);
    view.Tick(0.05f);
    CHECK(view.Projectiles().at(42).x > 4.0f);
    CHECK(view.Projectiles().at(42).x < 6.0f);
    view.Tick(0.05f);
    CHECK(view.ProjectileCount() == 0);
    CHECK(view.Impacts().size() == 1);
    CHECK(view.Impacts()[0].x == 6.0f);
    ProjectileVisual instant;
    instant.id = 43;
    instant.x = 3.0f;
    instant.z = 4.0f;
    instant.vx = 20.0f;
    view.SpawnProjectile(instant);
    CHECK(view.DestroyProjectile(43, 7.0f, 4.0f));
    CHECK(view.ProjectileCount() == 1);  // spawn and hit in one network drain
    view.Tick(0.1f);
    CHECK(view.Projectiles().at(43).x > 3.0f);
    CHECK(view.Projectiles().at(43).x < 7.0f);
    view.Tick(0.1f);
    CHECK(view.ProjectileCount() == 0);
    CHECK(view.Impacts().size() == 1);
    CHECK(view.Impacts()[0].x == 7.0f);
    view.Tick(CombatView::kImpactSeconds);
    CHECK(view.Impacts().empty());

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
        "1001\tIron Sidearm\tweapon\tAttack +5\n"
        "\n"
        "2001\tVitality Relic\trelic\tMax health +25, no healing\n"
        "bogus line without id,name\n";
    const std::size_t loaded = ParseEquipmentTable(text, table);
    CHECK(loaded == 2);
    CHECK(table.size() == 2);
    const auto it = table.find(1001);
    CHECK(it != table.end());
    if (it != table.end()) {
        CHECK(it->second.name == "Iron Sidearm");
        CHECK(it->second.slot == "weapon");
        CHECK(it->second.description == "Attack +5");
    }
    const auto second = table.find(2001);
    CHECK(second != table.end());
    if (second != table.end()) {
        CHECK(second->second.description == "Max health +25, no healing");
    }
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
    CHECK(chosen == 2001);
    CHECK(view.State() == RewardState::kChosen);
    // A second choice is refused locally (single-shot).
    CHECK(!view.ChooseByIndex(1, chosen));

    // Refusal is reported honestly.
    view.ApplyResult(false, 2001, 200);
    CHECK(view.State() == RewardState::kRejected);
    CHECK(view.Note().find("refused") != std::string::npos);

    // Deadline expiry only affects an unanswered offer.
    RewardView timed;
    timed.SetOptions({2001}, 100, table);
    timed.Timeout();
    CHECK(timed.State() == RewardState::kTimedOut);
    CHECK(!timed.Active());
    timed.ApplyResult(true, 2001, 1);
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
    recovery.MarkResumeSent();
    CHECK(!recovery.WantsResumeRequest());    // exactly one per connection

    recovery.OnResumeResult(true);
    CHECK(recovery.Phase() == RecoveryPhase::kRestored);
    CHECK(!recovery.Active());
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
        if (i + 1 < RecoveryState::kMaxAttempts) {
            exhausted.OnDisconnect(now);           // attempt failed
            now += RecoveryState::kMaxBackoffSeconds;  // wait out the backoff
        }
    }
    CHECK(exhausted.Exhausted());
    CHECK(!exhausted.ShouldRetry(now + 1000.0));

    recovery.Reset();
    CHECK(recovery.Phase() == RecoveryPhase::kIdle);
}

void TestMovementPredictorReconciliation() {
    MovementPredictor predictor;
    // Two unconfirmed inputs at 30Hz, 5 units/s => 5/30 per tick.
    predictor.RecordInput(InputCommand{1, 1.0f, 0.0f});
    predictor.RecordInput(InputCommand{2, 1.0f, 0.0f});
    CHECK(predictor.HasPrediction());
    CHECK(predictor.PendingCount() == 2);
    CHECK(std::fabs(predictor.X() - (10.0f / 30.0f)) < kEps);

    // Server confirms only input 1 and reports its own position: we snap there
    // and replay the still-pending input 2.
    predictor.ApplyAuthoritative(5.0f, 7.0f, 1);
    CHECK(predictor.PendingCount() == 1);
    CHECK(std::fabs(predictor.X() - (5.0f + 5.0f / 30.0f)) < kEps);
    CHECK(std::fabs(predictor.Z() - 7.0f) < kEps);
    CHECK(predictor.LastCorrectionDistance() > 0.0f);

    // Server confirms everything: no pending inputs, exact authoritative pose.
    predictor.ApplyAuthoritative(5.0f, 7.0f, 2);
    CHECK(predictor.PendingCount() == 0);
    CHECK(std::fabs(predictor.X() - 5.0f) < kEps);
    CHECK(std::fabs(predictor.Z() - 7.0f) < kEps);

    // Reset (new session / resume) drops predictions entirely.
    predictor.Reset();
    CHECK(!predictor.HasPrediction());
    CHECK(predictor.PendingCount() == 0);
}

void TestStepMovementRules() {
    // Diagonal is length-limited: 30 ticks diagonal == 30 ticks straight.
    float dx = 0.0f;
    float dz = 0.0f;
    {
        auto [x1, z1] = StepMovement(0.0f, 0.0f, 1.0f, 0.0f, 30.0f * kSimulationStepSeconds);
        dx = x1;
        dz = z1;
    }
    const auto [x2, z2] = StepMovement(0.0f, 0.0f, 1.0f, 1.0f, 30.0f * kSimulationStepSeconds);
    const float diagonal_length = std::sqrt(x2 * x2 + z2 * z2);
    CHECK(std::fabs(diagonal_length - 5.0f) < 1e-3f);
    CHECK(std::fabs(dx - 5.0f) < 1e-3f);
    CHECK(std::fabs(dz) < kEps);

    // Zero intent does not move, and the arena clamps.
    const auto [x3, z3] = StepMovement(3.0f, 4.0f, 0.0f, 0.0f, kSimulationStepSeconds);
    CHECK(x3 == 3.0f);
    CHECK(z3 == 4.0f);
    const auto [x4, z4] = StepMovement(19.9f, 19.9f, 1.0f, 1.0f, kSimulationStepSeconds);
    CHECK(x4 <= kArenaMax);
    CHECK(z4 <= kArenaMax);

    // Local prediction stops at the same cover edge as the server.
    const auto layout = odyssey::client::sync::MakeArenaLayout(1, 0);
    const auto& cover = layout.blocks[0];
    float x = cover.max_x + 0.45f;
    const float z = (cover.min_z + cover.max_z) * 0.5f;
    for (int i = 0; i < 10; ++i) {
        x = StepMovement(x, z, -1.0f, 0.0f, kSimulationStepSeconds, layout).first;
    }
    CHECK(x >= cover.max_x + 0.32f);
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

}  // namespace

int main() {
    TestProceduralStageScenes();
    TestDynamicArenaLayouts();
    TestAuthoritativeMatchScoreboard();
    TestReplayAfterTerminalStage();
    TestBoundedCombatFeedback();
    TestWindowTitleIdentifiesScoreboardBuild();
    TestRewardCardSelection();
    TestChineseEquipmentLabels();
    TestShortTextDoesNotSplitChineseUtf8();
    TestOneShotActionSurvivesUntilInputTick();
    TestNormalizeIdle();
    TestNormalizeAxes();
    TestNormalizeDiagonalNotFaster();
    TestSequencerCountsEveryTick();
    TestGameViewApplyAndRemoveMissing();
    TestGameViewClosedEmpties();
    TestGameViewDefensiveSort();
    TestCombatViewMonstersFullSet();
    TestCombatViewPickupsUseFullSetSemantics();
    TestCombatViewProjectilesAndStage();
    TestGameViewCombatFields();
    TestCombatViewFeedback();
    TestParseEquipmentTable();
    TestRewardViewFlow();
    TestRecoveryStateFlow();
    TestMovementPredictorReconciliation();
    TestStepMovementRules();
    TestSnapshotInterpolation();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
