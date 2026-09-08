# Protocol Workspace

唯一真相源为根目录 `proto/`。A 是协议 Owner；任何 .proto 的改动必须经过 A 审核 + C 兼容性确认，详见 [CONTRIBUTING.md](../../CONTRIBUTING.md)。

## 0. 生成命令

```powershell
. .\scripts\env.ps1
pwsh -File scripts\generate-proto\generate.ps1
```

输出：

- Go：`server/generated/protocol/`
- C++：`client/generated/protocol/`
- Descriptor set：`build/protocol.pb`

脚本校验 `protoc 33.4` 与 `protoc-gen-go v1.36.12`，与 vcpkg 中的 C++ runtime 对齐。版本对不上就拒绝执行 —— **不要**用系统 PATH 上的其它 protoc 凑数。

## 1. 16-byte 帧头

详见 [frame.md](frame.md)。字段如下：

| 偏移 | 字段 | 大小 | 说明 |
| ---: | --- | ---: | --- |
| 0 | Magic | 2 | 建议 `0x4E52`，Network Byte Order |
| 2 | Version | 1 | 起始 1；breaking change 才升 |
| 3 | Flags | 1 | bit0=compressed（reserved）, bit1=fragmented（reserved） |
| 4 | MessageType | 2 | 见 [common.proto::MessageType](../../proto/common.proto) |
| 6 | Reserved | 2 | 写入 0；保留给未来 Flags 扩展 |
| 8 | BodyLength | 4 | Protobuf payload 字节数，<= 1<<20 (1 MiB) |
| 12 | Sequence | 4 | Frame Sequence，per-connection 单调；与 Input Sequence 不互通 |

Header 与 Protobuf payload 分两次写缓冲；先 header 后 body，BodyLength 必须严格等于实际 payload 字节。

## 2. MessageType 区间（已分配）

| 区间 | 模块 | 文件 |
| --- | --- | --- |
| 0–99 | System | [system.proto](../../proto/system.proto) |
| 100–199 | Login / Session | [session.proto](../../proto/session.proto) |
| 200–299 | Lobby / Match | [lobby.proto](../../proto/lobby.proto) |
| 300–399 | Game / Entity / Combat | [game.proto](../../proto/game.proto) |
| 400–499 | Stage / Reward | [stage.proto](../../proto/stage.proto) |
| 500–599 | Director / Metrics | 共用（见 common.proto） |

### 已定义消息

| ID | 名称 | 方向 | 文件 |
| ---: | --- | --- | --- |
| 1 | Ping | C→S | system.proto |
| 2 | Pong | S→C | system.proto |
| 3 | Disconnect | S→C | system.proto |
| 101 | LoginRequest | C→S | session.proto |
| 102 | LoginResponse | S→C | session.proto |
| 103 | ResumeRequest | C→S | session.proto |
| 104 | ResumeResponse | S→C | session.proto |
| 200 | MatchRequest | C→S | lobby.proto |
| 201 | MatchFound | S→C | lobby.proto |
| 202 | MatchCancel | C→S | lobby.proto |
| 300 | PlayerInput | C→S | game.proto |
| 310 | WorldSnapshot | S→C | game.proto |
| 320 | ProjectileSpawnEvent | S→C | game.proto |
| 321 | ProjectileDestroyEvent | S→C | game.proto |
| 322 | DamageEvent | S→C | game.proto |
| 323 | DeathEvent | S→C | game.proto |
| 324 | EntityRemovedEvent | S→C | game.proto |
| 400 | StageStarted | S→C | stage.proto |
| 401 | StageCleared | S→C | stage.proto |
| 410 | RewardOptions | S→C | stage.proto |
| 411 | RewardChoice | C→S | stage.proto |
| 412 | RewardApplied | S→C | stage.proto |
| 413 | NextStageRequest | C→S | stage.proto |
| 500 | PerformanceMetrics | S→C | （Week 3） |
| 501 | StagePlan | S→C | （Week 3） |

任何 ID 变更 = breaking change，按 §3 处理。

## 3. 版本与稳定性策略（继承 ARCHITECTURE §34）

- 新增字段不升 Version；
- 增加新 MessageType 不升 Version；
- 删除字段使用 `reserved`，**不要**复用编号；
- 重命名 MessageType 或改 wire 字节序 = 升 Version。

## 4. 子文档

| 主题 | 文件 |
| --- | --- |
| 16 字节帧头布局、字节序 | [frame.md](frame.md) |
| Session 状态机与消息路由 | [message-routing.md](message-routing.md) |
| Frame Sequence vs Input Sequence | [sequence.md](sequence.md) |
| Snapshot 分层（Self / Other / Monster） | [snapshots.md](snapshots.md) |
| Reliable Queue vs Snapshot Queue 选型 | [events.md](events.md) |

## 5. 三方契约

- **A**：协议作者，负责本文档及 proto/ 变更审核。
- **B**：Room / Director / Combat 实现者。消费 proto；不修改 proto 字段语义。
- **C**：客户端实现者 + proto 兼容性复核者。每次新增/删除字段，C 必须确认下游可消费。
- **D**：匹配 / 持久化 / 压测；Resume token 的 Redis 映射在 D；ResumeRequest/Response 协议归 A。
