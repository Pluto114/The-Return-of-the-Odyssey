# 最新协作需求：A/B 网络与战斗集成

同步日期：2026-09-09。A 最新基线：`feature/network@fe4d84c`；B 原基线：`codex/game-core-phase1@775fb5a`；集成分支：`codex/network-core-integration`。
本页是本轮协作入口；详细接口以 [首关战斗契约](../architecture/COMBAT-CORE.md) 为准，移动基础见 [原移动契约](../architecture/GAME-CORE-PHASE1.md)。

## 当前可用成果

B 已实现权威移动、房间生命周期、玩家生命、怪物追击/攻击、子弹碰撞、伤害/死亡事件和首关清场/团灭。
普通测试、go vet 和 Linux race 检查通过，39 个顶层测试及 1 个示例覆盖移动与战斗核心；[验证范围与限制](../verification/combat-core/README.md) 已记录。
A 已交付 v0 协议、TCP/Session、DTO 转换、Join/Leave 编排、快照/事件 dispatcher 和关闭通知。集成测试已用两个 Go TCP 连接跑通移动及首关清场事件，详见 [A/B 集成验证](../verification/network-core/README.md)。正式 gameserver、D 匹配器和 C++ 客户端仍未联调，不能视为全组验收通过。
原 [前三天计划](PHASE1-DAYS1-3.md) 仍保留为全组移动闭环验收基准；B 已按用户授权先行推进独立战斗代码。

无需网络即可查看战斗结果：在仓库根目录打开 PowerShell 7，执行：

```powershell
git fetch origin
# 首次检出该远端分支时执行；已有同名本地分支则切换后按团队流程更新。
git switch --track origin/codex/game-core-phase1
. ./scripts/env.ps1
go run ./server/cmd/core-demo
```

固定演示预期：第 25 Tick 清场，玩家 HP 100、剩余怪物 0、击杀 2、命中 4、开火 5。
这是离线功能演示，不是客户端联调、网络 Bot 或性能数据。
日常开发继续使用各自功能分支；B 分支由 D 评审后合入 develop，再由各成员同步，不直接将未联调变更合并 main。

## 各角色下一步交付

| 负责人 | 需要完成的工作 | 完成标准 / 交付给谁 |
| --- | --- | --- |
| A：协议、网络、Session | 把已完成的 convert/router 接入正式 gameserver；处理 Send=false、断线 Leave、停服连接回收；与 B/D 统一可靠队列策略 | Join 成功回执后才 InRoom；Reader 不阻塞；慢连接不静默丢事件；生命周期测试通过。协议字段同步 C/D |
| C：客户端、联调 | 移动与瞄准/射击输入；显示自己/队友/怪物 HP；消费完整快照和视觉子弹事件；关闭与断连处理 | 两个真实客户端可同房移动、攻击同一批怪物、看到一致清场/团灭状态；同 Room/ServerTick 对齐状态，提供日志或录像 |
| D：匹配、平台、验证 | 房间注册与分配；监听 Done 注销；断线 Leave 重试；从 TickSamples/Stats 接指标；组织 Bot 与异常验证 | 两名玩家成功入房后再触发本轮双人战斗；玩家/房间计数可回收；事件拥塞有关闭原因；提交真实联调与监控证据。负责 B 代码评审 |
| B：游戏核心 | 维护 StageIndex 等领域事件元数据；配合正式入口联调；已完成装备修改器、药水、奖励状态、下一关、性能指标和 Rule-Based Director，继续 D9 性能与隔离验证 | 双 TCP 战斗回归、领域测试和 race 通过；接口变更同步 A/C/D；未实现玩法不提供假成功返回 |

## 接口对齐项

以下为 A/B 当前共同实现；仍有差异的条目以集成验证记录为准，不在 B 分支自行改 Message ID。

| 项目 | 当前约束与接入要求 |
| --- | --- |
| 实体身份 | uint64；玩家 ID 为 1..2^63−1，怪物/子弹使用高半区。C++ 使用 64 位；展示到 JavaScript/JSON 时避免 Number 精度损失 |
| 输入 | Seq 从 1 严格递增、不回绕；Direction / Aim 是二维向量，Shoot 表示按住状态；客户端不发送权威位置或伤害 |
| 输入确认 | 返回已在 Tick 中实际应用的最新意图序号；不表示之前每个输入包各模拟过一次。C 的预测/重放需按此语义设计 |
| 坐标与频率 | 服务端 X/Y → 客户端 X/Z；30Hz 模拟、10Hz 完整快照；释放键发送零移动/Shoot=false，200ms 输入超时后在下一 Tick 停止 |
| 配置 | 使用 room.DefaultConfig() 后覆盖值；新增 Combat / EventCapacity 不可漏填为零。B 不负责 .env 加载 |
| 启动关卡 | StartStage 是可信服务端编排命令，复制 Plan 并返回回执；只允许 Waiting 且至少一名存活玩家。本轮双人联调由编排方等待两人 Join 成功 |
| 中途加入 | Playing / StageClear / Failed 不接收新玩家；lobby 应选择可加入房间。既有 Session/Player 重复绑定仍幂等 |
| 快照 | 一个 dispatcher 消费再分发；含玩家、MonsterView、Stage，全量集合缺失的怪物要移除；不含子弹列表 |
| 事件 | A 已实现单 dispatcher 和七类消息映射；StageIndex 由 B 事件产生时携带，不能从可能滞后的 10Hz 快照推断。Spawn/Destroy 驱动视觉子弹，Damage/Death/Stage 驱动反馈 |
| 拥塞与关闭 | 快照已有 capacity=1 的 Latest Wins；Room 事件出口满时关闭并记录 event_backpressure。网络可靠队列的 Send=false 目前仍被 dispatcher 忽略，A/D 必须完成策略和慢 Socket 验收 |
| 奖励与下一关 | B 已提供 StartReward、ChooseReward、定向 RewardUpdates、CompletedStage 和确定性 Director。A 仍需接协议路由、单播奖励出口、在线玩家 Ready 屏障和正式 gameserver 编排；C/D 按对应接口接入 |

## 联调顺序与验收记录

1. A/C 先完成 Frame、登录、Input 和 Snapshot 互通；B/D 按 Join 回执接入。不开关卡时仍能验证原移动闭环。
2. 两个真实客户端同房移动 5 分钟，通过原计划的身份校验、跨房隔离、断线移除测试。
3. A/D 接入 StartStage 与可靠事件，C 显示怪物、生命和子弹；检查射速不随发包频率加快、每只怪物只死亡一次、两端终局一致。
4. 验证清场与团灭两条路径；同 Tick 最后击杀且全队死亡应判 Failed。清场后不展示尚未实现的奖励/下一关入口。
5. D 配合 A 做真正的慢 Socket/可靠队列饱和、10 Bot / 5 房间 10 分钟移动检查及真实监控；新增战斗负载参数另行记录，不能直接沿用移动性能结论。

每次联调记录同一提交号、协议版本、各机环境/端口、步骤、期望/实际、日志、责任人和复测结果；未执行项写明未执行。
本机已知 metrics 默认端口 9091 被 Windows 保留，D 需选可绑定端口并同时修改服务端监听与 Prometheus target；其他机器先检查实际端口。
环境版本仍见 [SETUP.md](../SETUP.md)。这次战斗实现未新增外部依赖，仓库保留原骨架预置的 Go 依赖供各角色接入。
