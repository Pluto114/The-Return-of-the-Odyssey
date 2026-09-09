# 初始化验收记录

初始验证日期：2026-09-07；平台模块与运行验收更新：2026-09-09。环境：本机 Windows x64，仓库 D:/The-Return-of-the-Odyssey。
此记录描述骨架与工具链验证，不代表游戏功能或性能验收。

| 检查项 | 结果 |
| --- | --- |
| 架构文档 | ARCHITECTURE.md 与用户提供的原文件 SHA256 一致 |
| 原有内容 | CLion .idea 保留；Hello World main.cpp 与原 CMakeLists.txt 备份到本机 .local-backup |
| Git | origin=https://github.com/Pluto114/The-Return-of-the-Odyssey.git；骨架以远程原始 Initial commit 为父提交，采用 main/develop 分支 |
| 目录 | 文档列出的 server/client/bot/dashboard/proto/deploy/data/docs/scripts 目录已建立；lobby 已开始实现，其余空模块可随 .gitkeep 入库 |
| 凭据与产物 | .env、.tools、IDE 文件、node_modules、vcpkg 产物、生成协议代码均被 gitignore 排除；scripts/build 可正常入库 |
| Windows portable 工具 | Go 1.26.8、Node 24.20.0/npm 11.19.0、protoc 33.4 官方下载包 SHA256 校验通过；bootstrap 重跑未覆盖任何已有 .env |
| Go 模块 | server 与 bot 分别以 GOWORK=off 下载全部依赖、go mod verify 通过；已生成各自 go.sum |
| 协议生成 | 六个空 schema 同时生成 Go/C++ 与 descriptor set；protoc-gen-go v1.36.12 |
| Go 检查 | 生成协议包编译、lobby、persistence、metrics 与 Bot load 单元测试、go test、go vet 通过 |
| CMake 骨架 | scaffold preset configure / build 通过，没有业务 executable |
| C++ 工具与依赖 | vcpkg 固定 baseline 的 17 个直接/传递依赖安装成功，含 Debug/Release；MSVC 19.37.32825 + CMake 3.31.12 + Ninja 1.13.2 配置通过；六个生成的 .pb.cc 全部编译并链接为 odyssey_protocol.lib；首次依赖安装约 10 分钟 |
| npm | package-lock.json 已生成，npm ci 成功；官方 registry；安装时 audit 报告 0 vulnerabilities |
| Dashboard | Vite 8.2.2 静态占位页打包成功；Vue plugin 和 /api、/ws 代理配置可加载；Grafana API 已确认 Odyssey Overview 及其 6 个面板完成 provision |
| Compose 解析 | 独立 Compose 5.5.1 config --quiet 通过；服务列表为 mysql、redis、prometheus、grafana |
| 镜像与容器 | 2026-09-08 四个固定版本镜像均拉取成功，mysql、redis、prometheus、grafana 均启动并显示 healthy |
| doctor | server、dashboard、client 角色基础工具检查通过；2026-09-08 platform 检查通过，Docker Engine 29.7.2 / Compose 5.5.1 可用；doctor 本身不检查镜像下载或容器健康 |
| 文件语法 | 仓库 PowerShell 脚本和 JSON 文件可解析 |
| CI | GitHub Actions 配置已添加；本地对应检查已执行，远程工作流尚未运行；当前 CI 不包含 C++ 全量构建 |

## 2026-09-08 Docker 运行验收

用户已安装 PowerShell 7.6.5 与 Docker Desktop，使用 Linux containers。Docker Hub 认证请求曾报 EOF；显式通过已有系统代理访问认证站点返回 HTTP 200，直连请求 20 秒超时。重试后完成镜像下载；没有更换镜像版本或修改 Docker Desktop 的代理配置。

本机有两个默认端口落入 Windows TCP 保留范围，已仅调整本地 .env（团队 .env.example 默认值保持不变）：

| 服务 | 本机访问地址 | 验证结果 |
| --- | --- | --- |
| MySQL | 127.0.0.1:33306 | Windows 保留端口范围覆盖 3306；已同步 server/configs/.env；应用账号 SELECT 1 返回 1 |
| Redis | 127.0.0.1:6379 | PING 返回 PONG |
| Prometheus | http://127.0.0.1:9090 | ready 返回 HTTP 200，自监控 target 为 up |
| Grafana | http://127.0.0.1:33000 | Windows 保留端口范围覆盖 3000；health 的 database=ok |

四个容器保持运行。再次启动或查看状态：

```powershell
pwsh -File deploy/scripts/infra.ps1 -Action up
pwsh -File scripts/doctor.ps1 -Role platform
```

Grafana 登录凭据在本地 .env 的 GRAFANA_ADMIN_USER / GRAFANA_ADMIN_PASSWORD 中。
gameserver target 仍为 down，因为游戏服务未实现；业务指标尚未接入。
本机预留的游戏指标端口 9091 当前可用；接入 metrics 前仍需重新检查占用，并同步服务配置与 deploy/prometheus/prometheus.yml 的采集目标。

ResumeTokenStore 已通过真实 Redis 集成测试：重复签发不会覆盖、消费为原子一次性操作、TTL 到期后返回未找到。测试入口为 `scripts/test/integration.ps1 -Target persistence`。

2026-09-09 Docker Desktop 曾因本地运行时套接字无法重命名而启动失败。停止 Docker Desktop 后，将 `%LOCALAPPDATA%\Docker\run` 原目录保留为 `run.stale-backup-20260909-085343`，再创建空运行目录并启动，Engine 29.7.2 恢复；没有删除镜像、容器、数据卷或设置。Grafana 仪表盘 provider 显式设置 10 秒扫描周期，新文件可在运行期间自动导入。

## 本机兼容问题及处理

- 原 PATH 为 Node 22.18/npm 10.9、protoc 31.1；项目 env.ps1 使用固定的本地 Node 24/protoc 33.4。
- 原 npm 缓存 D:/Node/node_cache 无写权限；当前会话改用 .tools/npm-cache。
- 原 D:/vcpkg 存在 Git ownership 限制；新建项目本地 vcpkg，不改系统 Git safe.directory。
- git 与 Go 直连外网超时；使用 Windows 已有系统代理重试成功，可用 env.ps1 -UseSystemProxy。
- 原 vcpkg 二进制缓存源无法解析；环境脚本已切换为项目本地文件缓存，不改系统 NuGet 配置。

## 范围边界

已实现并测试内存 FIFO Matchmaker、Redis ResumeTokenStore、Prometheus 指标模块和 Bot 并发调度模块；尚未接入 Session、Room 或 protobuf。没有实现 Player/Room/Tick/TCP/AI/数据库表、Bot 网络行为或管理后台业务。
server/client/bot 环境变量模板尚没有配置加载器，HTTP、WebSocket、pprof 和游戏指标端点仅作约定预留。
Linux/WSL2 实机、Go race detector、图形窗口与端到端联网尚未验证。
