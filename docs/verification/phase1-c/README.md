# 角色 C 第一阶段：客户端本地验证记录

日期：2026-09-08。范围：C 的客户端 D1 协议无关部分（Frame / 组帧 / 网络线程 / 输入 / 快照视图 / 窗口），
**不是全组阶段验收，也不包含与真实 Go Server 的跨语言验收**。
工作分支：`feature/client`（fork: xxhsir/The-Return-of-the-Odyssey），基于 develop 的
`aa65d00aa3ef18847d5568d74fd1d713ebc3386a`；已向主仓库 develop 提交 PR #1。

## 实现状态

| 计划 | C 的当前结果 |
| --- | --- |
| D1：raylib 窗口、Asio 连接与 Frame 收发、Network→Main 队列、连接状态可见 | 协议无关部分已实现并本地验证；真实 Ping/Pong/Login 字节联调依赖 A 的协议 |
| D2：WASD→30Hz Input、快照应用、自/远端显示、调试文字 | 输入意图/序号与快照消费框架已就位；wire DTO 解码与真实服务器联调待 A 协议与服务器 |
| D3：双客户端同房互见、断线实体移除、演示 | 未执行（依赖真实服务器与双实例），联调期执行 |

客户端侧实现口径与给 A/B/D 的接入要求见 [客户端接入契约](../../architecture/CLIENT-PHASE1.md)。

## 已执行检查

| 检查 | 环境 / 结果 |
| --- | --- |
| `pwsh -File scripts/doctor.ps1 -Role client` | Windows；git / Go 1.26.8 / protoc 33.4 / cmake 3.31.6 / ninja / vcpkg 检查通过 |
| `pwsh -File scripts/generate-proto/generate.ps1` | 生成 Go + C++ 协议（当前为空 schema），无业务消息 |
| `pwsh -File scripts/build/build.ps1 -Target client` | Windows x64 / VS2022 BuildTools MSVC 14.44.35207 / CMake 3.31.6 / Ninja / vcpkg baseline 9e593bb（2026.07.29）；`odyssey_client.exe` 及全部测试目标编译链接成功 |
| `ctest --test-dir build\client-windows -C Debug --output-on-failure` | **3/3 passed**：odyssey_core_tests、odyssey_net_tests、odyssey_logic_tests |
| 单测合计 | **600 checks，0 failures**（core 538 + net 25 + logic 37） |

## 测试覆盖（对照计划测试项）

- Frame 编解码固定字节样例（大端 16B、Magic `0x4E52`、Version 1）与字段往返 —— T01 方向。
- 单帧/合并流/逐字节输入/13 字节半头边界/**100 帧一次性合并** —— 每条消息恰好解码一次、顺序一致 —— T02 方向。
- 坏 Magic / 坏 Version / body 超限（分配前拒绝）/ EOF 半包截断 —— T03 方向（组帧层，服务器侧策略归 A/D）。
- 有界队列：容量上限、丢最旧、TryPush 拒绝、Close 唤醒、5000 条并发生产消费。
- 网络线程：连接成功事件、Ping→Pong 回环（序号+载荷）、对端关闭 → 断连事件、连接被拒 → failed、
  二次 Start 拒绝、**出站队列饱和丢最旧并产生 kOutboundDropped** —— T10 方向（客户端发送队列）。
- 输入：WASD 意图映射、对角限长（斜向不加速，T05 方向）、零向量释放、InputSeq 每 30Hz Tick 递增。
- 快照消费：全量替换、缺失实体移除、closed 清空、self ack、无序输入防御性排序 —— B 语义。

## 环境补充与未完成项

本机首次 vcpkg 依赖安装需要在普通用户终端完成（受限执行环境下 vcpkg 解压子进程不可用）；
依赖安装完成后增量构建与 ctest 均正常。已安装依赖：raylib 6.0、asio 1.32.0、protobuf 6.33.4#2、imgui 1.92.8#1
（含传递依赖），基线 `9e593bb18ea69cc5095e012465dcd675a822ed0d`。

## 本机环境待办：raylib 动态库呈现问题（不影响逻辑/网络联调）

现象：raylib 窗口在「显式 `PollInputEvents()` + 手动限帧」后消息泵正常（Responding=true、60fps、ESC/X 可退出），
但**纯色图元（矩形/圆）不上屏，背景与文字正常**；GPU 帧缓冲读回（TakeScreenshot）确认绘制内容正确，
CopyFromScreen/PrintWindow 抓屏显示图元缺失。已尝试 `FLAG_VSYNC_HINT|FLAG_WINDOW_HIGHDPI` 无改善。
定位为 raylib（动态库 raylib.dll+glfw3.dll）在该机 Intel UHD 770 驱动下的呈现/合成问题。

处理：不作为代码缺陷；画面类验收（双人几何绘制等）暂以「本机待驱动更新或 raylib 静态链接后复验」记录。
逻辑、协议编解码、网络联调均通过无头测试/日志验证，不依赖本问题。


| 尚未完成 | 后续责任 |
| --- | --- |
| 最小 proto 消息集 + Message ID 表 + Frame 十六进制固定样例 + 错误处理表 | A（C 复核） |
| PlayerInput / WorldSnapshot wire 字段与客户端 DTO 适配（30Hz 真发输入、快照解码→GameView→双人绘制、Room/Entity/Tick/InputSeq 调试显示） | A 定稿后 C 实施，B 确认语义 |
| 开发用登录与真实 Pong/登录结果显示 | A（客户端已留占位） |
| 真实 Go Server 实例、D2 单客户端移动、D3 双客户端同房互见与断线清理 | A+B+D 提供服务器；C 主测 T08，配合 T11/T12 |
| D 评审、PR / develop 集成、GitHub CI | 团队后续协作（PR #1 已开） |

不能把本记录当作跨语言互通、真实移动链路或阶段验收通过。
