// Client input intent types and the 30Hz input sequencer.
//
// The client only ever sends *intent* (direction), never positions or final
// results (server-authoritative movement). Keyboard state is mapped to a
// 2D world-space direction vector (server x/y <=> client x/z per plan);
// release maps to the zero vector so the server stops the player.
//
// Field/axis conventions here are PROVISIONAL until the D1 alignment with
// A's PlayerInput wire fields; the sequencer/sampling logic itself is
// protocol-independent.
#pragma once

#include <cmath>
#include <cstdint>

namespace odyssey::client::input {

// Discrete keyboard intent before any smoothing.
struct InputSample {
    int dx = 0;  // +1 = D / east, -1 = A / west
    int dz = 0;  // +1/-1 = S/W; world z sign finalized at D1 alignment
};

inline bool operator==(const InputSample& lhs, const InputSample& rhs) {
    return lhs.dx == rhs.dx && lhs.dz == rhs.dz;
}
inline bool operator!=(const InputSample& lhs, const InputSample& rhs) {
    return !(lhs == rhs);
}

// Normalized world-space direction vector. Diagonal keyboard input is
// length-limited to the unit circle instead of moving diagonally faster.
struct InputVector {
    float x = 0.0f;  // corresponds to server x
    float z = 0.0f;  // corresponds to server y
};

// Maps discrete samples to a normalized vector (provisional sign conventions).
inline InputVector NormalizeInput(const InputSample& sample) {
    const float dx = static_cast<float>(sample.dx);
    const float dz = static_cast<float>(sample.dz);
    const float length_sq = dx * dx + dz * dz;
    if (length_sq <= 0.0f) {
        return InputVector{};
    }
    // Limit to the unit circle so diagonal movement is not faster (plan T05).
    const float scale = length_sq > 1.0f ? (1.0f / std::sqrt(length_sq)) : 1.0f;
    InputVector out;
    out.x = dx * scale;
    out.z = dz * scale;
    return out;
}

// One 30Hz input report. InputSeq is independent from the TCP Frame Sequence
// (architecture section 8.1); it increments once per simulated input tick.
struct InputReport {
    std::uint32_t sequence = 0;
    InputVector vector;  // normalized; zero means "no movement intent"
};

// Tracks the client-side InputSeq. The client emits one report per 30Hz tick
// while in a room: the current intent (possibly zero after release) plus an
// increasing sequence number. Whether a report is transmitted is decided by
// the caller once the room/session state and A's PlayerInput payload exist.
class InputSequencer {
public:
    InputReport Tick(const InputSample& sample) {
        InputReport report;
        report.sequence = ++sequence_;
        report.vector = NormalizeInput(sample);
        return report;
    }

    std::uint32_t LastSequence() const { return sequence_; }
    bool HasSent() const { return sequence_ > 0; }
    void Reset() { sequence_ = 0; }

private:
    std::uint32_t sequence_ = 0;
};

}  // namespace odyssey::client::input
