# Task Notes

## Overview
- Add the ability to attach multiple timestamped notes to tasks, recording progress over time
- Notes are appended to the existing `Body` field using `--- YYYY-MM-DD HH:MM:SS ---` separators — no schema migration
- A new `NoteCount` field on Task provides fast badge rendering without body parsing
- Three entry points: TUI detail panel (Enter), CLI `note` command, and the existing YAML editor (`e`)
- A TUI right-side detail panel shows full task info and notes, with an inline textarea for adding new notes
- A CLI `show` command prints full task detail to stdout

## Context (from discovery)
- `internal/model/task.go`: Task struct with `Body string` field — notes append here
- `internal/store/store.go`: File-based CRUD, `Resolve()` for ULID prefix/initials matching
- `internal/tui/model.go`: Bubble Tea TUI with modal system, textarea/textinput already imported
- `internal/tui/vlist.go`: Variable-height scrollable list, `Render()` callback, `item` struct with `Title()`/`Description()`
- `cmd/`: One file per command, pattern: `newXxxCmd() *cobra.Command`, registered in `root.go`
- `cmd/helpers.go`: `openStore()` returns `(*store.Store, repoPath, error)`

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility (existing tasks without NoteCount work fine)

## Testing Strategy
- **Unit tests**: required for every task
- Model-layer functions (`AppendNote`, note count field) — pure function tests
- CLI commands (`note`, `show`) — follow existing patterns in `cmd/task_commands_test.go`
- TUI changes — tested via existing TUI test patterns in `internal/tui/model_test.go`

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## Solution Overview

### Storage
Notes live inside the existing `Body` string, separated by timestamped headers:
```
original body text

--- 2026-04-16 14:32:07 ---
first note

--- 2026-04-17 09:15:22 ---
second note (can be multi-line)
```

A new `NoteCount int` field (JSON `note_count`, omitempty) tracks the count. Incremented by `AppendNote`-callers, read directly for badge display.

### TUI Detail Panel
- `Enter` toggles a right-side panel (default: closed)
- Panel header: title (bold), schedule, tags, created date (completed date if done)
- Panel body: the task's `Body` rendered as-is, scrollable
- Panel footer: textarea for new notes (Enter=submit, Alt+Enter=newline)
- `Esc` closes the panel and returns focus to the task list
- Navigating tasks (↑/↓) while panel is open refreshes the panel content

### Note Badge
- `[N]` appended after the title in `renderListItem` when `NoteCount > 0`
- Shown in the `Description()` line alongside existing metadata (ShortID, schedule)

### CLI Commands
- `monolog note <identifier> "text"` — resolves task, appends note, increments count, saves, auto-commits
- `monolog show <identifier>` — prints title, schedule, tags, dates, body to stdout

## Implementation Steps

### Task 1: Add NoteCount field and AppendNote helper

**Files:**
- Modify: `internal/model/task.go`
- Create: `internal/model/note.go`
- Create: `internal/model/note_test.go`

- [x] Add `NoteCount int json:"note_count,omitempty"` field to `Task` struct in `task.go`
- [x] Create `note.go` with `AppendNote(body, text string, now time.Time) string` — formats separator + note, handles empty body (no leading blank line)
- [x] Write tests for `AppendNote`: empty body, existing body, multi-line note text, timestamp formatting
- [x] Run tests: `go test ./internal/model/`

### Task 2: CLI `note` command

**Files:**
- Create: `cmd/note.go`
- Modify: `cmd/root.go`
- Create: `cmd/note_test.go`

- [x] Create `cmd/note.go` with `newNoteCmd() *cobra.Command` — takes `<identifier> <text>`, resolves via `store.Resolve()`, calls `AppendNote`, increments `NoteCount`, sets `UpdatedAt`, saves via `store.Update()`, auto-commits
- [x] Register command in `root.go`: `rootCmd.AddCommand(newNoteCmd())`
- [x] Write tests: successful note addition, task not found, output message format
- [x] Run tests: `go test ./cmd/`

### Task 3: CLI `show` command

**Files:**
- Create: `cmd/show.go`
- Modify: `cmd/root.go`
- Create: `cmd/show_test.go`

- [x] Create `cmd/show.go` with `newShowCmd() *cobra.Command` — resolves task, prints formatted detail (title, schedule, tags, created/completed dates, body) to stdout
- [x] Register command in `root.go`: `rootCmd.AddCommand(newShowCmd())`
- [x] Write tests: output format with body+notes, output without body, tag/schedule display
- [x] Run tests: `go test ./cmd/`

### Task 4: Note count badge in TUI task list

**Files:**
- Modify: `internal/tui/model.go`

- [x] Update `item.Description()` (or `renderListItem`) to append `[N]` after the title when `task.NoteCount > 0`
- [x] Write tests for badge rendering (count > 0 shows badge, count 0 no badge)
- [x] Run tests: `go test ./internal/tui/`

### Task 5: TUI detail panel — state and toggle

**Files:**
- Modify: `internal/tui/model.go`

- [x] Add new mode or state fields to `Model`: `detailOpen bool`, `detailScroll int`, `noteArea textarea.Model` (for the note input textarea)
- [x] Initialize `noteArea` in `newModel()` with placeholder text, configure Alt+Enter for newlines
- [x] Handle `Enter` key in `modeNormal` — toggle `detailOpen`, focus `noteArea` when opening, reset scroll
- [x] Handle `Esc` when `detailOpen` — close panel, return focus to list navigation
- [x] Handle `↑/↓` navigation when panel is open — refresh panel content for newly selected task
- [x] Write tests for panel toggle state transitions (open/close, focus management)
- [x] Run tests: `go test ./internal/tui/`

### Task 6: TUI detail panel — rendering

**Files:**
- Modify: `internal/tui/model.go`

- [x] Create `detailPanelView() string` method — renders panel with: title (bold), schedule, tags, created date (completed if done), blank line, body as-is (scrollable), separator, textarea
- [x] Update `View()` — when `detailOpen`, render task list and detail panel side-by-side using `lipgloss.JoinHorizontal`. Allocate ~55% width to list, ~45% to panel.
- [x] Adjust list width when panel is open (narrower list to make room)
- [x] Handle terminal resize — recalculate split widths in `WindowSizeMsg`
- [x] Write tests for panel rendering (metadata display, body content, layout dimensions)
- [x] Run tests: `go test ./internal/tui/`

### Task 7: Update help/status bar with Enter hotkey

**Files:**
- Modify: `internal/tui/model.go`

- [x] Update `helpLine()` in `modeNormal` — add `Enter:notes` to both schedule-view and tag-view help bars
- [x] Update `helpLine()` when `detailOpen` — show `Esc:close Enter:submit` instead of the normal keybindings
- [x] Run tests: `go test ./internal/tui/`

### Task 8: TUI detail panel — note submission

**Files:**
- Modify: `internal/tui/model.go`

- [ ] Handle `Enter` in textarea when `detailOpen` and textarea is focused — call `AppendNote`, increment `NoteCount`, set `UpdatedAt`, save via `store.Update()`, auto-commit, clear textarea, reload task/panel
- [ ] Handle `Alt+Enter` in textarea — insert newline (default textarea behavior with proper config)
- [ ] Ignore submission when textarea is empty (just Enter with no text = no-op)
- [ ] Write tests for note submission flow (body update, count increment, empty input ignored)
- [ ] Run tests: `go test ./internal/tui/`

### Task 9: Verify acceptance criteria

- [ ] Verify all requirements from Overview are implemented
- [ ] Verify edge cases: empty body + first note, very long notes, tasks with no notes
- [ ] Test TUI panel open/close/navigate cycle manually
- [ ] Test CLI `note` and `show` commands manually
- [ ] Run full test suite: `go test ./...`
- [ ] Run vet: `go vet ./...`

### Task 10: [Final] Update documentation

- [ ] Update CLAUDE.md with task notes design decisions (detail panel, note format, NoteCount field)
- [ ] Move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- TUI: open panel with Enter, verify metadata display, add a note, close panel, reopen — note persists
- TUI: navigate between tasks with panel open — panel refreshes correctly
- TUI: verify note badge `[N]` appears in task list after adding notes
- CLI: `monolog note <id> "text"` and verify with `monolog show <id>`
- Cross-device: add note on one device, sync, verify note appears on other device
