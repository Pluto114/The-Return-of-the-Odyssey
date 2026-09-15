# 角色 C 第二阶段（D4–D9 客户端侧）：本地验证记录

日期：2026-09-11。范围：C 的客户端 D4–D9 实现（战斗表现、奖励、恢复、预测/插值）**及 A5 缺口硬化**，
**不是全组端到端验收**。
分支：D4–D9 原为 `feature/week2-client-gameplay`（基于 `main` `d4809ce`），已并入 `main` `a68acfc`；
A5 硬化在 `feature/week2-client-hardening`（基于集成后的 `main`）上继续。

## 已执行检查

| 检查 | 环境 / 结果 |
| --- | --- |
| `scripts/build/build.ps1 -Target client` | Windows x64 / MSVC 14.44 / CMake 3.31.6 / Ninja / vcpkg 固定基线；**含 A5 四处改动后重新配置并构建成功（`[24/24] Linking CXX executable client\odyssey_client.exe`）** |
| `ctest --test-dir build\client-windows -C Debug` | **5/5 passed**（硬化分支 `cc715c1` 实跑：`odyssey_core_tests` 0.15s、`odyssey_net_tests` 2.35s、`odyssey_logic_tests` 0.16s、`odyssey_config_tests` 0.15s、`odyssey_protocol_tests` 0.20s；合计 3.02s）。D4–D9 时点为 4/4，C-g 补入第 5 套件 |
| 端点配置手测（C-g） | `odyssey_client.exe --help` 打印用法、不开窗；`--port 0` → `invalid port '0' (expected a decimal number in 1..65535)` + 用法、退出码 2；默认启动打印 `main: server endpoint 127.0.0.1:7777 (source=default)`；`--server 192.168.1.20:7777` → `(source=cli)` |
| `odyssey_logic_tests` | **352 checks**（D4–D9 的 158 + A5：C-a/C-e 75、C-d 39、C-b 25、C-f 41、装备表解析/去引号 14；其中 297 为硬化分支 `cc715c1` 的 `ctest` 实测值，后续为独立编译结果，待下次完整构建复核）：输入归一化/序号（含 `EnsureGreaterThan` 下界）、全量快照移除、战斗视图（怪物/子弹/受击/死亡）、**装备显示表解析（生成格式的 CSV 去引号、描述内逗号、转义引号、旧式无引号行）**、奖励（选项/选择/拒绝/超时/CSV/权威收口）、**恢复状态机（退避/拒绝/耗尽/握手超时终止/二次闪断可恢复）**、**tick 驱动预测（每 tick 一步、300Hz 发包不加速、服务器移速、静止、死亡、切关瞬移、异常 tick 限幅、`ClearIntent`）**、步进规则、插值、**会话门控（StageState 取值与命名、可否发输入与拦截原因、可否报 Ready、InputSeq 下界）** |
| `odyssey_protocol_tests` | 载荷往返：Ping/Pong、Login、Match、PlayerInput(含 aim)、WorldSnapshot(玩家/怪物/Stage/属性)、战斗事件（Spawn/Destroy/Damage/Death/Stage）、奖励（Options/Choice/Applied）、Resume |
| 端点配置解析（C-g） | `client/tests/config_tests.cpp` 独立编译运行（MSVC 14.44 `/W4`，无警告）：**129 checks / 0 failures**；`main()` 序言代理程序实测 default/cli/env 三种来源、非法端口 exit 2、`--help` exit 0 |
| 逻辑套件（独立编译复现） | `client/tests/gameview_tests.cpp` 用 MSVC 14.44 `/std:c++20 /W4` 单独编译运行，无警告；各提交时点：158（D4–D9）→ 233（C-a/C-e）→ 272（C-d）→ 297（C-b，与 `ctest` 实测一致）→ 338（C-f）→ **352（装备表）** |
| UI 基础设施 P0a（Katana Zero 重构） | `client/src/ui/{UiGeometry,HealthBar,FloaterPool,AssetPath,Theme}.h` 全部 raylib-free，测试在 `odyssey_logic_tests` 内新增 192 项断言：`scale=1`（960×540、1280×720 带黑边）与 `scale≥2`（1920×1080、2560×1440、3840×2160、限高轴）布局、退化窗口（0/负尺寸不产生 NaN 偏移、小于目标时对称裁切）、`WindowToRT` 黑边钳制与 `IsInsideTarget`、`World↔RT` 往返与角点映射、`WorldToWindow` 与 `WindowToRT` 复合一致、分段血条（满/零/负/半/边界/过量治疗/NaN/单段）、`SegmentWidth` 退化、飘字池生命周期与 FIFO 覆盖（128 槽）、去重表（命中/不同键/过期/容量 256 淘汰/清空）、资源根候选优先级与去重、settings 路径（`%APPDATA%` > `XDG_CONFIG_HOME` > `~/.config` > exe 目录）、主题色与无障碍开关默认值 |
| HUD 字体与文本 P1a（Katana Zero 重构） | ✅ 已完成代码侧：`ui/PixelFont.{h,cpp}`（`LoadFontEx` 启动加载 + `IsFontValid` 回退 + `TEXTURE_FILTER_POINT` + ASCII 降级）、`ui/HudMath.h`（受击方向/受伤残影/六边形准星，纯逻辑）。实测：`PixelFont.cpp` 对真实 raylib 6.0 头文件 `cl /c /W4` 类型检查零警告；`main.cpp` 渲染块内 `std::string`/`std::to_string`/`std::vector` 出现次数为 **0**（HUD 全部改为定长缓冲 + `snprintf` + `DrawTextEx`）；logic 套件新增 50 项断言（**594 checks**）。字体文件（`client/assets/fonts/pixel_hud.ttf` + LICENSE）由团队提供，未到位时走默认字体并告警 |
| HUD 信息架构 P1b-1（Katana Zero 重构） | ✅ **已构建运行验证**（用户本地构建并启动，截图复核）：`F1` 由"常显诊断列"改为**整屏诊断视图**（SESSION / NETWORK / INPUT / PREDICTION / WORLD / EVENTS 六组，仍为零分配定长缓冲）；竞技场居中放大为 `480×480 @ (240,30)`；新增顶部目标行 `STAGE nn · HOSTILES m`、过渡卡（clear/preparing/failed/closed）、大厅卡（登录/匹配/等待开局 + 房间/Player ID）、断线态（世界压暗 + 顶部红色横幅 + `press R to reconnect`）；移除"Phase 1 …"水印与常显 FPS；渲染块内 `std::string`/`std::vector` 构造仍为 0。设计依据见 [CLIENT-HUD-DESIGN.md](../../architecture/CLIENT-HUD-DESIGN.md) §3/§4/§8 |
| 首帧截图复核 + 可读性修正（P1b-1 后） | 用私有测试截图做**像素级复核**（图像区正好 960 宽 = scale 1、四周黑边确认为 letterbox、状态矩阵生效），并量化出 3 个问题：① 采样 12.6 万点发现 **88% 游玩区亮度仅 `#050507`（2%）**、网格 5%、边框 7%；② 1155×918 窗口在 scale=1 下上下各浪费 ~170px；③ 首次启动误报 `LINK LOST`。修正：新增 `Theme::arena_floor`（`#14141F`）叠在钉死的 `#0A0A10` 之上、网格提亮到 `#2A2A44`、边框到 `#3C3C58`、离线压暗 0.45 → 0.20、横幅加强并区分 `NOT CONNECTED / CONNECTING / CONNECTION FAILED / LINK LOST`（附端点）、**启动自动选显示器能容纳的最大整数倍窗口**（本机 2× = 1920×1080）。单测新增亮度阶梯断言（background < floor < grid < edge）→ **598 checks / 0 failures** |
| 暗底主题适配（P1a 补充） | ✅ 已完成：场景底色为提示词要求的深紫黑 `#0A0A10`（RT 内清屏）、Letterbox 用 `theme.letterbox`；竞技场新增 `theme.grid` 单位网格地板；HUD 文字/世界实体/血条/奖励面板全部改用 `ui/Theme.h` 调色板（渲染块内已无 raylib 亮底常量，仅保留 blit 的 `WHITE` tint）。实测：渲染块内 `theme.` 引用 47 处、旧常量仅剩注释与 blit tint；logic 套件 594 checks / 0 failures |
| 渲染管线 P0b（Katana Zero 重构） | ✅ **已构建验证**（用户本地跑通 `build.ps1 -Target client` + `ctest` 5/5 + 手工启动）：可缩放窗口 + `SetExitKey(KEY_NULL)` + RT(960×540) + 双层清屏 + `-540` 翻转 blit + `rlDrawRenderBatchActive()`；启动日志出现 `viewport`/`asset root`/`HUD font`/`accessibility` 四行（字体缺失时按预期告警并回退默认字体） |
| 单帧窗口探针 | `odyssey_window_probe.exe`（红块/蓝圆/文字）用于渲染与事件泵诊断 |

## 覆盖范围（对照 WEEK2 计划）

- **D4**：鼠标 Aim、SPACE Shoot、`MonsterSnapshot` + 七类可靠事件消费、几何表现
- **D5**：玩家/怪物血条、受击闪环、死亡标记、关卡 HUD、实体全量移除、切关清空子弹
- **D6**：宝箱面板（名称/槽位/属性文本）、1–3 选择、超时、`RewardApplied` 如实显示；HUD 属性来自快照
- **D7（部分）**：关卡号/状态/剩余怪物、Ready 发送；难度/Modifier/Director 摘要等待协议字段
- **D8**：有界退避自动重连、Resume 单次发送、令牌被拒回退登录、不重放旧 Session 输入
- **D9**：tick 驱动的本地预测 + 服务器校正（A5 C-d 后：每 30Hz 边界一步、按 tick 而非按包重放、移速与存活取自快照）、远端/怪物 10Hz 插值（1 快照延迟）

## A5 缺口硬化（分支 `feature/week2-client-hardening`，基于集成后 `main` `a68acfc`）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| **C-g 端点配置化** | ✅ 已完成 | `client/src/core/ClientConfig.h`（命令行 `--host`/`--port`/`--server`、环境变量 `ODYSSEY_SERVER_HOST`/`ODYSSEY_SERVER_PORT`、默认 `127.0.0.1:7777`、非法值报错退出、`--help` 不开窗）；`client/tests/config_tests.cpp` 独立编译运行 **129 checks / 0 failures**（MSVC 14.44 `/W4` 无警告）；`main.cpp` 启动日志打印实际端点与来源 |
| **C-a Ready 门控** | ✅ 已完成 | `client/src/sync/SessionGate.h`（`StageState` 与服务器 iota 对齐、`CanReportReady` = 权威 `PreparingNextStage` + 自身奖励结清 + 每关一次、`ReadyBlockReason`）；`main.cpp` 删掉魔术 `3`、按关卡 latch、权威状态结束后自动收口奖励面板；`RewardView::SettleAfterAuthoritativeEnd()` 覆盖漏收 `RewardApplied` 的情形 |
| **C-e 恢复期匹配/续号/输入静默** | ✅ 已完成 | Resume 成功置 `match_sent`（**不再发 MatchRequest**）；`session_snapshots` 按连接清零，`CanSendInput` 要求本会话首帧快照后才发输入；首帧快照 `InputSequencer::EnsureGreaterThan(InputSeqFloor(最高已发, LastProcessedInputSeq))`，绝不重放旧区间 |
| **C-b 阶段/存活/恢复输入门控** | ✅ 已完成（客户端侧） | `SessionGate.h` 的 `InputGate` + `CanSendInput` + `InputBlockReason`：已入房、本会话有快照、无恢复、存活、`stage.state == playing` 才发；门控翻转时 `MovementPredictor::ClearIntent()` 丢弃残留方向，`kStageStartedEvent` 同样清空；`main.cpp` 每帧计算门控并与 HUD/日志共用原因。**在途旧输入的服务器侧丢弃策略仍待 A 确认** |
| C-c 药水 | 阻塞 | 需 A 解除 `UsePotion` 占位拒绝 + 快照补装备/药水字段 |
| **C-d 服务器移速预测 / ACK 语义** | ✅ 已完成 | `Prediction.h` 重写为 tick 驱动：`AdvanceTick()` 每 30Hz 边界一步、`ApplyAuthoritative(x,z,ack,server_tick,move_speed,alive)` 只用 tick 时间线补推进（上限 10 tick）；服务器 `world.go` 的 "stages the newest intent; does not advance position or ack" 即依据。单测覆盖 300Hz 发包不加速、装备移速、静止、死亡/复活、切关瞬移、异常 tick 限幅 |
| **C-f 有界等待 / 二次闪断** | ✅ 已完成（客户端侧） | `RecoveryState`：新增终止态 `kExhausted`（尝试用尽后不再无声等待，HUD `exhausted (press R)`）；`kHandshakeTimeoutSeconds=5` 覆盖 `ResumeRequest` 与 `LoginRequest`（`MarkResumeSent(now)`/`MarkLoginSent(now)`/`HandshakeTimedOut(now)`/`OnHandshakeTimeout(now)`），超时即丢令牌回落全新登录，且与连接失败共用同一尝试预算；恢复成功后再次闪断会重置预算（可反复恢复）。**Token 轮换仍待 A4**（`ResumeResponse` 无新 token 字段，客户端在二次闪断时用旧 token 尝试、被拒后回落全新登录） |

端点解析为纯函数（无窗口/无 socket），因此上述 129 checks 可在任意终端独立复现：

```powershell
ctest --test-dir build\client-windows -C Debug -R odyssey_config_tests --output-on-failure
```

## 未完成 / 依赖

| 尚未完成 | 责任 |
| --- | --- |
| **Ready 屏障的端到端验收**：`MSG_NEXT_STAGE_REQUEST` 全仓只在 `server/internal/session/session.go` 的合法性表中出现，**没有 handler 消费**；`PreparingNextStage` 由服务器在奖励轮次 `Complete()` 后自行推进 | 需 A 接线；C 侧发送时机（C-a）已按 A5 收敛 |
| 双客户端同房战斗、清场/团灭、奖励与三关循环的**真实端到端**验收（W01/W03/W05–W09） | 需服务器 D4–D9 路由（A）与平台（D）；C 配合联调 |
| 难度/全局 Modifier/Director 决策摘要显示 | 需协议先补字段（A/B） |
| 装备显示表同源（D1/D7） | ✅ 已合并到 D 的方案：`data/equipment/catalog.json` 为唯一手写源，**CMake 配置期**校验 `version == 1` 并生成 `equipment.tsv`，post-build 复制到 `<exe>/assets/equipment.tsv` 与 `<exe>/equipment.tsv`；客户端经资源根解析加载（exe 目录回退）。本次已删除 C 先前的 CSV 生成脚本与 `client/assets/data/equipment.csv`（避免两套并行方案），并把 `EquipmentDisplay::stats` 统一为 `description`、解析器改为 TSV（字段数必须为 4，否则跳过）。logic 套件实测 **589 checks / 0 failures** |
| 100 Bot / Grafana / MySQL 等 | D |
| 干净 clone 发布演练（W15）中客户端部分的记录归档 | C（D10 执行时补） |

## 已知问题

- 客户端不决定命中/伤害/奖励合法性；所有权威结果来自服务器快照与事件。
- 早期“纯色图元不上屏”的原因是该 raylib 构建启用 `SUPPORT_CUSTOM_FRAME_CONTROL`：
  `EndDrawing()` 只提交绘制，present 需要显式 `SwapScreenBuffer()`；`main.cpp` / `probe_window.cpp` 已调用（由 main 的 `e55dad1` 修复）。
- 预测/插值均为客户端表现层，任何分歧都以权威快照为准。
