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
| StageStartedEvent | Event（B: StageStarted） | Reliable |
| ProjectileSpawnEvent | Event（B: ProjectileSpawned） | Reliable |
| ProjectileDestroyEvent | Event（B: ProjectileDestroyed） | Reliable |
| DamageEvent | Event（B: DamageDealt） | Reliable |
| DeathEvent | Event（B: EntityDied，玩家/怪物统一） | Reliable |
| StageClearedEvent | Event（B: StageCleared） | Reliable |
| TeamDefeatedEvent | Event（B: TeamDefeated） | Reliable |
| RewardOptions / RewardApplied | 一次性 | Reliable |
| NextStageRequest | 意图 | 反向：进 RoomCommand |

> 注：B 的领域事件统一用 `EntityDied` 表示玩家和怪物死亡；协议层 `DeathEvent`
> 对应它，不再区分 `EntityRemovedEvent`（despawn 语义暂未实现，ID 保留）。
> `ProjectileDestroyEvent` 不携带 reason（B 不区分命中/过期/主人死亡，客户端
> 只播一个销毁特效）。

## Reliable Queue

- 有界 FIFO，建议容量 256。
- 满时策略：丢最早 + metric 报警（**不要**断连，reliable 语义就毁了）。
- v1 不实现补发；v2 才加 `event_seq` + receiver ack。

## Event Backpressure（对接 B 的 room.EventCapacity）

B 的房间事件队列有硬上限（默认 64 个 Tick 批次）。当事件出口满或 World 事件
缓冲溢出时，Room 会**结束模拟并关闭**，记录 `event_backpressure`，发布 Closed
空快照。这不是正常清场，A 必须：

1. 用单个 dispatcher 及时消费 `r.Events()`，事件所有权转交给 dispatcher，不得
   让每个客户端各自竞争读 channel。
2. 收到房间关闭（`Done` 关闭 + `Stats.CloseReason == "event_backpressure"`）时，
   向房内客户端发 `Disconnect(REASON_EVENT_BACKPRESSURE)`，并通知 D 注销房间。
3. **绝不能**把事件拥塞当作清场/团灭成功路径上报。

对应 ReasonCode：`REASON_ROOM_CLOSED`(requested) / `REASON_ROOM_IDLE`(idle) /
`REASON_EVENT_BACKPRESSURE`(event_backpressure)。

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
