// Damage floater pool and hit de-duplication (Katana Zero HUD).
//
// Fixed storage only: the render loop must not allocate, so the pool is a
// preallocated array of 128 slots with FIFO replacement when it overflows, and
// the dedupe table is a preallocated array of 256 entries aged out by server tick.
//
// Both are cleared on a cross-session reconnect/resume so events stay idempotent
// and memory cannot grow without bound.
//
// Raylib-free on purpose.
#pragma once

#include <array>
#include <cstddef>
#include <cstdint>

namespace odyssey::client::ui {

// A damage event's identity on the wire: the same source hitting the same target
// inside one server tick is the same hit (redundant duplicate packets, or the
// same hit reported by two paths).
struct FloaterKey {
    std::uint64_t server_tick = 0;
    std::uint32_t source_id = 0;
    std::uint32_t target_id = 0;
};

inline bool operator==(const FloaterKey& lhs, const FloaterKey& rhs) {
    return lhs.server_tick == rhs.server_tick && lhs.source_id == rhs.source_id &&
           lhs.target_id == rhs.target_id;
}
inline bool operator!=(const FloaterKey& lhs, const FloaterKey& rhs) { return !(lhs == rhs); }

struct Floater {
    bool active = false;
    FloaterKey key;
    float world_x = 0.0f;
    float world_z = 0.0f;
    float value = 0.0f;        // damage amount for the text
    float born_seconds = 0.0f; // monotonic clock at spawn
    float lifetime = 0.9f;     // seconds until it fades out
};

// Preallocated ring of floaters. `Spawn` never allocates; when every slot is in
// use the oldest one is overwritten (FIFO), which is the documented trade-off of
// a bounded pool.
class FloaterPool {
public:
    static constexpr std::size_t kCapacity = 128;
    static constexpr float kDefaultLifetimeSeconds = 0.9f;

    // Returns true when a slot was taken. `enabled == false` (accessibility
    // `disable_damage_floaters`) consumes the event without occupying a slot: the
    // hit is still recorded by the dedupe table and simply not shown.
    bool Spawn(const FloaterKey& key, float world_x, float world_z, float value, float now,
               bool enabled = true) {
        if (!enabled) {
            return false;
        }
        Floater& slot = slots_[next_];
        if (slot.active) {
            ++replaced_;
        }
        slot.active = true;
        slot.key = key;
        slot.world_x = world_x;
        slot.world_z = world_z;
        slot.value = value;
        slot.born_seconds = now;
        slot.lifetime = kDefaultLifetimeSeconds;
        next_ = (next_ + 1) % kCapacity;
        return true;
    }

    // Retires floaters whose lifetime elapsed. Call once per frame with the same
    // monotonic clock used for `Spawn`.
    void Tick(float now) {
        for (Floater& slot : slots_) {
            if (slot.active && (now - slot.born_seconds) >= slot.lifetime) {
                slot.active = false;
            }
        }
    }

    void Clear() {
        for (Floater& slot : slots_) {
            slot.active = false;
        }
        next_ = 0;
        replaced_ = 0;
    }

    std::size_t ActiveCount() const {
        std::size_t count = 0;
        for (const Floater& slot : slots_) {
            if (slot.active) {
                ++count;
            }
        }
        return count;
    }

    // Diagnostics: how many live floaters the ring had to overwrite.
    std::uint64_t ReplacedCount() const { return replaced_; }
    bool At(std::size_t index, Floater& out) const {
        if (index >= kCapacity || !slots_[index].active) {
            return false;
        }
        out = slots_[index];
        return true;
    }

private:
    std::array<Floater, kCapacity> slots_{};
    std::size_t next_ = 0;
    std::uint64_t replaced_ = 0;
};

// Remembers which hits were already shown. Aged out by server tick (the tick
// counter is monotonic, so no wall clock is needed and tests stay deterministic).
class DamageDedupeTable {
public:
    static constexpr std::size_t kCapacity = 256;
    // Roughly four seconds at the 30Hz tick rate.
    static constexpr std::uint64_t kMaxAgeTicks = 120;

    // True when this hit has not been seen: the caller should render it.
    bool Accept(const FloaterKey& key) {
        if (key.server_tick > latest_tick_) {
            latest_tick_ = key.server_tick;
        }
        Expire();
        for (std::size_t i = 0; i < kCapacity; ++i) {
            if (entries_[i].used && entries_[i].key == key) {
                return false;
            }
        }
        Entry& slot = entries_[next_];
        slot.used = true;
        slot.key = key;
        next_ = (next_ + 1) % kCapacity;
        return true;
    }

    // Drops entries older than kMaxAgeTicks relative to the newest tick seen.
    void Expire() {
        for (Entry& entry : entries_) {
            if (entry.used && latest_tick_ > entry.key.server_tick &&
                (latest_tick_ - entry.key.server_tick) > kMaxAgeTicks) {
                entry.used = false;
            }
        }
    }

    void Clear() {
        for (Entry& entry : entries_) {
            entry.used = false;
        }
        next_ = 0;
        latest_tick_ = 0;
    }

    std::size_t Size() const {
        std::size_t count = 0;
        for (const Entry& entry : entries_) {
            if (entry.used) {
                ++count;
            }
        }
        return count;
    }

    std::uint64_t LatestTick() const { return latest_tick_; }

private:
    struct Entry {
        bool used = false;
        FloaterKey key;
    };
    std::array<Entry, kCapacity> entries_{};
    std::size_t next_ = 0;
    std::uint64_t latest_tick_ = 0;
};

}  // namespace odyssey::client::ui
