# 可观测性模块约定

`server/internal/metrics` 使用独立 Prometheus Registry，统一拥有指标名、帮助文本、Bucket 和标签集合。业务模块只提交领域结果，不直接创建 Prometheus Collector。

D4 预留的战斗指标为 `odyssey_active_monsters`、`odyssey_active_projectiles`、`odyssey_damage_dealt_total` 和 `odyssey_stage_results_total{result}`。D5 的 gameserver 适配器从 Room 权威快照聚合怪物 Gauge，从可靠 Spawn/Destroy 事件集合聚合子弹 Gauge，并只在 World 已应用的伤害和终局事件上累加 Counter；`result` 仅允许 `cleared` 与 `defeated`。房间删除会立即重新聚合 Gauge，Dashboard 不填充固定假数据。

Grafana 的 `Combat Entities`、`Damage Throughput` 和 `Stage Results` 面板直接查询上述指标。AI 与碰撞耗时需要 B 在 Room TickSample 中提供权威采样后再接入。

## 当前指标

| 指标 | 类型 | 含义 |
| --- | --- | --- |
| `odyssey_online_players` | Gauge | 当前在线玩家数 |
| `odyssey_active_rooms` | Gauge | 当前活跃房间数 |
| `odyssey_match_queue_players` | Gauge | 当前排队玩家数 |
| `odyssey_matches_total` | Counter | 累计成功组成的比赛数 |
| `odyssey_match_duration_seconds` | Histogram | 成功匹配的等待时长 |
| `odyssey_reconnect_attempts_total{result}` | Counter | 按固定结果分类的重连尝试数 |

重连结果标签只允许 `success`、`invalid_token`、`expired`、`backend_error`，禁止使用玩家 ID、令牌、错误文本等高基数或敏感值。

成员 A/B 的集成层负责把 Session、Room 和 Matchmaker 状态汇总为 `Snapshot`，并在匹配、重连完成后调用对应观察操作。指标记录错误不能回滚或改变游戏结果。

未来 gameserver 在 `ODYSSEY_METRICS_ADDR` 上将 `Metrics.Handler()` 挂载到 `/metrics`。当前入口尚未实现，因此 Prometheus 的 gameserver target 显示 DOWN 是正常状态。

Grafana 的 `Odyssey Overview` 面板已按这些真实指标预配置。在 gameserver 暴露指标以前，应用面板显示 No data，不代表 Prometheus 或 Grafana 故障。
