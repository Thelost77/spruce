# Spruce Playback Failure — Root Cause Analysis & Remediation Plan

Deep cross-check of `spruce` against the `pine` reference architecture and the
official Jellyfin OpenAPI specification. This document supersedes the
high-level `AUDIT.md` with concrete, file:line-verified defects and a
build-ready remediation plan focused on the primary symptom: **audio files do
not load and play in mpv**.

> Note on the reference repo: `pine` is an **Audiobookshelf** TUI, not a
> Jellyfin client. Its value here is the **Elm-architecture pattern**
> (render stack, back-stack, command delegation, error banner), not the
> Jellyfin API surface. `spruce`'s Jellyfin `MediaBrowser` auth, stream URL
> shape, and `PositionTicks` conversion are already correct; the failures are
> in the integration layer, not the API translation.

---

## 1. Verified Root Causes (ranked by impact on the symptom)

### RC-1 — Stream URL has no `api_key`; auth breaks on redirect
`internal/jellyfin/client.go:88-90`
```go
func (c *Client) StreamURL(itemID string) string {
    return fmt.Sprintf("%s/Audio/%s/stream?static=true", c.baseURL, itemID)
}
```
Auth is delivered only via `--http-header-fields=Authorization: MediaBrowser
...` (`mpv.go:113-115`). mpv does **not** re-apply `--http-header-fields`
across HTTP 30x redirects. Jellyfin commonly redirects
`/Audio/{id}/stream?static=true` to a remux/CDN/filesystem URL; the
redirected GET carries no credentials → **401/403 → mpv exits with code 1
within milliseconds**.

Jellyfin accepts `api_key=<token>` (also `ApiKey=`) as a query-string
credential on the streaming endpoint. Putting it in the URL is the robust,
officially supported pattern for direct stream and survives redirects.

### RC-2 — Launch/auth failure is invisible and misclassified as EOF
`internal/player/commands.go:25-45` + `internal/app/playback.go:79-91`
```go
// TickCmd returns PositionMsg{Err} on ANY socket error
// handlePositionMsg treats every error as natural end-of-track:
if msg.Err != nil {
    logger.Info("track ended or player error, advancing queue", "err", msg.Err)
    nextIdx := m.nextIndex(m.currentIndex + 1)
    if nextIdx < len(m.tracks) && nextIdx != m.currentIndex {
        return m.startPlaybackAt(nextIdx)   // skips to next, which also fails
    }
    return m.stopPlayback()                 // queue vanishes, no message
}
```
A 401 at launch → first `TickCmd` (500ms later) hits a dead socket → app
silently "advances" through the entire queue → `stopPlayback()`. The user
sees the queue clear with **zero explanation**. There is no listener for
mpv's `end-file` event reason (`eof` vs `error` vs `redirect`), so genuine
EOF and fatal load failure are indistinguishable.

### RC-3 — `PlayerLaunchErrMsg` only logs, never surfaces to the user
`internal/app/model.go:284-287`
```go
case player.PlayerLaunchErrMsg:
    logger.Error("player failed to launch", "err", msg.Err)
    newM, cmd := m.stopPlayback()
    return newM, cmd
```
No `ErrorBanner` component is wired into `View()` (RC-5), and no
`ErrorBannerMsg` exists in the message catalog (`app/messages.go` is 14
lines: only `Screen` enum + a dead `SwitchScreenMsg`). Launch failure is
written to the log file and then discarded.

### RC-4 — Player footer bar is never rendered
`internal/app/model.go:389-410` — `View()` switches on `m.screen` and
returns `libraryScreen.View()` / `queueScreen.View()` / `loginScreen.View()`.
`m.playerState.View()` (`player/view.go:11-58`) is **never called**. Worse,
`screenHeight()` (`model.go:121-130`) reserves 1 row for the footer when
`playerState.Title != ""`, so the user sees a blank line where the bar
should be — a visible "something is missing" signal with no content.

### RC-5 — No header, no error banner, no hints bar
The root `View()` emits none of the chrome `pine`'s `render.go` composes:
header (`pine › <screen>`), error banner, hints/status bar, footer. There is
no `components.ErrorBanner` wired in, so even if RC-3 were fixed there is
nowhere to display the message.

### RC-6 — No pagination; library fetch silently truncates or aborts
`internal/jellyfin/client.go:190-269` — `GetArtists`, `GetAlbums`,
`GetTracks`, `GetAllTracks` set `Recursive=true` and `IncludeItemTypes` but
**never** set `StartIndex` or `Limit`. Jellyfin servers cap unbounded
`/Users/{id}/Items` responses (default often 100/1000). Combined with the
`httpClient.Timeout = 10s` (`client.go:52`), `GetAllTracks` on a large
library times out → `AllTracksLoadedMsg.Err` is swallowed
(`library/model.go:181-185` sets `m.allTracks = nil` silently) → empty track
list → "nothing to play."

### RC-7 — No back-stack; `esc`/`left` unbound at the top level
`app/messages.go` has no `GoBackMsg`/`NavigateMsg`. `library/model.go:192-200`
climbs Tracks→Albums→Artists internally, then comments "parent handles esc"
— but `app/model.go:176-233` has **no `esc`/`left`/`back` case**. Users are
trapped at the Artists level. `q` quits the whole app without calling
`stopPlayback()`, so Jellyfin never receives `ReportPlaybackStopped` and the
server-side session hangs open.

### RC-8 — Player keys (h/l/+/-/[/]) never reach the player model
`app/model.go` `Update` returns from the `tea.KeyMsg` branch before ever
calling `m.playerState.Update(msg)`. The `PlayerKeyMap` in
`player/model.go` is unreachable from the root. Seek/volume controls are dead.

### RC-9 — Debug log masks RC-1
`internal/player/mpv.go:264` — `describeStreamURL` checks
`parsed.Query().Has("token")`. Jellyfin uses `api_key`/`ApiKey`, never
`token`. The `streamHasToken` log line at `mpv.go:144` therefore always
reads `false` even after a fix that adds the query param under the wrong
name, hiding the diagnosis.

---

## 2. What spruce already got right (do not "fix")

| Area | Status | Evidence |
| :--- | :--- | :--- |
| `MediaBrowser` Authorization format | Correct | `client.go:79-85` |
| Headers reach mpv via `--http-header-fields` | Correct | `mpv.go:113-115` |
| `PositionTicks = int64(seconds * 1e7)` | Correct (100ns ticks, .NET TimeSpan) | `types.go:86-91` |
| Playback report endpoints | Correct paths | `/Sessions/Playing`, `/Sessions/Playing/Progress`, `/Sessions/Playing/Stopped` |
| Generation-counter guard on ticks | Correct | `playback.go:80` |
| Socket-ready race (3s retry) | Handled | `commands.go:107-118` |
| Tick waits for `PlayerReadyMsg` | Correct ordering | `model.go:277-282` |
| Both `MusicArtist` and `Artist` queried | Correct | `client.go:194` |
| MPRIS bridge wiring | Correct | `model.go:102-110` |

---

## 3. Jellyfin API Reference (verified against official OpenAPI)

Source: `https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json`
(retrieved via Context7 `/openapi/api_jellyfin_openapi_jellyfin-openapi-stable_json`).

### 3.1 Authentication
- Security scheme: `Authorization` API-key header.
- Canonical client form (the one spruce already uses):
  ```
  Authorization: MediaBrowser Client="<c>", Device="<d>", DeviceId="<id>", Version="<v>", Token="<accessToken>"
  ```
- For direct stream URLs that mpv fetches, Jellyfin also accepts
  `?api_key=<token>` (or `?ApiKey=<token>`) as a query credential. **Use
  both** header + query for redirect safety.

### 3.2 `GET /Audio/{itemId}/stream`
- Required path: `itemId` (uuid).
- Key query params for static direct play:
  - `static=true` — stream original file, no encoding.
  - `api_key=<token>` — credential that survives redirects.
  - `playSessionId=<id>` — correlates with session reports.
  - `mediaSourceId=<id>` — alternate version id.
  - `deviceId=<id>` — lets the server kill encoding jobs for this client.
  - `startTimeTicks=<int64>` — resume offset; **1 tick = 10000 ms** per the
    OpenAPI doc (i.e. 10^7 ticks per second; .NET TimeSpan).
  - `tag=<etag>` — optional cache validator.
- Response: `200` audio body, `503` server starting.

### 3.3 `POST /Sessions/Playing` (start)
Body top-level (PlayStartInfo shape — same fields as Progress minus a few):
`Item`, `ItemId`, `SessionId`, `MediaSourceId`, `AudioStreamIndex`,
`SubtitleStreamIndex`, `IsPaused`, `IsMuted`, `PositionTicks`,
`PlaybackStartTimeTicks`, `VolumeLevel`, `AspectRatio`, `PlayMethod`,
`LiveStreamId`, `PlaySessionId`, `RepeatMode`, `PlaybackOrder`,
`NowPlayingQueue`, `PlaylistItemId`, `CanSeek`.

### 3.4 `POST /Sessions/Playing/Progress`
Same shape as Start. Key fields spruce must add:
- `PlaySessionId` — **required** to correlate with the start/stopped reports.
- `MediaSourceId` — identifies which MediaSource is playing.
- `PositionTicks` — `int64`, 10^7 per second.
- `PlayMethod` — enum `Transcode` | `DirectStream` | `DirectPlay`.
- `IsPaused`, `CanSeek`, `RepeatMode`, `PlaybackOrder`.

### 3.5 `POST /Sessions/Playing/Stopped`
Body (`PlaybackStopInfo`): `Item`, `ItemId`, `SessionId`, `MediaSourceId`,
`PositionTicks`, `LiveStreamId`, `PlaySessionId`, `Failed`, `NextMediaType`,
`PlaylistItemId`, `NowPlayingQueue`. Note: **no `PlayMethod`** on Stopped.

### 3.6 `GET /Users/{userId}/Items`
- Paging: `StartIndex` (int), `Limit` (int). Response includes
  `TotalRecordCount` — loop until `StartIndex + returned >= TotalRecordCount`.
- Filters: `IncludeItemTypes` (e.g. `MusicArtist,Artist`, `MusicAlbum`,
  `Audio`), `Recursive=true`, `ParentId` (library id), `SearchTerm`.
- Sort: `SortBy` (e.g. `SortName`, `ProductionYear`),
  `SortOrder` (`Ascending`, `Descending`).
- Enrichment: `Fields` (e.g. `PrimaryImageAspectRatio`, `MediaSources`,
  `MediaStreams`, `Genres`, `Tags`), `EnableImageTypes`.

### 3.7 `GET /Users/{userId}/Views`
Returns `itemsResponse[Library]` — `Items[]` of `{Id, Name, CollectionType}`.
Filter strictly to `CollectionType == "music"` (spruce currently also accepts
`""`, which picks mixed folders — see Minor-2 below).

### 3.8 `POST /Items/{itemId}/PlaybackInfo`
Returns `MediaSources[]` where each `MediaSourceInfo` includes
`RequiredHttpHeaders` (map header→value) plus `SupportsDirectStream`,
`SupportsDirectPlay`, `TranscodingUrl`. Use this to decide
`DirectPlay` vs `DirectStream` vs `Transcode` and to populate
`MediaSourceId` / `PlaySessionId` correctly. Spruce does not call this today;
adding it makes the session reports spec-correct.

### 3.9 `PlayMethod` enum
`Transcode` | `DirectStream` | `DirectPlay`. Spruce hardcodes
`"DirectStream"` (`types.go:79`); with `static=true` the actual method is
`DirectPlay` and should be reported as such.

---

## 4. Remediation Plan (ordered, build-ready)

### Stage A — Make audio actually play (fixes the headline symptom)

**A1. Add `api_key` to the stream URL.** `internal/jellyfin/client.go`:
```go
func (c *Client) StreamURL(itemID, playSessionID string) string {
    q := url.Values{}
    q.Set("static", "true")
    q.Set("api_key", c.token)
    q.Set("playSessionId", playSessionID)
    q.Set("deviceId", "spruce-tui")
    return fmt.Sprintf("%s/Audio/%s/stream?%s", c.baseURL, itemID, q.Encode())
}
```
Keep `StreamHeaders()` as a **belt-and-suspenders** auth path for
non-redirecting servers. Update `playback.go:32-44` to pass a `playSessionID`
(generate once per `startPlaybackAt`).

**A2. Fix the debug log token check.** `internal/player/mpv.go:264`:
`HasToken: parsed.Query().Has("api_key") || parsed.Query().Has("ApiKey")`.

**A3. Distinguish EOF from fatal error.** Two-part fix:
1. In `player/commands.go`, subscribe to mpv's `end-file` event via the IPC
   socket (`{"command":["observe_event","end-file"]}` or read the
   `{"event":"end-file","reason":...}` broadcast). Emit a typed
   `PlayerEndMsg{Reason: "eof"|"error"|"redirect"}` instead of relying on a
   socket-error heuristic.
2. In `app/playback.go` `handlePositionMsg`, treat `PositionMsg.Err` as a
   **fatal launch/load error** (surface via banner, do not auto-advance).
   Reserve auto-advance for `PlayerEndMsg{Reason:"eof"}` only.

**A4. Surface launch errors to the user.** Add a `components.ErrorBanner`
field to `Model` (mirror `pine/internal/ui/components/error.go`). On
`PlayerLaunchErrMsg` and on the new fatal-load path, call
`m.err.SetError(msg.Err)`; render `m.err.View()` in `View()` (Stage C).

**A5. Quit cleanly.** `q` and `ctrl+c` must call `stopPlayback()` (which
fires `ReportPlaybackStopped`) **before** `tea.Quit`. Without this the
Jellyfin session is orphaned and the server keeps the item marked "now
playing."

### Stage B — Make the library complete

**B1. Paginate every `/Users/{id}/Items` call.** Add a helper:
```go
func (c *Client) fetchPaged(ctx context.Context, base string, params url.Values, into any) error
```
Loop `StartIndex=0, Limit=200` until `TotalRecordCount` reached. Apply to
`GetArtists`, `GetAlbums`, `GetTracks`, `GetAllTracks`.

**B2. Stop swallowing `AllTracksLoadedMsg.Err`.** In
`library/model.go:181-185`, if `msg.Err != nil`, surface it via the error
banner (Stage C) and set a "Library load failed — retry with `r`" state
instead of silently nil-ing the cache.

**B3. Filter music libraries strictly.** `client.go:178` — only
`CollectionType == "music"`. Pick the first music view, not `msg.libraries[0]`.

**B4. Raise the API client timeout** for paged fetches or move the
full-library snapshot to a background `tea.Batch` job with its own
`context.Background()` (no 10s deadline). The 10s timeout stays for
interactive single calls.

### Stage C — Make playback visible (render stack)

Port `pine/internal/app/render.go` into `spruce/internal/app/render.go`.
Five-layer vertical composition:
```go
func (m Model) View() string {
    header    := m.viewHeader()       // "spruce › library"
    errBanner := m.err.View()
    body      := m.viewScreen()
    hints     := m.viewHints()        // per-screen keybindings
    footer    := m.playerState.View() // the missing bar

    parts := []string{header}
    if errBanner != "" { parts = append(parts, errBanner) }
    parts = append(parts, body)
    if hints   != "" { parts = append(parts, hints) }
    if footer  != "" { parts = append(parts, footer) }
    content := lipgloss.JoinVertical(lipgloss.Left, parts...)

    if m.width > 0 && m.height > 0 {
        w := m.width - 1                              // cursor-wrap fix
        content = lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, content)
        content = strings.Join(normalizeOverlayCanvas(content, w, m.height), "\n")
    }
    if m.palette.Visible() { content = m.overlayPaletteModal(content) }
    return content
}
```
Rework `screenHeight()` to subtract header(2) + errorBanner(1 if active) +
hints(1) + footer(1 if active) before propagating to sub-screens, exactly as
`pine/navigation.go:screenHeight()` does.

### Stage D — Make navigation reversible (back-stack)

Port `pine/internal/app/navigation.go`:
1. Add `backStack []Screen` to `Model`.
2. `navigate(target Screen)`: push current, set `m.screen`, `propagateSize`,
   fire target's `Init`.
3. `back()`: pop, set, `propagateSize`.
4. New messages `NavigateMsg{Screen}` and `BackMsg{}` in `app/messages.go`;
   handle in `Update`.
5. Bind `esc` and `left` to `back()` (no-op when stack empty). Bind `q`
   exclusively to quit-with-stop (A5). Bind `tab` to `navigate(ScreenQueue)`
   / `navigate(ScreenLibrary)` instead of inline toggle.
6. Delete the dead `SwitchScreenMsg` or repurpose it as `NavigateMsg`.

### Stage E — Make the player controllable

In `app/model.go` `Update`, after the global key handling, route
`tea.KeyMsg` to `m.playerState.Update(msg)` so `PlayerKeyMap`
(seek `h`/`l`, volume `+`/`-`/`[`/`]`) actually fires. Mirror
`pine/internal/app/model.go:842-852`.

### Stage F — Spec-correct session reporting

1. Generate a `playSessionID` per `startPlaybackAt` (uuid or timestamp).
2. Add `PlaySessionId` + `MediaSourceId` to `PlaybackProgressRequest` and
   `PlaybackStopRequest` (`types.go`).
3. Set `PlayMethod = "DirectPlay"` when `static=true`; fall back to
   `"DirectStream"` only if a transcoding URL is used.
4. Optionally call `POST /Items/{id}/PlaybackInfo` once per track to obtain
   the real `MediaSourceId` and `PlaySessionId` from the server, then use
   those for all three reports.

### Stage G — Verification

1. Unit test `StreamURL` includes `api_key`, `playSessionId`, `deviceId`.
2. Unit test `describeStreamURL` recognises `api_key` and `ApiKey`.
3. E2E test with a mock Jellyfin HTTP server: assert that selecting a track
   → `GET /Audio/{id}/stream?static=true&api_key=...` is requested by mpv
   → `POST /Sessions/Playing` fires → footer renders with the track title
   → `esc` pops the back-stack → `q` fires `ReportPlaybackStopped` before
   exit.
4. E2E test the redirect case: mock server 302-redirects the stream URL;
   assert the redirected request still authenticates (query param survives).
5. E2E test launch failure: mock stream returns 401; assert the error banner
   appears and the queue is **not** silently advanced.

---

## 5. Defect Index (file:line → fix stage)

| # | File:Line | Defect | Stage |
| :--- | :--- | :--- | :--- |
| RC-1 | `jellyfin/client.go:88-90` | Stream URL missing `api_key` → 401 on redirect | A1 |
| RC-2 | `app/playback.go:84-91` | Socket error misclassified as EOF; queue silently skips | A3 |
| RC-3 | `app/model.go:284-287` | `PlayerLaunchErrMsg` only logged, not surfaced | A4 |
| RC-4 | `app/model.go:389-410` | `playerState.View()` never called — no footer | C |
| RC-5 | `app/model.go:389-410` | No header / error banner / hints bar | C |
| RC-6 | `jellyfin/client.go:190-269` | No pagination; library truncates/times out | B1 |
| RC-7 | `app/messages.go:1-14`, `app/model.go:176-233` | No back-stack; `esc`/`left` unbound; `q` leaks session | D, A5 |
| RC-8 | `app/model.go:176-233` | Player keys never reach `playerState.Update` | E |
| RC-9 | `player/mpv.go:264` | `HasToken` checks `"token"` not `"api_key"` — masks RC-1 | A2 |
| Minor-1 | `player/commands.go:101` | `--start=%f` emits `0.000000`; omit when 0 | A (trivial) |
| Minor-2 | `jellyfin/client.go:178` | `CollectionType==""` accepted as music | B3 |
| Minor-3 | `jellyfin/types.go:77-83` | Progress/Stop omit `PlaySessionId`, `MediaSourceId` | F |
| Minor-4 | `jellyfin/types.go:79` | `PlayMethod` hardcoded `"DirectStream"`; should be `"DirectPlay"` for static | F |
| Minor-5 | `app/model.go:484-497` | Shuffle `nextIndex` can replay same track on 1-item queue | E (trivial) |
| Minor-6 | `app/model.go:121-130` | `screenHeight()` can return 0 on tiny windows | C |

---

## 6. Minimum viable fix (if only one session remains)

If time permits only one pass, do **Stage A** alone (A1–A5). That directly
resolves "audio doesn't load and play in mpv":

1. `StreamURL` gets `api_key` + `playSessionId` + `deviceId`.
2. mpv's redirected GET now authenticates → audio plays.
3. EOF vs fatal error separated → no more silent queue wipe.
4. Launch failure shows in a (newly added) error banner → user sees the 401.
5. Clean quit reports stopped to Jellyfin.

Stages B–G are quality/completeness work; Stage A is the unblock.
