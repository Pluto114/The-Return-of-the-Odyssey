# 角色 B：D9 游戏核心性能与隔离基线

日期：2026-09-12（Asia/Shanghai）  
分支：`codex/week2-game-loop`  
环境：Windows 11、Intel Core i9-13900HX（32 logical CPUs）、Go 1.26.8 windows/amd64，普通测试构建。

## 覆盖范围

- 真实时钟运行 50 个 Room，每房 2 名玩家和 64 只怪物，持续 10 秒；持续消费快照、事件、奖励与 TickSample。
- 24 个 Room 的确定性隔离测试，验证 Snapshot 的 RoomID、PlayerID、Seed、怪物状态和可靠事件不串房。
- 在 Room 命令边界比较 30Hz 与 300Hz 输入；两者移动距离和服务端实际发射数相同，最新合法意图分别正常确认。
- 分别测量 64 怪 AI Tick、64 怪 × 256 活跃子弹的碰撞 Tick、2 玩家 × 64 怪完整快照，以及 64 怪下一关 Director 决策。
- 对最慢的碰撞场景采集 CPU profile。

## 实测结果

50 房实时门：

| 指标 | 实测 | 验收线 |
| --- | ---: | ---: |
| Tick 样本 | 15,088 | 无缺失样本 |
| 每房 Tick 频率 | 29.986–29.995Hz | 29–31Hz |
| Tick work p99 | 1.0122ms | <33.33ms |
| 结束状态 | 50/50 Room Closed、Players=0 | 全部回收 |

五轮 1 秒微基准范围：

| 场景 | ns/op 范围 | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| AI64 | 6,229–14,932 | 528 | 2 |
| Collision64x256 | 759,754–806,912 | 2,576 | 3 |
| Snapshot2x64 | 20,529–22,981 | 8,992 | 9 |
| Director64 | 40,645–42,913 | 9,144 | 6 |

碰撞 profile 中 `systems.SegmentCircle` 占 34.46% flat CPU，`World.stepCombat` 累计占 96.89%，符合 256×64 碰撞对的预期。当前最坏微基准只占单房 33.33ms Tick 预算约 2.5%，50 房 AI 实时 p99 约占 3.1%；本轮不修改生产算法，避免在没有预算压力时增加空间索引的复杂度。

这些数据是服务端游戏核心的本机基线，不包含 TCP、protobuf、客户端渲染、Redis/MySQL 或 D 的 100 Bot 全链路负载，不能替代 D9 全系统性能门。

## 复现

在仓库根目录的 PowerShell 7 中：

```powershell
. ./scripts/env.ps1
pwsh -NoProfile -File scripts/benchmark/game-core.ps1 *>&1 |
    Tee-Object docs/benchmark/week2-b-d9-game-core.txt
```

脚本运行 50 房实时门、隔离与 300Hz 输入测试、五轮微基准，并输出碰撞 CPU profile 的 Top 15。原始输出保存在同目录的 `week2-b-d9-game-core.txt`。
