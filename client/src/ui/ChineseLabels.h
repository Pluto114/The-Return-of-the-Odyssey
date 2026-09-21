#pragma once

#include <array>

namespace odyssey::client::ui {

// Player-facing copy lives here so the font atlas is built from the same
// strings that the interface actually draws. F3 diagnostics stay in English.
inline constexpr const char* kTitle = "奥德赛归途";
inline constexpr const char* kWindowTitle = "奥德赛归途 · 战术地图 V2 · 积分榜";
inline constexpr const char* kSubtitle = "双人远征 / 星际航线 / 战术地图 V2";
inline constexpr const char* kPilot = "玩家";
inline constexpr const char* kAttack = "攻击";
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
inline constexpr const char* kPlayAgain = "再玩一把";
inline constexpr const char* kClickOrN = "点击按钮或按 N";
inline constexpr const char* kFlightRecord = "远征飞行记录";
inline constexpr const char* kTeamScore = "团队评分";
inline constexpr const char* kClearedSectors = "已完成关卡";
inline constexpr const char* kElapsed = "用时";
inline constexpr const char* kRank = "排名";
inline constexpr const char* kKills = "击杀";
inline constexpr const char* kDamage = "伤害";
inline constexpr const char* kDamageTaken = "承伤";
inline constexpr const char* kDowns = "倒地";
inline constexpr const char* kScore = "积分";
inline constexpr const char* kNoScoreData = "等待战斗记录同步";
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
inline constexpr const char* kNAfterFinish = "结束后按 N";
inline constexpr const char* kExit = "退出";
inline constexpr const char* kDeveloper = "开发视图";
inline constexpr const char* kPotion = "药剂";
inline constexpr const char* kPressQ = "按 Q 使用";
inline constexpr const char* kAttackIncreased = "攻击力已提升至";
inline constexpr const char* kMaxHealthIncreased = "生命上限已提升至";
inline constexpr const char* kMoveSpeedIncreased = "移动速度已提升至";
inline constexpr const char* kHealthRestored = "生命已恢复";
inline constexpr const char* kHealth = "生命";
inline constexpr const char* kWeapon = "武器";
inline constexpr const char* kBaseWeapon = "基础武器";
inline constexpr const char* kWeaponCollected = "已拾取武器";
inline constexpr const char* kNoPotion = "暂无";
inline constexpr const char* kMapSupplies = "本关道具";
inline constexpr const char* kPickupSyncMissing = "未收到地图道具，请重启游戏服务器";
inline constexpr const char* kDirector = "AI 导演 / 难度";
inline constexpr const char* kLastStage = "上关用时";
inline constexpr const char* kTeamHealth = "队伍生命";
inline constexpr const char* kLearning = "正在观察团队表现";
inline constexpr const char* kAmmo = "弹药";
inline constexpr const char* kReload = "换弹";
inline constexpr const char* kReloading = "装弹中…";
inline constexpr const char* kEmptyMagazine = "弹匣已空 / 按 R 换弹";
inline constexpr const char* kMagazineIncreased = "弹匣容量已提升至";
inline constexpr const char* kEffects = "特效";
inline constexpr const char* kEffectsFull = "完整";
inline constexpr const char* kEffectsReduced = "简化";
inline constexpr const char* kEnvironment = "星域";
inline constexpr const char* kAzureNebula = "蔚蓝星云";
inline constexpr const char* kFrozenMoon = "冰封卫星";
inline constexpr const char* kEmberRift = "余烬裂谷";
inline constexpr const char* kAncientRelay = "远古中继站";
inline constexpr const char* kVoidGarden = "虚空花园";
inline constexpr const char* kIonStorm = "离子风暴";
inline constexpr const char* kFormation = "阵型";
inline constexpr const char* kCrosswindGates = "错流闸门";
inline constexpr const char* kBrokenRing = "破环阵列";
inline constexpr const char* kTwinCorridors = "双层走廊";
inline constexpr const char* kSpiralRelay = "螺旋中继";
inline constexpr const char* kCornerBastions = "四角堡垒";
inline constexpr const char* kStaggeredGauntlet = "交错险径";

inline constexpr std::array kPlayerLabels{
    kTitle, kSubtitle, kPilot, kAttack, kStage, kAlly, kDown, kWaiting, kOnline, kOffline,
    kHostiles, kMission, kYou, kStageWaiting, kStagePlaying, kStageClear,
    kStageReward, kStagePreparing, kStageFailed, kStageClosed, kStageStandby, kStagePrefix,
    kStageStartedBanner, kStageClearedBanner, kStageDefeatedBanner, kSalvage,
    kChoose, kInstalling, kReadyWaiting, kInstalled, kExpired, kRejected,
    kExpeditionComplete, kAllSecure, kScanning, kIncoming, kNewExpedition,
    kCrewDefeated, kPreparingSector, kRestartTogether, kStayOnline, kStarting,
    kStartNewRun, kPlayAgain, kClickOrN, kFlightRecord, kTeamScore, kClearedSectors,
    kElapsed, kRank, kKills, kDamage, kDamageTaken, kDowns, kScore, kNoScoreData,
    kAssembling, kLinking, kSecondPilot, kKeepServer,
    kAutoMatch, kRetryLink, kUpgrade, kClickOrDigits, kWaitInstall, kWaitServer,
    kNextSector, kWaitAlly, kPressReady, kWaitUpgrade, kMove, kAim, kMouse,
    kFire, kHoldSpace, kRestart, kNAfterDefeat, kNAfterFinish, kExit, kDeveloper,
    kPotion, kPressQ, kAttackIncreased, kMaxHealthIncreased, kMoveSpeedIncreased,
    kHealthRestored, kHealth, kWeapon, kBaseWeapon, kWeaponCollected, kNoPotion,
    kMapSupplies, kPickupSyncMissing, kDirector, kLastStage, kTeamHealth, kLearning,
    kAmmo, kReload, kReloading, kEmptyMagazine, kMagazineIncreased,
    kEffects, kEffectsFull, kEffectsReduced, kEnvironment, kAzureNebula, kFrozenMoon,
    kEmberRift, kAncientRelay, kVoidGarden, kIonStorm, kFormation, kCrosswindGates,
    kBrokenRing, kTwinCorridors, kSpiralRelay, kCornerBastions, kStaggeredGauntlet
};

}  // namespace odyssey::client::ui
