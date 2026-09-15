// Gameserver endpoint configuration for the Odyssey client.
//
// Cross-machine playtests need a real entry point instead of a hardcoded
// address, and a bad address must fail loudly instead of silently connecting
// elsewhere. Precedence is command line > environment > built-in default.
//
//   --host <addr>        | --host=<addr>        server host name or address
//   --port <n>           | --port=<n>           TCP port, 1..65535
//   --server <h[:port]>  | --server=<h[:port]>  shorthand for both
//   --help | -h                                 print usage and exit
//
//   ODYSSEY_SERVER_HOST, ODYSSEY_SERVER_PORT
//
// This header is deliberately free of raylib/asio/protobuf so the parsing rules
// stay headlessly unit-testable.
#pragma once

#include <cstdint>
#include <cstdlib>
#include <string>
#include <string_view>

namespace odyssey::client::core {

inline constexpr const char* kDefaultServerHost = "127.0.0.1";
inline constexpr std::uint16_t kDefaultServerPort = 7777;

inline constexpr const char* kServerHostEnvVar = "ODYSSEY_SERVER_HOST";
inline constexpr const char* kServerPortEnvVar = "ODYSSEY_SERVER_PORT";
// Deterministic UI skip used by the performance acceptance run (plan P3): the world
// and the whole network/game path keep running, only the UI layer is dropped.
inline constexpr const char* kUiOffEnvVar = "ODYSSEY_UI_OFF";

// The host name limit is the DNS maximum; hosts longer than this cannot
// resolve, so they are rejected at parse time rather than at connect time.
inline constexpr std::size_t kMaxHostLength = 253;

struct ClientEndpoint {
    std::string host = kDefaultServerHost;
    std::uint16_t port = kDefaultServerPort;
};

// Where the effective endpoint came from (printed at startup so a playtest can
// confirm that its arguments were actually consumed).
enum class EndpointSource {
    kDefault,
    kEnvironment,
    kCommandLine,
};

inline const char* ToString(EndpointSource source) {
    switch (source) {
        case EndpointSource::kDefault: return "default";
        case EndpointSource::kEnvironment: return "env";
        case EndpointSource::kCommandLine: return "cli";
    }
    return "?";
}

// Environment values, injected so tests do not depend on the real environment.
struct EndpointEnv {
    const char* host = nullptr;
    const char* port = nullptr;
    const char* ui_off = nullptr;  // ODYSSEY_UI_OFF: 1/true/yes/on disables the UI
};

struct ClientOptions {
    ClientEndpoint endpoint;
    EndpointSource source = EndpointSource::kDefault;
    bool help_requested = false;
    // False when --no-ui / ODYSSEY_UI_OFF=1 was given: the render loop still draws the
    // world but skips the HUD, the panels and (once it exists) the ImGui layer, which
    // is what makes the UI-on/UI-off frame-time comparison reproducible.
    bool ui_enabled = true;
};

struct ConfigParseResult {
    bool ok = true;
    ClientOptions options;
    // Empty when ok; otherwise a single-line, user-facing reason.
    std::string error;
};

inline const char* ClientUsageText() {
    return
        "The Return of the Odyssey - client\n"
        "\n"
        "Usage: odyssey_client [options]\n"
        "\n"
        "Options:\n"
        "  --host <addr>        gameserver host (default 127.0.0.1)\n"
        "  --port <n>           gameserver TCP port, 1..65535 (default 7777)\n"
        "  --server <h[:port]>  set --host and --port in one argument\n"
        "  --no-ui              skip the HUD/panels (performance measurement mode)\n"
        "  --help, -h           print this message and exit\n"
        "\n"
        "Environment (overridden by the options above):\n"
        "  ODYSSEY_SERVER_HOST, ODYSSEY_SERVER_PORT, ODYSSEY_UI_OFF\n"
        "\n"
        "Examples:\n"
        "  odyssey_client                             connect to 127.0.0.1:7777\n"
        "  odyssey_client --server 192.168.1.20:7777  connect to a LAN host\n"
        "  odyssey_client --host localhost --port 9000\n"
        "  odyssey_client --no-ui                     same scene, UI layer skipped\n";
}

namespace detail {

// Hosts allow DNS labels, IPv4 literals and IPv6 literals, so the accepted set
// is letters/digits plus '.', '-', '_' and ':'.
inline bool IsValidHost(std::string_view host) {
    if (host.empty() || host.size() > kMaxHostLength) {
        return false;
    }
    for (const char c : host) {
        const bool alnum = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
                           (c >= '0' && c <= '9');
        if (!alnum && c != '.' && c != '-' && c != '_' && c != ':') {
            return false;
        }
    }
    return true;
}

// Accepts a bare host or a bracketed IPv6 literal (`[::1]`) and strips the
// brackets, because an IPv6 host needs them wherever a port is appended.
inline bool NormalizeHost(std::string_view raw, std::string& out) {
    std::string_view host = raw;
    if (host.find('[') != std::string_view::npos || host.find(']') != std::string_view::npos) {
        if (host.size() < 3 || host.front() != '[' || host.back() != ']') {
            return false;
        }
        host = host.substr(1, host.size() - 2);
    }
    if (!IsValidHost(host)) {
        return false;
    }
    out.assign(host);
    return true;
}

// Strict decimal parse: no sign, no whitespace, no leading '+', value 1..65535.
inline bool ParsePort(std::string_view text, std::uint16_t& out) {
    if (text.empty() || text.size() > 5) {
        return false;
    }
    std::uint32_t value = 0;
    for (const char c : text) {
        if (c < '0' || c > '9') {
            return false;
        }
        value = value * 10u + static_cast<std::uint32_t>(c - '0');
    }
    if (value == 0 || value > 65535u) {
        return false;
    }
    out = static_cast<std::uint16_t>(value);
    return true;
}

inline bool IsAllDigits(std::string_view text) {
    if (text.empty()) {
        return false;
    }
    for (const char c : text) {
        if (c < '0' || c > '9') {
            return false;
        }
    }
    return true;
}

// On/off values accepted for boolean environment switches (ODYSSEY_UI_OFF).
inline bool ParseSwitch(std::string_view text, bool& out) {
    if (text == "1" || text == "true" || text == "yes" || text == "on") {
        out = true;
        return true;
    }
    if (text == "0" || text == "false" || text == "no" || text == "off") {
        out = false;
        return true;
    }
    return false;
}

inline std::string UnknownOption(const std::string& option) {
    return "unknown option '" + option + "' (try --help)";
}

inline std::string MissingValue(const std::string& option) {
    return "option '" + option + "' requires a value";
}

inline std::string DuplicateHost() {
    return "gameserver host given more than once (--host / --server)";
}

inline std::string DuplicatePort() {
    return "gameserver port given more than once (--port / --server)";
}

inline std::string BadHost(const std::string& value) {
    return "invalid host '" + value +
           "' (expected a DNS name or IP address, at most 253 characters)";
}

inline std::string BadPort(const std::string& value) {
    return "invalid port '" + value + "' (expected a decimal number in 1..65535)";
}

// Splits a `--server` value into host and optional port. A single colon splits
// (so `host:9000` and a trailing `host:` are both explicit), while two or more
// colons are read as a bare IPv6 literal. Bracketed literals may carry a port:
// `[::1]:9000`.
inline bool SplitHostPort(std::string_view value,
                          std::string& host,
                          bool& has_port,
                          std::uint16_t& port,
                          std::string& error) {
    has_port = false;
    std::string_view host_part = value;
    std::string_view port_part;

    if (!value.empty() && value.front() == '[') {
        const std::size_t close = value.find(']');
        if (close == std::string_view::npos) {
            error = BadHost(std::string(value));
            return false;
        }
        host_part = value.substr(0, close + 1);
        const std::string_view rest = value.substr(close + 1);
        if (!rest.empty()) {
            if (rest.front() != ':' || rest.size() == 1) {
                error = BadPort(std::string(rest));
                return false;
            }
            port_part = rest.substr(1);
        }
    } else {
        std::size_t first = value.find(':');
        std::size_t last = value.rfind(':');
        if (first != std::string_view::npos && first == last) {
            host_part = value.substr(0, first);
            port_part = value.substr(first + 1);
        } else if (first != std::string_view::npos && last + 1 == value.size()) {
            // `host::` is not an IPv6 literal and not a host:port pair.
            error = BadPort(std::string(port_part));
            return false;
        }
    }

    if (!port_part.empty() || (host_part.size() != value.size())) {
        // A `:port` suffix was present; an empty one is an explicit error.
        if (!IsAllDigits(port_part)) {
            error = BadPort(std::string(port_part));
            return false;
        }
        if (!ParsePort(port_part, port)) {
            error = BadPort(std::string(port_part));
            return false;
        }
        has_port = true;
    }

    if (!NormalizeHost(host_part, host)) {
        error = BadHost(std::string(host_part));
        return false;
    }
    return true;
}

}  // namespace detail

// Parses command line plus explicit environment values. `argv[0]` is ignored.
// On failure the caller should print `error` and the usage text, then exit
// non-zero; no partial or fallback endpoint is reported as success.
inline ConfigParseResult ParseClientOptions(int argc,
                                            const char* const* argv,
                                            const EndpointEnv& env) {
    ConfigParseResult result;

    ClientEndpoint cli_endpoint;
    bool cli_host_seen = false;
    bool cli_port_seen = false;

    for (int i = 1; i < argc; ++i) {
        const std::string arg = (argv[i] != nullptr) ? argv[i] : "";
        std::string name = arg;
        std::string value;
        bool has_inline_value = false;
        const std::size_t eq = arg.find('=');
        if (eq != std::string::npos) {
            name = arg.substr(0, eq);
            value = arg.substr(eq + 1);
            has_inline_value = true;
        }

        if (name == "--help" || name == "-h") {
            if (has_inline_value) {
                result.ok = false;
                result.error = detail::UnknownOption(arg);
                return result;
            }
            result.options.help_requested = true;
            continue;
        }

        if (name == "--no-ui") {
            // Pure flag: --no-ui=1 is a mistake, not a value to guess at.
            if (has_inline_value) {
                result.ok = false;
                result.error = detail::UnknownOption(arg);
                return result;
            }
            result.options.ui_enabled = false;
            continue;
        }

        if (name != "--host" && name != "--port" && name != "--server") {
            result.ok = false;
            result.error = detail::UnknownOption(arg);
            return result;
        }

        if (has_inline_value) {
            if (value.empty()) {
                result.ok = false;
                result.error = detail::MissingValue(name);
                return result;
            }
        } else {
            if (i + 1 >= argc) {
                result.ok = false;
                result.error = detail::MissingValue(name);
                return result;
            }
            value = (argv[++i] != nullptr) ? argv[i] : "";
            if (value.empty()) {
                result.ok = false;
                result.error = detail::MissingValue(name);
                return result;
            }
        }

        const bool sets_host = (name == "--host" || name == "--server");
        const bool sets_port = (name == "--port" || name == "--server");
        // Tracks repeat use so a mistyped script fails instead of silently
        // keeping whichever value happened to come last.
        if (sets_host && cli_host_seen) {
            result.ok = false;
            result.error = detail::DuplicateHost();
            return result;
        }
        if (sets_port && cli_port_seen) {
            result.ok = false;
            result.error = detail::DuplicatePort();
            return result;
        }

        if (name == "--port") {
            std::uint16_t port = 0;
            if (!detail::ParsePort(value, port)) {
                result.ok = false;
                result.error = detail::BadPort(value);
                return result;
            }
            cli_endpoint.port = port;
            cli_port_seen = true;
        } else if (name == "--host") {
            std::string host;
            if (!detail::NormalizeHost(value, host)) {
                result.ok = false;
                result.error = detail::BadHost(value);
                return result;
            }
            cli_endpoint.host = host;
            cli_host_seen = true;
        } else {
            std::string host;
            bool has_port = false;
            std::uint16_t port = 0;
            std::string error;
            if (!detail::SplitHostPort(value, host, has_port, port, error)) {
                result.ok = false;
                result.error = error;
                return result;
            }
            cli_endpoint.host = host;
            cli_host_seen = true;
            if (has_port) {
                cli_endpoint.port = port;
                cli_port_seen = true;
            }
        }
    }

    bool from_env = false;
    if (!cli_host_seen && env.host != nullptr && env.host[0] != '\0') {
        std::string host;
        if (!detail::NormalizeHost(env.host, host)) {
            result.ok = false;
            result.error = std::string("invalid ") + kServerHostEnvVar + ": " +
                           detail::BadHost(env.host);
            return result;
        }
        result.options.endpoint.host = host;
        from_env = true;
    }
    if (!cli_port_seen && env.port != nullptr && env.port[0] != '\0') {
        std::uint16_t port = 0;
        if (!detail::ParsePort(env.port, port)) {
            result.ok = false;
            result.error = std::string("invalid ") + kServerPortEnvVar + ": " +
                           detail::BadPort(env.port);
            return result;
        }
        result.options.endpoint.port = port;
        from_env = true;
    }

    if (cli_host_seen || cli_port_seen) {
        if (cli_host_seen) {
            result.options.endpoint.host = cli_endpoint.host;
        }
        if (cli_port_seen) {
            result.options.endpoint.port = cli_endpoint.port;
        }
        result.options.source = EndpointSource::kCommandLine;
    } else if (from_env) {
        result.options.source = EndpointSource::kEnvironment;
    } else {
        result.options.source = EndpointSource::kDefault;
    }

    // UI switch: the environment can only add to what the command line asked for
    // (--no-ui always wins), and a malformed value is reported instead of ignored.
    if (env.ui_off != nullptr && env.ui_off[0] != '\0') {
        bool ui_off = false;
        if (!detail::ParseSwitch(env.ui_off, ui_off)) {
            result.ok = false;
            result.error = std::string("invalid ") + kUiOffEnvVar +
                           ": expected 1/0 (true/false, yes/no, on/off), got '" +
                           std::string(env.ui_off) + "'";
            return result;
        }
        if (ui_off) {
            result.options.ui_enabled = false;
        }
    }

    return result;
}

// MSVC flags std::getenv as unsafe; the value is copied into a std::string by
// the caller and never retained, so this wrapper only silences that warning at
// the single call site instead of disabling it project-wide.
inline const char* ReadEnv(const char* name) {
#if defined(_MSC_VER)
#pragma warning(push)
#pragma warning(disable : 4996)  // 'getenv': This function or variable may be unsafe
#endif
    return std::getenv(name);
#if defined(_MSC_VER)
#pragma warning(pop)
#endif
}

// Command line plus the process environment.
inline ConfigParseResult ParseClientOptions(int argc, const char* const* argv) {
    const EndpointEnv env{ReadEnv(kServerHostEnvVar), ReadEnv(kServerPortEnvVar),
                          ReadEnv(kUiOffEnvVar)};
    return ParseClientOptions(argc, argv, env);
}

}  // namespace odyssey::client::core
