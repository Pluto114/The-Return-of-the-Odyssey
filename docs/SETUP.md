# 团队开工环境清单

适用阶段：Repository Scaffolding。统一版本后即可分模块开工；当前尚无可运行的游戏。
本机实际完成程度另见 [VERIFICATION.md](VERIFICATION.md)，此处是全组应使用的配置。

本机 2026-09-08 已启动全部四个容器。Windows 保留端口范围覆盖默认的 MySQL 3306 和 Grafana 3000，本机 .env 覆盖为 MYSQL_PORT=33306、GRAFANA_PORT=33000；Prometheus 仍为 9090、Redis 仍为 6379。下文表格保留团队模板默认端口，实际访问以各自 .env 为准。

## 1. 版本基线

| 组件 | 项目统一版本 | 安装 / 锁定方式 |
| --- | --- | --- |
| Git | 2.46+ | 系统安装；本机 2.46.2 |
| PowerShell | 7.x | 所有 .ps1 统一使用 pwsh，Windows/Linux 通用检查入口 |
| Go | **1.26.8** | .go-version、go.work、两个 go.mod；Windows bootstrap 下载到 .tools |
| Node.js | **24.20.0 LTS** | .nvmrc、package.json engines；Windows bootstrap |
| npm | **11.19.0** | 随上述 Node 安装；packageManager + package-lock.json |
| C++ 标准 | **C++20** | CMake target；不使用旧 MinGW 8.1 |
| Windows 编译器 | Visual Studio 2022/2026 的 MSVC x64 | “使用 C++ 的桌面开发”、Windows SDK；本机 VS 2026/MSVC 14.51 |
| Linux 编译器 | GCC 13+ | 原生 Linux 构建；race detector 也需要 GCC |
| CMake | **3.31+**（本机 3.31.6） | 系统或 IDE 提供；Ninja generator 避免依赖特定 VS generator |
| Ninja | 1.12+ | VS 附带版本可由 env.ps1 自动加入当前会话 PATH；Linux 用系统包 |
| vcpkg | **2026.07.29** | commit `9e593bb18ea69cc5095e012465dcd675a822ed0d`，client/vcpkg.json |
| raylib | **6.0** | vcpkg baseline |
| Standalone Asio | **1.32.0** | vcpkg baseline，ASIO_STANDALONE |
| Protobuf C++ | **6.33.4#2** | vcpkg baseline；#2 是 port revision |
| protoc | **33.4** | Windows bootstrap；必须匹配上述 C++ runtime |
| protoc-gen-go / Go protobuf | **v1.36.12** | 生成脚本自动安装插件；Go Modules |
| Dear ImGui | **1.92.8#1** | vcpkg baseline，仅核心库；raylib 桥接后续实现 |
| Vue / ECharts | **3.5.42 / 6.1.0** | npm 精确版本和 package-lock.json |
| Vite / Vue plugin | **8.2.2 / 6.0.8** | npm 精确版本和 package-lock.json |
| MySQL driver | **v1.10.1** | github.com/go-sql-driver/mysql |
| go-redis | **v9.22.0** | github.com/redis/go-redis/v9 |
| Prometheus Go client | **v1.24.1** | github.com/prometheus/client_golang |
| Docker | Docker Desktop（Linux containers）或 Linux Docker Engine | Compose 插件 >=2.20，支持 up --wait；独立 validator 不能运行容器 |
| MySQL | **8.4.11** | mysql:8.4.11 |
| Redis | **7.4.11** | redis:7.4.11-alpine |
| Prometheus | **3.13.2** | prom/prometheus:v3.13.2 |
| Grafana OSS | **13.2.1** | grafana/grafana:13.2.1 |

镜像固定版本标签，尚未固定镜像 digest；如需部署完全可复现，由 D 在真实拉取后登记 digest。
不手动下载 C++ 第三方 DLL，也不将不同 vcpkg triplet、MSVC 和 MinGW 产物混用。

## 2. 按角色准备

| 成员 | 必需环境 | 开工入口 |
| --- | --- | --- |
| A 网络 / 协议 | Git、pwsh、Go、protoc | proto/、server/internal/network、session |
| B 游戏核心 | Git、pwsh、Go；联调时 protoc | server/internal/room、game |
| C 客户端 / 同步 | Git、pwsh、Go（生成插件）、protoc、MSVC 或 GCC、CMake、Ninja、vcpkg | client/；负责后台时加 Node |
| D 平台 / 压测 | Git、pwsh、Go、protoc、Docker；后台需 Node | bot/、deploy/、dashboard/、lobby、persistence、metrics |

全功能 Windows 开发建议预留 16GB 内存与 15GB 可用磁盘，vcpkg 首次编译会下载较多依赖。
容器与 Go 服务端可用 Linux/WSL2；图形客户端优先 Windows 原生工具链。

## 3. Windows 首次初始化

1. 安装 Git、[PowerShell 7](https://learn.microsoft.com/powershell/scripting/install/installing-powershell-on-windows)，客户端成员安装 VS C++ workload（可只装 Build Tools）。
2. 在项目根目录打开 PowerShell 7，确认命令是 pwsh，而非 Windows PowerShell 5.1。
3. 执行以下命令。bootstrap 下载带固定 SHA256 的官方 Go、Node、protoc 包，仅写入项目 .tools，不改系统 PATH，不覆盖已有 .env。

```powershell
pwsh -NoProfile -File scripts/bootstrap.ps1
. ./scripts/env.ps1
go version
node --version
protoc --version
```

每次新开终端需重新执行 `. ./scripts/env.ps1`；也可自行全局安装表内相同版本。
env.ps1 同时将 npm 缓存指向 .tools/npm-cache，避免继承其他安装目录的缓存权限问题。
开发脚本会自动加载项目工具路径。VS Code/CLion 自己启动的终端或构建工具不一定继承此 PATH，按下一节设置。

bootstrap 自动创建根目录及 server/configs、bot/configs、client、dashboard 下缺失的 .env。
根目录 .env 被 Compose 读取，dashboard/.env 被 Vite 读取；gameserver 使用 `-env server/configs/.env` 加载服务配置，未传 `-env` 时使用内置默认值。client/bot 的环境文件仍是配置契约。
修改根目录数据库账号、端口或密码时，也同步 server/configs/.env 的连接配置。

```powershell
pwsh -File scripts/generate-proto/generate.ps1
pwsh -File scripts/test/check.ps1
pwsh -File scripts/doctor.ps1 -Role server
```

从仓库根目录启动服务端，确保默认装备目录的相对路径可解析：

```powershell
go run ./server/cmd/gameserver -env server/configs/.env
```

`server/configs/.env.example` 提供版本化装备目录、奖励时长、至少三关的终局上限、首关 Seed 基值和 Director 参数。启动时服务端只读取一次 `data/equipment/catalog.json`，要求版本为 1；路径缺失、JSON 损坏、版本不符或参数非法都会明确报错并在监听端口前退出。C++ 构建从同一 JSON 生成 `equipment.tsv` 并复制到可执行文件旁，不要另建手写装备表。

Go Modules 会下载并验证依赖。首次初始化后生成的 go.sum、npm 锁文件要提交。
Go 官方代理连通性不足时，可以在自己的终端配置可信 GOPROXY；不要在项目中关闭 GOSUMDB 或硬编码个人代理。
如果浏览器可访问 GitHub 而 git/Go/npm 超时，且 Windows 已配置可用的系统代理，可在当前 PowerShell 会话先执行 `. ./scripts/env.ps1 -UseSystemProxy`，再重试上述命令；此选项读取现有代理，不写全局配置或提交个人代理地址。

## 4. C++ 客户端依赖与 IDE

```powershell
pwsh -File scripts/setup-client.ps1
pwsh -File scripts/generate-proto/generate.ps1
pwsh -File scripts/build/build.ps1 -Target client
```

setup-client 将固定版本 vcpkg 安装到项目 .tools/vcpkg，保留系统现有 VCPKG_ROOT 的目录。
env.ps1 为当前会话使用项目内 .tools/vcpkg-cache 二进制缓存，避免继承其他项目的 NuGet 源；默认将依赖编译并发限制为 4。vcpkg 可能自行下载较新 CMake/解压工具用于编译 ports，这些与入口 CMake 分开管理。
build 会加载 MSVC x64 环境、安装清单依赖并编译**空 schema 生成的协议静态库**；没有游戏可执行文件。
首次 vcpkg 编译需要访问 GitHub、源码发行站点及 CMake 下载源，耗时取决于网络与 CPU。

CLion：打开根 CMakeLists.txt；Toolchains 选择 Visual Studio/MSVC x64，Generator 选择 Ninja。
启用 `client-windows` preset，并将构建环境 VCPKG_ROOT 指向 `<仓库>/.tools/vcpkg`。
若已有 CLion profile 仍指向原 Hello World target，请切到新 preset；原示例已留在本机 .local-backup。
团队不提交 .idea 或本机绝对路径。VS 2026 建议用 Ninja preset，不使用旧 CMake 中不存在的 VS 2026 generator。

只检查目录骨架、不安装 C++ 库：

```powershell
pwsh -File scripts/build/build.ps1 -Target scaffold
```

## 5. 管理后台

```powershell
. ./scripts/env.ps1
npm --prefix dashboard ci
npm --prefix dashboard run dev
# 另一个终端验证打包：
pwsh -File scripts/build/build.ps1 -Target dashboard
```

打开 http://127.0.0.1:5173，当前显示静态环境占位页。src/ 预留 Vue 页面。
Vite 已预留 /api 与 /ws 到 http://127.0.0.1:8080 的代理，服务端接口尚未实现。
可在 dashboard/.env 设置 ODYSSEY_ADMIN_PROXY_TARGET。页面打包不代表 Vue/ECharts 业务已经完成。

## 6. Docker 基础设施

Windows 安装 [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/) 并启动 Linux containers，按安装器要求启用 WSL2/虚拟化。
本项目脚本不自动更改系统虚拟化、重启或安装 Docker Desktop。

```powershell
docker version
docker compose version
pwsh -File deploy/scripts/infra.ps1 -Action config
pwsh -File deploy/scripts/infra.ps1 -Action up
pwsh -File deploy/scripts/infra.ps1 -Action status
```

| 服务 | 本机地址 / 端口 | 状态 |
| --- | --- | --- |
| MySQL | 127.0.0.1:3306 | Compose；库 odyssey，用户 odyssey，密码见 .env |
| Redis | 127.0.0.1:6379 | Compose；密码见 .env |
| Prometheus | http://127.0.0.1:9090 | 自监控及 gameserver 预留目标 |
| Grafana | http://127.0.0.1:3000 | 用户 admin，密码见 .env；Prometheus 数据源自动配置 |
| 游戏 TCP | 127.0.0.1:7777 | 预留，尚无服务 |
| 管理 HTTP / WS | 127.0.0.1:8080 | 预留，尚无服务 |
| 游戏指标 | http://127.0.0.1:9091/metrics | 预留，尚无服务 |
| pprof | http://127.0.0.1:6060/debug/pprof/ | 预留，尚未接入 |
| Vite / preview | 127.0.0.1:5173 / 4173 | 后台开发 / 打包预览 |

基础设施映射仅监听本机；容器内通过 mysql:3306、redis:6379、prometheus:9090 互访。
默认示例密码只用于本机开发。Grafana 首次初始化读取管理员密码，MySQL 首次创建数据卷时读取库与账号；后续改 .env 不会自动修改已有数据库账号。

Prometheus 的 gameserver target 在业务服务实现前显示 DOWN 属正常现象，不能当成游戏指标接入完成。
容器通过 host.docker.internal:9091 采集宿主机服务；将来的指标监听不能只绑定宿主机 127.0.0.1，因此模板预留 0.0.0.0:9091，防火墙仅放行所需 Docker 本地网络。
pprof 仍只绑定本机。Grafana 已预配置 Odyssey Overview 面板；gameserver 尚未暴露指标时，应用面板显示 No data 属正常现象。

验证实际基础设施：

```powershell
docker compose exec mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" mysql -u"$MYSQL_USER" -D"$MYSQL_DATABASE" -e "SELECT 1"'
docker compose exec redis sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping'
Invoke-RestMethod http://127.0.0.1:9090/-/ready
Invoke-RestMethod http://127.0.0.1:3000/api/health
# 停止服务并保留数据卷：
pwsh -File deploy/scripts/infra.ps1 -Action down
```

`down` 保留数据；不要把 `down -v` 作为日常停止命令，它会删除持久化数据卷。
端口占用时调整根目录 .env 中相应端口，并同步消费方配置；9091 的预留采集端口则同时修改 Prometheus target 与服务配置。

## 7. Linux / WSL2

Windows portable bootstrap 只支持 Windows x64。Linux 安装上述版本 Go、Node、protoc、PowerShell 7 后，同一套生成、检查与构建脚本可用。
建议 Ubuntu 24.04 x64；客户端原生构建另需 GCC 13+、CMake 3.31+、Ninja、pkg-config、zip/unzip、curl，以及 raylib 使用的 X11/OpenGL/音频开发包：

```bash
sudo apt-get update
sudo apt-get install build-essential ninja-build pkg-config zip unzip curl git \
  libx11-dev libxrandr-dev libxinerama-dev libxcursor-dev libxi-dev \
  libgl1-mesa-dev libasound2-dev
# Ubuntu 默认 CMake 可能低于 3.31，请另外安装符合版本的官方 CMake。
# Go / Node / protoc / pwsh 安装完毕后，回到仓库根目录：
cp -n .env.example .env
cp -n dashboard/.env.example dashboard/.env
pwsh -File scripts/setup-client.ps1
pwsh -File scripts/generate-proto/generate.ps1
pwsh -File scripts/build/build.ps1 -Target client
pwsh -File scripts/test/check.ps1 -Race
```

服务端开发无需安装图形依赖或执行 client build。Linux Docker Engine 需单独安装 Compose 插件。
Linux/macOS 的 protoc 也必须使用 33.4；macOS 客户端 preset 尚未提供，由实际需要该平台的成员补齐。

## 8. 自检与同步给团队

```powershell
pwsh -File scripts/doctor.ps1 -Role server
pwsh -File scripts/doctor.ps1 -Role client
pwsh -File scripts/doctor.ps1 -Role dashboard
pwsh -File scripts/doctor.ps1 -Role platform
# 或检查全部：
pwsh -File scripts/doctor.ps1 -Role all
```

doctor 失败会返回非零退出码；client build 才是编译器与 vcpkg 库版本的完整验证。
Windows bootstrap 可加 `-WithComposeValidator` 获取项目内独立 Compose 5.5.1，仅用于没有 Docker 时解析配置：

```powershell
. .\scripts\env.ps1
docker-compose --env-file .env.example config --quiet
```

独立 Compose 配置校验不能替代 Docker Engine，也不能证明镜像和容器健康。
交接时同步仓库和本清单即可，不要复制 .tools、.idea、node_modules、vcpkg_installed、.env 或构建目录。

## 官方核对来源

- [Go 发行版](https://go.dev/dl/)、[Node 24.20.0 分发文件及校验值](https://nodejs.org/dist/v24.20.0/)、[Vite 环境要求](https://vite.dev/guide/)。
- [vcpkg 固定发行版](https://github.com/microsoft/vcpkg/releases/tag/2026.07.29)、[CMake 集成方式](https://learn.microsoft.com/en-us/vcpkg/users/buildsystems/cmake-integration)。
- [Protobuf 33.4](https://github.com/protocolbuffers/protobuf/releases/tag/v33.4)、[Protobuf 跨版本兼容约束](https://protobuf.dev/support/cross-version-runtime-guarantee/)。
- [MySQL 官方镜像](https://hub.docker.com/_/mysql)、[Redis 官方镜像](https://hub.docker.com/_/redis)、[Prometheus 下载](https://prometheus.io/download/)、[Grafana 下载](https://grafana.com/grafana/download)。
