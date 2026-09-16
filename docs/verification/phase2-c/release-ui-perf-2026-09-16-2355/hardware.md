# Test machine and environment

Filled 2026-09-16 for the run in this directory. WMI/CIM is blocked in the agent's sandbox
(`Access denied`), so every value below comes from a non-WMI source: the registry, the .NET
`Microsoft.VisualBasic.Devices.ComputerInfo` and `System.Windows.Forms` APIs, and the process
list. The one inferred value is marked as such.

| Item | Value |
| --- | --- |
| CPU | 13th Gen Intel(R) Core(TM) i5-13500HX, 20 logical processors, ~2688 MHz nominal (`HKLM:\HARDWARE\DESCRIPTION\System\CentralProcessor\0`) |
| Memory | **15.7 GB** total, 9.9 GB available at the time of writing (`Microsoft.VisualBasic.Devices.ComputerInfo`; the registry resource map and the Storage module were both unreadable) |
| GPU / driver | Two adapters present: **Intel(R) UHD Graphics** (`32.0.101.6556`, 2025-01-23) and **NVIDIA GeForce RTX 4060 Laptop GPU** (`32.0.16.1664`). Which one actually rendered the measured window was not recorded — driver-level per-application assignment decides it |
| Storage | **Samsung NVMe SSD** — `SCSI\Disk&Ven_NVMe&Prod_SAMSUNG_MZVL2512` (`HKLM:\SYSTEM\CurrentControlSet\Services\disk\Enum`); only affects the startup phase, and the capture excludes the warm-up frames anyway |
| OS build | Registry reports `Windows 10 Home China`, DisplayVersion `25H2`, build **26200**; `[System.Environment]::OSVersion` = 10.0.26200.0. (Build 26200 is a Windows 11 25H2 build number, so the registry product name is the stale one.) |
| Display resolution / scaling | **Inferred 2560×1440 at 150 %**, single display. Evidence: a DPI-unaware process reports 1707×960 (= 2560/1.5 and 1440/1.5), while the client selected `scale=2` (1920×1080), which is only possible if the renderer saw at least 1920×1080 physical pixels. `PerMonitorSettings\DpiValue = 0` (recommended scaling). **Not independently confirmed** |
| Client window / viewport scale | 1920×1080, `scale=2`, offset (0,0) — from `main: viewport 1920x1080 scale=2 offset=(0,0)` in every run log |
| Power mode | Power scheme **Balanced** (`HKLM:\...\Power\User\PowerSchemes\ActivePowerScheme = 381b4222-…`), machine was on **AC power** (`PowerLineStatus = Online`). Battery readings are odd (`BatteryLifePercent = 1` with `BatteryChargeStatus = High`), which does not affect a plugged-in run |
| Background load | Same machine ran the **gameserver**, one **loadbot** and the measured client. Also running throughout: the agent's own desktop app (DSH Desktop, WebView2 children started 23:09) — six `msedgewebview2` processes — and `MSPCManagerService`/`LightStudio-background`. **No browser, IDE, Docker, node/java build or antivirus scan was running**, and no compilation was in progress: the acceptance run started after all build work had stopped |
| Build commit | Branch `feature/week2-client-hardening`; the measured client was built 2026-09-16 23:40:33, i.e. the UI code as of `57d8b3b` (the later `281118f` adds only a matchmaking log line and touches no UI path) |
| Build type / compiler | `CMAKE_BUILD_TYPE=Release`, MSVC 14.44 (VS 2022 Build Tools), Ninja, vcpkg triplet `x64-windows` from the shared manifest and the repository binary cache (`.tools/vcpkg-cache`) |

Notes:

- The measured client was the only GUI process of the game; the counterpart is a headless bot,
  so no second renderer competed for CPU — which is what the plan asks for.
- The agent's desktop app is part of the background load and was open for the whole run; it
  was equally present for both the UI-on and the UI-off runs, so it cannot bias the delta.
- `client/assets/fonts/pixel_hud.ttf` is not in the repository, so the HUD used the raylib
  default font in both runs (see report.md, Notes and caveats).
