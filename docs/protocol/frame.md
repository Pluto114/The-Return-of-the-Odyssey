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
3. `BodyLength > 65536`（64 KiB）一律拒绝并断开连接；任何 `protocol payload > 64 KiB` 都是异常。**注意**：此上限是 PHASE1 草案建议值，D1 对齐会上由 A 提案最终定死（候选 64 KiB / 1 MiB），在此之前不得按 1 MiB 实现。分配 body 缓冲前必须先校验 `BodyLength`，禁止先 `make([]byte, BodyLength)` 再检查。
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
| `REASON_FRAME_TOO_LARGE` | BodyLength > 64 KiB 或 payload 超过限制 |
| `REASON_DECODE_FAILED` | protobuf 解码错误 |
| `REASON_RATE_LIMIT` | 同一连接在窗口期内收到的 Frame 数量超过上限 |
| `REASON_PROTOCOL_DEPRECATED` | Version 即将淘汰，连接应当断开并升级 |

## 固定字节样例（T01 跨语言基准）

这是 Go 与 C++ 互通验收的**黄金样例**。两端必须对同一帧编出完全一致的字节、解出完全一致的字段。验收时用本样例比对，不允许用「自己编码、自己解码成功」代替。

### 样例 1：Ping（`client_time_ms=1, nonce=2`）

**Header（16 字节，大端）**

| 偏移 | 字段 | 值 | 十六进制 |
| ---: | --- | ---: | --- |
| 0 | Magic | `0x4E52` | `4E 52` |
| 2 | Version | `1` | `01` |
| 3 | Flags | `0` | `00` |
| 4 | MessageType | `1` (MSG_PING) | `00 01` |
| 6 | Reserved | `0` | `00 00` |
| 8 | BodyLength | `4` | `00 00 00 04` |
| 12 | Sequence | `1` | `00 00 00 01` |

**Body（4 字节，protobuf）**

```
08 01   -> field 1 (client_time_ms), varint = 1
10 02   -> field 2 (nonce), varint = 2
```

**完整帧（Header + Body，共 20 字节）**

```
4E 52 01 00 00 01 00 00 00 00 00 04 00 00 00 01  08 01 10 02
```

### 样例 2：空 Ping（`client_time_ms=0, nonce=0`，即空 body）

protobuf 对全零标量字段不编码，body 为 0 字节。`BodyLength == 0` 是合法的（见读写契约第 2 条）。

```
4E 52 01 00 00 01 00 00 00 00 00 00 00 00 00 01
```

### 样例 3：Pong（`client_time_ms=1, server_time_ms=2, nonce=3`）

Body 编码：

```
08 01   -> field 1 (client_time_ms) = 1
10 02   -> field 2 (server_time_ms) = 2
18 03   -> field 3 (nonce) = 3
```

Header：Magic=`4E52`、Version=`01`、Flags=`00`、MessageType=`2`(MSG_PONG)=`00 02`、Reserved=`00 00`、BodyLength=`6`=`00 00 00 06`、Sequence=`1`。

完整帧：

```
4E 52 01 00 00 02 00 00 00 00 00 06 00 00 00 01  08 01 10 02 18 03
```

### 校验点

- Header 16 字节必须与上表**逐字节一致**，任何小端/字段错位都判失败。
- Body 解码后字段值必须与样例标注相同（`Ping.client_time_ms==1 && Ping.nonce==2`）。
- 两端序列化 body 的字节不要求总是逐字节相同（protobuf 允许同值多编码），但**样例帧是固定输入**，此时两端应产生完全相同的字节；若不一致，说明有一端编码顺序/实现不同，需要排查。
- 此样例随 Frame 契约一起进 `feature/network` 提交，作为 T01 验收输入。
