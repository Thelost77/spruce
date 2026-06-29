# Spruce TUI: Deep Audit & Architectural Gap Analysis

This document provides a rigorous architectural audit of the `spruce` Jellyfin TUI client, comparing its current implementation against the proven `pine` reference architecture and official Jellyfin API specifications. It catalogs root causes for observed failures (playback initiation, track discovery, UI composition, navigation) and outlines the structural remediation required for the next development session.

---

## Executive Summary

While `spruce` successfully reused standalone components (`mpv` subprocess wrapper, `mpris` adapter, `ui` primitives), its **core integration layer (`internal/app`)** deviated significantly from `pine`'s Elm-architecture patterns. Instead of implementing a unified screen stack, structured command delegation, and formal view composition, `spruce` relied on ad-hoc state mutation and partial screen switching. 

This architectural drift directly caused the failures reported during UAT:
1. **Broken Playback & Footer Visibility**: The root view renderer completely omitted the player footer sub-model, and playback start commands failed to properly handshake with the `mpv` socket subprocess.
2. **Brittle Navigation**: Instead of a centralized back-stack router (`push`/`pop`), navigation was split across nested sub-model levels (`LevelArtists` → `LevelAlbums` → `LevelTracks`) and hardcoded global keys, trapping users or quitting unexpectedly.
3. **Incomplete Track Discovery**: Data loading lacked pagination and relied on a single un-paginated REST call without progressive caching or background hierarchy resolution.

---

## 1. View Composition & Player Footer Rendering

### Architectural Comparison

| Feature | `pine` Reference (`internal/app/render.go`) | `spruce` Implementation (`internal/app/model.go`) |
| :--- | :--- | :--- |
| **View Pipeline** | Unified pipeline joining `header`, `errBanner`, `body` (active screen), `hints`, and `footer` (`m.player.View()`). | Raw switch on `m.screen` returning `libraryScreen.View()`, `queueScreen.View()`, or `loginScreen.View()`. |
| **Footer Integration** | `footer := m.player.View()` explicitly evaluated and appended to vertical layout stack. | `m.playerState.View()` was **never called** inside `View()`, making the bottom panel permanently invisible. |
| **Modal Overlay** | Modal overlays (`chapterOverlay`, confirmations) wrap the composed base canvas after size normalization. | `overlayPaletteModal` wrapped individual screen strings without accounting for global header/footer dimensions. |

### Root Cause Analysis: Playback Panel Absence
In `spruce/internal/app/model.go`, the `View()` function simply executed:
```go
func (m Model) View() string {
    var content string
    switch m.screen {
    case ScreenLibrary: content = m.libraryScreen.View()
    case ScreenQueue:   content = m.queueScreen.View()
    default:            content = m.loginScreen.View()
    }
    return placed
}
```
Because the root model did not dedicate the bottom screen lines to `m.playerState.View()`, even when playback state (`m.playerState.Playing = true`) and titles were correctly populated in memory, the TUI never rendered the visual playback footer.

### Remediation Blueprint
Create a dedicated `internal/app/render.go` in `spruce` matching `pine`'s layout contract:
1. Deduct exact lines for header (1 line), error banner (if active), hints bar (1 line), and player footer (1 line if active) before passing height to sub-screens.
2. Assemble the final frame using `lipgloss.JoinVertical(lipgloss.Left, header, errBanner, screenContent, hints, playerFooter)`.

---

## 2. Navigation Router & Screen Management

### Architectural Comparison

| Feature | `pine` Reference (`internal/app/navigation.go`) | `spruce` Implementation (`internal/screens/library/model.go`) |
| :--- | :--- | :--- |
| **Screen State** | Explicit `Screen` enum managed by root router with `m.backStack = append(m.backStack, m.screen)`. | Binary/ternary switches (`ScreenLogin`, `ScreenLibrary`, `ScreenQueue`) with internal state machine inside `libraryScreen`. |
| **Back Action** | Screens emit typed messages (`GoBackMsg`). Root router catches message, pops `backStack`, and calls `propagateSize()`. | Hardcoded key checks (`esc`, `h`, `left`) inside internal list helpers. Root key handler intercepted `esc` as `tea.Quit`. |
| **Hierarchy Depth** | Separate screens for `Library` (items list), `SeriesList`, `Series`, and `Detail`. | Single `libraryScreen` attempting to multiplex Artists, Albums, and Tracks via an internal `level` enum. |

### Root Cause Analysis: Navigation Traps
In `spruce`, navigating deep into an artist's album tracklist shifted `libraryScreen.level = LevelTracks`. When pressing `esc` or `h`:
1. If `libraryScreen` caught the key, it decremented its internal enum (`LevelTracks` → `LevelAlbums`).
2. If at `LevelArtists`, `libraryScreen` ignored the key, expecting the root parent to handle it.
3. However, the root `KeyMsg` handler either ignored it or executed `tea.Quit` (prior to remediation), abruptly terminating the application instead of returning to a dashboard or queue screen.

### Remediation Blueprint
1. **Decouple Hierarchy Screens**: Separate the library into distinct sub-models or adopt formal message delegation where every backward transition emits a clean `NavigateBackMsg`.
2. **Implement Root Back-Stack**: Transplant `pine`'s `navigate(target Screen)` and `back()` stack functions into `spruce/internal/app/navigation.go`.
3. **Global Key Discipline**: Reserve `q` exclusively for application quit (`tea.Quit`). Route `esc`, `h`, and `left` strictly through the stack popping router.

---

## 3. Playback Lifecycle & IPC Synchronization

### Architectural Comparison

| Feature | `pine` Reference (`internal/app/playback.go`) | `spruce` Implementation (`internal/app/playback.go`) |
| :--- | :--- | :--- |
| **Launch Trigger** | `handlePlaySessionMsg` prepares session data, sets generation counters, synchronizes MPRIS bridge, and fires `player.LaunchCmd`. | `startPlaybackAt` mutated local track index and triggered `player.LaunchCmd`, but lacked generation guard validation. |
| **Tick & Heartbeat** | 500ms `TickCmd` polls socket position; fires ABS progress reports asynchronously without blocking UI rendering. | Fires Jellyfin `/Sessions/Playing` synchronously during launch setup; tick progress reporting was unverified against socket EOF events. |
| **MPRIS Bridge** | Root model intercepts `StartPlayMsg`, binds `ModelAccessor` property reads, and pushes updates to D-Bus adapter. | Bridge initialized in `SetProgram`, but disconnected from track transition events during multi-track playback. |

### Root Cause Analysis: Silent Playback Failures
When clicking play or selecting a track in `spruce`:
1. The REST streaming endpoint `/Audio/{ItemId}/stream?static=true` requires valid authentication headers (`Authorization: MediaBrowser ...` or `X-Emby-Token`). If stream URL parameters or headers were malformed or missing during the `mpv` subprocess launch, `mpv` exited immediately with code 1.
2. Because `spruce` did not surface `player.PlayerLaunchErrMsg` visually via an auto-dismissing error banner (like `pine`'s `ErrorBanner`), silent socket connection failures left the UI in an unplayable state with no feedback.

### Remediation Blueprint
1. **Surface IPC Errors**: Wire `player.PlayerLaunchErrMsg` and `player.PlayerErrMsg` directly into a visible 5-second header error banner.
2. **Stream URL Verification**: Ensure Jellyfin direct stream URLs include API tokens explicitly when passed to the `mpv` command line argument list (`--http-header-fields=...`).
3. **Heartbeat Loop**: Implement a structured 5-second recurring ticker that reports `PositionTicks` ($seconds \times 10^7$) to Jellyfin's `/Sessions/Playing/Progress` endpoint.

---

## 4. Jellyfin API & Track Discovery Engine

### Jellyfin REST Specification vs. Implementation

| Specification Requirement | `spruce` Implementation Status | Identified Deficiencies |
| :--- | :--- | :--- |
| **Library View Discovery** (`GET /Users/{Id}/Views`) | Implemented (`GetMusicLibraries`) | Correctly identifies libraries of type `music`. |
| **Artist Querying** (`GET /Users/{Id}/Items`) | Partial (`IncludeItemTypes=MusicArtist,Artist`) | Jellyfin databases often split artists between `MusicArtist` (album artists) and `Artist` (track performers). Both must be queried with `Recursive=true`. |
| **Pagination & Batching** (`StartIndex` & `Limit`) | Missing | `spruce` fetched items without pagination limits. On large media libraries (>5,000 tracks), this causes API timeouts or partial payload truncation by intermediate reverse proxies. |
| **Progressive Caching** | Missing | Unlike `pine`'s SQLite/memory cache (`internal/cache`), `spruce` executed synchronous network blocking calls on every screen switch. |

### Remediation Blueprint
1. **Implement Background Library Snapshot**: On startup (after credentials load), spawn an asynchronous batch job (`tea.Batch`) that fetches Artists, Albums, and Tracks in paginated chunks (100 items/page).
2. **Local Memory Index**: Store normalized items in an in-memory lookup map (`map[string]*jellyfin.Track`), giving the search command palette (`Ctrl+P`) sub-millisecond query access across the entire server library.

---

## 5. Roadmap for Next Development Session

When development resumes, execution should follow this strict 4-stage remediation plan:

### Stage 1: Structural View & Router Refactoring
* Create `internal/app/render.go` implementing exact 5-layer vertical composition (Header → ErrorBanner → ActiveScreen → Hints → PlayerFooter).
* Create `internal/app/navigation.go` implementing `backStack []Screen`, `navigate(Screen)`, and `back()`.

### Stage 2: Jellyfin API & Data Pipeline Hardening
* Refactor `internal/jellyfin/client.go` to support paginated fetching (`Limit=200`, `StartIndex=N`) until `TotalRecordCount` is reached.
* Build a background indexing command that populates global Artist/Album/Track search slices without blocking UI interactivity.

### Stage 3: Playback Engine & Socket IPC Verification
* Audit `player.LaunchCmd` header formatting for Jellyfin streaming URLs.
* Wire automated error reporting so mpv socket failures display clearly in the UI header banner.
* Connect mpv EOF / track completion events to automatically advance the queue index.

### Stage 4: End-to-End Verification & UAT Suite
* Update programmatic E2E tests (`internal/app/e2e_test.go`) using mock HTTP servers to verify that track selection transitions state, renders footer strings, and handles back-stack pops cleanly.
