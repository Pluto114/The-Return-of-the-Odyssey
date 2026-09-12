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

#include <cstdint>
#include <map>
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

struct ProjectileVisual {
    std::uint64_t id = 0;
    std::uint64_t owner_id = 0;
    float x = 0.0f;
    float z = 0.0f;
    float vx = 0.0f;
    float vz = 0.0f;
    std::uint64_t expires_at_tick = 0;
    std::uint64_t server_tick = 0;
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

    // Projectiles: created only by spawn events, removed only by destroy events.
    void SpawnProjectile(const ProjectileVisual& projectile) {
        projectiles_[projectile.id] = projectile;
    }

    bool DestroyProjectile(std::uint64_t id) {
        return projectiles_.erase(id) > 0;
    }

    void ClearProjectiles() { projectiles_.clear(); }

    std::size_t ProjectileCount() const { return projectiles_.size(); }
    const std::map<std::uint64_t, ProjectileVisual>& Projectiles() const { return projectiles_; }

    void SetStage(const StageInfo& stage) { stage_ = stage; }
    const StageInfo& Stage() const { return stage_; }

    void Clear() {
        monsters_.clear();
        projectiles_.clear();
        stage_ = StageInfo{};
    }

private:
    std::map<std::uint64_t, MonsterEntity> monsters_;
    std::map<std::uint64_t, ProjectileVisual> projectiles_;
    StageInfo stage_;
};

}  // namespace odyssey::client::sync
