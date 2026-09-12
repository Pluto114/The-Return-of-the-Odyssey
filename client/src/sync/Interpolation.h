// Snapshot interpolation for remote players and monsters (D9).
//
// Snapshots arrive at 10Hz while rendering runs at 60Hz, so remote entities are
// rendered slightly in the past (default: one snapshot interval) and lerped
// between the two bracketing authoritative samples. Local prediction stays a
// separate concern (see Prediction.h). Pure logic: unit-testable.
#pragma once

#include <cstdint>
#include <deque>
#include <map>
#include <utility>
#include <vector>

namespace odyssey::client::sync {

struct InterpSample {
    std::uint64_t tick = 0;
    float x = 0.0f;
    float z = 0.0f;
};

class EntityTrack {
public:
    void Push(std::uint64_t tick, float x, float z) {
        if (!samples_.empty() && samples_.back().tick == tick) {
            samples_.back().x = x;
            samples_.back().z = z;
            return;
        }
        if (!samples_.empty() && tick < samples_.back().tick) {
            return;  // out-of-order sample: ignore rather than rewind
        }
        samples_.push_back(InterpSample{tick, x, z});
        while (samples_.size() > kMaxSamples) {
            samples_.pop_front();
        }
    }

    // Samples at target_tick, clamping to the newest/oldest sample.
    bool Sample(double target_tick, float& x, float& z) const {
        if (samples_.empty()) {
            return false;
        }
        if (target_tick <= static_cast<double>(samples_.front().tick)) {
            x = samples_.front().x;
            z = samples_.front().z;
            return true;
        }
        if (target_tick >= static_cast<double>(samples_.back().tick)) {
            x = samples_.back().x;
            z = samples_.back().z;
            return true;
        }
        for (std::size_t i = 1; i < samples_.size(); ++i) {
            const auto& previous = samples_[i - 1];
            const auto& next = samples_[i];
            if (target_tick <= static_cast<double>(next.tick)) {
                const double span = static_cast<double>(next.tick - previous.tick);
                const double alpha = span > 0.0 ? (target_tick - static_cast<double>(previous.tick)) / span : 0.0;
                x = static_cast<float>(previous.x + (next.x - previous.x) * alpha);
                z = static_cast<float>(previous.z + (next.z - previous.z) * alpha);
                return true;
            }
        }
        x = samples_.back().x;
        z = samples_.back().z;
        return true;
    }

    std::size_t Size() const { return samples_.size(); }
    std::uint64_t LatestTick() const { return samples_.empty() ? 0 : samples_.back().tick; }

private:
    static constexpr std::size_t kMaxSamples = 16;
    std::deque<InterpSample> samples_;
};

class SnapshotInterpolator {
public:
    // Rendering delay in snapshot ticks (10Hz snapshots => 1 tick ~ 100ms).
    void SetDelayTicks(double delay_ticks) { delay_ticks_ = delay_ticks < 0.0 ? 0.0 : delay_ticks; }
    double DelayTicks() const { return delay_ticks_; }

    // Full-set apply: entities missing from the newest snapshot are removed.
    std::vector<std::uint64_t>
    ApplyEntities(const std::map<std::uint64_t, std::pair<float, float>>& positions,
                  std::uint64_t tick) {
        std::vector<std::uint64_t> removed;
        for (auto it = tracks_.begin(); it != tracks_.end();) {
            if (positions.find(it->first) == positions.end()) {
                removed.push_back(it->first);
                it = tracks_.erase(it);
            } else {
                ++it;
            }
        }
        for (const auto& [id, position] : positions) {
            tracks_[id].Push(tick, position.first, position.second);
        }
        if (tick > latest_tick_) {
            latest_tick_ = tick;
        }
        return removed;
    }

    // Position to render this frame (latest tick minus the render delay).
    bool SampleEntity(std::uint64_t id, float& x, float& z) const {
        const auto it = tracks_.find(id);
        if (it == tracks_.end()) {
            return false;
        }
        return it->second.Sample(RenderTick(), x, z);
    }

    double RenderTick() const {
        const double target = static_cast<double>(latest_tick_) - delay_ticks_;
        return target < 0.0 ? 0.0 : target;
    }

    std::uint64_t LatestTick() const { return latest_tick_; }
    std::size_t Count() const { return tracks_.size(); }
    bool HasEntity(std::uint64_t id) const { return tracks_.find(id) != tracks_.end(); }

    void Clear() {
        tracks_.clear();
        latest_tick_ = 0;
    }

private:
    std::map<std::uint64_t, EntityTrack> tracks_;
    std::uint64_t latest_tick_ = 0;
    double delay_ticks_ = 1.0;
};

}  // namespace odyssey::client::sync
