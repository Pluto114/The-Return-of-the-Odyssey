#pragma once

#include <cstddef>
#include <optional>

namespace odyssey::client::ui {

struct RewardCardBounds {
    float x;
    float y;
    float width;
    float height;
};

// Coordinates are in the 1280x720 virtual canvas, before window scaling.
constexpr RewardCardBounds CardBounds(std::size_t index) {
    return {94.0f + static_cast<float>(index) * 365.0f, 436.0f, 342.0f, 126.0f};
}

// A digit of 0 means that no number key was pressed this frame.
inline std::optional<std::size_t> SelectRewardOption(int digit, bool mouse_clicked,
                                                     float canvas_x, float canvas_y,
                                                     std::size_t option_count) {
    if (digit >= 1 && digit <= 3 && static_cast<std::size_t>(digit) <= option_count) {
        return static_cast<std::size_t>(digit - 1);
    }
    if (mouse_clicked) {
        for (std::size_t i = 0; i < option_count && i < 3; ++i) {
            const auto card = CardBounds(i);
            if (canvas_x >= card.x && canvas_x < card.x + card.width &&
                canvas_y >= card.y && canvas_y < card.y + card.height) {
                return i;
            }
        }
    }
    return std::nullopt;
}

}  // namespace odyssey::client::ui
