// Netcode metric estimators for the debug overlay (P3).
//
// The dashboard metrics belong to role D and are compared against Grafana naming and
// semantics, so the formulas here are the ones the plan fixes rather than anything
// invented locally:
//
//   * RTT          - ping/pong round trip, smoothed with EMA(alpha = 0.1)
//   * Server tick  - delta(server_tick) / delta(t): snapshots arrive at 10Hz but each
//                    carries the 30Hz tick, so the RATE OF TICKS is what is reported.
//                    Reporting the snapshot rate as the tick rate is exactly the
//                    mistake the plan calls out.
//   * Prediction error - EMA of the reconciliation distance, updated only when a
//                    snapshot arrives (never per frame).
//
// Pure and raylib-free so the arithmetic is unit tested instead of eyeballed.
#pragma once

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <cstdint>

namespace odyssey::client::ui {

// Exponentially weighted moving average. The first sample seeds the value so a cold
// start reads the measurement rather than zero.
class Ema {
public:
    explicit Ema(float alpha = 0.1f) : alpha_(std::clamp(alpha, 0.0f, 1.0f)) {}

    void Add(float sample) {
        if (!std::isfinite(sample)) {
            return;  // a NaN from a degenerate delta must not poison the average
        }
        if (!has_value_) {
            value_ = sample;
            has_value_ = true;
            return;
        }
        value_ += alpha_ * (sample - value_);
        ++samples_;
    }

    void Reset() {
        value_ = 0.0f;
        has_value_ = false;
        samples_ = 0;
    }

    float Value() const { return value_; }
    bool HasValue() const { return has_value_; }
    std::uint64_t Samples() const { return samples_; }

private:
    float alpha_ = 0.1f;
    float value_ = 0.0f;
    bool has_value_ = false;
    std::uint64_t samples_ = 0;
};

// Ping/pong round trip in milliseconds.
class RttEstimator {
public:
    explicit RttEstimator(float alpha = 0.1f) : ema_(alpha) {}

    // Call when a Pong arrives, with the client_time_ms of the Ping it answers.
    void OnPong(std::uint64_t ping_client_time_ms, std::uint64_t pong_arrived_ms) {
        if (pong_arrived_ms < ping_client_time_ms) {
            return;  // clock went backwards; ignore rather than report a negative RTT
        }
        ema_.Add(static_cast<float>(pong_arrived_ms - ping_client_time_ms));
    }

    float Milliseconds() const { return ema_.Value(); }
    bool HasValue() const { return ema_.HasValue(); }
    void Reset() { ema_.Reset(); }

private:
    Ema ema_;
};

// Server tick rate in Hz, derived from the tick carried by consecutive snapshots.
class ServerTickRateEstimator {
public:
    explicit ServerTickRateEstimator(float alpha = 0.2f) : ema_(alpha) {}

    // `snapshot_seconds` is the monotonic arrival time of the snapshot.
    void OnSnapshot(std::uint64_t server_tick, double snapshot_seconds) {
        if (has_previous_) {
            const double elapsed = snapshot_seconds - previous_seconds_;
            if (elapsed > 1e-4) {
                const double ticks = (server_tick >= previous_tick_)
                                         ? static_cast<double>(server_tick - previous_tick_)
                                         : 0.0;  // server restarted: no samples
                if (ticks > 0.0) {
                    ema_.Add(static_cast<float>(ticks / elapsed));
                }
            }
        }
        previous_tick_ = server_tick;
        previous_seconds_ = snapshot_seconds;
        has_previous_ = true;
    }

    float Hertz() const { return ema_.Value(); }
    bool HasValue() const { return ema_.HasValue(); }
    void Reset() {
        ema_.Reset();
        has_previous_ = false;
    }

private:
    Ema ema_;
    std::uint64_t previous_tick_ = 0;
    double previous_seconds_ = 0.0;
    bool has_previous_ = false;
};

// Prediction error: how far reconciliation had to move the predicted position.
class PredictionErrorEstimator {
public:
    explicit PredictionErrorEstimator(float alpha = 0.1f) : ema_(alpha) {}

    // Call ONLY when a snapshot arrives (the plan is explicit that this is
    // snapshot-driven, not per-frame).
    void OnSnapshotCorrection(float correction_distance) { ema_.Add(correction_distance); }

    float Distance() const { return ema_.Value(); }
    bool HasValue() const { return ema_.HasValue(); }
    void Reset() { ema_.Reset(); }

private:
    Ema ema_;
};

// Fixed-capacity ring of samples for the overlay's little line graph. No allocation
// after construction, so it can be appended to from the render loop.
class MetricSeries {
public:
    static constexpr std::size_t kCapacity = 120;

    void Add(float sample) {
        if (!std::isfinite(sample)) {
            return;
        }
        samples_[next_] = sample;
        next_ = (next_ + 1) % kCapacity;
        if (count_ < kCapacity) {
            ++count_;
        }
        max_value_ = std::max(max_value_, sample);
    }

    void Reset() {
        for (float& sample : samples_) {
            sample = 0.0f;
        }
        next_ = 0;
        count_ = 0;
        max_value_ = 0.0f;
    }

    std::size_t Count() const { return count_; }
    float MaxValue() const { return max_value_; }
    // Oldest first, so index 0 is the leftmost point of the graph.
    float At(std::size_t index) const {
        if (index >= count_) {
            return 0.0f;
        }
        const std::size_t start = (count_ == kCapacity) ? next_ : 0;
        return samples_[(start + index) % kCapacity];
    }

private:
    float samples_[kCapacity] = {};
    std::size_t next_ = 0;
    std::size_t count_ = 0;
    float max_value_ = 0.0f;
};

}  // namespace odyssey::client::ui
