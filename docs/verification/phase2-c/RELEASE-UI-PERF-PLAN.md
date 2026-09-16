# Release UI 性能验收方案（角色 C）

状态：**方案就绪，待 Release 构建预设到位即可执行**（预设由 D/A 提供，见 §2）。
依据：定稿提示词 §四.3 的性能验收唯一口径；指标语义与 [CLIENT-HUD-DESIGN.md](../../architecture/CLIENT-HUD-DESIGN.md) §附录一致。
被测对象：`build/client-windows-release/client/odyssey_client.exe`。

---

## 1. 验收口径（不可替换、不可放宽）

1. **只在 Release 下测定**；Debug 数字一律不作为验收依据（只可用于趋势参考，且需标注）。
2. **同一战斗场景**下比较 **UI 开启 vs UI 关闭**的**整帧 CPU 时间**，二者使用同一构建、同一场景、同一窗口尺寸与缩放。
3. 连续测量 **≥600 帧**，报告**均值与最大值**（本方案另附中位数与 p95 供诊断）。
4. 判定：**UI 每帧增量 ≤ 1.5 ms**，即 `mean(UI on) − mean(UI off) ≤ 1.5 ms`。
   - 同时报告 `max` 差值；若 max 超预算但均值达标，必须写明是否为孤立抖动（给出 p95 与该帧上下文）。
5. **剔除首帧字体图集加载开销**：客户端已内建该规则（`PerfCapture` 丢弃计数前的 10 个预热帧，
   其中第 0 帧即字体图集上传帧），报告不得再手工把首帧算进去。
6. 报告必须**附测试机硬件与运行环境说明**（§2.4 模板），否则结果不可复现、不予采信。

> "UI 层"的定义（`--no-ui` 关闭的范围）：HUD 文字、分段血条与受伤残影、准星、受击方向弧、伤害飘字、操作提示、F1/F2/F3 面板。
> **不关闭**：RT 渲染管线与双层清屏、`-540` 翻转 blit、世界（网格 + 玩家/怪物/投射物）、以及全部网络与游戏逻辑。因此差值反映的正是 UI 的整帧增量。

---

## 2. 前置条件

### 2.1 Release 构建预设（**阻塞项：需 D/A 提供**）

`CMakePresets.json` 当前只有 Debug（`client-base` 里 `CMAKE_BUILD_TYPE="Debug"`，`client-windows`
继承它）。验收需要一条等价的 Release 配置。建议改动（三处，都是共享文件，由 D 协同修改、A 统一组装）：

1）`CMakePresets.json` → `configurePresets` 追加（`binaryDir` 由 `client-base` 的
`${sourceDir}/build/${presetName}` 自动给成 `build/client-windows-release`，因此必须叫这个名字）：

```json
{
  "name": "client-windows-release",
  "displayName": "Windows client (Release)",
  "inherits": "client-base",
  "condition": { "type": "equals", "lhs": "${hostSystemName}", "rhs": "Windows" },
  "cacheVariables": {
    "CMAKE_BUILD_TYPE": "Release",
    "VCPKG_TARGET_TRIPLET": "x64-windows",
    "VCPKG_HOST_TRIPLET": "x64-windows",
    "CMAKE_C_COMPILER": "cl",
    "CMAKE_CXX_COMPILER": "cl"
  }
}
```

2）同文件 `buildPresets` 追加：

```json
{ "name": "client-windows-release", "configurePreset": "client-windows-release", "jobs": 4 }
```

3）`scripts/build/build.ps1` 第 2 行的 `ValidateSet` 加 `'client-release'`，并在第 12 行的映射里加：

```powershell
} elseif ($Target -eq 'client-release') { 'client-windows-release'
```

之后验收与发布都走同一条命令：

```powershell
pwsh -File scripts/build/build.ps1 -Target client-release
# 产物：build/client-windows-release/client/odyssey_client.exe
```

**C 不改动这三个文件**（避免与 D 的构建工作冲突）；下面 §2.2 是等价的临时路径。

### 2.2 预设未就位时的临时路径（不阻塞测量，不改共享文件）

**一条命令**（推荐）：

```powershell
pwsh -File scripts/verify/build-client-release.ps1
# 产物：build/client-windows-release/client/odyssey_client.exe
# -Reconfigure 强制重新配置；-BuildDir 换目录；脚本拒绝复用非 Release 的已有缓存
```

该脚本把下列变量按 `client-windows` 预设 1:1 复制，只把 `CMAKE_BUILD_TYPE` 换成 `Release`，并在**独立目录**配置（不碰 `build/client-windows`，不会把 Debug 树翻成 Release），也**不改** `CMakePresets.json` / `build.ps1`。D 的共享预设落地后改用 `pwsh -File scripts/build/build.ps1 -Target client-release`，并删除该脚本与本节。

等价手工命令（供核对脚本行为）：

```powershell
cd <repo-root>          # 你本地的仓库根目录
. .\scripts\env.ps1 -Client
cmake -S . -B build/client-windows-release -G Ninja `
  -DCMAKE_BUILD_TYPE=Release `
  -DCMAKE_TOOLCHAIN_FILE="$env:VCPKG_ROOT/scripts/buildsystems/vcpkg.cmake" `
  -DVCPKG_MANIFEST_DIR="$PWD/client" -DVCPKG_TARGET_TRIPLET=x64-windows -DVCPKG_HOST_TRIPLET=x64-windows `
  -DODYSSEY_CLIENT_DEPS=ON -DCMAKE_C_COMPILER=cl -DCMAKE_CXX_COMPILER=cl
cmake --build build/client-windows-release --target odyssey_client
```
> 这段复制了预设里的变量，**D 的预设落地后请改用预设并删除本节**，以免两处漂移。

### 2.3 场景与进程布置（保证"同一战斗场景"且只有一个 GUI 进程被测）

- 本机起 `gameserver`（默认 `127.0.0.1:7777`，见 [FIRST-STAGE-PLAYTEST.md](FIRST-STAGE-PLAYTEST.md) §2）。
- 第二名玩家用 **`loadbot`**（不是第二个 GUI 客户端）：房间容量为 2，匹配满员后服务器才 `StartStage`，用 bot 凑人数可以让**被测客户端是唯一的 GUI 进程**，避免两个客户端互相抢占 CPU 污染数据。
- 两次运行（UI 开 / UI 关）之间**不重启服务器**，场景保持同一关；若中途清场进入下一关，需重跑该轮并在报告中注明。
- **bot 必须活过整轮测量**：`-mode functional` 打完一局就退出，因此 3 轮（6 次客户端运行）会从第 2 轮起没有对手 → 客户端进不了 `playing` → 没有数据。用 **`-mode sustained`** 并给足 `-duration`（读完一局自动开新会话，直到时限结束）。`-stages 1` 是有意的：奖励/Ready 路由尚未进 main，只要求第一关的战斗与 `StageCleared`。

**目录与终端要点**

- `go.work` 在仓库根，两个 Go 模块是 `./server` 与 `./bot`：`go run ./server/...` 与 `go run ./bot/...` **必须在仓库根执行**。
- `scripts/*.ps1` 自己定位仓库根，可在任意目录执行；但**必须用 PowerShell 7**（它们与 `env.ps1` 都带 `#requires -Version 7.0`）。**在 Windows PowerShell 5.1 窗口里不要 dot-source `env.ps1`**，会直接报 `#requires` 版本错误 —— 用 `pwsh -File <脚本>` 即可。
- **服务器与 bot 不需要 `env.ps1`**：`go` 已在 PATH，`env.ps1` 只设置 `GOBIN`/npm 缓存/vcpkg 与 MSVC 环境（那是客户端构建需要的）。若确实要那套环境，请在 `pwsh`（PS7）窗口里执行。

```powershell
# 终端 1：服务器（工作目录 = 仓库根；不需要 env.ps1）
cd <repo-root>
go run ./server/cmd/gameserver

# 终端 2：第二名玩家（工作目录 = 仓库根；sustained 保证跨全部轮次都有对手）
cd <repo-root>
go run ./bot/cmd/loadbot -mode sustained -clients 1 -stages 1 -duration 15m -ramp 0s -use-potion=false

# 一次性依赖（已完成，可跳过；每个模块一次，上游新增 filippo.io/edwards25519）
go -C server mod download all
go -C bot    mod download all
# 代理：Go 默认的 proxy.golang.org 在大陆网络不通，且 Go 不读 Windows 系统代理，需显式配置：
#   go env -w GOPROXY=https://goproxy.cn,direct
#   go env -w GOSUMDB=sum.golang.google.cn      # 校验库也要换，否则 sum.golang.org 同样超时
# 已实测（2026-09-16）：两个模块下载 exit 0，且 gameserver 与 loadbot 均编译成功；缓存填满后运行不再需要网络
```

> 第 ① 步的 Release 构建**不会重编译第三方依赖**：`env.ps1` 把 vcpkg 二进制缓存设到仓库内 `.tools/vcpkg-cache`（已实测 17 个包 / 144 MB），新构建树按 ABI 哈希直接还原。若该目录被清空，vcpkg 会改为从源码构建（可能数十分钟并需要网络）。

### 2.4 硬件与环境说明模板（报告必须包含）

| 项 | 填写 |
| --- | --- |
| CPU | 型号、物理/逻辑核数、基准频率（示例：Intel Core i5-12600K，6P+4E / 16 线程，3.7 GHz） |
| 内存 | 容量与频率、是否双通道 |
| GPU / 驱动 | 型号、驱动版本（示例：Intel UHD 770，32.0.101.6556） |
| 存储 | SSD/HDD（影响首帧资源加载，只影响启动阶段） |
| 操作系统 | 版本与 build（示例：Windows 11 24H2，26100.x） |
| 显示 | 分辨率、系统缩放（示例：2560×1440 @125%） |
| 客户端窗口 | 启动后的窗口尺寸与 `viewport scale`（取自启动日志） |
| 电源模式 | 高性能 / 平衡、是否插电 |
| 后台负载 | 是否有浏览器/杀软扫描/其他构建在跑；两次运行的差异 |
| 构建信息 | 提交号、`CMAKE_BUILD_TYPE=Release`、编译器版本、vcpkg 基线 |

---

## 3. 测量流程（逐步可执行）

> **一键路径**：`scripts/verify/client-release-ui-perf.ps1` 已把本节与 §5 自动化——它按轮次跑
> UI 开/关、解析 CSV、算出 Δ 与轮间离散度、判定 PASS/FAIL/UNSTABLE，并写出 `report.md` +
> `hardware.md` 模板。它**不启动 bot**、也不在两次运行之间重启服务器（场景必须一致），并且
> **拒绝**对看起来不是 Release 的二进制出结论。手动流程保留在下面，用于核对脚本行为或分步排查。
>
> ```powershell
> pwsh -File scripts/verify/client-release-ui-perf.ps1 -Rounds 3 -Frames 600
> # 服务器在别的机器/探测被拦时：-NoServer -ServerHost 192.168.1.20
> # 只让脚本起服务器（仍需自己让第二名玩家入房）：-StartServer
> ```
>
> 脚本**不启动 bot**，所以开跑前必须已有 bot 在等匹配（§2.3 的 sustained 命令）；判据是客户端日志里
> 出现 `main: perf capture started (stage=playing alive=yes)`。这一行始终不出现就说明场景没起来
> （房间没满 / 匹配没成 / 玩家死亡），此时**没有有效数据**，而不是"性能很好"。

客户端内建采集：设置 `ODYSSEY_PERF_FRAMES`（1..4096，建议 600）后，客户端**先待命**，并在
`ODYSSEY_PERF_TRIGGER=playing` 时**从进入真实战斗的第一帧开始计数**（判据与"可否发送输入"完全一致：
已入房 + 本会话已有快照 + 无恢复 + 存活 + 阶段为 playing），计数满后写 CSV、打印汇总并**自行退出**。
这样 UI 开/关两次运行测的是同一类战斗场景，而不是把 600 帧预算花在登录/匹配/大厅上。

> 触发点为什么必须存在：若从进程启动即计数，600 帧（约 10 秒）几乎全部落在登录与匹配阶段，
> 既不是"同一战斗场景"，也不含真实战斗渲染负载。`ODYSSEY_PERF_TRIGGER=immediate` 仅用于
> 快速冒烟（确认采集通路可用），**不得用于验收数字**。

计数前的**预热帧**（`PerfCapture::kWarmupFrames = 10`）被丢弃：第 0 帧承担字体图集上传与首批
着色器/批次初始化——这正是提示词要求剔除的首帧开销；其余几帧吸收刚进场时的首批批次初始化。
两次运行丢弃的帧数完全相同，因此对差值判定无影响。

```powershell
# 脚本自己定位仓库根，可在任意目录执行
$exe = ".\build\client-windows-release\client\odyssey_client.exe"
$dir = "docs\verification\phase2-c\release-ui-perf-$(Get-Date -Format yyyy-MM-dd)"
New-Item -ItemType Directory -Force $dir | Out-Null

# 公共环境：帧预算 + 触发时机（进入战斗才计数）
$env:ODYSSEY_PERF_FRAMES  = '600'
$env:ODYSSEY_PERF_TRIGGER = 'playing'

# --- 第 1 轮：UI 开 ---
$env:ODYSSEY_PERF_LOG = "$dir\ui-on-1.csv"
& $exe --server 127.0.0.1:7777 | Tee-Object "$dir\ui-on-1.log"

# --- 第 2 轮：UI 关（同一场景、同一关，仅多一个开关） ---
$env:ODYSSEY_PERF_LOG = "$dir\ui-off-1.csv"
& $exe --server 127.0.0.1:7777 --no-ui | Tee-Object "$dir\ui-off-1.log"

# --- 建议：各重复 3 轮（-1/-2/-3），取轮次间中位数，用于判断环境噪声 ---
```

每次运行在 stdout 上按顺序留下三行关键证据：

```text
main: perf capture 600 frames trigger=playing -> ...\ui-on-1.csv
main: perf capture started (stage=playing alive=yes)
main: perf frames=600 mean=0.412ms median=0.401ms p95=0.550ms max=1.230ms min=0.310ms ui=on trigger=playing
```

若第二行始终不出现，说明客户端没有进入战斗（没人开局/匹配未满/玩家死亡），此时**没有有效数据**，
而不是"性能很好"——请检查 §2.3 的服务器与 bot。

CSV 形如（`#` 开头两行是汇总，随后是逐帧）：

```text
# frames,mean_ms,median_ms,p95_ms,max_ms,min_ms
# 600,0.4123,0.4010,0.5500,1.2300,0.3100
frame,cpu_ms
0,0.4123
...
```

快速汇总（无需额外工具）：

```powershell
foreach ($f in Get-ChildItem "$dir\*.csv") {
  $s = (Get-Content $f | Where-Object { $_ -like '# *' } | Select-Object -Index 1)
  "{0,-14} {1}" -f $f.Name, $s
}
```

判定脚本（示例：单轮结果比较）：

```powershell
function Mean($csv) { (Get-Content $csv | Where-Object { $_ -like '# *' } | Select-Object -Index 1).Split(',')[1] -as [double] }
$on = Mean "$dir\ui-on-1.csv"; $off = Mean "$dir\ui-off-1.csv"
"UI per-frame delta = {0:N4} ms (budget 1.5)" -f ($on - $off)
```

---

## 4. 结果与判定

| 指标 | 来源 | 用途 |
| --- | --- | --- |
| `mean_ms` | CSV 汇总行 | **验收主指标**：`mean(on) − mean(off) ≤ 1.5 ms` |
| `max_ms` | CSV 汇总行 | **必须报告**；超预算需给出 p95 与是否孤立抖动 |
| `median_ms` / `p95_ms` | CSV 汇总行 | 诊断（区分持续开销与偶发抖动） |
| 轮次间离散度 | 3 轮的中位数极差 | 噪声判据：极差 > 预算 30%（0.45 ms）时判"环境不稳，需重测并说明" |

**通过条件（全部满足）**：
1. Release 构建；2. 每轮 ≥600 有效帧（`# frames` 行）；3. Δmean ≤ 1.5 ms；4. 报告含硬件与环境说明；5. 场景说明（关号、bot 参数、窗口尺寸与缩放）；6. 日志中出现 `perf capture started` 且汇总行 `trigger=playing`。

**不得使用**：Debug 结果、两个 GUI 客户端互测的结果、以 FPS 代替 CPU 时间、只报均值不报最大值、把 `--no-ui` 理解为"连世界渲染也关"（那会把世界渲染时间算进 UI 增量）、`ODYSSEY_PERF_TRIGGER=immediate` 的结果（含登录/匹配帧，非同一战斗场景）。

---

## 5. 证据归档

```text
docs/verification/phase2-c/release-ui-perf-<YYYY-MM-DD>/
  hardware.md        # §2.4 模板填写
  ui-on-1.csv  ...   # 每轮的逐帧数据（含汇总行）
  ui-off-1.csv ...
  ui-on-1.log  ...   # stdout（含 `main: perf frames=... mean=... max=... ui=on/off` 与启动日志）
  report.md          # 结论表（见下）+ 是否达标 + 未达标项的原始数据片段
```

`report.md` 结论表模板：

| 轮次 | UI | frames | mean (ms) | median | p95 | max (ms) |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | on | | | | | |
| 1 | off | | | | | |
| 2 | on | | | | | |
| 2 | off | | | | | |
| 3 | on | | | | | |
| 3 | off | | | | | |

**UI 每帧增量（mean）= ____ ms ≤ 1.5 ms → 通过 / 不通过**；max 差值 = ____ ms（说明：）。

---

## 6. 复现步骤（给复核人）

1. 按 §2.4 记录环境；2. 按 §2.3 起服与 bot；3. 按 §3 跑 UI 开/关各 ≥600 帧；4. 用 §3 的汇总命令得出 Δmean；5. 按 §5 归档。整个测量约 2×（600 帧 / 60fps ≈ 10 s）+ 构建时间。
