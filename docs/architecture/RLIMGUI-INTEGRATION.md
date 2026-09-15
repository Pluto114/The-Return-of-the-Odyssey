# rlImGui 依赖引入方案（待 Reviewer A 审批）

状态：**方案 + 已落地补丁（本分支）**，请 A 审核后决定是否进入 P2。
提交人：成员 C。审核人：成员 A（按 CONTRIBUTING 的分支约定，C 的 Reviewer 是 A）。
范围：只引入 **rlImGui 集成层**；不含 ImGui 本体（走 vcpkg），不含 P2 的面板实现。

---

## 1. 为什么需要它

提示词的渲染闭环要求顶层 UI 走 ImGui：

```
BeginDrawing → BeginTextureMode(rt) → 世界 + Raylib HUD → EndTextureMode
  → 黑边清屏 + 翻转 blit → rlDrawRenderBatchActive()
  → rlImGuiBegin() → ImGui 面板 → rlImGuiEnd()
EndDrawing → SwapScreenBuffer()
```

rlImGui 正是把 raylib 的输入喂给 ImGui、并用 raylib 自己的批处理（`rlgl`）提交 ImGui 绘制数据的后端，因此**不引入第二套图形后端**。P2 的奖励卡与状态面板、P3 的 F1/F2/F3 面板都要用它。

## 2. 版本选择（已核实，非猜测）

| 项 | 值 | 说明 |
| --- | --- | --- |
| 仓库 | `raylib-extras/rlImGui` | rlImGui 的官方仓库（默认分支 `main`） |
| 分支 | **`RL60ImGui19207`** | 仓库按 `RL<raylib><ImGui>` 命名分支；这条正是 **raylib 6.0 + ImGui 1.92.07**，与本项目的 vcpkg raylib 6.0 对应 |
| 提交 | **`3bc5731c4216bb8caa67fbea24aa85ce80d57ccb`** | 固定到提交，不跟分支浮动 |
| 许可 | **zlib**（Copyright (c) 2020-2021 Jeffery Myers） | 允许商用与再分发；要求保留许可与"改动需显著标注" |

另有 `RL55ImGui19207`（raylib 5.5）与 `main`（最新），均不使用。

## 3. Vendor 清单（逐字节一致，可复核）

只取 5 个文件到 `client/third_party/rlImGui/`。每个文件都与该提交的 git blob **逐字节一致**：

| 文件 | 大小 | git blob SHA-1 |
| --- | --- | --- |
| `rlImGui.h` | 8230 | `6c87d228938d4333829c2e884e520d9e22dc9add` |
| `rlImGui.cpp` | 29207 | `018651ef7532be7893e1aa972d4a866cf7304c20` |
| `imgui_impl_raylib.h` | 2480 | `e98938f29752626a921da7a1622e6a8813b93b79` |
| `rlImGuiColors.h` | 1680 | `017bcd343af093a8bc77483f6c19620275720e48` |
| `LICENSE` | 863 | `84be421e9af16ac2bf89a3627823b88b3ed6174c` |

复核方式：`git hash-object client/third_party/rlImGui/<file>` 应等于上表；`git cat-file blob <sha>` 可读出上游内容对照。记录文件为同目录 `VENDORED.md`（含来源、分支、提交、许可、排除清单）。

> `imgui_impl_raylib.h` **不是可选项**：`rlImGui.cpp` 里 `#include "imgui_impl_raylib.h"` 才拿到 `ImGui_ImplRaylib_*` 的低层声明。
> `rlImGuiColors.h` 是两个 raylib `Color` ↔ `ImVec4` 的转换函数（约 1.7KB），P2 主题会用到。

## 4. 明确不引入的内容（防符号冲突与无谓体积）

| 上游路径 | 不引入的理由 |
| --- | --- |
| `imgui/`（子模块）及任何 `imgui*.cpp` | Dear ImGui 由 vcpkg 提供（`imgui 1.92.8#1`），再带一份会产生重复符号；项目侧直接链接 vcpkg 预编译静态库（`odyssey_client_dependencies` 已含 `imgui::imgui`） |
| `extras/IconsFontAwesome6.h`、`extras/FA6FreeSolidFontData.h`、其许可 | 73KB 图标码点 + 1.4MB 压缩字体数据；本 UI 按契约只用 ASCII，P2 卡片也不需要图标。编译时定义 **`NO_FONT_AWESOME`** 即可同时排除头与字体注册 |
| `examples/`、`resources/` | 示例代码与样例素材，非库本体 |
| `premake5`、`premake5.exe`、`premake5.osx`、`*.lua`、`*.bat` | 预编译构建工具与另一个构建系统（约 7MB）；本项目用 CMake + Ninja |

## 5. 补丁内容（已在本分支落地，均为可回退的小改动）

| 文件 | 改动 |
| --- | --- |
| `client/third_party/rlImGui/*` | 新增 5 个 vendor 文件（上表） |
| `client/third_party/rlImGui/VENDORED.md` | 新增：来源/分支/提交/日期/许可、文件与 blob 哈希、排除清单、验证记录、升级步骤 |
| `client/third_party/.editorconfig` | 新增：`[*] indent_style = unset`、`end_of_line = unset` 等，停止继承本仓库格式化规则 |
| `.gitattributes` | 新增一行 `client/third_party/** -text`：vendor 源码保持逐字节一致（否则 `* text=auto eol=lf` 会改写换行，blob 哈希随之变化） |
| `client/CMakeLists.txt` | 若 `third_party/rlImGui/rlImGui.cpp` 存在则编入 `odyssey_client`、加包含目录、定义 `NO_FONT_AWESOME`；**绝不编入任何测试目标**；不存在时打印一行 STATUS 并保持可构建 |
| `CONTRIBUTING.md` | 「格式与依赖」下新增 rlImGui 登记条目（版本、提交、许可、只 vendor 哪几个文件、禁止再 vendor ImGui 本体） |

## 6. 验证证据（提交前已执行）

| 检查 | 结果 |
| --- | --- |
| 五个文件与上游逐字节一致 | ✅ 大小 + git blob SHA-1 全部匹配（见 §3） |
| 对本项目 raylib 6.0 + vcpkg ImGui 1.92.8 可编译 | ✅ `cl /c /std:c++20 /MDd /DNO_FONT_AWESOME` 退出码 0、无错误、无警告 |
| 未混入 ImGui 本体 | ✅ 该目录仅上述 4 个源码/头文件 + LICENSE |
| 不污染无头测试 | ✅ CMake 只把源码加进 `odyssey_client`；测试目标保持无窗口/无 ImGui |

**已知差异**：上游分支标注 ImGui **1.92.07**，vcpkg 提供 **1.92.8**。两者同属 1.92.x，rlImGui 使用的动态纹理 API（`ImTextureData`、`ImGuiBackendFlags_RendererHasTextures`）在 1.92.8 存在，实测编译通过。若将来升级 ImGui 大版本导致不兼容，应改换 rlImGui 对应分支，而不是就地改这份拷贝（zlib 要求改动须显著标注）。

## 7. 审批后 P2 会做什么（本次不含）

1. `main.cpp` 渲染闭环接入 `rlImGuiSetup(false)` → 自定义霓虹主题（`ui/Theme.h` 调色板 + `rlImGuiColors::Convert`）→ `rlImGuiBegin/End`；
2. 设 `io.IniFilename = nullptr` 阻止生成 `imgui.ini`；在 `rlImGuiSetup` 完成后用 `assets/fonts/pixel_hud.ttf` 重建图集（`AddFontFromFileTTF` + `PixelSnapH`/`OversampleH=1`），失败回退默认字体；
3. 奖励三选一卡片 + 单次提交锁定，并与 Raylib 旧托盘互斥；
4. 鼠标/键盘双门控（`io.WantCaptureMouse` 冻结瞄准、`WantCaptureKeyboard` 屏蔽 WASD/SPACE/1–3）、ESC 优先级（有窗口先关窗）；
5. 状态面板（登录/匹配/过渡）迁到 ImGui 顶层。

## 8. 请 A 确认的点

1. 是否接受 rlImGui 以 **vendor 5 个文件 + zlib 许可 + VENDORED.md** 的形式引入？
2. 是否同意 `NO_FONT_AWESOME`（不要 Font Awesome 图标与 1.4MB 字体数据）？
3. `.gitattributes` 的 `client/third_party/** -text` 与 `client/third_party/.editorconfig` 是否照此提交？
4. 是否同意 `CONTRIBUTING.md` 中"ImGui 本体一律 vcpkg、禁止再 vendor"的约束？
