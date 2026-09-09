# 恢复令牌持久化约定

状态：Redis 实现已完成，等待成员 A 接入 Session 生命周期。

`server/internal/persistence.ResumeTokenStore` 把 Redis 的命令、键前缀、TTL 和错误语义隐藏在两个操作后面：

- `Issue(ctx, token, sessionKey)` 原子写入新令牌，已有令牌不会被覆盖。
- `Consume(ctx, token)` 原子读取并删除令牌，保证一个令牌最多成功恢复一次。

令牌和会话键都是不透明字符串，长度限制为 1–256 字节。持久化模块不定义 Session、Player 或 Room 数据结构，也不生成令牌；令牌必须由成员 A 使用密码学安全随机源生成。Redis 键使用版本化前缀 `odyssey:resume:v1:`，过期清理由 Redis TTL 完成。

Redis 连接或命令失败会作为包装错误返回；不存在、已消费和已过期统一返回 `ErrResumeTokenNotFound`。调用方不得在日志、指标标签或客户端错误消息中输出原始令牌。

## 验证

普通单元测试不连接 Redis：

```powershell
pwsh -File scripts/test/check.ps1
```

显式集成测试会启动 Compose 基础设施，从本地 `.env` 读取 Redis 端口和密码，并验证冲突、单次消费与 TTL：

```powershell
pwsh -File scripts/test/integration.ps1 -Target persistence
```

## 待成员 A 确认

- `sessionKey` 是进程内 Session ID、跨服路由键，还是版本化序列化记录。
- 新连接完成绑定与 Full Snapshot 发送失败时，是否允许重新签发令牌。
- 重连窗口长度和主动退出时的令牌撤销策略。
