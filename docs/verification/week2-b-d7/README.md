# 角色 B：D7 Director 与连续关卡验证

## 已验证行为

| 场景 | 结果 |
| --- | --- |
| 指标冻结 | ClearTime、HP%、DPS、死亡、实际承伤和装备强度在清场 Tick 冻结 |
| 奖励隔离 | 清场后装备变化不会回写上一关 PerformanceMetrics |
| Room 查询 | CompletedStage 经控制队列返回复制的 Plan 和指标，不破坏 World 单写者 |
| Director 纯函数 | 相同 Config、Plan、Metrics 逐字段产生相同 Plan 和 Decision，上一关 Plan 不变 |
| 难度边界 | 强表现不超过 +20%，弱表现不低于 -15%，并受全局 0.5–10 限制 |
| Seed 与出生 | 下一关 Seed 确定且跨关变化；出生在地图内并避开玩家出生点 |
| 非法输入 | 非有限/越界指标、非法上一关和非法 Director 配置均拒绝 |
| 三关循环 | 离线权威 World 连续完成 Fight→Clear→Reward→Director→Next Stage 三关 |

## 复现

```powershell
. ./scripts/env.ps1
go test -count=1 ./server/internal/game/... ./server/internal/room/...
pwsh -File scripts/test/check.ps1
```

竞态检查：

```bash
GOWORK=off CGO_ENABLED=1 go test -race -count=1 -timeout=90s ./internal/game/... ./internal/room/... ./cmd/core-demo
```

本记录覆盖服务端核心，不宣称 gameserver 的 Ready/协议编排或客户端三关 UI 已完成。A 应在 Reward 完成和所有在线玩家 Ready 后调用 Planner 与 StartStage；D 可直接记录 `Decision.Reasons` 和输入输出。
