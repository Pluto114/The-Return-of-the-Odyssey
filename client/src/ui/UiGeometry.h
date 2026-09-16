// Window / render-target / world coordinate transforms for the Katana Zero UI.
//
// The game always renders into a fixed 960x540 RenderTexture that is blitted to
// the window with an INTEGER scale and centred letterbox bars. Three transforms
// cover every consumer:
//
//   WindowToRT    window mouse pixels -> RT pixels (letterbox offset + scale),
//                 clamped onto the RT edge when the pointer sits in a black bar
//                 so aim never jumps to a mirrored angle;
//   RTToWorld / WorldToRT  RT pixels <-> world [0,20]^2 for crosshairs, damage
//                 floaters and anything drawn in arena space;
//   WorldToWindow  world -> window in one step, for anchoring top-layer ImGui
//                 overlays onto world positions.
//
// The ray texture is blitted with a flipped source height (see the render loop);
// that flip belongs to the blit, NOT to these transforms, so RT y increases
// downwards exactly like window y.
//
// Raylib-free on purpose: the headless logic suite has no window dependency.
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>

namespace odyssey::client::ui {

inline constexpr float kTargetWidth = 960.0f;
inline constexpr float kTargetHeight = 540.0f;
inline constexpr float kWorldSize = 20.0f;  // world is [0,20]^2 on both axes

struct Vec2f {
    float x = 0.0f;
    float y = 0.0f;
};

struct Rectf {
    float x = 0.0f;
    float y = 0.0f;
    float w = 0.0f;
    float h = 0.0f;
};

struct ViewportLayout {
    float scale = 1.0f;
    float offset_x = 0.0f;  // letterbox bar width on the left
    float offset_y = 0.0f;  // letterbox bar height on the top
};

// Integer letterbox fit: scale = max(1, floor(min(w/960, h/540))), centred.
// Integer-only scaling is what keeps the pixel art crisp; a fit below 1 is
// clamped to 1 so a window smaller than the target crops symmetrically instead
// of blurring. A minimised window (0 or negative size) reports a 1:1 layout at
// the origin rather than NaN offsets.
inline ViewportLayout ComputeViewportLayout(int window_w, int window_h) {
    ViewportLayout layout;
    if (window_w <= 0 || window_h <= 0) {
        return layout;
    }
    const float fit = std::min(static_cast<float>(window_w) / kTargetWidth,
                               static_cast<float>(window_h) / kTargetHeight);
    layout.scale = static_cast<float>(std::max(1, static_cast<int>(std::floor(fit))));
    layout.offset_x = (static_cast<float>(window_w) - kTargetWidth * layout.scale) * 0.5f;
    layout.offset_y = (static_cast<float>(window_h) - kTargetHeight * layout.scale) * 0.5f;
    return layout;
}

// Window mouse position -> RT pixels. Positions inside the letterbox bars clamp
// to the RT edge (0 or 960/540) instead of going negative or past the target.
inline Vec2f WindowToRT(const Vec2f& window_pos, const ViewportLayout& layout) {
    if (layout.scale <= 0.0f) {
        return Vec2f{};
    }
    const float x = (window_pos.x - layout.offset_x) / layout.scale;
    const float y = (window_pos.y - layout.offset_y) / layout.scale;
    return Vec2f{std::clamp(x, 0.0f, kTargetWidth), std::clamp(y, 0.0f, kTargetHeight)};
}

// RT pixels -> world coordinates inside the rectangle the arena occupies on the
// RT. A degenerate view reports the world origin instead of dividing by zero.
inline Vec2f RTToWorld(const Vec2f& rt_pos, const Rectf& view) {
    if (view.w <= 0.0f || view.h <= 0.0f) {
        return Vec2f{};
    }
    return Vec2f{(rt_pos.x - view.x) / view.w * kWorldSize,
                 (rt_pos.y - view.y) / view.h * kWorldSize};
}

// World coordinates -> RT pixels (inverse of RTToWorld).
inline Vec2f WorldToRT(const Vec2f& world_pos, const Rectf& view) {
    if (view.w <= 0.0f || view.h <= 0.0f) {
        return Vec2f{view.x, view.y};
    }
    return Vec2f{view.x + world_pos.x / kWorldSize * view.w,
                 view.y + world_pos.y / kWorldSize * view.h};
}

// World coordinates -> window pixels, for top-layer (ImGui) anchoring.
inline Vec2f WorldToWindow(const Vec2f& world_pos, const Rectf& view,
                           const ViewportLayout& layout) {
    const Vec2f rt = WorldToRT(world_pos, view);
    return Vec2f{rt.x * layout.scale + layout.offset_x, rt.y * layout.scale + layout.offset_y};
}

// True when a window-space point lands inside the rendered 960x540 image rather
// than in a letterbox bar (used to keep the last aim direction while the pointer
// is outside, instead of snapping it).
inline bool IsInsideTarget(const Vec2f& window_pos, const ViewportLayout& layout) {
    if (layout.scale <= 0.0f) {
        return false;
    }
    return window_pos.x >= layout.offset_x &&
           window_pos.x <= layout.offset_x + kTargetWidth * layout.scale &&
           window_pos.y >= layout.offset_y &&
           window_pos.y <= layout.offset_y + kTargetHeight * layout.scale;
}

}  // namespace odyssey::client::ui
