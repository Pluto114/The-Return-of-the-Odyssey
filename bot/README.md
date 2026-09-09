# Bot 压测模块

`internal/load` 负责并发调度，`internal/client` 使用 A 的 Frame/protobuf 与正式 gameserver 完成 Login→Match→30Hz Input，并持续读取 Snapshot。`cmd/loadbot` 将两者组装为可执行压测入口。

调度模块通过注入 `Worker` 与协议实现解耦。未来的真实 Worker 必须遵守以下约定：

- 一个 Worker 代表一个 Bot 完整生命周期。
- 收到 Context 取消后及时退出；整场时限触发的取消视为正常结束。
- 自身连接、协议或状态机错误直接返回，由 Runner 聚合失败数量和第一个错误。
- Worker 不负责创建额外 goroutine 后立即返回，否则 Runner 无法保证测试结束时资源已释放。

运行 10 Bot / 5 房间、10 分钟验收：

```powershell
go run ./bot/cmd/loadbot -clients 10 -duration 10m -ramp 1s
```

命令结束后输出 JSON 汇总和 OS/架构/Go 版本；任何 Bot 协议或连接失败都会以非零状态退出。
