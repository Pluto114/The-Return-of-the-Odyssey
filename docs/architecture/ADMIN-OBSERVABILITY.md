# 管理入口与实时可观测性

`gameserver` 在 `ODYSSEY_ADMIN_ADDR` 提供只读 Admin HTTP/WebSocket，在 `ODYSSEY_METRICS_ADDR` 提供 Prometheus 指标。两者读取同一份进程内权威状态，不访问或修改 World。

## Admin 接口

| 路径 | 用途 |
| --- | --- |
| `GET /healthz` | 进程级健康检查 |
| `GET /api/status` | 在线、房间、实体、Room/网络队列、最近 Director 决策的完整快照 |
| `GET /api/rooms` | 按 Room ID 排序的权威房间明细 |
| `GET /api/director/recent` | 最近 32 条已成功应用的 Director 决策 |
| `GET /ws` | 每秒推送一份与 `/api/status` 相同结构的 WebSocket 文本 JSON |

Admin 不返回昵称、Token、密码或客户端原始文本。当前没有认证与 TLS，因此配置层强制 `ODYSSEY_ADMIN_ADDR` 使用 `127.0.0.1`、`::1` 或 `localhost`；需要远程管理时应先增加认证并由受控反向代理终止 TLS，不能直接改成 `0.0.0.0`。WebSocket 浏览器 Origin 同样限制为回环地址。

Dashboard 开发服务器将 `/api` 和 `/ws` 代理到 Admin 地址。WebSocket 断开时页面自动退回每 5 秒 HTTP 轮询，并按上限 15 秒的退避重连。页面只保留当前浏览器实际收到的最近 120 个采样点，不注入示例数据。

## 数据来源与单消费者边界

- 实体 Gauge 来自 `gameApplication` 的唯一 `Room.Snapshots()` 消费者。
- 伤害与关卡 Counter 来自唯一 `Room.Events()` 消费者，只统计 World 已应用的事件。
- Tick 耗时和 Room 队列来自既有 `TickSamples()` 消费者与 `Room.Stats()`；累积源先转为差量，避免重复采样造成 Counter 翻倍。
- 网络可靠队列深度、拒绝与最新快照替换来自 `network.Server.Stats()`。连接关闭后累积 Counter 仍保留，Gauge 只汇总存活连接。
- 奖励观察函数由 A 的唯一 `RewardUpdates()` dispatcher 调用；D 不另起消费者。`offered/chosen/defaulted/invalid` 是固定标签集合。
- Director 指标与最近决策只在下一关 Plan 已获得 Room 成功回执后写入，失败或重试不得计为已应用。
- 恢复沿用固定结果集合的 `odyssey_reconnect_attempts_total{result}`，由 A4/D4 的真实恢复入口调用。

当前主流程尚未接入 A2/A3/A4，所以奖励、Director 与恢复系列会保持 0，最近 Director 列表为空。这表示“没有真实事件”，不能解释为这些流程已经验收。

## 新增 Prometheus 指标

| 指标 | 类型 | 含义 |
| --- | --- | --- |
| `odyssey_rewards_total{result}` | Counter | offered/chosen/defaulted/invalid 奖励结果 |
| `odyssey_director_decisions_total` | Counter | 已成功应用的 Director 决策数 |
| `odyssey_director_decision_duration_seconds` | Histogram | 决策生成到成功应用的耗时 |
| `odyssey_director_input_*` / `odyssey_director_output_*` | Gauge | 最近一次已应用决策的输入/输出摘要 |
| `odyssey_room_control_queue_depth` / `odyssey_room_input_queue_depth` | Gauge | 所有活跃 Room 当前队列深度 |
| `odyssey_network_reliable_queue_depth` | Gauge | 所有存活连接当前可靠队列深度 |
| `odyssey_room_queue_rejections_total` | Counter | Room 队列满导致的命令准入拒绝 |
| `odyssey_room_rejected_inputs_total` | Counter | Room 权威校验拒绝的已入队输入 |
| `odyssey_room_dropped_snapshots_total` / `odyssey_room_dropped_tick_samples_total` | Counter | Room 最新优先快照替换与有损采样丢弃 |
| `odyssey_network_reliable_queue_rejections_total` | Counter | 可靠发送被满队列或关闭连接拒绝 |
| `odyssey_network_snapshot_replacements_total` | Counter | 网络层被更新快照替换的旧快照 |

所有 Prometheus 标签均为代码内封闭集合；Room ID、Stage、Seed 和原因文本只进入日志或 Admin 查询，防止高基数序列。
