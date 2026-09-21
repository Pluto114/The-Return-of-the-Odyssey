#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <map>
#include <set>
#include <vector>

namespace odyssey::client::sync {

// Combat-owned world entities (monsters/projectiles) use the high bit. Player
// ids are session identities below this boundary. The scoreboard consumes only
// authoritative server events and never accepts a client-submitted score.
inline constexpr std::uint64_t kFirstWorldEntityId = std::uint64_t{1} << 63;
inline constexpr double kServerTicksPerSecond = 30.0;

struct PlayerMatchScore {
    std::uint64_t player_id = 0;
    double damage_dealt = 0.0;
    double damage_taken = 0.0;
    std::uint32_t kills = 0;
    std::uint32_t deaths = 0;
    float health = 0.0f;
    float max_health = 0.0f;
    bool alive = false;
    std::int64_t score = 0;
};

class MatchScoreboard {
public:
    void Reset() {
        players_.clear();
        cleared_stages_.clear();
        started_ = false;
        started_at_tick_ = 0;
        ended_at_tick_ = 0;
    }

    void ObservePlayer(std::uint64_t player_id, float health, float max_health, bool alive) {
        if (!IsPlayer(player_id)) return;
        auto& player = players_[player_id];
        player.player_id = player_id;
        player.health = std::max(0.0f, health);
        player.max_health = std::max(0.0f, max_health);
        player.alive = alive;
    }

    void ObserveDamage(std::uint64_t source_id, std::uint64_t target_id, double amount) {
        if (!std::isfinite(amount) || amount <= 0.0) return;
        if (IsPlayer(source_id) && IsWorldEntity(target_id)) {
            Player(source_id).damage_dealt += amount;
        }
        if (IsWorldEntity(source_id) && IsPlayer(target_id)) {
            Player(target_id).damage_taken += amount;
        }
    }

    void ObserveDeath(std::uint64_t entity_id, std::uint64_t killer_id) {
        if (IsWorldEntity(entity_id) && IsPlayer(killer_id)) {
            ++Player(killer_id).kills;
        }
        if (IsPlayer(entity_id)) {
            auto& player = Player(entity_id);
            ++player.deaths;
            player.health = 0.0f;
            player.alive = false;
        }
    }

    void ObserveStageStarted(std::uint32_t stage_index, std::uint64_t server_tick) {
        if (stage_index == 0) return;
        if (!started_ || stage_index == 1) {
            started_ = true;
            started_at_tick_ = server_tick;
            ended_at_tick_ = 0;
        }
    }

    void ObserveStageCleared(std::uint32_t stage_index, std::uint64_t server_tick) {
        if (stage_index == 0) return;
        cleared_stages_.insert(stage_index);
        ended_at_tick_ = std::max(ended_at_tick_, server_tick);
    }

    void ObserveTeamDefeated(std::uint64_t server_tick) {
        ended_at_tick_ = std::max(ended_at_tick_, server_tick);
    }

    [[nodiscard]] std::vector<PlayerMatchScore> Rankings() const {
        std::vector<PlayerMatchScore> rankings;
        rankings.reserve(players_.size());
        for (const auto& [id, stored] : players_) {
            (void)id;
            auto player = stored;
            player.score = PlayerScore(player);
            rankings.push_back(player);
        }
        std::sort(rankings.begin(), rankings.end(), [](const auto& left, const auto& right) {
            if (left.score != right.score) return left.score > right.score;
            if (left.kills != right.kills) return left.kills > right.kills;
            if (left.damage_dealt != right.damage_dealt) return left.damage_dealt > right.damage_dealt;
            return left.player_id < right.player_id;
        });
        return rankings;
    }

    [[nodiscard]] std::uint32_t ClearedStages() const {
        return static_cast<std::uint32_t>(cleared_stages_.size());
    }

    [[nodiscard]] double DurationSeconds() const {
        if (!started_ || ended_at_tick_ <= started_at_tick_) return 0.0;
        return static_cast<double>(ended_at_tick_ - started_at_tick_) / kServerTicksPerSecond;
    }

    [[nodiscard]] std::int64_t TeamScore() const {
        std::uint64_t kills = 0;
        std::uint64_t deaths = 0;
        for (const auto& [id, player] : players_) {
            (void)id;
            kills += player.kills;
            deaths += player.deaths;
        }
        const auto time_penalty = static_cast<std::int64_t>(std::llround(DurationSeconds() * 5.0));
        const std::int64_t score = static_cast<std::int64_t>(ClearedStages()) * 1000 +
                                   static_cast<std::int64_t>(kills) * 100 -
                                   static_cast<std::int64_t>(deaths) * 300 - time_penalty;
        return std::max<std::int64_t>(0, score);
    }

private:
    static bool IsPlayer(std::uint64_t id) { return id != 0 && id < kFirstWorldEntityId; }
    static bool IsWorldEntity(std::uint64_t id) { return id >= kFirstWorldEntityId; }

    PlayerMatchScore& Player(std::uint64_t player_id) {
        auto& player = players_[player_id];
        player.player_id = player_id;
        return player;
    }

    static std::int64_t PlayerScore(const PlayerMatchScore& player) {
        const std::int64_t score = static_cast<std::int64_t>(std::llround(player.damage_dealt)) +
                                   static_cast<std::int64_t>(player.kills) * 200 +
                                   static_cast<std::int64_t>(std::llround(player.health * 2.0f)) -
                                   static_cast<std::int64_t>(player.deaths) * 300;
        return std::max<std::int64_t>(0, score);
    }

    std::map<std::uint64_t, PlayerMatchScore> players_;
    std::set<std::uint32_t> cleared_stages_;
    bool started_ = false;
    std::uint64_t started_at_tick_ = 0;
    std::uint64_t ended_at_tick_ = 0;
};

}  // namespace odyssey::client::sync
