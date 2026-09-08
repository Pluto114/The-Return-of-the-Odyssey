# Message Routing & Session State Machine

Session 是协议层之上、业务层之下的唯一上下文。**任何消息进入处理流水线前，必须先经过 Session 状态机校验**。

## SessionState 枚举

来自 `common.proto::SessionState`：

| 状态 | 含义 |
| --- | --- |
| `SESSION_STATE_CONNECTED` | TCP 已建连，未收到 LoginRequest |
| `SESSION_STATE_LOBBY` | 登录成功，在大厅等玩家点匹配 |
| `SESSION_STATE_MATCHING` | 已发 MatchRequest，等 MatchFound |
| `SESSION_STATE_IN_ROOM` | 进入 Room，可发 PlayerInput |
| `SESSION_STATE_REWARD` | StageClear，等待 RewardChoice |
| `SESSION_STATE_DISCONNECTED` | TCP 断了但 session 保留（在 resume 窗口内） |
| `SESSION_STATE_CLOSED` | 终结态；释放资源，token 失效 |

转换：

```text
   CONNECTED -- Login OK --> LOBBY
   LOBBY       -- MatchRequest    --> MATCHING
   MATCHING    -- MatchFound      --> IN_ROOM
   MATCHING    -- MatchCancel     --> LOBBY
   IN_ROOM     -- StageStarted    --> IN_ROOM (steady)
   IN_ROOM     -- RewardOptions   --> REWARD
   REWARD      -- RewardChoice    --> IN_ROOM (NextStageRequest after)
   *           -- TCP loss        --> DISCONNECTED
   DISCONNECTED -- ResumeRequest ok --> IN_ROOM (or LOBBY)
   *           -- resume_window_expired --> CLOSED
```

`CLOSED` 是单向终态；任何消息到达 `CLOSED` 都返回 `REASON_INVALID_STATE` 并立即关连接。

## 消息 × 状态合法性矩阵

| 消息 | CONNECTED | LOBBY | MATCHING | IN_ROOM | REWARD | DISCONNECTED | CLOSED |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Ping | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ |
| Pong | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ |
| Disconnect (S→C) | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ |
| LoginRequest | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| LoginResponse (S→C) | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| ResumeRequest | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| ResumeResponse (S→C) | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| MatchRequest | ❌ | ✅ | (no-op) | ❌ | ❌ | ❌ | ❌ |
| MatchFound (S→C) | ❌ | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ |
| MatchCancel | ❌ | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ |
| PlayerInput | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |
| WorldSnapshot (S→C) | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ |
| Event\* | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ |
| StageStarted (S→C) | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ |
| StageCleared | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ |
| RewardOptions | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ |
| RewardChoice | ❌ | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ |
| RewardApplied | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ |
| NextStageRequest | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ |

> "❌" ≠ 静默丢弃；一律回复 `REASON_INVALID_STATE`（若是状态机拒绝）或断连（若是协议层伪消息）。

## 拒绝语义

- 拒绝是**协议级**：服务端立即回写一个 ReasonCode，并在 metric 计数。
- 拒绝消息**不会**回写具体回包（避免在状态机未登录时回业务消息）；只用 Disconnect 或计数 + 断连表达。
- 客户端实现必须假设拒绝 = 立即关闭本地输入回路，等服务端 Reset。

## 路由归属

| 阶段 | 归属 | 备注 |
| --- | --- | --- |
| Frame 解码 + MessageType 路由 | A (`network.Router`) | |
| Session 状态机校验 | A (`session.Session`) | 在路由到 Room 之前 |
| Room 业务逻辑 | B (`room.Room`) | A 不写 |
| 持久化、Redis token | D | A 仅消费 ResumeRequest/ResumeResponse |
| 客户端解码 | C | |
