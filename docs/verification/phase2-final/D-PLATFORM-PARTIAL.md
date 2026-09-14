# D 平台阶段验收记录

状态：D1–D5 的 D 侧独立实现完成；A1–A4 正式生命周期接线及跨端最终验收未完成。本记录不能替代最终版本验收。

## 基线与环境

- 分支：`feature/week2-platform`
- D4：`25568e3`（Redis Resume 存储装配）
- D5：`eac51f4`（MySQL 异步幂等结算）
- 日期：2026-09-14，Asia/Shanghai
- 环境：Microsoft Windows NT 10.0.26200.0、PowerShell 7、本机 Docker Desktop；MySQL/Redis 端口和凭据从未提交的根目录 `.env` 读取
- Go：`go1.26.8 windows/amd64`；本次系统 CPU/内存查询因本机权限拒绝未采集
- 协议和装备目录版本：当前仓库协议；装备目录版本 1

## 已执行结果

| 范围 | 命令/方式 | 实际结果 |
| --- | --- | --- |
| Server 全包 | `GOPROXY=off; go test ./...; go vet ./...` | 通过 |
| Bot 全包 | `GOPROXY=off; go test ./...; go vet ./...` | 通过 |
| Redis/MySQL 真实集成 | `pwsh -File scripts/test/integration.ps1 -Target persistence` | 通过；Compose 四服务 Healthy |
| MySQL 结果 | integration-tag 测试 | 胜利、团灭、离弃均插入；重复内容返回幂等；同 ID 异内容冲突；结果与玩家行可读回 |
| MySQL 故障 | 关闭已连接的测试连接池后异步提交 | `Submit` 未等待数据库；后台重试后进入可恢复失败记录 |
| production 装配 | 隐藏启动本地 gameserver，启用 Resume 与 Results | `/healthz` 返回 `ok`；`/metrics` 含 `odyssey_result_queue_depth` |
| 真实首关 Bot | `a1-loadbot -mode functional -clients 2 -stages 1 -duration 90s -ramp 0s -resume=false -use-potion=false` | 2/2 成功；权威 StageCleared；最后 Tick 32；原始输出见 `d2-two-bot-one-stage.json` |
| 真实战斗指标/Admin | 10 Bot / 5 房间单关，同时轮询 `/metrics` 与 `/api/status` | 10/10 成功；峰值在线/房间/怪物/投射物为 10/5/15/28；伤害 600；清场 5；Admin 抓到 5 个 `playing` 房间、最大 Tick 12，且无 Token/密码/昵称字段；见 `d3-ten-bot-live-metrics.json` |
| 代码格式 | `git diff --check` | 通过 |

统一 `scripts/test/check.ps1` 的 `go mod download all` 在沙箱内被网络策略拒绝，在获准联网后又因 `proxy.golang.org` 连接超时失败。其后的实际 Server/Bot 全包 test 与 vet 已使用本地已校验依赖逐模块通过；不得把依赖下载失败记为测试失败，也不得把本次记录记为干净 clone 验收。

## 明确未执行

- Race：按项目负责人当前决定，开发完成并合并后统一执行。
- 两个真实 C++ 客户端、三关、奖励、Director、断线恢复、100 Bot/50 房间/10 分钟：A2–A4 正式入口尚未完整接线，不能用已通过的真实单关 Bot 或模块测试冒充通过。
- `PlayerProgress`、`EquipmentOwnership` 长期成长表：账号身份与成长规则尚未冻结，本周只保存 MatchHistory/GameResult 与终局玩家属性/装备快照。
- 干净 clone、两台主机和非开发者发布演练：留到候选合并提交。

## 待联调清单

1. A 在 Room 唯一终局路径生成并保留稳定 `match_id`，调用 D 的 `ResultWriter.Submit`；队列满不得销毁唯一结果。
2. A4 调用一次性 Resume Route，完成 Token 轮换、旧连接失效、首个完整快照与宽限期 Leave。
3. A2/A3 将奖励、Ready、Director 和三关事件接入唯一正式路由，D 再执行真实 Bot 与指标验收。
4. 合并候选版本后执行 Race、C++ CTest、Dashboard 构建、100 Bot 持续负载、故障注入和发布演练。
