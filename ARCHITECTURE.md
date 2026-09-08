# The-Return-of-the-Odyssey
## 软件工程实训项目架构与开发约定

> 本文档用于项目初始化、仓库骨架搭建、多人协作和后续 Codex/本地开发工具的统一上下文。  
> 当前阶段只搭建目录、模块边界、协议边界和协作规范，不实现具体业务代码。

---

## 1. 项目定位

**The-Return-of-the-Odyssey** 是一个以就业为导向的软件工程实训项目。

项目表面形态是一个 **多人协作 Roguelike 实时战斗游戏**，但真正的技术核心是：

- Go 高并发实时游戏服务器
- Server Authoritative 服务端权威架构
- TCP 长连接与自定义二进制消息帧
- Protobuf 跨语言协议
- Fixed Tick 服务端实时模拟
- C++ 客户端预测、服务器校正和快照插值
- Room-per-Goroutine 并发模型
- 动态难度 AI Director
- Redis / MySQL
- Bot 压测
- Prometheus / Grafana / pprof
- Docker Compose
- 可观测性与性能优化

项目目标不是堆叠业务功能，而是在三周内做出一个：

> **小而完整、可压测、可解释、可答辩、可写进简历的实时多人游戏系统。**

---

# 2. 游戏玩法范围

## 2.1 基本形态

游戏采用固定地图的多人 Roguelike PvE 玩法。

单局流程：

1. 玩家进入房间
2. 开始当前关卡
3. 服务器生成若干怪物
4. 玩家移动、瞄准、射击
5. 怪物由服务器 AI 控制移动和攻击
6. 玩家击杀全部怪物
7. 服务器统计本关表现
8. 进入奖励阶段
9. 玩家选择装备/药水/圣遗物
10. AI Director 根据本关表现计算下一关难度
11. 玩家进入下一关
12. 重复循环

---

## 2.2 地图

为了控制工程量：

- 所有关卡使用同一张地图
- 地图尺寸固定
- 服务端只模拟二维平面坐标
- 客户端可以使用 3D 顶视角表现
- 服务端完全不关心 3D 渲染

逻辑坐标：

- Server：二维 `x / y`
- Client：映射成 OpenGL/raylib 世界中的 `x / z`

---

## 2.3 玩家输入

核心操作：

- WASD：移动
- 鼠标：瞄准方向
- Space：射击
- 可选按键：使用药水

客户端只发送 **Input**，不发送最终位置、最终伤害等权威结果。

---

# 3. 核心游戏对象

## 3.1 Player

玩家包含：

### 基础战斗属性

- Attack
- Defense
- MaxHealth
- Health
- MoveSpeed
- AttackSpeed

### 状态

- EntityID
- Position
- Velocity
- Aim
- Alive / Dead
- LastProcessedInputSeq

### 装备

三个逻辑槽位：

- Weapon
- Relic
- Potion

其中：

- Weapon：持续修改属性
- Relic：持续修改属性
- Potion：一次性消耗品

服务器保留 `BaseStats` 与 `CurrentStats` 的概念，避免装备、Buff、Debuff 直接污染基础属性。

---

## 3.2 Monster

所有怪物可以共用同一种基础外观。

每只怪物的差异来自配置：

- Attack
- Defense
- MaxHealth
- Health
- MoveSpeed
- AttackRange
- AttackCooldown

可通过客户端视觉做轻量区分：

- 体型
- 颜色
- 血条
- 移动速度

怪物无需复杂子类体系，采用数据驱动配置。

---

## 3.3 Projectile

子弹是服务器中的权威实体。

服务器负责：

- 生成
- 移动
- 碰撞
- 命中
- 伤害
- 销毁

客户端的子弹只是视觉表现。

客户端不得通过本地碰撞直接决定伤害。

---

## 3.4 Equipment

装备采用数据驱动设计。

装备类型：

- Weapon
- Relic
- Potion

装备效果统一表达为属性修改器：

- Target Stat
- Operation
- Value

支持：

- ADD
- MULTIPLY

例如：

- Attack +20
- Defense -5
- MaxHealth ×1.25

---

## 3.5 Stage

Stage 表示当前关卡配置。

包含：

- StageIndex
- MonsterCount
- MonsterConfig / MonsterArchetype
- GlobalModifiers
- DifficultyScore
- Seed
- StageState

Stage 不拥有复杂地图数据。

---

## 3.6 Global Modifier

全局 Buff/Debuff 可作用于：

- 全体玩家
- 全体怪物

本质仍复用统一属性修改器系统。

示例：

- Player Attack +20%
- Player Defense +15%
- Monster HP +30%
- Monster MoveSpeed +20%
- Player Attack +50% / Defense -30%

---

# 4. AI 设计

## 4.1 Monster AI

怪物 AI 不使用大模型。

采用简单 FSM：

- Idle
- Chase
- Attack
- Dead

核心逻辑：

1. 选择最近的存活玩家
2. 距离过远时追击
3. 进入攻击距离后攻击
4. 根据 Cooldown 决定攻击频率
5. 玩家死亡后切换目标

AI Decision 可以低于 Simulation Tick 频率，例如：

- Server Simulation：30Hz
- Monster AI Decision：10Hz

怪物移动仍保持 30Hz。

---

## 4.2 Adaptive AI Director

AI Director 是项目的特色模块。

它不直接操纵 World，而是：

**输入 PerformanceMetrics，输出 StagePlan。**

输入指标包括：

- ClearTime
- TeamHPPercent
- AverageDPS
- DeathCount
- DamageTaken
- EquipmentPower

输出包括：

- DifficultyScore
- MonsterCount
- MonsterStats
- GlobalModifiers
- StageSeed

第一版使用 **Rule-Based Director**，强调：

- 可解释
- 可测试
- 可复现
- 可调参

难度变化需要限制每关变化幅度，避免剧烈震荡。

示例：

- 每关最大提升约 20%
- 每关最大下降约 15%

Director 不直接：

- SpawnMonster
- 修改 Player
- 修改 World

而是：

PerformanceMetrics  
→ Director.Generate  
→ StagePlan  
→ StageSystem.Apply  
→ World Spawn

---

# 5. 总体技术栈

## 5.1 服务端

- Go
- Go 标准库 `net`
- Goroutine
- Channel
- 自定义 TCP Frame
- Protobuf
- MySQL
- Redis
- `database/sql` + 可选 sqlx
- go-redis
- log/slog 或 Zap
- Prometheus
- Grafana
- pprof
- go test / benchmark / race detector
- Docker Compose
- Linux 部署

---

## 5.2 客户端

优先方案：

- C++20
- raylib
- Standalone Asio
- Protobuf C++
- Dear ImGui
- CMake
- vcpkg

原则：

> raylib 只负责窗口、输入、资源、音频和渲染基础设施，网络同步等核心技术必须自己实现。



---

## 5.3 管理后台

- Vue 3
- Vite
- ECharts
- WebSocket / HTTP

用于展示：

- Online Players
- Active Rooms
- Entity Count
- Tick Duration
- Network Throughput
- AI Duration
- Collision Duration
- Snapshot Size
- Director Decision
- Room Detail

---

## 5.4 压测

单独开发 Go Bot Client。

Bot 不渲染，只执行：

- Connect
- Login
- Match
- Move
- Aim
- Shoot
- Reward
- Next Stage

目标支持：

- 100
- 500
- 1000
- 5000

等规模的并发模拟。

---

# 6. 明确不采用的技术

第一版不使用：

- Unity
- Unreal Engine
- 复杂 Godot GDExtension
- 游戏服务器框架
- 微服务
- Kubernetes
- Kafka
- RocketMQ
- 战斗链路 gRPC
- 第三方 Multiplayer Replication
- 复杂 ECS
- 第一阶段 UDP

原因：

项目核心能力必须由我们自己实现，而不是交给框架。

---

# 7. 网络架构原则

客户端与服务器采用：

> **TCP 长连接 + 16-byte 自定义消息头 + Protobuf Payload**

第一阶段全部使用 TCP。

未来如果需要升级：

- Reliable：TCP
- Realtime：UDP

但游戏层不能依赖具体 Transport。

---

# 8. TCP Frame

固定 16 Byte Header：

| 字段 | 大小 |
|---|---:|
| Magic | 2 Bytes |
| Version | 1 Byte |
| Flags | 1 Byte |
| MessageType | 2 Bytes |
| Reserved | 2 Bytes |
| BodyLength | 4 Bytes |
| Sequence | 4 Bytes |

统一使用 Network Byte Order / Big Endian。

建议：

- Magic：`0x4E52`
- Version：1

Frame：

Header  
+  
Protobuf Payload

---

## 8.1 Sequence 规则

必须区分两种 Sequence：

### Frame Sequence

属于 TCP Frame Header。

用途：

- 日志
- 调试
- 网络统计
- 为未来 UDP ACK 预留

### Input Sequence

属于 PlayerInput。

用途：

- Client Prediction
- Server Reconciliation

两者不能混用。

---

# 9. Message ID 区间

建议：

| 区间 | 类型 |
|---|---|
| 0-99 | System |
| 100-199 | Login / Session |
| 200-299 | Lobby / Match |
| 300-399 | Game |
| 400-499 | Stage / Reward |

建议消息：

### System

- Ping
- Pong

### Login / Session

- LoginRequest
- LoginResponse
- ResumeRequest
- ResumeResponse

### Lobby

- MatchRequest
- MatchFound

### Game

- PlayerInput
- WorldSnapshot
- ProjectileSpawnEvent
- ProjectileDestroyEvent
- DamageEvent
- DeathEvent
- EntityRemovedEvent

### Stage / Reward

- StageStarted
- StageCleared
- RewardOptions
- RewardChoice
- RewardApplied
- NextStageRequest

---

# 10. 数据模型分层

这是项目的重要架构约束。

必须严格区分：

## Domain Model

服务器内部真实游戏对象。

例如：

- Player
- Monster
- Projectile
- Room
- Stage

---

## Network DTO

客户端与服务器之间的数据。

例如：

- PlayerSnapshot
- PlayerInput
- MonsterSnapshot
- DamageEvent

---

## Domain Command / Event

服务器内部模块之间的数据。

例如：

- PlayerInputCommand
- DamageRequest
- DamageResolved

---

## 禁止事项

禁止直接把 Protobuf 对象当成服务器游戏对象。

禁止：

- Room 直接存 protobuf PlayerSnapshot
- Network goroutine 直接修改 Player
- Render Thread 直接操作 Network Thread 数据

---

# 11. 静态数据与动态数据

网络传输遵循：

> **静态数据传 ID，动态数据传状态。**

例如：

不要每次 Snapshot 都发送完整装备定义。

只发送：

- weapon_id
- relic_id
- potion_id

客户端通过本地静态 DataTable 映射：

ItemID  
→ Name  
→ Description  
→ Display Data

同理，Monster 使用 archetype_id。

---

# 12. Server Authoritative 原则

客户端只允许发送意图：

- Move
- Aim
- Shoot
- UsePotion
- RewardChoice
- NextStageRequest

客户端不得发送权威结果：

- 自己的最终 Position
- 自己造成的最终 Damage
- Monster HP
- 是否命中
- 下一关 Difficulty
- 任意装备 ID 作弊选择

所有最终状态必须由服务器计算。

---

# 13. PlayerInput

PlayerInput 至少包括：

- InputSeq
- ClientTick
- MoveX
- MoveY
- Aim
- Shoot
- UsePotion

服务端必须再次验证：

- Move Vector 长度
- Attack Cooldown
- Potion 是否存在
- Session 是否处于合法状态

---

# 14. Snapshot

Server Simulation：

- 30Hz

World Snapshot：

- 10Hz

客户端 Render：

- 60/120/144Hz

必须解耦。

---

## 14.1 Self Snapshot

自己的 Snapshot 需要包含：

- Position
- Velocity
- Aim
- HP
- State
- Weapon
- CurrentStats
- EquipmentState
- LastProcessedInputSeq

用于：

- HUD
- Prediction
- Reconciliation

---

## 14.2 Other Player Snapshot

其他玩家只发送必要状态：

- EntityID
- Position
- Velocity
- Aim
- HP
- MaxHP
- State
- WeaponID

---

## 14.3 Monster Snapshot

包含：

- EntityID
- ArchetypeID
- Position
- Velocity
- HP
- MaxHP
- State

不发送 AI 内部状态：

- TargetID
- AttackCooldown
- DecisionTimer

---

# 15. State 与 Event

必须区分。

## State

描述：

> 世界当前是什么样。

例如：

- Position
- Velocity
- HP
- State

如果丢失一次，下一次 Snapshot 可以恢复。

---

## Event

描述：

> 刚刚发生了什么。

例如：

- Shoot
- Damage
- Death
- StageClear
- Reward

Event 用于及时视觉表现。

原则：

> Event 提供视觉及时性，Snapshot 提供最终一致性。

---

# 16. Projectile 网络策略

第一版不把 Projectile 放入每次 WorldSnapshot。

服务器发送：

- ProjectileSpawnEvent
- ProjectileDestroyEvent
- DamageEvent

客户端创建 Visual Projectile。

真实碰撞与伤害仍由服务器内部 Projectile 负责。

---

# 17. 客户端预测

Local Player：

按键后：

1. 生成 InputSeq
2. 保存 PendingInput
3. 本地立即模拟
4. 发送服务器

服务器处理后：

Snapshot 返回 LastProcessedInputSeq。

客户端：

1. 将 authoritative position 作为基准
2. 删除已确认 Input
3. 重放尚未确认 Input
4. 得到新的 predicted position

即：

> Client Prediction + Server Reconciliation

---

# 18. 远端实体插值

其他玩家和怪物：

- 不进行本地权威模拟
- 保存多个 Snapshot
- Render Time 刻意落后 Server 一小段时间
- 在相邻 Snapshot 之间插值

即：

> Snapshot Interpolation

第一版不做复杂 Extrapolation。

---

# 19. Go 服务端并发模型

核心模型：

> **Connection-per-goroutine + Room-per-goroutine**

每个客户端 Connection：

- Reader Goroutine
- Writer Goroutine

每个 Room：

- 独立 Goroutine

---

# 20. Room Ownership

这是服务端最重要的并发约束。

> **Room 是其 Game World State 的唯一 Writer。**

以下对象只能被对应 Room goroutine 修改：

- Players
- Monsters
- Projectiles
- Stage

Network goroutine 禁止直接修改 Player。

必须走：

Connection Reader  
→ Decode  
→ Session  
→ RoomCommand  
→ Room Command Channel  
→ Room Tick

这样减少：

- Mutex
- Lock Contention
- Race Condition

---

# 21. Room Tick

Fixed Tick：

- 30Hz
- 每 Tick 约 33.33ms

建议阶段：

1. Consume Commands
2. Update Player Movement
3. Update Monster AI
4. Update Monster Movement
5. Update Projectiles
6. Collision Detection
7. Damage Resolution
8. Death / Cleanup
9. Stage Logic
10. Replication

各阶段后续需要独立埋点统计耗时。

---

# 22. Damage Pipeline

不要让 Collision System 直接操作 HP。

建议职责：

Collision  
→ DamageRequest  
→ CombatSystem  
→ DamageResolved  
→ HP Update  
→ DamageEvent

即：

- Collision：判断是否碰撞
- Combat：计算最终伤害
- Snapshot：最终权威状态
- Event：视觉表现

---

# 23. Damage Formula

第一版使用简单稳定公式：

`Damage = Attack × 100 / (100 + Defense)`

避免出现负伤害。

---

# 24. Stage 生命周期

Room State 至少包含：

- Waiting
- Playing
- StageClear
- Reward
- PreparingNextStage
- Closed

流程：

Playing  
→ Monsters == 0  
→ StageClear  
→ Reward  
→ Reward Complete  
→ NextStage  
→ Director.Generate  
→ StageStarted  
→ Playing

---

# 25. Session 状态

建议：

- Connected
- Lobby
- Matching
- InRoom
- Reward
- Disconnected
- Closed

服务器必须根据 Session State 验证消息合法性。

例如：

Lobby 状态收到 PlayerInput 应直接拒绝。

---

# 26. Backpressure

Room Tick 不允许被慢客户端网络 IO 阻塞。

游戏逻辑不能直接调用：

Socket Write

必须：

Room  
→ Session Queue  
→ Writer Goroutine  
→ Socket

---

## 26.1 Reliable Queue

适合：

- StageStarted
- RewardOptions
- DamageEvent
- DeathEvent
- LoginResponse

按顺序发送。

---

## 26.2 Snapshot Queue

Snapshot 使用：

> Latest State Wins

如果旧 Snapshot 尚未发送，新 Snapshot 到达：

- 丢弃旧 Snapshot
- 保留最新 Snapshot

Snapshot Queue 建议容量非常小，甚至为 1。

---

# 27. 客户端线程模型

至少分：

## Main / Render Thread

负责：

- Input
- Game Logic
- Snapshot Apply
- Prediction
- Interpolation
- Render
- UI

## Network Thread

负责：

- Socket
- Frame Decode
- Protobuf Decode
- Message Queue

Network Thread 禁止直接修改 GameWorld。

必须：

Network Thread  
→ Thread-safe Queue  
→ Main Thread

---

# 28. 数据持久化

## MySQL

用于：

- User
- GameSession / MatchHistory
- PlayerProgress
- EquipmentOwnership
- GameResult

不允许把实时 Tick 状态写入 MySQL。

比赛结果等非实时数据应异步写入。

---

## Redis

用于：

- Online Session
- Reconnect Token
- Match Queue
- Leaderboard
- Room Routing

Redis 必须有真实业务用途，不能为“技术栈”强行加入。

---

# 29. 重连

断线后：

1. Connection Lost
2. Session Detached
3. Room 暂时保留 Player
4. Redis / Server 保留 Resume Token
5. 客户端重新连接
6. ResumeRequest
7. Validate Token
8. Bind New Connection
9. 服务器发送 Full Snapshot
10. 客户端恢复世界

第一版可以设置短暂重连窗口。

---

# 30. 可观测性

必须接入：

- Prometheus
- Grafana
- pprof

核心 Metrics：

- online_players
- active_rooms
- entity_count
- monster_count
- tick_duration
- ai_duration
- collision_duration
- combat_duration
- snapshot_build_duration
- snapshot_bytes
- network_in_bytes
- network_out_bytes
- connection_total
- disconnect_total
- goroutine_count

---

# 31. 性能优化原则

不提前优化。

流程必须是：

1. Bot 压测
2. Prometheus / pprof
3. 找到真实瓶颈
4. 修改
5. Before / After Benchmark
6. 记录结果

例如：

Naive Collision  
→ pprof 发现 Collision 占 40% CPU  
→ Uniform Spatial Grid  
→ 再次测试降到 8%

这种数据要保留用于：

- README
- 报告
- 答辩
- 简历

---

# 32. Repository Structure

仓库建议如下：

```text
The-Return-of-the-Odyssey/
│
├── README.md
├── ARCHITECTURE.md
├── .gitignore
├── .editorconfig
├── docker-compose.yml
│
├── docs/
│   ├── architecture/
│   ├── protocol/
│   ├── benchmark/
│   ├── diagrams/
│   └── meeting/
│
├── proto/
│   ├── common.proto
│   ├── system.proto
│   ├── session.proto
│   ├── lobby.proto
│   ├── game.proto
│   └── stage.proto
│
├── server/
│   ├── cmd/
│   │   └── gameserver/
│   │       └── .gitkeep
│   │
│   ├── internal/
│   │   ├── network/
│   │   ├── session/
│   │   ├── lobby/
│   │   ├── room/
│   │   ├── game/
│   │   │   ├── entity/
│   │   │   ├── systems/
│   │   │   ├── stage/
│   │   │   └── director/
│   │   ├── persistence/
│   │   ├── metrics/
│   │   └── config/
│   │
│   ├── generated/
│   │   └── protocol/
│   │
│   ├── configs/
│   ├── migrations/
│   └── tests/
│
├── client/
│   ├── src/
│   │   ├── network/
│   │   ├── game/
│   │   ├── sync/
│   │   ├── render/
│   │   ├── input/
│   │   ├── ui/
│   │   └── core/
│   │
│   ├── assets/
│   │   ├── models/
│   │   ├── textures/
│   │   ├── audio/
│   │   └── data/
│   │
│   ├── generated/
│   │   └── protocol/
│   │
│   └── tests/
│
├── bot/
│   ├── cmd/
│   ├── internal/
│   │   ├── network/
│   │   ├── behavior/
│   │   └── metrics/
│   └── configs/
│
├── dashboard/
│   ├── src/
│   └── public/
│
├── deploy/
│   ├── docker/
│   ├── prometheus/
│   ├── grafana/
│   └── scripts/
│
├── data/
│   ├── equipment/
│   ├── monsters/
│   └── modifiers/
│
└── scripts/
    ├── generate-proto/
    ├── build/
    ├── test/
    └── benchmark/
```

Codex 当前阶段只需要创建上述目录与必要空文件/占位文件，不实现业务逻辑。

---

# 33. proto Ownership

`proto/` 是 Go Server 与 C++ Client 的唯一协议真相源。

生成代码分别输出：

- server/generated/protocol/
- client/generated/protocol/

禁止：

- Go 和 C++ 各自维护一套重复消息定义
- 手写两份协议结构体
- 客户端私自改变字段语义

---

# 34. Protocol Versioning

Header 包含 Version。

一般新增 Proto 字段：

- 不升级主协议版本
- 保持向后兼容

Breaking Change 才升级 Version。

Proto 字段编号：

- 一旦使用不可重新用于不同语义
- 删除字段后使用 `reserved`

---

# 35. 四人分工

项目不采用：

- 前端
- 后端
- 数据库
- 测试

这种低效分工。

每个人必须拥有一个核心技术子系统。

---

## 成员 A：Realtime Network & Protocol Owner

主要目录：

- proto/
- server/internal/network/
- server/internal/session/

核心职责：

- TCP Server
- Connection Lifecycle
- Reader / Writer Goroutine
- 16-byte Frame
- 粘包/拆包
- Protobuf
- Message Router
- Heartbeat
- Session
- Reliable Queue
- Snapshot Queue
- Backpressure

横向职责：

- Protocol Owner
- 所有 proto 变更审核

技术卖点：

- 高并发长连接
- 自定义协议
- 应用层 Backpressure
- 异步收发

Reviewer：

- C

---

## 成员 B：Game Server Core Owner

主要目录：

- server/internal/room/
- server/internal/game/
- server/internal/game/entity/
- server/internal/game/systems/
- server/internal/game/stage/
- server/internal/game/director/

核心职责：

- Room
- Room-per-Goroutine
- Fixed Tick
- Player
- Monster
- Projectile
- Movement
- Monster FSM
- Collision
- Combat
- Stage
- AI Director

横向职责：

- Game Logic Test Owner
- go test 规范

技术卖点：

- Server Authoritative
- Fixed Tick
- Room Ownership
- Lock Contention Reduction
- AI / Collision / Combat

Reviewer：

- D

---

## 成员 C：C++ Client & Realtime Sync Owner

主要目录：

- client/

核心职责：

- raylib / Existing OpenGL Client
- Asio TCP Client
- Frame Codec
- Protobuf
- Network Thread
- Main Thread Message Queue
- Input
- Client Prediction
- Server Reconciliation
- Snapshot Buffer
- Entity Interpolation
- Game Rendering
- HUD
- Reward UI
- Debug Overlay

横向职责：

- Integration Owner
- 每日客户端-服务端联调

技术卖点：

- C++ 实时客户端
- Prediction
- Reconciliation
- Snapshot Interpolation
- Thread Isolation

Reviewer：

- A

---

## 成员 D：Scalability / Performance / Reliability Owner

主要目录：

- bot/
- server/internal/persistence/
- server/internal/metrics/
- matchmaking / reconnect 相关目录
- deploy/
- dashboard/ 部分

核心职责：

- Matchmaking
- Redis
- MySQL
- Reconnect
- Resume Session
- Bot Load Generator
- Prometheus
- Grafana
- pprof
- Benchmark
- Docker Compose
- CI
- Performance Analysis

横向职责：

- Performance Owner
- Deployment Owner

技术卖点：

- 千级/多千级 Bot 压测
- 可观测性
- 性能分析
- Redis Session
- Reconnect
- Before/After 优化

Reviewer：

- B

---

# 36. AI Director 为全组共享特色模块

B 主实现。

但四人均参与：

### A

负责：

- Director 相关协议
- Stage Event

### B

负责：

- Director Algorithm
- Stage Plan
- Server Integration

### C

负责：

- Director Decision UI
- Difficulty Presentation

### D

负责：

- PerformanceMetrics
- Dashboard / Benchmark 展示

所有人都必须能解释 AI Director。

---

# 37. Git Ownership

建议：

| 路径 | Primary Owner |
|---|---|
| proto/ | A |
| server/internal/network/ | A |
| server/internal/session/ | A + D |
| server/internal/room/ | B |
| server/internal/game/ | B |
| server/internal/game/director/ | B |
| client/ | C |
| bot/ | D |
| server/internal/persistence/ | D |
| server/internal/metrics/ | D |
| deploy/ | D |
| dashboard/ | C + D |
| docs/ | ALL |

---

# 38. Git Workflow

长期分支：

- main
- develop

短期功能分支：

- feature/network
- feature/game-core
- feature/client
- feature/platform

原则：

- 不允许三周后一次性合并
- 每天至少一次 Integration Build
- 核心模块 Merge Request / Pull Request 需要 Reviewer
- 协议变更必须同步全组

---

# 39. 三周开发阶段

## Week 1：最小实时闭环

目标：

> 两个客户端能够通过服务器权威状态完成多人移动。

### A

完成：

- TCP
- Frame
- Protobuf
- Login
- Ping/Pong
- Session

### B

完成：

- Room
- 30Hz Tick
- Player
- Movement

### C

完成：

- Client
- Asio
- Frame
- Input
- 简单渲染

### D

完成：

- Redis/MySQL 基础环境
- Match Skeleton
- Bot Skeleton
- Prometheus Skeleton

Week 1 关键验收：

Client  
→ WASD  
→ TCP  
→ Go Server  
→ Room Tick  
→ Server Position  
→ Snapshot  
→ Client Render

---

## Week 2：完整游戏循环

目标：

> 完整 Roguelike Loop 可玩。

### A

- Async Writer
- Backpressure
- Snapshot Queue
- Events
- Heartbeat

### B

- Monster AI
- Projectile
- Collision
- Combat
- Stage
- Director

### C

- Prediction
- Reconciliation
- Interpolation
- HUD
- Reward UI

### D

- Bot 100 → 1000
- Reconnect
- Matchmaking
- Redis
- Metrics
- pprof

Week 2 关键验收：

Fight  
→ Clear  
→ Reward  
→ Director  
→ Next Stage

---

## Week 3：性能与答辩

禁止增加大型新功能。

重点：

- Profiling
- Benchmark
- Stability
- Presentation
- Bug Fix
- Documentation

### A

- Network Profiling
- Queue Optimization
- Protocol Statistics

### B

- AI Profiling
- Collision Optimization
- Director Tuning

### C

- Visual Polish
- Debug Overlay
- Animation / Particle
- Demo Stability

### D

- 1000 / 5000 Bot
- Grafana
- pprof
- Benchmark Report
- Docker
- Deployment

全员：

- Test
- Bugfix
- README
- Software Engineering Report
- PPT
- Demo Script

---

# 40. 全组共同 KPI

## Week 1

- 2 Clients
- 同时移动
- Server Authoritative
- Snapshot 可用

## Week 2

- 完整 Rogue Loop
- AI Monster
- Combat
- Reward
- Director
- Prediction / Interpolation

## Week 3

- 1000+ Bot
- Stable Tick
- Grafana
- pprof
- Benchmark
- Live Demo

---

# 41. 质量原则

## 不追求功能数量

目标不是：

- 商城
- 好友
- 公会
- 技能树
- 背包
- NPC
- 多地图
- 大量怪物皮肤

目标是：

- 网络正确
- 同步稳定
- 并发可测
- 性能可解释
- 架构清晰
- 答辩可演示

---

# 42. 性能优先级

后续优化必须由数据驱动。

优先观察：

1. Tick Duration
2. Collision
3. Monster AI
4. Snapshot Build
5. Network Queue
6. GC / Allocation
7. Goroutine Count
8. Redis / MySQL IO

---

# 43. 答辩理想演示流程

建议最终 Demo：

1. 打开 Grafana / Admin Dashboard
2. 显示当前在线人数与房间
3. 启动 2-4 个真实客户端
4. 玩家移动、射击、击杀怪物
5. 展示 Prediction / Interpolation Debug Overlay
6. 清关
7. 展示 Stage Statistics
8. 展示 Director Decision
9. 进入下一关，难度变化
10. 启动 Bot
11. 在线人数上涨
12. Grafana 显示 Tick / CPU / Network 变化
13. 展示服务器仍可稳定运行
14. 可选演示断线重连
15. 可选展示一次 pprof 优化前后数据

---

# 44. README 后续必须包含

正式完成后 README 至少包含：

- Project Overview
- Architecture Diagram
- Tech Stack
- Gameplay Loop
- Network Protocol
- Concurrency Model
- Server Authoritative
- Prediction / Reconciliation
- Snapshot Interpolation
- AI Director
- Persistence
- Observability
- Benchmark Environment
- Benchmark Results
- Profiling Before / After
- How To Run
- Repository Structure
- Team Contribution

---

# 45. 当前 Codex 任务

本地 Codex 当前只执行 **仓库初始化**。

需要：

1. 按本文档创建目录
2. 创建必要占位文件
3. 创建基础 README / ARCHITECTURE 文件位置
4. 创建 `.gitignore`
5. 创建 `.editorconfig`
6. 创建 Docker / proto / server / client / bot / dashboard / docs 等骨架
7. 不实现游戏业务逻辑
8. 不自行引入未讨论的大型框架
9. 不修改本架构的核心模块边界
10. 如果必须做额外技术决策，优先留下 TODO 而不是自行扩张范围

---

# 46. 当前冻结的核心架构

最终浓缩为：

```text
C++ Client
    │
    │ PlayerInput 30Hz
    ▼
TCP + 16B Frame + Protobuf
    │
    ▼
Go Network Layer
    │
    ▼
Session
    │
    ▼
RoomCommand Channel
    │
    ▼
Room-per-Goroutine
    │
    ├─ Player
    ├─ Monster AI
    ├─ Projectile
    ├─ Collision
    ├─ Combat
    ├─ Stage
    └─ Director
    │
    ▼
Snapshot 10Hz + Events
    │
    ▼
Session Writer
    │
    ▼
TCP
    │
    ▼
C++ Client
    │
    ├─ Local Player Prediction
    ├─ Server Reconciliation
    ├─ Remote Entity Interpolation
    └─ Render
```

横向平台：

```text
Go Bot
Redis
MySQL
Prometheus
Grafana
pprof
Vue Dashboard
Docker Compose
```

---

# 47. 项目核心一句话

> **The-Return-of-the-Odyssey 是一个基于 Go Server-Authoritative 架构的多人实时 Roguelike 游戏系统，通过自定义 TCP/Protobuf 协议、Fixed Tick、Room-per-Goroutine、客户端预测与状态插值、Adaptive AI Director 以及完整可观测与压测体系，重点展示高并发实时服务端设计与工程化能力。**

---

## Status

当前状态：

**Architecture Frozen / Repository Scaffolding**

下一阶段：

**Protocol Definition + Network Skeleton + Minimal Realtime Loop**
