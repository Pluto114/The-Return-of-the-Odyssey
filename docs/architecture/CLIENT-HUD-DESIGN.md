# 客户端 HUD / 界面设计（Katana Zero 霓虹风）

状态：**待审设计稿**（成员 C 提交，Reviewer：A）。落地分期见 [CLIENT-PHASE1.md](CLIENT-PHASE1.md) §10 与定稿提示词 `UIprompt/UIprompt.md`。
范围：只描述**画面上的信息架构与视觉规范**；不改协议、不改 stdout 证据链日志、不改游戏逻辑。

---

## 1. 为什么需要这份设计

重构前的屏幕是一列等宽诊断文字（`Inbound: type=… seq=…`、`Pong: nonce=…`、`Netcode: pending=… corr=…`、`Stats(snapshot): ATK=…`…共十余行），那是 Phase 1 联调期的**调试台**，不是玩家 HUD：无层级、术语面向开发者、缺少游戏 HUD 的锚点布局。

本设计把屏幕分成两层：

| 层 | 内容 | 何时可见 |
| --- | --- | --- |
| **游戏层** | 关卡目标、血条、准星、飘字、受击提示、状态卡 | 常显（按状态矩阵） |
| **开发层** | 本文 §7 的全部诊断信息 | 仅按 `F1` / `F2` / `F3` 打开 |

stdout 周期性/事件日志（每 120 帧、login/match/first snapshot/stage event/recovery/resume）**完全保留**，只是不再常驻屏幕。

---

## 2. 设计原则

1. **异常优先**：断线/重连比重连成功、比战斗信息更优先；异常态下世界 HUD 收起，只显示横幅与倒计时。
2. **两档字号**：主信息 20px、次信息 12–14px；不再出现"所有东西 20px"。
3. **锚点布局**：目标在顶部、生命在左下、装备在右下、提示在底部居中、准星在中心；安全边距左右各 24px、上下各 16px。
4. **玩家语言**：`HP 82/100`、`STAGE 03`、`HOSTILES 4`；不出现 `pending/ack/seq/corr/predTick` 这类词。
5. **数据只画权威值**：位置/血量/剩余怪物全部来自快照；客户端不臆造命中、伤害与奖励。
6. **零分配**：HUD 每帧只用定长 `char buf[]` + `snprintf` + `DrawHudText`（P1a 已达成，渲染块内 `std::string` 构造为 0）。
7. **可降级**：`AccessibilityConfig` 的 `disable_glitch_fx` / `disable_screen_shake` / `disable_damage_floaters` 必须实时生效。
8. **一处改风格**：颜色/间距全部取自 `ui/Theme.h`，不在绘制代码里写死颜色。

---

## 3. 布局（960×540 RT 坐标）

```
┌──────────────────────────── 960 × 540 ────────────────────────────┐
│ [异常横幅]  ⚠ LINK LOST · retry 3/5 · 2.4s          y=16..44 居中  │
│                                                                   │
│                     STAGE 03   HOSTILES 4        y=24 顶部居中     │
│                                                                   │
│                        ◇  准星（世界锚定）                          │
│                    ↗ 受击方向弧   -17 伤害飘字                      │
│                  ● 自己        ▲ 怪物                              │
│                                                                   │
│ ▮▮▮▮▮▮▯▯  HP 82/100            [WPN] [RELIC] [POT ×1]            │
│ y=470 左下                              右对齐至 x=936             │
│            WASD 移动 · 鼠标瞄准 · SPACE 开火     y=506 底部居中      │
└───────────────────────────────────────────────────────────────────┘
```

| 元素 | 位置（RT 坐标） | 数据源 | 阶段 |
| --- | --- | --- | --- |
| 异常横幅（断线/重连/恢复） | 顶部居中，y=16，宽 ≤600 | `RecoveryState` 阶段/次数/倒计时、`ConnectionState` | P1b |
| 关卡目标行 `STAGE nn · HOSTILES m` | 顶部居中 y=24 | `stage.index`、`stage.monsters_remaining` | P1b |
| 过渡横幅 `STAGE CLEAR / TEAM DEFEATED` | 顶部居中 y=64，TTL 2.5s | `kStageClearedEvent` 等 | P1b |
| 分段能量血条 + `HP x/y` | 左下 (24,470)，8 段，宽 320 | `self.hp`/`self.max_hp` + `HealthBar.h` | P1b |
| 受伤白色残影（Damaged Shake） | 覆盖在血条上 | `HudMath.h::DamageGhost` | P1b |
| 装备槽（武器/遗物/药水×数量） | 右下，右对齐 x=936 | **需 A3**（快照装备 ID）；到位前显示快照 `SPD`/`DMG` 数值 | P1b(临时)/A3 后转正 |
| 像素准星 | 世界锚定（鼠标 RT 位置） | `WindowToRT` | P1b |
| 伤害飘字（128 槽池） | 目标世界坐标上方 | `kDamageEvent` + 去重键 `(server_tick, source, target)` | P1b |
| 受击方向弧 | 以自己为圆心 R≈46 | `kDamageEvent` + 来源实体位置 | P1b |
| 操作提示（6s 后淡出） | 底部居中 y=506 | 静态文案 | P1b |
| 奖励卡（三选一 + 倒计时环） | 底部居中，620×150 | `RewardOptions/Applied` + 装备显示表 | P2（ImGui） |
| 登录/匹配面板 | 居中 | `login_*`、`match_*`、`room_id` | P2（ImGui） |
| 调试台 / 实体调试 / 无障碍 | 覆盖层 | 见 §7 | P3 |

**世界几何（已定）**：竞技场**居中放大**为 `480×480 @ (240,30)`（世界是 [0,20]² 正方形；原 `340×280 @ (560,190)` 偏右是为 Phase 1 的左侧诊断列让位，诊断收进 F1 后不再需要）。只改 `kArenaView`/`kArena*` 一处常量，坐标变换代码不变。

**启动窗口（已定）**：保持**整数缩放**，但启动时取显示器能容纳的**最大整数倍**（1920×1080 显示器取 2×，即 1920×1080；实测 1155×918 窗口在 scale=1 下上下各浪费 ~170px 黑边）。窗口仍可自由缩放，`IsWindowResized()` 每次重算 layout；窗口小于 960×540 时按 `max(1, …)` 裁切属已知边界（`SetWindowMinSize` 已尽量阻止）。

---

## 4. 状态矩阵（优先级自上而下）

| 状态 | 可见元素 | 收起 |
| --- | --- | --- |
| `Disconnected / Connecting / Resuming`（最高优先级） | 异常横幅 + 重连倒计时；世界压暗（0.35 黑罩） | 目标行、血条、装备、奖励卡 |
| `Login / Matching / InRoom(waiting)` | 居中面板：状态大字 + 房间号 + Player ID + "WAITING FOR STAGE" | 目标行、血条、装备 |
| `Playing` | 目标行、血条（+残影）、准星、飘字、受击弧、装备、提示（淡出） | 诊断信息 |
| `Reward` | 奖励卡 + 倒计时；世界 HUD 保留但降透明度 | Raylib 旧奖励面板（互斥） |
| `StageClear / PreparingNextStage` | 过渡卡 "STAGE CLEAR" / "NEXT STAGE IN…" | 准星、提示 |
| `Failed / Closed` | 过渡卡 + 结果摘要（`kGameResult` 到位后） | 准星、飘字 |

失焦（window unfocused）：叠加半透明遮罩 + "CLICK TO FOCUS"，切断移动输入但**继续发送零意图**（提示词 §二.4），切回焦点由最新快照重建。

---

## 5. 交互与快捷键

| 键 | 作用 | 阶段 |
| --- | --- | --- |
| 鼠标 | 瞄准（经 `WindowToRT` → `RTToWorld`） | 已实现 |
| `WASD` / `SPACE` | 移动 / 开火（受输入门控：阶段 playing、存活、恢复中静默） | 已实现 |
| `1` `2` `3` | 奖励选择（P2 后由 ImGui 卡接管，Raylib 面板降级时兜底） | P1b/P2 |
| `ENTER` | 报告 ready（仅权威 `PreparingNextStage` + 自身奖励结清） | 已实现 |
| `F1` | 调试台（网络/会话/预测/输入/世界分组） | P1b 最小版 / P3 完整版 |
| `F2` | 实体调试（包围盒、瞄准锥、插值缓冲） | P3 |
| `F3` | 无障碍菜单（三项开关，持久化 `settings.ini`） | P3 |
| `ESC` | 有 ImGui 窗口/面板打开 → 关闭之；否则退出游戏 | P2 |
| `R` | 手动重连（`kExhausted` 后的唯一出口） | 已实现 |

鼠标/键盘双门控（提示词 §一.5）：`io.WantCaptureMouse` 为真时冻结瞄准并 `EnableCursor()`；`io.WantCaptureKeyboard` 为真时屏蔽 WASD/SPACE/1–3，防止在 UI 里输入却让角色移动。模态关闭后保留最后有效 Aim，直到指针回到 RT 有效区域（`IsInsideTarget`）。

---

## 6. 视觉规范

| 项目 | 规范 |
| --- | --- |
| 底色 | `theme.background` `#0A0A10`（RT 内清屏，提示词钉死）；Letterbox 用 `theme.letterbox`；默认帧缓冲再清一次 |
| 战斗地板 | `theme.arena_floor` `#14141F` 填充在 `#0A0A10` 之上（实测：只留底色时 88% 游玩区域亮度仅 2%，观感是"黑屏 + 一层看不见的网格"）；其上叠 `theme.grid` `#2A2A44` 单位网格（中线 `panel_edge` `#3C3C58` 提亮） |
| 亮度阶梯（相对值，Luma/255） | background 10.7 → arena_floor 21.3 → grid 45.0 → panel_edge 63.2，单调递增；单测断言这条阶梯，防止有人把地板调回与底色相同 |
| 离线压暗 | `Fade(BLACK, 0.20)`（原 0.45；暗底再重压会让整个游玩区落到 `#050507`） |
| 文字 | 主 `theme.text`、次 `text_dim`、警告 `text_warn`、危险/断线 `text_danger` |
| 霓虹强调 | `neon_cyan`（正常连接/自己/瞄准线）、`neon_magenta`（过渡横幅）、`neon_yellow`（受击环/奖励标题）、`neon_red`（怪物血条） |
| 实体 | 自己 `player`、队友 `peer`、怪物 `monster`、尸体 `dead`、弹丸 `projectile` |
| 面板 | 半透明 `theme.panel`（α≈235）+ 1px `panel_edge` 描边，无圆角（像素风） |
| 字号 | 主 20 / 次 12–14；标题 32 仅用于启动水印移除后的过渡卡 |
| 动效 | 飘字上浮淡出 0.9s；受伤抖动 0.25s 内衰减（`DamageShakeOffset`，确定性无 RNG）；过渡横幅 2.5s |
| 降级 | `disable_glitch_fx` 关闭扫描线/抖动；`disable_screen_shake` 关闭屏幕位移；`disable_damage_floaters` 仍消费事件但不绘制 |
| 字体 | `assets/fonts/pixel_hud.ttf`（团队提供 + LICENSE）；缺失回退 `FontDefault` 并告警；非 ASCII 降级 `?` / `[RAW_ID_n]` |

---

## 7. 诊断信息迁移表（屏幕常显 → F1）

| 现常显内容 | 迁移后归属（F1 分组） |
| --- | --- |
| `Connection` / `Recovery` / `Login` / `Match` | Session |
| `Inbound: type/seq/bytes`、`Ping/Pong nonce`、`Outbound drops` | Network |
| 双向队列深度（瞬时/峰值） | Network（P3 需 `BoundedQueue::Depth()/MaxDepth()`） |
| `Input intent`、输入门控原因 | Input |
| `Netcode: pending/corr/predTick/ack/spd/alive`、插值延迟与轨道数 | Prediction |
| `View: players/room/tick/snaps`、`Stage: idx/state/remain/monsters/bullets/seed` | World |
| `Last event`、`server_note` | Events |
| `Stats(snapshot): ATK/DEF/SPD/seed` | World（数值）＋ HUD（SPD/DMG 展示） |
| `FPS` | F1 面板标题栏 |
| `Phase 1 - authoritative two-player movement` 水印 | 删除 |

P3 在此之上新增：RTT EMA(α=0.1)、Server Tick 频率（Δ server_tick/Δt，**不得把 10Hz 快照率标成 tick 率**）、Prediction Error EMA（仅在快照到达时更新）、队列深度 EMA 折线图。

---

## 8. P1b 交付清单

**P1b-1（已完成）**
1. ✅ 移除常显诊断列 → `F1` 切换**整屏**诊断视图（SESSION / NETWORK / INPUT / PREDICTION / WORLD / EVENTS 六组，零分配、定长缓冲）；`F2` 实体视图留给 P3
2. ✅ 顶部目标行 `STAGE nn · HOSTILES m`
3. ✅ 过渡卡（clear / preparing / failed / closed，居中面板 + 霓虹品红标题）
4. ✅ 大厅卡（登录/匹配/等待开局，居中；标题只在此屏出现）
5. ✅ 断线态：世界压暗 45% + 顶部红色横幅（含重连说明与 `press R to reconnect`）
6. ✅ 竞技场几何改为 `480×480 @ (240,30)` 居中放大

**P1b-2（已完成）**

7. ✅ 左下分段能量血条（`HealthBar.h`，8 段）+ 白色 Damaged Shake 残影（`HudMath.h::DamageGhost`，抖动幅度受 `disable_screen_shake` 控制）
8. ✅ 世界锚定像素准星（`HexagonCrosshair`；慢速自转属 glitch 效果，受 `disable_glitch_fx` 控制；指针落黑边时变淡）
9. ✅ 受击方向弧（`HudMath.h::HitMarker`，来源位置取 `CombatView::FindMonster` / `GameView::Find`）
10. ✅ 伤害飘字上屏（`DamageDedupeTable` → `FloaterPool` → `WorldToRT`），受 `disable_damage_floaters` 控制；跨会话断开时清空去重表与飘字池
11. ✅ 操作提示 6s 后淡出（每次进入 play 重新计时）

**验收**：`ctest` 5/5 与 logic 套件全绿；P1b 结束后按提示词 §四.3 每期回归门禁执行；无操作时屏幕只剩游戏 HUD。

**P3（部分已完成）**

- ✅ **F2 实体调试**（Raylib，在 RT 内叠加、不替换世界）：玩家/怪物包围盒、自身**发送中的瞄准锥**、自身**权威位姿 ↔ 预测位姿的误差线**、远端与怪物的**原始快照位姿 ↔ 插值位姿连线**（直观显示插值延迟），左下角一行 `F2 DEBUG players=.. monsters=.. interpDelay=..t snaps=..` 便于单张截图自证
- ✅ **F3 无障碍菜单**（Raylib 绘制；同一份状态将来直接喂给 ImGui 菜单）：三项开关（glitch / 屏幕抖动 / 伤害飘字），↑↓ 选择、ENTER/SPACE 切换，改动**立即持久化**到 `settings.ini`（写失败只告警）；全部字段实时生效（`disable_screen_shake` 归零抖动幅度、`disable_damage_floaters` 只消费事件不绘制、`disable_glitch_fx` 停准星自转）
- ✅ **ESC 优先级**：F3 → F2 → F1 依次关闭，都没打开时才退出游戏（对齐提示词 §一.5）
- ⏳ 待做：F1 面板补**双向队列深度（瞬时/峰值）**与 EMA 折线图、RTT/Server Tick/Prediction Error 指标口径落地、`--no-ui` / `ODYSSEY_UI_OFF=1` 开关、Release 下 ≤1.5ms 整帧增量验收（后者的预设来自 D）

---

## 9. 决策记录

| # | 议题 | 结论 |
| --- | --- | --- |
| Q1 | 世界几何 | ✅ **布局 A**：`480×480 @ (240,30)` 居中放大（2026-09-11 拍板） |
| Q2 | A3 到位前的装备槽 | ✅ 先显示快照 `SPD`/`DMG`（A3 携带 WeaponID/RelicID/PotionID 后再换成真实槽位） |
| Q3 | P2 前的奖励选择 UI | ⏳ 暂用现有 Raylib 文本托盘（已知观感一般，P2 换 ImGui 卡后与旧面板互斥）；若希望 P1b 就做卡片式 Raylib 托盘可提出 |
| Q4 | 难度/Modifier/Director 摘要（A4） | ⏳ 待 A4 字段到位再定；在此之前放 F1 的 World 分组 |
