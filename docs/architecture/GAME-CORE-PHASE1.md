# 角色 B：第一阶段核心与接入契约

状态：角色 B 已开始实现；本文件描述当前本地实现，待 D 评审、A/C/D 接入后再进行全组验收。
依据：[前三天计划](../plans/PHASE1-DAYS1-3.md)。房间与移动参数采用计划建议值，跨团队协议尚未由本次工作定稿。

## 1. 本次交付范围

| 位置 | 作用 |
| --- | --- |
| [game/world.go](../../server/internal/game/world.go) | World、输入意图、固定步长、完整状态副本 |
| [game/entity/player.go](../../server/internal/game/entity/player.go) | Player ID、二维位置、实际速度、输入确认序号 |
| [game/systems/movement.go](../../server/internal/game/systems/movement.go) | 方向限长、固定步移动、边界限制 |
| [room/room.go](../../server/internal/room/room.go) | 单 goroutine 房间、有界命令、Session 绑定、快照、生命周期、指标采样接口 |
| [room/example_test.go](../../server/internal/room/example_test.go) | 会随 go test 编译执行的最小接入示例 |

生产代码只使用 Go 标准库和本模块，不引入新依赖，不导入 protobuf、network、lobby、metrics 或数据库包。
World 本身不是并发对象；线上只通过 Room 调用。A/D 不得保留、构造或修改线上房间的 World。

## 2. 默认参数与模拟语义

| 项目 | 当前实现 |
| --- | --- |
| Tick / Snapshot | 30Hz / 每 3 Tick 一次，即 10Hz；关闭时额外发最终空快照 |
| 模拟步长 | 恒定 1/30 秒，与输入包数、客户端 dt、墙钟调度间隔无关 |
| 房间容量 | 2；首人入房后即可移动，无战斗开局状态机 |
| 坐标 / 出生点 | 服务端 X/Y → 客户端 X/Z；地图 [0,20]²；暂时都出生于 (10,10)，移动后分开 |
| 速度 | 5 单位/秒；长度小于 1 的模拟摇杆方向保留幅度；较长向量限制至单位长度 |
| 非法浮点数 | NaN / Inf 拒绝，极大但有限的向量正常限长且不溢出 |
| 停止 | 零输入在下一次模拟应用；输入入队后满 200ms 失效，在随后第一次 Tick 停止（正常调度约 200–233.34ms） |
| 输入序号 | uint32，从 1 开始，严格递增；0 保留给未确认，不接受回绕。重连使用新会话，A/C 须对齐 wire 字段 |
| 入退房队列 | 容量 64，每 Tick 最多处理 16 条，先于移动输入处理 |
| 输入队列 | 容量 256，每 Tick 最多处理 128 条，入队满时返回 ErrQueueFull |
| 快照出口 | 容量 1，未消费的旧快照被替换；另可读取 LatestSnapshot 的独立副本 |
| 指标采样出口 | 容量 128，满时丢新样本并计数；不能用于无丢失审计 |
| 空房回收 | 空房持续 5 秒即关闭；从未有人进入的房间同样回收 |

可调参数通过 `room.DefaultConfig()` / `game.DefaultConfig()` 修改并传入构造函数；非法配置会返回错误。
本次没有增加 `.env` 加载器，A/D 在服务入口将实际配置传入；Tick 频率和快照周期在本阶段固定。

输入只更新意图，不直接改位置。一 Tick 消费到的最新有效序号覆盖之前的意图，只执行一次移动。
`LastProcessedInputSeq` 在 Step 应用意图时更新；已入队、仅消费、过期或被更新输入覆盖的命令都不单独确认。
该确认是“本 Tick 使用的最新意图编号”，不代表该编号前每个输入包各模拟过一次。后续 C 做预测/重放时必须沿用此时间语义或联合修订协议。
重复/过期序号不刷新超时。超时使用服务端入队时间，排队积压不会续命；不接受客户端时间戳、最终坐标或 Player ID 来控制移动。
Ticker 调度落后时允许掉 Tick，不无限补帧；因此必须同时观察实际 Tick 频率和工作耗时，不能仅看耗时推断实时性能。

## 3. A / D 接入接口

模块路径为 `github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room`，只能从 server 模块允许的 internal 范围内使用。
Bot 继续通过协议访问网络入口，不直接 import 该包。

| 接口 | 调用方与含义 |
| --- | --- |
| `room.Start(ctx, roomID, config)` | D 创建房间并登记到 lobby；成功即启动一个 owner goroutine。ID 非零且由服务器分配 |
| `r.Join(sessionID, playerID)` | A/D 提交绑定，返回单次结果 channel 和入队错误；**结果 channel 中收到 nil 后**才发送 MatchFound / 切换 InRoom |
| `r.Input(sessionID, game.Input{...})` | A 校验 Session、解码 DTO 后提交；Input 不包含 Player ID，由 Room 查绑定；成功只表示入队 |
| `r.Leave(sessionID)` | A 在 EOF/关闭时通知，D 配合清理匹配登记；重复 Leave 安全 |
| `r.Snapshots()` | A 指定一个 dispatcher 消费，再按 Session 编码投递。不要让每个客户端各竞争读取该 channel |
| `r.LatestSnapshot()` | 多个调用方可安全读取最新已发布快照，每次返回独立副本；可能比当前 Stats 落后 1–2 Tick |
| `r.TickSamples()` / `r.Stats()` | D 消费耗时样本和统计，不把 Prometheus 回调或网络操作放入 Room |
| `r.Done()` / `r.Close()` | D 监听 Done 后从注册表移除房间；Close 可重复调用并等待清理；父 context 取消也会关闭 |

完整示例见 [可执行示例](../../server/internal/room/example_test.go)。生成协议、Session 状态、TCP 入口及匹配算法仍由 A/D 实现，本次未改这些目录。

### 入退房结果与错误处理

- `Join/Leave` 的返回值先区分入队错误；若入队成功，结果 channel 恰好返回一个 error，然后关闭。Room 提前关闭时，未处理请求返回 ErrClosed。
- 相同 Session/Player 重复 Join 成功且不产生第二个实体；同 Session 改绑另一个仍在房中的 Player 返回 ErrSessionBound；已占用 Player ID 返回 ErrPlayerExists；满房返回 ErrWorldFull。
- Input 在 Join 尚未应用时返回 ErrNotJoined；A 必须等 Join 的成功结果。非法方向/序号在入队前拒绝；序号过期、绑定已变化或队列积压超时在 Tick 中拒绝，计入 RejectedInputs。
- 同 Tick 先 Leave 后重入时，旧绑定下排队的 Input 不影响新玩家，即使复用了 Session ID。正式重连仍应使用新 Session ID。
- 队列已满就返回 ErrQueueFull，不无限等待。输入可由 A 按过载策略丢弃/限流；**Leave 不可静默丢弃**，需重试至入队或房间关闭。A/D 应限制重复匹配请求，避免生命周期队列自身被刷满。
- 在正常调度下，默认队列中已接收的生命周期命令最多约 4 Tick 后应用；实际 EOF→移除的 2 秒指标需要 A 的通知与重试链路共同验收。
- 调用方取消等待不撤销已入队的 Join；如果连接同时断开，必须继续安排 Leave，或等结果后清理。不要把调用方放弃等待当作未入房。

### 快照与线程边界

每个快照含 RoomID、ServerTick、Closed 和按 Player ID 升序排列的全量玩家列表。
每个玩家含 ID、Position、Velocity、LastProcessedInputSeq；A 可从中提取当前会话的 self ack，转换成自己负责的 DTO。
快照不含 Session 身份凭据；C 按完整实体集合移除缺失实体，关闭快照表示房间清空。
发布、读取、channel 交付均隔离切片所有权；消费方修改副本不影响 live World 或另一读取方。

Room 只使用短临界区保护命令入队、Session 绑定查询及关闭门禁。World 和模拟状态始终只有 Room goroutine 写入。
房间内部没有 Socket Write、数据库访问、外部回调或为每个输入启动 goroutine；慢快照/指标消费者不会阻塞 Tick。
A 的 dispatcher 仍须使用 Session 的有界可靠队列和最新快照槽位；B 的出口无法代替真正 Socket Writer 背压测试。

## 4. D 的指标适配

`TickSample` 包含 RoomID、ServerTick、StartedAt、WorkDuration、当 Tick 消费的控制/输入条数和玩家数。
WorkDuration 覆盖消费命令、模拟、快照构建与发布，不含等待下次 Tick，以及样本投递和 Stats 发布本身。
`Stats` 还提供队列深度、QueueRejections、RejectedInputs、DroppedSnapshots、DroppedTickSamples、Closed。
队列深度是调用时读数，其余模拟字段来自最近 Tick，因此 Stats 不是与队列深度同一时刻的事务快照。

建议 D 在房间外消费 TickSamples 并 Observe 到 histogram；按同房间 ServerTick/StartedAt 差计算实际频率。
采样出口丢失时，必须报告 DroppedTickSamples，不能把不完整样本的 p99 当作完整验收结果。
在线会话数仍由 Session 统计；Room 的 Players 只代表入房人数。D 管理活跃房间计数并在 Done 后移除登记，不从零玩家推断房间已关闭。
当前没有实际 Prometheus endpoint、Grafana 图表或真实 Bot 数据；这些仍属于 D 的接入工作。

## 5. B 的测试与交接

在仓库根目录打开 PowerShell 7：

```powershell
. ./scripts/env.ps1
go test -count=1 ./server/internal/game/... ./server/internal/room/...
pwsh -File scripts/test/check.ps1
```

Linux 安装项目规定的 Go 版本和 GCC 后，从 server 目录执行：

```bash
GOWORK=off CGO_ENABLED=1 go test -race -count=1 -timeout=90s ./internal/game/... ./internal/room/...
```

World 测试使用确定 Tick 数；Room 并发/超时测试使用 Go `testing/synctest` 虚拟时间，验证真实 goroutine 和队列行为，不依赖人工按键或长时间 sleep。
虚拟时钟下的耗时不能当作性能结果。双客户端、真实 Socket 慢写以及 10 Bot / 5 房间 10 分钟测试须待 A/C/D 接入后执行。
本次检查记录见 [角色 B 验证记录](../verification/phase1-b/README.md)。角色 B 的实现和本地测试完成后仍须 D 评审、合并 develop，再记录全组阶段通过。
