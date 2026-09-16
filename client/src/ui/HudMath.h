// HUD feedback math: hit-direction marker, damaged-bar ghost and the pixel
// crosshair outline.
//
// Pure and raylib-free so the rules (when a marker appears, how fast the white
// damage ghost drains, where the hexagon vertices land) are unit tested instead
// of being tuned by eye inside the renderer.
#pragma once

#include <algorithm>
#include <cmath>

namespace odyssey::client::ui {

// Direction the last hit came from, shown as an arc around the player.
struct HitMarker {
    bool active = false;
    float dir_x = 0.0f;  // unit vector from the player towards the attacker
    float dir_z = 0.0f;
    float born_seconds = 0.0f;
    float lifetime = 0.7f;
};

// Returns false when the attacker sits on top of the player (no meaningful
// direction) or the input is not finite; the previous marker is left untouched.
inline bool SetHitDirection(HitMarker& marker, float self_x, float self_z, float source_x,
                            float source_z, float now) {
    const float dx = source_x - self_x;
    const float dz = source_z - self_z;
    const float length_sq = dx * dx + dz * dz;
    if (!std::isfinite(length_sq) || length_sq <= 1e-8f) {
        return false;
    }
    const float inverse = 1.0f / std::sqrt(length_sq);
    marker.dir_x = dx * inverse;
    marker.dir_z = dz * inverse;
    marker.born_seconds = now;
    marker.active = true;
    return true;
}

inline void UpdateHitMarker(HitMarker& marker, float now) {
    if (marker.active && (now - marker.born_seconds) >= marker.lifetime) {
        marker.active = false;
    }
}

// 1 at the moment of the hit, fading to 0 at the end of the lifetime.
inline float HitMarkerFade(const HitMarker& marker, float now) {
    if (!marker.active || marker.lifetime <= 0.0f) {
        return 0.0f;
    }
    const float progress = (now - marker.born_seconds) / marker.lifetime;
    return std::clamp(1.0f - progress, 0.0f, 1.0f);
}

// White ghost of the health bar: it holds the pre-hit level briefly so the loss
// reads, then drains into the real bar.
struct DamageGhost {
    bool active = false;
    float shown_fraction = 0.0f;  // level the ghost bar still covers
    float amount = 0.0f;          // 0..1 how far ahead of the real bar it is
    float since_hit = 0.0f;
    float hold_seconds = 0.12f;
    float catchup_per_second = 1.1f;
};

// Call with the health fraction before and after applying a snapshot/event.
inline void OnHealthFraction(DamageGhost& ghost, float previous_fraction, float new_fraction) {
    if (!std::isfinite(previous_fraction) || !std::isfinite(new_fraction)) {
        return;
    }
    if (new_fraction + 1e-4f < previous_fraction) {
        ghost.shown_fraction = previous_fraction;
        ghost.amount = previous_fraction - new_fraction;
        ghost.since_hit = 0.0f;
        ghost.active = true;
    }
}

// Advances the ghost by one frame. Healing simply lets the ghost finish.
inline void UpdateDamageGhost(DamageGhost& ghost, float dt) {
    if (!ghost.active || dt <= 0.0f) {
        return;
    }
    ghost.since_hit += dt;
    if (ghost.since_hit <= ghost.hold_seconds) {
        return;
    }
    const float drain = ghost.catchup_per_second * dt;
    ghost.shown_fraction -= drain;
    ghost.amount -= drain;
    if (ghost.amount <= 0.0f) {
        ghost.amount = 0.0f;
        ghost.active = false;
    }
}

// Deterministic horizontal shake for the ghost bar: a fast ripple that decays,
// bounded by `amplitude`. No RNG, so replays and screenshots are reproducible.
inline float DamageShakeOffset(const DamageGhost& ghost, float amplitude) {
    if (!ghost.active) {
        return 0.0f;
    }
    const float decay = std::clamp(1.0f - ghost.since_hit / 0.25f, 0.0f, 1.0f);
    return amplitude * std::sin(ghost.since_hit * 48.0f) * decay;
}

inline constexpr int kCrosshairPoints = 6;

// Writes kCrosshairPoints (x, y) pairs of the pixel hexagon crosshair into
// `out_xy` (fixed buffer, no allocation). Returns the number of floats written,
// or 0 when the buffer is too small or null.
inline int HexagonCrosshair(float center_x, float center_y, float radius, float rotation_radians,
                            float* out_xy, int capacity_floats) {
    constexpr float kTau = 6.28318531f;
    const int needed = kCrosshairPoints * 2;
    if (out_xy == nullptr || capacity_floats < needed) {
        return 0;
    }
    for (int i = 0; i < kCrosshairPoints; ++i) {
        const float angle =
            rotation_radians + (static_cast<float>(i) / static_cast<float>(kCrosshairPoints)) * kTau;
        out_xy[i * 2] = center_x + std::cos(angle) * radius;
        out_xy[i * 2 + 1] = center_y + std::sin(angle) * radius;
    }
    return needed;
}

}  // namespace odyssey::client::ui
