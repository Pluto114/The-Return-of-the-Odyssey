# 角色 B：D6 World / Room 奖励接线验证

本增量把已经验证的 equipment/reward 领域层纳入 World 和 Room 单写者流程。TCP、protobuf 转换和客户端暂未接入。

## 已验证行为

| 场景 | 结果 |
| --- | --- |
| StartReward 原子性 | 先验证所有物品对所有玩家均可应用；非法目录保持 StageClear 且不产生候选 |
| 定向候选 | 每名玩家得到独立 RewardOptionsAvailable，包含 PlayerID、最多三项 ID 和 DeadlineTick |
| Session 权威 | Room.ChooseReward 从 Session 绑定解析 PlayerID；零/未入房 Session 和零物品 ID 拒绝 |
| 选择提交 | 非候选、重复和过期选择不修改玩家；成功后装备 ID 与 CurrentStats 同时进入快照 |
| 状态转换 | StageClear→Reward→PreparingNextStage；下一关只接受上一关 Index+1 |
| 超时 | 截止 Tick 本身仍接受；下一 Tick 应用各玩家候选首项并标记 Defaulted |
| 跨关状态 | 装备和存活者 HP 保留、位置重置；死亡队友以当前 MaxHealth 50% 复活 |
| Potion 输入 | Playing 中仅在新 InputSeq 消费一次，治疗封顶并清空 PotionID |
| Reward 背压 | 定向可靠队列满时 Room 以 event_backpressure 关闭，不静默丢消息 |
| 断开处理 | 永久离开会移除该玩家待选项；剩余玩家均完成后可继续下一关 |

## 复现

```powershell
. ./scripts/env.ps1
go test -count=1 ./server/internal/game/... ./server/internal/room/...
pwsh -File scripts/test/check.ps1
```

竞态检查使用 WSL Ubuntu 20.04 / Go 1.26.8 / GCC 9.4.0：

```bash
GOWORK=off CGO_ENABLED=1 go test -race -count=1 -timeout=90s ./internal/game/... ./internal/room/... ./cmd/core-demo
```

A 接入时必须同时消费 `Room.Events()` 和 `Room.RewardUpdates()`；后者按 PlayerID 单播。只调用 StartReward 而不消费定向出口会在有界队列耗尽后按设计关闭房间。
