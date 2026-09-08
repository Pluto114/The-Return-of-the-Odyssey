# 最新协作需求：接入 B 的移动与首关战斗核心

同步日期：2026-09-08。交付分支：`codex/game-core-phase1`。
本页是本轮协作入口；详细接口以 [首关战斗契约](../architecture/COMBAT-CORE.md) 为准，移动基础见 [原移动契约](../architecture/GAME-CORE-PHASE1.md)。

## 当前可用成果

B 已实现权威移动、房间生命周期、玩家生命、怪物追击/攻击、子弹碰撞、伤害/死亡事件和首关清场/团灭。
普通测试、go vet 和 Linux race 检查通过，39 个顶层测试及 1 个示例覆盖移动与战斗核心；[验证范围与限制](../verification/combat-core/README.md) 已记录。
其他角色的代码尚未在该分支接入，不能将这些测试视为全组联调通过。
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
| A：协议、网络、Session | 定义/生成移动与战斗消息；DTO→game.Input；Join/Leave 路由；消费 Snapshot / Events 并投递 Session 队列 | Go↔C++ 固定样例通过；Join 成功回执后才切换 InRoom；真实输入能移动/开火；死亡/子弹事件不静默丢失。协议字段表同步 B/C/D |
| C：客户端、联调 | 移动与瞄准/射击输入；显示自己/队友/怪物 HP；消费完整快照和视觉子弹事件；关闭与断连处理 | 两个真实客户端可同房移动、攻击同一批怪物、看到一致清场/团灭状态；同 Room/ServerTick 对齐状态，提供日志或录像 |
| D：匹配、平台、验证 | 房间注册与分配；监听 Done 注销；断线 Leave 重试；从 TickSamples/Stats 接指标；组织 Bot 与异常验证 | 两名玩家成功入房后再触发本轮双人战斗；玩家/房间计数可回收；事件拥塞有关闭原因；提交真实联调与监控证据。负责 B 代码评审 |
| B：游戏核心 | 配合上述适配；处理契约/模拟问题；继续装备修改器、药水、奖励状态、下一关和 Director 算法 | 接口变更同步 A/C/D；领域测试与 race 通过；奖励/下一关真正实现前不提供假成功返回 |

## 接口对齐项

以下为当前 B 实现的约束，A 负责将其转为正式协议字段并与 C/D 对齐；这里不自行分配 wire 消息 ID。

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
| 事件 | 一个 dispatcher 消费 Events；显式映射领域枚举，补 Room ID。Spawn/Destroy 驱动视觉子弹，Damage/Death/Stage 事件驱动反馈 |
| 拥塞与关闭 | 快照可替换旧值，可靠事件不可替换；事件出口满时 Room 关闭并记录 event_backpressure。A/D 须通知客户端、注销房间，不能当作正常清场 |
| 留白 | Director 只有 Planner 接口；Seed 尚不生成随机布局；Reward/PreparingNextStage 只有预留枚举。StageClear/Failed 后不能直接重开下一关 |

## 联调顺序与验收记录

1. A/C 先完成 Frame、登录、Input 和 Snapshot 互通；B/D 按 Join 回执接入。不开关卡时仍能验证原移动闭环。
2. 两个真实客户端同房移动 5 分钟，通过原计划的身份校验、跨房隔离、断线移除测试。
3. A/D 接入 StartStage 与可靠事件，C 显示怪物、生命和子弹；检查射速不随发包频率加快、每只怪物只死亡一次、两端终局一致。
4. 验证清场与团灭两条路径；同 Tick 最后击杀且全队死亡应判 Failed。清场后不展示尚未实现的奖励/下一关入口。
5. D 配合 A 做真正的慢 Socket/可靠队列饱和、10 Bot / 5 房间 10 分钟移动检查及真实监控；新增战斗负载参数另行记录，不能直接沿用移动性能结论。

每次联调记录同一提交号、协议版本、各机环境/端口、步骤、期望/实际、日志、责任人和复测结果；未执行项写明未执行。
本机已知 metrics 默认端口 9091 被 Windows 保留，D 需选可绑定端口并同时修改服务端监听与 Prometheus target；其他机器先检查实际端口。
环境版本仍见 [SETUP.md](../SETUP.md)。这次战斗实现未新增外部依赖，仓库保留原骨架预置的 Go 依赖供各角色接入。
