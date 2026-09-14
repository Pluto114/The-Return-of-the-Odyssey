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

## 测试

```powershell
ctest --test-dir build\client-windows -C Debug --output-on-failure
```

四个无头套件：`odyssey_core_tests`（帧编解码/组帧/队列）、`odyssey_net_tests`（网络线程/断连/背压）、
`odyssey_logic_tests`（输入、视图、战斗、奖励、恢复、预测/插值）、`odyssey_protocol_tests`（协议载荷往返）。

## 运行（本地联调）

```powershell
# 终端 1：服务器（默认 127.0.0.1:7777）
. ./scripts/env.ps1
go run ./server/cmd/gameserver
# 终端 2：客户端
build\client-windows\client\odyssey_client.exe
```

当前客户端在 `client/src/main.cpp` 顶部将 `kServerHost` 写为 `10.22.31.251`、`kServerPort` 写为 `7777`。本机体验先将 Host 改为 `127.0.0.1` 并重新构建；跨主机体验填写服务端局域网地址，同时配置服务端监听和端口放行。可配置端点属于本轮待收口项，不能假设当前已有命令行参数。

当前主分支正式入口支持匹配和移动，但尚未启动首关；下列战斗/奖励/恢复操作需 A/D 接通入口后联调。完整剩余需求见 [A / D 收尾清单](../docs/plans/WEEK2-AD-FINALIZATION.md)。

## 操作

| 输入 | 作用 |
| --- | --- |
| `WASD` | 移动意图（30Hz 发送，只发意图不发坐标） |
| 鼠标 | 瞄准方向（相对自身权威位置归一化） |
| `SPACE` | 射击（按住持续开火，冷却由服务器决定） |
| `1` `2` `3` | 奖励宝箱选择（服务器校验合法性） |
| `ENTER` | Reward 状态下“准备下一关”（Ready 屏障归服务器） |
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
- D9 同步质量：本地预测 + 服务器校正（只重放未确认输入）、远端玩家/怪物 10Hz 插值

## 已知限制 / 依赖

- 装备显示以 `data/equipment/catalog.json` 为唯一手写数据源；CMake 配置阶段校验版本 1，并生成、复制 `equipment.tsv` 到客户端可执行文件旁。客户端不从显示表推导战斗效果。
- Ready 当前只在 Reward=3 发送，与 B 奖励完成后的 PreparingNextStage=4 不匹配；阶段输入门控、恢复后匹配门控/续号、药水与装备移速预测均按收尾清单修正，尚未宣称真实三关/恢复通过。
- 难度/Modifier/Director 摘要需要协议先补字段（当前 `StageState` 仅 index/seed/state/monsters_remaining）。
- 早期“纯色图元不上屏”根因是该 raylib 构建启用 `SUPPORT_CUSTOM_FRAME_CONTROL`：
  `EndDrawing()` 只提交绘制，需要显式 `SwapScreenBuffer()`（已在 main/probe 中调用）。
- 端到端验收（双客户端三关、断线恢复、Bot/指标）依赖服务器侧 D4–D9 路由与 D 的平台工作。
