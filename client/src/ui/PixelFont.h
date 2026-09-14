// Pixel bitmap font for the in-RT HUD, plus the ASCII-only text helpers the HUD
// contract requires.
//
// The font file is provided by the team (see client/README.md) and loaded ONCE at
// startup: the atlas upload must never happen in the render loop. When the file is
// missing or invalid the client falls back to raylib's default font with a stdout
// warning - a playtest must never fail because of a missing asset.
//
// This header is raylib-dependent, so only ui/PixelFont.cpp (compiled into the
// client) and main.cpp include it; it is never part of a test target.
#pragma once

#include "raylib.h"

#include <cstddef>
#include <cstdint>
#include <string>

namespace odyssey::client::ui {

// Expected asset path (relative to the asset root) and the base size the atlas is
// rasterised at; the HUD scales from there with DrawTextEx.
inline constexpr const char* kHudFontRelativePath = "fonts/pixel_hud.ttf";
inline constexpr int kHudFontBaseSize = 32;

struct HudFont {
    Font font{};
    bool using_default = true;
    std::string source;  // resolved path, or "<raylib default>"
};

// Loaded on first use and cached. Safe to call every frame.
const HudFont& GetHudFont();

// Releases a loaded HUD font before the window closes (a no-op for the default
// font, which belongs to raylib).
void ReleaseHudFont();

// Copies `input` into `output`, replacing every non-ASCII byte with '?' so a
// server string can never index glyphs the atlas does not contain. Always
// NUL-terminates, truncates instead of overflowing, and returns `output` so it can
// be used directly inside a formatting call.
char* SanitizeAscii(const char* input, char* output, std::size_t output_size);

// Placeholder for a server string that cannot be shown at all:
// "[RAW_ID_<id>]". Used when only an identifier is trustworthy. Returns `output`.
char* FormatRawIdFallback(char* output, std::size_t output_size, std::uint64_t id);

Vector2 MeasureHudText(const char* text, float size);

// DrawTextEx against the loaded HUD font with pixel-snapped integer positions.
void DrawHudText(const char* text, float x, float y, float size, Color color);

}  // namespace odyssey::client::ui
