# 16-byte Frame Layout

每个 wire-level packet 由 16 字节 Header + Protobuf Payload 组成。大端（Network Byte Order）。

## 字段

| 偏移 | 字段 | 大小 | 字节序 | 说明 |
| ---: | --- | ---: | --- | --- |
| 0 | Magic | 2 | BE | 固定 `0x4E52`，用于过滤掉来自其它服务的流量 |
| 2 | Version | 1 | — | 协议版本，1 起步 |
| 3 | Flags | 1 | — | bit0 reserved (compression), bit1 reserved (fragmentation) |
| 4 | MessageType | 2 | BE | 见 `common.proto::MessageType` |
| 6 | Reserved | 2 | BE | 写入 0；接收端读不到也要跳过 |
| 8 | BodyLength | 4 | BE | protobuf payload 字节数，**含** varint 长度 |
| 12 | Sequence | 4 | BE | Frame Sequence；每连接 0..2^32 循环 |

## 读写契约

1. 一次读满 16 字节 header，禁止跨多条 Frame 复用粘包字节。
2. `BodyLength == 0` 是合法的（空 Ping 之类）。
3. `BodyLength > 1<<20` 一律拒绝并断开连接；任何 `protocol payload >1 MiB` 都是异常。
4. 写时序：先写满 16 字节 header → flush → 再写 body → flush。Writer goroutine 不能在 header 与 body 之间让出。
5. `MessageType == 0` (MESSAGE_UNSPECIFIED) 视为非法；服务端计数 + 断连。
6. Server 端校验 Magic 一致性；不一致直接断连并打 `REASON_INVALID_MAGIC` metric。

## 大小端

统一 **Big Endian**。任何小端/混合实现都按 bug 处理。所有校验和示例都基于 BE 解码。

## Frame Sequence

- 单调递增，**每连接**独立。
- 仅用于：日志、调试、网络统计、未来 UDP ACK。
- **严禁**与 PlayerInput::input_seq 混用 —— 详见 [sequence.md](sequence.md)。

## Version 策略

- v1 = 起始版本；当前 lockstep = `1`。
- 新增字段 / 新增 MessageType / 新增 ReasonCode：保持 Version=1。
- 重命名 / 改字节序 / 删除已有字段：升 Version，老客户端识别为 REASON_PROTOCOL_DEPRECATED 即可断连。

## 错误码（FRAME 层）

| ReasonCode | 触发条件 |
| --- | --- |
| `REASON_INVALID_MAGIC` | header.Magic ≠ 0x4E52 |
| `REASON_INVALID_VERSION` | header.Version 不在 server 支持列表内 |
| `REASON_FRAME_TOO_LARGE` | BodyLength > 1 MiB 或 payload 超过限制 |
| `REASON_DECODE_FAILED` | protobuf 解码错误 |
| `REASON_RATE_LIMIT` | 同一连接在窗口期内收到的 Frame 数量超过上限 |
| `REASON_PROTOCOL_DEPRECATED` | Version 即将淘汰，连接应当断开并升级 |
