#pragma once

#include <cstddef>
#include <string>

namespace odyssey::client::ui {

inline bool IsUtf8Continuation(unsigned char value) {
    return (value & 0xC0U) == 0x80U;
}

// Keeps player-facing labels inside compact HUD panels without cutting a
// multi-byte UTF-8 character in half. The limit is measured in displayed
// codepoints rather than storage bytes.
inline std::string ShortText(const std::string& value, std::size_t limit) {
    std::size_t codepoints = 0;
    for (const unsigned char byte : value) {
        if (!IsUtf8Continuation(byte)) ++codepoints;
    }
    if (codepoints <= limit) return value;
    if (limit <= 3) return std::string(limit, '.');

    const std::size_t keep = limit - 3;
    std::size_t kept = 0;
    std::size_t cut = 0;
    while (cut < value.size() && kept < keep) {
        ++cut;
        while (cut < value.size() &&
               IsUtf8Continuation(static_cast<unsigned char>(value[cut]))) {
            ++cut;
        }
        ++kept;
    }
    return value.substr(0, cut) + "...";
}

}  // namespace odyssey::client::ui
