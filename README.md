# The Return of the Odyssey

基于 Go 服务端权威架构的多人 Roguelike 实训项目。
当前功能分支进度与待验收项见 [项目开发进度（2026-09-16）](docs/PROJECT-STATUS.md)；下面的 9 月 15 日描述仅说明当时的主分支基线。
当前阶段：**D 单关平台集成基线（2026-09-15）**。正式入口已启动首关战斗；D 已接入装备同源配置、战斗 Bot、真实指标与管理看板、Redis 恢复存储和 MySQL 异步结算模块。完整奖励、三关、恢复和终局写入仍需整合 A 的新入口与 C 的兼容修复，当前不是最终完整玩法版本。

远程仓库：[Pluto114/The-Return-of-the-Odyssey](https://github.com/Pluto114/The-Return-of-the-Odyssey)。团队日常开发从 develop 创建功能分支。

团队开工请先阅读 **[环境配置清单与安装步骤](docs/SETUP.md)**，并遵守 [协作约定](CONTRIBUTING.md)。
**本轮必读：[A / D 收尾需求与最终合并验收](docs/plans/WEEK2-AD-FINALIZATION.md)**。从本次 `main` 同步后继续各自功能分支，A/D 完成接线与共同验收后再合并最终版本；当前检查见 [整合验证记录](docs/verification/week2-main-integration/README.md)。历史与职责入口见 [当前协作说明](docs/plans/CURRENT-COLLABORATION.md)。
最新检查：[9 月 15 日 D 交付复查与剩余项](docs/verification/phase2-final/D-SYNC-REVIEW-2026-09-15.md)，包含结算关闭修复及真实 Bot 单关结果。
前三天的角色目标、完成标准和联调测试见 [第一阶段计划](docs/plans/PHASE1-DAYS1-3.md)。角色 B 接入接口及默认参数见 [房间与游戏核心交接文档](docs/architecture/GAME-CORE-PHASE1.md)。
完整设计保留在 [ARCHITECTURE.md](ARCHITECTURE.md)，本次初始化的具体选择记录在 [环境决策](docs/architecture/ENVIRONMENT.md)。

## 目录

| 路径 | 用途 | 负责人 |
| --- | --- | --- |
| proto/ | 唯一协议源，已有 v0 系统、会话、匹配、游戏和关卡消息 | A，C 复核 |
| server/internal/network、session | TCP、Frame、连接与会话 | A，D 参与会话/重连 |
| server/internal/room、game | 房间、世界、系统、关卡、Director | B |
| client/ | C++20 客户端与实时同步 | C |
| bot/ | 独立 Go Bot 模块 | D |
| server/internal/lobby、persistence、metrics | 匹配、持久化、观测 | D |
| dashboard/ | Vue 3 / Vite / ECharts 工程配置与静态占位页 | C + D |
| deploy/ | Docker、Prometheus、Grafana | D |
| data/ | 装备、怪物、全局修饰器静态数据预留 | B + C |
| docs/ | 架构、协议、压测、图示、会议记录 | 全组 |
| scripts/ | 环境安装、检查、生成、构建入口 | D |

## Windows 开始使用

先安装 Git、PowerShell 7；客户端开发还需要 Visual Studio 的“使用 C++ 的桌面开发”组件。
在仓库根目录执行：

```powershell
pwsh -NoProfile -File scripts/bootstrap.ps1
# 打开 PowerShell 7 后，在当前终端加载项目工具路径：
. ./scripts/env.ps1
pwsh -File scripts/generate-proto/generate.ps1
pwsh -File scripts/test/check.ps1
npm --prefix dashboard ci
pwsh -File scripts/build/build.ps1 -Target dashboard
```

客户端依赖验证与基础设施启动见 [SETUP.md](docs/SETUP.md)。
当前 gameserver 可运行 Login → Match → Join → 首关战斗 → 清场。玩家实际操作及遇到失败画面时的处理见 [玩家操作指南](docs/PLAYER-GUIDE.md)；客户端构建见 [client/README.md](client/README.md)，Bot 见 [bot/README.md](bot/README.md)。Bot 单关已有真实 TCP 验证；奖励、三关、恢复与终局落库仍按收尾清单整合，不能将已装配的存储模块等同于全流程已完成。
客户端当前默认连接 `127.0.0.1:7777`，跨主机体验可以配置 `ODYSSEY_SERVER_HOST` 和 `ODYSSEY_SERVER_PORT`。不要直接运行单个 `main.cpp`，应生成协议后构建整个 CMake 客户端目标。
角色 B 的离线演示可在加载环境后运行 `go run ./server/cmd/core-demo`；战斗、装备奖励、连续关卡和恢复结果接口分别见 [首关战斗交接文档](docs/architecture/COMBAT-CORE.md)、[装备与奖励领域接口](docs/architecture/EQUIPMENT-REWARD.md)、[Director 接口](docs/architecture/DIRECTOR.md) 和 [恢复与结果接口](docs/architecture/RESUME-GAME-RESULT.md)。

## 固定架构边界

- TCP + 16 字节大端消息头 + Protobuf；Frame Sequence 与 Input Sequence 分开。
- Room goroutine 是对应 World 唯一写入者；网络层只投递 Command。
- Domain、Network DTO、Command/Event 分层，禁止用 protobuf 对象承载领域状态。
- 服务端模拟 30Hz、快照 10Hz、怪物 AI 决策 10Hz，渲染独立。
- 客户端负责预测、校正和插值；伤害、碰撞和最终状态由服务端计算。

## 验证与待办

本机验证结果见 [初始化验收记录](docs/VERIFICATION.md)。
B 的原实现检查见 [角色 B 第一阶段验证记录](docs/verification/phase1-b/README.md)，合入 A 后的 TCP 移动/战斗检查见 [A/B 集成验证](docs/verification/network-core/README.md)，D6–D7 服务端核心进展见 [奖励接线验证](docs/verification/week2-b-d6-world/README.md) 和 [Director/三关验证](docs/verification/week2-b-d7/README.md)，D9 数据见 [游戏核心性能与隔离基线](docs/benchmark/WEEK2-B-D9.md)。这些记录各有范围，不能替代本轮完整入口的双客户端三关、恢复、真实数据库和 100 Bot 验收。
后续目标见 [一周计划](docs/plans/WEEK2-DAYS4-10.md)，本轮剩余任务和发布门槛以 [A / D 收尾清单](docs/plans/WEEK2-AD-FINALIZATION.md) 为准。
