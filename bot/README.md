# Bot 压测模块

当前已实现 `internal/load` 并发调度模块，负责客户端编号、并发启动、Ramp-up、整场时限、取消和有界结果汇总。网络连接、登录、匹配和游戏行为仍等待成员 A 定稿 TCP Frame 与 protobuf 消息后接入。

调度模块通过注入 `Worker` 与协议实现解耦。未来的真实 Worker 必须遵守以下约定：

- 一个 Worker 代表一个 Bot 完整生命周期。
- 收到 Context 取消后及时退出；整场时限触发的取消视为正常结束。
- 自身连接、协议或状态机错误直接返回，由 Runner 聚合失败数量和第一个错误。
- Worker 不负责创建额外 goroutine 后立即返回，否则 Runner 无法保证测试结束时资源已释放。

当前验证：

```powershell
pwsh -File scripts/test/check.ps1
```

在真实协议接入前不提供占位 CLI，避免产生“命令可运行即完成压测”的错误结论。
