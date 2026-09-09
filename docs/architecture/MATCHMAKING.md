# 匹配模块约定

状态：第一版骨架，等待 A、B 评审接入接口。

`server/internal/lobby` 拥有匹配规则和等待队列。模块当前只接收不透明的 `PlayerID`，按 FIFO 将固定数量的唯一玩家组成 `Match`。协议解码、Session 状态迁移和 Room 创建不属于该模块。

## 第一版行为

- `NewMatchmaker` 固定每局人数；非正数配置会被拒绝。
- `Enqueue` 保证同一玩家不能重复排队。
- 队列凑满后，最早进入的玩家先组成一组并从等待队列移除。
- `Cancel` 只取消仍在等待的玩家；已经匹配或从未排队时返回 `false`。
- 所有公开操作可并发调用；调用方可以安全修改返回的玩家切片。
- 第一版只有单队列，不处理组队、段位、区域和超时扩圈。

## 接入边界

成员 A 负责把已认证 Session 的稳定玩家标识传给 `Enqueue`，并在自身模块中验证 `Lobby`、`Matching`、`InRoom` 等状态。`lobby` 不创建第二套 Session，也不依赖 protobuf 类型。

成员 B 负责接收完整 `Match` 并创建 Room。Room 创建成功后，由接入层取得 Room ID，再通过 A 的协议通道发送 `MatchFound`。`lobby` 不导入 `room` 包，也不生成 Room ID。

成员 D 后续在模块外记录排队人数、匹配数量和匹配耗时；指标采集失败不得改变匹配结果。

## 评审前待定项

- 每局人数及是否需要按游戏模式分别建队列。
- 断线时立即取消还是保留短暂宽限期。
- Room 创建失败后玩家重排队的顺序和重试上限。
- Redis Match Queue 在单服和多服阶段分别承担的职责。
- `MatchRequest`、取消请求和 `MatchFound` 的 protobuf 字段及消息 ID，由 A 审核、C 确认兼容性。
