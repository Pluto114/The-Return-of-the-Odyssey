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

`WorldSnapshot` 现在的结构（对齐 B 的 `game.Snapshot` + `room.Snapshot`）：

```text
WorldSnapshot
  ├─ server_tick            uint64
  ├─ last_processed_input   uint32   ← 本玩家的输入 ack
  ├─ self                   PlayerSnapshot
  ├─ players                repeated PlayerSnapshot  ← 其他玩家，按 player_id 升序
  ├─ monsters               repeated MonsterSnapshot ← 全量怪物，缺即移除
  └─ stage                  StageState
```

### Self Snapshot（`PlayerSnapshot.self`）

随 `WorldSnapshot.self` 一起发。所有字段都齐，客户端可以立即消费：

- 用于 HUD
- 用于 Prediction（结合 last_processed_input reconciliation）
- 不在 players 列表里重复

`PlayerSnapshot` 字段：`player_id / position / velocity / aim(Vec2) / hp /
max_hp / attack / defense / move_speed / alive`。`alive=false` 表示已死亡，
客户端应抑制该实体的输入。

### Other Player Snapshot（`players`）

与 self 同构（`PlayerSnapshot`），按 player_id 升序排列，稳定顺序。

### Monster Snapshot（`monsters`）

独立 `MonsterSnapshot`，字段：`monster_id / position / velocity / hp / max_hp /
state`。`state` 对齐 `entity.MonsterState`：0 idle / 1 chase / 2 attack / 3 dead。

不发送 AI 内部状态：

- 不发 target_id
- 不发 attack_cooldown
- 不发 decision_timer

怪物全量集合语义：**缺失即移除**——客户端按 monster_id 对齐，列表里没有的
怪物要从画面上删掉。子弹（projectile）**不进快照**，只通过
ProjectileSpawn/Destroy 事件维护视觉效果（B 的显式约定）。

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
