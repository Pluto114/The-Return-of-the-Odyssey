# 角色 B：首关战斗验证记录

验证范围为基于 `c094fee` 的首关战斗增量，随该增量发布至 codex/game-core-phase1，供 D 评审和全组接入；尚未确认 develop 集成。
本轮实现没有新增外部依赖。开始开发前已有的本地 server/go.mod、server/go.sum 依赖裁剪未纳入本轮提交，仓库继续保留原骨架的预置依赖。

## 检查与复现

```powershell
. ./scripts/env.ps1
go test -count=1 -timeout=90s ./server/internal/game/... ./server/internal/room/... ./server/cmd/core-demo
go vet ./server/internal/game/... ./server/internal/room/... ./server/cmd/core-demo
go run ./server/cmd/core-demo
```

Windows / Go 1.26.8：上述普通测试和 vet 通过，包含原有移动/房间回归及新增战斗测试。
离线演示返回：`stage_clear`，第 25 Tick，玩家 HP 100，剩余怪物 0，2 次死亡事件、4 次命中、5 次开火。
这只是固定场景的功能结果，不能换算成真实服务器 Tick 性能或真人游玩效果。

Linux 的竞态检查命令（从 server 目录）：

```bash
GOWORK=off CGO_ENABLED=1 go test -race -count=1 -timeout=90s ./internal/game/... ./internal/room/... ./cmd/core-demo
```

已在 WSL Ubuntu 20.04、GCC 9.4.0、Go 1.26.8 上执行通过，包含新增战斗生产者/消费者和关闭路径，未报告数据竞态。

## 本轮新增验证场景

| 场景 | 验证内容 |
| --- | --- |
| 伤害结算 | 防御减伤、过量伤害裁剪、零伤害、非法数值、极大浮点攻击不溢出 |
| 连续碰撞 | 快弹穿越目标、起点重叠、切线、离开方向、静止弹；最近目标优先；移动目标不会按最终位置产生错误命中 |
| 输入与射速 | 30Hz / 300Hz 包数下均最多 5 发/秒；释放/超时停止射击；非法或零瞄准拒绝；极小/极大瞄准向量归一化 |
| 怪物 | 固定速度追击、最近目标平局按 ID 排序、攻击冷却、目标退出后换目标 |
| 死亡与关卡 | 伤害到死亡到清场；死亡实体只结算一次；团灭冻结战斗；同 Tick 双方死亡判负 |
| 资源边界 | 子弹容量/寿命限制；清场清理子弹；关卡非法参数不污染状态；中途加人/重复启动关卡被拒绝 |
| 确定性 | 重复执行同一计划和输入得到一致的快照、事件序列和事件 Tick |
| Room 接入 | 启动关卡命令复制 Plan；事件与快照对应真实战斗；事件队列实际满载后明确关闭；两个房间不串数据 |
| 并发 | 战斗中同时提交输入、读取/修改快照副本、消费/修改事件副本和关闭房间 |

测试使用确定 Tick 和 synctest 虚拟时间，Director 目前只有接口，未运行算法测试。
双 C++ 客户端、TCP/可靠事件交付、Prometheus/Grafana 和真实 Bot 尚未接入，均未宣称通过。
