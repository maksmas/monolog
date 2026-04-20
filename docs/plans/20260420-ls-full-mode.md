# ls Full Mode

## Overview

Add a `--full` / `-f` flag to `monolog ls` that renders each task as a multi-line detail block
(title + metadata + body), separated by horizontal rules — similar to `git log` vs `git log --oneline`.
The current compact single-line format remains the default. When stdout is a TTY, full-mode output
is piped through `$PAGER` (default: `less -FR`) so long lists are scrollable.

## Context (from discovery)

- Files/components involved: `cmd/ls.go`, `internal/display/table.go`, `cmd/ls_test.go`, `internal/display/table_test.go`
- Related patterns found: `cmd/show.go` already renders show-style per-task blocks for a single task — full mode replicates that layout for a list
- Dependencies identified: terminal detection for pager activation; project already pulls in `golang.org/x/term` transitively via bubbletea stack (verify in go.mod before use)

## Development Approach

- **testing approach**: Regular (code first, tests after)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- Run tests after each change: `go test ./...`

## Testing Strategy

- **unit tests**: `internal/display/table_test.go` for the new formatter; `cmd/ls_test.go` for flag wiring
- Pager helper unit-tested by verifying it is a no-op when the writer is a `bytes.Buffer` (not a TTY file)
- No e2e / UI tests needed

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Solution Overview

1. Add `FormatTasksFull(w io.Writer, tasks []model.Task, now time.Time, layout string)` to `internal/display/table.go`. Each task block:
   - Header line: `<active?> <marker>  <shortID>  <full title>` (no truncation)
   - Indented metadata: Status, Schedule, Recur (if set), Tags (if any), Created, Updated (if set), Completed (if set), Notes count (if >0)
   - Blank line + indented body lines (if Body is non-empty)
   - Separator: `strings.Repeat("─", 60)` after every task
2. Add `withPager(w io.Writer, fn func(io.Writer) error) error` helper in `cmd/pager.go`. Activates only when `w` is `*os.File` and is a TTY; starts `$PAGER` or `less -FR` as a subprocess with our pipe as its stdin.
3. Wire `--full` / `-f` flag into `cmd/ls.go`; route to `FormatTasksFull` wrapped in `withPager` when set.

## Technical Details

**FormatTasksFull block layout (per task):**
```
* 1  01J5K3AB  Fix login bug
   Status:    open
   Schedule:  today
   Tags:      backend, auth
   Recur:     weekly:mon
   Created:   20-04-2026
   Notes:     2

   Some body text here...
   --- 20-04-2026 10:30:00 ---
   Looked into the issue.

────────────────────────────────────────────────────────────
```

**Pager activation conditions:**
- Writer is `*os.File` (i.e. `os.Stdout`, not a `bytes.Buffer`)
- `term.IsTerminal(int(f.Fd()))` returns true
- If `$PAGER` is set, use it; otherwise `less -FR`
- On any exec error, fall back to writing directly (no crash)

**Label alignment:** 10-char left-aligned label field (`"Status:   "`, `"Schedule: "`, etc.) matching `cmd/show.go` style.

## What Goes Where

**Implementation Steps** (`[ ]` checkboxes): all code changes below

**Post-Completion** (no checkboxes):
- Manual smoke test: `monolog ls --full --all` in a real terminal to verify pager launches and renders correctly
- Verify Cmd-click URLs still work in full-mode body output (OSC 8 sequences; currently intentionally omitted per CLAUDE.md — plain text in CLI output is the stated contract, so no change needed)

## Implementation Steps

### Task 1: Add FormatTasksFull to internal/display/table.go

**Files:**
- Modify: `internal/display/table.go`
- Modify: `internal/display/table_test.go`

- [x] add `FormatTasksFull(w io.Writer, tasks []model.Task, now time.Time, layout string)` to `internal/display/table.go`
- [x] render header line: active marker + position/done marker + short ID + full title (no truncation)
- [x] render indented metadata lines: Status, Schedule (bucket + display date, mirroring show.go logic), Recur (omitempty), Tags (omitempty), Created, Updated (omitempty), Completed (omitempty), Notes count (omitempty)
- [x] render blank line + indented body lines when Body is non-empty
- [x] render `strings.Repeat("─", 60)` separator after each task block
- [x] emit `"No tasks.\n"` when slice is empty (same as FormatTasks)
- [x] write `TestFormatTasksFull_Empty` — empty slice → "No tasks.\n"
- [x] write `TestFormatTasksFull_SingleTask` — verify title, ID, status, schedule appear; no truncation
- [x] write `TestFormatTasksFull_WithBody` — task with Body shows body lines indented; separator present
- [x] write `TestFormatTasksFull_Omitempty` — Recur/Tags/Updated/Completed absent when unset
- [x] write `TestFormatTasksFull_ActiveMarker` — active task shows `* ` prefix
- [x] run `go test ./internal/display/` — must pass before task 2

### Task 2: Add pager helper in cmd/pager.go

**Files:**
- Create: `cmd/pager.go`
- Create: `cmd/pager_test.go`

- [x] create `cmd/pager.go` with `withPager(w io.Writer, fn func(io.Writer) error) error`
- [x] detect TTY: type-assert `w` to `*os.File`, then `term.IsTerminal(int(f.Fd()))` — if either fails, call `fn(w)` directly
- [x] resolve pager command: `os.Getenv("PAGER")`; if empty default to `"less -FR"`
- [x] start pager subprocess (stdin=pipe, stdout=w, stderr=os.Stderr); on exec error fall back to `fn(w)`
- [x] write to pipe via `fn(pw)`, close pipe, call `cmd.Wait()`
- [x] check whether `golang.org/x/term` is already in `go.sum`; if not, add via `go get golang.org/x/term` (used `github.com/charmbracelet/x/term` — already an indirect dep, promoted to direct)
- [x] write `TestWithPager_NoOpWhenNotFile` — pass `bytes.Buffer`; verify fn is called with buffer directly, no subprocess spawned
- [x] run `go test ./cmd/ -run TestWithPager` — must pass before task 3

### Task 3: Wire --full flag into cmd/ls.go

**Files:**
- Modify: `cmd/ls.go`
- Modify: `cmd/ls_test.go`

- [x] add `full bool` local var and `--full` / `-f` flag registration in `newLsCmd()`
- [x] in `RunE`: when `full` is true, call `withPager(cmd.OutOrStdout(), func(w) { FormatTasksFull(w, tasks, now, config.DateFormat()); return nil })`; otherwise keep existing `FormatTasks` call
- [x] write `TestLsCommand_FullFlag_ShowsMetadata` — tasks with body/tags; `ls --full --all` output contains indented metadata keys and separator line
- [x] write `TestLsCommand_FullFlag_Empty` — `ls --full` on empty store → "No tasks.\n"
- [x] write `TestLsCommand_FullFlag_WithBody` — task with body; verify body appears in `--full` output but not in default compact output
- [x] run `go test ./cmd/ -run TestLsCommand_Full` — must pass before task 4

### Task 4: Verify acceptance criteria

- [x] verify all three tasks above are complete and `[x]`
- [x] run full test suite: `go test ./...`
- [x] run `go vet ./...`
- [x] verify compact mode (`ls` with no flags) output is unchanged
- [x] verify `ls --full --all` renders multi-line blocks with separator lines
- [x] verify `ls --full` is composable with existing filters (`--schedule`, `--tag`, `--done`, `--active`)

### Task 5: [Final] Update documentation

**Files:**
- Modify: `CLAUDE.md`
- Move: this plan to `docs/plans/completed/`

- [ ] add `--full` flag description to the `ls` command section in `CLAUDE.md` if a CLI surface summary exists (check first — avoid adding docs that don't exist yet)
- [ ] move this plan to `docs/plans/completed/20260420-ls-full-mode.md`

## Post-Completion

**Manual verification:**
- Run `monolog ls --full --all` in an interactive terminal; confirm pager launches and scrolls
- Run `monolog ls --full --all | cat` to verify no pager when piped (not a TTY)
- Run `PAGER=more monolog ls --full` to verify custom pager env var is respected
