#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <map>
#include <vector>

namespace odyssey::client::sync {

// Presentation only: never changes authoritative positions, damage or timing.
// Recent positions survive snapshot removals so a lethal event can still burst.
class CombatFeedback {
public:
    struct Particle { float x, z, vx, vz, age, lifetime; bool danger; };
    struct Number { float x, z, amount, age; bool danger; };
    static constexpr std::size_t kMaxParticles = 256, kMaxNumbers = 48, kMaxTargets = 256;

    void Track(std::uint64_t id, float x, float z) {
        if (!positions_.count(id) && positions_.size() >= kMaxTargets) return;
        positions_[id] = {x, z, 1.0f};
    }
    void Damage(std::uint64_t id, float amount, bool self_hurt, bool self_hit) {
        if (!(amount > 0) || !std::isfinite(amount)) return;
        if (self_hurt) hurt_ = 0.28f;
        if (self_hit) hit_ = 0.16f;
        const auto it = positions_.find(id);
        if (it == positions_.end()) return;
        const auto& p = it->second;
        if (numbers_.size() >= kMaxNumbers) numbers_.erase(numbers_.begin());
        numbers_.push_back({p.x, p.z, amount, 0, self_hurt});
        Burst(p.x, p.z, 7, 2.3f, self_hurt);
    }
    void Death(std::uint64_t id) {
        const auto it = positions_.find(id);
        if (it != positions_.end()) Burst(it->second.x, it->second.z, 20, 4.4f, false);
    }
    void Muzzle(float x, float z) { Burst(x, z, 3, 1.4f, false); }
    void Tick(float dt) {
        if (!std::isfinite(dt) || dt <= 0) return;
        hit_ = std::max(0.0f, hit_ - dt);
        hurt_ = std::max(0.0f, hurt_ - dt);
        for (auto& p : particles_) { p.age += dt; p.x += p.vx * dt; p.z += p.vz * dt; }
        for (auto& n : numbers_) n.age += dt;
        particles_.erase(std::remove_if(particles_.begin(), particles_.end(),
                         [](const auto& p) { return p.age >= p.lifetime; }), particles_.end());
        numbers_.erase(std::remove_if(numbers_.begin(), numbers_.end(),
                       [](const auto& n) { return n.age >= 0.8f; }), numbers_.end());
        for (auto it = positions_.begin(); it != positions_.end();) {
            it->second.ttl -= dt;
            if (it->second.ttl <= 0) it = positions_.erase(it); else ++it;
        }
    }
    void Clear() { particles_.clear(); numbers_.clear(); positions_.clear(); hit_ = hurt_ = 0; }
    const auto& Particles() const { return particles_; }
    const auto& Numbers() const { return numbers_; }
    float HitMarker() const { return hit_ / 0.16f; }
    float HurtFlash() const { return hurt_ / 0.28f; }

private:
    struct Position { float x, z, ttl; };
    void Burst(float x, float z, int count, float speed, bool danger) {
        for (int i = 0; i < count && particles_.size() < kMaxParticles; ++i) {
            const float angle = i * 2.399963f + static_cast<float>(serial_++ % 19);
            const float velocity = speed * (0.55f + 0.15f * (i % 4));
            particles_.push_back({x, z, std::cos(angle) * velocity, std::sin(angle) * velocity,
                                  0, 0.2f + 0.06f * (i % 5), danger});
        }
    }
    std::map<std::uint64_t, Position> positions_;
    std::vector<Particle> particles_;
    std::vector<Number> numbers_;
    std::uint32_t serial_ = 0;
    float hit_ = 0, hurt_ = 0;
};
} // namespace odyssey::client::sync
