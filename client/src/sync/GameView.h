// Client-side authoritative view storage.
//
// Mirrors the domain snapshot semantics documented by B (GAME-CORE-PHASE1):
// every snapshot is FULL: it lists the whole room by Player ID (ascending),
// and entities missing from the newest complete snapshot must be removed on
// the client. This layer is protocol-agnostic - the mapping from A's wire
// DTO to SnapshotView happens in the sync adapter that waits for the proto.
#pragma once

#include <algorithm>
#include <cstdint>
#include <map>
#include <optional>
#include <utility>
#include <vector>

namespace odyssey::client::sync {

// Server coordinate axes x/y map to client x/z (plan section 2).
struct PlayerView {
    std::uint64_t id = 0;
    float x = 0.0f;  // server x
    float z = 0.0f;  // server y
    float vx = 0.0f;
    float vz = 0.0f;
    // Server-side acknowledgement: newest input sequence applied by the last
    // simulated tick (B semantics - it is "the newest intent used this tick",
    // not a per-packet confirmation).
    std::uint32_t last_processed_input_seq = 0;
};

struct SnapshotView {
    std::uint64_t room_id = 0;
    std::uint64_t server_tick = 0;
    bool closed = false;
    std::vector<PlayerView> players;  // expected sorted ascending by id
};

class GameView {
public:
    // Replaces the world with the given full snapshot. Returns the ids that
    // disappeared (removed from view). A `closed` snapshot empties the view.
    std::vector<std::uint64_t> Apply(const SnapshotView& snapshot);

    const PlayerView* Find(std::uint64_t id) const;

    std::size_t PlayerCount() const { return players_.size(); }

    std::uint64_t RoomId() const { return room_id_; }
    std::uint64_t ServerTick() const { return server_tick_; }

    std::vector<PlayerView> Players() const {
        std::vector<PlayerView> result;
        result.reserve(players_.size());
        for (const auto& [id, player] : players_) {
            (void)id;
            result.push_back(player);
        }
        return result;
    }
    bool IsClosed() const { return closed_; }

    // Latest acknowledged input sequence for a given player id.
    std::optional<std::uint32_t> SelfAck(std::uint64_t self_id) const;

private:
    std::map<std::uint64_t, PlayerView> players_;
    std::uint64_t room_id_ = 0;
    std::uint64_t server_tick_ = 0;
    bool closed_ = false;
};

inline std::vector<std::uint64_t> GameView::Apply(const SnapshotView& snapshot) {
    room_id_ = snapshot.room_id;
    server_tick_ = snapshot.server_tick;
    closed_ = snapshot.closed;

    std::vector<std::uint64_t> removed;
    if (snapshot.closed) {
        for (const auto& [id, player] : players_) {
            (void)player;
            removed.push_back(id);
        }
        players_.clear();
        return removed;
    }

    // Entities absent from the complete snapshot disappear. Sort defensively
    // first so id-order guarantees hold even for an unsorted incoming list.
    std::vector<PlayerView> sorted = snapshot.players;
    std::sort(sorted.begin(), sorted.end(),
              [](const PlayerView& a, const PlayerView& b) { return a.id < b.id; });
    std::vector<std::uint64_t> next_ids;
    next_ids.reserve(sorted.size());
    for (const auto& player : sorted) {
        next_ids.push_back(player.id);
    }
    for (auto it = players_.begin(); it != players_.end();) {
        if (!std::binary_search(next_ids.begin(), next_ids.end(), it->first)) {
            removed.push_back(it->first);
            it = players_.erase(it);
        } else {
            ++it;
        }
    }
    for (const auto& player : sorted) {
        players_[player.id] = player;
    }
    return removed;
}

inline const PlayerView* GameView::Find(std::uint64_t id) const {
    const auto it = players_.find(id);
    return it == players_.end() ? nullptr : &it->second;
}

inline std::optional<std::uint32_t> GameView::SelfAck(std::uint64_t self_id) const {
    const PlayerView* player = Find(self_id);
    if (player == nullptr) {
        return std::nullopt;
    }
    return player->last_processed_input_seq;
}

}  // namespace odyssey::client::sync
