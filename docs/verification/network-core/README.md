# A/B 协议集成验证

验证日期：2026-09-08。结论：A 的 TCP/Protobuf/开发登录可以连接 B 的 Room 并返回双人移动快照；这是测试装配通过，正式游戏入口与完整战斗联调尚未完成。

## 版本与工作区

- A：`origin/feature/network`，`0558a601ca0759256758be0b4cb5fd7950e8fe4b`，包含 `91fdf06` 的 v0 协议。
- B：`775fb5a07909e483ba0bdfdfcdc00ce4f9210754`。
- 合并提交：`aa983d3`，无冲突；本记录对应其后的适配与测试变更，分支 `codex/network-core-integration`。
- 本机使用独立工作区 `E:\The-Return-of-the-Odyssey-integration`。原 `E:\The-Return-of-the-Odyssey` 的 B 分支和未提交 go.mod/go.sum 修改保留。工具目录通过本机忽略的 junction 复用，不属于交付文件。
- 本轮未改 `.proto`，不替 A/C 决定协议语义。生成产物保持忽略，每台机器从协议源重新生成。

## 已做的适配与修复

1. `game.Input.Seq` / `Player.LastProcessedInputSeq` 改为 uint64，保留 Frame Sequence 的 uint32；超过 32 位的序号有 Protobuf 往返和 World 应用验证。
2. 新增 `server/internal/protocolbridge`，不让 game/room 依赖 Protobuf。Input 转换角度、校验有限数/移动长度、重复或超大序号跨度，药水请求明确返回未实现。调用方只在 Room.Input 入队成功后推进 lastAccepted，不能使用快照 ack 或 Frame Sequence 代替。
3. Snapshot 按接收者区分 self/其他实体，并使用该玩家实际应用的输入确认；保留高半区实体 ID。怪物 archetype_id 必须由配置提供映射，HP 不能无损转为 int32 时返回错误，不静默取整。
4. 复现并修复 A 的一个网络退出缺陷：Reader EOF 关闭发送 channel 后，Writer 仍阻塞，此时 Send 会 `panic: send on closed channel`。修复前新增回归测试稳定失败；修复后通过。通过锁将队列准入与关闭串行化，关闭队列时同步拒绝新发送；待 A 复核此最小修复。
5. 修正协议生成脚本的成功提示，避免生成了消息后仍提示空 schema。

## 已执行检查

| 检查 | 结果与边界 |
| --- | --- |
| 协议生成 | Go、C++、descriptor set 生成成功 |
| `go test -count=1 -timeout=90s ./server/...` | 全部通过，包含 A 的网络/Session、B 的移动/战斗、桥接与真实 TCP 测试 |
| `go vet ./server/...` | 通过 |
| server / bot `go mod verify` | 均通过；Bot 尚无业务源码 |
| Linux `go test -race -count=1 -timeout=90s ./...` | server 全包通过，包含新增 TCP 与 EOF 回归 |
| 离线 core-demo | 仍为第 25 Tick 清场，HP 100、怪物 0、死亡 2、命中 4、开火 5 |
| C++ 协议库构建 | 编译进行中，结果待补；不等于真实 C++ 客户端联调 |

真实 TCP 用例见 `server/cmd/gameserver/integration_test.go`：使用 A 的 Frame codec、network.Server、真实 Ping/Login handler 和 Session，测试侧替代 D 的匹配器分配固定房间 99，等待 B 的 Join 回执再切换 InRoom。两个 Go 测试端通过 loopback TCP 发包，覆盖分段写入 Ping、登录、匹配响应、输入入队、权威 Tick、按接收者发送快照，并比较相同 ServerTick 的双方位置。输入序号 1 与 Frame Sequence 900/1000 分开验证。

测试装配仅验证正常路径；无真实 C++ 玩家、正式匹配、鉴权、断线 Leave 重试、战斗事件 dispatcher 或慢 Socket 队列策略。测试结束显式关闭连接，不能据此认定 A 的服务停机资源回收已完善。测试 handler 的错误退出不是生产用输入拒绝策略，接线时需实现下表的不掉线处理。

## 必须对齐的事项

| 负责人 | 差异 / 当前处理 | 下一步完成标准 |
| --- | --- | --- |
| A/B/C | wire HP/maxHP/Damage 为 int32，B 权威 HP/伤害为 float64，允许小数。目前 Snapshot 对小数 HP 返回 ErrLossyHealth | 明确浮点、定点或一致的舍入语义，覆盖小数伤害与存活显示；当前适配不能用于任意战斗配置 |
| A/B/C | WorldSnapshot 无 Room ID、Stage/Failed/Closed，且无明确团灭消息 | 确定会话房间上下文、阶段恢复和团灭/异常关闭的表达，真实两端终局一致 |
| A/B/C | StageStarted.seed 与 StageCleared.difficulty_score 为 uint32；B Plan 对应值为 int64/float64，领域事件未含完整阶段元数据 | 明确范围与转换，并提供 stage index、seed、数量等来源；不截断强转 |
| A/B/C | B 事件无完整 archetype/子弹销毁原因，边界与清场回收也需映射；武器系统未实现 | 配置提供稳定 archetype；确认 reason、weapon_id 和 alive flags（当前暂按示例 alive=1、weapon_id=0）；补战斗事件桥接 |
| A/C | 协议要求输入跨度 >64 拒绝且不掉线并校正，但缺少明确的非终止拒绝/校正反馈约定 | 路由处理 ErrInputGap、重复输入、药水未实现和队列满，定义客户端恢复；禁止把所有适配错误直接用作断线原因 |
| A/D | events.md 建议 Reliable 满时丢最早且无 v1 补发，和原架构/B 的可靠事件溢出关闭策略不同。当前实际网络是单个 FIFO256，Send 满时返回 false，而 main.sendMessage 忽略返回值 | 统一可靠投递/失败语义；拆分最新快照槽与可靠队列，处理发送失败；慢连接饱和测试证明事件不会静默丢失或伪报成功 |
| A/D | 正式 gameserver 只完成 Ping/Login/Session 校验，Match/Input 尚无 Room 接线；测试只用固定房间 | 接入真实匹配、Join 回执、输入游标、单消费者 dispatcher、Leave 重试、Done 注销、客户端关闭通知 |
| A/C/D | 尚无 Go↔C++ 固定字节样例互解、两个真实客户端移动/战斗、Bot 压力和监控证据 | 按前三天计划执行并记录提交、日志、实际结果；本次不宣称全组验收通过 |

另有仅通过代码检查发现的 A 网络生命周期待查项：Serve 取消只关闭 listener；Writer 写失败和 CloseAfterFlush 完成后没有主动唤醒仍在读取的 Reader，run 先等待 Reader；发送排空未设超时。这些不属于本次已修复的 EOF/Send panic，需 A 增加独立回归后处理。

## 复现

在本集成分支仓库根目录运行（PowerShell 7，先完成 SETUP 工具配置）：

```powershell
. ./scripts/env.ps1
pwsh -File scripts/generate-proto/generate.ps1
go test -count=1 -timeout=90s ./server/...
go vet ./server/...
go run ./server/cmd/core-demo
pwsh -File scripts/build/build.ps1 -Target client
```

Linux/WSL 配置兼容的 Go 与 C 编译器，先生成协议，再在 server 目录执行：

```sh
GOWORK=off CGO_ENABLED=1 go test -race -count=1 -timeout=90s ./...
```

不依赖 Docker/MySQL/Redis，因此上述结果不代表之前 Docker Hub 拉取问题已解决。完整协作顺序见 [最新协作需求](../../plans/CURRENT-COLLABORATION.md)。
