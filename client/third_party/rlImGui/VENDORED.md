# Vendored: rlImGui (Raylib + Dear ImGui integration)

Source: https://github.com/raylib-extras/rlImGui
Branch: `RL60ImGui19207` (raylib 6.0 + Dear ImGui 1.92.07)
Commit: `3bc5731c4216bb8caa67fbea24aa85ce80d57ccb`
Vendored on: 2026-09-15 by role C (client)
License: **zlib** — Copyright (c) 2020-2021 Jeffery Myers (see `LICENSE` in this directory)

## Why

The client draws its world and HUD into a 960x540 RenderTexture, and the reward /
status panels are specified to live in the top ImGui layer. rlImGui is the raylib
backend for Dear ImGui: it feeds raylib input into ImGui and submits ImGui draw data
through raylib's own batch (`rlgl`), so no second graphics backend is introduced.

The branch is pinned to the raylib 6.0 line because this repository builds raylib 6.0
from the vcpkg baseline (`scripts/toolchain.json`).

## Files

These are the ONLY files taken from upstream. Each is byte-identical to the pinned
commit; the git blob hashes below let anyone re-verify that with
`git hash-object <file>` (or `git cat-file blob <sha>` to read upstream's copy):

| File | Size | git blob SHA-1 |
| --- | --- | --- |
| `rlImGui.h` | 8230 | `6c87d228938d4333829c2e884e520d9e22dc9add` |
| `rlImGui.cpp` | 29207 | `018651ef7532be7893e1aa972d4a866cf7304c20` |
| `imgui_impl_raylib.h` | 2480 | `e98938f29752626a921da7a1622e6a8813b93b79` |
| `rlImGuiColors.h` | 1680 | `017bcd343af093a8bc77483f6c19620275720e48` |
| `LICENSE` | 863 | `84be421e9af16ac2bf89a3627823b88b3ed6174c` |

`imgui_impl_raylib.h` is required: `rlImGui.cpp` includes it for the low-level
`ImGui_ImplRaylib_*` declarations. `rlImGuiColors.h` is a two-function raylib-Color /
ImVec4 converter used by the theme layer.

## Deliberately NOT vendored

| Upstream path | Why not |
| --- | --- |
| `imgui/` (submodule) and any `imgui*.cpp` | Dear ImGui comes from vcpkg (`imgui 1.92.8#1`); a second copy would duplicate symbols. ImGui is linked from the vcpkg static library. |
| `extras/IconsFontAwesome6.h`, `extras/FA6FreeSolidFontData.h`, `extras/FontAwsome_LICENSE.txt` | 73 KB of icon codepoints plus 1.4 MB of compressed font data for glyphs this UI never uses: the HUD is ASCII-only by contract and the reward panel needs no icons. `rlImGui.cpp`/`rlImGui.h` are compiled with **`NO_FONT_AWESOME`**, which excludes both the include and the font registration. |
| `examples/`, `resources/` | Demo code and sample assets, not part of the library. |
| `premake5`, `premake5.exe`, `premake5.osx`, `*.lua`, `*.bat` | Prebuilt build-system binaries (~7 MB) for a build system this project does not use (CMake + Ninja). |

## Build wiring

`client/CMakeLists.txt` compiles `rlImGui.cpp` into the `odyssey_client` target only
(never into the test targets, which must stay window-free), adds this directory to its
include path, and defines `NO_FONT_AWESOME`. Dear ImGui itself is already linked from
vcpkg through `odyssey_client_dependencies` (`imgui::imgui`).

## Verification performed when vendoring

| Check | Result |
| --- | --- |
| Every file byte-identical to upstream | ✅ sizes and git blob SHA-1s match (see table) |
| Compiles against this project's raylib 6.0 and vcpkg ImGui 1.92.8 | ✅ `cl /c /std:c++20 /MDd /DNO_FONT_AWESOME` exits 0, no errors (an unused-variable-free, warning-clean compile of one translation unit) |
| No ImGui sources added | ✅ only the four files above exist in this directory |

The upstream branch targets ImGui **1.92.07** while vcpkg provides **1.92.8**. Both are
1.92.x and the translation unit compiles cleanly; the dynamic-texture API rlImGui uses
(`ImTextureData`, `ImGuiBackendFlags_RendererHasTextures`) is present in 1.92.8. If a
later ImGui upgrade breaks it, move to the rlImGui branch matching that version rather
than patching this copy.

## Updating

1. Pick the branch for the raylib/ImGui line in use (naming: `RL<raylib><ImGui>`).
2. Replace the files and update this table with the new commit, sizes and hashes.
3. Re-run the compile check above before wiring anything to it.
