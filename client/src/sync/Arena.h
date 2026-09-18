// Shared fixed 20x20 arena layout. Fractions match server/internal/game/arena.go.
#pragma once

#include <array>
#include <algorithm>
#include <utility>

namespace odyssey::client::sync {

struct CoverBlock { float min_x, min_z, max_x, max_z; };
inline constexpr std::array<CoverBlock, 4> kCoverBlocks{{
    {5.6f, 11.2f, 7.0f, 12.6f},
    {10.7f, 5.6f, 12.1f, 7.0f},
    {13.0f, 8.2f, 14.4f, 9.6f},
    {7.9f, 13.0f, 9.3f, 14.4f},
}};

inline bool InsideCover(float x, float z, float radius) {
    for (const auto& block : kCoverBlocks) {
        if (x > block.min_x-radius && x < block.max_x+radius &&
            z > block.min_z-radius && z < block.max_z+radius) return true;
    }
    return false;
}

inline std::pair<float, float> MoveAroundCover(float x, float z, float dx, float dz,
                                               float radius = 0.32f) {
    const float next_x = std::clamp(x + dx, 0.0f, 20.0f);
    if (!InsideCover(next_x, z, radius)) x = next_x;
    const float next_z = std::clamp(z + dz, 0.0f, 20.0f);
    if (!InsideCover(x, next_z, radius)) z = next_z;
    return {x, z};
}

}  // namespace odyssey::client::sync
