// Headless integration tests for the Network Thread slice.
//
// A tiny in-process Asio echo server (test stub only - D1 allows stubs/fakes
// so C can advance without A's real gameserver) validates:
//   - async connect produces a kConnected state event
//   - Ping frames round-trip as Pong frames with matching sequence
//   - peer close surfaces as a kDisconnected state event
//   - connecting to a closed port surfaces a kFailed state event
//   - Stop() joins the Network Thread cleanly
//
// Message types 1/2 are PROVISIONAL test placeholders, never wire constants.
#include "core/BoundedQueue.h"
#include "core/Frame.h"
#include "core/FramingReader.h"
#include "network/NetClient.h"
#include "network/NetMessage.h"

#include <asio.hpp>

#include <atomic>
#include <chrono>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <memory>
#include <string>
#include <thread>
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

using asio::ip::tcp;
using odyssey::client::core::BoundedQueue;
using odyssey::client::core::EncodeFrame;
using odyssey::client::core::Frame;
using odyssey::client::core::FrameHeader;
using odyssey::client::core::FramingReader;
using odyssey::client::network::ConnectionState;
using odyssey::client::network::NetClient;
using odyssey::client::network::NetEvent;
using odyssey::client::network::NetMessage;

constexpr std::uint16_t kPing = 1;  // provisional
constexpr std::uint16_t kPong = 2;  // provisional

// ---- local stub echo server (test-only) -----------------------------------

class EchoServer {
public:
    EchoServer() : io_(), acceptor_(io_) {
        tcp::endpoint endpoint(asio::ip::make_address("127.0.0.1"), 0);
        acceptor_.open(endpoint.protocol());
        acceptor_.set_option(asio::socket_base::reuse_address(true));
        acceptor_.bind(endpoint);
        acceptor_.listen();
        port_ = acceptor_.local_endpoint().port();
        thread_ = std::thread([this] { io_.run(); });
        AcceptNext();
    }

    ~EchoServer() { Close(); }

    std::uint16_t Port() const { return port_; }

    int PongCount() const { return pong_count_.load(); }

    void Close() {
        io_.post([this] {
            std::error_code ignored;
            acceptor_.close(ignored);
            for (const auto& session : sessions_) {
                session->Close();
            }
        });
        io_.stop();
        if (thread_.joinable()) {
            thread_.join();
        }
    }

private:
    class Session : public std::enable_shared_from_this<Session> {
    public:
        Session(asio::io_context& io, std::atomic<int>& pong_count)
            : socket_(io), reader_(odyssey::client::core::kMaxFrameBodyBytes),
              pong_count_(pong_count) {
            reader_.SetFrameCallback([this](Frame&& frame) {
                if (frame.header.message_type == kPing) {
                    // Echo the same sequence back as a Pong with a copy of the payload.
                    FrameHeader reply;
                    reply.message_type = kPong;
                    reply.sequence = frame.header.sequence;
                    auto bytes = EncodeFrame(reply, frame.body.data(), frame.body.size());
                    std::error_code ec;
                    asio::write(socket_, asio::buffer(bytes), ec);
                    if (!ec) {
                        pong_count_.fetch_add(1);
                    }
                }
            });
        }

        void Start() {
            ReadMore();
        }

        void Close() {
            std::error_code ignored;
            socket_.close(ignored);
        }

        tcp::socket& Socket() { return socket_; }

    private:
        void ReadMore() {
            const auto self = shared_from_this();
            socket_.async_read_some(
                asio::buffer(read_buffer_),
                [self](const std::error_code& ec, std::size_t bytes) {
                    if (ec) {
                        return;
                    }
                    self->reader_.Append(self->read_buffer_.data(), bytes);
                    self->ReadMore();
                });
        }

        tcp::socket socket_;
        FramingReader reader_;
        std::atomic<int>& pong_count_;
        std::array<std::uint8_t, 4096> read_buffer_{};
    };

    void AcceptNext() {
        auto session = std::make_shared<Session>(io_, pong_count_);
        sessions_.push_back(session);
        acceptor_.async_accept(session->Socket(),
                               [this, session](const std::error_code& ec) {
                                   if (!ec) {
                                       session->Start();
                                       AcceptNext();
                                   }
                               });
    }

    asio::io_context io_;
    tcp::acceptor acceptor_;
    std::uint16_t port_ = 0;
    std::thread thread_;
    std::vector<std::shared_ptr<Session>> sessions_;
    std::atomic<int> pong_count_{0};
};

// ---- helpers ---------------------------------------------------------------

using Inbox = BoundedQueue<NetEvent>;

// Pops events for up to timeout_ms, returning the first that satisfies pred.
bool WaitEvent(Inbox& inbox, const std::function<bool(const NetEvent&)>& pred,
               NetEvent& out, int timeout_ms = 5000) {
    const auto deadline = std::chrono::steady_clock::now() + std::chrono::milliseconds(timeout_ms);
    while (std::chrono::steady_clock::now() < deadline) {
        while (auto event = inbox.TryPop()) {
            if (pred(*event)) {
                out = std::move(*event);
                return true;
            }
        }
        std::this_thread::sleep_for(std::chrono::milliseconds(2));
    }
    return false;
}

bool WaitState(Inbox& inbox, ConnectionState state, NetEvent& out) {
    return WaitEvent(inbox, [state](const NetEvent& e) {
        return e.kind == NetEvent::Kind::kStateChanged && e.state == state;
    }, out);
}

bool WaitMessage(Inbox& inbox, std::uint16_t type, std::uint32_t seq, NetEvent& out) {
    return WaitEvent(inbox, [type, seq](const NetEvent& e) {
        return e.kind == NetEvent::Kind::kMessage &&
               e.message.message_type == type && e.message.sequence == seq;
    }, out);
}

// ---- tests -----------------------------------------------------------------

void TestConnectAndPingPong() {
    EchoServer server;
    NetClient client;
    Inbox inbox(256);
    client.SetEventCallback([&inbox](NetEvent&& event) { inbox.Push(std::move(event)); });
    CHECK(client.Start("127.0.0.1", static_cast<std::uint16_t>(server.Port())));

    NetEvent connected;
    CHECK(WaitState(inbox, ConnectionState::kConnected, connected));

    const std::uint8_t payload[] = {0x11, 0x22, 0x33};
    for (std::uint32_t seq = 1; seq <= 3; ++seq) {
        client.SendFrame(kPing, seq, payload, sizeof(payload));
        NetEvent pong;
        CHECK(WaitMessage(inbox, kPong, seq, pong));
        if (pong.kind == NetEvent::Kind::kMessage) {
            CHECK(pong.message.payload.size() == sizeof(payload));
            if (pong.message.payload.size() == sizeof(payload)) {
                CHECK(std::memcmp(pong.message.payload.data(), payload, sizeof(payload)) == 0);
            }
        }
    }
    CHECK(server.PongCount() == 3);
    client.Stop();
}

void TestPeerCloseDisconnects() {
    EchoServer server;
    NetClient client;
    Inbox inbox(256);
    client.SetEventCallback([&inbox](NetEvent&& event) { inbox.Push(std::move(event)); });
    CHECK(client.Start("127.0.0.1", static_cast<std::uint16_t>(server.Port())));

    NetEvent connected;
    CHECK(WaitState(inbox, ConnectionState::kConnected, connected));

    server.Close();  // peer closes: client must observe a state change

    NetEvent ended;
    CHECK(WaitEvent(inbox, [](const NetEvent& e) {
        return e.kind == NetEvent::Kind::kStateChanged &&
               (e.state == ConnectionState::kDisconnected || e.state == ConnectionState::kFailed);
    }, ended));
    client.Stop();
}

void TestConnectRefused() {
    // Grab a port and release it so the connect is refused immediately.
    asio::io_context io;
    tcp::acceptor probe(io, tcp::endpoint(asio::ip::make_address("127.0.0.1"), 0));
    const std::uint16_t closed_port = probe.local_endpoint().port();
    probe.close();

    NetClient client;
    Inbox inbox(256);
    client.SetEventCallback([&inbox](NetEvent&& event) { inbox.Push(std::move(event)); });
    CHECK(client.Start("127.0.0.1", closed_port));

    NetEvent failed;
    CHECK(WaitEvent(inbox, [](const NetEvent& e) {
        return e.kind == NetEvent::Kind::kStateChanged && e.state == ConnectionState::kFailed;
    }, failed));
    CHECK(!failed.detail.empty());
    client.Stop();
}

void TestNoDoubleStart() {
    EchoServer server;
    NetClient client;
    Inbox inbox(256);
    client.SetEventCallback([&inbox](NetEvent&& event) { inbox.Push(std::move(event)); });
    CHECK(client.Start("127.0.0.1", static_cast<std::uint16_t>(server.Port())));
    CHECK(!client.Start("127.0.0.1", static_cast<std::uint16_t>(server.Port())));  // second Start refused
    NetEvent connected;
    CHECK(WaitState(inbox, ConnectionState::kConnected, connected));
    client.Stop();
}

}  // namespace

int main() {
    TestConnectAndPingPong();
    TestPeerCloseDisconnects();
    TestConnectRefused();
    TestNoDoubleStart();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
