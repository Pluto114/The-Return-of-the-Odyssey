# Snapshot Convention

10Hz 服务端权威快照。**静态数据传 id，动态数据传状态**。

## 频率

| 阶段 | 频率 | 来源 |
| --- | --- | --- |
| 服务端模拟 (Server Simulation) | 30Hz | Room Tick |
| **世界快照 (World Snapshot)** | **10Hz** | Snapshot Queue (Latest Wins) |
| 怪物 AI 决策 | 10Hz | 在 30Hz tick 内分摊 |
| 客户端渲染 | 60 / 120 / 144Hz | Main Thread |
| 客户端输入 | 30Hz | 与服务端对齐 |

服务端 30Hz 模拟 → 每 3 tick 凑齐 1 次 WorldSnapshot 进 Snapshot Queue；旧快照被替换（详见 [events.md](events.md)）。

## 三种 Snapshot

### Self Snapshot（`PlayerSnapshot`）

随 `WorldSnapshot.self` 一起发。所有字段都齐，客户端可以立即消费：

- 用于 HUD
- 用于 Prediction（结合 last_processed_input reconciliation）
- 不在 EntitySnapshot 列表里重复

### Other Player Snapshot（走 `EntitySnapshot`）

字段精简：**没有** weapon_id、state_flags 之外的 static-like 字段，详见 game.proto。每条 entity 8 字段以下。

> 注意：`state_flags` 在 player 与 monster 上 bit 定义可能不同；客户端按 archetype_id 区分解码。

### Monster Snapshot（也是 `EntitySnapshot`）

不发送 AI 内部状态：

- 不发 target_id
- 不发 attack_cooldown
- 不发 decision_timer

`archetype_id` 让客户端从本地 DataTable 查体型 / 颜色 / 模型。

## 静态数据传 id

| 静态维度 | wire 形式 | 客户端解码 |
| --- | --- | --- |
| 武器 | `weapon_id: uint32` | DataTable → name / icon / model |
| 怪物 | `archetype_id: uint32` | DataTable → size / color / 血条 |
| 装备修饰 | `StatModifier` 数组 | 客户端只用于 tooltip；最终数值服务器算 |
| 全局修饰 | 同上 | 同上 |

**不要**把静态 data table 的字段写进 Snapshot，否则要付带宽税。

## 增量/全量

v1 全量发。理由：单 Room 内 entity 数量 ≤ 32（4 player + ≤ 28 monster 的设计上限），序列化 < 2 KB/帧，压缩收益不值得加复杂度。

如果将来 entity 数量爆掉：

- 引入 `WorldSnapshotDelta`，但与全量 WorldSnapshot 并存；
- 客户端必须按 `server_tick` 单调消费，不准缓存 delta；
- 切换路径需要 A 评审、C 双向兼容。

## 谁负责

- 服务端构造：`server/internal/room`（B 实现）
- protobuf 形状：A 拥有 game.proto
- 客户端解码 / 插值 / 预测：C 实现
- 一致性 / 公平性 metric：A 在 metrics.go 暴露 `snapshot_*`
