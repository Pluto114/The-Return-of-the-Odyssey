# Odyssey C++ Client

服务端权威多人 Roguelike 的 C++20 客户端（角色 C：客户端 / 实时同步 / 联调）。

- 渲染：raylib 6.0
- 网络：standalone Asio（独立 Network Thread）
- 协议：`proto/` 生成的 Protobuf + 16 字节大端帧头（见 [docs/protocol](../docs/protocol/README.md)）

## 构建

在仓库根目录（PowerShell 7）：

```powershell
pwsh -NoProfile -File scripts/bootstrap.ps1          # 首次：装本地工具链
. ./scripts/env.ps1
pwsh -File scripts/setup-client.ps1                  # 固定版本 vcpkg + raylib/asio/protobuf/imgui
pwsh -File scripts/generate-proto/generate.ps1       # 从 proto/ 生成 Go + C++
pwsh -File scripts/build/build.ps1 -Target client
```

产物：`build/client-windows/client/odyssey_client.exe`（另有 `odyssey_window_probe.exe` 纯窗口诊断程序）。

装备显示表由 B 的目录**生成**（客户端不解析 JSON）：

```powershell
pwsh -File scripts/generate-equipment/generate.ps1          # 目录变更后重新生成
pwsh -File scripts/generate-equipment/generate.ps1 -Check   # 判表是否过期（不一致退出码 1）；当前未挂进共享 check 脚本，由调用方执行
```

## 资源与设置（UI 重构 P0b）

- 构建后 `client/assets/` 会被复制到 `<exe 目录>/assets`；客户端启动时按 `<exe>/assets` → `<CWD>/assets` → `<CWD>/client/assets` 的顺序解析**并缓存**一次资源根（渲染循环内不做路径解析），因此可以从任意工作目录启动
- 资源缺失只告警并回退（字体缺 → raylib 默认字体；装备表缺 → 奖励面板显示原始 ID），不会崩溃

## HUD 字体（P1）

- HUD 使用点阵字体：把 TTF 放到 **`client/assets/fonts/pixel_hud.ttf`**（随源码提交对应 LICENSE，例如 OFL 授权字体）
- 启动时用 `LoadFontEx` 加载一次（基础字号 32，只取可打印 ASCII 32–126 的字符集）并做 `TEXTURE_FILTER_POINT`；文件缺失/无效 → 自动回退 `GetFontDefault()` 并打印 `main: WARN HUD font ...`，**不会崩溃**
- 界面统一英文 + 数字 + 十六进制；任何可能来自服务器/操作系统的字符串（如本地化的连接错误）都会经 `SanitizeAscii` 把非 ASCII 字节降级为 `?`，避免采样图集里不存在的字形；只有 ID 可信时使用 `[RAW_ID_<id>]` 占位
- 渲染循环内的自研代码**不构造 `std::string`/`std::vector`**：所有 HUD 行都用定长 `char buf[]` + `snprintf` 拼装（已核对渲染块内相关构造为 0）
- `settings.ini` 写入系统配置目录（Windows `%APPDATA%\Odyssey\`，POSIX `$XDG_CONFIG_HOME/odyssey/` 或 `~/.config/odyssey/`，都不行才落到 exe 目录），**绝不写入源码树**；读取失败或目录不可写只告警并保留内存默认值
- 窗口：可缩放（最小 960×540），画面按**整数倍**缩放并加黑边（`scale = max(1, floor(min(w/960, h/540)))`）；`ESC` 由客户端接管（raylib 默认关窗已被禁用），当前无 ImGui 面板时按 ESC 直接退出

## 测试

```powershell
ctest --test-dir build\client-windows -C Debug --output-on-failure
```

五个无头套件：`odyssey_core_tests`（帧编解码/组帧/队列）、`odyssey_net_tests`（网络线程/断连/背压）、
`odyssey_logic_tests`（输入、视图、战斗、奖励、恢复、预测/插值）、`odyssey_protocol_tests`（协议载荷往返）、
`odyssey_config_tests`（端点配置解析与校验，129 checks）。

## 运行（本地联调）

```powershell
# 终端 1：服务器（默认 127.0.0.1:7777）
. ./scripts/env.ps1
go run ./server/cmd/gameserver
# 终端 2：客户端
build\client-windows\client\odyssey_client.exe
```

## 端点配置（A5 / C-g）

客户端不再硬编码服务器地址，端点按 **命令行 > 环境变量 > 内置默认** 解析，默认 `127.0.0.1:7777`：

| 方式 | 写法 |
| --- | --- |
| 命令行 | `--host <addr>`、`--port <n>`、`--server <host[:port]>`（均支持 `--opt=value` 形式）；`--help` / `-h` 打印用法 |
| 环境变量 | `ODYSSEY_SERVER_HOST`、`ODYSSEY_SERVER_PORT` |

```powershell
build\client-windows\client\odyssey_client.exe                              # 127.0.0.1:7777
build\client-windows\client\odyssey_client.exe --server 192.168.1.20:7777   # 跨机联调
build\client-windows\client\odyssey_client.exe --host localhost --port 9000
```

非法值（端口 `0`/`65536`/非数字、host 含空白或超长、未知选项、缺参数值、同一项重复指定）**直接报错退出（exit 2）**，
不会静默回退或连到别的地址；`--help` 不开窗直接退出（exit 0）。启动第一行日志打印实际生效的端点与来源，便于联调核对：

```text
main: server endpoint 192.168.1.20:7777 (source=cli)     # source: cli | env | default
```

跨机还需要服务端监听 `0.0.0.0` 并放行该 TCP 端口（属 A/D 侧）。解析与校验实现见 `client/src/core/ClientConfig.h`，单测 `odyssey_config_tests`。

当前主分支正式入口支持匹配和移动，但尚未启动首关；下列战斗/奖励/恢复操作需 A/D 接通入口后联调。完整剩余需求见 [A / D 收尾清单](../docs/plans/WEEK2-AD-FINALIZATION.md)。

## 操作

| 输入 | 作用 |
| --- | --- |
| `WASD` | 移动意图（30Hz 发送，只发意图不发坐标） |
| 鼠标 | 瞄准方向（相对自身权威位置归一化） |
| `SPACE` | 射击（按住持续开火，冷却由服务器决定） |
| `1` `2` `3` | 奖励宝箱选择（服务器校验合法性） |
| `ENTER` | 报告“准备下一关”：仅当权威状态已是 `PreparingNextStage` 且自身奖励已结清；提前按会显示被拦截原因 |
| `R` | 失败后手动重连 |
| `ESC` / 关闭按钮 | 停止网络线程并退出 |

## 线程与边界（校验用）

- **Network Thread** 只做 Socket / 组帧 / 解码，向有界队列投递 `NetEvent`；**绝不**修改客户端世界或渲染状态。
- **Main Thread** 每帧 Drain 队列、应用快照、绘制；所有权威状态来自服务器。
- 客户端不发送坐标、命中或伤害结果；当前发送输入序号、时间、移动、瞄准与射击。协议已有 `use_potion`，但客户端键位/编码尚待补齐。

## 已实现（第二周 D4–D9 客户端侧）

- D4 战斗：Aim/Shoot 发送；`MonsterSnapshot` 与七类可靠事件消费；几何图形表现
- D5 表现：玩家/怪物血条、受击闪环、死亡标记、关卡 HUD、实体全量移除、切关清空子弹
- D6 奖励：宝箱面板（名称/槽位/属性）、1–3 选择、超时、`RewardApplied` 如实显示
- D7（部分）：关卡号/状态/剩余怪物与 Ready 发送（难度/全局 Modifier/Director 摘要**等待协议字段**）
- D8 恢复：有界退避自动重连、`ResumeRequest` 单次发送、令牌被拒后回退全新登录、**不重放旧会话输入**
- D9 同步质量：tick 驱动的本地预测 + 服务器校正（每 30Hz 边界一步、按 tick 而非按包重放）、远端玩家/怪物 10Hz 插值

## 已实现（A5 收尾硬化，分支 `feature/week2-client-hardening`）

- **C-g 端点配置化**：`--host`/`--port`/`--server` 与 `ODYSSEY_SERVER_HOST`/`ODYSSEY_SERVER_PORT`，带校验、默认本机、启动日志标注来源（详见上节）
- **C-a Ready 门控**：只认权威 `PreparingNextStage`（服务器在奖励轮次 `Complete()` 后自行进入该状态）+ 自身奖励已结清，并按关卡一次性 latch；权威状态已结束而本地面板仍挂着（漏收 `RewardApplied`）时会自动收口，避免永久阻塞
- **C-e 恢复期会话/续号**：Resume 成功后**不再发送 MatchRequest**；每个连接在收到首帧权威快照前**不发输入、不喂预测**；恢复会话的首帧快照会把 InputSeq 抬到 `max(断线前最高已发, LastProcessedInputSeq)` 之上，绝不重放旧区间
- **C-d 预测口径**：改为 tick 驱动——每 30Hz 边界恰好一步（发包 30Hz 还是 300Hz 都不改变预测速度）、移速取快照 `self.move_speed`（装备加成即时生效、非法值拒绝并保留上次有效值）、死亡时不推进、校正只补"本地已模拟过快照 tick"的那几 tick 且限幅 10，杜绝按包加步
- **C-b 输入门控**：意图只在「已入房 + 本会话已收到权威快照 + 无恢复进行中 + 自己存活 + 关卡正在 `playing`」时发送；任一条件不满足即静默（不消耗 InputSeq、不发包），并在门控翻转时丢弃已记住的方向，避免阶段切换/死亡/恢复后残留旧意图再走一步。HUD 与日志直接给出被拦截原因
- **C-f 有界等待**：重连尝试用尽后进入**终止态** `kExhausted`（HUD 显示 `exhausted (press R)`、日志提示按 R），不再无声停摆；`ResumeRequest`/`LoginRequest` 若 5 秒无响应即判超时——丢弃令牌、回落全新登录，HUD 显示 `handshake_left=<秒>` 倒计时；超时与连接失败共用同一份尝试预算（最多 5 次）后终止；恢复成功后再次闪断会重置尝试次数与退避（可反复恢复）
- 待办：C-c 药水（阻塞于 A2/A3）；Token 轮换语义待 A4（`ResumeResponse` 目前无新 token 字段）

## 已知限制 / 依赖

- 装备显示表 `client/assets/data/equipment.csv` **由 `data/equipment/catalog.json` 生成**（`scripts/generate-equipment/generate.ps1`），运行期不解析 JSON、也不手改 CSV：
  - 行格式 `id,"name","slot","stats"`，`stats` 直接取目录里的 `description` 原文（客户端**不**从 `modifiers` 反推数值），解析器会剥掉 CSV 引号
  - ID 为 B 的版本化编号（当前 1001/1002 武器、2001/2002 遗物、3001/3002 药水）；脚本拒绝 <1000 的旧占位 ID、重复 ID 与缺字段
  - `-Check` 模式供 CI 判"表是否与目录脱节"；目录改了就重新生成，否则奖励面板会退化成 `equipment#<id> (config pending)`
- **`NextStageRequest` 服务器侧尚无处理逻辑**：全仓只在 `server/internal/session/session.go` 的合法性表里出现（InRoom/Reward 合法），没有任何 handler 消费它；奖励完成后的 `PreparingNextStage` 是服务器自己推进的。因此客户端已按 A5 要求把 ready 收敛到正确时机，但**ready 屏障的端到端验收仍取决于 A 接线**。
- 药水（C-c）尚未收口。**Token 轮换待 A4**：`LoginResponse` 只发一次 `resume_token`，`ResumeResponse` 没有新 token 字段，所以客户端在二次闪断时仍会用旧 token 尝试 resume，被拒后回落全新登录（不会重放旧输入）；若 A 决定 resume 后令牌单次消费并轮换，请给出新 token 的下发字段。
- **输入只在权威 `stage.state == playing` 时发送**（C-b）：如果服务器尚未启动首关（状态停在 `waiting`），客户端会如实保持静默并在 HUD 显示 `input=muted:stage is not being played` —— 这是 A1（Match 后提交 `StartStage`）未接线的可见表现，而不是客户端卡死。
- 在途旧输入的**丢弃策略仍需 A 确认**：客户端当前采取保守做法（门控翻转即丢弃意图、不重放、序号继续单调），若服务器在切关时对在途输入另有处理（丢弃窗口/复位期望序号），请同步给 C。
- 难度/Modifier/Director 摘要需要协议先补字段（当前 `StageState` 仅 index/seed/state/monsters_remaining）。
- 早期“纯色图元不上屏”根因是该 raylib 构建启用 `SUPPORT_CUSTOM_FRAME_CONTROL`：
  `EndDrawing()` 只提交绘制，需要显式 `SwapScreenBuffer()`（已在 main/probe 中调用）。
- 端到端验收（双客户端三关、断线恢复、Bot/指标）依赖服务器侧 D4–D9 路由与 D 的平台工作。
