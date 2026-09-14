# 角色 C 第二阶段（D4–D9 客户端侧）：本地验证记录

日期：2026-09-11。范围：C 的客户端 D4–D9 实现（战斗表现、奖励、恢复、预测/插值），
**不是全组端到端验收**。
分支：`feature/week2-client-gameplay`（基于 `main` `d4809ce`；团队要求 D4 前先把 `develop` 快进到 `e55dad1`，
本分支在 develop 就绪后 rebase 即可）。

## 已执行检查

| 检查 | 环境 / 结果 |
| --- | --- |
| `scripts/build/build.ps1 -Target client` | Windows x64 / MSVC 14.44 / CMake 3.31.6 / Ninja / vcpkg 固定基线；客户端与全部测试目标编译链接通过 |
| `ctest --test-dir build\client-windows -C Debug` | 4/4 passed（D4–D9 时点）：`odyssey_core_tests`、`odyssey_net_tests`、`odyssey_logic_tests`、`odyssey_protocol_tests`；C-g 已加入第 5 套件 `odyssey_config_tests`（待下次完整构建复核为 5/5） |
| `odyssey_logic_tests` | **233 checks**（D4–D9 的 158 + A5 C-a/C-e 新增 75）：输入归一化/序号（含 `EnsureGreaterThan` 下界）、全量快照移除、战斗视图（怪物/子弹/受击/死亡）、奖励（选项/选择/拒绝/超时/CSV/权威收口）、恢复状态机（退避/拒绝/耗尽）、预测校正重放、步进规则、插值、**会话门控（StageState 取值与命名、可否发输入、可否报 Ready、拦截原因、InputSeq 下界）** |
| `odyssey_protocol_tests` | 载荷往返：Ping/Pong、Login、Match、PlayerInput(含 aim)、WorldSnapshot(玩家/怪物/Stage/属性)、战斗事件（Spawn/Destroy/Damage/Death/Stage）、奖励（Options/Choice/Applied）、Resume |
| 端点配置解析（C-g） | `client/tests/config_tests.cpp` 独立编译运行（MSVC 14.44 `/W4`，无警告）：**129 checks / 0 failures**；`main()` 序言代理程序实测 default/cli/env 三种来源、非法端口 exit 2、`--help` exit 0 |
| 会话门控逻辑（C-a/C-e） | `client/tests/gameview_tests.cpp` 独立编译运行（MSVC 14.44 `/W4`，无警告）：**233 checks / 0 failures**（较改动前 158 增加 75 项） |
| 单帧窗口探针 | `odyssey_window_probe.exe`（红块/蓝圆/文字）用于渲染与事件泵诊断 |

## 覆盖范围（对照 WEEK2 计划）

- **D4**：鼠标 Aim、SPACE Shoot、`MonsterSnapshot` + 七类可靠事件消费、几何表现
- **D5**：玩家/怪物血条、受击闪环、死亡标记、关卡 HUD、实体全量移除、切关清空子弹
- **D6**：宝箱面板（名称/槽位/属性文本）、1–3 选择、超时、`RewardApplied` 如实显示；HUD 属性来自快照
- **D7（部分）**：关卡号/状态/剩余怪物、Ready 发送；难度/Modifier/Director 摘要等待协议字段
- **D8**：有界退避自动重连、Resume 单次发送、令牌被拒回退登录、不重放旧 Session 输入
- **D9**：本地预测 + 服务器校正（仅重放未确认输入）、远端/怪物 10Hz 插值（1 快照延迟）

## A5 缺口硬化（分支 `feature/week2-client-hardening`，基于集成后 `main` `a68acfc`）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| **C-g 端点配置化** | ✅ 已完成 | `client/src/core/ClientConfig.h`（命令行 `--host`/`--port`/`--server`、环境变量 `ODYSSEY_SERVER_HOST`/`ODYSSEY_SERVER_PORT`、默认 `127.0.0.1:7777`、非法值报错退出、`--help` 不开窗）；`client/tests/config_tests.cpp` 独立编译运行 **129 checks / 0 failures**（MSVC 14.44 `/W4` 无警告）；`main.cpp` 启动日志打印实际端点与来源 |
| **C-a Ready 门控** | ✅ 已完成 | `client/src/sync/SessionGate.h`（`StageState` 与服务器 iota 对齐、`CanReportReady` = 权威 `PreparingNextStage` + 自身奖励结清 + 每关一次、`ReadyBlockReason`）；`main.cpp` 删掉魔术 `3`、按关卡 latch、权威状态结束后自动收口奖励面板；`RewardView::SettleAfterAuthoritativeEnd()` 覆盖漏收 `RewardApplied` 的情形 |
| **C-e 恢复期匹配/续号/输入静默** | ✅ 已完成 | Resume 成功置 `match_sent`（**不再发 MatchRequest**）；`session_snapshots` 按连接清零，`CanSendInput` 要求本会话首帧快照后才发输入；首帧快照 `InputSequencer::EnsureGreaterThan(InputSeqFloor(最高已发, LastProcessedInputSeq))`，绝不重放旧区间 |
| C-b 输入阶段门控 | 待做 | 现按 `in_room` + 首帧快照；存活/阶段细分与在途旧输入丢弃策略待 A |
| C-c 药水 | 阻塞 | 需 A 解除 `UsePotion` 占位拒绝 + 快照补装备/药水字段 |
| C-d 服务器移速预测 | 待做 | 真实验证需 A2 后 |
| C-f 有界等待/令牌轮换 | 部分 | 客户端侧可做；`ResumeResponse` 无新 token 字段，轮换语义待 A4 |

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
