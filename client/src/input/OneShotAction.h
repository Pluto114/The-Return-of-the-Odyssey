#pragma once

namespace odyssey::client::input {

// Latches a short key press until the next fixed-rate input packet consumes it.
// This prevents a one-frame action from being missed between 30 Hz sends.
class OneShotAction {
public:
    void Press() { pending_ = true; }

    bool Consume() {
        const bool was_pending = pending_;
        pending_ = false;
        return was_pending;
    }

    bool Pending() const { return pending_; }
    void Reset() { pending_ = false; }

private:
    bool pending_ = false;
};

}  // namespace odyssey::client::input
