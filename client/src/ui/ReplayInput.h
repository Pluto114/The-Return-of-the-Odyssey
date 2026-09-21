#pragma once

#include <cstdint>

namespace odyssey::client::ui {

// The current three-sector expedition ends only after the third clear. The
// server remains authoritative: it rejects a replay from any unfinished room.
inline constexpr std::uint32_t kExpeditionStageLimit = 3;

inline bool ExpeditionComplete(std::uint32_t stage_index, std::uint32_t stage_state,
                               double seconds_since_clear) {
    return stage_index >= kExpeditionStageLimit && stage_state == 2 &&
           seconds_since_clear >= 1.5;
}

inline bool CanReplay(std::uint32_t stage_index, std::uint32_t stage_state,
                      double seconds_since_clear) {
    return stage_state == 5 || ExpeditionComplete(stage_index, stage_state, seconds_since_clear);
}

}  // namespace odyssey::client::ui
