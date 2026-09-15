// Headless tests for the endpoint configuration slice (A5 item C-g):
// command line parsing, environment fallback and validation rules.
// No window, no sockets, no dependency on the real process environment.
#include "core/ClientConfig.h"

#include <cstdint>
#include <cstdio>
#include <initializer_list>
#include <string>
#include <vector>

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                              \
    do {                                                                         \
        ++g_checks;                                                              \
        if (!(cond)) {                                                           \
            ++g_failures;                                                        \
            std::printf("FAIL %s:%d  %s\n", __FILE__, __LINE__, #cond);          \
        }                                                                        \
    } while (0)

using odyssey::client::core::ClientOptions;
using odyssey::client::core::ClientUsageText;
using odyssey::client::core::ConfigParseResult;
using odyssey::client::core::EndpointEnv;
using odyssey::client::core::EndpointSource;
using odyssey::client::core::kDefaultServerHost;
using odyssey::client::core::kDefaultServerPort;
using odyssey::client::core::kServerHostEnvVar;
using odyssey::client::core::kServerPortEnvVar;
using odyssey::client::core::kUiOffEnvVar;
using odyssey::client::core::ParseClientOptions;
using odyssey::client::core::ToString;

ConfigParseResult Parse(std::initializer_list<const char*> args, const EndpointEnv& env = {}) {
    std::vector<const char*> argv{"odyssey_client"};
    for (const char* arg : args) {
        argv.push_back(arg);
    }
    return ParseClientOptions(static_cast<int>(argv.size()), argv.data(), env);
}

// Asserts success and returns the parsed options for further field checks.
ClientOptions Ok(const ConfigParseResult& result, int line) {
    ++g_checks;
    if (!result.ok) {
        ++g_failures;
        std::printf("FAIL %s:%d  expected parse ok, got error: %s\n", __FILE__, line,
                    result.error.c_str());
        return ClientOptions{};
    }
    return result.options;
}

#define OK(result) Ok((result), __LINE__)

void ExpectError(const ConfigParseResult& result, int line) {
    ++g_checks;
    if (result.ok) {
        ++g_failures;
        std::printf("FAIL %s:%d  expected a parse error, got success\n", __FILE__, line);
        return;
    }
    if (result.error.empty()) {
        ++g_failures;
        std::printf("FAIL %s:%d  error text must not be empty\n", __FILE__, line);
    }
}

#define EXPECT_ERROR(result) ExpectError((result), __LINE__)

void TestDefaults() {
    const auto options = OK(Parse({}));
    CHECK(options.endpoint.host == kDefaultServerHost);
    CHECK(options.endpoint.port == kDefaultServerPort);
    CHECK(options.source == EndpointSource::kDefault);
    CHECK(!options.help_requested);
    CHECK(std::string(ToString(EndpointSource::kDefault)) == "default");
    CHECK(std::string(ToString(EndpointSource::kEnvironment)) == "env");
    CHECK(std::string(ToString(EndpointSource::kCommandLine)) == "cli");
}

void TestHostForms() {
    const auto spaced = OK(Parse({"--host", "example.com"}));
    CHECK(spaced.endpoint.host == "example.com");
    CHECK(spaced.endpoint.port == kDefaultServerPort);
    CHECK(spaced.source == EndpointSource::kCommandLine);

    const auto inline_form = OK(Parse({"--host=game.internal"}));
    CHECK(inline_form.endpoint.host == "game.internal");
    CHECK(inline_form.source == EndpointSource::kCommandLine);

    // Brackets are only notation for IPv6 literals and are stripped.
    const auto ipv6 = OK(Parse({"--host", "[::1]"}));
    CHECK(ipv6.endpoint.host == "::1");

    const auto bare_ipv6 = OK(Parse({"--host", "2001:db8::1"}));
    CHECK(bare_ipv6.endpoint.host == "2001:db8::1");

    const std::string max_host(253, 'a');
    const auto longest = OK(Parse({"--host", max_host.c_str()}));
    CHECK(longest.endpoint.host == max_host);
}

void TestPortForms() {
    const auto spaced = OK(Parse({"--port", "1"}));
    CHECK(spaced.endpoint.port == 1);
    CHECK(spaced.endpoint.host == kDefaultServerHost);

    const auto inline_form = OK(Parse({"--port=65535"}));
    CHECK(inline_form.endpoint.port == 65535);

    const auto both = OK(Parse({"--host", "10.0.0.7", "--port", "9000"}));
    CHECK(both.endpoint.host == "10.0.0.7");
    CHECK(both.endpoint.port == 9000);
}

void TestServerShorthand() {
    const auto with_port = OK(Parse({"--server", "10.0.0.5:9000"}));
    CHECK(with_port.endpoint.host == "10.0.0.5");
    CHECK(with_port.endpoint.port == 9000);

    const auto inline_form = OK(Parse({"--server=10.0.0.5:9000"}));
    CHECK(inline_form.endpoint.host == "10.0.0.5");
    CHECK(inline_form.endpoint.port == 9000);

    // Without a port the host is applied and the default port is kept.
    const auto host_only = OK(Parse({"--server", "10.0.0.5"}));
    CHECK(host_only.endpoint.host == "10.0.0.5");
    CHECK(host_only.endpoint.port == kDefaultServerPort);

    // A bracketed literal may carry a port; a bare one must not be split.
    const auto bracketed = OK(Parse({"--server", "[::1]:9000"}));
    CHECK(bracketed.endpoint.host == "::1");
    CHECK(bracketed.endpoint.port == 9000);

    const auto bare_ipv6 = OK(Parse({"--server", "::1"}));
    CHECK(bare_ipv6.endpoint.host == "::1");
    CHECK(bare_ipv6.endpoint.port == kDefaultServerPort);

    const auto long_ipv6 = OK(Parse({"--server", "2001:db8::1"}));
    CHECK(long_ipv6.endpoint.host == "2001:db8::1");
    CHECK(long_ipv6.endpoint.port == kDefaultServerPort);
}

void TestBadPorts() {
    for (const char* value : {"0", "65536", "99999", "abc", "-1", "+80", " 80", "80 ",
                              "8a", "1.5", "0x50"}) {
        EXPECT_ERROR(Parse({"--port", value}));
    }
    EXPECT_ERROR(Parse({"--port="}));
    EXPECT_ERROR(Parse({"--port"}));
    EXPECT_ERROR(Parse({"--server", "10.0.0.5:"}));
    EXPECT_ERROR(Parse({"--server", "10.0.0.5:70000"}));
    EXPECT_ERROR(Parse({"--server", "[::1]:"}));
}

void TestBadHosts() {
    EXPECT_ERROR(Parse({"--host", ""}));
    EXPECT_ERROR(Parse({"--host="}));
    EXPECT_ERROR(Parse({"--host"}));
    EXPECT_ERROR(Parse({"--host", "bad host"}));
    EXPECT_ERROR(Parse({"--host", "host/path"}));
    EXPECT_ERROR(Parse({"--host", "host!"}));
    EXPECT_ERROR(Parse({"--host", "host:7777:extra?"}));
    EXPECT_ERROR(Parse({"--host", "["}));
    EXPECT_ERROR(Parse({"--host", "[::1"}));
    EXPECT_ERROR(Parse({"--host", "[]"}));
    EXPECT_ERROR(Parse({"--server", "["}));
    EXPECT_ERROR(Parse({"--server", "[]:9000"}));

    const std::string too_long(254, 'a');
    EXPECT_ERROR(Parse({"--host", too_long.c_str()}));
}

void TestUnknownAndDuplicateOptions() {
    EXPECT_ERROR(Parse({"--nope"}));
    EXPECT_ERROR(Parse({"-x"}));
    EXPECT_ERROR(Parse({"127.0.0.1"}));
    EXPECT_ERROR(Parse({"--help=1"}));

    EXPECT_ERROR(Parse({"--host", "a.example", "--host", "b.example"}));
    EXPECT_ERROR(Parse({"--port", "1", "--port", "2"}));
    EXPECT_ERROR(Parse({"--host", "a.example", "--server", "b.example:1"}));
    EXPECT_ERROR(Parse({"--server", "a.example:1", "--port", "2"}));
    EXPECT_ERROR(Parse({"--server", "a.example", "--server", "b.example"}));
}

void TestEnvironmentFallback() {
    const auto host_only = OK(Parse({}, EndpointEnv{"192.168.1.20", nullptr}));
    CHECK(host_only.endpoint.host == "192.168.1.20");
    CHECK(host_only.endpoint.port == kDefaultServerPort);
    CHECK(host_only.source == EndpointSource::kEnvironment);

    const auto port_only = OK(Parse({}, EndpointEnv{nullptr, "9100"}));
    CHECK(port_only.endpoint.host == kDefaultServerHost);
    CHECK(port_only.endpoint.port == 9100);
    CHECK(port_only.source == EndpointSource::kEnvironment);

    const auto both = OK(Parse({}, EndpointEnv{"10.1.1.1", "9100"}));
    CHECK(both.endpoint.host == "10.1.1.1");
    CHECK(both.endpoint.port == 9100);

    // Empty environment values are "unset", not "invalid".
    const auto empty = OK(Parse({}, EndpointEnv{"", ""}));
    CHECK(empty.endpoint.host == kDefaultServerHost);
    CHECK(empty.endpoint.port == kDefaultServerPort);
    CHECK(empty.source == EndpointSource::kDefault);
}

void TestCommandLineOverridesEnvironment() {
    const auto host_override = OK(Parse({"--host", "cli.example"}, EndpointEnv{"env.example", "9100"}));
    CHECK(host_override.endpoint.host == "cli.example");
    CHECK(host_override.endpoint.port == 9100);   // untouched env part still applies
    CHECK(host_override.source == EndpointSource::kCommandLine);

    const auto port_override = OK(Parse({"--port", "1234"}, EndpointEnv{"env.example", "9100"}));
    CHECK(port_override.endpoint.host == "env.example");
    CHECK(port_override.endpoint.port == 1234);
    CHECK(port_override.source == EndpointSource::kCommandLine);

    const auto server_override = OK(Parse({"--server", "cli.example:4321"}, EndpointEnv{"env.example", "9100"}));
    CHECK(server_override.endpoint.host == "cli.example");
    CHECK(server_override.endpoint.port == 4321);
    CHECK(server_override.source == EndpointSource::kCommandLine);
}

void TestInvalidEnvironmentIsRejected() {
    const auto bad_host = Parse({}, EndpointEnv{"bad host", nullptr});
    EXPECT_ERROR(bad_host);
    CHECK(bad_host.error.find(kServerHostEnvVar) != std::string::npos);

    const auto bad_port = Parse({}, EndpointEnv{nullptr, "0"});
    EXPECT_ERROR(bad_port);
    CHECK(bad_port.error.find(kServerPortEnvVar) != std::string::npos);

    // An explicit option still wins, so a stale environment cannot break it.
    const auto cli_wins = OK(Parse({"--host", "cli.example", "--port", "5555"},
                                   EndpointEnv{"bad host", "0"}));
    CHECK(cli_wins.endpoint.host == "cli.example");
    CHECK(cli_wins.endpoint.port == 5555);
}

void TestHelp() {
    const auto short_help = OK(Parse({"-h"}));
    CHECK(short_help.help_requested);
    CHECK(short_help.source == EndpointSource::kDefault);

    const auto long_help = OK(Parse({"--help"}));
    CHECK(long_help.help_requested);

    // Help does not mask other valid arguments.
    const auto with_args = OK(Parse({"--host", "example.com", "--help"}));
    CHECK(with_args.help_requested);
    CHECK(with_args.endpoint.host == "example.com");

    // Invalid arguments are still reported even when help was requested.
    EXPECT_ERROR(Parse({"--help", "--nope"}));

    CHECK(std::string(ClientUsageText()).find("--server") != std::string::npos);
}

void TestUiSwitch() {
    // Default: UI on.
    const auto defaults = OK(Parse({}));
    CHECK(defaults.ui_enabled);

    // --no-ui is a pure flag.
    const auto cli_off = OK(Parse({"--no-ui"}));
    CHECK(!cli_off.ui_enabled);
    // ... and combines with an endpoint.
    const auto combined = OK(Parse({"--no-ui", "--server", "10.0.0.5:9000"}));
    CHECK(!combined.ui_enabled);
    CHECK(combined.endpoint.host == "10.0.0.5");
    CHECK(combined.endpoint.port == 9000);
    // A value attached to it is a mistake, not something to interpret.
    EXPECT_ERROR(Parse({"--no-ui=1"}));

    // Environment switch; an explicit "off" keeps the UI on.
    const auto env_off = OK(Parse({}, EndpointEnv{nullptr, nullptr, "1"}));
    CHECK(!env_off.ui_enabled);
    const auto env_truthy = OK(Parse({}, EndpointEnv{nullptr, nullptr, "true"}));
    CHECK(!env_truthy.ui_enabled);
    const auto env_explicit_on = OK(Parse({}, EndpointEnv{nullptr, nullptr, "off"}));
    CHECK(env_explicit_on.ui_enabled);
    const auto env_empty = OK(Parse({}, EndpointEnv{nullptr, nullptr, ""}));
    CHECK(env_empty.ui_enabled);
    // A malformed value is reported rather than silently ignored.
    const auto env_bad = Parse({}, EndpointEnv{nullptr, nullptr, "maybe"});
    EXPECT_ERROR(env_bad);
    CHECK(env_bad.error.find(kUiOffEnvVar) != std::string::npos);

    CHECK(std::string(ClientUsageText()).find("--no-ui") != std::string::npos);
}

}  // namespace

int main() {
    TestDefaults();
    TestHostForms();
    TestPortForms();
    TestServerShorthand();
    TestBadPorts();
    TestBadHosts();
    TestUnknownAndDuplicateOptions();
    TestEnvironmentFallback();
    TestCommandLineOverridesEnvironment();
    TestInvalidEnvironmentIsRejected();
    TestHelp();
    TestUiSwitch();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
