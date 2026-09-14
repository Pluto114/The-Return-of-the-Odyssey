# 角色 B：第二阶段 D4 验证记录

本增量提供正式服务器可直接调用的首关 `StagePlan` 生成接口，并完成 D4 战斗规则复核和 D5 的 AI 决策频率解耦。实现不依赖协议、Socket、数据库或全局随机源。

## 交付接口

```go
plan, err := game.NewFirstStagePlan(roomConfig.World, seed)
receipt, err := rm.StartStage(plan)
if err == nil {
    err = <-receipt
}
```

A 的正式入口应在两名玩家 Join 成功并订阅可靠事件后调用。相同 `game.Config` 和 Seed 生成逐字段一致的三怪首关 Plan；计划在返回前通过 `ValidateStage`，`MaxMonsters < 3` 时返回错误。默认怪物为 40 HP、8 Attack、1.5 MoveSpeed、30 Tick 攻击冷却，半径 0.4、攻击距离 1。

怪物仍在 30Hz 固定 Tick 中移动和攻击。重新选择最近存活玩家的周期由 `AIDecisionRate=10` / `AIDecisionEvery=3` 明确定义，不再复用网络快照常量。

## 已验证场景

| 场景 | 预期与结果 |
| --- | --- |
| 相同 Seed 重放 | `NewFirstStagePlan(config, 42)` 两次结果逐字段一致 |
| 不同 Seed | 固定锚点的选择或顺序发生变化 |
| 计划合法性 | 三只怪物均在地图内、位置不重复且不与默认玩家出生点重合 |
| 容量边界 | `MaxMonsters < 3` 明确拒绝，不返回部分计划 |
| 重复开关 | 首次 StartStage 成功并仅产生一次 StageStarted，第二次返回 ErrStageState |
| AI 决策周期 | 目标离开后，非决策 Tick 保持 Idle，在下一个 10Hz 决策 Tick 切换到存活玩家 |
| 射速 | 原有 30Hz / 300Hz 输入测试均为默认每秒最多 5 发 |
| 子弹 | 原有容量、60 Tick 寿命、连续碰撞、最近命中和终局清理测试通过 |
| 终局优先级 | 同 Tick 最后击杀与全队死亡只判 Failed，不产生 StageCleared |

## 复现命令

在仓库根目录加载工具链后执行：

```powershell
. ./scripts/env.ps1
go test -count=1 ./server/internal/game/... ./server/internal/room/...
go run ./server/cmd/core-demo
pwsh -File scripts/test/check.ps1
```

`core-demo` 使用 Seed 42 的正式首关生成器，当前确定性结果为第 49 Tick 清场、玩家 HP 100、3 次击杀、6 次命中、9 次开火。

Windows / Go 1.26.8 的 Server 与 Bot 全包测试和 vet 通过。竞态检查在 WSL Ubuntu 20.04 / Go 1.26.8 / GCC 9.4.0 下对 `internal/game/...`、`internal/room/...` 和 `cmd/core-demo` 执行通过。Windows PATH 中的 GCC 8.1 运行 Go race binary 会以 `0xc0000139` 退出，因此没有把该工具链结果计作代码失败或通过。

本记录只确认 B 的领域层与 Room 接口。真实双客户端战斗需等待 A 调用该接口并由 C/D 完成对应消息和 Bot 接入后共同验收。
