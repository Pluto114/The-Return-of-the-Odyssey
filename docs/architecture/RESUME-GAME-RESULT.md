# 角色 B：恢复状态与 GameResult 接口

本接口完成 D8 中 B 负责的运行状态边界。Room 继续是 World 唯一写入者；恢复不会反序列化旧 World、重建玩家或回滚 Tick。A 负责连接和 Session，D 负责一次性 Resume Token 与异步持久化。

## 1. 恢复顺序

1. TCP 断开后，A 将 Session 置为 Disconnected 并使旧 Connection 失效；宽限期内不要调用 `Room.Leave`。
2. World 继续以 30Hz 运行。断线前最后一个输入最多保持到 `InputTimeout`，之后移动和射击停止；怪物、伤害、死亡与奖励 Deadline 不暂停。
3. D 原子消费 Resume Token，并校验它仍指向同一 `session_id/player_id/room_id`。伪造、过期或重复 Token 在进入 Room 前拒绝。
4. A 绑定新 Connection 后，以保留的 SessionID 调用 `Room.ResumeState(sessionID)`。
5. 回执包含查询 Tick 的完整权威 Snapshot；若奖励 Round 尚在，另含该玩家私有的 RewardOptions 或已提交的 RewardApplied 重建值。A 只能向该玩家单播。
6. C 清空旧 PendingInput，以 Snapshot 的 `LastProcessedInputSeq` 为基线发送更大的新序号。旧序号进入 Room 后仍由 World 按 stale input 拒绝。
7. 宽限期到期后 A/D 调用 `Room.Leave`；这才永久移除玩家及其奖励项。此后 `ResumeState` 返回 `ErrNotJoined`。

```go
receipt, err := rm.ResumeState(existingSessionID)
resumed := <-receipt

full := resumed.State.Snapshot
privateReward := resumed.State.Reward // nil、RewardOptionsAvailable 或 RewardSelectionApplied
```

恢复期间死亡不会撤销；恢复者会看到当前 `Alive/Health`。已超时的奖励会得到服务端默认选择，恢复结果会携带 `Defaulted=true`。Ready 屏障由 A 持有，不写入 World；连接断开时应清除该 Session 的 Ready，恢复后需要重新提交，除非下一关已按“所有在线玩家 Ready”规则开始。

## 2. GameResult

`Room.GameResult(outcome)` 在所有者队列上生成协议无关、完全复制的结果，供 D 在 Room Tick 外异步落库：

- `GameVictory`：只允许 StageClear、Reward 或 PreparingNextStage；EndedAtTick 固定为最后一关 ClearTick。
- `GameDefeat`：只允许权威 StageFailed；EndedAtTick 在团灭 Tick 冻结，后续空 Tick 不改变结果。
- `GameAbandoned`：只允许仍在运行且未团灭的对局，由可信生命周期编排调用。
- 结果包含开始/结束 Tick、最后 StageIndex、所有已清关的 Seed/Difficulty/Performance 摘要，以及按 PlayerID 排序的最终 HP、属性和装备 ID。

```go
receipt, err := rm.GameResult(game.GameDefeat)
result := (<-receipt).Result

// D 使用自己的 match_id/room_id 做幂等键并异步写入；不得在 Room Tick 内访问 MySQL。
```

同一 World 可以重复读取结果以应对队列交付重试，返回值不会引用内部切片。恰好一次持久化由 D 使用对局幂等键保证。

## 3. 接入边界

- B 不验证 Token、不替换 Connection、不修改 Session 状态，也不访问 Redis/MySQL。
- A 不缓存或恢复旧 Snapshot；必须从 `ResumeState` 获取当前完整状态。
- Reward 字段是私有定向状态，不得随房间广播。
- 当前提交提供服务端领域和 Room 接口；A 的 Resume 协议路由、D 的结果表与客户端恢复 UI 仍待接入。
