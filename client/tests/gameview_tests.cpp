// Headless tests for the D2-direction logic slice: input normalization &
// sequencing, and full-snapshot application semantics. No window, no sockets.
#include "input/InputSample.h"
#include "sync/GameView.h"

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
using odyssey::client::sync::GameView;
using odyssey::client::sync::PlayerView;
using odyssey::client::sync::SnapshotView;

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

}  // namespace

int main() {
    TestNormalizeIdle();
    TestNormalizeAxes();
    TestNormalizeDiagonalNotFaster();
    TestSequencerCountsEveryTick();
    TestGameViewApplyAndRemoveMissing();
    TestGameViewClosedEmpties();
    TestGameViewDefensiveSort();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
