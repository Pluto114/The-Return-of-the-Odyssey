// Client-side combat view storage (D4 movement + combat consumption).
//
// Rules mirrored from the protocol:
//  - Monsters come from the authoritative WorldSnapshot and are a FULL set:
//    a monster missing from the newest snapshot is removed.
//  - Projectiles are NOT in the snapshot; they exist only through reliable
//    ProjectileSpawn / ProjectileDestroy events.
//  - Stage state is the snapshot's `stage` sub-message (single source of truth).
//
// Pure data + logic: no raylib/asio/protobuf here so it stays unit-testable.
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <map>
#include <set>
#include <vector>

namespace odyssey::client::sync {

struct MonsterEntity {
    std::uint64_t id = 0;
    float x = 0.0f;
    float z = 0.0f;
    float vx = 0.0f;
    float vz = 0.0f;
    float hp = 0.0f;
    float max_hp = 0.0f;
    std::uint32_t state = 0;  // 0 idle / 1 chase / 2 attack / 3 dead
};

struct PickupEntity {
    std::uint64_t id = 0;
    std::uint32_t kind = 0;  // 1 health / 2 weapon
    float x = 0.0f;
    float z = 0.0f;
    std::uint32_t equipment_id = 0;
    float value = 0.0f;
};

struct ProjectileVisual {
    std::uint64_t id = 0;
    std::uint64_t owner_id = 0;
    float x = 0.0f;
    float z = 0.0f;
    float vx = 0.0f;
    float vz = 0.0f;
    float origin_x = 0.0f;
    float origin_z = 0.0f;
    float impact_x = 0.0f;
    float impact_z = 0.0f;
    float impact_time_left = 0.0f;
    float flight_age = 0.0f;
    bool finishing = false;
    std::uint64_t expires_at_tick = 0;
    std::uint64_t server_tick = 0;
};

struct ProjectileImpact {
    float x = 0.0f;
    float z = 0.0f;
    float vx = 0.0f;
    float vz = 0.0f;
    float age = 0.0f;
};

struct StageInfo {
    std::uint32_t index = 0;
    std::int64_t seed = 0;
    std::uint32_t state = 0;
    std::uint32_t monsters_remaining = 0;
};

class CombatView {
public:
    // Replaces the monster set with the snapshot's full list; returns ids of
    // monsters that disappeared.
    std::vector<std::uint64_t> ApplyMonsters(const std::vector<MonsterEntity>& monsters) {
        std::vector<std::uint64_t> removed;
        std::map<std::uint64_t, MonsterEntity> next;
        for (const auto& monster : monsters) {
            next[monster.id] = monster;
        }
        for (const auto& [id, existing] : monsters_) {
            (void)existing;
            if (next.find(id) == next.end()) {
                removed.push_back(id);
            }
        }
        monsters_ = std::move(next);
        return removed;
    }

    const MonsterEntity* FindMonster(std::uint64_t id) const {
        const auto it = monsters_.find(id);
        return it == monsters_.end() ? nullptr : &it->second;
    }

    std::size_t MonsterCount() const { return monsters_.size(); }
    const std::map<std::uint64_t, MonsterEntity>& Monsters() const { return monsters_; }

    void ApplyPickups(const std::vector<PickupEntity>& pickups) {
        pickups_.clear();
        for (const auto& pickup : pickups) {
            pickups_[pickup.id] = pickup;
        }
    }

    std::size_t PickupCount() const { return pickups_.size(); }
    const std::map<std::uint64_t, PickupEntity>& Pickups() const { return pickups_; }

    // Spawn/destroy events are authoritative. A destroy can arrive in the same
    // network drain as its spawn; finish that short flight visually before
    // removing the sprite, without changing server-side collision or damage.
    void SpawnProjectile(const ProjectileVisual& projectile) {
        auto visual = projectile;
        visual.origin_x = projectile.x;
        visual.origin_z = projectile.z;
        visual.finishing = false;
        visual.impact_time_left = 0.0f;
        visual.flight_age = 0.0f;
        projectiles_[projectile.id] = visual;
    }

    bool DestroyProjectile(std::uint64_t id, float x, float z) {
        const auto it = projectiles_.find(id);
        if (it == projectiles_.end()) return false;
        auto& visual = it->second;
        const float dx = x - visual.x;
        const float dz = z - visual.z;
        const float distance = std::hypot(dx, dz);
        const float speed = std::hypot(visual.vx, visual.vz);
        // Do not fly backwards when a late destroy follows local extrapolation.
        if (distance < 0.02f || speed < 0.01f ||
            (dx * visual.vx + dz * visual.vz) <= 0.0f) {
            impacts_.push_back({x, z, visual.vx, visual.vz, 0.0f});
            projectiles_.erase(it);
            return true;
        }
        visual.finishing = true;
        visual.impact_x = x;
        visual.impact_z = z;
        visual.impact_time_left = std::clamp(distance / speed, 0.08f, 0.30f);
        return true;
    }

    bool DestroyProjectile(std::uint64_t id) {
        const auto it = projectiles_.find(id);
        return it != projectiles_.end() && DestroyProjectile(id, it->second.x, it->second.z);
    }

    void ClearProjectiles() {
        projectiles_.clear(); impacts_.clear(); hit_flash_.clear(); dead_.clear();
    }

    std::size_t ProjectileCount() const { return projectiles_.size(); }
    const std::map<std::uint64_t, ProjectileVisual>& Projectiles() const { return projectiles_; }
    const std::vector<ProjectileImpact>& Impacts() const { return impacts_; }
    static constexpr float kImpactSeconds = 0.20f;

    void SetStage(const StageInfo& stage) { stage_ = stage; }
    const StageInfo& Stage() const { return stage_; }

    // ---- Combat feedback (D5) --------------------------------------------
    // Damage/Death events drive short-lived client-side feedback only; the
    // authoritative HP still comes from the next snapshot.
    void ApplyDamageFx(std::uint64_t target_id) { hit_flash_[target_id] = kHitFlashSeconds; }
    void ApplyDeath(std::uint64_t entity_id) { dead_.insert(entity_id); }

    // Decays transient feedback (call once per frame with the frame delta).
    void Tick(float dt) {
        for (auto it = projectiles_.begin(); it != projectiles_.end();) {
            auto& projectile = it->second;
            projectile.flight_age += dt;
            if (projectile.finishing) {
                const float fraction = std::min(1.0f, dt / projectile.impact_time_left);
                projectile.x += (projectile.impact_x - projectile.x) * fraction;
                projectile.z += (projectile.impact_z - projectile.z) * fraction;
                projectile.impact_time_left -= dt;
                if (projectile.impact_time_left <= 0.0f || fraction >= 1.0f) {
                    impacts_.push_back({projectile.impact_x, projectile.impact_z,
                                        projectile.vx, projectile.vz, 0.0f});
                    it = projectiles_.erase(it);
                    continue;
                }
            } else {
                projectile.x += projectile.vx * dt;
                projectile.z += projectile.vz * dt;
            }
            ++it;
        }
        for (auto it = impacts_.begin(); it != impacts_.end();) {
            it->age += dt;
            if (it->age >= kImpactSeconds) it = impacts_.erase(it);
            else ++it;
        }
        for (auto it = hit_flash_.begin(); it != hit_flash_.end();) {
            it->second -= dt;
            if (it->second <= 0.0f) {
                it = hit_flash_.erase(it);
            } else {
                ++it;
            }
        }
    }

    bool IsDead(std::uint64_t id) const { return dead_.find(id) != dead_.end(); }
    bool IsHitFlashing(std::uint64_t id) const { return hit_flash_.find(id) != hit_flash_.end(); }
    std::size_t HitFlashCount() const { return hit_flash_.size(); }

    void Clear() {
        monsters_.clear();
        pickups_.clear();
        projectiles_.clear();
        impacts_.clear();
        hit_flash_.clear();
        dead_.clear();
        stage_ = StageInfo{};
    }

private:
    static constexpr float kHitFlashSeconds = 0.35f;

    std::map<std::uint64_t, MonsterEntity> monsters_;
    std::map<std::uint64_t, PickupEntity> pickups_;
    std::map<std::uint64_t, ProjectileVisual> projectiles_;
    std::vector<ProjectileImpact> impacts_;
    std::map<std::uint64_t, float> hit_flash_;
    std::set<std::uint64_t> dead_;
    StageInfo stage_;
};

}  // namespace odyssey::client::sync
