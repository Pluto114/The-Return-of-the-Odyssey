# Bot 压测模块

`internal/load` 负责并发调度，`internal/client` 使用 A 的 Frame/protobuf 与正式 gameserver 完成 Login→Match→30Hz Input。Bot 从权威 Snapshot 选择最近的存活怪物，发送 Aim/Shoot，消费可靠战斗事件，并且只有在收到已确认输入的 Snapshot 和 `StageCleared` 后才算成功。`cmd/loadbot` 将两者组装为可执行压测入口。

调度模块通过注入 `Worker` 与协议实现解耦。未来的真实 Worker 必须遵守以下约定：

- 一个 Worker 代表一个 Bot 完整生命周期。
- 收到 Context 取消后及时退出；如果尚未确认 Snapshot 或收到 `StageCleared`，整场时限触发会作为失败报告，避免把未完成战斗记为成功。
- 自身连接、协议或状态机错误直接返回，由 Runner 聚合失败数量和第一个错误。
- Worker 不负责创建额外 goroutine 后立即返回，否则 Runner 无法保证测试结束时资源已释放。

运行最多 10 分钟的 10 Bot 首关功能检查（当前需 A 先接通正式入口 StartStage）：

```powershell
go run ./bot/cmd/loadbot -clients 10 -duration 10m -ramp 1s
```

命令结束后输出 JSON 汇总和 OS/架构/Go 版本；任何 Bot 协议或连接失败都会以非零状态退出。

当前 Worker 首关清场后即退出，`-duration 10m` 是时限，不代表维持负载十分钟。奖励、Ready、三关、恢复和持续负载模式尚待 D 完成，具体要求见 [A / D 收尾清单](../docs/plans/WEEK2-AD-FINALIZATION.md)。
