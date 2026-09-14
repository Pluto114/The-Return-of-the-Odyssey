# Bot 压测模块

`internal/load` 负责并发调度，`internal/client` 使用 A 的 Frame/protobuf 与正式 gameserver 完成 Login→Match→战斗→奖励→Ready→连续三关。Bot 从权威 Snapshot 选择最近的存活怪物，发送 Aim/Shoot，仅在 Playing 期间发送 30Hz Input；奖励按固定 Seed 选择，也可运行非法、重复和超时场景。收到 Resume Token 后，瞬断会进行一次有界恢复。只有每关战斗输入得到 Snapshot ACK，且收到对应 `StageCleared`，对局才算成功。

调度模块通过注入 `Worker` 与协议实现解耦。未来的真实 Worker 必须遵守以下约定：

- 一个 Worker 代表一个 Bot 完整生命周期。
- 功能模式下收到 Context 取消时，如果尚未完成目标关数，整场时限会作为失败报告，避免把等待时间记成通关。
- 持续模式会在每局完成后建立新会话并继续补充对局，直到统一时限结束；因此 `-duration` 在该模式才代表维持负载的时间。
- 自身连接、协议或状态机错误直接返回，由 Runner 聚合失败数量和第一个错误。
- Worker 不负责创建额外 goroutine 后立即返回，否则 Runner 无法保证测试结束时资源已释放。

按验收顺序运行单局功能检查：

```powershell
go run ./bot/cmd/loadbot -mode functional -clients 2 -stages 1 -duration 3m -ramp 0s
go run ./bot/cmd/loadbot -mode functional -clients 10 -stages 3 -duration 10m -ramp 1s -seed 1
```

100 Bot / 50 房间持续 10 分钟：

```powershell
go run ./bot/cmd/loadbot -mode sustained -clients 100 -stages 3 -duration 10m -ramp 30s -seed 1
```

奖励异常场景分别运行：

```powershell
go run ./bot/cmd/loadbot -mode functional -clients 2 -stages 3 -reward-scenario invalid
go run ./bot/cmd/loadbot -mode functional -clients 2 -stages 3 -reward-scenario duplicate
go run ./bot/cmd/loadbot -mode functional -clients 2 -stages 3 -reward-scenario timeout
```

命令输出 JSON，包含各阶段成功/失败数、完成对局/关数、奖励/Ready/药水、最后 Room/Stage/Tick、断线原因和恢复次数；任何非预期协议或连接失败都会以非零状态退出。`-use-potion` 和 `-resume` 默认启用，可按场景关闭。

Bot 侧状态机与 fake TCP 回归已完成；正式 gameserver 的 A2/A3 奖励、Ready、三关路由以及 A4 Token 签发尚未进入当前主线，所以以上真实 E2E 目前仍是阻塞项，不能用单元测试结果代替。具体门槛见 [A / D 收尾清单](../docs/plans/WEEK2-AD-FINALIZATION.md)。
