// Deterministic, client-only visual layout for an expedition stage.
//
// The server remains authoritative for movement and the four cover blocks.
// This data only drives non-colliding scenery, so it can make every stage
// visually distinct without changing combat or network protocol semantics.
#pragma once

#include <array>
#include <cstdint>

namespace odyssey::client::scene {

enum class Biome : std::uint32_t {
    kAzureNebula,
    kFrozenMoon,
    kEmberRift,
    kAncientRelay,
    kVoidGarden,
    kIonStorm,
    kCount,
};

struct ScenicPoint {
    float x = 0.0f;       // normalized arena coordinate
    float z = 0.0f;
    float size = 0.0f;    // normalized relative size
    float phase = 0.0f;   // deterministic animation offset / orientation
    std::uint32_t variant = 0;
};

struct StageScene {
    Biome biome = Biome::kAzureNebula;
    std::uint64_t fingerprint = 0;
    float lane_x0 = 0.0f;
    float lane_z0 = 0.0f;
    float lane_x1 = 1.0f;
    float lane_z1 = 1.0f;
    std::uint32_t grid_columns = 10;
    std::uint32_t grid_rows = 8;
    std::array<ScenicPoint, 52> stars{};
    std::array<ScenicPoint, 9> fields{};
    std::array<ScenicPoint, 12> landmarks{};
};

namespace detail {

inline std::uint64_t Mix(std::uint64_t value) {
    value += 0x9e3779b97f4a7c15ULL;
    value = (value ^ (value >> 30U)) * 0xbf58476d1ce4e5b9ULL;
    value = (value ^ (value >> 27U)) * 0x94d049bb133111ebULL;
    return value ^ (value >> 31U);
}

class StageRandom {
public:
    explicit StageRandom(std::uint64_t state) : state_(state) {}

    std::uint64_t Next() {
        state_ = Mix(state_);
        return state_;
    }

    float Unit() {
        // Use the high 24 bits: exactly representable and portable as float.
        return static_cast<float>((Next() >> 40U) & 0xFFFFFFULL) / 16777215.0f;
    }

    float Range(float minimum, float maximum) {
        return minimum + (maximum - minimum) * Unit();
    }

    std::uint32_t Index(std::uint32_t count) {
        return count == 0 ? 0 : static_cast<std::uint32_t>(Next() % count);
    }

private:
    std::uint64_t state_;
};

}  // namespace detail

inline StageScene MakeStageScene(std::uint32_t stage_index, std::int64_t stage_seed) {
    // Index participates even if a test server reuses its seed. Stage zero is
    // the lobby scene and intentionally receives its own stable layout.
    const auto seed_bits = static_cast<std::uint64_t>(stage_seed);
    const std::uint64_t fingerprint = detail::Mix(
        seed_bits ^ (static_cast<std::uint64_t>(stage_index) + 1ULL) * 0xd1b54a32d192ed03ULL);
    detail::StageRandom random(fingerprint);

    StageScene scene;
    scene.fingerprint = fingerprint;
    const auto biome_count = static_cast<std::uint32_t>(Biome::kCount);
    const std::uint32_t seed_offset = static_cast<std::uint32_t>(detail::Mix(seed_bits) % biome_count);
    scene.biome = static_cast<Biome>((seed_offset + stage_index) % biome_count);
    scene.grid_columns = 8 + random.Index(6);
    scene.grid_rows = 6 + random.Index(5);

    // A broad navigation lane crosses the arena at one of four orientations.
    // Its placement changes by stage but leaves the arena geometry untouched.
    switch (random.Index(4)) {
        case 0:
            scene.lane_x0 = -0.05f; scene.lane_z0 = random.Range(0.18f, 0.42f);
            scene.lane_x1 = 1.05f; scene.lane_z1 = random.Range(0.58f, 0.82f);
            break;
        case 1:
            scene.lane_x0 = -0.05f; scene.lane_z0 = random.Range(0.58f, 0.82f);
            scene.lane_x1 = 1.05f; scene.lane_z1 = random.Range(0.18f, 0.42f);
            break;
        case 2:
            scene.lane_x0 = random.Range(0.18f, 0.42f); scene.lane_z0 = -0.05f;
            scene.lane_x1 = random.Range(0.58f, 0.82f); scene.lane_z1 = 1.05f;
            break;
        default:
            scene.lane_x0 = random.Range(0.58f, 0.82f); scene.lane_z0 = -0.05f;
            scene.lane_x1 = random.Range(0.18f, 0.42f); scene.lane_z1 = 1.05f;
            break;
    }

    for (auto& star : scene.stars) {
        star.x = random.Range(0.012f, 0.988f);
        star.z = random.Range(0.025f, 0.975f);
        star.size = random.Range(0.45f, 1.65f);
        star.phase = random.Range(0.0f, 6.2831853f);
        star.variant = random.Index(5);
    }
    for (auto& field : scene.fields) {
        field.x = random.Range(0.06f, 0.94f);
        field.z = random.Range(0.08f, 0.92f);
        field.size = random.Range(0.07f, 0.17f);
        field.phase = random.Range(0.0f, 6.2831853f);
        field.variant = random.Index(4);
    }
    for (auto& landmark : scene.landmarks) {
        landmark.x = random.Range(0.06f, 0.94f);
        landmark.z = random.Range(0.10f, 0.90f);
        landmark.size = random.Range(0.018f, 0.047f);
        landmark.phase = random.Range(0.0f, 360.0f);
        landmark.variant = random.Index(4);
    }
    return scene;
}

inline const char* BiomeCode(Biome biome) {
    switch (biome) {
        case Biome::kAzureNebula: return "AZURE NEBULA";
        case Biome::kFrozenMoon: return "FROZEN MOON";
        case Biome::kEmberRift: return "EMBER RIFT";
        case Biome::kAncientRelay: return "ANCIENT RELAY";
        case Biome::kVoidGarden: return "VOID GARDEN";
        case Biome::kIonStorm: return "ION STORM";
        default: return "UNKNOWN SECTOR";
    }
}

}  // namespace odyssey::client::scene
