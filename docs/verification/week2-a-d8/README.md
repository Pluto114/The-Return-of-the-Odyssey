# 角色 A：D8 恢复与连接重绑验证

本记录覆盖 A 在 D8 负责的协议层 Resume 流程：一次性 Token 签发/消费、连接重绑、旧连接失效、过期/伪造 Token 拒绝。Token 的 Redis 持久化（D）与 MySQL 结果落库（D）不在本记录范围。

## 已验证行为

| 场景 | 结果 |
| --- | --- |
| 登录签发 Token | LoginResponse 携带非空一次性 ResumeToken；重复登录重新签发会使旧 Token 失效 |
| 断线宽限期 | TCP 断开后 Session 转 Disconnected，不立即 `Room.Leave`；World 保留身份/装备/HP |
| 成功恢复 | ResumeRequest 消费 Token 后，新连接收到 ResumeResponse，session_id/player_id/room_id 与登录一致 |
| 完整权威快照 | 恢复后首个 `MSG_WORLD_SNAPSHOT` 是完整权威状态（含 Self 与 LastProcessedInputSeq），不回放旧输入 |
| 奖励中恢复 | 房间处于 Reward 阶段时，恢复状态携带该玩家私有的 RewardOptions / RewardApplied 重建值 |
| 伪造 Token | 未签发 Token 返回 `REASON_RESUME_TOKEN_INVALID`，不创建 Session |
| Token 单次消费 | 同一 Token 第二次 Resolve 失败；Registry 在消费/过期时清理条目 |
| 旧连接失效 | 重绑后遍历并关闭仍指向同一 Session 的半开旧连接，旧连接不能再以该玩家身份发输入 |
| 宽限期到期 | 未恢复的 Session 在宽限期后 `Room.Leave` 并转 Closed，Room 不残留第二个玩家 |

## 实现文件

- `server/internal/session/registry.go`：进程内一次性 Token 注册表（Issue/Resolve/MarkDisconnected/Revoke），Token 过期与单次消费语义。
- `server/internal/session/session.go`：允许 `Disconnected -> Reward` 转换（恢复时房间可能正处于奖励阶段）。
- `server/cmd/gameserver/application.go`：`handleLogin` 签发 Token、`handleResume` 消费并重绑、`deliverResumeState` 发送完整快照与奖励状态、`disconnected` 宽限期逻辑、`expireSession` 超时清理。
- `server/internal/config/config.go`：新增 `ODYSSEY_RESUME_GRACE_SEC`（默认 60s）。

## 测试

- `registry_test.go`：签发/消费/单次/过期/重签发/撤销。
- `resume_test.go`：端到端重绑同身份 + 伪造 Token 拒绝（真实 TCP 驱动）。

## 待 D/A 协调

- **断线清除 Ready**：RESUME-GAME-RESULT.md §1.7 要求“连接断开时清除该 Session 的 Ready，恢复后重新提交”。当前实现断线不 `Leave`，该 Session 的 Ready 标记与 `members` 绑定保留；屏障会等待其恢复或宽限期过期（过期后 `Leave` 会清除）。若需断线即排除出屏障，需在 Room 层区分“绑定”与“在线”状态，属 A/B 交叉，D8 联调对齐。
- **Token 持久化**：本实现为进程内注册表；跨进程重启恢复需 D 的 Redis Token Store 接入。
