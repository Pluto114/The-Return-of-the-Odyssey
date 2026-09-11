# 角色 B：装备与奖励领域接口

本增量实现 D6 的纯领域层：版本化装备目录、槽位替换、属性重算、药水消费，以及按玩家生成的确定性奖励轮次。代码不包含 protobuf、Socket、Session 或数据库访问，可在 A 完成 Room/Session 奖励编排前独立测试。

## 1. 单一静态数据源

装备定义位于 `data/equipment/catalog.json`。当前版本为 1，包含两个 Weapon、两个 Relic 和两个 Potion。服务器启动时由 D 的配置装配层读取一次：

```go
file, err := os.Open("data/equipment/catalog.json")
catalog, err := equipment.Parse(file)
```

`Parse` 拒绝未知 JSON 字段、多个根值、空版本、重复 ID/Key、未知槽位/属性/操作、非有限数值、非正 MULTIPLY，以及同时携带持续修改器和治疗量的 Potion。`Catalog` 保存和返回的切片均已分离，Room Tick 不读取文件。

C 使用同一文件中的 ID、名称和描述构建显示表；线上消息只发送 `equipment_id`，不重复发送完整定义。ID 0 保留为“空槽位”，已发布 ID 不复用。

## 2. 属性和槽位规则

`equipment.Loadout` 只有 Weapon、Relic、Potion 三个槽位，新奖励替换同槽旧物品。`equipment.Resolve` 每次从 `BaseStats` 重建 `CurrentStats`，不在上一次结果上继续叠加：

1. 应用 Weapon，按 JSON 中修改器顺序执行。
2. 应用 Relic，按 JSON 中修改器顺序执行。
3. ADD 执行加法，MULTIPLY 执行乘法。
4. AttackSpeed 先以 `TickRate / BaseCooldownTicks` 转成每秒攻击次数，完成修改后再四舍五入回冷却 Tick，最短为 1 Tick。
5. 最终属性必须通过 `CombatStats.Valid`；失败时调用者保留原 Loadout、HP 和属性。

`equipment.Apply` 先在临时值上替换槽位和重算。MaxHealth 增加不会附赠治疗；MaxHealth 降低时只把当前 HP 裁剪到新上限。Potion 不产生持续修改器，`UsePotion` 将 HP 裁剪到当前 MaxHealth，并在成功调用时把 PotionID 清零；满血使用仍会消耗，第二次使用返回 `ErrNoPotion`。

## 3. 奖励轮次规则

`reward.NewRound(stageIndex, seed, openedAtTick, durationTicks, players, catalog, optionCount)` 为每名玩家生成 1–3 个不重复候选。玩家 ID 会先排序，所以调用者传入顺序不影响结果；Stage、Seed 和 PlayerID 共同决定私有 SplitMix64 洗牌，相同输入逐字段一致。

选择流程必须保持以下顺序：

```go
selection, err := round.ValidateChoice(playerID, equipmentID, serverTick)
// 使用 selection.EquipmentID() 调用 equipment.Apply，并先写入临时值。
if err == nil && applySucceeded {
    err = round.Commit(selection)
}
```

`ValidateChoice` 拒绝未知玩家、非候选 ID、重复选择和 `serverTick > DeadlineTick`。截止 Tick 本身仍可选择；从下一 Tick 起，`DueDefaults` 为所有未选择玩家返回各自候选列表第一项，并标记 Defaulted。默认项也必须先成功 Apply 再 Commit。`Selection` 绑定创建它的 Round，不能拿另一个 Round 的校验结果提交。

所有玩家提交后 `round.Complete()` 才返回 true。A 的 Session/Room 编排可据此从 Reward 进入 PreparingNextStage；客户端消息不能直接修改 Round、Loadout 或 CurrentStats。

## 4. 尚待接线的边界

- B 下一增量：把 Catalog、Round 和玩家 Loadout 纳入 World/Room 单写者状态，提供开始奖励、选择和超时默认命令，并把装备 ID 放入权威快照。
- A：将 RewardOptions/RewardChoice/RewardApplied 路由到 B 的 Room 命令；按 PlayerID 单播候选，不能用现有战斗事件广播器泄露其他玩家选项。
- C：从同版本目录显示名称、描述和属性变化；仅在 RewardApplied 成功后更新 UI，最终仍以快照为准。
- D：在进程启动阶段加载并校验目录，暴露目录版本和 offered/chosen/defaulted/invalid 指标；配置失败时禁止启动正式玩法。

当前增量不宣称 Reward 已进入 gameserver 正式流程。它冻结了 B 的数据和选择语义，使 A/C/D 可以并行接入而不各自实现一套规则。
