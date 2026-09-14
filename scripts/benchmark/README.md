# 压测入口

角色 B 的服务端游戏核心基线使用：

```powershell
. ./scripts/env.ps1
pwsh -NoProfile -File scripts/benchmark/game-core.ps1
```

该脚本运行 50 房实时 Tick 门、多房隔离、300Hz 输入测试、AI/碰撞/快照/Director 微基准和碰撞 CPU profile。它不经过 TCP、protobuf、Redis 或 MySQL，结果见 [D9 游戏核心记录](../../docs/benchmark/WEEK2-B-D9.md)。

D 的全链路负载继续使用 `bot/cmd/loadbot`，补固定 Seed、100/500/1000/5000 并发、持续时间、warmup、环境记录与结果输出。原始结果和优化前后对比放入 `docs/benchmark/`；不得填写未实际测得的数据。
