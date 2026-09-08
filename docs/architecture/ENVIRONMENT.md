# 初始化环境决策

本次工作依据用户“搭建骨架、环境配置，暂不写业务代码”的请求。
原始架构文档作为设计参考完整复制到仓库根目录，未改写其内容。

| 项目 | 本次选择 | 原因 / 后续处理 |
| --- | --- | --- |
| 仓库形态 | 单仓库，server 与 bot 为两个 Go module，根目录 go.work | Bot 独立程序，仍复用同一份网络 DTO |
| Go module 名 | github.com/Pluto114/The-Return-of-the-Odyssey/server、github.com/Pluto114/The-Return-of-the-Odyssey/bot | 已按用户提供的 GitHub 仓库地址统一 module、replace 与 go_package；Bot 在单仓库内通过 replace 复用服务端协议 |
| Go | 1.26.8 | 固定稳定工具链；net、slog、database/sql、pprof 使用标准库 |
| Go 第三方依赖 | mysql driver、go-redis、Prometheus、protobuf | 仅覆盖文档技术栈；暂不额外加入 sqlx / Zap |
| Windows C++ | MSVC x64 + Ninja + CMake 3.31+ + C++20 | 避免已有 MinGW 8.1 与 MSVC vcpkg 二进制混用 |
| C++ 依赖 | vcpkg 2026.07.29 固定 commit | 固定所有传递依赖；项目内安装不改已有 D:/vcpkg |
| Protobuf | protoc 33.4 / C++ runtime 6.33.4 / Go plugin 1.36.12 | 编译器发行号与 C++ runtime 的版本格式不同；33.4 对应 6.33.4；Go 有独立版本线 |
| Proto 定义 | 六个仅含 syntax/package/go_package 的合法文件 | 不抢先决定 Message ID、消息字段和领域类型 |
| CMake | 默认不启用语言；client preset 才解析依赖、编译生成协议 | 尚无业务入口，也能验证骨架与工具链 |
| Dashboard | JavaScript、Vue 3、Vite、ECharts | 文档未要求 TypeScript，暂不增加另一套语言工具；只有静态环境占位页 |
| 本地基础设施 | 四个 Compose 服务 | 没有可执行程序前不创建 gameserver/Bot 镜像 |
| 凭据 | .env.example 使用本地示例值 | bootstrap 只创建缺失的 .env，不覆盖个人配置；实际凭据不入库 |
| 数据库迁移 | 空目录 | 表结构属于后续业务设计 |
| 指标与面板 | Prometheus 抓取预留、Grafana 数据源及面板目录 | 无虚构指标、数据或已完成图表 |
| Git | origin 指向 Pluto114/The-Return-of-the-Odyssey | 保留远程原始 Initial commit；长期分支采用 main/develop，团队权限与保护规则由管理员配置 |

## 待 Owner 决定

- A/C：Message ID、字段编号、最大 Frame Body 大小、心跳/超时、队列容量及兼容规则。
- B/D：房间人数、Tick 超限处理、重连时限、持久化 schema 与指标名称/标签。
- C：raylib 与 ImGui 的渲染桥接；当前只安装库，不自行引入额外桥接框架。
- C/D：管理后台的 HTTP/WebSocket 路由和身份验证；/api、/ws 目前仅为开发代理约定。
- 全组：其余成员身份、分支保护规则、资源许可与后续集成评审。
