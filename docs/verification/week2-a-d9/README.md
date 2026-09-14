# week2-a-d9 — 角色 A 验证记录

日期：2026-09-14
角色：A（网络 / 协议 / Session）
分支：`feature/network`（D7–D9 改动累积，未提交）

## D9 目标（WEEK2-DAYS4-10.md §9）

- 增加网络吞吐、快照字节、队列深度/拒绝指标
- 故障注入：坏帧、半包、慢读、断线风暴
- 完成标准：坏客户端不影响其他房间；停服后连接和 goroutine 回收

## 已实现

### 1. 网络可观测指标（`internal/metrics/metrics.go`）

新增 Prometheus collector（均注册进隔离 registry）：

| 指标 | 类型 | 说明 |
| --- | --- | --- |
| `odyssey_connections_accepted_total` | Counter | TCP 连接建立 |
| `odyssey_connections_closed_total` | Counter | 连接彻底拆除 |
| `odyssey_network_bytes_received_total` | Counter | 入站字节 |
| `odyssey_network_bytes_sent_total` | Counter | 出站字节 |
| `odyssey_network_frames_received_total` | Counter | 成功解码帧 |
| `odyssey_network_frames_sent_total` | Counter | 成功写出帧 |
| `odyssey_snapshot_frames_sent_total` | Counter | 快照帧（latest-wins）单独计数 |
| `odyssey_snapshot_bytes_sent_total` | Counter | 快照字节（10Hz 主带宽） |
| `odyssey_reliable_queue_depth` | Gauge | 可靠队列当前深度 |
| `odyssey_reliable_queue_rejections_total` | Counter | 可靠队列饱和拒绝（背压） |
| `odyssey_snapshot_drops_total` | Counter | latest-wins 旧快照驱逐 |
| `odyssey_invalid_frames_total{reason}` | CounterVec | 坏帧按 bounded reason（invalid_magic/invalid_version/too_large/message_type_zero/short_read） |

新增 `metrics.FrameResult` 有界枚举 + `ObserveInvalidFrame` 校验（拒绝客户端可控字符串标签，防无界序列）。

### 2. 解耦：`network.Observer` 接口（`internal/network/server.go`）

网络层不 import metrics，通过 `Observer` 接口上报传输事件：

- `Server.SetObserver(o)` 在 accept 前安装；每个 `Connection` 继承引用
- 读侧：`ReadFrame` 成功后 `OnFrameReceived` + `OnBytesReceived`；失败经 `observeReadError` 映射为 bounded reason（`errors.Is` 区分 magic/version/too_large/type_zero/short_read，EOF 不算坏帧）
- 写侧：`writeFrame` 回调 `OnFrameSent` + `OnBytesSent`；`writeSnapshotFrame` 额外 `OnSnapshotSent(bytes)` 分离快照带宽
- 队列：`Send` 入队后 `OnReliableDepth(len(out))`，满则 `OnReliableRejection`；`SendSnapshot` 驱逐旧快照时 `OnSnapshotDrop`

桥接：`cmd/gameserver/metrics_adapter.go` 的 `metricsObserver` 把接口回调转发到 `*metrics.Metrics`；`main.go` 装配 `srv.SetObserver(newMetricsObserver(metricSet))`。

### 3. 故障注入测试（`internal/network/fault_injection_test.go`）

- `TestBadFrameDoesNotDisturbHealthyPeer`：坏 magic 帧注入后，健康对端仍收到 Pong；坏帧计入 `invalid_frames`。
- `TestFrameTooLargeRejectedWithoutAllocation`：超大 BodyLength 在分配前被拒（T03 回归）。
- `TestHalfPacketDisconnectIsIsolated`：半包头后断连，对端被 untrack，健康对端不受影响。
- `TestConnectionStormAllRecover`：50 连接同时断连，`ActiveConns` 归零。

### 4. 停服回收测试（`internal/network/shutdown_test.go`）

- `TestServerShutdownReleasesGoroutines`：8 连接在线 → cancel Serve + `CloseConnections` → `ActiveConns` 归零，`runtime.NumGoroutine` 回落至基线（±2）。

## 验证结果

- `go build ./...` 通过
- `go vet ./...` 通过
- `go test ./...` 全绿（network 2.6s、gameserver 3.6s、metrics 通过）

## 未覆盖 / 待协调

- `-race`：本机缺 gcc，无法运行（D9 性能门 `Server/Bot 全包 race 通过` 留 Linux/CI）。
- 100 Bot / 50 房间 / 10 分钟压测（W13）：归 D，A 的指标已就位供 D 接入 Grafana。
- `reliable_queue_depth` 为逐连接点采样（writer drain 时上报），非严格全局求和；足以观测背压涨落，如需精确全局值可在 application 层聚合。
