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

本机若仍开着旧的 `build/client-windows` 客户端，不要将旧窗口当作已更新的新版本，也不要覆盖正在使用的 exe；请先关闭它们再使用下方独立中文联调目录。

中文联调推荐用单独目录构建：`cmake --preset client-windows -B build/client-zh`，然后 `cmake --build build/client-zh --target odyssey_client`。双击 `build/client-zh/client/odyssey_client.exe` 可识别“奥德赛归途 · 中文测试版”的窗口标题。客户端从 Windows 已安装的黑体加载显示所需的中文字形，不复制或分发系统字体；中文装备名称从 `client/assets/equipment.zh-CN.json` 按权威装备 ID 生成并放在 exe 同目录。

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
# 终端 2：中文客户端；另开一个终端再次执行以双开
build\client-zh\client\odyssey_client.exe
```

客户端默认连接 `127.0.0.1:7777`，无需修改源码。跨主机体验可在启动终端设置 `ODYSSEY_SERVER_HOST` 和 `ODYSSEY_SERVER_PORT` 环境变量（端口须为 1–65535）；同时配置服务端监听和端口放行。`client/.env.example` 是配置示例，不会自动加载。

当前功能分支正式入口已支持匹配、战斗、按玩家发放奖励、双人 Ready 和 Director 下一关，自动化 TCP 回归已走通第 1 关到第 2 关。两个真实客户端的三关流程、恢复和最终结算仍需联调，剩余需求见 [A / D 收尾清单](../docs/plans/WEEK2-AD-FINALIZATION.md)。

首关现有 8 只怪物，怪物会在两名存活玩家间分配追击目标。战场四处掩体会阻挡移动和子弹，怪物会绕行；客户端保留短暂弹道轨迹，因此近距离命中也能看到射击反馈。

## 操作

给实际游玩的同学看 [玩家操作指南](../docs/PLAYER-GUIDE.md)，其中包含失败后重开、断线和窗口无响应的处理方式。

| 输入 | 作用 |
| --- | --- |
| `WASD` | 移动意图（30Hz 发送，只发意图不发坐标） |
| 鼠标 | 瞄准方向（相对自身权威位置归一化） |
| `SPACE` | 射击（按住持续开火，冷却由服务器决定） |
| 点击奖励卡片，或主键盘/小键盘 `1` `2` `3` | 奖励宝箱选择（服务器校验合法性） |
| `ENTER` | Reward 状态下“准备下一关”（Ready 屏障归服务器） |
| `R` | 连接失败或断开后重新尝试连接，不能在团灭后重开本局 |
| 点击“重新开局”或按 `N` | 团灭后为当前两名在线队友创建新房间，重置战斗与血量；无需重启服务器 |
| `F3` | 显示或隐藏开发诊断面板；普通玩家无需打开 |
| `ESC` / 关闭按钮 | 停止网络线程并退出 |

Windows 玩家版默认以普通窗口程序启动，不再显示开发控制台。每个进程分别把事件诊断写入工作目录下的 `odyssey-client-<PID>.log` 和 `odyssey-client-error-<PID>.log`，因此两个客户端可以从同一目录启动。开发时若确实需要控制台，可在 CMake 配置阶段设置 `-DODYSSEY_CLIENT_CONSOLE=ON` 后重新构建。

窗口可以拖动边框或使用最大化按钮调整大小。界面会按 16:9 战术画布等比例缩放并居中，额外区域使用深色留边，鼠标瞄准坐标也会同步换算。

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
- Ready 在奖励已应用且阶段处于 Reward=3 或 PreparingNextStage=4 时发送，阶段外停止战斗输入。两名真实玩家的三关完整验收与恢复、结算仍待联调。
- 难度/Modifier/Director 摘要需要协议先补字段（当前 `StageState` 仅 index/seed/state/monsters_remaining）。
- 早期“纯色图元不上屏”根因是该 raylib 构建启用 `SUPPORT_CUSTOM_FRAME_CONTROL`：
  `EndDrawing()` 只提交绘制，需要显式 `SwapScreenBuffer()`（已在 main/probe 中调用）。
- 端到端验收（双客户端三关、断线恢复、Bot/指标）依赖服务器侧 D4–D9 路由与 D 的平台工作。
