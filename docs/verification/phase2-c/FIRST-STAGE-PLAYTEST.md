# 双客户端首关联调手册（角色 C）

状态：**可执行**（main 已接线首关启动 `server/cmd/gameserver/application.go`：匹配完成后 `rm.StartStage(firstPlan)`）。
范围：验证**正式入口 + 两个真实客户端**能否走通「连接 → 登录 → 匹配同房 → 首关开始 → 战斗 → 清场 → 奖励选择」。
**不是**三关 / 恢复 / 药水 / 终局的完整验收（那需要 A 的 `feature/network` 与 D 的 Redis/MySQL 接线合入 main，见 §5）。

配套脚本：`scripts/verify/client-first-stage-playtest.ps1`（起服 + 起两个客户端 + 日志落盘 + 打印清单）。

---

## 1. 前提

| 项 | 要求 | 检查命令 |
| --- | --- | --- |
| 客户端 | 已构建 | `Test-Path build\client-windows\client\odyssey_client.exe` |
| Go 工具链 | `go` 在 PATH，模块已下载 | `go version`；首次 `cd server; go mod download` |
| 端口 | `7777`(TCP) 空闲，`8080`(Admin) / `19091`(Metrics) 未被占用 | `netstat -ano \| findstr :7777` |
| 服务器配置 | **可缺省**：`config.Load` 在无 `.env` 时使用内置默认值（与 `configs/.env.example` 一致），`ODYSSEY_RESUME_ENABLED=false`、`ODYSSEY_RESULTS_ENABLED=false`，因此**不需要 Redis/MySQL** | 需要覆盖时：`Copy-Item server\configs\.env.example server\configs\.env` 后改（该文件已被 `.gitignore` 忽略） |

> 跨机联调：服务器用 `ODYSSEY_TCP_ADDR=0.0.0.0:7777` 起，客户端用 `--server <服务端局域网 IP>:7777`；同时放行该 TCP 端口。

---

## 2. 步骤

### A. 启动服务器（终端 1）

```powershell
cd C:\Users\xunxue\Desktop\The-Return-of-the-Odyssey-main
. .\scripts\env.ps1
go run ./server/cmd/gameserver
```

期望看到（D 的日志）：`gameserver starting env=development tcp=127.0.0.1:7777 tick_hz=30 ...` 与 `listening addr=127.0.0.1:7777`。

### B. 启动两个客户端（终端 2 / 3）

```powershell
$exe = ".\build\client-windows\client\odyssey_client.exe"
& $exe --server 127.0.0.1:7777 | Tee-Object -FilePath .\client-a.log
# 第二个终端
& $exe --server 127.0.0.1:7777 | Tee-Object -FilePath .\client-b.log
```

> 两个客户端共用同一套 dev 登录（服务器分配 Session/Player ID），因此可用相同命令行；`settings.ini` 只读使用，不冲突。

### C. 逐项核对 §3 清单，并把结果与截图归档到 §6 的目录。

---

## 3. 核对清单（期望日志 = 客户端 stdout 实际字符串）

| # | 阶段 | 期望日志（客户端 stdout） | HUD / 画面 | 结果 |
| --- | --- | --- | --- | --- |
| 1 | 启动 | `main: server endpoint 127.0.0.1:7777 (source=cli)`、`main: viewport <W>x<H> scale=<n> offset=(..)`、`main: asset root '...' (settings '...')`、`main: loaded 6 equipment entries from ...equipment.tsv` | 窗口按显示器最大整数倍打开 | ☐ |
| 2 | 连接 | `main: net state -> connecting (127.0.0.1:7777)` → `-> connected` | 顶部横幅从 `CONNECTING` 变为消失 | ☐ |
| 3 | 登录 | `main: net state -> connected` 后无错误；F1 面板 `login ok session=N player=M` | 大厅卡从 `LOGGING IN` → `MATCHMAKING` | ☐ |
| 4 | 匹配同房 | `main: match ready room=<R> teammates=1`（两个客户端的 `<R>` **必须相同**） | 大厅卡显示 `room R   player M` | ☐ |
| 5 | 首帧快照 | `main: first world snapshot tick=<T>` 与 `main: input enabled after first snapshot` | 出现竞技场网格 | ☐ |
| 6 | **首关启动（A1 关键项）** | `main: stage started index=1 tick=<T>`、`main: input enabled stage=playing alive=yes` | 顶部出现 `STAGE 01   HOSTILES <n>`；按 F1 应见 `gate=open` | ☐ |
| 7 | 移动/瞄准 | 无错误；F1 `keys(dx=..) vec(..) seq=<递增> @30Hz` 序号持续增长 | 自己（浅蓝圆）随 WASD 移动、准星跟随鼠标并缓慢自转 | ☐ |
| 8 | 战斗 | `main: damage target=<id> amount=<a> hp=<h>`、`main: projectile spawn/destroy id=..` | 怪物头顶 `-<a>` 飘字上浮淡出；怪物/玩家血条变化；命中闪环 | ☐ |
| 9 | 受伤 | 同上且 `target` 为自己的 id | 左下血条白影抖动后收回；自身周围出现朝向来源的红色弧 | ☐ |
| 10 | 清场 | `main: stage cleared index=1 tick=<T>` | 居中过渡卡 `STAGE CLEAR`；准星收起 | ☐ |
| 11 | 奖励 | `main: reward options stage=1 count=<c> deadline=<tick>`；按键 1–3 后 `main: reward choice sent id=<e>` → `main: reward applied id=<e> ok=1` | 底部奖励托盘显示装备名称/槽位/描述（此时无 `equipment#` 占位） | ☐ |
| 12 | Ready | 奖励结算后 `main: next stage ready sent stage=1 state=preparing`（按 ENTER）；若早按应打印 `main: ready blocked (<reason>) stage=.. state=..` | 按 F1 见 `ready=ready / sent` | ☐ |
| 13 | 断线表现 | 关掉服务器后：`main: net state -> failed/disconnected` → `main: connection lost -> recovery (...)` → `main: input muted (<reason>) ...`；随后 `reconnect attempt n/5` | 世界压暗 45% + 顶部红条 `LINK LOST - ...`；重连耗尽后 `press R to reconnect` | ☐ |
| 14 | 稳定性 | 每 120 帧一行 `main: frame <N> state=... elapsed=... fps=...` 持续输出 | 无「未响应」、无花屏/拖影 | ☐ |

**失败排查**：按 **F1** 看 `gate=` 一行（`not in room` / `waiting for the first authoritative snapshot` / `player is dead` / `stage is not being played` / `session recovery in progress`）——它直接给出输入被拦的原因；`stage` 一行给出权威 `state`。第 6 项若长时间停在 `gate=stage is not being played`，说明服务器没有启动首关（回到 A 的日志找 `first-stage plan failed` / `start stage failed`）。

---

## 4. 截图要求（与提示词 §五.2 一致）

| 状态 | 何时可取 |
| --- | --- |
| `disconnected` | 本地即可（不起服直接启动客户端） |
| `login` / `lobby` | 起服后、匹配前 |
| `playing` | 第 6–9 项任一时刻（需两个客户端同房） |
| `reward` | 第 11 项面板出现时 |
| `debug` | 按 F1 的诊断视图 |

统一分辨率、不得出现个人绝对路径；若某状态因环境未就绪无法取得，**必须在文档中注明"未执行 + 原因"**，不得伪造。

---

## 5. 已知限制（预期失败，不算缺陷）

1. **奖励 / Ready / Director 的服务端路由未在 main**：A 的 `feature/network`（`1bfd796` 奖励/Ready/Director + `4462d21` UsePotion）尚未与 D 的入口整合。因此第 11–12 项可能出现「选项到达但提交无效」或「ready 无响应」——这是接线顺序问题，记录为阻塞项而非客户端缺陷。
2. **药水（use_potion）**：proto 字段存在且 A 已解除服务端占位拒绝，但该提交未进 main；客户端侧尚未编码发送，故本次不测。
3. **恢复（Resume）**：main 默认 `ODYSSEY_RESUME_ENABLED=false`，第 13 项只能验证「客户端侧重连与门控」，**不能**验证令牌恢复语义（那需要 Redis + A 的恢复编排）。
4. **装备槽 / 难度摘要**：快照协议尚无 `WeaponID/RelicID/PotionID` 与难度/Director 字段（A3/A4），装备槽暂以 SPD/DMG 显示。
5. **三关与终局**：本次只覆盖首关（StageLimit=3 但第二关之后需要第 1 项的服务端接线）。

---

## 6. 证据归档

```text
docs/verification/phase2-c/first-stage-<YYYY-MM-DD>/
  server.log         # 服务器 stdout（含 first-stage/StartStage/listening）
  client-a.log       # 客户端 A 全量 stdout（提示词要求保留的证据链）
  client-b.log       # 客户端 B
  checklist.md       # §3 表格的填写结果（含未通过项的原始日志片段）
  shots/             # 截图（命名：playing-a.png / reward-b.png / debug-a.png …）
```

归档后在本目录 `README.md` 的"已执行检查"表中登记一行（日期、结论、失败项与责任方）。
