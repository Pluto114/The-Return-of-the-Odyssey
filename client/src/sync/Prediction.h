// Client-side movement prediction and server reconciliation (D9).
//
// The server stays authoritative: every snapshot carries the player's true
// position plus `last_processed_input` (the newest InputSeq actually applied).
// The client predicts locally with the SAME movement rules the server uses
// (unit-length direction, 5 units/s, 1/30 fixed step, [0,20]^2 arena), then on
// each snapshot snaps back to the authoritative position and REPLAYS only the
// inputs the server has not confirmed yet.
//
// Pure logic (no raylib/asio/protobuf) so reconciliation is unit-testable.
#pragma once

#include <cmath>
#include <cstdint>
#include <deque>
#include <utility>

#include "sync/Arena.h"

namespace odyssey::client::sync {

// Movement rules shared with the server plan (not tuning knobs: changing them
// requires the server to agree).
inline constexpr float kMoveSpeedUnitsPerSecond = 5.0f;
inline constexpr float kSimulationStepSeconds = 1.0f / 30.0f;
inline constexpr float kArenaMin = 0.0f;
inline constexpr float kArenaMax = 20.0f;

struct InputCommand {
    std::uint32_t seq = 0;
    float dx = 0.0f;  // normalized move intent
    float dz = 0.0f;
};

// One deterministic movement step: direction is limited to unit length first,
// so diagonal input never moves faster (plan T05/T06), then the arena clamps.
inline std::pair<float, float> StepMovement(float x, float z, float dx, float dz, float dt,
                                            const ArenaLayout& layout) {
    const float length_sq = dx * dx + dz * dz;
    if (length_sq > 1.0f) {
        const float inv = 1.0f / std::sqrt(length_sq);
        dx *= inv;
        dz *= inv;
    }
    return MoveAroundCover(layout, x, z, dx * kMoveSpeedUnitsPerSecond * dt,
                           dz * kMoveSpeedUnitsPerSecond * dt);
}

inline std::pair<float, float> StepMovement(float x, float z, float dx, float dz, float dt) {
    return StepMovement(x, z, dx, dz, dt, MakeArenaLayout(0, 0));
}

class MovementPredictor {
public:
    void SetArenaLayout(const ArenaLayout& layout) { layout_ = layout; }

    // New session (or first authoritative snapshot of a resumed session).
    void Reset() {
        pending_.clear();
        x_ = 0.0f;
        z_ = 0.0f;
        has_prediction_ = false;
        last_correction_distance_ = 0.0f;
    }

    // Called for every input we actually send to the server. The move is
    // applied locally immediately (prediction) and remembered for replay.
    void RecordInput(const InputCommand& command) {
        const auto [nx, nz] = StepMovement(x_, z_, command.dx, command.dz,
                                           kSimulationStepSeconds, layout_);
        x_ = nx;
        z_ = nz;
        has_prediction_ = true;
        pending_.push_back(command);
        while (pending_.size() > kMaxPending) {
            pending_.pop_front();
        }
    }

    // Authoritative correction: snap to the server position, drop the inputs
    // the server has applied (seq <= ack), then replay what is still pending.
    void ApplyAuthoritative(float server_x, float server_z, std::uint32_t ack_seq) {
        if (has_prediction_) {
            const float ddx = x_ - server_x;
            const float ddz = z_ - server_z;
            last_correction_distance_ = std::sqrt(ddx * ddx + ddz * ddz);
        }
        while (!pending_.empty() && pending_.front().seq <= ack_seq) {
            pending_.pop_front();
        }
        x_ = server_x;
        z_ = server_z;
        has_prediction_ = true;
        for (const auto& command : pending_) {
            const auto [nx, nz] = StepMovement(x_, z_, command.dx, command.dz,
                                               kSimulationStepSeconds, layout_);
            x_ = nx;
            z_ = nz;
        }
    }

    bool HasPrediction() const { return has_prediction_; }
    float X() const { return x_; }
    float Z() const { return z_; }
    std::size_t PendingCount() const { return pending_.size(); }
    // Distance the last correction had to remove (debug HUD; converges to ~0
    // when prediction agrees with the server).
    float LastCorrectionDistance() const { return last_correction_distance_; }

private:
    static constexpr std::size_t kMaxPending = 256;
    std::deque<InputCommand> pending_;
    float x_ = 0.0f;
    float z_ = 0.0f;
    bool has_prediction_ = false;
    float last_correction_distance_ = 0.0f;
    ArenaLayout layout_ = MakeArenaLayout(0, 0);
};

}  // namespace odyssey::client::sync
