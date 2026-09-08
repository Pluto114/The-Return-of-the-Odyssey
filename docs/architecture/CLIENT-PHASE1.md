# 角色 C：第一阶段客户端与接入契约

状态：D1 中**协议无关**部分已实现并在 `feature/client`（fork: xxhsir）验证通过（CTest 3/3：core 235 + net 21 + logic 37）。
本文描述客户端已落地的接口与边界，供 A（网络/协议）、B（游戏核心）、D（平台/联调）接入时对齐；跨团队 wire 协议未定稿前，凡涉及 A 的消息字段/ID 均为**占位**，见「5. 待定与 Owner」。

依据：[前三天计划](../plans/PHASE1-DAYS1-3.md)、[总体架构](../../ARCHITECTURE.md)、B 的 [核心交接文档](GAME-CORE-PHASE1.md)。

## 1. 本阶段已交付范围（客户端侧）

| 模块 | 位置 | 作用 |
| --- | --- | --- |
| Frame 编解码 | `client/src/core/Frame.{h,cpp}` | 16B 大端帧头编解码；body 超限在分配前拒绝 |
| 流式组帧 | `client/src/core/FramingReader.{h,cpp}` | TCP 字节流 → 完整帧；拆包/粘包；坏 Magic/Version/超限/EOF 截断分类 |
| 有界队列 | `client/src/core/BoundedQueue.h` | 线程安全有界队列：Push(丢最旧)/TryPush(拒绝)；Close 唤醒等待者 |
| 网络线程 | `client/src/network/NetClient.{h,cpp}` | Asio TCP 客户端，独立 Network Thread；异步连接/读写；断连事件 |
| 事件/消息类型 | `client/src/network/NetMessage.h` | Network→Main 交接类型（状态变化/入站帧/出站丢弃） |
| 输入 | `client/src/input/InputSample.h`、`InputSampler.{h,cpp}` | WASD→意图向量；对角限长；30Hz InputSeq 递增器 |
| 快照视图 | `client/src/sync/GameView.h` | 按 B 语义应用**全量快照**：缺失实体移除、closed 清空、self ack |
| 窗口/HUD | `client/src/main.cpp` | raylib 窗口；连接状态/入站消息/输入意图/视图计数显示；R 重试；ESC/关窗干净退出 |

构建与自检：

```powershell
pwsh -NoProfile -File scripts/generate-proto/generate.ps1
pwsh -NoProfile -File scripts/build/build.ps1 -Target client
ctest --test-dir build\client-windows -C Debug --output-on-failure
# 窗口（可选）：build\client-windows\client\odyssey_client.exe
```

## 2. 客户端与服务器之间的传输契约（C 侧实现口径）

| 项目 | 当前实现 / 约定 | 依赖 |
| --- | --- | --- |
| Transport | TCP 长连接 | 定稿 |
| Frame 头 | 16B、大端：Magic 2B `0x4E52` + Version 1B `1` + Flags 1B + MessageType 2B + Reserved 2B + BodyLength 4B + Sequence 4B | A 复核 |
| Body 上限 | 64 KiB，**解码端在读到长度后、分配前检查** | A 复核 |
| 连接端点 | 默认 `127.0.0.1:7777`（当前为 main.cpp 常量；地址进配置由 A/D 的 config 阶段统一） | A + D |
| 入站解码 | Network Thread 只产出 `NetEvent`（见 §4），payload 不在此层解析 | A 提供 proto |

Frame Sequence 与 Input Sequence 相互独立（架构 §8.1）。客户端不使用 Sequence 做可靠性判断。

## 3. 客户端希望服务器按下列语义提供消息（占位，A 提案后替换）

消息类型值沿用架构 §9 区间，**以下是客户端目前使用的占位**，等待 A 的最小消息集（A，C 复核）：

| 占位 | 值 | 用途（C 侧） |
| --- | --- | --- |
| Ping / Pong | 系统区 0-99 内占位 `1`/`2` | 连通性演示；真实 ID/样例由 A 定 |
| LoginRequest / LoginResponse | 待 A | D2 显示登录结果 |
| MatchRequest / MatchFound | 待 A | D2 获取 Room/Player ID |
| PlayerInput | 待 A | D2 起以 30Hz 发送意图（见 §6） |
| WorldSnapshot | 待 A | D2 起驱动 GameView（见 §7） |

客户端**不会**自行增改消息字段；A 发布生成源后 C 只做 DTO→客户端类型的映射适配。

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

## 5. 客户端当前尚未实现 / 依赖清单（请各 Owner 关注）

| # | 事项 | Owner | 客户端需要的输入 |
| --- | --- | --- | --- |
| 5.1 | 最小 proto 消息集 + Message ID 表 + Frame 十六进制固定样例 + 错误处理表 | A（C 复核） | 同一生成源；样例用于 T01/T02 真实验证 |
| 5.2 | `PlayerInput`、`WorldSnapshot` 的 wire 字段定义（须覆盖 B 的域快照字段与 InputSeq 语义） | A（B 确认语义） | 见 §6/§7 映射表 |
| 5.3 | 开发用登录（development 昵称 → Session/Player ID） | A | 客户端 Login 显示与后续匹配 |
| 5.4 | 可联调的真实 Go Server 实例与地址/端口 | A + B + D | D2 起接入真实服务器验收（stub 只允许到 D1） |
| 5.5 | B 的域语义确认（供自检样例）：出生 (10,10)、速度 5/s、地图 [0,20]²、30Hz/快照 10Hz、InputSeq 从 1 递增不回绕 | B（已在 GAME-CORE-PHASE1.md 声明） | 若协议字段名/单位与之不一致，请 A/B 修订后同步 |

> 5.4 的服务器在 D2 前就绪可让 C 提前做真实字节联调；D 提供的 Bot 用于 T09/T13，不阻塞 C 的 D1/D2 路径。

## 6. 输入契约（C → 服务器，D2 起生效）

- 频率 30Hz；一 Tick 一报；InputSeq（uint32，从 1 递增）与 Frame Sequence 独立。
- **只发意图，不发坐标/速度/最终结果**。释放按键即产生零向量（服务器据此停止）。
- 当前键盘映射（**占位，签名随 A 字段定稿**）：A/D → dx ∓、W/S → dz；对角输入归一化到单位圆（不允许斜向加速）。
- 客户端 `InputSequencer` 在「已连接且（将来）已入房」时每 30Hz 产出 `InputReport{seq, vector}`；实际发送在 PlayerInput wire 落地后由发送层接上（主循环预留）。

## 7. 快照消费契约（服务器 → C，D2 起生效）

客户端 `GameView` 按 B 的域快照语义实现，等待 A 的 WorldSnapshot DTO 适配：

| B 域快照字段 | 客户端 `PlayerView` 映射 | 处理规则 |
| --- | --- | --- |
| RoomID | `room_id:u32` | 展示 |
| ServerTick | `server_tick:u32` | 展示/调试 |
| Closed | `closed:bool` | closed=true → 清空视图 |
| Player.ID（升序） | `id:u64` | 视图按键排序；缺失实体从最新完整快照移除 |
| Position（服务器 x/y） | `x/z:float` | 客户端 x/z |
| Velocity | `vx/vz` | 预留预测/插值 |
| LastProcessedInputSeq | `last_processed_input_seq:u32` | self ack 显示（语义：本 Tick 使用的最新意图编号，非逐包确认） |

客户端不做权威判定；伤害/碰撞等由服务端计算。

## 8. D1 联调测试中 C 的角色（对照计划清单）

- **T01/T02**：C 提供/核对固定 Frame 字节样例与拆包粘包证据（已有 core 235 项含固定样例与逐字节边界）。
- **T03**：非法帧处理为 A 主测，C 提供组帧错误分类供对齐。
- **T07（D2）**：按键释放/停止发送 Input → 与 B 协作验证停止语义。
- **T08/T11/T12（D3）**：双真实客户端同房互见、断线实体移除、Windows 构建——需 5.4 服务器就绪后执行。
- **D3 统一提交**：联调当天由 C 选定 develop 提交号，全组同一提交构建、生成协议（计划 §7.5）。

## 9. 未决 / 风险记录

- 消息 ID 与 payload 全部为占位；A 定稿后 `main.cpp` 的 Ping 占位与发送层将替换为真实消息。
- 输入轴符号（W 对应 dz 正负）未与服务器对齐，D2 联调前必须定稿，否则方向可能镜像。
- 预测/校正/插值（渲染平滑）不在本阶段；10Hz 阶梯感不作为失败项。
