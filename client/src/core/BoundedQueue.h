// Bounded thread-safe message queue for the client's Network -> Main handoff.
//
// Thread model rule (ARCHITECTURE.md section 27): the Network Thread must never
// mutate the client GameWorld; it hands decoded messages to the Main Thread
// through a queue like this one. The queue is bounded so a misbehaving peer
// cannot make the client grow memory without limit.
#pragma once

#include <cstddef>
#include <condition_variable>
#include <deque>
#include <mutex>
#include <optional>
#include <utility>

namespace odyssey::client::core {

enum class QueuePushResult {
    kAccepted,        // item queued
    kDroppedOldest,   // queue was full; oldest item evicted, new item queued
    kRejected,        // queue was full; item refused (reliable semantics)
};

// Single shared queue for one producer thread and one consumer thread
// (or several, when externally synchronized). Default overflow policy is
// drop-oldest, matching the plan's bounded-queue behaviour; TryPush gives
// callers the reliable "refuse when full" path.
template <typename T>
class BoundedQueue {
public:
    explicit BoundedQueue(std::size_t capacity) : capacity_(capacity == 0 ? 1 : capacity) {}

    // Moves `item` in. When the queue is full, evicts the oldest item and
    // accepts the new one. Never blocks and never throws (no allocation of
    // unbounded size: deque growth is bounded by capacity_).
    QueuePushResult Push(T item) {
        std::lock_guard<std::mutex> lock(mutex_);
        QueuePushResult result = QueuePushResult::kAccepted;
        if (queue_.size() >= capacity_) {
            queue_.pop_front();
            result = QueuePushResult::kDroppedOldest;
        }
        queue_.push_back(std::move(item));
        not_empty_.notify_one();
        return result;
    }

    // Reliable variant: refuses the item when full so the caller can decide
    // (plan: a saturated reliable queue is a policy decision for the caller).
    QueuePushResult TryPush(T item) {
        std::lock_guard<std::mutex> lock(mutex_);
        if (queue_.size() >= capacity_) {
            return QueuePushResult::kRejected;
        }
        queue_.push_back(std::move(item));
        not_empty_.notify_one();
        return QueuePushResult::kAccepted;
    }

    // Blocks until an item is available or StopWaiters() is called.
    std::optional<T> Pop() {
        std::unique_lock<std::mutex> lock(mutex_);
        not_empty_.wait(lock, [this] { return closed_ || !queue_.empty(); });
        if (queue_.empty()) {
            return std::nullopt;  // closed_ and drained
        }
        T item = std::move(queue_.front());
        queue_.pop_front();
        return item;
    }

    // Non-blocking pop.
    std::optional<T> TryPop() {
        std::lock_guard<std::mutex> lock(mutex_);
        if (queue_.empty()) {
            return std::nullopt;
        }
        T item = std::move(queue_.front());
        queue_.pop_front();
        return item;
    }

    // Removes all items (returns them so the caller can log/handle leftovers).
    std::deque<T> Drain() {
        std::lock_guard<std::mutex> lock(mutex_);
        std::deque<T> drained;
        drained.swap(queue_);
        return drained;
    }

    // Wakes every Pop() waiter; subsequent Pops return nullopt once empty.
    void Close() {
        std::lock_guard<std::mutex> lock(mutex_);
        closed_ = true;
        not_empty_.notify_all();
    }

    std::size_t Size() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return queue_.size();
    }

    std::size_t Capacity() const { return capacity_; }

private:
    const std::size_t capacity_;
    std::deque<T> queue_;
    mutable std::mutex mutex_;
    std::condition_variable not_empty_;
    bool closed_ = false;
};

}  // namespace odyssey::client::core
