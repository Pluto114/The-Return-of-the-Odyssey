# Frame Sequence vs Input Sequence

项目里有两种"序列号"，**绝不互通**，混用就是 bug。

## Frame Sequence（header-level）

- 位置：16 字节 header 末 4 字节。
- 单位：每条 wire-level Frame 一号，每连接独立，从 0 起。
- 用途：日志关联、丢帧观测、duplicate 帧检测、未来 UDP ACK。
- **不**参与业务逻辑；服务器与客户端都可以丢掉它。
- **不**用于预测 / 校正 / 输入去重。

```text
incoming frame ──> header.Sequence ──> log + stats + (maybe ACK later)
                         |
                         └──> 永远不参与 PlayerInput::input_seq
```

## Input Sequence（PlayerInput-level）

- 位置：`PlayerInput.input_seq`，protobuf 字段。
- 单位：玩家每次按键（或者每个客户端 tick）递增一号。
- 用途：客户端 Prediction + Server Reconciliation。
- 起点：每个 session 独立，从 1 开始（0 表示未初始化）。
- 服务器看到 `input_seq` gap > 64 时：
  - 在 lag/spike 期间容忍 gap；
  - 超过阈值返回 `REASON_INPUT_GAP_TOO_LARGE` 并强制 reconciliation 到当前权威快照；
  - **不**主动断连 —— 客户端应有自愈流程。

## 协作流程

客户端预测：

```text
1. 按键 → input_seq++
2. 保存本地 (input_seq -> 动作) 列表
3. 立即本地模拟
4. 发送 PlayerInput { input_seq, ... }
```

服务器回放：

```text
1. 收到 PlayerInput
2. 应用到 World State
3. 下个 WorldSnapshot 回写 last_processed_input
```

客户端 reconciliation：

```text
1. 收到 WorldSnapshot { self, last_processed_input }
2. self 为权威基准
3. 删除所有 input_seq <= last_processed_input 的本地预测
4. 用剩余 input 重放，得到新预测位置
```

## 校验点

- 真实 bug 排查第一件事：确认是 Frame Sequence 还是 Input Sequence 别混了。
- pprof / metric 名空间已分：`network_frame_seq_*` vs `input_seq_*`，不要共用。
- Trace 日志：Frame 层打 `frame_seq=…`；PlayerInput 业务层打 `input_seq=…`。不要重名。
