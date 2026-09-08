# Events vs State, and Queue Selection

协议必须区分 "**状态**" 与 "**事件**"，并按此分配队列。

## 原则

> **Event 提供视觉及时性；Snapshot 提供最终一致性。**

- **State**：世界当前什么样（Position、Velocity、HP）。
  - 丢失一次 → 下次 Snapshot 恢复。
  - 走 Snapshot Queue（Latest Wins）。
- **Event**：刚刚发生什么（Shoot / Damage / Death / StageClear / Reward）。
  - 丢失即错过，需要补发机制（**实现位于 v2**）。
  - v1 走 Reliable Queue（FIFO，固定容量，有 drop 监控）。

## 选型矩阵

| 消息 | 性质 | 队列 |
| --- | --- | --- |
| Ping / Pong | 系统层保活 | 系统自带 timer |
| LoginResponse / ResumeResponse | 一次性握手结果 | Reliable（写入即发） |
| Disconnect | 一次性提示 | Reliable |
| MatchFound | 一次性提示 | Reliable |
| WorldSnapshot | State | Snapshot (Latest Wins, capacity ≈ 1) |
| PlayerInput | 意图输入 | 反向：进 RoomCommand channel |
| StageStarted / StageCleared | 阶段性状态变更 | Reliable |
| RewardOptions / RewardApplied | 一次性 | Reliable |
| DamageEvent | Event | Reliable |
| DeathEvent | Event | Reliable |
| ProjectileSpawnEvent | Event | Reliable |
| ProjectileDestroyEvent | Event | Reliable |
| EntityRemovedEvent | Event | Reliable |
| NextStageRequest | 意图 | 反向：进 RoomCommand |

## Reliable Queue

- 有界 FIFO，建议容量 256。
- 满时策略：丢最早 + metric 报警（**不要**断连，reliable 语义就毁了）。
- v1 不实现补发；v2 才加 `event_seq` + receiver ack。

## Snapshot Queue

- 容量 1（Latest Wins）。
- 满时：丢弃旧的，保留新的；永远不上 backpressure 到 Room Tick。
- Write Timer：从 Room Tick 推到 Writer goroutine，禁止 Room 协程直接做 socket write。

## Writer Goroutine 背压

```text
Room Tick
  │
  ▼ (snapshots)
Snapshot Queue ──── ► Writer Goroutine ──── ► Socket
  │
Room Tick
  │
  ▼ (events)
Reliable Queue ──── ► Writer Goroutine ──── ► Socket
```

- Writer Goroutine 是连接级单例，与 Connection 一起创建、一起销毁。
- Room Tick 永远不直接调 socket write。
- 慢客户端：Writer 在 socket blocking 时，Reliable Queue 满 → 丢最早 + 报警；Snapshot Queue 满 → 永远是最新（无损失）。

## v2 待办（不在 v1 范围）

- Reliable Queue 的 event_seq + selective retry
- Snapshot 的 delta 模式
- Encryption（TLS over TCP → 仍在 L4 之上，Header 同样透传）
