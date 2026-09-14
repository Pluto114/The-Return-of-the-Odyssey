// Client-side movement prediction and server reconciliation (D9, A5 item C-d).
//
// The server stays authoritative: every snapshot carries the player's true
// position, the newest InputSeq it has applied (`last_processed_input`), the
// server tick, the player's current move speed and whether they are alive.
//
// Server movement semantics this model mirrors
// (server/internal/game/world.go: ApplyInput "stages the newest intent; it does
// not advance position or ack"): the server performs exactly ONE movement step
// per tick, using the newest intent it has received. Steps are therefore counted
// in ticks, never in input packets - a client that sends at 300Hz must not move
// ten times faster than one that sends at 30Hz.
//
// So prediction is tick-driven:
//   * RecordInput() only remembers the newest intent (the server coalesces too),
//   * AdvanceTick() advances exactly one 1/30 step and is called once per 30Hz
//     boundary regardless of the input send rate,
//   * ApplyAuthoritative() snaps to the server pose and re-advances only the
//     ticks the client has already simulated past the snapshot's server tick,
//     measured on the server tick timeline rather than by packet count.
//
// Pure logic (no raylib/asio/protobuf) so reconciliation is unit-testable.
#pragma once

#include <cmath>
#include <cstdint>
#include <deque>
#include <utility>

namespace odyssey::client::sync {

// Fallback movement speed used until a snapshot supplies the authoritative one.
// The server's own value wins: equipment and stat modifiers change MoveSpeed, so
// hardcoding it makes prediction drift as soon as a reward is applied.
inline constexpr float kMoveSpeedUnitsPerSecond = 5.0f;
inline constexpr float kSimulationStepSeconds = 1.0f / 30.0f;
inline constexpr float kArenaMin = 0.0f;
inline constexpr float kArenaMax = 20.0f;

// Upper bound on how many ticks a single correction may replay. A stale or
// bogus server tick must not be able to fling the predicted position away.
inline constexpr std::uint64_t kMaxReplayTicks = 10;

struct InputCommand {
    std::uint32_t seq = 0;
    float dx = 0.0f;  // normalized move intent
    float dz = 0.0f;
};

// One deterministic movement step: direction is limited to unit length first,
// so diagonal input never moves faster (plan T05/T06), then the arena clamps.
// `speed` is the authoritative units-per-second (server-provided).
inline std::pair<float, float> StepMovement(float x, float z, float dx, float dz,
                                            float speed, float dt) {
    const float length_sq = dx * dx + dz * dz;
    if (length_sq > 1.0f) {
        const float inv = 1.0f / std::sqrt(length_sq);
        dx *= inv;
        dz *= inv;
    }
    x += dx * speed * dt;
    z += dz * speed * dt;
    if (x < kArenaMin) x = kArenaMin;
    if (x > kArenaMax) x = kArenaMax;
    if (z < kArenaMin) z = kArenaMin;
    if (z > kArenaMax) z = kArenaMax;
    return {x, z};
}

class MovementPredictor {
public:
    // New session (or first authoritative snapshot of a resumed session).
    void Reset() {
        unacked_.clear();
        newest_intent_ = InputCommand{};
        has_intent_ = false;
        x_ = 0.0f;
        z_ = 0.0f;
        has_prediction_ = false;
        alive_ = true;
        move_speed_ = kMoveSpeedUnitsPerSecond;
        predicted_tick_ = 0;
        has_tick_anchor_ = false;
        ack_seq_ = 0;
        last_correction_distance_ = 0.0f;
    }

    // Ignored when the value cannot come from a real player state (0, negative or
    // non-finite), so a malformed snapshot cannot freeze or teleport prediction.
    void SetMoveSpeed(float units_per_second) {
        if (std::isfinite(units_per_second) && units_per_second > 0.0f) {
            move_speed_ = units_per_second;
        }
    }

    // A dead player is not moved by the server, so prediction must not run ahead
    // of (or away from) the authoritative corpse.
    void SetAlive(bool alive) { alive_ = alive; }

    // Every input we actually send: only the newest intent matters for movement
    // (the server applies the newest it has received on each tick). Earlier
    // intents are kept solely so the HUD can count what is still unacknowledged.
    void RecordInput(const InputCommand& command) {
        newest_intent_ = command;
        has_intent_ = true;
        unacked_.push_back(command);
        while (unacked_.size() > kMaxUnacked) {
            unacked_.pop_front();
        }
    }

    // Exactly one simulation step, once per 30Hz boundary. Called independently of
    // how many inputs were transmitted, which is what keeps a 300Hz send rate from
    // moving the client ten times per server tick. The tick timeline advances even
    // while dead (the server keeps ticking); only the movement is skipped.
    void AdvanceTick() {
        if (alive_ && has_intent_) {
            const auto [nx, nz] = StepMovement(x_, z_, newest_intent_.dx, newest_intent_.dz,
                                               move_speed_, kSimulationStepSeconds);
            x_ = nx;
            z_ = nz;
        }
        has_prediction_ = true;
        ++predicted_tick_;
    }

    // Authoritative correction. `server_tick` is the tick the snapshot describes;
    // the client has already simulated up to predicted_tick_, so only the
    // difference is re-applied - counted in ticks on the server timeline, never in
    // received packets. `ack_seq` is the newest input the server applied.
    void ApplyAuthoritative(float server_x, float server_z, std::uint32_t ack_seq,
                            std::uint64_t server_tick, float move_speed, bool alive) {
        if (has_prediction_) {
            const float ddx = x_ - server_x;
            const float ddz = z_ - server_z;
            last_correction_distance_ = std::sqrt(ddx * ddx + ddz * ddz);
        }

        while (!unacked_.empty() && unacked_.front().seq <= ack_seq) {
            unacked_.pop_front();
        }

        SetMoveSpeed(move_speed);
        alive_ = alive;
        ack_seq_ = ack_seq;
        x_ = server_x;
        z_ = server_z;
        has_prediction_ = true;

        // The prediction keeps its own tick counter; the server tick must never
        // move it backwards, and a snapshot from the future is clamped so the
        // replay stays bounded. The first snapshot anchors the counter onto the
        // server's timeline (the two start out unrelated).
        if (!has_tick_anchor_) {
            predicted_tick_ = server_tick;
            has_tick_anchor_ = true;
        } else if (server_tick > predicted_tick_) {
            predicted_tick_ = server_tick;
        }
        std::uint64_t owed = predicted_tick_ - server_tick;
        if (owed > kMaxReplayTicks) {
            // Too far apart to be a real tick gap (stale/garbage snapshot): accept
            // the authoritative tick instead of replaying an unbounded future.
            predicted_tick_ = server_tick;
            owed = 0;
        }
        if (!alive_) {
            return;
        }
        for (std::uint64_t i = 0; i < owed; ++i) {
            if (!has_intent_) {
                break;
            }
            const auto [nx, nz] = StepMovement(x_, z_, newest_intent_.dx, newest_intent_.dz,
                                               move_speed_, kSimulationStepSeconds);
            x_ = nx;
            z_ = nz;
        }
    }

    bool HasPrediction() const { return has_prediction_; }
    bool Alive() const { return alive_; }
    float X() const { return x_; }
    float Z() const { return z_; }
    float MoveSpeed() const { return move_speed_; }
    // Inputs sent but not yet acknowledged by a snapshot (HUD/diagnostics).
    std::size_t PendingCount() const { return unacked_.size(); }
    std::uint64_t PredictedTick() const { return predicted_tick_; }
    std::uint32_t AckSeq() const { return ack_seq_; }
    // Distance the last correction had to remove (debug HUD; converges to ~0
    // when prediction agrees with the server).
    float LastCorrectionDistance() const { return last_correction_distance_; }

private:
    static constexpr std::size_t kMaxUnacked = 256;
    std::deque<InputCommand> unacked_;
    InputCommand newest_intent_;
    bool has_intent_ = false;
    float x_ = 0.0f;
    float z_ = 0.0f;
    bool has_prediction_ = false;
    bool alive_ = true;
    float move_speed_ = kMoveSpeedUnitsPerSecond;
    std::uint64_t predicted_tick_ = 0;
    bool has_tick_anchor_ = false;
    std::uint32_t ack_seq_ = 0;
    float last_correction_distance_ = 0.0f;
};

}  // namespace odyssey::client::sync
