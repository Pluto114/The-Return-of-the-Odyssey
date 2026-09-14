# 恢复令牌持久化约定

状态：Redis Token/路由存储与可选生产装配已完成，等待成员 A 将 A4 Session 生命周期接入该边界。

`server/internal/persistence.ResumeTokenStore` 把 Redis 的命令、键前缀、TTL 和错误语义隐藏在一组操作后面：

- `Issue(ctx, token, sessionKey)` 原子写入新令牌，已有令牌不会被覆盖。
- `Consume(ctx, token)` 原子读取并删除令牌，保证一个令牌最多成功恢复一次。
- `IssueRoute(ctx, token, route)` 保存版本化的 `session_id/player_id/room_id/generation` 绑定，不保存 World。
- `ConsumeRoute(ctx, token)` 原子消费后解析并校验绑定；损坏值不能再次使用。
- `Revoke(ctx, token)` 在永久离开或退出时幂等撤销令牌。

令牌和兼容接口的会话键都是不透明字符串，长度限制为 1–256 字节。持久化模块不生成令牌；令牌必须由成员 A 使用密码学安全随机源生成。Redis 键使用版本化前缀 `odyssey:resume:v1:` 加 Token 的 SHA-256 摘要，避免在 keyspace 中暴露可重放令牌；过期清理由 Redis TTL 完成。

Redis 连接或命令失败统一包含 `ErrResumeBackend`；不存在、已消费和已过期统一返回 `ErrResumeTokenNotFound`，路由损坏返回 `ErrCorruptResumeRoute`。调用方据此映射固定恢复结果，禁止在日志、指标标签或客户端错误消息中输出原始令牌和密码。

`OpenResumeService` 使用配置的操作超时建立 Redis 客户端并执行有界 `PING`。`ODYSSEY_RESUME_ENABLED=false` 时开发入口不连接 Redis；`production` 配置强制启用。`ODYSSEY_RESUME_TTL_SEC` 同时注入 Token TTL 与 A4 的断线宽限期，避免存储过期和 Room Leave 使用两套时钟。

## 验证

普通单元测试不连接 Redis：

```powershell
pwsh -File scripts/test/check.ps1
```

显式集成测试会启动 Compose 基础设施，从本地 `.env` 读取 Redis 端口和密码，并验证冲突、单次消费与 TTL：

```powershell
pwsh -File scripts/test/integration.ps1 -Target persistence
```

## 待成员 A 接线

- 登录后签发版本化 `ResumeRoute`，恢复成功后轮换新 Token，并在永久 Leave/退出时 `Revoke`。
- 消费后校验 Session/Player/Room/连接代次，旧 Connection 不得继续提交输入、Ready 或奖励。
- 绑定新连接后查询 `Room.ResumeState` 并先发完整快照；若绑定或发送失败，由 A 定义是否签发替代 Token。
- 调用 `ObserveReconnect` 映射 success/invalid_token/expired/backend_error，日志只记录固定原因与 Session/Room ID。
