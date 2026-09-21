#pragma once

#include <cstdint>

namespace odyssey::client::ui {

// The limit comes from the server; an unknown limit must never imply victory.

inline bool ExpeditionComplete(std::uint32_t stage_index, std::uint32_t stage_state,
                               double seconds_since_clear, std::uint32_t stage_limit) {
    return stage_limit > 0 && stage_index >= stage_limit && stage_state == 2 &&
           seconds_since_clear >= 1.5;
}

inline bool CanReplay(std::uint32_t stage_index, std::uint32_t stage_state,
                      double seconds_since_clear, std::uint32_t stage_limit) {
    return stage_state == 5 || ExpeditionComplete(stage_index, stage_state, seconds_since_clear, stage_limit);
}

}  // namespace odyssey::client::ui
