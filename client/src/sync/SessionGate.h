// Session/stage gating rules for the client loop (A5 items C-a and C-e).
//
// Both rules below come from server-side facts rather than client preference:
//
//   * The server owns stage progression. stage.State becomes
//     PreparingNextStage once the reward round is complete
//     (server/internal/game/rewards.go), so "ready for the next stage" may only
//     be reported once the authoritative snapshot says so - not while the
//     reward panel is still open.
//   * A successful ResumeResponse rebinds the session and is followed by a full
//     WorldSnapshot (proto/session.proto). Until that snapshot arrives the client
//     holds no authoritative state, so it must not send input (and must not feed
//     prediction) from a stale or empty view.
//
// They live here as pure predicates so the loop cannot drift from them and the
// rules stay headlessly unit-testable.
#pragma once

#include "sync/RewardView.h"

#include <algorithm>
#include <cstdint>

namespace odyssey::client::sync {

// Mirrors server stage.State (the iota order in server/internal/game/stage/plan.go).
// Values are compared against the raw WorldSnapshot.stage.state field; unknown
// wire values are reported as "?" rather than silently mapped onto a state.
enum class StageState : std::uint32_t {
    kWaiting = 0,
    kPlaying = 1,
    kStageClear = 2,
    kReward = 3,
    kPreparingNextStage = 4,
    kFailed = 5,
    kClosed = 6,
};

inline const char* StageStateName(std::uint32_t wire_state) {
    switch (static_cast<StageState>(wire_state)) {
        case StageState::kWaiting: return "waiting";
        case StageState::kPlaying: return "playing";
        case StageState::kStageClear: return "clear";
        case StageState::kReward: return "reward";
        case StageState::kPreparingNextStage: return "preparing";
        case StageState::kFailed: return "failed";
        case StageState::kClosed: return "closed";
    }
    return "?";
}

inline bool IsPreparingNextStage(std::uint32_t wire_state) {
    return wire_state == static_cast<std::uint32_t>(StageState::kPreparingNextStage);
}

// The reward phase is over for this client: nothing is being offered and no
// choice is awaiting its RewardApplied acknowledgement.
inline bool RewardSettled(RewardState state) {
    switch (state) {
        case RewardState::kNone:
        case RewardState::kApplied:
        case RewardState::kRejected:
        case RewardState::kTimedOut:
            return true;
        case RewardState::kOffered:
        case RewardState::kChosen:
            return false;
    }
    return false;
}

// True once the authoritative stage state proves the reward round ended. Used to
// close a panel left open by a missed/after-the-fact RewardApplied, so it cannot
// block the ready barrier forever.
inline bool AuthoritativeRewardPhaseEnded(std::uint32_t wire_state) {
    switch (static_cast<StageState>(wire_state)) {
        case StageState::kPreparingNextStage:
        case StageState::kFailed:
        case StageState::kClosed:
            return true;
        case StageState::kWaiting:
        case StageState::kPlaying:
        case StageState::kStageClear:
        case StageState::kReward:
            return false;
    }
    return false;
}

// Input may only be transmitted with a live room session and after the first
// authoritative snapshot of that session has been applied.
inline bool CanSendInput(bool in_room, bool session_has_snapshot) {
    return in_room && session_has_snapshot;
}

// The ready barrier: the server has moved to PreparingNextStage, this client has
// finished its own reward interaction, and it has not reported ready yet for
// this stage.
inline bool CanReportReady(bool in_room,
                           std::uint32_t wire_stage_state,
                           RewardState reward_state,
                           bool already_reported) {
    return in_room && IsPreparingNextStage(wire_stage_state) && RewardSettled(reward_state) &&
           !already_reported;
}

// Why ready is currently unavailable, for the HUD and the pairing logs. Only
// meaningful while CanReportReady() is false.
inline const char* ReadyBlockReason(bool in_room,
                                    std::uint32_t wire_stage_state,
                                    RewardState reward_state,
                                    bool already_reported) {
    if (!in_room) {
        return "not in room";
    }
    if (IsPreparingNextStage(wire_stage_state) && !RewardSettled(reward_state)) {
        return "your reward choice is still pending";
    }
    if (already_reported) {
        return "already reported for this stage";
    }
    return "waiting for authoritative preparing state";
}

// A resumed session must never look like a replay: the next InputSeq has to stay
// strictly above both the highest sequence already sent on this connection and
// the server's LastProcessedInputSeq from the snapshot that follows the resume.
inline std::uint32_t InputSeqFloor(std::uint32_t highest_sent, std::uint32_t last_processed) {
    return std::max(highest_sent, last_processed);
}

}  // namespace odyssey::client::sync
