# 角色 B：PerformanceMetrics 与 Rule-Based Director

本增量实现 D7 的纯 Director、关卡性能冻结和连续关卡核心。Director 不持有 World 指针，也不修改 Room；它只把上一关不可变结果转换成下一份完整 `stage.Plan`。

## 1. 指标口径

World 在 StartStage 时重置采样，在 StageClear 的同一 Tick 冻结以下值：

| 字段 | 计算方式 |
| --- | --- |
| ClearTimeSeconds | 从 StartStage 时的 ServerTick 到清场 Tick，除以固定 30Hz |
| TeamHPPercent | 全队当前 HP 总和 / 当前 MaxHealth 总和，死亡玩家计 0 |
| AverageDPS | 玩家实际结算到怪物的伤害 / ClearTimeSeconds；过量伤害只计目标剩余 HP |
| DeathCount | 本关玩家从存活变为死亡的次数 |
| DamageTaken | 玩家实际扣除的 HP 总和，过量伤害裁剪 |
| EquipmentPower | 每名玩家 Attack、Defense、MaxHealth、MoveSpeed、AttackSpeed 相对 BaseStats 的倍率均值，再取全队均值 |

只有 StageClear、Reward、PreparingNextStage 可以读取冻结结果。奖励改变属性后，上一关结果保持不变；下一关 StartStage 会清空旧结果并开始新一轮采样。

Room 外部通过单写者查询，不读取 World：

```go
receipt, err := rm.CompletedStage()
completed := <-receipt // completed.Result.Plan + completed.Result.Performance
```

Plan 会复制后返回，调用者修改 receipt 不会污染 World。

## 2. 难度规则

`RuleBasedPlanner.Decide` 从基础推进 `+3%` 开始，再按可解释规则调整：

- 清场时间 ≤目标 75%：`+6%`；≥目标 150%：`-8%`。
- TeamHP ≥75%：`+4%`；≤35%：`-6%`。
- 每次死亡 `-3%`，本项最多 `-6%`。
- `AverageDPS / EquipmentPower` ≥目标 125%：`+4%`；≤目标 65%：`-4%`。
- 承伤 ≤目标 50%：`+2%`；≥目标 150%：`-3%`。

最终单关 Adjustment 强制限制在 `[-15%, +20%]`，DifficultyScore 还会裁剪到配置的全局 `[0.5, 10]`。`Decision.Reasons` 保存本次命中的规则，供 D 的日志、指标和管理端显示。

怪物数量按上一关数量与难度倍率平方根计算，并受 MaxMonsters 限制。每只怪物复用上一关原型，按每怪预算确定性缩放 Attack、MaxHealth、Defense 和 MoveSpeed；攻击冷却、半径和攻击距离保留。下一关 Seed 由上一关 Seed 和新 StageIndex 确定，出生位置使用私有 SplitMix64 在地图安全边距内生成，避开玩家出生点。

## 3. 编排顺序

```go
completedReceipt, _ := rm.CompletedStage()
completed := <-completedReceipt

nextPlan, decision, err := planner.Decide(
    completed.Result.Plan,
    completed.Result.Performance,
)

// Reward 完成且 A 的在线玩家 Ready 屏障通过后：
startReceipt, err := rm.StartStage(nextPlan)
err = <-startReceipt
```

World 只在 PreparingNextStage 接受下一关，并要求 Index 严格加一。Planner 输出在返回前通过 StagePlan 校验；生成失败不会部分修改 World。

A 仍负责在线玩家 Ready 屏障、Session 状态和正式入口调用。D 负责保存 Decision、采样生成耗时和难度曲线。客户端不提交 PerformanceMetrics、DifficultyScore、Seed 或 MonsterStats。
