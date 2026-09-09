# A/B 网络与战斗集成验证

更新日期：2026-09-09。结论：已同步 A 的 `feature/network` 最新提交 `fe4d84c`。A 新增的正式 DTO 转换、Room Join/Leave、按玩家快照分发、七类战斗事件分发、最新快照槽和房间关闭通知，已与 B 核心合并到 `codex/network-core-integration`。测试装配已跑通双 TCP 客户端的登录、入房、移动、开关卡、射击、伤害、死亡与清场事件；正式 gameserver 与 D 的匹配器仍未接线。

## 版本与处理原则

- A 基线：`fe4d84cf29fd93422d961f3b73a9b3ff34ffd3c8`。
- B 基线：`775fb5a07909e483ba0bdfdfcdc00ce4f9210754`。
- 集成分支：`codex/network-core-integration`，独立工作区 `E:\The-Return-of-the-Odyssey-integration`。
- A 已把 B 的两个核心提交带入自己的分支。合并冲突以 A 的新协议和正式 `convert/router` 实现为基准，删除昨日临时 `protocolbridge`，避免保留两套 DTO 边界。
- 原工作区 `E:\The-Return-of-the-Odyssey` 中用户未提交的 `CMakePresets.json`、`server/go.mod`、`server/go.sum` 均未改动。
- 本轮未修改 `.proto` 的字段或 Message ID；协议仍由 A 审核、C 确认客户端兼容性。

## 本轮发现并处理的问题

1. A 已将协议重新对齐 B：Input Sequence 为 uint32、Aim 为 Vec2；快照使用 float HP 并携带玩家属性、怪物和 StageState；七类 B 事件均有 wire 消息。昨日为旧协议做的 uint64/角度/整型 HP 适配已撤销。
2. 保留并合并 EOF/Send 并发回归修复：Reader 关闭可靠队列时，Send 不会再出现 `send on closed channel`；队列准入与关闭由同一把锁串行化。
3. gameserver 的 `sendMessage` 原先忽略可靠队列拒绝，调用方会把失败当成功。现已在 `Send=false` 时返回明确错误。
4. A 的 Input converter 注释要求药水未实现时不能静默丢弃，但代码实际忽略 `use_potion=true`。现已返回 `ErrPotionUnsupported` 并增加测试。
5. A 的 EventDispatcher 原先从 10Hz `LatestSnapshot()` 读取关卡编号。StartStage 在非快照 Tick 执行时会把 `StageStartedEvent.stage_index` 发成旧值。B 的 `game.Event` 现直接携带权威 `StageIndex`，StageStarted/StageCleared/TeamDefeated 均在产生事件时写入；converter 与 dispatcher 不再依赖滞后快照。
6. 协议生成脚本成功提示已与实际消息生成结果一致。

## 验证范围

`server/cmd/gameserver/integration_test.go` 使用真实 loopback TCP、A 的 Frame codec/network.Server、开发登录、Session、convert、SnapshotDispatcher、EventDispatcher，以及 B 的 Room。测试侧仅用固定房间 99 代替 D 的匹配器。

双连接测试覆盖：分片 Ping、登录、等待 Join 回执、MatchFound、两名玩家分别移动、相同 ServerTick 的双方视图一致、Input Sequence 与 Frame Sequence 分离、StartStage、ProjectileSpawn/Destroy、Damage、Death、StageCleared。两端都必须收到同一个关卡编号和完整事件类型集合。

当前已通过：

- 协议 Go/C++/descriptor 重新生成。
- Windows `go test -count=1 -timeout=90s ./server/...`。
- Windows `go vet ./server/...`。
- Linux/WSL `go test -race -count=1 -timeout=90s ./...`；初次暴露 Router 测试读取未发布 Stats 的时序假设，修正等待 Tick 收尾后全包通过。
- server、bot `go mod verify`；Bot 仍无业务源码。
- core-demo 固定结果：第 25 Tick 清场，HP 100、怪物 0、死亡 2、命中 4、开火 5。
- C++ `odyssey_protocol` 在 MSVC 14.51 / vcpkg x64-windows 下重新编译通过。这只验证生成代码编译，不代表真实 C++ 客户端联调。

## 尚未完成及需要 A/D 决策的事项

| 负责人 | 当前问题 | 完成标准 |
| --- | --- | --- |
| A/D | 正式 gameserver 仍只处理 Ping/Login；Match/Input 分支未连接 A 新增 router/dispatcher，断线 Leave 与房间注册也未接入 | D 提供房间分配/注册接口后，入口异步等待 Join 回执，订阅输出，EOF 重试 Leave，Done 注销 |
| A/D | main.sendMessage 已返回发送拒绝，但 EventDispatcher 和 CloseWatcher 仍忽略 `sink.Send=false`，战斗事件或关闭通知仍可能静默丢失 | 统一 v1 策略并处理 dispatcher 返回值；慢 Socket 饱和测试证明要么完整投递，要么明确断开/恢复，不能静默丢失 |
| A/B/C | `docs/protocol/events.md` 仍写可靠队列满时“丢最早且不要断连”，与 B 的 event_backpressure 关闭约定及“可靠”语义冲突 | A/D 选定策略并同步文档、代码、指标和客户端恢复流程 |
| A/C | `sequence.md` 仍要求 gap >64 拒绝和强制校正；新 proto 注释也写拒绝 gap，但 converter/World 只拒绝 0、重复和倒退，没有实现跨度 64 | 决定保留或删除 gap 规则；若保留，定义非断线反馈并增加跨 Router 的恢复测试 |
| A/B/C | proto 注释写移动长度 <=1.05 由服务端拒绝；B 实际对任意有限向量归一化，converter 不做长度拒绝 | 选择“拒绝”或“归一化”作为唯一契约，更新文档和跨语言样例 |
| A | Server 取消只关闭 listener；现有连接、Writer 失败后的 Reader 唤醒、CloseAfterFlush 排空超时仍需生命周期测试 | 停服能在限定时间内关闭 listener 与全部连接，无 goroutine/房间绑定泄漏 |
| C/D | 尚无真实 C++ 客户端、Bot、鉴权/恢复、跨房隔离、5 分钟双人运行和压力/监控证据 | 按前三天计划记录提交、协议版本、环境、日志及实际结果后，才算全组联调通过 |

本次 TCP 测试中的固定房间不是生产匹配实现，Join 当前在测试 handler 内等待，不能直接复制进 Reader goroutine。正式接线应把可能阻塞到下个 Tick 的 Join/Leave 编排移出 Reader。

## 复现

在集成工作区根目录运行：

```powershell
. ./scripts/env.ps1
pwsh -File scripts/generate-proto/generate.ps1
go test -count=1 -timeout=90s ./server/...
go vet ./server/...
go run ./server/cmd/core-demo
pwsh -File scripts/build/build.ps1 -Target client
```

Linux/WSL 在 `server` 目录运行：

```sh
GOWORK=off CGO_ENABLED=1 go test -race -count=1 -timeout=90s ./...
```

以上检查不依赖 Docker/MySQL/Redis，不代表之前 Docker Hub 拉取问题已解决。协作入口见 [最新需求](../../plans/CURRENT-COLLABORATION.md)。
