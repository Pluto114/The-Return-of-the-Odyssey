#pragma once

#include <array>

namespace odyssey::client::ui {

// Player-facing copy lives here so the font atlas is built from the same
// strings that the interface actually draws. F3 diagnostics stay in English.
inline constexpr const char* kTitle = "奥德赛归途";
inline constexpr const char* kSubtitle = "双人远征 / 星际航线";
inline constexpr const char* kPilot = "玩家";
inline constexpr const char* kStage = "关卡";
inline constexpr const char* kAlly = "队友";
inline constexpr const char* kDown = "已倒下";
inline constexpr const char* kWaiting = "等待中";
inline constexpr const char* kOnline = "已连接";
inline constexpr const char* kOffline = "未连接";
inline constexpr const char* kHostiles = "个敌人";
inline constexpr const char* kMission = "任务 / 清除所有敌人";
inline constexpr const char* kYou = "你";
inline constexpr const char* kStageWaiting = "等待队友";
inline constexpr const char* kStagePlaying = "战斗中";
inline constexpr const char* kStageClear = "本关完成";
inline constexpr const char* kStageReward = "选择奖励";
inline constexpr const char* kStagePreparing = "准备下一关";
inline constexpr const char* kStageFailed = "远征失败";
inline constexpr const char* kStageClosed = "房间已关闭";
inline constexpr const char* kStageStandby = "待命";
inline constexpr const char* kStagePrefix = "第";
inline constexpr const char* kStageStartedBanner = "关开始";
inline constexpr const char* kStageClearedBanner = "关已完成";
inline constexpr const char* kStageDefeatedBanner = "队伍全灭";
inline constexpr const char* kSalvage = "回收战利品";
inline constexpr const char* kChoose = "点击一张卡片，或按数字键 1 / 2 / 3";
inline constexpr const char* kInstalling = "正在装备所选奖励…";
inline constexpr const char* kReadyWaiting = "已准备，等待队友";
inline constexpr const char* kInstalled = "奖励已装备，按回车准备下一关";
inline constexpr const char* kExpired = "选择超时，等待服务器发放默认奖励";
inline constexpr const char* kRejected = "选择未生效，等待服务器确认";
inline constexpr const char* kExpeditionComplete = "远征完成";
inline constexpr const char* kAllSecure = "所有关卡已完成";
inline constexpr const char* kScanning = "正在搜寻本关战利品…";
inline constexpr const char* kIncoming = "奖励即将送达";
inline constexpr const char* kNewExpedition = "新的远征";
inline constexpr const char* kCrewDefeated = "队伍全灭";
inline constexpr const char* kPreparingSector = "正在准备新关卡";
inline constexpr const char* kRestartTogether = "两位玩家将一起重新开始";
inline constexpr const char* kStayOnline = "请保持两位玩家在线";
inline constexpr const char* kStarting = "启动中";
inline constexpr const char* kStartNewRun = "重新开局";
inline constexpr const char* kClickOrN = "点击按钮或按 N";
inline constexpr const char* kAssembling = "等待队友加入";
inline constexpr const char* kLinking = "正在连接服务器";
inline constexpr const char* kSecondPilot = "还需要一位玩家";
inline constexpr const char* kKeepServer = "请先启动游戏服务器";
inline constexpr const char* kAutoMatch = "组队将自动开始";
inline constexpr const char* kRetryLink = "按 R 重试连接";
inline constexpr const char* kUpgrade = "奖励";
inline constexpr const char* kClickOrDigits = "点卡片 / 按 1 2 3";
inline constexpr const char* kWaitInstall = "装备中…";
inline constexpr const char* kWaitServer = "等待服务器";
inline constexpr const char* kNextSector = "下一关";
inline constexpr const char* kWaitAlly = "等待队友";
inline constexpr const char* kPressReady = "按回车准备";
inline constexpr const char* kWaitUpgrade = "先选择奖励";
inline constexpr const char* kMove = "移动";
inline constexpr const char* kAim = "瞄准";
inline constexpr const char* kMouse = "鼠标";
inline constexpr const char* kFire = "射击";
inline constexpr const char* kHoldSpace = "按住空格";
inline constexpr const char* kRestart = "重开";
inline constexpr const char* kNAfterDefeat = "失败后按 N";
inline constexpr const char* kExit = "退出";
inline constexpr const char* kDeveloper = "开发视图";

inline constexpr std::array kPlayerLabels{
    kTitle, kSubtitle, kPilot, kStage, kAlly, kDown, kWaiting, kOnline, kOffline,
    kHostiles, kMission, kYou, kStageWaiting, kStagePlaying, kStageClear,
    kStageReward, kStagePreparing, kStageFailed, kStageClosed, kStageStandby, kStagePrefix,
    kStageStartedBanner, kStageClearedBanner, kStageDefeatedBanner, kSalvage,
    kChoose, kInstalling, kReadyWaiting, kInstalled, kExpired, kRejected,
    kExpeditionComplete, kAllSecure, kScanning, kIncoming, kNewExpedition,
    kCrewDefeated, kPreparingSector, kRestartTogether, kStayOnline, kStarting,
    kStartNewRun, kClickOrN, kAssembling, kLinking, kSecondPilot, kKeepServer,
    kAutoMatch, kRetryLink, kUpgrade, kClickOrDigits, kWaitInstall, kWaitServer,
    kNextSector, kWaitAlly, kPressReady, kWaitUpgrade, kMove, kAim, kMouse,
    kFire, kHoldSpace, kRestart, kNAfterDefeat, kExit, kDeveloper
};

}  // namespace odyssey::client::ui
