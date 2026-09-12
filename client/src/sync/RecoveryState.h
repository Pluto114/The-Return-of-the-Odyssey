// Client-side reconnect/resume state machine (D8).
//
// The server owns session identity. This tracks the client's view of a
// transient outage: retry with bounded backoff, send ResumeRequest when a
// resume token is available, and fall back to a fresh login when the token is
// refused. Time is injected (seconds from a monotonic clock) so tests are
// deterministic.
#pragma once

#include <algorithm>
#include <cstdint>
#include <string>
#include <vector>

namespace odyssey::client::sync {

enum class RecoveryPhase {
    kIdle,            // no outage in progress
    kWaitingToRetry,  // waiting for the next reconnect attempt
    kConnecting,      // TCP connect in flight
    kResuming,        // connected, ResumeRequest must be sent
    kRestored,        // session restored (or fresh login after a failure)
    kFailed,          // resume refused / retries exhausted (needs fresh login)
};

class RecoveryState {
public:
    static constexpr int kMaxAttempts = 5;
    static constexpr double kInitialBackoffSeconds = 2.0;
    static constexpr double kMaxBackoffSeconds = 10.0;

    void SetToken(const std::vector<std::uint8_t>& token) { token_ = token; }
    void ClearToken() { token_.clear(); }
    bool HasToken() const { return !token_.empty(); }
    const std::vector<std::uint8_t>& Token() const { return token_; }

    // Socket dropped while we had a live session (or a reconnect attempt
    // failed). The first drop retries immediately; a failed attempt honours the
    // current backoff so repeated failures do not hammer the server.
    void OnDisconnect(double now) {
        const bool already_retrying = Active();
        phase_ = RecoveryPhase::kWaitingToRetry;
        resume_sent_ = false;
        if (!already_retrying) {
            attempts_ = 0;
            backoff_seconds_ = kInitialBackoffSeconds;
            next_attempt_at_ = now;  // first retry immediately
        } else {
            next_attempt_at_ = now + backoff_seconds_;
        }
        note_ = HasToken() ? "link lost: will resume session" : "link lost: will login again";
    }

    bool ShouldRetry(double now) const {
        return phase_ == RecoveryPhase::kWaitingToRetry && attempts_ < kMaxAttempts &&
               now >= next_attempt_at_;
    }

    // Called by the main loop right before it initiates a connect attempt.
    void MarkRetryStarted(double now) {
        ++attempts_;
        phase_ = RecoveryPhase::kConnecting;
        next_attempt_at_ = now + backoff_seconds_;
        backoff_seconds_ = std::min(backoff_seconds_ * 2.0, kMaxBackoffSeconds);
        note_ = "reconnect attempt " + std::to_string(attempts_) + "/" +
                std::to_string(kMaxAttempts);
    }

    // TCP is up again.
    void OnConnected() {
        if (HasToken()) {
            phase_ = RecoveryPhase::kResuming;
            resume_sent_ = false;
            note_ = "connected: sending ResumeRequest";
        } else {
            phase_ = RecoveryPhase::kConnecting;
            note_ = "connected: fresh login";
        }
    }

    // ResumeRequest must be sent exactly once per connection.
    bool WantsResumeRequest() const {
        return phase_ == RecoveryPhase::kResuming && !resume_sent_;
    }
    void MarkResumeSent() { resume_sent_ = true; }

    void OnResumeResult(bool ok) {
        if (ok) {
            phase_ = RecoveryPhase::kRestored;
            attempts_ = 0;
            note_ = "session restored";
        } else {
            // The token is unusable (expired/forged/replayed): the caller
            // clears it and starts a fresh login instead of replaying inputs.
            ClearToken();
            phase_ = RecoveryPhase::kFailed;
            note_ = "resume refused: falling back to fresh login";
        }
    }

    void OnFreshLoginOk() {
        phase_ = RecoveryPhase::kRestored;
        attempts_ = 0;
        note_ = "logged in";
    }

    RecoveryPhase Phase() const { return phase_; }
    const std::string& Note() const { return note_; }
    int Attempts() const { return attempts_; }
    bool Exhausted() const { return attempts_ >= kMaxAttempts; }
    bool Active() const {
        return phase_ == RecoveryPhase::kWaitingToRetry || phase_ == RecoveryPhase::kConnecting ||
               phase_ == RecoveryPhase::kResuming;
    }

    void Reset() {
        phase_ = RecoveryPhase::kIdle;
        attempts_ = 0;
        backoff_seconds_ = kInitialBackoffSeconds;
        next_attempt_at_ = 0.0;
        resume_sent_ = false;
        note_.clear();
    }

private:
    std::vector<std::uint8_t> token_;
    RecoveryPhase phase_ = RecoveryPhase::kIdle;
    int attempts_ = 0;
    double backoff_seconds_ = kInitialBackoffSeconds;
    double next_attempt_at_ = 0.0;
    bool resume_sent_ = false;
    std::string note_;
};

}  // namespace odyssey::client::sync
