# TUI Auto-Refresh on External Changes

## Overview

The interactive TUI does not currently notice when another process mutates the
task store. The Raycast capture flow shells out to `monolog add`, which writes a
new JSON file under `.monolog/tasks/` and commits it; a TUI that is already
running on the same repo keeps showing the stale list until the user manually
hits `s` (sync), reschedules a task, or restarts the TUI. The same gap exists
for tasks added/edited from a second terminal or pulled in by a sibling tool
running `git pull`.

This plan adds a filesystem watcher to the TUI that observes the tasks
directory and triggers `reloadAll()` whenever an external process changes
something there. Existing in-process mutations already call `reloadAll()`
themselves, so the new path only matters when the change comes from outside the
running TUI.

## Context (from discovery)

- Files/components involved:
  - `internal/tui/tui.go` — `Run` constructs the Model and starts Bubble Tea
  - `internal/tui/model.go` — `Model.Init` (~line 937), `Model.Update` (~line 946), `reloadAll` (~line 506)
  - `internal/tui/email.go` — reference pattern for goroutine-backed `tea.Cmd` + tick loop (`emailSyncCmd`, `emailTickCmd`)
  - `go.mod` — currently no `fsnotify` dependency (will be added)
- Key patterns:
  - Bubble Tea side effects flow through `tea.Cmd`s that return `tea.Msg`s; the email integration is the closest analogue and shows the message-result pattern (`emailTickMsg`, `emailSyncResult`).
  - Mutations in the TUI commit via `git.AutoCommitSHA`, then dispatch `taskSavedMsg` which already calls `reloadAll`. The new watcher must NOT undermine that flow — the simple, idempotent approach is to reload on every debounced event, even if the event was caused by the TUI's own write.
- Scope boundary:
  - Watch only `<repo>/.monolog/tasks/` (where Raycast/`monolog add` and any external `git pull` write task JSON files).
  - Do not watch `.monolog/config.json`, `.monolog/themes/`, or the `.git/` tree — config/theme reload-on-write is a separate feature; git internals would be noisy and irrelevant.
  - macOS + Linux only via `fsnotify`. Windows is not a supported target today and the existing TUI has no Windows-specific code paths.

## Development Approach

- **Testing approach**: Regular (code first, tests after)
- Complete each task fully before moving to the next
- All tests must pass before starting next task
- Run `go test ./...` after each task

## Testing Strategy

- **Unit tests**: `internal/tui/watcher_test.go` for the debounce + channel
  plumbing. The fsnotify integration itself is covered by writing real files
  into a `t.TempDir()` and asserting the watcher delivers a single event after
  the debounce window even when several writes arrive in burst.
- **Manual smoke test**:
  1. Open the TUI in one terminal.
  2. From a second terminal, run `monolog add "external test"`.
  3. Within ~half a second the new task should appear in the TUI without any
     keypress.
  4. Repeat with `monolog edit <id> --schedule tomorrow` from the second
     terminal — the task should move to the Tomorrow tab automatically.

## Solution Overview

Add a small debounced fsnotify wrapper (`internal/tui/watcher.go`) that owns a
goroutine, reads filesystem events on `<repo>/.monolog/tasks/`, coalesces
events arriving within a 250ms window, and emits one signal per debounced
burst onto a Go channel. Wire this into `Model.Init` so the channel-read
appears as a Bubble Tea command that returns an `externalChangeMsg`. The
`Update` handler reacts by calling `reloadAll()` + `recomputeLayout()` (the
same operations `taskSavedMsg` already runs on a successful in-process
mutation), then re-arms the cmd to wait for the next signal.

Why fsnotify rather than a poll: events are immediate (sub-100ms perceived
refresh) and zero-cost while idle. Why a debounce: a single CLI mutation can
produce a `Create` + `Write` (and the TUI's own writes can issue similar
sequences); coalescing them prevents three reloads per add. Why "always
reload, even on self-triggered events": `reloadAll()` is already idempotent
and runs in well under 100ms for realistic task counts; trying to filter out
self-triggered events would require synchronisation with the goroutine that
performs the mutation and is much harder to make race-free than just
accepting one harmless extra reload.

## Technical Details

- **New dependency**: `github.com/fsnotify/fsnotify` (small, well-maintained,
  Bubble Tea ecosystem already pulls in similar maintained packages).
- **Watcher API** (new, internal to `internal/tui`):
  ```go
  type taskWatcher struct {
      ch     chan struct{}    // debounced "something changed" signals
      stopFn func() error     // closes the underlying fsnotify watcher
  }

  func newTaskWatcher(tasksDir string, debounce time.Duration) (*taskWatcher, error)
  func (w *taskWatcher) Stop() error
  ```
  The constructor opens an `fsnotify.Watcher`, adds `tasksDir` to it, and
  spawns a goroutine that reads events, applies the debounce, and writes to
  `w.ch`. Errors from the goroutine (e.g. `Watcher.Errors`) are dropped on the
  floor after a one-time stderr warning — the TUI must keep running even if
  the watcher dies. (Future enhancement could surface a status flash.)
- **Debounce semantics**: timer-based — first event starts a `time.Timer` for
  `debounce`; subsequent events reset it; on fire, send one struct{} on `ch`
  if the consumer is ready (non-blocking send via `select`/`default`, since
  another debounce window already coalesces the case where the consumer is
  briefly busy).
- **Bubble Tea integration**:
  - New message: `externalChangeMsg struct{}`.
  - New cmd: `(m *Model) watchCmd() tea.Cmd` reads one value from
    `m.watcher.ch` and returns `externalChangeMsg{}`. Returns `nil` if
    `m.watcher == nil` (watcher failed to start) so `tea.Batch` drops it.
  - `Model.Init` adds `m.watchCmd()` to the existing `tea.Batch` of
    email-sync + email-tick. When email is disabled, `Init` previously
    returned `nil`; it now returns `m.watchCmd()` directly (or `nil` if the
    watcher also failed).
  - `Model.Update` gets a new case for `externalChangeMsg`:
    1. Call `reloadAll()` + `recomputeLayout()`.
    2. Skip a separator if needed (mirroring the `taskSavedMsg` tail).
    3. Return `m, m.watchCmd()` so the next signal is awaited.
  - **Status feedback**: keep the TUI quiet on routine refreshes (no flash
    on every external change — would be noisy if the user is making rapid
    changes from another terminal). If the reload returns an error, surface
    it via `m.err` like other reload sites already do.
- **Lifecycle**:
  - The watcher is created in `tui.Run` after `newModel` succeeds. Failure to
    create the watcher prints a `monolog: watcher: <err>` line to stderr and
    proceeds with `m.watcher = nil`; the TUI runs without auto-refresh
    instead of failing to start. (Same precedent as `bootstrapExampleTheme`
    and `initThemes` in `tui.Run`.)
  - The watcher is stopped after `p.Run()` returns (deferred in `tui.Run`).
    Goroutine leak on hard process exit is irrelevant because the OS reaps
    file handles.
- **Self-triggered reload cost**: `reloadAll()` reads N small JSON files;
  the redundant reload on our own mutation is bounded and invisible to the
  user. Acceptable.
- **Why not poll**: a 1–2s mtime poll would be dependency-free but always
  on, and the perceived latency would be 1–2s per add. The user explicitly
  asked about auto-refresh in response to a Raycast workflow that already
  feels instant — matching that latency matters.
- **Edge cases**:
  - Tasks directory does not yet exist when `newTaskWatcher` is called:
    `git.Init` always creates `.monolog/tasks/` before the TUI launches, but
    if for any reason it is missing, `fsnotify.Watcher.Add` returns an
    error; we treat that as a non-fatal "watcher disabled" startup case.
  - Tasks directory is removed at runtime (`rm -rf .monolog/tasks/`):
    fsnotify reports a remove on the watched path. We log once to stderr
    and stop the watcher; subsequent external changes will not auto-refresh
    until the TUI restarts. Acceptable for an edge case.
  - File rename (not expected — ULIDs are stable): treated like
    create+remove, both trigger a debounced reload.
  - `MONOLOG_NO_WATCH=1` env var as an escape hatch (mirrors
    `MONOLOG_NO_LINKS=1`) — when set, `newTaskWatcher` returns `nil, nil`
    and the TUI runs without auto-refresh.

## What Goes Where

**Implementation Steps** — all within this codebase.

**Post-Completion**:
- Manual TUI smoke test (see Testing Strategy).
- Verify on Linux + macOS (fsnotify abstracts both, but a sanity check is
  cheap).

## Implementation Steps

### Task 1: Add the `taskWatcher` helper

**Files:**
- Create: `internal/tui/watcher.go`
- Create: `internal/tui/watcher_test.go`
- Modify: `go.mod`, `go.sum`

- [x] add `github.com/fsnotify/fsnotify` to `go.mod` via `go get github.com/fsnotify/fsnotify`
- [x] implement `taskWatcher` with `newTaskWatcher(tasksDir string, debounce time.Duration) (*taskWatcher, error)` and `Stop() error`
- [x] honour `MONOLOG_NO_WATCH=1` by returning `(nil, nil)` from the constructor
- [x] write `TestWatcher_DebouncesBurst` — create a temp dir, start the watcher with 50ms debounce, write three files in rapid succession, assert exactly one signal arrives within ~150ms and no second signal arrives within the next 200ms
- [x] write `TestWatcher_DetectsExternalCreate` — create a temp dir, start the watcher, write one file after a short sleep, assert one signal arrives
- [x] write `TestWatcher_StopReturnsCleanly` — start, stop, assert no panic and the channel is closed
- [x] write `TestWatcher_NoWatchEnv` — set `MONOLOG_NO_WATCH=1` via `t.Setenv`, assert the constructor returns `nil, nil`
- [x] run `go test ./internal/tui/` — must pass

### Task 2: Wire the watcher into the TUI

**Files:**
- Modify: `internal/tui/tui.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] in `tui.Run`, after `newModel` succeeds, call `newTaskWatcher(filepath.Join(repoPath, ".monolog", "tasks"), 250*time.Millisecond)`. On error, `fmt.Fprintf(os.Stderr, "monolog: watcher: %v\n", err)` and proceed with `m.watcher = nil`. Defer `m.watcher.Stop()` (guarded for nil) before `p.Run()`.
- [x] add `watcher *taskWatcher` field to `Model`
- [x] add `externalChangeMsg struct{}` near the other message types
- [x] add `(m *Model) watchCmd() tea.Cmd` that returns `nil` if `m.watcher == nil`, otherwise returns a func that reads one value from `m.watcher.ch` and returns `externalChangeMsg{}` (channel closed → return `nil` so the loop quietly stops)
- [x] update `Model.Init` to include `m.watchCmd()` in the returned batch (alongside email cmds when applicable)
- [x] add an `externalChangeMsg` case in `Model.Update` that calls `reloadAll()` (set `m.err` on failure), `recomputeLayout()`, `skipSeparator(0)` if `viewMode == viewTag`, then returns `m, m.watchCmd()`
- [x] add `TestExternalChangeMsg_TriggersReload` — construct a Model against a temp store with one task, dispatch `externalChangeMsg{}` via `m.Update`, write a second task to disk, dispatch the message again, assert the second task appears in the appropriate tab. (No real watcher used in the test — message dispatch verifies the Update handler.)
- [x] run `go test ./internal/tui/` — must pass

### Task 3: Verify acceptance criteria

- [x] open the TUI; from a second terminal run `monolog add "external test"`; new task appears within ~500ms with no keypress
- [x] from a second terminal run `monolog edit <id> --schedule tomorrow`; task moves to the Tomorrow tab without a keypress
- [x] simulate Raycast: trigger the `monolog-capture.sh` script (or `monolog add "x" --body "y" --tags inbox`) and confirm the running TUI updates
- [x] set `MONOLOG_NO_WATCH=1`, restart the TUI, repeat step 1, confirm no auto-refresh and `s` still works (manual fallback intact)
- [x] run full test suite: `go test ./...`
- [x] run `go vet ./...`

### Task 4: [Final] Update documentation

- [x] add a "TUI auto-refresh" bullet to CLAUDE.md `Key Design Decisions` describing the watcher: directory watched, debounce window, `MONOLOG_NO_WATCH=1` escape hatch, idempotent reload, fsnotify dep
- [x] move this plan to `docs/plans/completed/`
