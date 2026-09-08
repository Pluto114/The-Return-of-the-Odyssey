# The Return of the Odyssey

基于 Go 服务端权威架构的多人 Roguelike 实训项目。
当前分支阶段：**角色 B 第一阶段核心开发**。已有可独立测试的房间与权威移动模块；网络协议消息、客户端游戏逻辑、匹配服务及数据库业务尚待对应角色接入。

远程仓库：[Pluto114/The-Return-of-the-Odyssey](https://github.com/Pluto114/The-Return-of-the-Odyssey)。团队日常开发从 develop 创建功能分支。

团队开工请先阅读 **[环境配置清单与安装步骤](docs/SETUP.md)**，并遵守 [协作约定](CONTRIBUTING.md)。
前三天的角色目标、完成标准和联调测试见 [第一阶段计划](docs/plans/PHASE1-DAYS1-3.md)。角色 B 接入接口及默认参数见 [房间与游戏核心交接文档](docs/architecture/GAME-CORE-PHASE1.md)。
完整设计保留在 [ARCHITECTURE.md](ARCHITECTURE.md)，本次初始化的具体选择记录在 [环境决策](docs/architecture/ENVIRONMENT.md)。

## 目录

| 路径 | 用途 | 负责人 |
| --- | --- | --- |
| proto/ | 唯一协议源，当前只有合法的空 schema | A，C 复核 |
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
当前没有 gameserver、Bot 或游戏客户端可执行入口；不要将环境构建成功视为游戏已可运行。

## 固定架构边界

- TCP + 16 字节大端消息头 + Protobuf；Frame Sequence 与 Input Sequence 分开。
- Room goroutine 是对应 World 唯一写入者；网络层只投递 Command。
- Domain、Network DTO、Command/Event 分层，禁止用 protobuf 对象承载领域状态。
- 服务端模拟 30Hz、快照 10Hz、怪物 AI 决策 10Hz，渲染独立。
- 客户端负责预测、校正和插值；伤害、碰撞和最终状态由服务端计算。

## 验证与待办

本机验证结果见 [初始化验收记录](docs/VERIFICATION.md)。
B 的新增实现检查见 [角色 B 第一阶段验证记录](docs/verification/phase1-b/README.md)。A/C 确定协议字段与 Message ID，A/D 接入房间接口后共同联调最小实时闭环。
三周功能计划、玩法与性能目标以架构文档为参考；尚无性能数据。
