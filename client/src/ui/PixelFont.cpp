// Implementation of the pixel-font loader and text helpers (see ui/PixelFont.h).
// Client-only: raylib and the asset root, no test target links this.
#include "ui/PixelFont.h"

#include "ui/AssetPath.h"
#include "ui/Metrics.h"

#include <cstdio>
#include <vector>

namespace odyssey::client::ui {
namespace {

// Diagnostics counter for the HUD text layer (see ui/Metrics.h::CommandCounter). Only the
// Main Thread draws, so no synchronisation is needed.
CommandCounter& TextCounter() {
    static CommandCounter counter;
    return counter;
}

// The HUD is English + digits + hex by contract, so only printable ASCII needs a
// glyph. Loading a tight range keeps the atlas small.
constexpr int kFirstCodepoint = 32;
constexpr int kLastCodepoint = 126;

struct HudFontState {
    HudFont hud;
    bool released = false;
};

HudFontState& State() {
    static HudFontState state = [] {
        HudFontState out;
        const std::string path = GetAssetPath(kHudFontRelativePath);
        std::vector<int> codepoints;  // startup-only allocation: never the render loop
        codepoints.reserve(static_cast<std::size_t>(kLastCodepoint - kFirstCodepoint + 1));
        for (int code = kFirstCodepoint; code <= kLastCodepoint; ++code) {
            codepoints.push_back(code);
        }
        const Font loaded = LoadFontEx(path.c_str(), kHudFontBaseSize, codepoints.data(),
                                       static_cast<int>(codepoints.size()));
        if (IsFontValid(loaded) && loaded.texture.id != 0) {
            out.hud.font = loaded;
            out.hud.using_default = false;
            out.hud.source = path;
            std::printf("main: HUD font '%s' base=%d glyphs=%d\n", path.c_str(),
                        kHudFontBaseSize, static_cast<int>(codepoints.size()));
        } else {
            out.hud.font = GetFontDefault();
            out.hud.using_default = true;
            out.hud.source = "<raylib default>";
            std::printf("main: WARN HUD font '%s' unavailable; using the raylib default font\n",
                        path.c_str());
        }
        // Nearest-neighbour keeps the bitmap edges crisp at integer scales.
        SetTextureFilter(out.hud.font.texture, TEXTURE_FILTER_POINT);
        std::fflush(stdout);
        return out;
    }();
    return state;
}

}  // namespace

const HudFont& GetHudFont() { return State().hud; }

void ReleaseHudFont() {
    HudFontState& state = State();
    if (state.released || state.hud.using_default) {
        return;
    }
    UnloadFont(state.hud.font);
    state.hud.font = GetFontDefault();
    state.hud.using_default = true;
    state.hud.source = "<raylib default>";
    state.released = true;
}

char* SanitizeAscii(const char* input, char* output, std::size_t output_size) {
    if (output == nullptr || output_size == 0) {
        return output;
    }
    if (input == nullptr) {
        output[0] = '\0';
        return output;
    }
    std::size_t written = 0;
    for (const char* cursor = input; *cursor != '\0' && written + 1 < output_size; ++cursor) {
        const unsigned char byte = static_cast<unsigned char>(*cursor);
        if (byte >= 0x20 && byte < 0x7F) {
            output[written++] = static_cast<char>(byte);
        } else if (byte == '\t') {
            output[written++] = ' ';
        } else {
            // Includes UTF-8 continuation bytes: one '?' per byte is deliberate,
            // the fallback is an ID placeholder rather than mojibake.
            output[written++] = '?';
        }
    }
    output[written] = '\0';
    return output;
}

char* FormatRawIdFallback(char* output, std::size_t output_size, std::uint64_t id) {
    if (output == nullptr || output_size == 0) {
        return output;
    }
    std::snprintf(output, output_size, "[RAW_ID_%llu]", static_cast<unsigned long long>(id));
    return output;
}

Vector2 MeasureHudText(const char* text, float size) {
    const HudFont& hud = GetHudFont();
    const float spacing = size / static_cast<float>(kHudFontBaseSize);
    return MeasureTextEx(hud.font, text, size, spacing);
}

void DrawHudText(const char* text, float x, float y, float size, Color color) {
    const HudFont& hud = GetHudFont();
    const float spacing = size / static_cast<float>(kHudFontBaseSize);
    // Integer positions: a half-pixel offset would soften the bitmap glyphs.
    DrawTextEx(hud.font, text, Vector2{static_cast<float>(static_cast<int>(x)),
                                       static_cast<float>(static_cast<int>(y))},
               size, spacing, color);
    TextCounter().Add();
}

std::uint64_t TakeHudTextCommands() { return TextCounter().Take(); }

std::uint64_t HudTextCommandTotal() { return TextCounter().Total(); }

}  // namespace odyssey::client::ui
