#include "network/NetClient.h"

#include "core/Frame.h"
#include "core/FramingReader.h"

#include <asio.hpp>

#include <array>
#include <atomic>
#include <deque>
#include <memory>
#include <mutex>
#include <thread>
#include <utility>
#include <vector>

namespace odyssey::client::network {

using asio::ip::tcp;

namespace {

constexpr std::size_t kDefaultOutboundQueueCapacity = 64;
constexpr std::size_t kReadBufferBytes = 8192;

}  // namespace

class NetClient::Impl : public std::enable_shared_from_this<NetClient::Impl> {
public:
    explicit Impl(std::size_t outbound_capacity)
        : io_(), work_guard_(asio::make_work_guard(io_)), strand_(asio::make_strand(io_)),
          socket_(strand_), resolver_(strand_), reader_(core::kMaxFrameBodyBytes),
          outbound_capacity_(outbound_capacity > 0 ? outbound_capacity
                                                   : kDefaultOutboundQueueCapacity) {
        reader_.SetFrameCallback([this](core::Frame&& frame) {
            NetEvent event;
            event.kind = NetEvent::Kind::kMessage;
            event.message.message_type = frame.header.message_type;
            event.message.sequence = frame.header.sequence;
            event.message.payload = std::move(frame.body);
            Emit(std::move(event));
        });
    }

    ~Impl() { Stop(); }

    void SetEventCallback(EventCallback callback) {
        on_event_ = std::move(callback);
    }

    bool Start(std::string host, std::uint16_t port) {
        if (thread_started_ || stopping_.load() || !on_event_) {
            return false;
        }
        thread_started_ = true;
        io_thread_ = std::thread([self = shared_from_this()] { self->io_.run(); });
        Connect(std::move(host), port);
        return true;
    }

    bool IsRunning() const {
        return thread_started_ && !stopping_.load();
    }

    // Published outbound queue depth for the debug overlay. The queue itself is
    // io-thread only, so the Main Thread reads these atomics instead.
    std::size_t OutboundDepthPublished() const {
        return outbound_depth_.load(std::memory_order_relaxed);
    }

    std::size_t OutboundPeakPublished() const {
        return outbound_peak_.load(std::memory_order_relaxed);
    }

    void ResetOutboundPeak() {
        outbound_peak_.store(outbound_depth_.load(std::memory_order_relaxed),
                             std::memory_order_relaxed);
    }

    // Initiates a connection attempt. No-op while connecting/connected/stopped.
    void Connect(std::string host, std::uint16_t port) {
        if (stopping_.load() || !on_event_) {
            return;
        }
        asio::post(strand_, [self = shared_from_this(), host = std::move(host), port] {
            self->BeginConnect(host, port);
        });
    }

    void SendFrame(std::uint16_t message_type, std::uint32_t sequence,
                   const std::uint8_t* payload, std::size_t payload_size) {
        if (stopping_.load()) {
            return;
        }
        std::vector<std::uint8_t> bytes;
        try {
            core::FrameHeader header;
            header.message_type = message_type;
            header.sequence = sequence;
            bytes = core::EncodeFrame(header, payload, payload_size);
        } catch (const std::invalid_argument&) {
            return;
        }
        asio::post(strand_, [self = shared_from_this(), bytes = std::move(bytes)] {
            self->EnqueueOutbound(std::move(bytes));
        });
    }

    void Stop() {
        bool should_join = false;
        {
            std::lock_guard<std::mutex> lock(stop_mutex_);
            if (stopping_.exchange(true)) {
                return;
            }
            should_join = thread_started_;
        }
        if (should_join) {
            asio::post(strand_, [self = shared_from_this()] {
                std::error_code ignored;
                self->socket_.close(ignored);
                self->resolver_.cancel();
            });
            work_guard_.reset();
            if (io_thread_.joinable()) {
                io_thread_.join();
            }
        }
    }

private:
    void BeginConnect(const std::string& host, std::uint16_t port) {
        if (connecting_ || connected_ || stopping_.load()) {
            return;
        }
        connecting_ = true;
        EmitState(ConnectionState::kConnecting, host + ":" + std::to_string(port));
        const auto self = shared_from_this();
        resolver_.async_resolve(
            host, std::to_string(port),
            [self](const std::error_code& ec, const tcp::resolver::results_type& results) {
                self->OnResolved(ec, results);
            });
    }

    void OnResolved(const std::error_code& ec, const tcp::resolver::results_type& results) {
        if (stopping_.load()) {
            return;
        }
        if (ec) {
            OnConnectFailed("resolve: " + ec.message());
            return;
        }
        const auto self = shared_from_this();
        asio::async_connect(socket_, results,
                            [self](const std::error_code& ec2, const tcp::endpoint&) {
                                self->OnConnected(ec2);
                            });
    }

    void OnConnected(const std::error_code& ec) {
        connecting_ = false;
        if (stopping_.load()) {
            return;
        }
        if (ec) {
            OnConnectFailed(ec.message());
            return;
        }
        connected_ = true;
        std::error_code local_ec;
        const std::string peer = socket_.remote_endpoint(local_ec).address().to_string();
        EmitState(ConnectionState::kConnected, peer);
        reader_.Reset();
        StartRead();
    }

    void OnConnectFailed(const std::string& reason) {
        connecting_ = false;
        connected_ = false;
        std::error_code ignored;
        socket_.close(ignored);
        EmitState(ConnectionState::kFailed, reason);
    }

    void StartRead() {
        const auto self = shared_from_this();
        socket_.async_read_some(
            asio::buffer(read_buffer_),
            [self](const std::error_code& ec, std::size_t bytes) {
                self->OnRead(ec, bytes);
            });
    }

    void OnRead(const std::error_code& ec, std::size_t bytes) {
        if (stopping_.load()) {
            return;
        }
        if (ec) {
            if (ec == asio::error::operation_aborted) {
                return;
            }
            OnIoClosed(ec == asio::error::eof || ec == asio::error::connection_reset
                           ? ConnectionState::kDisconnected
                           : ConnectionState::kFailed,
                       ec.message());
            return;
        }
        if (!reader_.Append(read_buffer_.data(), bytes)) {
            OnIoClosed(ConnectionState::kFailed, "framing error: " + reader_.error_message());
            return;
        }
        StartRead();
    }

    // EOF/reset or a fatal framing error: the socket is done.
    void OnIoClosed(ConnectionState state, const std::string& reason) {
        connected_ = false;
        std::error_code ignored;
        socket_.close(ignored);
        EmitState(state, reason);
    }

    void EnqueueOutbound(std::vector<std::uint8_t> bytes) {
        if (stopping_.load() || !connected_) {
            return;
        }
        if (write_queue_.size() >= outbound_capacity_) {
            write_queue_.pop_front();
            NetEvent dropped;
            dropped.kind = NetEvent::Kind::kOutboundDropped;
            dropped.detail = "outbound queue full; oldest frame dropped";
            Emit(std::move(dropped));
        }
        write_queue_.push_back(std::move(bytes));
        PublishOutboundDepth();
        PumpWrites();
    }

    // Queue depth for the debug overlay: this runs on the io thread while the Main
    // Thread reads, so the values are published through atomics rather than exposing
    // the queue itself.
    void PublishOutboundDepth() {
        const std::size_t depth = write_queue_.size();
        outbound_depth_.store(depth, std::memory_order_relaxed);
        if (depth > outbound_peak_.load(std::memory_order_relaxed)) {
            outbound_peak_.store(depth, std::memory_order_relaxed);
        }
    }

    void PumpWrites() {
        if (write_in_flight_ || write_queue_.empty() || stopping_.load() || !connected_) {
            return;
        }
        write_buffer_ = std::move(write_queue_.front());
        write_queue_.pop_front();
        PublishOutboundDepth();
        write_in_flight_ = true;
        const auto self = shared_from_this();
        asio::async_write(socket_, asio::buffer(write_buffer_),
                          [self](const std::error_code& ec, std::size_t) {
                              self->write_in_flight_ = false;
                              if (ec) {
                                  if (ec == asio::error::operation_aborted) {
                                      return;
                                  }
                                  self->OnIoClosed(ConnectionState::kFailed,
                                                   "write: " + ec.message());
                                  return;
                              }
                              self->PumpWrites();
                          });
    }

    void EmitState(ConnectionState state, const std::string& detail) {
        NetEvent event;
        event.kind = NetEvent::Kind::kStateChanged;
        event.state = state;
        event.detail = detail;
        Emit(std::move(event));
    }

    void Emit(NetEvent&& event) {
        if (on_event_) {
            on_event_(std::move(event));
        }
    }

    asio::io_context io_;
    asio::executor_work_guard<asio::io_context::executor_type> work_guard_;
    asio::strand<asio::io_context::executor_type> strand_;
    tcp::socket socket_;
    tcp::resolver resolver_;
    core::FramingReader reader_;
    std::array<std::uint8_t, kReadBufferBytes> read_buffer_{};

    EventCallback on_event_;
    bool connecting_ = false;  // io-thread only
    bool connected_ = false;   // io-thread only
    std::atomic<bool> stopping_{false};
    bool thread_started_ = false;  // main-thread only
    std::thread io_thread_;
    mutable std::mutex stop_mutex_;

    std::deque<std::vector<std::uint8_t>> write_queue_;  // io-thread only
    std::vector<std::uint8_t> write_buffer_;             // io-thread only
    bool write_in_flight_ = false;                       // io-thread only
    const std::size_t outbound_capacity_;
    // Published to the Main Thread for the debug overlay (plan: instant + peak queue
    // depth for both directions).
    std::atomic<std::size_t> outbound_depth_{0};
    std::atomic<std::size_t> outbound_peak_{0};
};

const char* ToString(ConnectionState state) {
    switch (state) {
        case ConnectionState::kIdle: return "idle";
        case ConnectionState::kConnecting: return "connecting";
        case ConnectionState::kConnected: return "connected";
        case ConnectionState::kDisconnected: return "disconnected";
        case ConnectionState::kFailed: return "failed";
    }
    return "unknown";
}

NetClient::NetClient(std::size_t outbound_queue_capacity)
    : impl_(std::make_shared<Impl>(outbound_queue_capacity)) {}

NetClient::~NetClient() = default;

void NetClient::SetEventCallback(EventCallback callback) {
    impl_->SetEventCallback(std::move(callback));
}

bool NetClient::Start(std::string host, std::uint16_t port) {
    return impl_->Start(std::move(host), port);
}

void NetClient::Connect(std::string host, std::uint16_t port) {
    impl_->Connect(std::move(host), port);
}

bool NetClient::IsRunning() const {
    return impl_->IsRunning();
}

std::size_t NetClient::OutboundDepth() const {
    return impl_->OutboundDepthPublished();
}

std::size_t NetClient::OutboundMaxDepth() const {
    return impl_->OutboundPeakPublished();
}

void NetClient::ResetOutboundMaxDepth() {
    impl_->ResetOutboundPeak();
}

void NetClient::SendFrame(std::uint16_t message_type, std::uint32_t sequence,
                          const std::uint8_t* payload, std::size_t payload_size) {
    impl_->SendFrame(message_type, sequence, payload, payload_size);
}

void NetClient::Stop() {
    impl_->Stop();
}

}  // namespace odyssey::client::network
