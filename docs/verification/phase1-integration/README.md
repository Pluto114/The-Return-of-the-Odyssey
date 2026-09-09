# 第一阶段集成验收记录

日期：2026-09-09（Asia/Shanghai）  
集成代码提交：`14694ea`  
集成基线：A `fe4d84c`、B `775fb5a`、C `7720504`（PR #1 head）、D `afc72a9`。

## 本次装配结果

- 正式 `gameserver` 已接入 Session、两人 FIFO 匹配、Room Join/Input、10Hz 个性化快照、可靠事件、EOF Leave 重试及空房回收。
- C++ 客户端已接入 Login→Match→30Hz PlayerInput→WorldSnapshot，显示房间、Tick、自身与队友位置。
- Go Bot 已使用同一 Frame/protobuf 完成真实 Login→Match→Input→Snapshot，不再是调度器占位实现。
- `/metrics`、本机 `/debug/pprof/` 与游戏进程同时启动；在线人数、房间、匹配和 Room Tick 工作耗时来自真实运行状态。
- 可靠事件队列拒绝发送时关闭对应慢连接；服务器关闭时主动终止所有活跃连接。

## 自动化检查

| 检查 | 结果 |
| --- | --- |
| `pwsh -File scripts/generate-proto/generate.ps1` | 通过；Go/C++ 生成代码与 descriptor 重新生成 |
| `pwsh -File scripts/test/check.ps1` | 通过；Server/Bot 全包 test + vet |
| `go test -race ./...`（Server 与 Bot） | 通过；使用 GCC 15.2，Go 临时缓存位于项目 `build/` |
| `pwsh -File scripts/build/build.ps1 -Target client` | 通过；MSVC 14.51、CMake 3.31.6、vcpkg x64-windows |
| `ctest --test-dir build/client-windows -C Debug --output-on-failure` | 4/4 通过：core、network、logic、protocol |
| Dashboard `npm run build` | 通过；Vite 生产构建完成 |
| Compose / Grafana 配置解析 | `docker compose --env-file .env.example config --quiet` 与 dashboard JSON 解析通过 |

Windows 首次 `-race` 使用 PATH 中的 GCC 8.1 时以 `0xc0000139` 退出，同时系统盘只剩约 155 MiB。将构建缓存转移到 E 盘并显式选择本机 GCC 15.2 后，全包 race 复测通过。

## 真实进程联调

### 两个 C++ 客户端

同一正式 Go Server 上启动两个独立 `odyssey_client.exe`：

- 两端 raylib/OpenGL 窗口均初始化成功；
- 两端均完成 Login，收到 `MatchFound(room=1, teammates=1)`；
- 两端均从 `server_tick=3` 开始收到权威完整快照；
- 强制退出一端 1 秒后，指标为 1 人 / 1 房；退出最后一端 6 秒后为 0 人 / 0 房，服务端记录 `room closed reason=idle`。

本次自动运行没有模拟物理键盘按键，因此 T08 的“人工 WASD 连续 5 分钟并录像”仍需 C 在演示验收时执行；输入编码、权威移动和实体移除路径已由 TCP 应用测试与真实 Bot 覆盖。

### 10 Bot / 5 房间 / 10 分钟

命令等价于：

```powershell
build/smoke/loadbot.exe -server 127.0.0.1:17777 -clients 10 -duration 10m -ramp 1s
```

结果：

| 项目 | 实测 |
| --- | --- |
| Bot | planned 10、started 10、succeeded 10、failed 0 |
| 持续时间 | 10m0.001s |
| 房间 | 5；等待队列 0 |
| Tick 样本 | 90,016 |
| 每房 Tick 频率 | 30.001Hz |
| Tick 工作耗时 p99 | `< 0.1ms`（89,997 / 90,016 样本不超过 0.1ms） |
| 结束回收 | 6 秒后 online_players=0、active_rooms=0 |

环境：Windows 11 专业版 64 位（10.0.26200）、Intel Core i9-13900HX（24 核 / 32 线程）、15.8 GiB RAM、Go 1.26.8 windows/amd64；Server 与 Bot 为本机普通构建，Docker 未参与本轮负载。

## 尚未完成的外部环境验收

Docker Desktop 守护进程当前未就绪，Redis `127.0.0.1:6379` 不可连接；因此 Redis token store 的真实容器集成、Prometheus 抓取和 Grafana 页面截图本次未执行。相关单元测试、Compose 展开、Prometheus 配置和 Grafana dashboard JSON 均已通过；恢复 Docker 后运行：

```powershell
Copy-Item .env.example .env
pwsh -File deploy/scripts/infra.ps1 -Action up
pwsh -File scripts/test/integration.ps1
```

不要把未执行的容器验收记为通过。当前代码主链路不依赖 MySQL/Redis，实时 Tick 不访问数据库。
