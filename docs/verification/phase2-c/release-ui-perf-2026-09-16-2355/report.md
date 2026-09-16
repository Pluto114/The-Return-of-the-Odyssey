# Release UI performance acceptance run

Date: 2026-09-16 23:56  
Endpoint: 127.0.0.1:7777  
Build: `C:\Users\xunxue\Desktop\The-Return-of-the-Odyssey-main\build\client-windows-release\client\odyssey_client.exe`  
Frames per run: 600 (plan floor 600)  
Budget: mean(UI on) - mean(UI off) <= 1.5 ms  
Script: scripts/verify/client-release-ui-perf.ps1

## Verdict

**PASS - UI per-frame delta 0.1224 ms <= 1.5 ms**

## Per-round results

| Round | UI | frames | mean (ms) | median | p95 | max (ms) |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | on | 600 | 0.2355 | 0.1979 | 0.3697 | 9.3867 |
| 1 | off | 600 | 0.1296 | 0.0871 | 0.2112 | 9.2977 |
| 1 | delta | | **0.1059** | | | 0.0890 |
| 2 | on | 600 | 0.2395 | 0.1634 | 0.3534 | 9.3779 |
| 2 | off | 600 | 0.0992 | 0.0573 | 0.1154 | 8.4112 |
| 2 | delta | | **0.1403** | | | 0.9667 |
| 3 | on | 600 | 0.2444 | 0.1747 | 0.3388 | 9.6453 |
| 3 | off | 600 | 0.1220 | 0.0560 | 0.2353 | 8.4309 |
| 3 | delta | | **0.1224** | | | 1.2144 |

Median delta over valid rounds: **0.1224 ms** (budget 1.5 ms)  
Round-to-round spread: 0.0344 ms  

## Scene and environment

- Stage index / result: **stage 1, cleared** in every run (`main: stage started stage=1 tick=3` → `main: stage cleared stage=1 tick=183` in `ui-on-1.log`); both runs of a round are a fresh stage-1 match, so the comparison is same-stage-same-start.
- Scene validity: every run logged `main: match ready room=1 teammates=1` and `main: perf capture started (stage=playing alive=yes)`; the counterpart bot reported `succeeded=1 failed=0 stages_cleared=1` for all six runs.
- Second player (bot command line): `go run ./bot/cmd/loadbot -mode functional -clients 1 -stages 1 -duration 5m -ramp 0s -use-potion=false -resume=false -server 127.0.0.1:7777`, one process per run, started by `scripts/verify/client-release-ui-perf.ps1` once the server's `odyssey_match_queue_players` gauge showed the client queued.
- Window size and viewport scale: `main: viewport 1920x1080 scale=2 offset=(0,0)` (integer scale, no letterbox) for all runs.
- Hardware: see [hardware.md](hardware.md).

## Notes and caveats

- **Measured binary provenance**: the client was built at 2026-09-16 23:40:33 from the branch as of `57d8b3b` (all current UI code: P0–P3, D7 stage summary, potion input, queue-depth chart, UI command record). Commit `281118f` afterwards only adds a log line and touches no UI path, so these numbers describe the current UI code; a rebuild for exactness is optional.
- **HUD font**: `client/assets/fonts/pixel_hud.ttf` is absent from the repository, so both runs used the raylib default font (the client logged the fallback warning). This is identical in the UI-on and UI-off runs and therefore does not affect the delta, but the pixel bitmap font deliverable is still waiting on that asset.
- **Outliers**: the maximum per-frame times (8.4–9.6 ms) are two orders of magnitude above the means (0.10–0.24 ms) while p95 stays at 0.12–0.37 ms, i.e. isolated single-frame hitches rather than a sustained cost. The per-round max delta (0.089 / 0.967 / 1.214 ms) also stays inside the 1.5 ms budget, so no max-exception note is required; a longer run on an idle machine would be the way to characterise them further.
- **Sampling**: 600 counted frames per run after the 10 warm-up frames that `PerfCapture` discards (frame 0 carries the font atlas upload).

## Evidence

| File | Contents |
| --- | --- |
| ui-on-N.csv / ui-off-N.csv | Per-frame CPU times plus the summary line |
| ui-on-N.log / ui-off-N.log | Client stdout: capture start line, summary line, startup lines |
| bot-ui-on-N.log | Counterpart bot report (JSON) for that run, when the script started it |
| hardware.md | Test machine template (must be filled in) |
| server.log | Only when -StartServer was used |

Counterpart: one loadbot per run, started once the client reports matching  
Frames per run: 600; rounds: 3; budget: 1.5 ms

Raw data is authoritative: this report is a summary of it.
