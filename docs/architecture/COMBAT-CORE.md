# 角色 B：首关战斗核心与接入接口

本增量在移动原型上实现一个可独立运行、可重复验证的首关战斗过程。原来不调用 StartStage 的房间继续作为移动房间工作。
代码仍由 Room goroutine 唯一写入；协议、Session、客户端渲染、匹配、监控接入由对应成员完成。
协作安排与验收顺序见 [最新需求清单](../plans/CURRENT-COLLABORATION.md)。

## 1. 直接运行

在仓库根目录的 PowerShell 7 中执行：

```powershell
. ./scripts/env.ps1
go run ./server/cmd/core-demo
```

演示使用一个玩家和 `NewFirstStagePlan(DefaultConfig(), 42)` 生成的三只怪物，自动产生瞄准/射击输入；通过同一个 World.Step 执行真实战斗逻辑。
当前固定场景在第 49 Tick 清场：玩家 HP=100，击杀 3，命中 6，开火 9。
这是离线固定步模拟，没有网络连接，不是 Go Bot、真实客户端联调或性能测试。

## 2. 已实现行为

| 模块 | 当前规则 |
| --- | --- |
| 玩家 | BaseStats 与 CurrentStats 分离；HP / Alive / Aim 进入全量快照；死亡后停止移动和射击 |
| 射击 | Input 新增 Aim、Shoot；输入依旧只包含意图；Aim 必须有限，Shoot=true 时不能是零向量 |
| 射速 | 默认冷却 6 Tick，即最高 5 次/秒；按住 Shoot 自动连发；高频包不缩短冷却 |
| 子弹 | 服务端分配 ID、记录出生点/速度/攻击值/失效 Tick；默认速度 20、半径 0.1、寿命 60 Tick |
| 碰撞 | 子弹与怪物按一个 Tick 内的相对运动做线段/圆检测，命中最近目标后销毁，不穿透、不友伤；越界和到期也销毁 |
| 怪物 | 10Hz 重新选择最近存活玩家，等距时选较小 Player ID；30Hz 移动、按攻击距离和 Tick 冷却近战；目标断开/死亡后换目标 |
| 伤害 | Collision/AI 产生 DamageRequest；CombatSystem 计算 Attack × 100 / (100 + Defense)；World 更新 HP 并产生 Damage/Death 事件 |
| 清理 | 怪物死亡后移出全量快照；玩家死亡仍保留实体并标记 Alive=false；清场和团灭清理在飞子弹 |
| 关卡 | Waiting → Playing → StageClear 或 Failed；最后击杀与团灭同 Tick 时判 Failed；Room 关闭时进入 Closed |

属性和几何输入会校验：不允许 NaN/Inf、负生命/防御/攻击、零冷却。坐标范围限制在 ±1e6 内，避免碰撞运算溢出。
默认最多 64 怪物、256 子弹；配置硬上限分别为 128、1024，防止单房无界生成实体。
出生配置、实体遍历和最近目标平局规则固定后，相同计划和输入产生相同事件与快照。
首关生成器使用进程内私有的 SplitMix64 序列，从地图边缘的八个相对锚点中按 Seed 选择并排序三处出生点；不使用全局随机源。相同配置和 Seed 会得到逐字段一致的 Plan，不同房间可用 room_id 等服务器可信值派生 Seed。

## 3. A / C / D 需要接入的地方

| 接口 | 接入方式 |
| --- | --- |
| `room.Config` / `game.Config` | 从 DefaultConfig 获取配置，再覆盖必要字段；新增 Combat / EventCapacity 有校验，不能用旧的零值字面量漏填 |
| `game.NewFirstStagePlan(config, seed)` | B 提供的首关计划入口；传入 `room.Config.World` 和服务端 Seed，返回经过 `ValidateStage` 的三怪 Plan；怪物容量小于 3 时明确报错 |
| `r.StartStage(stage.Plan)` | 服务端可信关卡编排入口；先入房，再提交计划并等成功回执；计划含 Index / Seed / DifficultyScore / Monsters，提交时复制 |
| `game.Input.Aim / Shoot` | A 从协议 DTO 转换，C 提供瞄准向量和按键状态；释放发送 Shoot=false，输入超时同样停止射击 |
| `r.Events()` | A 用单个 dispatcher 消费，再封装 Room ID、映射消息 ID/DTO，并投递 Session 可靠队列 |
| `r.RewardUpdates()` | B 的定向可靠奖励出口；A 必须按 PlayerID 单播 RewardOptions/RewardApplied，不可使用战斗广播器 |
| `r.Snapshots()` | 仍为 10Hz 完整快照；新增 MonsterView 和 Stage，玩家新增生命/属性/瞄准；C 按 ID 对齐并移除缺失怪物 |
| `Stats.CloseReason` | D 可观察 requested / idle / event_backpressure；触发关闭后注销房间、通知对应 Session |
| `director.Planner` | 仅声明 Generate(previous Plan, PerformanceMetrics) → (Plan, error)；具体规则算法尚未实现 |

StartStage 只能在 Waiting 且至少有一名存活玩家时成功；进入战斗后不允许新增玩家，重复绑定现有玩家仍幂等。
正式入口应先完成两名玩家的 Join 和事件订阅，再生成首关 Plan、提交 StartStage 并等待 receipt；任何一步失败都按房间创建失败清理，不能向客户端宣称关卡已开始。
StageClear 经 StartReward 进入 Reward；所有选择或超时默认完成后进入 PreparingNextStage。此时 StartStage 只接受 `Index=上一关+1` 的合法计划；Failed 仍是终局。

实体 ID 使用 uint64：玩家 ID 范围为 1 到 2^63−1，怪物/子弹使用高半区并在同一 World 内单调分配。A/C 需要保留 64 位，不可缩窄到 uint32。
这里的 State / EventKind 都是领域枚举，A 应显式映射到自己维护的协议枚举和消息 ID，不直接当成 wire 编号。
游戏内没有 Proto、Socket、数据库或 Prometheus 依赖。A 的 `server/internal/convert` 和 `router` 在领域层外完成协议映射；正式服务入口仍待接线。

## 4. 状态与事件出口

事件包括 StageStarted、ProjectileSpawned、ProjectileDestroyed、DamageDealt、EntityDied、StageCleared、TeamDefeated。
它们携带 ServerTick、StageIndex、相关实体 ID，以及事件需要的位置/速度/伤害/剩余生命。A 已按七类事件拆分正式协议消息；StageIndex 由事件产生时写入，不能从滞后的快照推断。
子弹不进入 WorldSnapshot，符合原架构的视觉子弹策略；C 根据 Spawn / Destroy 事件维护视觉效果。
怪物快照只含 ID、位置、速度、HP/MaxHP 和 State，不暴露目标 ID、攻击冷却计时或内部决策状态。

Room 的事件队列默认容纳 64 个 Tick 批次，只有一个消费者，每批事件所有权转交给消费者。
可靠事件不能像旧快照一样覆盖。队列饱和或 World 事件缓冲溢出时，Room 结束模拟、清理实体、发布 Closed 空快照，并记录 event_backpressure。
该关闭属于接入失败，需要 A/D 处理并明确报告；不表示所有事件已经可靠交付客户端。A 的 Session 可靠队列仍须单独验证。
离线直接使用 World 时每 Tick 调用 TakeEvents；未消费缓冲上限 4096 条，达到上限会返回 Overflow=true，不得忽略该标志。

## 5. 明确留待后续的部分

- A：将已完成的射击/战斗事件映射接入正式入口，补 Session 可靠发送失败与断线通知。
- C：怪物/HP 展示、视觉子弹、事件效果、预测与插值。
- D：匹配调用、监控映射、真实 Bot / 联调 / 性能验证。
- B：装备/奖励纯领域接口已进入 [装备与奖励领域接口](EQUIPMENT-REWARD.md)；World/Room 接线、下一关转换、Director 规则、逐阶段性能采样和优化继续推进。首关生成器和显式 10Hz AI 决策周期已完成。

本轮只实现 B 的首关原型，尚未完成架构中的完整 Roguelike 循环。验证结果见 [本轮记录](../verification/combat-core/README.md)。
