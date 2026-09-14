# 第二阶段：后续一周（D4–D10）完整玩法闭环冲刺计划

2026-09-14 更新：当前交付进展、统一 main 基线和 A/D 收尾任务见 [最新收尾清单](WEEK2-AD-FINALIZATION.md)。下文保留原周计划；这次提前同步已交付代码不等于 D10 最终验收通过，最终发布仍需完成对应门槛。

状态：D1–D3 跨主机联调已通过。两台主机可以进入同一房间、自由移动并看到对方。  
基线：以 `main` 当前稳定提交 `e55dad1` 为起点；D4 开工前先将 `develop` 快进到该基线，A/B/C/D 的新分支都从同一个 `develop` 提交创建。  
目标：用七个开发日把现有“联机移动原型”推进为可连续游玩、可观测、可压测、可答辩的首个完整 Roguelike PvE 版本。

本计划依据总体架构，但不是要求一周内实现所有远期设想。完成 D10 后，应达到约 85%–90% 的首版项目完成度：核心游戏循环完整，剩余工作主要是内容、美术、正式账号和更大规模优化。

## 1. D10 必须交付的完整体验

两名玩家执行以下闭环，连续完成至少三关：

> Login → Match → Join Room → Stage Start → 移动/瞄准/射击 → 怪物追击和攻击 → 清场或团灭 → 奖励宝箱 → 装备/药水生效 → Director 生成下一关 → 双人 Ready → Next Stage

同时满足：

- 服务端继续权威决定位置、子弹、碰撞、伤害、死亡、奖励合法性和关卡状态。
- 玩家断线后有短暂恢复窗口；成功恢复时保留 Session、Player、房间和装备，过期后明确拒绝并清理。
- Go Bot 能完成战斗、选奖励和进入下一关，而不只是移动。
- Prometheus/Grafana 和管理后台展示真实房间、实体、Tick、AI、碰撞、快照、Director 和断线数据。
- MySQL 只异步保存非实时结果；Redis 保存恢复令牌和在线路由；Room Tick 不访问数据库。
- Windows 客户端可从干净仓库构建；Server/Bot 的测试、vet、race 和端到端回归通过。

## 2. 本周范围和优先级

| 优先级 | 本周内容 | 完成定义 |
| --- | --- | --- |
| P0 | 正式房间启动战斗；瞄准/射击；怪物 AI；子弹/伤害/死亡；清场/团灭 | 两台主机看到一致结果，不能只用离线测试代替 |
| P0 | 奖励宝箱、装备修改器、药水、奖励超时与非法选择处理 | 服务端验证并在后续快照体现属性变化 |
| P0 | PerformanceMetrics、Rule-Based Director、按 Seed 生成下一关、三关循环 | 同输入可复现；每关难度变化受限；能连续完成三关 |
| P0 | Bot 战斗流程、真实指标、断线清理、全仓回归 | 100 Bot 验收和双人三关端到端通过 |
| P1 | Redis 恢复、MySQL 对局结果、最小管理 API/WS | 真实容器集成通过；持久化不进入 Tick |
| P1 | 客户端预测/校正、远端插值、战斗表现优化 | 不影响权威结果；网络抖动下移动观感稳定 |
| P2 | 500 Bot、Dashboard 视觉优化、更多装备和怪物配置 | P0 全通过后再做 |

宝箱在首版中定义为“清场后的奖励交互”：每名玩家出现一个宝箱/奖励面板，从最多三个合法选项中选择一个。暂不实现地图内随机掉落、复杂拾取碰撞和背包拖拽。

本周不纳入完成度阻断项：正式注册登录、支付、复杂账号安全、UDP、程序化地图、复杂美术/音频、5000 Bot、Kubernetes、多人组队大厅。它们不能挤占完整游戏循环。

## 3. 角色职责

| 角色 | 本周主责 | 不得跨越的边界 |
| --- | --- | --- |
| A：网络 / 协议 / Session | 战斗与奖励路由、状态机、可靠事件、Resume、正式入口编排 | Network 不写 World；协议变更必须同步 C/D 并重新生成 |
| B：游戏核心 / AI / Director | Monster FSM、战斗规则、装备/奖励、性能统计、Director、关卡转换 | Room 不写 Socket/Redis/MySQL；Director 不直接修改 World |
| C：C++ 客户端 / 体验 | 瞄准射击、怪物/子弹/血条、奖励 UI、多关 HUD、预测插值、恢复 UI | Network Thread 不直接修改渲染世界；客户端不决定命中/伤害/奖励 |
| D：平台 / Bot / 数据 / 可观测性 | Bot 完整行为、Redis/MySQL、指标、管理 API/WS、Grafana、压测与验收归档 | 实时 Tick 不访问数据库；Dashboard 不使用固定假数据通过验收 |

评审关系保持 A↔C、B↔D。涉及协议、Session 状态或 gameserver 入口的改动必须额外由 A 统一检查；涉及奖励、属性和 Director 语义的改动必须由 B 统一检查。

## 4. D4：正式战斗纵向闭环

当天目标：把 B 已有的离线战斗核心接入正式匹配房间，让两个真实客户端能够射击并清掉第一关。

| 角色 | 工作目标 | 当天完成标准 |
| --- | --- | --- |
| A | 冻结战斗消息用法；Match 完成并成功 Join 两人后异步提交 `StartStage`；把 PlayerInput 的 Aim/Shoot 路由到 Room；发送怪物快照和七类可靠事件 | Reader 不等待 Room Tick；只启动一次关卡；两个连接收到同一 StageIndex/ServerTick 的事件 |
| B | 提供首关默认 StagePlan 和确定性出生配置；复核射速、子弹寿命、碰撞、伤害、死亡、清场/团灭优先级 | 相同 Seed 和输入得到相同结果；发包频率不改变攻击冷却；最后击杀与全队死亡同 Tick 时按约定判定 |
| C | 鼠标计算 Aim，Space 发送 Shoot；解码 MonsterSnapshot 和 Projectile/Damage/Death/Stage 事件；先用几何图形表现 | 客户端不发送位置/命中；按住 Space 能持续射击；两端看到相同怪物数量、生命和终局 |
| D | Bot 增加 Aim/Shoot 和战斗事件消费；为 active monsters、projectiles、damage、stage result 预留真实指标 | 2 Bot 能自动完成首关；Bot 会验证快照确认和 StageCleared，而不是按时间假定成功 |

D4 协议决定：继续使用现有 320–327 段作为权威战斗事件；`WorldSnapshot.stage` 是当前状态真相。`stage.proto` 中 400/401 的同名生命周期消息本周不重复发送，避免客户端收到两套“开始/清场”；410–413 专用于奖励和下一关流程。A 在协议文档中标明该约定。

D4 联调门：两个真实 C++ 客户端进入同房，至少共同击杀三只怪物；双方原始事件中每个 Projectile/Death 只出现一次，最终 StageState 一致。

## 5. D5：怪物 AI 与战斗表现完整

当天目标：让首关同时具备可玩的敌人行为、失败路径和可读的客户端反馈。

| 角色 | 工作目标 | 当天完成标准 |
| --- | --- | --- |
| A | 补齐事件可靠队列饱和、坏事件、房间关闭和团灭断开行为；日志关联 room/player/entity/tick | 慢连接只影响自身；可靠事件不静默丢失；关闭原因可追踪 |
| B | 将 Monster AI 明确为 Idle→Chase→Attack→Dead FSM；10Hz 选最近存活玩家，30Hz 移动；实现换目标、攻击距离和冷却 | 固定 Seed 测试可复现；目标死亡后正确切换；两名玩家全死只产生一次 TeamDefeated |
| C | 渲染怪物、子弹、玩家/怪物血条、受击/死亡反馈、关卡 HUD；处理实体从完整快照消失 | 10Hz 快照下实体不残留；子弹只由 Spawn/Destroy 事件创建/删除；断连时 UI 仍可退出 |
| D | 接入 entity/monster/projectile、AI duration、collision duration、stage result 指标；Grafana 增加战斗面板 | 指标随真实战斗变化，房间结束后 Gauge 回收；标签集合有界 |

D5 联调门：分别完成“清场”和“团灭”各一次；两端 HP、死亡实体、怪物数量和终局一致。连续运行 10 分钟无事件泄漏、实体残留或非预期断连。

## 6. D6：奖励宝箱、装备和药水

当天目标：清场后进入真实 Reward 状态，每名玩家都能从宝箱选择奖励，属性在服务端生效。

| 角色 | 工作目标 | 当天完成标准 |
| --- | --- | --- |
| A | 路由 RewardOptions/Choice/Applied；Session 进入 Reward；拒绝重复、越权、过期和非候选选择 | RewardApplied 只在服务端应用成功后发送；重复包不重复叠加属性 |
| B | 实现 Weapon/Relic/Potion 数据模型、ADD/MULTIPLY 修改器、BaseStats→CurrentStats 重算、每玩家奖励选项、截止 Tick 和默认选择 | 修改器顺序确定；非法值不污染状态；MaxHealth 变化时当前 HP 规则明确；药水只消费一次 |
| C | 显示宝箱和最多三个奖励选项；展示名称、槽位和属性变化；支持选择、超时和 RewardApplied 错误 | 选择期间仍能处理网络事件；确认后 HUD 属性与后续快照一致；错误不会伪装成功 |
| D | 建立版本化装备配置校验；Bot 按 Seed 选择奖励；记录 reward offered/chosen/defaulted/invalid 指标 | Server/C++/Bot 对同一 equipment_id 语义一致；配置缺项启动即失败 |

静态装备数据按 ID 传输。仓库维护单一版本化配置源，客户端构建时复制显示所需字段；协议不重复发送完整静态装备表。

D6 联调门：两名玩家清场后分别选择不同奖励；下一次快照中的 Attack/Defense/MaxHealth/MoveSpeed 按规则变化。测试非法 ID、重复 Choice、超时默认选择和药水重复使用。

## 7. D7：AI Director 与连续多关

当天目标：完成 Clear→Reward→Director→Next Stage，并连续运行至少三关。

| 角色 | 工作目标 | 当天完成标准 |
| --- | --- | --- |
| A | 实现双人 NextStage Ready 屏障；只在奖励处理完成且所有在线玩家 Ready 后生成下一关；处理玩家在 Reward 阶段断线 | 重复 Ready 幂等；不能提前开关；StageIndex 单调增加 |
| B | 收集 ClearTime、TeamHP%、DPS、DeathCount、DamageTaken、EquipmentPower；实现 Rule-Based Director 和 Seed 出生布局 | Planner 是纯函数；同输入输出相同；单关提升≤20%、下降≤15%；生成 Plan 全部通过校验 |
| C | 显示关卡号、难度、全局 Modifier、两人 Ready 状态和 Director 决策摘要 | 连续切换关卡不残留旧怪物/子弹/奖励 UI；状态跳转清晰 |
| D | Bot 支持 Reward 与 NextStage；记录 Director input/output、生成耗时、难度曲线；管理端可查看最近决策 | 10 Bot 能各自完成至少三关；日志和指标可关联 room/stage/seed |

D7 联调门：固定 Seed 完成三关，难度变化符合上下限；重新运行得到相同 StagePlan。任何客户端不得直接提交 Difficulty、MonsterStats 或最终奖励效果。

## 8. D8：恢复、持久化与完整生命周期

当天目标：短暂掉线可以恢复；对局结果和成长数据可靠落库，且不影响实时 Tick。

| 角色 | 工作目标 | 当天完成标准 |
| --- | --- | --- |
| A | 接入 ResumeRequest/Response、连接重绑和恢复状态机；旧连接失效；过期/伪造 token 明确拒绝 | 成功恢复保持 session_id/player_id/room_id；不会在 Room 创建第二个玩家 |
| B | 定义可恢复的玩家运行状态和最终 GameResult；确认恢复期间输入、死亡、奖励和 Ready 语义 | 重绑不回滚 World；恢复后首次快照是完整权威状态 |
| C | 自动重连与恢复 UI；恢复时清空旧 PendingInput，再从首个完整快照建立视图 | 网络闪断后窗口不冻结；成功/失败状态明确；不会重放旧 Session 输入 |
| D | Redis 保存一次性 Resume Token、在线 Session 和 Room Routing；MySQL 建最小 MatchHistory/GameResult/PlayerProgress/EquipmentOwnership，异步批量写入 | Redis TTL/单次消费/冲突集成测试通过；同一对局结果只写一次；数据库慢不拖慢 Tick |

D8 联调门：战斗中断网后在宽限期内恢复，另一名玩家继续运行；恢复者保持身份、装备和 HP。超过 TTL 后恢复失败并在规定时间内从房间移除。清场/团灭结果在 MySQL 中恰好一条。

## 9. D9：同步质量、管理端与性能

当天目标：提高可演示质量，并验证新增 AI/奖励/Director 没有破坏并发和性能。

| 角色 | 工作目标 | 当天完成标准 |
| --- | --- | --- |
| A | 增加网络吞吐、快照字节、队列深度/拒绝指标；故障注入坏帧、半包、慢读、断线风暴 | 坏客户端不影响其他房间；停服后连接和 goroutine 回收 |
| B | profile AI、碰撞、快照与 Director；只优化有数据证明的热点；补多房隔离和高频射击测试 | Tick 仍为 29–31Hz；无跨房实体/事件；300Hz 输入不提高移动或射速 |
| C | 完成本地预测/服务器校正和远端/怪物插值；整理操作说明和低成本视觉反馈 | 预测误差能收敛；抖动下远端运动连续；权威快照始终可覆盖本地状态 |
| D | 最小 Admin HTTP/WS 输出真实房间详情；Dashboard/Grafana 展示在线、房间、实体、Tick、AI、碰撞、快照、Director；执行 100 Bot | 100 Bot / 50 房间 / 10 分钟无崩溃和非预期断连；结束计数归零；500 Bot 仅在 100 Bot 通过后测试 |

D9 性能门：Server/Bot 全包 race 通过；100 Bot 下每房 Tick 频率 29–31Hz，Tick work p99 <33.33ms，事件队列无持续增长。记录 CPU、内存、OS、Go 版本、构建模式和原始指标。

## 10. D10：冻结、发布与答辩演练

当天只接受阻断缺陷修复，不新增玩法。

1. 从干净 clone 重新生成协议，构建 Server、Bot、C++ 客户端和 Dashboard。
2. 运行 Server/Bot 全包 test、vet、race；运行全部 CTest、协议黄金样例和 Docker 集成测试。
3. 两台真实主机完成三关：至少一次选择装备、一次使用药水、一次断线恢复，并演示清场和团灭。
4. 运行 100 Bot / 50 房间 / 10 分钟回归；保存原始报告和 Grafana 截图。
5. 检查 MySQL 对局结果、Redis TTL/清理、进程退出后的在线/房间计数和 goroutine。
6. 更新 README、SETUP、协议表、运行命令、架构图、验收记录和已知限制；删除过时的“尚无可执行入口”等描述。
7. 所有 P0 门通过后，将 `develop` 快进/合并到 `main`，创建候选标签 `v0.2.0-phase2`。

D10 发布门：一名没有参与开发的成员只阅读 README，能在 30 分钟内启动服务端和两个客户端并完成一关。任何必需步骤依赖个人机器隐藏配置，都算发布阻断。

## 11. 统一接口约定

- `PlayerInput` 只携带 InputSeq、Move、Aim、Shoot、UsePotion；坐标、命中、伤害和奖励结果永远由服务端决定。
- Room 仍是 World 唯一写入者。网络、Director、Redis、MySQL 和管理端都不能直接修改 World。
- Monster AI 10Hz 只更新决策/目标，移动和战斗仍由 30Hz 固定 Tick 应用。
- Snapshot 是 10Hz 完整状态；缺失实体必须删除。Projectile 继续由可靠 Spawn/Destroy 事件维护。
- RewardOptions 按玩家生成并由服务端保存；客户端只提交候选 equipment_id。
- Director 输入来自上一关冻结后的 PerformanceMetrics，输出新的 StagePlan；生成失败不得部分修改关卡。
- Redis/MySQL 写入走 Room 外部异步队列；队列必须有界并有失败指标。实时模拟不得等待数据库。
- 客户端重连后必须以首个完整快照重建状态；旧 PendingInput 和旧可靠事件不能跨 Session 重放。

## 12. 一周验收测试清单

| 编号 | 验收内容 | 预期 |
| --- | --- | --- |
| W01 | 两个真实 C++ 客户端首关射击 | 双方看到相同怪物、子弹、HP、死亡和清场 |
| W02 | Monster FSM、目标死亡与换目标 | 10Hz 决策确定，攻击距离/冷却正确，无攻击已死玩家 |
| W03 | 同 Tick 最后击杀与全队死亡 | 只产生一次约定终局，不同时 Clear 和 Failed |
| W04 | 30Hz 与 300Hz 输入/射击对比 | 位移和射速上限一致，不按包数加速 |
| W05 | 奖励合法、非法、重复、超时 | 只应用一次合法选择；非法不污染属性；超时使用明确默认 |
| W06 | ADD/MULTIPLY、槽位替换、药水 | CurrentStats 重算正确，BaseStats 不被污染，药水只消费一次 |
| W07 | 固定 Director 输入与 Seed 重跑 | StagePlan、出生布局和难度一致，变化幅度在上下限内 |
| W08 | 连续三关双人流程 | 状态严格按 Fight→Reward→Next 循环，无旧实体/UI 残留 |
| W09 | 战斗中断线与恢复 | 宽限期内身份/房间/装备/HP 保留，Room 不产生重复玩家 |
| W10 | 过期/伪造/重复 Resume Token | 明确拒绝；token 单次消费；旧连接不能继续发输入 |
| W11 | MySQL/Redis 故障或延迟 | Tick 不阻塞；失败可观测；结果写入幂等或进入重试/死信 |
| W12 | 慢客户端、坏帧、断线风暴 | 其他玩家和房间继续递增 Tick，资源最终回收 |
| W13 | 100 Bot / 50 房间 / 10 分钟 | 全流程成功率 100%，无 panic/race，Tick 达标，结束计数归零 |
| W14 | Grafana + Admin 实时观察 | 数值随真实战斗、关卡和断线变化，不使用固定假数据 |
| W15 | 干净 clone 发布演练 | 按文档可构建、启动、完成一关并正常关闭 |

## 13. 每日合并和联调节奏

- 每天开始：四人同步前一日提交、当日协议/接口和阻塞，限定 20 分钟。
- 协议和共享配置改动在当天前半段完成；C/D 当天必须重新生成并编译，不允许维护私有字段。
- 每个功能切成可独立评审的小提交；不要把七天工作积成一个大分支。
- 每天至少预留最后 90 分钟在同一 `develop` 提交上联调；最后 30 分钟只修阻断缺陷。
- 每日结论写入 `docs/meeting/day-N.md`：提交号、环境、步骤、期望、实际、日志、失败和 Owner。
- `main` 始终保持可演示。D4–D9 合入 `develop`，只有完成当日门禁且无已知 P0 回归才同步稳定节点；D10 统一发布。

建议分支：

- A：`feature/week2-network-orchestration`
- B：`feature/week2-game-loop`
- C：`feature/week2-client-gameplay`
- D：`feature/week2-platform`

## 14. 范围失控时的削减顺序

如果进度落后，按以下顺序削减，不能牺牲服务端权威和完整循环：

1. 取消额外怪物种类、额外装备数量和视觉特效，只保留一套数据驱动样例。
2. 将 500 Bot 降为扩展项，保留 100 Bot 强制验收。
3. 简化 Admin 页面布局，保留真实 API、核心指标和 Grafana。
4. 降低预测/插值的精细程度，保留权威快照和可玩的基础显示。
5. MySQL 只保存最小 MatchHistory/GameResult，成长查询界面延后。

不得削减：真实双客户端战斗、奖励合法性、Director 三关循环、断线清理、Bot 全协议、race、干净构建和真实指标。

## 15. D10 后允许留下的工作

完成本计划后，项目应已具备完整技术主线。后续可以继续做：更多怪物/装备/关卡内容、美术音效、正式账号、好友组队、排行榜、客户端设置、安装包、云部署、500/1000 Bot 优化和答辩材料。它们是在已完成闭环上扩展，不再是“游戏无法完整运行”的基础缺口。
