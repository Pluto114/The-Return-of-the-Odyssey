# Test machine and environment

Filled 2026-09-16 for the run in this directory. Fields marked **TODO** could not be read
from the machine automatically (WMI/CIM is unavailable to the agent's sandbox) and are the
operator's to complete — the acceptance criterion is only reproducible with them.

| Item | Value |
| --- | --- |
| CPU | 13th Gen Intel(R) Core(TM) i5-13500HX, 20 logical processors, ~2688 MHz nominal |
| Memory | **TODO** (capacity, channels) |
| GPU / driver | **TODO** (model, driver version) |
| Storage | **TODO** (SSD/HDD; only affects the startup phase) |
| OS build | Registry reports `Windows 10 Home China`, DisplayVersion `25H2`, build `26200`; `[System.Environment]::OSVersion` = 10.0.26200.0. (Build 26200 is a Windows 11 25H2 build number, so the registry product name is the stale one.) |
| Display resolution / scaling | Single display. DPI-virtualised reading 1707×960 (≈ 2560×1440 at 150%); `LogPixels` not set explicitly. The client window was 1920×1080, which fits. **TODO**: confirm the physical resolution and the system scaling |
| Client window / viewport scale | 1920×1080, `scale=2`, offset (0,0) — from `main: viewport 1920x1080 scale=2 offset=(0,0)` in every run log |
| Power mode | **TODO** (best performance / balanced, plugged in?) |
| Background load | The measurement ran with the gameserver, one loadbot and the measured client on this machine; **TODO**: note any browser, antivirus scan or build that was also running |
| Build commit | Branch `feature/week2-client-hardening`, client built 2026-09-16 23:40:33 (UI code as of `57d8b3b`; `281118f` afterwards adds only a log line) |
| Build type / compiler | `CMAKE_BUILD_TYPE=Release`, MSVC 14.44 (VS 2022 Build Tools), Ninja, vcpkg x64-windows from the shared manifest and the repository binary cache |

Notes:

- The client, the gameserver and the loadbot all ran on this machine; the measured client was
  the only GUI process (the counterpart is a headless bot), which is what the plan asks for so
  that no second renderer competes for CPU.
- `client/assets/fonts/pixel_hud.ttf` is not in the repository, so the HUD used the raylib
  default font in both runs (see report.md, Notes and caveats).
