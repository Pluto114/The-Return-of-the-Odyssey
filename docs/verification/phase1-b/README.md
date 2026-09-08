# 角色 B 第一阶段：本地验证记录

日期：2026-09-08。范围：B 的 World / Movement / Room，不是全组阶段验收或 Bot 性能报告。
工作分支：`codex/game-core-phase1`，基于 develop 的 `aa65d00aa3ef18847d5568d74fd1d713ebc3386a`。
本记录对应最初的移动核心验证，之后已随 `c094fee` 提交并推送；D 评审与 develop 联调尚未在此记录中确认。后续战斗增量见 [新验证记录](../combat-core/README.md)。

## 实现状态

| 计划 | B 的当前结果 |
| --- | --- |
| D1 领域对象、命令边界、离线 Tick 与移动 | 已实现，离线测试通过 |
| D2 Room 单写入者、入退房、输入确认、快照 | 已实现，房间组件测试通过；等待真实 Session / 网络接入 |
| D3 多人移动、多房隔离、异常输入、生命周期、Tick 采样 | B 范围内测试通过；等待 A/C/D 联调及 D 评审 |

接入接口、默认参数、过载处理与所有权说明见 [核心交接文档](../../architecture/GAME-CORE-PHASE1.md)。

## 已执行检查

| 检查 | 环境 / 结果 |
| --- | --- |
| `pwsh -File scripts/doctor.ps1 -Role server` | Windows；Git / Go 1.26.8 / protoc 33.4 检查通过 |
| `pwsh -File scripts/test/check.ps1` | Windows；依赖验证、server 测试、go vet 通过；Bot 无源码，明确 SKIP |
| B 包的 `go test -count=1` 与 `go vet` | Windows；21 个顶层 Test 和 1 个可执行 Example 均通过 |
| B 包合并 coverage | Windows；`game/...` 与 `room/...` 合并语句覆盖率 98.2%；不是全项目业务覆盖率 |
| B 包 `go test -race -count=1 -timeout=90s` | WSL Ubuntu 20.04 / linux-amd64 / GCC 9.4.0 / Go 1.26.8；测试和示例通过，未报告竞态 |

Linux 的最终测试事件保存在 [linux-race.jsonl](linux-race.jsonl)，包含每个测试/子测试及包的结果。
本次接受测试的 7 个 Go 源文件指纹见 [source-sha256.json](source-sha256.json)，用于识别尚未提交的具体工作树版本。
测试共涉及 4 个 Go package，其中 entity/systems 由 game/room 的测试间接验证，没有伪造占位测试。

普通测试与 race 检查覆盖：

- 30 Tick 轴向/斜向移动距离均为 5 单位；每 Tick 10 个输入包仍只推进一次。
- 零方向、模拟摇杆幅度、超大有限向量、NaN/Inf、边界位置和实际速度。
- 输入只入队/暂存时不确认；最新有效输入才确认；重复/旧序号、已过期输入不刷新移动生命周期。
- Session 入房回执、未入房输入、重复 Join、改绑错误、满房、跨房输入拒绝。
- 两名玩家独立移动、两个房间互不影响、退房重入不消费旧绑定输入。
- 队列确实达到容量上限、每 Tick 消费预算有效、满输入队列不阻塞 Leave 通道。
- 慢快照/指标消费者不阻塞房间；快照更新合并；修改任意读出副本不污染 World。
- 20 轮房间创建、加入、离开、空房关闭；未处理回执得到关闭结果；父 context 取消；并发生产者、消费者与关闭路径。

Room 的时序用 `testing/synctest` 驱动虚拟时间，实际启动并检查 goroutine；这些结果不能用于声称实际 30Hz、p99 或 10 分钟稳定性达标。

## 环境补充与未完成项

Windows 原有 MinGW 环境执行 race 测试时，测试进程以 `0xc0000139` 退出，未完成检测；该结果没有计为通过，也未更改全局编译器。
随后使用项目规定版本的 Linux Go 在 WSL 完成 race 检查。
为此仅在忽略目录 `.tools/linux-go/` 放置 Linux Go 1.26.8，并使用 WSL 中已有 GCC；没有改全局 PATH 或其他角色的工具链配置。
Linux archive SHA256：`d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b`，下载后与 go.dev 发布元数据核对一致。

| 尚未完成 | 后续责任 |
| --- | --- |
| 协议字段、DTO 转换、登录/Session 状态、TCP Reader/Writer、真实 EOF 通知 | A；B 协助接口联调 |
| 双 C++ 客户端 5 分钟同房移动与同 Tick 状态比对 | C 主测，A/B 配合 |
| lobby 房间注册、跨房唯一 Session 分配、断线重试与注销 | D + A；B 的 Room 只保证单房绑定 |
| 真实 Socket 慢写与可靠发送队列饱和 | A 主测，D/B 配合 |
| Prometheus / pprof / Grafana、10 Bot / 5 房间 10 分钟及 Tick 性能线 | D 主测，B 提供已实现的采样接口 |
| D 的代码评审、PR / develop 集成、GitHub CI | 团队后续协作 |

T05–T07 的领域行为已有测试证据；T09–T12 仅 B 的房间组件部分有证据。不能据此把整个测试项或第一阶段标成全组通过。
