// Deterministic stage-specific arena geometry shared conceptually with
// server/internal/game/arena.go. Coordinates are in the fixed [0,20] world.
#pragma once

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <utility>

namespace odyssey::client::sync {

struct CoverBlock {
    float min_x = 0.0f;
    float min_z = 0.0f;
    float max_x = 0.0f;
    float max_z = 0.0f;
};

enum class ArenaTopology : std::uint32_t {
    kCrosswindGates,
    kBrokenRing,
    kTwinCorridors,
    kSpiralRelay,
    kCornerBastions,
    kStaggeredGauntlet,
    kCount,
};

struct ArenaLayout {
    ArenaTopology topology = ArenaTopology::kCrosswindGates;
    std::uint32_t rotation = 0;
    bool mirrored = false;
    std::array<CoverBlock, 10> blocks{};
    std::size_t block_count = 0;
};

namespace arena_detail {

struct NormalizedBlock { float min_x, min_z, max_x, max_z; };

inline NormalizedBlock Transform(NormalizedBlock block, std::uint32_t rotation,
                                 bool mirrored) {
    if (mirrored) {
        block = {1.0f - block.max_x, block.min_z, 1.0f - block.min_x, block.max_z};
    }
    for (std::uint32_t turn = 0; turn < rotation; ++turn) {
        block = {1.0f - block.max_z, block.min_x, 1.0f - block.min_z, block.max_x};
    }
    return block;
}

inline void Add(ArenaLayout& layout, NormalizedBlock block) {
    if (layout.block_count >= layout.blocks.size()) return;
    block = Transform(block, layout.rotation, layout.mirrored);
    layout.blocks[layout.block_count++] = CoverBlock{
        block.min_x * 20.0f, block.min_z * 20.0f,
        block.max_x * 20.0f, block.max_z * 20.0f};
}

}  // namespace arena_detail

inline ArenaLayout MakeArenaLayout(std::uint32_t stage_index, std::int64_t stage_seed) {
    using arena_detail::Add;
    using arena_detail::NormalizedBlock;
    constexpr auto topology_count = static_cast<std::uint32_t>(ArenaTopology::kCount);
    const std::uint32_t normalized_stage = stage_index == 0 ? 0 : stage_index - 1;
    const std::uint64_t variant = static_cast<std::uint64_t>(stage_seed) ^
        static_cast<std::uint64_t>(stage_index) * 0x9e3779b97f4a7c15ULL;

    ArenaLayout layout;
    layout.topology = static_cast<ArenaTopology>(normalized_stage % topology_count);
    layout.rotation = static_cast<std::uint32_t>(variant & 3ULL);
    layout.mirrored = ((variant >> 2U) & 1ULL) != 0;
    if (stage_index == 0) return layout;

    switch (layout.topology) {
        case ArenaTopology::kCrosswindGates:
            Add(layout, NormalizedBlock{0.23f, 0.15f, 0.28f, 0.42f});
            Add(layout, NormalizedBlock{0.23f, 0.58f, 0.28f, 0.85f});
            Add(layout, NormalizedBlock{0.72f, 0.15f, 0.77f, 0.44f});
            Add(layout, NormalizedBlock{0.72f, 0.60f, 0.77f, 0.85f});
            Add(layout, NormalizedBlock{0.39f, 0.27f, 0.61f, 0.32f});
            Add(layout, NormalizedBlock{0.39f, 0.68f, 0.61f, 0.73f});
            break;
        case ArenaTopology::kBrokenRing:
            Add(layout, NormalizedBlock{0.27f, 0.27f, 0.44f, 0.31f});
            Add(layout, NormalizedBlock{0.56f, 0.27f, 0.73f, 0.31f});
            Add(layout, NormalizedBlock{0.27f, 0.69f, 0.44f, 0.73f});
            Add(layout, NormalizedBlock{0.56f, 0.69f, 0.73f, 0.73f});
            Add(layout, NormalizedBlock{0.27f, 0.34f, 0.31f, 0.46f});
            Add(layout, NormalizedBlock{0.27f, 0.54f, 0.31f, 0.66f});
            Add(layout, NormalizedBlock{0.69f, 0.34f, 0.73f, 0.46f});
            Add(layout, NormalizedBlock{0.69f, 0.54f, 0.73f, 0.66f});
            break;
        case ArenaTopology::kTwinCorridors:
            Add(layout, NormalizedBlock{0.10f, 0.30f, 0.42f, 0.35f});
            Add(layout, NormalizedBlock{0.58f, 0.30f, 0.90f, 0.35f});
            Add(layout, NormalizedBlock{0.18f, 0.65f, 0.46f, 0.70f});
            Add(layout, NormalizedBlock{0.54f, 0.65f, 0.82f, 0.70f});
            Add(layout, NormalizedBlock{0.18f, 0.43f, 0.23f, 0.57f});
            Add(layout, NormalizedBlock{0.77f, 0.43f, 0.82f, 0.57f});
            break;
        case ArenaTopology::kSpiralRelay:
            Add(layout, NormalizedBlock{0.22f, 0.21f, 0.70f, 0.26f});
            Add(layout, NormalizedBlock{0.70f, 0.21f, 0.75f, 0.59f});
            Add(layout, NormalizedBlock{0.39f, 0.59f, 0.75f, 0.64f});
            Add(layout, NormalizedBlock{0.34f, 0.40f, 0.39f, 0.64f});
            Add(layout, NormalizedBlock{0.34f, 0.35f, 0.58f, 0.40f});
            Add(layout, NormalizedBlock{0.58f, 0.35f, 0.63f, 0.50f});
            break;
        case ArenaTopology::kCornerBastions:
            Add(layout, NormalizedBlock{0.14f, 0.20f, 0.35f, 0.25f});
            Add(layout, NormalizedBlock{0.14f, 0.20f, 0.19f, 0.40f});
            Add(layout, NormalizedBlock{0.65f, 0.20f, 0.86f, 0.25f});
            Add(layout, NormalizedBlock{0.81f, 0.20f, 0.86f, 0.40f});
            Add(layout, NormalizedBlock{0.14f, 0.75f, 0.35f, 0.80f});
            Add(layout, NormalizedBlock{0.14f, 0.60f, 0.19f, 0.80f});
            Add(layout, NormalizedBlock{0.65f, 0.75f, 0.86f, 0.80f});
            Add(layout, NormalizedBlock{0.81f, 0.60f, 0.86f, 0.80f});
            break;
        case ArenaTopology::kStaggeredGauntlet:
            Add(layout, NormalizedBlock{0.10f, 0.19f, 0.34f, 0.24f});
            Add(layout, NormalizedBlock{0.29f, 0.19f, 0.34f, 0.39f});
            Add(layout, NormalizedBlock{0.38f, 0.38f, 0.62f, 0.43f});
            Add(layout, NormalizedBlock{0.38f, 0.38f, 0.43f, 0.58f});
            Add(layout, NormalizedBlock{0.66f, 0.61f, 0.90f, 0.66f});
            Add(layout, NormalizedBlock{0.66f, 0.61f, 0.71f, 0.81f});
            break;
        default: break;
    }
    return layout;
}

inline const char* ArenaTopologyCode(ArenaTopology topology) {
    switch (topology) {
        case ArenaTopology::kCrosswindGates: return "CROSSWIND GATES";
        case ArenaTopology::kBrokenRing: return "BROKEN RING";
        case ArenaTopology::kTwinCorridors: return "TWIN CORRIDORS";
        case ArenaTopology::kSpiralRelay: return "SPIRAL RELAY";
        case ArenaTopology::kCornerBastions: return "CORNER BASTIONS";
        case ArenaTopology::kStaggeredGauntlet: return "STAGGERED GAUNTLET";
        default: return "UNKNOWN LAYOUT";
    }
}

inline bool InsideCover(const ArenaLayout& layout, float x, float z, float radius) {
    for (std::size_t index = 0; index < layout.block_count; ++index) {
        const auto& block = layout.blocks[index];
        if (x > block.min_x - radius && x < block.max_x + radius &&
            z > block.min_z - radius && z < block.max_z + radius) return true;
    }
    return false;
}

inline std::pair<float, float> MoveAroundCover(const ArenaLayout& layout, float x, float z,
                                               float dx, float dz, float radius = 0.32f) {
    const float next_x = std::clamp(x + dx, 0.0f, 20.0f);
    if (!InsideCover(layout, next_x, z, radius)) x = next_x;
    const float next_z = std::clamp(z + dz, 0.0f, 20.0f);
    if (!InsideCover(layout, x, next_z, radius)) z = next_z;
    return {x, z};
}

}  // namespace odyssey::client::sync
