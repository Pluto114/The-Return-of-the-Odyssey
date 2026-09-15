# 第二阶段收尾：A / D 需求与最终合并验收

更新日期：2026-09-14。状态：**统一联调基线已整合，最终版本待验收**。

**2026-09-15 状态补充（优先于下文历史描述）：** D 的 `a340ee6` 已进入 main，首关启动、同源装备显示表、Bot 多关/恢复/持续模式、真实指标与 Admin/看板、Redis 路由存储、MySQL 迁移/异步写入均已交付。单关已有真实 Bot 验证；三关、恢复和正式终局落库尚未打通。A 的 `1bfd796` 已有奖励/Ready/Director 与进程内恢复实现，但未与 D 的入口及持久化合并。A1/D1 已不需要从零实现，D2–D5 重心改为接线、可靠性与实际验收；A5 客户端接缝仍需处理。以下保留 9 月 14 日原始需求和 F01–F09 最终门槛，不能将其中“当前未实现”理解为最新代码状态。

本次按项目负责人要求将已交付增量同步到 `main`，方便所有成员从同一代码继续工作；这次同步不表示 D10 已通过，不创建最终发布标签。A、D 完成本页任务并提交共同版本的验收证据后，再合并最终版本。

## 1. 本次合入范围与真实状态

| 来源 | 提交 | 已有成果 |
| --- | --- | --- |
| 原主分支 | `d4809ce` | 登录、匹配、双人入房、移动快照、基础指标与第一阶段联调记录 |
| B：`codex/week2-game-loop` | `d5cff55` | 确定性首关、战斗与 AI、装备/药水、奖励轮次、Director、三关领域流程、ResumeState、GameResult、核心性能测试 |
| C：Fork `xxhsir` / `feature/week2-client-gameplay` | `94978b6` | 瞄准射击、怪物/血条/事件表现、奖励界面、Ready 入口、恢复状态机、预测与插值、四套 CTest |
| A：`feature/network` | `2b68e73` | 可靠队列饱和时隔离并关闭慢连接，关闭原因与 T10 背压回归 |
| D：`feature/week2-platform` | `263a031` | 根据权威怪物快照战斗的 Bot、首关清场判定、战斗指标定义及单元测试 |

推送时收到的后续进展（本次测试/合并范围之外）：A 分支已到 `33c26ab`，其中 `cffa848` 提交了匹配后首关启动，`33c26ab` 补充背压/坏事件日志关联；D 分支已到 `cf443a8`，提交了权威战斗指标接入。这些新提交尚未纳入本页所列基线或验收结论。**A1、D3 请优先验证并整合已有新提交，不要重复实现**；其余条目继续按实际差异推进，最终仍执行 F01–F09。

正式 `server/cmd/gameserver/application.go` 目前仍只编排登录、匹配、入房和输入，**没有首关 StartStage、奖励、Ready、Resume 的完整路由**。所以能够编译客户端和通过模块测试，不等于能在正式入口完成战斗与三关。新版 Bot 要求收到 StageCleared 才成功，直接对当前入口运行会因没有开关而超时；不要把 fake-server 单元测试当作实际战斗通过。

B 的领域逻辑无需重写，接入以下已有接口即可：

| 已有接口 | 使用方式与边界 |
| --- | --- |
| `game.NewFirstStagePlan(config, seed)`、`Room.StartStage(plan)` | 首关计划与启动；下一关仍调用 StartStage，必须等待命令回执并检查错误 |
| `Room.StartReward(catalog, seed, durationTicks)` | 清场后由可信编排调用，不能由客户端指定候选或属性 |
| `Room.ChooseReward(sessionID, equipmentID)`、`RewardUpdates()` | Session 解析玩家身份；Options/Applied 带 PlayerID，必须定向单播 |
| `Room.CompletedStage()`、`RuleBasedPlanner.Decide` | 查询已冻结的上一关 Plan/Performance，纯函数产生下一关和决策原因 |
| `Room.ResumeState(sessionID)` | 当前查询 Tick 的完整 Snapshot，另含该玩家可选的私有奖励恢复值；不能回放旧 World |
| `Room.GameResult(outcome)` | 返回复制的结算值，供 Room 外异步落库；不会自行创建 match_id 或写数据库 |

详细契约：[战斗](../architecture/COMBAT-CORE.md)、[装备奖励](../architecture/EQUIPMENT-REWARD.md)、[Director](../architecture/DIRECTOR.md)、[恢复与结果](../architecture/RESUME-GAME-RESULT.md)。本次检查范围见 [整合验证记录](../verification/week2-main-integration/README.md)。

## 2. 先冻结的 A / D 交接约定

1. **入口归 A。** A 维护 `application.go`、Session 状态、dispatcher 与生命周期；D 提供配置、存储和观察适配器。D 避免另建 gameserver 或第二套 Session，双方改同一入口前先交换接口。
2. **配置归 D、规则归 B。** D 启动时解析 `data/equipment/catalog.json`，将不可变 Catalog、奖励时长、关卡上限（演示至少三关）和 Director 配置交给 A。非法配置明确启动失败；Tick 内不读文件。
3. **协议归 A。** 在 `proto/` 增量定义字段、原因码和版本兼容策略，更新 `docs/protocol/`，同步生成并编译 Go、Bot、C++。保留已发布字段号，不维护手写生成代码。
4. **单出口分发。** `Events()`、`Snapshots()`、`RewardUpdates()` 各只由一个 dispatcher 消费；D 指标通过该出口的观察钩子接入，不能抢读。TickSamples 也扩展既有消费者。指标失败不改变权威结果。
5. **终局信封。** A / D 定义稳定的 match_id、room_id、结束原因与 B 的复制 GameResult；在 Room 销毁前取得结果并可靠交给 D。持久化幂等键必须跨重试稳定，不能只依赖进程内递增 room_id。
6. **恢复交接。** D 负责 Token/路由存储与原子消费；A 负责校验绑定、连接代次、宽限期与 Room 查询。一次性 Token 消费后必须续发新 Token，字段和失败语义由 A 冻结并同步 C/Bot。

## 3. A 的必做任务

### A1 / P0：先接通正式首关

- 两名玩家 Join 回执全部成功后，只调用一次 `StartStage`；保持 Reader 不等待 Room Tick。
- 复用已有七类战斗事件 dispatcher 和完整怪物快照；本周生命周期使用 320–327 段，不重复发送 400/401 同名事件。
- StartStage、发送或 Join 失败要有可追踪原因并回收房间/绑定；不能报匹配成功后永久停在 Waiting。
- 验收：真实 gameserver + 两个 Bot 首关清场；两个 C++ 客户端看到相同怪物、HP 和清场。另测团灭、同 Tick 最后击杀与全队死亡，终局只产生一次。

### A2 / P0：奖励、药水与权威属性闭环

- 清场后提交 `StartReward`，路由 RewardChoice 到 `ChooseReward`，用单一私有 dispatcher 发送 Options/Applied；失败也必须明确回复原因。
- RewardApplied 成功只在 World 实际应用之后发出。非法候选、越权、重复、过期不能改变属性；超时默认选择也产生单播 Applied。
- 将 `game.Input.UsePotion` 接入 `convert/input.go`，解除当前占位拒绝；`PlayerInput.use_potion` 字段已存在，无需新造输入消息。
- 补快照 WeaponID/RelicID/PotionID，以及客户端显示/预测所需的实际属性；AttackSpeed/冷却选定统一表示。RewardApplied 增加阶段和 Defaulted 等恢复判定信息时，由 A 分配字段号。
- 验收：两人收到各自候选；截止 Tick 当 Tick 可选、下一 Tick 默认第一项；槽位替换不累加旧效果；满血药水也只消费一次、PotionID 清零；非法输入不污染状态。

### A3 / P0：Ready、Director 与三关状态编排

- 以权威状态推进 `Playing(1) → StageClear(2) → Reward(3) → PreparingNextStage(4) → Playing(1)`；Failed=5 走失败结算。
- 奖励完成并且所有在线玩家 Ready 后，从 `CompletedStage()` 获取冻结值，调用 `RuleBasedPlanner.Decide`，验证结果并提交 `StartStage`。同关只生成/应用一次，重复 Ready 幂等。
- 断线时清理该 Session 的 Ready；恢复后重提。没有在线玩家时不能凭空连跳关。宽限期内保留玩家的奖励仍按截止 Tick 处理。
- 明确第三关完成后的结束策略和胜利结算点；团灭/离弃也必须向 D 交付结果，不能无限循环或销毁后才查询。
- 补关卡难度、两人 Ready 状态、实际 Director 决策摘要的协议表达；未实现全局 Modifier 时明确为空，不能伪造效果。
- Seed 使用 B 的 int64 语义，Difficulty 保留浮点精度；不能套用旧 400/401 消息的 uint32 字段后截断数据。
- 验收：固定 Seed 连续三关；Index 严格加一，难度单关变化在 -15% 到 +20% 内；装备和存活 HP 保留，死亡队友按规则复活；失败的 Plan 不部分写入 World。

### A4 / P1（最终版必需）：Resume 与完整生命周期

- 新 TCP 连接允许先发 ResumeRequest，不能被现有 `login required` 门挡住；登录/恢复成功返回可用的轮换 Token。
- 断线先进入宽限期，禁止立即 `Room.Leave`；过期才永久移除。使旧 Connection 失效并阻止其输入、Ready 和奖励选择继续生效。
- 通过 `Room.ResumeState` 发送当前完整快照和该玩家私有奖励；断线期间 Tick、伤害、死亡、奖励倒计时继续，不能回滚 HP/装备。
- 绑定保持 session_id/player_id/room_id；恢复响应与首快照顺序、room_id 和新 Token 传递形式必须定案。错误要区分不可恢复与暂时后端故障，并支持客户端结束等待。
- 验收：战斗/奖励阶段各恢复一次；并发双恢复仅一个成功；伪造、过期、重用 Token 拒绝；反复断连不产生重复玩家；停服后连接与房间回收。

### A5 / P0：牵头修复 C 的跨端接缝（C 复核，D 同步 Bot）

以下均在本次 C 代码中确认，不能仅等待服务端接线：

| 现状与位置 | 收口要求 |
| --- | --- |
| `main.cpp` Ready 仅在 state=3 可发；B 奖励完成后进入 4 | Ready UI/发送条件按权威 PreparingNextStage 与自身奖励完成状态统一，不能靠抢在状态切换前按键 |
| `main.cpp` 只按 in_room 持续发送输入；Session Reward 状态拒绝 PlayerInput | 客户端按阶段/存活/恢复状态门控输入并清预测；A 对合法阶段切换中的在途旧输入给出安全丢弃策略，避免正常清场就被断开 |
| C `PayloadCodec.h` 尚未编码 use_potion | 补按键触发、协议编码和权威槽位/HP 显示；一次操作只消费一次 |
| `Prediction.h` 固定速度 5，并将每个未确认包重放为一步 | 使用服务端实际 MoveSpeed；校正遵守“最新意图被 Tick 应用”的 ACK 语义，不能按收包数增加模拟。测装备加速、300Hz 输入、静止、死亡与切关收敛 |
| Resume 成功分支未完成 match_sent 门控，且首快照前可恢复输入 | 成功恢复后不得自动重发 MatchRequest；等当前完整快照建立视图，清旧 PendingInput，下一序号须高于 max(断线前最高已发送 InputSeq, LastProcessedInputSeq)，保留同 Session 计数而非降到 ack+1；只有新登录才重新匹配 |
| 连接等待/恢复响应等待与 Token 轮换未完整收口 | 自动重连和等待响应都应有界；超时/拒绝可回退或重试；第二次闪断仍可恢复 |
| `main.cpp` 端点硬编码为 `10.22.31.251:7777` | 提供有校验的 host/port 配置入口，默认本机，文档给出局域网配置；干净环境不能依赖成员个人地址 |

A 负责保证协议与跨端补丁在同一验收版本内落地；不把这些条目记为 C 已完整验收。涉及领域语义由 B 复核。

## 4. D 的必做任务

### D1 / P0：装备配置与启动装配

- 用 B 的 `equipment.Parse` 加载版本 1 目录并在启动时验证；向 A 注入目录、奖励时长、首关/Director/终局参数，更新 `.env.example` 和 SETUP。
- 从同一 Catalog 构建 C 显示表；当前 C CSV ID 为 1–6，B 实际为 1001/1002、2001/2002、3001/3002，收到真实奖励会显示 `equipment#... (config pending)`。
- 消除手维护双表；CI 检查版本/ID/槽位/名称/描述一致，客户端构建和运行目录能找到资产。目录损坏、缺失或非法值时明确失败。

### D2 / P0：Bot 三关、异常与持续负载

- 当前 Worker 首关清场就退出；补 RewardOptions/Choice/Applied、按固定 Seed 选项、药水、Ready、三关终局及 Resume。不得由本地计时假定通关。
- 分开“单局功能验收”和“持续负载”模式。当前 `-duration 10m` 只是最长运行时限，不能证明持续十分钟；负载模式应维持目标在线数/房间数并按统一终局策略补充对局。
- 输出按阶段分类的成功/失败、最后 Room/Stage/Tick、断线原因与恢复结果；非法/重复/超时选择要有专门场景。
- 验收顺序：2 Bot 真首关 → 10 Bot 各至少三关 → 100 Bot / 50 房间持续 10 分钟。A1/A2/A3 未接通时明确阻塞，不用 mock 报端到端成功。

### D3 / P0：真实指标、管理入口与看板

- 新增 `SetCombatSnapshot`、`ObserveDamage`、`ObserveStageResult` 目前只有测试调用；从 A 的唯一分发出口接真实事件和权威计数，避免重试重复累计。
- 在既有在线/房间/匹配/Tick work 指标上补实体、伤害、关卡、奖励 offered/chosen/defaulted/invalid、Director 输入输出/耗时、恢复、网络队列与拒绝。标签集合有界，room/stage/seed 用日志或管理查询关联。
- Gauge 随房间退出归零；不得另起消费者抢 Room 通道。AI/碰撞细分耗时尚无现成线上采样字段，确有需要时向 B 请求测量接口，不能把基准报告当实时值。
- D 负责最小真实 Admin HTTP/WS 数据与 Dashboard/Grafana 展示；当前 Dashboard 仍是环境占位。至少可看在线/房间、实体、Tick/队列和最近 Director 决策。
- 验收：打开真实房间时指标变化，清场/奖励/下一关/断线时值能对应事件，结束回收；Prometheus target UP，导出原始抓取结果和截图。500 Bot、看板美化保持后续项。

### D4 / P1（最终版必需）：Redis 恢复存储

- 复用已有 `ResumeTokenStore.Issue`（SETNX+TTL）与 `Consume`（GETDEL），补实际服务装配、Session/Room 路由记录、TTL/清理和错误分类。当前存储器未被生产入口调用，登录 Token 为空。
- 与 A 实现原子消费、绑定校验和轮换；并发恢复、重复 Token、过期、伪造、Redis 故障有真实集成测试，记录不可用时的会话清理策略。
- 不保存旧 World 并覆盖实时状态；不输出 Token/密码到日志；恢复期限与 A 的 Leave 时机使用同一配置。

### D5 / P1（最终版必需）：MySQL 异步幂等结算

- `server/migrations` 当前只有占位。补最小 MatchHistory/GameResult 迁移、写入器和查询验证；成长/装备归属表按周计划的最小范围完成或明确延期，不扩大为完整账号系统。
- 接收 A 的稳定结算信封，在 Room 外经有界队列写库；唯一约束/事务保证同一对局重复提交仅一条结果。记录重试、失败与可恢复的未写入结果。
- 数据库慢/断开不能拖慢 Tick；进程退出明确 flush/未完成处理策略。验收胜利、团灭、离弃、重复提交和数据库故障，保存实际 SQL 查询证据。

## 5. 实施与合并顺序

1. A、D 先同步本次 `main`。已有功能分支合入主分支后再继续；新分支从此基线创建。不要把自己的旧分支直接覆盖主分支，不提交生成代码、构建产物、缓存或 `.env`。
2. A1 与 D1 并行，先得到真实首关；A 同时冻结 A2–A4 所需字段、配置与存储接口。
3. A2/A3/A5 与 D2/D3 并行，得到装备/药水/Ready/Director 三关；B 复核规则，C 复核表现与协议。
4. A4 与 D4/D5 共同完成恢复和结算，再进行故障注入与持续负载。
5. A、D 各推送功能分支，并在 `docs/verification/phase2-final/` 提交验收记录；A↔C、B↔D 评审，集成者在同一个提交重跑下面门槛。
6. 所有必需项通过后合并最终版至 `main`，再按周计划创建候选发布标签。发现回归先修复复测，不把本次联调基线提前标记为最终版本。

## 6. 最终版本验收清单

以下都是待执行的最终门槛，不是本次已通过清单。

| 编号 | 负责人 | 场景 | 通过标准与证据 |
| --- | --- | --- | --- |
| F01 | A + C，B 复核 | 两台主机、两个真实 C++ 客户端战斗 | 同房/同关，怪物、HP、死亡和终局一致；清场、团灭各一条证据；正常切阶段不被断开 |
| F02 | A + D，B 复核 | 奖励合法/非法/越权/重复/超时、药水 | 私有候选不串玩家；属性只生效一次，截止边界正确；药水槽清零；名称/描述来自同版本目录 |
| F03 | A + D + C | 固定 Seed 双人连续三关 | 每关奖励、Ready、Director 均真实运行；难度受限；两人 Ready 可见；无旧实体/UI/预测残留；第三关明确结束 |
| F04 | A + D + C | 战斗/奖励期间短断线、第二次恢复、过期/伪造/重复 Token | 身份/房间/HP/装备保留；旧连接无效；无重复匹配/玩家；首快照后续号；超时最终移除 |
| F05 | D，A 配合 | MySQL/Redis 慢与故障、结算重试 | Tick 不等待 IO；同 match_id 仅一条结果；失败/积压可观测，恢复后有处置证据 |
| F06 | A + D | 慢 Socket、可靠队列饱和、坏帧、断线风暴 | A 已有 T10 必须保持通过；慢端隔离、其他房间继续 Tick；可靠事件/奖励不能静默丢失 |
| F07 | D，B 复核 | 100 Bot / 50 房间持续 10 分钟 | 正常场景成功率 100%，无 panic/race/非预期断连；每房 29–31Hz，Tick work p99 <33.33ms；队列不持续增长；退出后计数回收 |
| F08 | 全组，D 归档 | 干净 clone 构建与回归 | 生成 Go/C++ 协议；Server/Bot 全包 test/vet/Linux race；全部 CTest；Dashboard 构建；真实 Redis/MySQL 集成通过 |
| F09 | D 主持，非原开发者执行 | 发布体验 | 仅按 README，30 分钟内启动服务端和两客户端并完成一关；无个人 IP/隐藏文件依赖；操作、配置、关闭步骤完整 |

每份记录必须写：代码 SHA、协议/装备版本、OS/CPU/内存、工具链、配置和端口、完整命令、起止时间、期望/实际、原始日志/指标、截图或录像位置、失败条目和复测提交。未执行项明确写“未执行”，失败项不能计作通过。压测数据放 `docs/benchmark/`，最终联调记录放 `docs/verification/phase2-final/`。
