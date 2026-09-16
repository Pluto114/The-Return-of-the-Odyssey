// Client-side reconnect/resume state machine (D8, A5 item C-f).
//
// The server owns session identity. This tracks the client's view of a
// transient outage: retry with bounded backoff, send ResumeRequest when a
// resume token is available, and fall back to a fresh login when the token is
// refused. Time is injected (seconds from a monotonic clock) so tests are
// deterministic.
//
// Every wait is bounded (A5 C-f). A retry loop cannot run forever - after
// kMaxAttempts failed connections the machine parks in kExhausted and waits for
// the operator (R) instead of silently retrying or silently stopping - and a
// handshake that is never answered (ResumeRequest or LoginRequest) times out
// into a fresh login rather than leaving the client "connected" but mute.
#pragma once

#include <algorithm>
#include <cstdint>
#include <string>
#include <vector>

namespace odyssey::client::sync {

enum class RecoveryPhase {
    kIdle,            // no outage in progress
    kWaitingToRetry,  // waiting for the next reconnect attempt
    kConnecting,      // TCP connect in flight, or a fresh login awaiting its answer
    kResuming,        // connected, ResumeRequest must be sent
    kRestored,        // session restored (or fresh login after a failure)
    kFailed,          // resume refused / handshake timed out (needs fresh login)
    kExhausted,       // retries or handshakes exhausted: parked until the user acts
};

class RecoveryState {
public:
    static constexpr int kMaxAttempts = 5;
    static constexpr double kInitialBackoffSeconds = 2.0;
    static constexpr double kMaxBackoffSeconds = 10.0;
    // A ResumeRequest or LoginRequest that gets no answer within this window is
    // treated as failed; the connection is not useful while it stays silent.
    static constexpr double kHandshakeTimeoutSeconds = 5.0;

    void SetToken(const std::vector<std::uint8_t>& token) { token_ = token; }
    void ClearToken() { token_.clear(); }
    bool HasToken() const { return !token_.empty(); }
    const std::vector<std::uint8_t>& Token() const { return token_; }

    // Socket dropped while we had a live session (or a reconnect attempt
    // failed). The first drop retries immediately; a failed attempt honours the
    // current backoff so repeated failures do not hammer the server. Once the
    // attempts are used up the machine parks in kExhausted instead of waiting on
    // a retry that will never be allowed to start.
    void OnDisconnect(double now) {
        const bool already_retrying = Active();
        handshake_pending_ = false;
        resume_sent_ = false;
        if (already_retrying) {
            if (attempts_ >= kMaxAttempts) {
                phase_ = RecoveryPhase::kExhausted;
                note_ = "reconnect gave up after " + std::to_string(attempts_) +
                        " attempts: press R to try again";
                return;
            }
            phase_ = RecoveryPhase::kWaitingToRetry;
            next_attempt_at_ = now + backoff_seconds_;
        } else {
            attempts_ = 0;
            backoff_seconds_ = kInitialBackoffSeconds;
            phase_ = RecoveryPhase::kWaitingToRetry;
            next_attempt_at_ = now;  // first retry immediately
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
    void MarkResumeSent(double now) {
        resume_sent_ = true;
        ArmHandshake(now);
    }

    // A fresh LoginRequest was sent on this connection. The phase returns to
    // kConnecting so a second unanswered login is detected too (the timeout check
    // only fires while connecting or resuming).
    void MarkLoginSent(double now) {
        phase_ = RecoveryPhase::kConnecting;
        ArmHandshake(now);
    }

    bool HandshakePending() const { return handshake_pending_; }

    double HandshakeSecondsLeft(double now) const {
        if (!handshake_pending_) {
            return 0.0;
        }
        return std::max(0.0, handshake_deadline_ - now);
    }

    // No answer arrived in time and the connection is still waiting for one.
    bool HandshakeTimedOut(double now) const {
        return handshake_pending_ && now >= handshake_deadline_ &&
               (phase_ == RecoveryPhase::kConnecting || phase_ == RecoveryPhase::kResuming);
    }

    // Give up on this handshake. The token is dropped (a resume that never got an
    // answer cannot be trusted to still be usable) and the caller is expected to
    // start a fresh login; repeated timeouts count towards the same bounded
    // attempt budget as failed connections.
    void OnHandshakeTimeout(double now) {
        (void)now;
        handshake_pending_ = false;
        ClearToken();
        ++attempts_;
        if (attempts_ >= kMaxAttempts) {
            phase_ = RecoveryPhase::kExhausted;
            note_ = "no server response after " + std::to_string(attempts_) +
                    " attempts: press R to try again";
            return;
        }
        phase_ = RecoveryPhase::kFailed;
        note_ = "no login/resume response in " +
                std::to_string(static_cast<int>(kHandshakeTimeoutSeconds)) +
                "s: logging in again";
    }

    void OnResumeResult(bool ok) {
        handshake_pending_ = false;
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
        handshake_pending_ = false;
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
        handshake_pending_ = false;
        handshake_deadline_ = 0.0;
        note_.clear();
    }

private:
    void ArmHandshake(double now) {
        handshake_pending_ = true;
        handshake_deadline_ = now + kHandshakeTimeoutSeconds;
    }

    std::vector<std::uint8_t> token_;
    RecoveryPhase phase_ = RecoveryPhase::kIdle;
    int attempts_ = 0;
    double backoff_seconds_ = kInitialBackoffSeconds;
    double next_attempt_at_ = 0.0;
    bool resume_sent_ = false;
    bool handshake_pending_ = false;
    double handshake_deadline_ = 0.0;
    std::string note_;
};

}  // namespace odyssey::client::sync
