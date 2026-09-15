# 角色 C 交接清单（第 2 周客户端）

> 目的：分支作者（C）无法当面沟通时的异步交接。主负责人按本文即可完成**验证 → 合并测试 → 决定是否进 main**。
> 分支：`feature/week2-client-hardening`（提交 `dfb0c57`），基于 `main`（含 `983c2fe`），**与 main 无冲突基础**（`main` 是 HEAD 的祖先，落后 0 / 领先 25）。
> 位置：已推送到主仓库与 fork 分支同名同提交。

---

## 一、已完成（对照定稿分期 P0–P3 与交付产物要求）

| 交付项 | 状态 | 主要文件 |
| --- | --- | --- |
| P0 基础设施 | ✅ | `ui/Theme.h`、`ui/UiGeometry.h`、`ui/HealthBar.h`、`ui/FloaterPool.h`、`ui/AssetPath.{h,cpp}`；单测覆盖 `scale=1` 与 `scale≥2`、退化窗口、飘字池溢出与去重键 |
| P1a 像素字体与零分配文本 | ✅ 代码侧 | `ui/PixelFont.{h,cpp}`、`ui/HudMath.h`；HUD 全量 `DrawText → DrawTextEx`，渲染块内**零** `std::string`/`std::vector` 构造。字体文件缺省走 raylib 默认字体 + 告警（见 §五） |
| P1b-1 HUD 信息架构与 F1 | ✅ | `main.cpp`：整屏 F1（SESSION/NETWORK/INPUT/PREDICTION/WORLD/EVENTS）、顶部目标行、过渡卡/大厅卡/断线态、启动自动选最大整数倍窗口 |
| P1b-2 战斗 HUD | ✅ | 8 段能量血条 + 受伤残影、六边形准星、受击方向弧、伤害飘字（128 槽池 + `(tick,source,target)` 去重）、操作提示淡出 |
| P2 ImGui 卡片与奖励流 | ⏸ 门禁中 | `client/third_party/rlImGui/**`（逐字节固定版本）+ `CONTRIBUTING.md` 登记 + `client/CMakeLists.txt` 受保护块（`NO_FONT_AWESOME`、仅客户端 target、ImGui 仍只来自 vcpkg）。**ImGui 卡片本身未实现**，等 A 审 `docs/architecture/RLIMGUI-INTEGRATION.md` |
| P3 诊断面板与性能 | ✅（一项待补，见 §二.A） | F1（队列深度瞬时/峰值、RTT/Tick/预测误差 EMA、RTT 与 Tick 折线图）、F2 实体调试、F3 无障碍菜单（写 `settings.ini`）、`--no-ui`/`ODYSSEY_UI_OFF`、Release 性能采集（`ODYSSEY_PERF_FRAMES`/`ODYSSEY_PERF_LOG`/`ODYSSEY_PERF_TRIGGER`） |
| Release 性能验收 | 📋 方案+工具就绪 | `docs/verification/phase2-c/RELEASE-UI-PERF-PLAN.md`（口径、前提、逐步命令、判定、证据归档）+ `scripts/verify/client-release-ui-perf.ps1`（一键跑并出 PASS/FAIL/UNSTABLE）。**执行被 D 的 Release 预设阻塞** |
| D7 阶段摘要 | ✅ 客户端侧就绪 | `sync/StageSummary.h` + `network/PayloadCodec.h` 两个域消息解码器；HUD 一行 + 过关卡结果行 + F1 行。**数字要等服务端发 400/401**（§五） |
| A5 缺口硬化 | ✅ 除 C-c/Token | C-a Ready 门控、C-b 输入门控、C-d tick 驱动预测 + 服务器移速、C-e 恢复期会话/续号、C-f 有界等待 + 二次闪断、C-g 端点配置化（移除硬编码 `10.22.31.251`） |
| 文档与脚本 | ✅ | `client/README.md`、`docs/architecture/CLIENT-PHASE1.md`、`CLIENT-HUD-DESIGN.md`、`RLIMGUI-INTEGRATION.md`、`docs/verification/phase2-c/*`、两个 `scripts/verify/*.ps1` |
| 装备显示表同源 | ✅ | 采用 D 的方案：`data/equipment/catalog.json` 唯一手写源，CMake 配置期校验 version 并生成 TSV，post-build 复制到 `<exe>/assets/equipment.tsv` |

**本分支不含任何私有目录**（本地规划文档与测试图片从未入库；已做推前审计：无个人绝对路径、无用户名、无密钥、无二进制）。

---

## 二、剩余工作，分四部分

### A. C 自己就能做完（不等任何人，约 1 天）

| # | 项 | 说明 | 量级 |
| --- | --- | --- | --- |
| 1 | **C-c 药水输入通路** | 按键 + `network/PayloadCodec.h` 编码 `use_potion` + 一次操作只消费一次。协议字段已存在（`game.proto` 的 `use_potion`）；**权威药水槽/HP 显示**仍缺快照字段（A3） | 半天 |
| 2 | **F1 双向队列深度 EMA 折线图** | 目前只有队列深度**数字**（瞬时/峰值）与 RTT/Tick 两条曲线；分期要求点名"Queue 深度 EMA 折线图" | 1–2h |
| 3 | 辅助指标 UI 命令记录 | 分期要求"辅助记录 UI 命令 + ImGui 提交耗时供诊断"；ImGui 提交耗时依赖 P2 | 1–2h |
| 4 | 文档补漏 | `client/README.md` 补 `ODYSSEY_PERF_*` 一段；`CLIENT-PHASE1.md` 的 `[LOCAL_DISPLAY]` 措辞；C-b 在途旧输入按服务端 `ErrStaleInput` 语义收口（§五） | 1–2h |
| 5 | 截图证据链 | `docs/verification/phase2-c` 目前**无截图**。`disconnected`/`login` 现在可截；`playing`/`reward` 需联调环境。要求：统一分辨率、无个人绝对路径 | 0.5 天（含联调） |

### B. 卡在其他人接线

| 项 | 卡谁 | 具体需要什么 |
| --- | --- | --- |
| **P2 ImGui 卡片（门禁）** | A | 审 `docs/architecture/RLIMGUI-INTEGRATION.md`（供应商代码、`NO_FONT_AWESOME`、git 属性、ImGui-from-vcpkg 规则） |
| **Ready 屏障端到端（A7）** | A | `handleNextStageRequest` **已实现**，但只在 A 的 `feature/network`（未进 main）。客户端发送时机已按 A5 收敛 |
| **D7 数字上线** | A | 发域消息 `MSG_STAGE_STARTED=400` / `MSG_STAGE_CLEARED=401`，并填 `modifiers` / `difficulty_score` / `clear_time_ms`（客户端解码与显示已就绪） |
| **Token 轮换（A4）** | A | `ResumeResponse` 至今**没有新 token 字段**；客户端二次闪断仍用旧 token，被拒后回落全新登录 |
| **装备/药水槽显示（A3）** | A | 快照仍无 Weapon/Relic/Potion 字段，装备槽显示 SPD/DMG |
| 在途旧输入策略 | A | 服务端 `world.go` 的 `ErrStaleInput`（拒绝过期序号）已是事实答案，需 A 书面确认即可闭合 C-b 尾巴 |
| **Release 构建预设** | **D** | `CMakePresets.json` 只有 Debug。精确改动（3 处）已写在 `RELEASE-UI-PERF-PLAN.md` §2.1：新增 configure preset `client-windows-release`、对应 build preset、`build.ps1` 的 `ValidateSet` 加 `client-release`。**这是 Release ≤1.5ms 验收的唯一阻塞** |
| 性能验收的第二名玩家 | B | `loadbot` 功能模式命令（房间满 2 人才 `StartStage`；用 bot 陪玩可让被测客户端成为唯一 GUI 进程） |
| HUD 像素字体 | B/D | `client/assets/fonts/pixel_hud.ttf`（缺失时回退默认字体，不崩，但分期要求的是点阵字体） |

### C. 结构性偏差（需你或 A 拍板，不紧急）

定稿要求"重构并生成 `ui/Theme.h`、`ui/HudRenderer.h/.cpp`、`ui/RewardWindow.h/.cpp`、`ui/DebugOverlay.h/.cpp`"。现状：`ui/Theme.h` 存在，**后三者不存在** —— HUD/奖励/调试的绘制都在 `main.cpp`（约 2.2k 行）配合 `ui/*.h` 的纯逻辑头。

两条路，请二选一：

1. **拆分重构**（0.5–1 天，有回归风险）：把绘制块搬进 `ui/HudRenderer.cpp`（HUD 常显层）、`ui/DebugOverlay.cpp`（F1/F2/F3）、`ui/RewardWindow.cpp`（奖励面板）。功能等价，纯结构搬迁。
2. **保留现状并书面认可**：在 `CLIENT-PHASE1.md` 里写明"HUD 绘制保留在 `main.cpp`、可测逻辑在 `ui/*.h`，理由：零分配渲染块与 RT 坐标耦合"，由 A 在评审记录里确认。

### D. 联调与最终验收（等 A/D 到位后一次性跑）

W01 双客户端首关 → W05/W06 奖励 → W08 三关循环 → W09 断线恢复；`WEEK2-AD-FINALIZATION.md` 的 F01（A+C）、F03（A+D+C）、F04（A+D+C）三门；Release 性能报告（同场景 ≥600 帧、`mean(on) − mean(off) ≤ 1.5 ms`、附硬件说明）；D10 干净 clone 演练中的客户端部分记录。

---

## 三、如何验证本分支

```powershell
# 构建与回归（门禁 5/5）
pwsh -File scripts/build/build.ps1 -Target client
ctest --test-dir build\client-windows -C Debug --output-on-failure
```

预期断言数（分支作者本机 `ctest` 已 5/5 通过；下列数字亦经独立编译复现）：

| 套件 | 断言数 |
| --- | --- |
| `odyssey_core_tests` | 557 |
| `odyssey_net_tests` | 未改动 |
| `odyssey_logic_tests` | 709 |
| `odyssey_config_tests` | 190 |
| `odyssey_protocol_tests` | 158 |

已做的额外检查：`main.cpp`、`NetClient.cpp` 以 CMake 同款参数 `/W4` 单独编译 exit 0（无我方警告）；`main.cpp` 纯 ASCII、大括号配平；渲染块内 `std::string`/`std::vector` 构造为 0。
**唯一未由 C 验证的点**：客户端 target 的 rlImGui + ImGui 完整链接，请在 CMake 构建里确认一次。

联调与性能：

```powershell
# 双客户端首关联调（14 步清单，脚本会自己起服务器）
pwsh -File scripts/verify/client-first-stage-playtest.ps1
# 前置（上游新增依赖，需网络）：cd server; go mod download all

# Release UI 性能验收（需 D 的 Release 预设；脚本拒绝用非 Release 二进制出结论）
pwsh -File scripts/verify/client-release-ui-perf.ps1 -Rounds 3 -Frames 600
```

---

## 四、下游必须知道的关键事实（避免踩坑）

1. **两类阶段消息不是一回事**
   - 可靠事件 `MSG_STAGE_STARTED_EVENT=325` / `MSG_STAGE_CLEARED_EVENT=326`：**只有 `stage_index` + `server_tick`**，服务端目前只发这两个。
   - 域消息 `MSG_STAGE_STARTED=400` / `MSG_STAGE_CLEARED=401`（`stage.proto`）：带 `modifiers` / `difficulty_score` / `clear_time_ms`，**服务端从未发送、字段也未填值**。
   - 两者字段号不重合（`StageCleared` 连 `server_tick` 都没有），**不可互相解析**：客户端为此写了两个独立解码器。
   - 因此现在游戏里 F1 会显示 `stage detail=none(400/401)`，这是**如实反映**，不是客户端缺陷。
2. **`--no-ui` 的边界**：只跳过 UI 层（HUD 文字/血条残影/准星/受击弧/飘字/提示/F1-F2-F3）；**保留** RT 管线、双层清屏、`-540` 翻转 blit、世界渲染与全部网络/游戏逻辑。这是"同一场景 UI 开 vs 关整帧 CPU 差"能成立的前提。
3. **性能口径**：只认 Release；同场景 ≥600 帧；**剔除首帧字体图集**（采集器丢弃计数前 10 个预热帧）；判定 `mean(on) − mean(off) ≤ 1.5 ms` 且必须报 max 与硬件说明。`ODYSSEY_PERF_TRIGGER=playing` 是验收必需项——它让计数从进入战斗开始，否则 600 帧会落在登录/匹配阶段。
4. **rlImGui 供应商代码**：`client/third_party/rlImGui/`（zlib 许可，分支 `RL60ImGui19207`，提交 `3bc5731c`，逐字节固定）。CMake 块是受保护的：仅客户端 target、`NO_FONT_AWESOME`、**不得链接进 `odyssey_*_tests`**（无头测试不能引入窗口依赖）。ImGui 一律来自 vcpkg，不 vendor。
5. **字体缺失即回退**：`client/assets/fonts/pixel_hud.ttf` 不在仓库里，缺省走 `GetFontDefault()` 并打印 `main: WARN HUD font ...`，不会崩。
6. **测试进程不得弹模态框**：`protocol_tests.cpp` 在 Debug 下把 CRT 报告改到 stderr —— 默认模式会弹模态对话框，会把无头 `ctest` 变成挂起而不是失败（F08/D10 回归会遇到）。
7. **设置文件位置**：`settings.ini` 写系统配置目录（Windows `%APPDATA%\Odyssey\`，POSIX `$XDG_CONFIG_HOME/odyssey/` 或 `~/.config/odyssey/`，都不行才落 exe 目录），**绝不写入源码树**。

---

## 五、撤回与清理

- 只撤主仓库分支：`git push upstream --delete feature/week2-client-hardening`
- 只撤 fork 分支：`git push origin --delete feature/week2-client-hardening`
- 两者都只删分支，`main` 不受影响（推送时 `main` 保持 `983c2fe` 未动）。

## 六、依赖速查

| 谁 | 需要他做什么 | 卡住 C 的哪一项 |
| --- | --- | --- |
| A | 审 rlImGui；合 `feature/network`（A7 Ready + `4462d21` 药水映射）；发 400/401 并填值；`ResumeResponse` 补 token；快照补装备/药水字段 | P2、A7 验收、D7 数字、Token 轮换、A3 装备槽 |
| D | 加 Release 预设（`RELEASE-UI-PERF-PLAN.md` §2.1 补丁） | Release ≤1.5ms 性能验收 |
| B | `loadbot` 功能模式命令；`pixel_hud.ttf` | 性能验收场景、P1a 点阵字体 |
| C | §二.A 五项 + §二.C 决策 | — |
