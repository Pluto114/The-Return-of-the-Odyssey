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
| `odyssey_logic_tests` | **338 checks**（D4–D9 的 158 + A5：C-a/C-e 75、C-d 39、C-b 25、C-f 41；其中 297 为硬化分支 `cc715c1` 的 `ctest` 实测值，338 为 C-f 后的独立编译结果，待下次完整构建复核）：输入归一化/序号（含 `EnsureGreaterThan` 下界）、全量快照移除、战斗视图（怪物/子弹/受击/死亡）、奖励（选项/选择/拒绝/超时/CSV/权威收口）、**恢复状态机（退避/拒绝/耗尽/握手超时终止/二次闪断可恢复）**、**tick 驱动预测（每 tick 一步、300Hz 发包不加速、服务器移速、静止、死亡、切关瞬移、异常 tick 限幅、`ClearIntent`）**、步进规则、插值、**会话门控（StageState 取值与命名、可否发输入与拦截原因、可否报 Ready、InputSeq 下界）** |
| `odyssey_protocol_tests` | 载荷往返：Ping/Pong、Login、Match、PlayerInput(含 aim)、WorldSnapshot(玩家/怪物/Stage/属性)、战斗事件（Spawn/Destroy/Damage/Death/Stage）、奖励（Options/Choice/Applied）、Resume |
| 端点配置解析（C-g） | `client/tests/config_tests.cpp` 独立编译运行（MSVC 14.44 `/W4`，无警告）：**129 checks / 0 failures**；`main()` 序言代理程序实测 default/cli/env 三种来源、非法端口 exit 2、`--help` exit 0 |
| 逻辑套件（独立编译复现） | `client/tests/gameview_tests.cpp` 用 MSVC 14.44 `/std:c++20 /W4` 单独编译运行，无警告；各提交时点：158（D4–D9）→ 233（C-a/C-e）→ 272（C-d）→ 297（C-b，与 `ctest` 实测一致）→ **338（C-f）** |
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
| 正式装备静态配置（当前 `client/assets/data/equipment.csv` 为**显示占位**，不参与战斗计算） | B 提供版本化配置后替换 |
| 100 Bot / Grafana / MySQL 等 | D |
| 干净 clone 发布演练（W15）中客户端部分的记录归档 | C（D10 执行时补） |

## 已知问题

- 客户端不决定命中/伤害/奖励合法性；所有权威结果来自服务器快照与事件。
- 早期“纯色图元不上屏”的原因是该 raylib 构建启用 `SUPPORT_CUSTOM_FRAME_CONTROL`：
  `EndDrawing()` 只提交绘制，present 需要显式 `SwapScreenBuffer()`；`main.cpp` / `probe_window.cpp` 已调用（由 main 的 `e55dad1` 修复）。
- 预测/插值均为客户端表现层，任何分歧都以权威快照为准。
