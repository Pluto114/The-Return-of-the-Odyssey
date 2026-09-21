# 动态远征验证（2026-09-21）

本轮实现默认十二关，服务端下发总关数和导演真实调整信息；基础弹匣 12 发，R 换弹 45 Tick（30 Hz 下 1.5 秒），武器可扩容至 16/18/24 发。客户端新增事件驱动命中闪白、伤害跳字、击杀粒子、受伤边框和命中准星，F4 可简化新增特效。

## 已执行

- `go test ./server/... ./bot/...`、`go vet ./server/... ./bot/...` 通过。
- 独立 Release 目录 `build/client-polish-release` 编译通过，CTest 4/4 通过。
- 新增弹药领域测试：12 发打空后停止开火、无自动装弹、45 Tick 手动装弹、装弹时可移动且不能开火、重复请求不重置倒计时、包合并保留一次性输入、扩容不赠弹、同槽替换不叠加、容量降低裁剪、死亡取消装弹。
- 真实 TCP 路由回归覆盖 1→2、3→4、11→12，断言总关数、导演难度及过关补满弹药；原胜利/失败重开回归保留。
- 客户端协议新增字段往返、服务端总关数终局判定（含未知 0）、特效衰减和数量上限测试。
- 独立服务器 `127.0.0.1:17777`、2 Bot、12 关、约 68 秒：2 成功、0 失败，每位玩家 12 次清关，总 22 次奖励与 Ready。
- Bot 报告最后一位玩家观察到 10 次权威装弹启动；导演难度 1.00→5.25055，最后几关增幅为 17%、13%、13%、4%。这是表现驱动结果，不是固定关卡曲线。导演低表现降难路径另有规则单元测试。
- 原始报告见 [双 Bot 十二关报告](two-bot-twelve-stage.json)。

复现命令（另起端口的服务器需指定十二关）：

```powershell
.\build\polish\loadbot.exe -server 127.0.0.1:17777 -mode functional -clients 2 -stages 12 -duration 5m -ramp 0s -resume=false
```

本次 E2E 关闭恢复和结果入库，未声称验证真实 Redis/MySQL、百人负载或双实体设备的新版视觉体验。已有旧版真人联机成功不代替新版换弹和特效验收。

## 更新和双机验收

其他成员拉取源码后，需要重新生成协议并编译服务端、客户端（构建产物不提交到 Git）。已完成工具链安装的 Windows 开发环境可在项目根目录执行：

```powershell
git switch main
git pull --ff-only origin main
. .\scripts\env.ps1 -Client
pwsh -NoProfile -File scripts/generate-proto/generate.ps1
go build -o build/polish/gameserver.exe ./server/cmd/gameserver
cmake --preset client-windows -B build/client-polish-release -DCMAKE_BUILD_TYPE=Release
cmake --build build/client-polish-release --parallel 4
ctest --test-dir build/client-polish-release -C Release --output-on-failure
```

本地存在未提交修改时先妥善提交或暂存，不要强制覆盖；若 `--ff-only` 提示分叉，先处理分支差异再继续。不要混用更新前的服务端、客户端或生成协议。

1. 先结束旧局并退出旧客户端，停止旧 gameserver；脚本不会强制中断当前游戏。
2. 项目根目录运行 `pwsh -File scripts/start-polish-server.ps1`，默认监听 `0.0.0.0:7777`、十二关；`-Stages 20` 可延长远征。
3. 两台设备均解压 `output/odyssey-client-dynamic-expedition-windows-x64.zip`，按 README 选择局域网或自定义服务器地址；不要混用旧客户端。
4. 如果原防火墙规则绑定旧 exe 路径，需给 `build/polish/gameserver.exe` 添加仅允许指定测试客户端 IP、TCP 7777 的入站规则；不要关闭防火墙。保持管理端口仅本机使用。
5. 验收：持续射击 12 发后按 R；装弹中移动且不出子弹；拾箱后容量变化；继续到第四关；比较导演 HUD 和上关表现；查看命中、击杀、己方受伤反馈并测试 F4；清关后双方选奖励并准备。

本机现有 `Odyssey Joint Test TCP 7777 - 10.22.45.122` 规则绑定旧文件 `build/final-sync/gameserver.exe`，不会自动放行新路径。如仍使用之前那台客户端，可在服务器的管理员 PowerShell 中新增：

```powershell
New-NetFirewallRule -DisplayName 'Odyssey Dynamic TCP 7777 - 10.22.45.122' -Direction Inbound -Action Allow -Protocol TCP -LocalPort 7777 -Program 'E:\The-Return-of-the-Odyssey\build\polish\gameserver.exe' -RemoteAddress 10.22.45.122 -Profile Any
```

客户端 IP 变化时只替换为实际测试设备地址。此命令未由本轮自动执行，原服务器与防火墙规则保持不变。

客户端打包脚本为 `scripts/package-polish-client.ps1`，只复制运行依赖与指南，不包含日志、凭据或源代码。已存在的包不会被自动覆盖。
