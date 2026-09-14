// Katana Zero neo-noir palette, theme tokens and accessibility switches.
//
// Colours are kept as a plain Rgba struct instead of raylib's Color so this
// header stays raylib-free and the palette can be unit tested in the headless
// logic suite. The renderer converts with ToColor()/RAYLIB_COLOR() once per draw.
//
// Raylib-free on purpose.
#pragma once

#include <cstdint>

namespace odyssey::client::ui {

struct Rgba {
    unsigned char r = 0;
    unsigned char g = 0;
    unsigned char b = 0;
    unsigned char a = 255;
};

inline constexpr Rgba RgbaFromHex(std::uint32_t rgb, unsigned char alpha = 255) {
    return Rgba{static_cast<unsigned char>((rgb >> 16) & 0xFFu),
                static_cast<unsigned char>((rgb >> 8) & 0xFFu),
                static_cast<unsigned char>(rgb & 0xFFu), alpha};
}

inline constexpr bool operator==(const Rgba& lhs, const Rgba& rhs) {
    return lhs.r == rhs.r && lhs.g == rhs.g && lhs.b == rhs.b && lhs.a == rhs.a;
}
inline constexpr bool operator!=(const Rgba& lhs, const Rgba& rhs) { return !(lhs == rhs); }

// Neo-noir base: near-black indigo ground with saturated neon accents. Values are
// the theme contract; the glitch effects layer on top of them.
struct Theme {
    // Ground / structure.
    Rgba background = RgbaFromHex(0x0A0A10);  // deep purple-black; cleared inside the RT
    Rgba letterbox = RgbaFromHex(0x000000);   // bars around the 960x540 image
    // The play field sits one step above the clear colour: the spec pins the clear
    // to #0A0A10, but measuring the rendered frame showed 88% of the play area at
    // 2% luminance - the arena read as "black with a faint grid" rather than a
    // place. The floor fill keeps the mandated ground and still defines the field.
    Rgba arena_floor = RgbaFromHex(0x14141F);
    Rgba panel = RgbaFromHex(0x14141F, 235);
    Rgba panel_edge = RgbaFromHex(0x3C3C58);
    Rgba grid = RgbaFromHex(0x2A2A44);

    // Text.
    Rgba text = RgbaFromHex(0xE8E8F0);
    Rgba text_dim = RgbaFromHex(0x8A8AA0);
    Rgba text_warn = RgbaFromHex(0xFFD166);
    Rgba text_danger = RgbaFromHex(0xFF4D6D);

    // Neon accents (HUD, crosshair, floaters, glitch).
    Rgba neon_cyan = RgbaFromHex(0x22E0FF);
    Rgba neon_magenta = RgbaFromHex(0xFF3DBB);
    Rgba neon_yellow = RgbaFromHex(0xFFE066);
    Rgba neon_red = RgbaFromHex(0xFF2E4C);

    // Bars / feedback.
    Rgba bar_fill = RgbaFromHex(0x36F1CD);
    Rgba bar_empty = RgbaFromHex(0x22222F);
    Rgba bar_damage = RgbaFromHex(0xFFF1F1);  // white shake ghost behind the fill

    // Entities.
    Rgba player = RgbaFromHex(0x4DA6FF);
    Rgba peer = RgbaFromHex(0xFF6B6B);
    Rgba monster = RgbaFromHex(0xFFA24D);
    Rgba projectile = RgbaFromHex(0xFFD166);
    Rgba dead = RgbaFromHex(0x555566);
};

inline constexpr Theme kDefaultTheme{};

// F3 accessibility menu state; persisted to settings.ini.
struct AccessibilityConfig {
    bool disable_glitch_fx = false;
    bool disable_screen_shake = false;
    bool disable_damage_floaters = false;
};

inline constexpr AccessibilityConfig kDefaultAccessibility{};

}  // namespace odyssey::client::ui
