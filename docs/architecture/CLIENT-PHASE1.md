# 角色 C：客户端与接入契约

状态（第二周 D4–D9 客户端侧已完成，A5 缺口硬化进行中）：开发分支 `feature/week2-client-hardening`（基于集成后的 main）。
战斗输入/事件消费、血条与死亡表现、奖励宝箱、断线恢复、预测/校正与插值均已实现并通过无头测试
（`ctest` 五套件；`odyssey_logic_tests` 233 checks、`odyssey_config_tests` 129 checks）。控件与运行方式见 [client/README.md](../../client/README.md)。
A5 硬化进度：C-a（Ready 门控）、C-b（阶段/存活/恢复输入门控）、C-d（tick 驱动预测 + 服务器移速）、C-e（恢复期匹配/续号/输入静默）、C-f（有界等待/二次闪断）、C-g（端点配置化）已完成；
剩 C-c（药水，阻塞于 A2/A3 协议字段）与 Token 轮换（待 A4）；其余见 [WEEK2-AD-FINALIZATION](../plans/WEEK2-AD-FINALIZATION.md) 的 C 缺口清单。
仍待：D7 的难度/Modifier/Director 摘要需要协议先补字段（当前 `StageState` 只有 index/seed/state/monsters_remaining）；
端到端验收（双客户端三关、断线恢复、Bot/指标）依赖服务器侧路由与 D 的平台工作。

依据：[第二周计划](../plans/WEEK2-DAYS4-10.md)、[前三天计划](../plans/PHASE1-DAYS1-3.md)、[总体架构](../../ARCHITECTURE.md)。

## 1. 已交付范围（客户端侧）

| 模块 | 位置 | 作用 |
| --- | --- | --- |
| Frame 编解码 | `client/src/core/Frame.{h,cpp}` | 16B 大端帧头编解码；body 超限在分配前拒绝 |
| 流式组帧 | `client/src/core/FramingReader.{h,cpp}` | TCP 字节流 → 完整帧；拆包/粘包；坏 Magic/Version/超限/EOF 截断分类 |
| 有界队列 | `client/src/core/BoundedQueue.h` | 线程安全有界队列：Push(丢最旧)/TryPush(拒绝)；Close 唤醒等待者 |
| 端点配置 | `client/src/core/ClientConfig.h` | 命令行/环境变量解析 + 校验（host/port），默认本机；无 raylib/asio/protobuf 依赖，可无头测试 |
| 装备显示表 | `scripts/generate-equipment/generate.ps1` → `client/assets/data/equipment.csv` | 从 B 的 `data/equipment/catalog.json` 生成客户端显示表（`-Check` 供 CI 判过期）；客户端运行期不解析 JSON |
| 网络线程 | `client/src/network/NetClient.{h,cpp}` | Asio TCP 客户端，独立 Network Thread；异步连接/读写；断连事件 |
| 消息 ID 适配 | `client/src/network/ProtocolIds.h` | A 的 `MessageType` 枚举 → 客户端 constexpr 常量（单一映射点） |
| 载荷编解码 | `client/src/network/PayloadCodec.h` | Ping/Pong/Login/Resume/Match/Input/Snapshot/战斗事件/奖励 ↔ POD 视图 |
| 事件/消息类型 | `client/src/network/NetMessage.h` | Network→Main 交接类型（状态变化/入站帧/出站丢弃） |
| 输入 | `client/src/input/InputSample.h`、`InputSampler.{h,cpp}` | WASD→移动意图、鼠标→瞄准、SPACE→射击、对角限长、30Hz InputSeq |
| 玩家/快照视图 | `client/src/sync/GameView.h` | 全量快照语义：缺失移除、closed 清空、self ack、HP/alive/属性 |
| 战斗视图 | `client/src/sync/CombatView.h` | 怪物全量集合、子弹仅由 Spawn/Destroy 事件增删、受击闪环/死亡标记 |
| 奖励视图 | `client/src/sync/RewardView.h` | 奖励选项/选择/超时/Applied 状态；本地静态装备显示表（由 `scripts/generate-equipment/generate.ps1` 从 B 的 `catalog.json` 生成，含 CSV 去引号） |
| 恢复状态机 | `client/src/sync/RecoveryState.h` | 有界退避重连、Resume 与全新登录决策、令牌失效处理 |
| 预测/插值 | `client/src/sync/Prediction.h`、`Interpolation.h` | tick 驱动的本地预测 + 服务器校正（每 30Hz 边界一步、按 tick 而非按包重放，采用快照移速与存活）；远端/怪物 10Hz 插值 |
| 会话/关卡门控 | `client/src/sync/SessionGate.h` | 纯谓词：StageState 与服务器 iota 对齐、可否发输入（需本会话首帧快照）、可否报 Ready（权威 `PreparingNextStage` + 自身奖励结清 + 每关一次）、InputSeq 下界 |
| 窗口/HUD | `client/src/main.cpp` | 连接/登录/匹配/战斗/奖励/恢复/网络统计 HUD；R 重试；ESC/关窗干净退出 |

构建与自检：

```powershell
pwsh -NoProfile -File scripts/generate-proto/generate.ps1
pwsh -NoProfile -File scripts/build/build.ps1 -Target client
ctest --test-dir build\client-windows -C Debug --output-on-failure
# 窗口：build\client-windows\client\odyssey_client.exe
```

## 2. 客户端与服务器之间的传输契约（C 侧实现口径）

| 项目 | 当前实现 / 约定 | 依赖 |
| --- | --- | --- |
| Transport | TCP 长连接 | 定稿 |
| Frame 头 | 16B、大端：Magic 2B `0x4E52` + Version 1B `1` + Flags 1B + MessageType 2B + Reserved 2B + BodyLength 4B + Sequence 4B | A 复核 |
| Body 上限 | 64 KiB，**解码端在读到长度后、分配前检查** | A 复核 |
| 连接端点 | 默认 `127.0.0.1:7777`；由 `--host`/`--port`/`--server`（命令行）或 `ODYSSEY_SERVER_HOST`/`ODYSSEY_SERVER_PORT`（环境变量）覆盖，优先级 命令行 > 环境变量 > 默认；非法值一律报错退出，不静默回退 | C 已实现（`core/ClientConfig.h`）；A/D 只需对齐同名环境变量 |
| 入站解码 | Network Thread 只产出 `NetEvent`（见 §4），payload 不在此层解析 | A 提供 proto |

Frame Sequence 与 Input Sequence 相互独立（架构 §8.1）。客户端不使用 Sequence 做可靠性判断。

## 3. 消息类型（已接入 A 的 v0 协议，非占位）

客户端消息 ID 统一来自 `common.proto::MessageType`（经 `ProtocolIds.h` 映射），不再使用占位值。
已接入：Ping/Pong、LoginRequest/Response、Disconnect、PlayerInput（编码）、WorldSnapshot（解码→GameView）。
Match/Stage/Reward 常量已声明，待 D 的 lobby 与后续阶段接入。

客户端**不自行增改消息字段**；A 更新 proto 后仅需重新生成（generate.ps1），必要时调整 `PayloadCodec` 映射。

## 4. Network Thread → Main Thread 交接契约（C 内部，供 A/D 评审）

事件在 Network Thread 上产生，投递进有界队列（Main 消费）。队列容量：收件 256（main.cpp），出站写队列 64（满时丢最旧帧并上报 `kOutboundDropped`）。

`NetEvent`：

```text
enum class NetEvent::Kind { kStateChanged, kMessage, kOutboundDropped };
ConnectionState { kIdle, kConnecting, kConnected, kDisconnected, kFailed }
NetMessage { message_type:u16, sequence:u32, payload:bytes }   // payload 不透明
```

红线（对齐计划 §8 与架构 §27）：

- Network Thread **禁止**直接修改客户端 GameWorld / 渲染状态；只向队列投事件。
- Main Thread 每帧 Drain 队列后应用状态；不存在跨线程共享的可变世界对象。
- 断连通知：EOF/连接重置 → `kDisconnected`；其余 IO/组帧错误 → `kFailed`（`detail` 含原因）。关闭窗口/ESC → `Stop()` 关闭 socket、join 线程后才退出进程。

## 5. 依赖与联调清单（请各 Owner 关注）

| # | 事项 | 状态 | 客户端需要的输入 |
| --- | --- | --- | --- |
| 5.1 | 协议 v0（proto + Message ID 表） | ✅ A 已在 `feature/network` 提供并接入；**未合入 develop** | 合入 develop 后同步回填 |
| 5.2 | PlayerInput/WorldSnapshot wire 字段 | ✅ 已按 A v0 实现编解码（PlayerInput `move`/`aim`/`shoot`；WorldSnapshot `self`+`players` 与 `last_processed_input`） | 真发/端到端待 Room 接入服务器 |
| 5.3 | 开发登录（dev token → Session/Player ID） | ✅ 客户端已实现登录流程（A 网络层支持 dev 登录） | 真实服务器就绪后联调 |
| 5.4 | 可联调的真实 Go Server + 地址/端口（及 D 的 lobby/匹配） | ⏳ A 网络层可独立起服；Room/匹配未接线 | 用于 D2/D3 验收（stub 只到 D1） |
| 5.5 | B 域语义（出生/速度/地图/30Hz/InputSeq） | ✅ GAME-CORE-PHASE1 已声明，客户端对齐 | 轴符号与 `move` 方向在联调前确认 |
| 5.6 | **Ready 屏障的服务端处理**（`MSG_NEXT_STAGE_REQUEST`） | ⏳ 全仓仅存在于 `server/internal/session/session.go` 的合法性表，**无任何 handler 消费**；`PreparingNextStage` 由服务器在奖励轮次 `Complete()` 后自行推进（`server/internal/game/rewards.go`） | C 已按 A5 把发送时机收敛到 `PreparingNextStage`；A 接线后才能做屏障端到端验收 |

## 6. 输入契约（C → 服务器）

- 频率 30Hz；一 Tick 一报；InputSeq（uint32，从 1 递增，0 保留）与 Frame Sequence 独立。
- **只发意图，不发坐标/速度/最终结果**。释放按键产生零向量（服务器停止）。
- 键盘映射：A/D → 服务器平面 x ∓、W/S → y（客户端 x/z 语义）；对角输入归一化到单位圆。
- 已按 A 的 `PlayerInput{input_seq, client_tick_ms, move(Vec2), aim, shoot}` 编码（`PayloadCodec::EncodePlayerInput`）。
- 真发门控（A5 C-a/C-b/C-e）：需「已登录 + 已入房 + 本会话已收到首帧权威快照 + 无恢复进行中 + 自己存活 + 权威 `stage.state == playing`」；不满足即静默（不消耗 InputSeq、不发包），恢复会话在首帧快照到达前完全静默。
- 门控翻转（进入静默）时丢弃已记住的方向意图，避免阶段切换/死亡/恢复后带着旧意图多走一步；`kStageStartedEvent` 同样清空意图。切关时对在途旧输入的服务器侧处理策略仍待 A 确认（客户端当前不重放、序号保持单调）。
- Ready 门控（A5 C-a）：只在权威 `stage.state == PreparingNextStage` 且自身奖励已结清（无待选项、无待确认选择）时报一次，按关卡 latch；提前按只产生可见的拦截原因，不发包。
- 恢复续号（A5 C-e）：Resume 成功后不重发 MatchRequest；恢复会话首帧快照把 InputSeq 抬到 `max(断线前最高已发, last_processed_input)` 之上，绝不重放旧区间。

## 7. 快照消费契约（服务器 → C）

客户端已按 A 的 `WorldSnapshot`（`server_tick`/`last_processed_input`(self ack)/`self`/`players`(其他玩家，升序)/`monsters`/`stage`）解码并应用到 `GameView`：

| WorldSnapshot 字段 | 客户端 `PlayerView` 映射 | 处理规则 |
| --- | --- | --- |
| server_tick | `server_tick` | 展示/调试 |
| last_processed_input | self 的 `last_processed_input_seq` | self ack（本 Tick 最新已应用意图，非逐包确认） |
| self + players | `players`（id 升序） | 全量替换：缺失实体移除；self 常驻 |
| position x/y | `x/z` | 客户端 x/z |
| velocity | `vx/vz` | 预留预测/插值 |
| monsters/stage | 暂未入视图 | 战斗阶段接入（含 HP/Stage 展示） |

客户端不做权威判定；伤害/碰撞等由服务端计算。

## 8. 联调测试中 C 的角色（对照计划清单）

- **T01/T02**：Frame 固定字节样例/拆包粘包证据（core 538 项含 100 帧合并、逐字节边界）。
- **T03**：非法帧策略由 A 主测；C 提供组帧错误分类供对齐。
- **T04–T07（D2）**：登录/未入房输入/重复匹配由 A+D 主测；客户端实现就绪，随 5.4 服务器联调执行。
- **T08/T11/T12（D3）**：双真实客户端同房互见、断线实体移除、Windows 构建——需 5.4 就绪。
- **D3 统一提交**：联调当天由 C 选定 develop 提交号，全组同一提交构建、生成协议。

## 9. 未决 / 风险记录

- 输入轴符号（W/S 在服务器平面上的正负）需在联调前与 A/B 定稿，防止方向镜像。
- PlayerInput 真发、Match/入房门控、双人绘制画面验收，依赖 5.4（Room 接线 + D lobby）与本机渲染环境修复。
- 预测/校正（A5 C-d）：tick 驱动。每 30Hz 边界恰好一步，步数按服务器 tick 时间线计算，**不按收发包数**；移速取自快照 `self.move_speed`（装备/属性加成即时生效），死亡时不推进。因此收包频率（30Hz/300Hz）不改变预测速度。
- 恢复等待（A5 C-f）全部有界：重连最多 5 次（退避 2s→10s），用尽后进入终止态 `kExhausted`（需按 R，不再无声重试）；`ResumeRequest`/`LoginRequest` 5s 无响应即超时 → 丢弃令牌回落全新登录，超时与连接失败共用同一预算；恢复成功后再次闪断会重置预算（可反复恢复）。Token 轮换需协议先给新 token 字段（A4）。
- 本机 raylib 动态库呈现问题（纯色图元不上屏）记录在验证文档，属环境待办，不影响逻辑/网络联调。
