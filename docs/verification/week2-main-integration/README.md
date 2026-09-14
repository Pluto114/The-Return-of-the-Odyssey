# 第二周主分支整合检查

日期：2026-09-14。检查对象：`02bca63f22aeb0d1aabb9b1a546a5f6d3e75163c` 的代码与依赖；后续提交只增加本记录和协作文档。

## 合入范围

从 `origin/main@d4809ce` 创建独立整合分支，合入 B `d5cff55`、C `94978b6`、A `2b68e73`、D `263a031`，四次合并均无文件冲突。随后修正 Linux 独立模块构建所需的 `github.com/prometheus/procfs v0.21.1` 间接依赖并补齐 Server/Bot 校验和，没有改动玩法或协议。

推送前再次 fetch 时发现 A `33c26ab`（包含首关启动 `cffa848`）及 D `cf443a8`（战斗指标接入）已到达；本轮测试不覆盖这些后续提交，已在收尾文档标注为优先验证/整合的进展。

原始工作目录的未提交修改未参与本次整合；生成协议、工具和构建产物均留在忽略目录，没有提交到仓库。

## 已执行检查

| 检查 | 环境 / 命令 | 结果 |
| --- | --- | --- |
| 协议生成 | protoc 33.4，固定 protoc-gen-go；`pwsh -File scripts/generate-proto/generate.ps1` | Go、C++ 与 descriptor 生成成功 |
| Server/Bot 独立模块 | Windows amd64，Go 1.26.8；`pwsh -File scripts/test/check.ps1` | 两模块 `GOWORK=off`，download/verify、全包 test、vet 通过；依赖修复后重新执行通过 |
| 客户端全目标构建 | Windows x64，MSVC 19.51.36248，CMake 3.31.6、Ninja；`cmake --build --preset client-windows` | 50 步构建完成，客户端、窗口诊断程序与四套测试均成功链接 |
| C++ 自动测试 | `ctest --test-dir build/client-windows --output-on-failure` | core/net/logic/protocol 全部通过，4/4，3.14 秒 |
| Linux Server race 与 vet | WSL、Go 1.26.8 linux/amd64、CGO/gcc；`GOWORK=off go test -race ./...` 与 `go vet ./...` | 使用修复后的真实 go.mod，15 个有测试包通过，vet 通过 |
| Linux Bot race 与 vet | 同上；`GOWORK=off go test -race -count=1 ./...` 与 `go vet ./...` | 2 个有测试包通过，vet 通过 |

CMake 配置使用仓库 `client-windows` preset，并复用本机已经安装的固定 vcpkg 依赖；通过 `VCPKG_INSTALLED_DIR` 指向现有安装目录、`VCPKG_MANIFEST_INSTALL=OFF` 禁止本次重新安装依赖。因此本次是全源码重新编译，**不是干净机器依赖安装或发布包验收**。

复现客户端检查时，先按 [SETUP](../../SETUP.md) 安装项目工具链和客户端依赖，再执行：

```powershell
. ./scripts/env.ps1 -Client
pwsh -File scripts/generate-proto/generate.ps1
cmake --preset client-windows
cmake --build --preset client-windows
ctest --test-dir build/client-windows --output-on-failure
```

Linux 复现时先生成协议，设置 `CGO_ENABLED=1`，在 `server`、`bot` 两个目录分别运行：

```bash
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

首次 Linux 独立 Server 检查提示 go.mod 需要更新；检查依赖差异后仅补入 procfs，保留既定 MySQL 等依赖，没有无关执行整仓 tidy。依赖校验产生的 checksum 更新已随代码提交。

Linux 检查复用本机模块和编译缓存，`GOTOOLCHAIN=local`、`GOPROXY=off`、`GOSUMDB=off`，未依赖联网下载；模块完整性由上面的 Windows `go mod verify` 另行检查。Server 最终 race 输出包含 gameserver、network、router、room、game、director、equipment、reward 等全部实际测试包，无失败或竞态报告；`[no test files]` 包不计作测试通过数量。

## 未执行与当前限制

- 本次没有运行两台真实 C++ 客户端首关/三关联调、实际 2/10/100 Bot 持续负载；正式入口缺 StartStage 等编排，当前不能宣称通关。
- 没有执行真实 Redis/MySQL 容器故障或结算集成；普通 persistence 测试不等于带 integration 标签的真实后端测试。
- 没有重新验收 Dashboard、Docker Compose、Grafana 或干净 clone 发布流程；这些不是本次新增玩法模块测试的覆盖范围。
- 客户端阶段/Ready、恢复匹配门控、药水/装备显示与预测、个人硬编码端点仍需收口；A/D 负责接通正式流程，C/B 配合复核。

所有剩余任务、责任人和最终验收门槛统一见 [A / D 收尾需求](../../plans/WEEK2-AD-FINALIZATION.md)。本次 `main` 是统一联调基线，不是最终版，不创建发布标签。
