// Segmented (energy-block) health bar math for the pixel HUD.
//
// The bar is drawn as a fixed number of blocks: fully filled blocks plus one
// partially filled boundary block. All of the arithmetic lives here so the
// renderer only consumes numbers and the rules stay unit testable and
// raylib-free.
#pragma once

#include <algorithm>
#include <cmath>

namespace odyssey::client::ui {

struct HealthSegments {
    int segments = 0;      // blocks requested
    int filled = 0;        // fully filled blocks
    float partial = 0.0f;  // 0..1 fill of the block after `filled`
    float fraction = 0.0f;  // clamped hp / max_hp
    bool alive = false;    // fraction > 0
};

// hp <= 0 -> nothing filled and not alive; hp >= max_hp -> every block filled and
// no partial block. Non-finite input and a non-positive max_hp are treated as
// "no data" (zeroed bar) instead of producing NaN geometry.
inline HealthSegments ComputeHealthSegments(float hp, float max_hp, int segments) {
    HealthSegments out;
    if (segments <= 0) {
        return out;
    }
    out.segments = segments;
    if (!std::isfinite(hp) || !std::isfinite(max_hp) || max_hp <= 0.0f) {
        return out;
    }
    out.fraction = std::clamp(hp / max_hp, 0.0f, 1.0f);
    out.alive = out.fraction > 0.0f;
    const float exact = out.fraction * static_cast<float>(segments);
    out.filled = static_cast<int>(std::floor(exact));
    if (out.filled >= segments) {
        out.filled = segments;
        out.partial = 0.0f;
    } else {
        out.partial = exact - static_cast<float>(out.filled);
    }
    return out;
}

// Pixel width of one block (and of the gap between blocks) inside `total_width`.
// Returns 0 for a degenerate bar so callers can skip drawing.
inline float SegmentWidth(float total_width, int segments, float gap) {
    if (segments <= 0 || total_width <= 0.0f || gap < 0.0f) {
        return 0.0f;
    }
    const float usable = total_width - gap * static_cast<float>(segments - 1);
    if (usable <= 0.0f) {
        return 0.0f;
    }
    return usable / static_cast<float>(segments);
}

}  // namespace odyssey::client::ui
