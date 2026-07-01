# Beads Sync Issues & Canonical Task List

## Problem with bd server-mode
The Dolt server view cache is stale — `bd update -s closed`, `bd delete`, etc. return success but `bd list` still shows old state. The `bd compact` command auto-imports from `.beads/issues.jsonl` which resurrects deleted tasks. Ignoring `bd list` output; trusting `bd ready` and Dolt history.

## Canonical Task List (ground truth from Dolt)

### In Progress (3)

**spruce-0wb** — Fix space/seek/key routing (pine-style)
- Created `internal/app/keymap.go` with `KeyMap` struct + `DefaultKeyMap(cfg)`
- Added `handleSeek` to `internal/app/playback.go`
- Restructured `KeyMsg` handler: app-level quit/seek/next/prev/shuffle via `key.Matches`, then `player.Update` for space/speed/volume, then screen.Update
- Added `mpris.SetRateMsg` handler for MPRIS speed control
- Build passes; 4 tests fail (tests written for old string-based routing)

**spruce-d30** — Fix 4 broken tests after key routing refactor [blocks: spruce-0wb]
- `G4_back_stack_library_queue_back` — esc routing changed, needs test update
- `G5_quit_during_playback_reports_stopped` — quit path changed
- `TestAppModel_CommandPaletteAndGlobalKeys` — `s`/`n` now use `key.Matches`, test uses string keys
- Probably 1 more in the suite

**spruce-c88** (was -sij, renumbered) — Sync queue + MPRIS after volume/speed keyboard changes [blocks: spruce-zq2]
- `internal/player/model.go:176-192` (volume) and `:159-174` (speed) call `SetVolumeCmd`/`SetSpeedCmd` but never `syncQueueScreen`
- Also: current changes via keyboard don't emit MPRIS property-change signals
- Fix: after volume/speed cmd in app-level routing, call `m.syncQueueScreen()`

### Pending (4)

**spruce-bq3** — Port sleep timer from pine [blocks: spruce-0wb]
- Pine: `S` cycles 0/15/30/45/60 min, `SleepTimerExpiredMsg`, footer countdown
- Spruce: `player.Model.SleepRemaining` field exists (copied) but never set
- Port: `cycleSleepTimer` + `SleepTimerExpiredMsg` handler + `S` key in app switch + footer already renders

**spruce-zq2** — Port MPRIS property-change signal emission [blocks: spruce-0wb]
- Pine emits `mprisPlaybackCmd`/`mprisPositionCmd`/`mprisVolumeCmd` after state changes
- Spruce's `syncQueueScreen` mutates `m.mprisState` but never fires D-Bus signals
- External clients (playerctl, DE widgets) show stale state
- Port pine's `mpris*Cmd` helpers + call from play/pause/seek/volume/speed/stop transitions

**spruce-7rr** — Port `?` help overlay from pine (optional)
- Nice-to-have, not breaking

**spruce-qi6** — Verify queue screen: remove/jump works
- Check `internal/screens/queue/model.go` handlers: `d`/`x`/`delete` for remove, `enter` for jump, `c` for clear
- May need status bar showing N queued (like pine shows in footer)

### Recently Closed (8 done)

**Hotfixes:**
- spruce-euc — Remove `Authorization` header from mpv (fixed HTTP 400 Bad Request)
- spruce-8fm — Audit: AUDIT_PLAYBACK.md (10 findings vs pine)

**Library & Navigation:**
- spruce-e9c — Albums→Tracks navigation (GetAllAlbums replaces Artists page)
- spruce-81k — esc/back navigation fix + list keymap deconfliction

**Queue:**
- spruce-fem — `a`/`A` queue keys (add track / add album)

**E2E Tests:**
- spruce-uu1 (was -2tt) — 7 E2E tests with mock Jellyfin (G1-G7)

**Full Stage A-F (all implemented and working):**
- Stages A (5 subtasks), B (3 subtasks), C, D, E, F — audio playback, pagination, render stack, back-stack, player keys, session reports

## Suggested Execution Order

1. **spruce-d30** (fix tests) — finish the space/seek/key routing work and verify suite is green
2. **spruce-c88** (sync volume/speed) — small, high-impact for MPRIS users
3. **spruce-zq2** (MPRIS signals) — unblocks c88, needed for external clients
4. **spruce-bq3** (sleep timer) — nice quality-of-life feature
5. **spruce-qi6** (queue screen polish) — verify and add status bar
6. **spruce-7rr** (help overlay) — last, nice-to-have

## Workaround Notes

When closing/deleting tasks in bd:
1. `bd update -s closed`
2. `bd dolt commit -m "..."` 

The `bd list` view will still be stale in server-mode. Ground truth is in `bd ready` and direct `bd show <id>` queries.
