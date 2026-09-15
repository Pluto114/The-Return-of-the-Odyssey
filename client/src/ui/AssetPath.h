// Asset-root and settings-path resolution.
//
// The client must not depend on the process working directory: the executable is
// copied next to its assets at build time, and users may launch it from anywhere.
// Two rules follow from that and are implemented here:
//
//   * the asset root is resolved ONCE at startup (never per frame, so the render
//     loop keeps its zero-allocation budget) by trying, in order, the assets
//     directory beside the executable and then CWD-relative candidates;
//   * settings are written outside the source tree - %APPDATA% on Windows, and
//     $XDG_CONFIG_HOME or ~/.config on POSIX - falling back to the executable
//     directory. A missing or unwritable location only logs a warning; it must
//     never block or crash the client.
//
// This header holds the PURE decision logic (no filesystem, no platform headers)
// so it can be unit tested headlessly. The platform half - locating the
// executable, touching the filesystem and caching the result - lives in
// ui/AssetPath.cpp, which is compiled into the client only and deliberately NOT
// linked into the test targets.
//
// Raylib-free on purpose.
#pragma once

#include "ui/Theme.h"

#include <cstddef>
#include <functional>
#include <string>
#include <vector>

namespace odyssey::client::ui {

// Joins two path fragments with '/' unless the left side already ends in a
// separator or is empty. Kept trivial and allocation-honest for startup use.
inline std::string JoinPath(const std::string& base, const std::string& leaf) {
    if (base.empty()) {
        return leaf;
    }
    std::string out = base;
    if (out.back() != '/' && out.back() != '\\') {
        out.push_back('/');
    }
    out += leaf;
    return out;
}

// Candidate asset roots in priority order: beside the executable first (the
// shipped layout, filled by the CMake post-build copy), then CWD variants for
// running straight out of the repo or a build tree. Duplicates are dropped.
inline std::vector<std::string> AssetRootCandidates(const std::string& executable_dir,
                                                    const std::string& working_dir) {
    std::vector<std::string> candidates;
    const auto add = [&candidates](const std::string& base, const std::string& relative) {
        if (base.empty()) {
            return;
        }
        const std::string path = JoinPath(base, relative);
        for (const std::string& existing : candidates) {
            if (existing == path) {
                return;
            }
        }
        candidates.push_back(path);
    };
    add(executable_dir, "assets");
    add(working_dir, "assets");
    add(working_dir, "client/assets");
    return candidates;
}

// First candidate the predicate accepts, or an empty string when none does (the
// caller then falls back to plain relative paths and logs a warning).
inline std::string ChooseAssetRoot(const std::vector<std::string>& candidates,
                                   const std::function<bool(const std::string&)>& exists) {
    for (const std::string& candidate : candidates) {
        if (exists(candidate)) {
            return candidate;
        }
    }
    return std::string();
}

// Directory that holds settings.ini, never inside the source tree when any of the
// platform locations is available.
inline std::string ChooseSettingsDirectory(const char* appdata,
                                           const char* xdg_config_home,
                                           const char* home,
                                           const std::string& executable_dir) {
    if (appdata != nullptr && appdata[0] != '\0') {
        return JoinPath(appdata, "Odyssey");
    }
    if (xdg_config_home != nullptr && xdg_config_home[0] != '\0') {
        return JoinPath(xdg_config_home, "odyssey");
    }
    if (home != nullptr && home[0] != '\0') {
        return JoinPath(JoinPath(home, ".config"), "odyssey");
    }
    return executable_dir;
}

inline std::string SettingsFilePathIn(const std::string& directory) {
    return JoinPath(directory, "settings.ini");
}

// ---------------------------------------------------------------------------
// Platform half, implemented in ui/AssetPath.cpp (compiled into the client only,
// never linked into the test targets because it touches the filesystem).
// ---------------------------------------------------------------------------

// Resolved once at startup and cached; empty when no candidate exists, in which
// case GetAssetPath() returns the relative path unchanged.
const std::string& AssetRoot();

// Absolute path for an asset, e.g. GetAssetPath("equipment.tsv"). Call it at
// startup only: the render loop must not resolve paths per frame.
std::string GetAssetPath(const std::string& relative_path);

// settings.ini location outside the source tree (%APPDATA% / XDG / ~/.config,
// executable directory as the last resort). Cached after the first call.
const std::string& SettingsFilePath();

bool SettingsFileExists();

// Reads the accessibility switches. A missing or unreadable file - and any write
// failure reported by SaveAccessibility - yields the defaults plus a stdout warning;
// it must never block or crash the client.
AccessibilityConfig LoadAccessibility();

// Writes the accessibility switches to SettingsFilePath(). Returns false (after a
// stdout warning) when the file cannot be written; the caller keeps its in-memory
// values either way. Comments a user added by hand are not preserved.
bool SaveAccessibility(const AccessibilityConfig& config);

}  // namespace odyssey::client::ui
