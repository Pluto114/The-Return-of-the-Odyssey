# 团队协作约定

开工前统一 [SETUP.md](docs/SETUP.md) 中的版本，并执行对应角色的 doctor 检查。

## 分支与评审

- 长期分支约定为 main 和 develop；功能分支从 develop 创建。
- A：feature/network，Reviewer C。
- B：feature/game-core，Reviewer D。
- C：feature/client，Reviewer A。
- D：feature/platform，Reviewer B。
- 每天至少合并一次可联调的增量；核心模块通过 PR/MR 评审。
- proto/ 变更由 A 审核，C 确认客户端兼容性，并同步全组。
- 远程仓库为 https://github.com/Pluto114/The-Return-of-the-Odyssey；main 保存集成基线，develop 用于日常开发。成员权限与分支保护由仓库管理员在 GitHub 设置中配置。

尚未提供完整团队成员的 GitHub 用户名，暂不创建带虚构账号的 CODEOWNERS。

## 模块边界

目录 Owner 见 README 与架构文档。D 负责 lobby 匹配；会话/重连由 A + D 协作，避免创建第二套 Session。
跨模块修改先对齐接口。Director 只接收 PerformanceMetrics、输出 StagePlan，由 StageSystem 应用。

## 格式与依赖

- UTF-8、LF，遵循 .editorconfig；Go 使用 gofmt。
- 提交 go.mod、go.sum、go.work、go.work.sum、package-lock.json、vcpkg.json；不提交 .env、node_modules、.tools、构建产物与生成协议代码。
- 统一从 proto/ 生成 Go/C++，Bot 复用 server/generated/protocol；不要手写两份协议结构。
- 新增依赖应说明用途并同步版本；不引入架构文档排除的大型框架。
- Vendor 的第三方源码登记（由 C 提交、A 审核）：
  - **rlImGui**（Raylib + Dear ImGui 的集成后端）：`client/third_party/rlImGui/`，来源 `raylib-extras/rlImGui`，分支 `RL60ImGui19207`，提交 `3bc5731c4216bb8caa67fbea24aa85ce80d57ccb`，zlib 许可。
    只 Vendor `rlImGui.{h,cpp}`、`imgui_impl_raylib.h`、`rlImGuiColors.h` 与 `LICENSE`（文件与 blob 哈希见该目录 `VENDORED.md`）；**不**带入其 `extras/`（Font Awesome 图标与 1.4MB 字体数据，编译时以 `NO_FONT_AWESOME` 排除）、`examples/`、`resources/` 与 premake 二进制。
    Dear ImGui 本体一律使用 vcpkg 的 `imgui`（当前 1.92.8#1）静态库，禁止再 Vendor 一份 ImGui 源以免符号冲突。
    该目录源码保持与上游逐字节一致，因此根 `.gitattributes` 对其标记 `client/third_party/** -text`，并由 `client/third_party/.editorconfig` 停止继承本仓库缩进/换行规则。
- 骨架阶段尚未引用的 Go 依赖由 go.mod 预置；此时执行 go mod tidy 会删掉未使用依赖，开始实现实际 import 后再整理。
- 协议源更改后执行生成脚本、Go 检查与 C++ 协议编译；生成代码不手改。

## 检查

```powershell
pwsh -File scripts/generate-proto/generate.ps1
pwsh -File scripts/test/check.ps1
pwsh -File scripts/build/build.ps1 -Target client
pwsh -File scripts/build/build.ps1 -Target dashboard
```

竞态检查在 Linux 执行 `pwsh -File scripts/test/check.ps1 -Race`，需要 GCC 和 CGO_ENABLED=1。
Bot 目录没有 Go 源码时明确 SKIP；不要添加空测试伪造业务覆盖率。
压测原始数据、机器信息、参数及优化前后对比统一放入 docs/benchmark/。
