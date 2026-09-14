# 角色 B：D6 装备与奖励领域层验证

验证基于 `codex/week2-game-loop`，范围为 `data/equipment/catalog.json`、`game/equipment` 和 `game/reward`。该提交冻结领域规则和接入接口，不代表正式 gameserver、协议路由或客户端 UI 已完成。

## 验证结果

| 检查 | 结果 |
| --- | --- |
| `pwsh -File scripts/test/check.ps1` | Server/Bot 全包 test 与 vet 通过 |
| Linux `go test -race ./internal/game/... ./internal/room/... ./cmd/core-demo` | Ubuntu 20.04 / Go 1.26.8 / GCC 9.4.0 通过 |
| 默认装备目录 | 版本 1，ID 1001–1002、2001–2002、3001–3002 均可加载，返回值无可变别名 |
| 非法目录 | 重复 ID、未知字段、非正倍率、Potion 混入持续修改器、多根 JSON 等均拒绝 |
| 属性重算 | 固定 Weapon→Relic→文件内顺序；从 BaseStats 重建；AttackSpeed 正确换算回冷却 Tick |
| 装备替换 | 同槽替换，不重复叠加；MaxHealth 上升不治疗、下降裁剪当前 HP；失败不提交临时状态 |
| 药水 | 治疗不超过 MaxHealth，成功后清空 PotionID，第二次使用返回 ErrNoPotion |
| 奖励生成 | 玩家输入顺序不影响结果；同 Stage/Seed/Player 得到相同的最多三项候选；切片已分离 |
| 奖励选择 | 未知玩家、非候选 ID、重复选择、截止 Tick 后选择均明确拒绝 |
| 超时 | 截止 Tick 当 Tick 仍接受；下一 Tick 为未选择玩家生成候选首项 Defaulted 选择 |

## 接入门

后续 World/Room 接线已按 `ValidateChoice → equipment.Apply 到临时值 → Commit` 实现。若 Apply 失败，不会 Commit，也不会产生成功的 RewardApplied。候选只单播给对应 PlayerID；现有战斗事件广播器不能直接复用。接线验证见 [D6 World / Room 记录](../week2-b-d6-world/README.md)。

静态目录必须在进程启动阶段读取，禁止 Room Tick 访问磁盘。C 使用相同版本文件做显示映射，D 负责启动校验和真实指标。详细规则见 [装备与奖励领域接口](../../architecture/EQUIPMENT-REWARD.md)。
