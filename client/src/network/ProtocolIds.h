// Thin adapter exposing A's authoritative message type IDs to client code.
//
// Single point of truth for wire message types - derived from the generated
// common.pb.h MessageType enum. Client modules reference these constants
// instead of pulling protobuf headers directly; when A updates the protocol
// we regenerate and only this file needs adjustment if any rename occurs.
#pragma once

#include "common.pb.h"

#include <cstdint>

namespace odyssey::client::network::ids {

inline constexpr std::uint16_t kPing = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_PING);
inline constexpr std::uint16_t kPong = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_PONG);
inline constexpr std::uint16_t kDisconnect = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_DISCONNECT);

inline constexpr std::uint16_t kLoginRequest = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_LOGIN_REQUEST);
inline constexpr std::uint16_t kLoginResponse = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_LOGIN_RESPONSE);
inline constexpr std::uint16_t kResumeRequest = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_RESUME_REQUEST);
inline constexpr std::uint16_t kResumeResponse = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_RESUME_RESPONSE);

inline constexpr std::uint16_t kMatchRequest = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_MATCH_REQUEST);
inline constexpr std::uint16_t kMatchFound = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_MATCH_FOUND);
inline constexpr std::uint16_t kMatchCancel = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_MATCH_CANCEL);

inline constexpr std::uint16_t kPlayerInput = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_PLAYER_INPUT);
inline constexpr std::uint16_t kWorldSnapshot = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_WORLD_SNAPSHOT);

// Stage / reward / metrics reserved for later phases.
inline constexpr std::uint16_t kStageStarted = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_STAGE_STARTED);
inline constexpr std::uint16_t kStageCleared = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_STAGE_CLEARED);
inline constexpr std::uint16_t kRewardOptions = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_REWARD_OPTIONS);
inline constexpr std::uint16_t kRewardChoice = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_REWARD_CHOICE);
inline constexpr std::uint16_t kRewardApplied = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_REWARD_APPLIED);
inline constexpr std::uint16_t kNextStageRequest = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_NEXT_STAGE_REQUEST);
inline constexpr std::uint16_t kPerformanceMetrics = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_PERFORMANCE_METRICS);
inline constexpr std::uint16_t kStagePlan = static_cast<std::uint16_t>(odyssey::protocol::v1::MSG_STAGE_PLAN);

}  // namespace odyssey::client::network::ids
