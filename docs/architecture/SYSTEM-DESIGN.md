# The Return of the Odyssey — 系统设计说明书

> 版本：v1.0　|　对应代码基线：`main` @ `73248af`（Phase-one 集成完成）
> 文档性质：基于**已落地实现**的系统设计总览，与 [ARCHITECTURE.md](../../ARCHITECTURE.md)（设计约定）互为补充——本文讲「现在是什么」，ARCHITECTURE 讲「应该怎样」。
> 适用范围：服务端（Go）为主，客户端（C++20）、Bot（Go）、Dashboard（Vue）为辅。

---

## 1. 系统概述

The Return of the Odyssey 是一个**服务端权威（Server-Authoritative）**的多人 Roguelike 实时游戏实训项目。客户端只发送操作意图，所有权威状态（位置、生命、伤害、碰撞、关卡进度）由服务端计算，并通过快照与事件下发给客户端渲染。

**Phase-one 已实现的能力闭环**：

```
连接 → 开发登录 → 两人 FIFO 匹配 → 入房 → WASD/瞄准/射击输入
     → 30Hz 权威模拟 → 10Hz 个性化快照 + 可靠事件 → 双端显示
     → 断线清理 → 空房回收 → Prometheus 指标 / pprof
```

**关键技术指标**（源自集成验收记录，实测）：

| 指标 | 值 |
| --- | --- |
| 服务端模拟频率 | 30 Hz（Tick 间隔约 33.33ms） |
| 快照频率 | 10 Hz（每 3 Tick 一次完整快照） |
| 输入确认 | LastProcessedInputSeq，仅确认已应用的输入 |
| 10 Bot / 5 房 / 10 分钟 | 10/10 成功，Tick 频率 30.001Hz，p99 工作耗时 < 0.1ms |

---

## 2. 总体架构分层

系统严格遵循**三层分离**，禁止跨界（ARCHITECTURE §10）：

```
┌─────────────────────────────────────────────────────────────┐
│  表现层：C++ 客户端 / Go Bot / Vue Dashboard                 │
│  （发送意图，接收快照与事件；不产生权威状态）                │
└───────────────────────────┬─────────────────────────────────┘
                            │ TCP + 16 字节帧 + Protobuf
┌───────────────────────────▼─────────────────────────────────┐
│  A · 网络层 network：Server / Connection / 帧编解码           │
│  A · 会话层 session：状态机 / 合法性校验                      │
│  A · 转换层 convert：DTO ↔ Domain 双向映射（无业务逻辑）      │
│  A · 编排层 router：Join/Leave、快照/事件分发、关闭观察        │
└───────────────────────────┬─────────────────────────────────┘
                            │ Domain Command（非 protobuf）
┌───────────────────────────▼─────────────────────────────────┐
│  B · room：Room-per-goroutine，World 唯一写入者               │
│  B · game：World / combat / systems（确定性模拟）            │
│  D · lobby：进程内 FIFO 匹配                                  │
│  D · metrics / persistence：指标与 Redis 续接令牌             │
└─────────────────────────────────────────────────────────────┘
```

**核心分层约束**（代码中反复强调的硬边界）：

1. `game` 领域模型**不 import** protobuf / socket / database / goroutine。
2. `room` 不 import network，不执行任何网络 I/O。
3. `convert` 是**唯一**同时认识 protobuf DTO 和 game 领域的包，且只做字段映射。
4. `session` 只依赖 protocol，不依赖 room/game（房间绑定用原始 uint64 存 ID）。
5. protobuf 对象**绝不**被当作领域模型承载状态。

---

## 3. 模块职责与归属

| 模块 | 路径 | 职责 | 负责人 |
| --- | --- | --- | --- |
| 协议 | `proto/` | 唯一真相源；MessageType / 消息定义 | A（审核）+ C（复核） |
| 帧编解码 | `server/internal/network/frame.go` | 16 字节大端帧、粘包/拆包、超限校验 | A |
| TCP 服务 | `server/internal/network/server.go` | 监听、每连接双 goroutine、双发送队列 | A |
| 会话状态机 | `server/internal/session/` | 状态转换、消息合法性矩阵、房间绑定 | A |
| DTO 转换 | `server/internal/convert/` | Input / Snapshot / Event 双向映射 | A |
| 编排路由 | `server/internal/router/` | Join/Leave、快照/事件分发、关闭观察 | A |
| 房间与世界 | `server/internal/room/`、`game/` | 单写入者、固定 Tick、移动/战斗 | B |
| 匹配 | `server/internal/lobby/` | 进程内 FIFO、幂等、取消 | D |
| 指标 | `server/internal/metrics/` | Prometheus 采集与隔离 Registry | D |
| 持久化 | `server/internal/persistence/` | Redis 一次性续接令牌 | D |
| 装配入口 | `server/cmd/gameserver/` | application.go 串联全部模块 | A + D |
| 客户端 | `client/` | C++20 / raylib / Asio | C |
| Bot | `bot/` | 独立 Go module，复用协议 | D |

---

## 4. 核心数据流

### 4.1 登录

```
Client ──LoginRequest(101)──▶ Connection.Reader
  → routeMessage 判定 Accept（仅 Ping/Login 在未登录时合法）
  → idAllocator.next() 分配 sessionID/playerID（原子递增，Phase1 无持久化）
  → AssignIdentity + Transition(Lobby)
  → LoginResponse(102){session_id, player_id}
```

> 开发登录**不具备鉴权能力**：测试昵称直接换服务器分配的 ID；重连视为新会话（`ResumeToken` 暂为 nil）。

### 4.2 匹配与入房

```
Client ──MatchRequest(200)──▶ handleMatchRequest
  → Transition(Matching)（重复请求幂等 no-op）
  → matcher.Enqueue(key)（FIFO，凑满 2 人出组）
  → 成组后 go createMatch(players)  ← 不阻塞 Reader goroutine
      → room.Start(ctx, roomID, config)  启动 Room goroutine
      → 启动 snapshots/events/close 三个 dispatcher goroutine
      → 逐个 router.Join(sess, rm, roomID)  ← 等 nil 回执才切 InRoom
      → 订阅快照(snapshot)/事件(EventSink)/关闭(CloseWatcher)
      → MatchFound(201){room_id, room_token, teammates} 发给各端
```

**关键语义**：`room.Join` 是**双错误**——立即返回的 admission error（入队失败）与异步 `<-chan error` 回执（nil = 真正加入）。只有回执为 nil，`router.Join` 才 `BindRoom + Transition(InRoom)`。入房必须在**新的 goroutine** 中完成，因为它会阻塞等待房间 Tick 处理回执，不能占用连接 Reader。

### 4.3 输入 → 权威状态

```
Client ──PlayerInput(300)──▶ handlePlayerInput
  → convert.Input：float32 Vec2 → float64 entity.Vec2，nil move=停止，nil aim=无瞄准
  → 查 activeRoom，调 room.Input(sessionID, game.Input)
      → Input 入有界队列（InputCapacity=256，非阻塞）
      → Tick 时按 ControlsPerTick/InputsPerTick 批量消费
      → 校验 generation / InputTimeout(200ms) / Seq 单调
      → world.ApplyInput（只暂存意图，不移动）
      → world.Step（每 Tick 固定 dt 模拟一次，与发包频率无关）
```

**权威原则**：客户端只发意图（Move/Aim/Shoot），不发位置/伤害；位移由 `StepSeconds = 1/30` 固定步长决定，杜绝「发得越快走得越快」。

### 4.4 快照下行（Latest-Wins）

```
Room 每 3 Tick → publish(false) → Snapshot 入 updates 通道（容量 1，旧快照被驱逐）
  → SnapshotDispatcher.Run 消费（单消费者）
  → 对快照内每个玩家：convert.WorldSnapshot(snapshot, selfID)
      self 玩家 → Self + LastProcessedInput（输入确认）
      其他玩家 → Players（已按 ID 升序）
  → 编码帧 → Connection.SendSnapshot（Latest-Wins 槽，容量 1，新覆盖旧）
```

### 4.5 事件下行（可靠）

```
Room 每 Tick → TakeEvents() → EventBatch{Events, Overflow}
  → EventDispatcher.Run 消费（单消费者）
  → 对每个 Event：convert.Event(e) → MessageType + wire message
  → 编码帧 → 广播到所有订阅者（EventSink.Send，可靠队列）
```

**7 种领域事件 → wire 消息**：

| EventKind | 消息 | MessageType |
| --- | --- | --- |
| StageStarted | StageStartedEvent | 325 |
| ProjectileSpawned | ProjectileSpawnEvent | 320 |
| ProjectileDestroyed | ProjectileDestroyEvent | 321 |
| DamageDealt | DamageEvent | 322 |
| EntityDied | DeathEvent | 323 |
| StageCleared | StageClearedEvent | 326 |
| TeamDefeated | TeamDefeatedEvent | 327 |

### 4.6 断连与关闭

```
Connection 断开 → onClose → app.disconnected(conn)
  → matcher.Cancel / 移除 waiting / 取消三个 dispatcher 订阅
  → Transition(Disconnected)
  → go leaveRoom：router.Leave 重试（队列满则 10ms 后重试，直到 Done）

Room 关闭（requested / idle / event_backpressure）
  → finish()：drain controls → publish(Closed=true) → close 四个通道
  → CloseWatcher.Run 感知 Done → 读 CloseReason → 映射 ReasonCode
  → 广播 Disconnect 帧 + OnClose 回调（D 注销房间）
```

**CloseReason → ReasonCode 映射**：`requested`→310、`idle`→311、`event_backpressure`→312。

---

## 5. 协议设计

### 5.1 16 字节大端帧头

| 偏移 | 字段 | 大小 | 值 |
| ---: | --- | ---: | --- |
| 0 | Magic | 2 | `0x4E52` |
| 2 | Version | 1 | `1` |
| 3 | Flags | 1 | bit0=compressed, bit1=fragmented（均 reserved） |
| 4 | MessageType | 2 | 见 MessageType 表 |
| 6 | Reserved | 2 | 0 |
| 8 | BodyLength | 4 | payload 字节数，**≤ 64 KiB**（分配前校验） |
| 12 | Sequence | 4 | Frame Sequence，per-connection 单调 |

### 5.2 MessageType 区间

| 区间 | 模块 |
| --- | --- |
| 0–99 | System（Ping/Pong/Disconnect） |
| 100–199 | Login / Session |
| 200–299 | Lobby / Match |
| 300–399 | Game / Entity / Combat |
| 400–499 | Stage / Reward |
| 500–599 | Director / Metrics |

### 5.3 双序列号语义（关键区分）

- **Frame Sequence**（帧头 Sequence）：仅用于日志/统计/未来 UDP ACK。
- **Input Sequence**（PlayerInput.input_seq）：玩家输入预测序号，从 1 严格递增不回绕；服务器用它在 `LastProcessedInputSeq` 中确认「已在模拟中应用」的输入。

两者**互不相通**，见 [sequence.md](../protocol/sequence.md)。

### 5.4 版本与稳定性

- 新增字段 / 新 MessageType → 不升 Version；
- 删除字段用 `reserved`，**不复用**编号；
- 重命名 MessageType / 改字节序 → 升 Version。

---

## 6. 并发模型

### 6.1 核心模型：Connection-per-goroutine + Room-per-goroutine

```
每个 Connection：
  Reader goroutine —— 读帧 → 调 Handler（只校验+入队，禁止阻塞 I/O）
  Writer goroutine —— 消费双队列 → 写 socket（唯一写者）

每个 Room：
  1 个 run goroutine —— 唯一 World 写入者
  + 3 个 dispatcher goroutine（snapshots / events / close）
  + 1 个 observeTicks goroutine（D 指标采集）
```

### 6.2 Room Ownership（最重要约束）

> **Room 是其 Game World State 的唯一 Writer。**

Network goroutine 禁止直接改 Player/World；必须走 `Reader → Session → RoomCommand → Room Channel → Room Tick`。这消除了绝大部分 mutex 和 race。

### 6.3 Backpressure（背压）

Room Tick **绝不允许**被慢客户端网络 I/O 阻塞：

| 队列 | 语义 | 容量 | 满时行为 |
| --- | --- | --- | --- |
| 可靠队列 `out` | FIFO，每帧必须按序到达 | 256 | `Send` 返回 false → `closingSink` 关闭慢连接 |
| 快照槽 `snapshot` | Latest-Wins | 1 | 驱逐旧快照，替换新快照，永不阻塞 |
| Control 队列 | Join/Leave/StartStage | 64 | `ErrQueueFull`，调用方重试 |
| Input 队列 | 玩家输入意图 | 256 | `ErrQueueFull` |
| 事件队列 `events` | 可靠事件批 | 64 | **满 → 关闭房间 `event_backpressure`** |

---

## 7. 关键设计决策与权衡

### 7.1 快照 vs 事件的差异化投递

- **快照（10Hz 全量）**：走 Latest-Wins 槽。只关心最新状态，旧快照丢失可接受。
- **事件（战斗/关卡）**：走可靠 FIFO。丢失一次就错过一次伤害/死亡，**不可替换**。事件出口满不是静默丢弃，而是关闭房间并记录 `event_backpressure`——宁可关房也不假清场。

### 7.2 DTO ↔ Domain 分离（convert 包）

float32（wire）与 float64（domain）的转换是**显式**的：`vec2f` 收窄、`vec2` 拓宽。精度差异是协议固有属性（如 `0.6f → 0.6000000238418579`），非转换 bug，已在注释说明。领域层负责校验有限性与范围，convert 不越权。

### 7.3 房间 ID 的归属

`room.Room` **不暴露** `ID()`。`router.Join` 的 `roomID` 由调用方（application 的 nextRoomID）传入——这落实了「房间注册与分配归 D」的边界，避免 A 的编排层反向依赖 room 内部结构。

### 7.4 事件自带 stage_index（集成演进）

B 在集成时给 `game.Event` 补上了 `StageIndex` 字段，使 `convert.Event(e)` 不再需要外部传入 stage 上下文。这是一次**领域模型向协议靠拢**的修正，消除了事件分发对「可能过期的快照」的依赖。

### 7.5 指标隔离 Registry

`metrics.New()` 创建**独立** Prometheus Registry（含 Go/Process collector），避免多实例/测试中的重复注册。label 值使用封闭枚举（如 `ReconnectResult`），防止客户端控制值造成无界时间序列。

---

## 8. 可观测性与部署

### 8.1 三个 HTTP 端口

| 端口 | 用途 | 说明 |
| --- | --- | --- |
| 7777 | 游戏 TCP | 主监听 |
| 19091 | Prometheus `/metrics` | 9091 被 Windows 保留，故改用 19091 |
| 6060 | pprof `/debug/pprof/` | 仅本机访问 |
| 8080 | 管理（预留） | Phase1 未启用 |

### 8.2 核心指标（namespace `odyssey`）

| 指标 | 类型 | 含义 |
| --- | --- | --- |
| `online_players` | Gauge | 在线玩家数 |
| `active_rooms` | Gauge | 活跃房间数 |
| `match_queue_players` | Gauge | 匹配队列人数 |
| `matches_total` | Counter | 成局总数 |
| `match_duration_seconds` | Histogram | 匹配等待时长 |
| `room_tick_work_duration_seconds` | Histogram | Tick 工作耗时（不含等待） |
| `reconnect_attempts_total` | CounterVec | 重连尝试（封闭 label） |

### 8.3 配置加载（.env）

`config.Load` 三级优先级：**内置默认 → .env 文件 → 进程环境变量（最高）**。环境变量名 `ODYSSEY_*`，`Validate` 强制 `SnapshotHz ∈ (0, TickHz]`、`TickHz > 0`。这修复了原计划中「.env 是死文件」的缺口。

---

## 9. 验收现状与遗留项

### 9.1 已通过（源自 `docs/verification/phase1-integration/`）

- 协议生成、Server/Bot 全包 `test`+`vet`、**`-race` 全通过**（GCC 15.2）
- C++ 客户端构建 + 4/4 ctest 通过（MSVC 14.51 / CMake 3.31 / vcpkg）
- 两个真实 C++ 客户端同房：Login→MatchFound→快照互通
- 10 Bot / 5 房 / 10 分钟：30.001Hz，p99 < 0.1ms，结束后计数归零

### 9.2 尚未完成

| 遗留项 | 状态 | 说明 |
| --- | --- | --- |
| T08 人工 WASD 5 分钟录像 | 未执行 | 需 C 演示验收；输入/移动/移除路径已由 TCP 测试 + Bot 覆盖 |
| Docker 容器集成 | 未执行 | 守护进程未就绪；Redis/Prometheus/Grafana 真实抓取未跑 |
| `EntityRemovedEvent`(324) | 预留 | 未发出，等后续 despawn 需求 |
| 客户端预测/插值 | 未实现 | 10Hz 阶梯感留后续阶段 |
| Reward / NextStage | 预留枚举 | 未真正实现，Director 仅 Planner 接口 |

---

## 10. 目录速查

```
proto/                          # 唯一协议源（6 文件）
server/
  cmd/gameserver/               # application.go（装配）+ main.go（入口）
  generated/protocol/           # protoc 生成（gitignore，需脚本再生成）
  internal/
    network/                    # A：帧编解码 + TCP 服务 + 双队列
    session/                    # A：状态机 + 合法性矩阵 + 房间绑定
    convert/                    # A：DTO↔Domain 映射（input/snapshot/event）
    router/                     # A：Join/Leave + 快照/事件分发 + 关闭观察
    room/                       # B：Room 单写入者 + 固定 Tick
    game/                       # B：World/combat/systems/entity/stage/director
    lobby/                      # D：进程内 FIFO 匹配
    metrics/                    # D：Prometheus
    persistence/                # D：Redis 续接令牌
client/                         # C：C++20 客户端
bot/                            # D：独立 Go Bot 模块
dashboard/                      # C+D：Vue3/Vite/ECharts
deploy/                         # D：Docker/Prometheus/Grafana
docs/                           # 架构/协议/验证/会议
```

---

## 附：协议生成

```powershell
. ./scripts/env.ps1
pwsh -File scripts/generate-proto/generate.ps1
```

生成 Go（`server/generated/protocol/`）、C++（`client/generated/protocol/`）与 descriptor（`build/protocol.pb`）。脚本强制 `protoc 33.4` + `protoc-gen-go v1.36.12`，版本不符拒绝执行。
