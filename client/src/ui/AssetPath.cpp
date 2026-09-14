// Platform/filesystem half of the asset-root and settings resolution declared in
// ui/AssetPath.h. Compiled into the client executable only - it is deliberately
// NOT part of any test target, so the headless suites keep their no-filesystem,
// no-window profile.
//
// Failure policy (mirrors the UI contract): every failure is a stdout warning plus
// a safe fallback. Nothing here may abort, block or write inside the source tree.
#include "ui/AssetPath.h"

#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <string>
#include <system_error>
#include <vector>

#if defined(_WIN32)
#ifndef NOMINMAX
#define NOMINMAX
#endif
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#else
#include <limits.h>
#include <unistd.h>
#endif

namespace odyssey::client::ui {
namespace {

void Warn(const std::string& message) {
    std::printf("main: WARN asset/settings: %s\n", message.c_str());
    std::fflush(stdout);
}

// Directory the running executable lives in, or empty when it cannot be
// determined (the caller then falls back to CWD candidates).
std::string ExecutableDirectory() {
#if defined(_WIN32)
    wchar_t buffer[MAX_PATH];
    const DWORD length = GetModuleFileNameW(nullptr, buffer, MAX_PATH);
    if (length == 0 || length >= MAX_PATH) {
        return {};
    }
    const int utf8_length = WideCharToMultiByte(CP_UTF8, 0, buffer, static_cast<int>(length),
                                                nullptr, 0, nullptr, nullptr);
    if (utf8_length <= 0) {
        return {};
    }
    std::string utf8(static_cast<std::size_t>(utf8_length), '\0');
    WideCharToMultiByte(CP_UTF8, 0, buffer, static_cast<int>(length), utf8.data(), utf8_length,
                        nullptr, nullptr);
    return std::filesystem::path(utf8).parent_path().string();
#else
    char buffer[PATH_MAX];
    const ssize_t length = readlink("/proc/self/exe", buffer, sizeof(buffer) - 1);
    if (length <= 0) {
        return {};
    }
    buffer[length] = '\0';
    return std::filesystem::path(buffer).parent_path().string();
#endif
}

std::string WorkingDirectory() {
    std::error_code ec;
    const std::filesystem::path cwd = std::filesystem::current_path(ec);
    return ec ? std::string() : cwd.string();
}

bool IsDirectory(const std::string& path) {
    std::error_code ec;
    return std::filesystem::is_directory(path, ec);
}

// MSVC flags std::getenv as unsafe; the value is copied into a std::string by the
// caller and never retained.
const char* ReadEnv(const char* name) {
#if defined(_MSC_VER)
#pragma warning(push)
#pragma warning(disable : 4996)  // 'getenv': This function or variable may be unsafe
#endif
    return std::getenv(name);
#if defined(_MSC_VER)
#pragma warning(pop)
#endif
}

struct AssetResolution {
    std::string root;
    std::vector<std::string> candidates;
    bool resolved = false;
};

// Resolved once, on first use, so the render loop never touches the filesystem.
const AssetResolution& Resolution() {
    static const AssetResolution resolution = [] {
        AssetResolution out;
        out.candidates = AssetRootCandidates(ExecutableDirectory(), WorkingDirectory());
        out.root = ChooseAssetRoot(out.candidates, IsDirectory);
        out.resolved = !out.root.empty();
        return out;
    }();
    return resolution;
}

bool ParseBool(const std::string& value, bool& out) {
    if (value == "1" || value == "true" || value == "yes" || value == "on") {
        out = true;
        return true;
    }
    if (value == "0" || value == "false" || value == "no" || value == "off") {
        out = false;
        return true;
    }
    return false;
}

std::string Trim(const std::string& text) {
    const std::size_t first = text.find_first_not_of(" \t\r\n");
    if (first == std::string::npos) {
        return {};
    }
    const std::size_t last = text.find_last_not_of(" \t\r\n");
    return text.substr(first, last - first + 1);
}

}  // namespace

const std::string& AssetRoot() { return Resolution().root; }

std::string GetAssetPath(const std::string& relative_path) {
    const std::string& root = AssetRoot();
    if (root.empty()) {
        // No asset directory was found: fall back to the relative path so the
        // client still starts (fonts and tables degrade to their defaults).
        return relative_path;
    }
    return JoinPath(root, relative_path);
}

const std::string& SettingsFilePath() {
    static const std::string path = [] {
        const std::string directory = ChooseSettingsDirectory(
            ReadEnv("APPDATA"), ReadEnv("XDG_CONFIG_HOME"), ReadEnv("HOME"), ExecutableDirectory());
        if (directory.empty()) {
            Warn("no usable settings directory; accessibility settings stay in memory");
            return std::string();
        }
        // Create it eagerly with error reporting: a read-only location is a warning,
        // not a failure, and the client keeps its in-memory defaults.
        std::error_code ec;
        std::filesystem::create_directories(directory, ec);
        if (ec) {
            Warn("cannot create settings directory '" + directory + "': " + ec.message() +
                 " (settings stay in memory)");
            return std::string();
        }
        return SettingsFilePathIn(directory);
    }();
    return path;
}

bool SettingsFileExists() {
    const std::string& path = SettingsFilePath();
    if (path.empty()) {
        return false;
    }
    std::error_code ec;
    return std::filesystem::is_regular_file(path, ec);
}

AccessibilityConfig LoadAccessibility() {
    AccessibilityConfig config = kDefaultAccessibility;
    const std::string& path = SettingsFilePath();
    if (path.empty() || !SettingsFileExists()) {
        // First run (or no writable location): defaults are the documented state.
        return config;
    }
    std::FILE* file = nullptr;
#if defined(_MSC_VER)
#pragma warning(push)
#pragma warning(disable : 4996)  // std::fopen: This function or variable may be unsafe
#endif
    file = std::fopen(path.c_str(), "rb");
#if defined(_MSC_VER)
#pragma warning(pop)
#endif
    if (file == nullptr) {
        Warn("cannot read '" + path + "'; using default accessibility settings");
        return config;
    }
    std::string contents;
    char buffer[256];
    std::size_t read = 0;
    while ((read = std::fread(buffer, 1, sizeof(buffer), file)) > 0) {
        contents.append(buffer, read);
    }
    std::fclose(file);

    std::size_t line_start = 0;
    while (line_start <= contents.size()) {
        const std::size_t line_end = contents.find('\n', line_start);
        const std::string line =
            Trim(contents.substr(line_start, (line_end == std::string::npos ? contents.size()
                                                                             : line_end) -
                                                  line_start));
        line_start = (line_end == std::string::npos) ? contents.size() + 1 : line_end + 1;
        if (line.empty() || line[0] == '#' || line[0] == ';' || line[0] == '[') {
            continue;
        }
        const std::size_t equals = line.find('=');
        if (equals == std::string::npos) {
            continue;
        }
        const std::string key = Trim(line.substr(0, equals));
        const std::string value = Trim(line.substr(equals + 1));
        bool enabled = false;
        if (!ParseBool(value, enabled)) {
            Warn("ignoring non-boolean setting '" + key + "=" + value + "'");
            continue;
        }
        if (key == "disable_glitch_fx") {
            config.disable_glitch_fx = enabled;
        } else if (key == "disable_screen_shake") {
            config.disable_screen_shake = enabled;
        } else if (key == "disable_damage_floaters") {
            config.disable_damage_floaters = enabled;
        }
    }
    return config;
}

}  // namespace odyssey::client::ui
