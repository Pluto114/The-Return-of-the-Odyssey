# D 交付同步与复查（2026-09-15）

结论：D 的 `a340ee6` 已由成员推入 `main`，本次本地直接快进，无合并冲突；随后修复了 D 自己记录的结算关闭遗漏问题。当前可确认的是单关平台集成，仍不是完整三关最终版。

修复代码提交：`4083e96bb6f34d209d338f10a6741da640ef0b5b`。后续提交仅更新文档与验证记录。

## 已完成

- 正式匹配入口按配置启动首关；装备目录在服务端校验，并在 C++ 构建时生成同源 `equipment.tsv`。
- Bot 已具备奖励、Ready、多关、恢复和持续模式代码；本次实际复查范围为首关。
- 真实战斗指标、Admin HTTP/WS、Dashboard/Grafana 已接入；本次验证 HTTP 状态和权威战斗计数，并构建 Dashboard。
- Redis Token/路由存储、MySQL 结果迁移及异步写入模块已装配；主线仍待 A 正式调用恢复与终局接口。
- 关闭超时后继续处理正在重试和队列内已接收结果，尝试写入独立 Context 的失败记录；失败有计数和 `ErrResultDeadLetter`，worker 完成后才关闭数据库/文件。

关闭清理共用一个 `AttemptTimeout` 预算，不按队列长度重复分配。它是 Context 预算，不是磁盘卡死时的硬墙钟上限；本地文件 write/Sync 不能被 Context 强制中断。失败记录介质不可用时仍需处理返回错误和未保存计数，不能无条件宣称所有结果已持久化。

## 本次实际验证

| 检查 | 结果 |
| --- | --- |
| 统一协议生成 | 固定 protoc 33.4 / protoc-gen-go，Go/C++/descriptor 生成成功 |
| Windows Server/Bot | 两模块独立 `GOWORK=off`，统一 check.ps1 的依赖下载/校验、全包 test 与 vet 通过；关闭修复后复跑通过 |
| 关闭回归 | 重试中关闭、处理中加多条排队结果、失败记录写入失败、共用截止、文件关闭顺序，五项回归各重复五次通过 |
| Linux race / vet | WSL、Go 1.26.8、CGO/gcc，Server/Bot 独立全包 `go test -race ./...` 与 `go vet ./...` 均通过（18 / 3 个有测试包） |
| C++ | 客户端构建成功；CTest 4/4 通过；生成表包含版本 1 的六个服务端装备 ID |
| Dashboard | npm ci 与生产构建通过 |
| 真实 TCP 首关 | 10/10 Bot 成功，0 失败，5 个双人房间清场，约 1.474 秒 |
| Admin / Metrics | health、status、metrics 返回 200；峰值在线 10、房间 5、怪物 15、投射物 30；伤害累计 600、清场计数 5 |

真实 TCP 使用独立随机 loopback 端口和本次构建的 gameserver/loadbot。参数为 `-mode functional -clients 10 -stages 1 -resume=false -use-potion=false -duration 30s -ramp 0s -seed 1`，服务端明确禁用 Resume/Results。测试进程均已停止，没有修改正在运行的用户服务。

原始 [Bot 输出](d-sync-2026-09-15/ten-bot-one-stage.json) 和 [采样摘要](d-sync-2026-09-15/tcp-smoke-summary.json) 已归档。摘要以 D 主线 `a340ee6` 为基线；测试期间的关闭修复不在此 TCP 场景覆盖路径内，关闭可靠性由独立回归与 race 验证。`matches_completed` 是 Bot 各自的完成数，不是独立房间数。

## 还差什么

1. **A 与 D 的正式入口整合。** A 分支 `1bfd796` 已有奖励、Ready/Director、进程内 Resume，尚未合入此主线；必须保留 D 的配置/指标/存储接线，补 Redis Token 轮换、在线 Ready 语义、三关终局与稳定 match_id 的结果提交。
2. **协议与 C 跨端收口。** 药水仍被转换层拒绝；装备槽协议字段、Ready 阶段、奖励期输入、恢复后重复匹配、预测移速和硬编码地址仍待处理。装备显示双表已由 D 修复，不再列为缺口。
3. **最终联合验收。** 两台真实客户端三关、药水、连续恢复、100 Bot/50 房间持续 10 分钟、真实数据库故障复测和干净环境发布。当前检查不能替代这些项目。

本机 Docker Engine 本次不可用，尝试启动 Desktop 后仍超时，**未重新执行真实 Redis/MySQL 集成**。D 的上一日记录称这些测试已通过，本次保留该历史证据，未将它计为新的通过结果。

最终门槛仍按 [A / D 收尾清单 F01–F09](../../plans/WEEK2-AD-FINALIZATION.md) 执行。本次没有合并 A 尚待适配的分支，也没有创建最终发布标签。
