# Active Tasks

## Overview

Add a lightweight "active" state for tasks — a way to mark which items the user is currently working on, surfaced visibly across the CLI and TUI without changing the underlying schedule/status model.

- **Storage**: an active task is one whose `Tags` slice contains a reserved `active` tag. No new field on `Task`; existing JSON files keep their shape.
- **`ls`**: a leading `*` marker on each active task's row; new `--active` filter flag.
- **`edit`**: tri-state flag `--active=true|false` adds or removes the reserved tag.
- **TUI list rows**: active tasks render in green via the existing `itemDelegate`. Grab styling still wins when a task is being moved.
- **TUI active panel**: a thin panel above the tab bar lists currently-active tasks; auto-hides when none are active.
- **TUI keybinding**: `a` toggles active on the focused row. The existing `a` (add) binding moves to `c` (create) so the letter is free.

## Context (from discovery)

- Files involved:
  - `internal/model/task.go` — Task struct + ID generation.
  - `internal/store/store.go` — `List(ListOptions{Tag: "active"})` already supports tag filtering, no store changes needed.
  - `internal/display/table.go` — `FormatTasks` renders the `ls` table; marker column sits at the start of each row.
  - `cmd/ls.go`, `cmd/edit.go` — cobra commands; mutating commands call `git.AutoCommit`.
  - `internal/tui/model.go` — bubble tea TUI, six tabs, custom `itemDelegate` already handling grab highlight (extends naturally to active highlight). Key handling lives in `Update`/`updateNormal`. `View()` composes header + body + help.
- Patterns observed:
  - All mutations follow: load task → modify in memory → `s.Update(task)` → `git.AutoCommit(repo, msg, taskRelPath)`.
  - `cmd/done.go` is the simplest mutation template.
  - TUI tests use `key(t, m, "x")` helper to drive Update; `runCmd` reflects async save messages back into the model.
- Dependencies: no new ones. Reuses `lipgloss`, `bubbles/list`, existing `git` and `store` packages.

## Development Approach

- **testing approach**: TDD — write the failing test first for each task, then make it pass.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `go test ./...` after each change
- maintain backward compatibility (existing task JSON files have no `active` tag and must continue to load and render unchanged)

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above)
- **e2e tests**: this project has no UI/Playwright e2e suite. The TUI is tested by driving `Model.Update` directly with synthetic key events (see `internal/tui/model_test.go`); treat those as the equivalent.
- For the active panel and green delegate styling, follow the pattern from `TestGrab_DelegateHighlightsGrabbedItemOnly` — force `lipgloss.SetColorProfile(termenv.TrueColor)` so ANSI codes survive in the no-TTY test environment.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

The reserved tag `model.ActiveTag = "active"` is the single source of truth. All read paths (ls, TUI lists, TUI panel) compute "is this task active?" by checking tag membership; all write paths (`edit --active=...`, TUI `a` key) mutate the `Tags` slice and persist via the existing `store.Update` + `git.AutoCommit` flow.

Three small additions on top of the tag:
1. A `Task.IsActive()` helper (and a `SetActive(bool)` mutator) so the rule is in one place and tag de-dup/removal stays consistent.
2. A new `*` marker column in the `ls` table.
3. A green-styled variant in the TUI's existing `itemDelegate` plus a new top panel constructed during `View()`.

**Why the tag approach (vs. a new struct field)**: the user explicitly chose this option. It avoids a Task schema change, makes existing `store.List(ListOptions{Tag: "active"})` immediately useful, and the user accepted that the tag will appear in tag listings alongside others.

**Grab vs. active styling precedence**: when a task is grabbed AND active, the grab style wins (transient action > persistent state). The `itemDelegate.Render` check ordering enforces this.

## Technical Details

### Constant + helpers
```go
// internal/model/task.go
const ActiveTag = "active"

func (t Task) IsActive() bool { /* membership check */ }
func (t *Task) SetActive(on bool) { /* idempotent add/remove, preserves order */ }
```

### Display marker
`FormatTasks` row format gains a 2-rune leading column: `* ` for active, `  ` (two spaces) otherwise. Existing column widths preserved; just shifts the marker to the front.

### `ls --active` filter
A bool flag that post-filters tasks via `IsActive()`. **Auto-lifts the default today-only schedule filter**: when `--active` is set and `--schedule` is not explicitly provided, behave as if `--all` were also set. Otherwise the user activates a "week"-bucket task, runs `ls --active`, and sees nothing — which makes the feature feel broken. Explicitly setting `--schedule` together with `--active` keeps both filters in effect.

### `edit --active=true|false`
Plain `bool` via `BoolVar`; presence detected via `cmd.Flags().Changed("active")` (matches the existing `scheduleArg`/`tags` pattern in `cmd/edit.go`). When changed, pass the value to `task.SetActive`.

### Preserving active state across tag rewrites
Because "active" is a tag, any path that overwrites `task.Tags` wholesale (`edit --tags`, TUI retag modal `t`) would silently drop the active state. Both write paths must preserve it: capture `wasActive := task.IsActive()` before assigning the new tags slice, then call `task.SetActive(wasActive)` after. Removing active stays the responsibility of the dedicated `--active=false` flag and the TUI `a` toggle.

### TUI active panel
- `Model` gains an `activeTasks []model.Task` field, repopulated whenever any tab is reloaded (call from `reloadAll` and from the `taskSavedMsg` handler).
- `View()` checks `len(m.activeTasks)`; when nonzero, renders a bordered panel above the tab bar listing each active task as `<short-id>  <title>` (truncated to fit width).
- When zero, panel is omitted entirely (auto-hide). Layout shifts are acceptable per user choice.
- **List height must compensate for the panel.** The `WindowSizeMsg` handler currently does `listH := msg.Height - 4` (header + help + padding). When the panel is visible, also subtract its rendered height (rows + border lines), recomputed per-render so it adjusts as active tasks come and go without resize events.

### Mark-done implication
Marking a task done implies it's no longer being worked on. Both `cmd/done.go` and the TUI `doneSelected()` will call `task.SetActive(false)` before saving so done tasks don't linger in the active panel. Tests for both paths must assert this.

### Done style precedence
The `itemDelegate` Render decision tree extends to: `grab > active > base`. Done tasks live only in the Done tab, so done+active is impossible after the Mark-done implication above; no extra style branch needed.

### TUI green highlight
`itemDelegate` gains a third `DefaultItemStyles` (`activeStyles`) that overrides `NormalTitle` / `NormalDesc` / `SelectedTitle` / `SelectedDesc` foregrounds to green. `Render` decision tree:
```
if mode == grab && index == m.Index():   grab styles (existing)
else if item.task.IsActive():            active (green) styles
else:                                    base styles
```

### TUI key remap + active toggle
- `updateNormal`: remove the `a` → `openAddModal` mapping, add `c` → `openAddModal`.
- `updateNormal`: add `a` → `toggleActive(currentSelected)`.
- `toggleActive`: load the task by ID, flip via `SetActive`, persist + commit (same pattern as `done`/`reschedule`), emit `taskSavedMsg` with no `focusID`.
- Help line updated: replace `a add` with `c add` and append `a active`.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all code, tests, README updates.
- **Post-Completion** (no checkboxes): manual smoke test of the TUI panel layout in a real terminal — automated tests cover behavior but not every visual.

## Implementation Steps

### Task 1: Active tag constant + Task helpers

**Files:**
- Modify: `internal/model/task.go`
- Create: `internal/model/task_test.go`

- [x] write failing test `TestTask_IsActive` — empty tags, tag present, tag absent among others
- [x] write failing test `TestTask_SetActive` — adding when absent, adding when already present (idempotent), removing when present, removing when absent (no-op), order preservation of other tags
- [x] add `const ActiveTag = "active"` to `internal/model/task.go`
- [x] implement `func (t Task) IsActive() bool`
- [x] implement `func (t *Task) SetActive(on bool)`
- [x] run `go test ./internal/model/` — must pass before next task

### Task 2: `ls` `*` marker for active tasks

**Files:**
- Modify: `internal/display/table.go`
- Modify: `internal/display/table_test.go`

- [x] write failing test `TestFormatTasks_ActiveMarker` — verify a task with `ActiveTag` renders with leading `* `, and a non-active task renders with leading `  `
- [x] update `FormatTasks` row format to prefix each row with `* ` (active) or `  ` (otherwise)
- [x] update any existing table tests whose expected output now starts with the 2-char marker
- [x] run `go test ./internal/display/` — must pass before next task

### Task 3: `ls --active` filter flag

**Files:**
- Modify: `cmd/ls.go`
- Modify: `cmd/ls_test.go`

- [x] write failing test `TestLs_ActiveFlag_LiftsScheduleFilter` — populate three tasks, one active and scheduled for "week"; assert `ls --active` (without `--schedule`) outputs the active week task
- [x] write failing test `TestLs_ActiveFlag_RespectsExplicitSchedule` — when both `--active` and `--schedule today` are passed, only active tasks scheduled for today are returned
- [x] add `--active` bool flag to `newLsCmd`
- [x] when `--active` is set and `--schedule` is not explicitly provided, lift the default today-only filter (treat as `--all` for the schedule axis)
- [x] post-filter `tasks` via `task.IsActive()` after the schedule filter
- [x] run `go test ./cmd/ -run TestLs` — must pass before next task

### Task 4: `edit --active=true|false` + tag-rewrite preservation

**Files:**
- Modify: `cmd/edit.go`
- Modify: `cmd/task_commands_test.go` (or wherever edit is tested)

- [x] write failing test `TestEdit_ActivateAndDeactivate` — start with no active tag; `--active=true` adds it; `--active=false` removes it; flag absent leaves tags unchanged
- [x] write failing test `TestEdit_TagsPreservesActive` — given an active task, `edit --tags "work"` results in tags `["active", "work"]` (or equivalent ordering); the active state survives a tag rewrite
- [x] add `bool` for `--active` flag via `BoolVar`; detect via `cmd.Flags().Changed("active")`
- [x] include `--active` in the "at least one of" required-flag check
- [x] when `--tags` is changed: capture `wasActive := task.IsActive()` before assigning new tags, then `task.SetActive(wasActive)` after
- [x] when `--active` is changed: call `task.SetActive(value)` (after the tags rewrite, if both flags were given, so explicit intent wins)
- [x] commit message reflects the change (e.g., `edit: <title>` is fine; no need for special `active:` prefix)
- [x] run `go test ./cmd/ -run TestEdit` — must pass before next task

### Task 5: TUI — rebind add from `a` to `c`

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] write failing test `TestTUI_CKeyOpensAddModal` — pressing `c` enters `modeAdd`
- [x] write failing test `TestTUI_AKeyDoesNotOpenAddModal` — after pressing `a` in normal mode, model is NOT in `modeAdd` (it's free for the active toggle in the next task)
- [x] update existing add-modal tests (any `key(t, m, "a")` that expects modeAdd) to use `"c"` instead
- [x] in `updateNormal`, swap the `a` → openAdd binding for `c` → openAdd
- [x] update `helpLine()` normal-mode string: `a add` → `c add`
- [x] run `go test ./internal/tui/` — must pass before next task

### Task 6: TUI — `a` key toggles active on focused task; retag preserves active

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] write failing test `TestTUI_AKeyTogglesActive` — focus a task, press `a`, run resulting cmd, verify task on disk has `ActiveTag`; press `a` again, verify tag removed
- [x] write failing test `TestTUI_AKeyNoOpWhenListEmpty` — pressing `a` on empty tab does not panic and emits no save cmd
- [x] write failing test `TestTUI_RetagPreservesActive` — open the retag (`t`) modal on an active task, type a different tag set, save; the `active` tag must still be present on disk
- [x] add `toggleActiveCmd` helper mirroring `doneSelected`'s in-memory copy pattern (copy selected item → SetActive → store.Update → git.AutoCommit → return `taskSavedMsg`)
- [x] in `updateNormal`, bind `a` → `m.toggleActive(currentSelected)`
- [x] in the retag save path (where `t.Tags = sanitizeTags(...)` happens): capture `wasActive := t.IsActive()` before, call `t.SetActive(wasActive)` after
- [x] append `a active` to the normal-mode help line
- [x] run `go test ./internal/tui/` — must pass before next task

### Task 7: TUI — green styling for active tasks in the list

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] write failing test `TestActive_DelegateRendersGreenForActiveItem` — set color profile to TrueColor; render an active row and a non-active row via `itemDelegate.Render`; assert their byte output differs (green codes present in active)
- [x] write failing test `TestActive_GrabStyleWinsOverActiveStyle` — grab an active task; rendered output for the grabbed row matches the grab-mode render, NOT the green-only render
- [x] extend `itemDelegate` with an `activeStyles list.DefaultItemStyles` (green foreground for both selected and unselected variants)
- [x] update `Render` decision tree: grab > active > base
- [x] run `go test ./internal/tui/` — must pass before next task

### Task 8: TUI — active tasks panel above the tab bar

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] write failing test `TestActivePanel_HiddenWhenNoActiveTasks` — `m.View()` output does NOT contain a panel marker (e.g., the panel's border style or label) when zero tasks are active
- [ ] write failing test `TestActivePanel_ShownWithActiveTasks` — after activating a task, `m.View()` contains the active panel and the task's title appears in it
- [ ] write failing test `TestActivePanel_RefreshedAfterToggle` — toggling active off via `a` removes the task from the panel after the save cmd resolves
- [ ] write failing test `TestActivePanel_ShrinksListHeight` — set a fixed window size; verify that when the panel is shown, the list's height is smaller than when it's hidden, by exactly the panel's rendered line count
- [ ] add `activeTasks []model.Task` field to `Model`; populate via a new `reloadActive()` method that calls `m.store.List(ListOptions{Tag: model.ActiveTag})`
- [ ] call `reloadActive()` from `reloadAll()` and from the `taskSavedMsg` handler
- [ ] add `activePanelView()` rendering a bordered panel listing each active task as `<short-id>  <title>`; returns empty string when `len(activeTasks) == 0`
- [ ] add `activePanelHeight()` returning the rendered line count (0 when hidden, otherwise rows + border lines)
- [ ] in the `WindowSizeMsg` handler (and any place that calls `SetSize` on the lists), subtract `activePanelHeight()` from the list height; recompute on every `taskSavedMsg` so the layout adjusts as tasks are activated/deactivated
- [ ] in `View()`, prepend `activePanelView()` to the join when non-empty
- [ ] run `go test ./internal/tui/` — must pass before next task

### Task 9: Auto-deactivate on done

**Files:**
- Modify: `cmd/done.go`
- Modify: `cmd/task_commands_test.go` (or wherever done is tested)
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] write failing test `TestDone_DeactivatesTask` (cmd) — start with active task; after `monolog done <id>`, on-disk task has `Status == "done"` AND `IsActive() == false`
- [ ] write failing test `TestTUI_DKeyOnActiveTaskDeactivates` — focus an active task, press `d`, run save cmd; on disk the task is done and no longer active; active panel no longer contains it
- [ ] in `cmd/done.go`, call `task.SetActive(false)` before `s.Update`
- [ ] in `internal/tui/model.go` `doneSelected` (or equivalent), call `t.SetActive(false)` before saving
- [ ] run `go test ./...` — must pass before next task

### Task 10: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] verify edge cases: task with `active` tag plus other tags renders green and shows other tags in description; `edit --tags` preserves active; retag modal preserves active; `done` removes active
- [ ] run full test suite: `go test ./...`
- [ ] run `go vet ./...`
- [ ] manually open the TUI in a real terminal: confirm panel auto-hides, green color renders, `c` adds, `a` toggles, `*` marker appears in `ls`, list height shrinks when panel appears

### Task 11: Update documentation and finalize

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md` (only if a new pattern was introduced worth documenting)

- [ ] document the active tag, `ls --active`, `edit --active=true|false`, and the TUI `a` toggle in `README.md`
- [ ] note the `c` (create) keybinding change in `README.md`
- [ ] move this plan to `docs/plans/completed/20260413-active-tasks.md`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification:**
- visually confirm green color renders correctly across light and dark terminal themes (lipgloss `AdaptiveColor` should handle this, but worth eyeballing)
- confirm the active panel doesn't cause noticeable layout flicker when toggling on/off in tabs with many tasks
- confirm `*` marker in `ls` doesn't break alignment when piped through `less` or other paginators

**External system updates:** none — this is a self-contained CLI change.
