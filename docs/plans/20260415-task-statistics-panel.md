# Task Statistics Panel

## Overview

Add a compact stats bar to the TUI that surfaces at-a-glance backlog health metrics. The bar appears as a single line always visible above the tab bar (and below the active panel in schedule view). In tag view it also shows the count of done tasks carrying the current tag.

Stats displayed:
- **Total / Open / Done**: global task counts
- **Tab**: open tasks in the current tab
- **Avg open**: mean age in days of currently open tasks
- **Avg done**: mean time-to-completion in days for done tasks (requires `CompletedAt` field)
- **Tag done** (tag view only): done tasks bearing the active tag

## Context (from discovery)

- Files involved: `internal/model/task.go`, `internal/tui/model.go`, new `internal/model/stats.go`
- Relevant patterns:
  - `activePanelView()` / `activePanelHeight()` mirror pattern — stats bar needs the same view+height pair
  - `recomputeLayout()` subtracts `activePanelHeight()` from list height; must also subtract `statsBarHeight()`
  - `rebuildForTagView()` already loads all tasks via `store.List(ListOptions{})` — can piggyback `allTasks` cache there
  - `doneSelected()` sets `t.Status = "done"` and `t.UpdatedAt = now()` — add `t.CompletedAt = now()` here
- Dependencies: no new third-party packages needed

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Testing Strategy

- **unit tests**: required for every task
- Stats computation is pure (`[]Task → Stats`) — easy to unit test with table-driven cases
- TUI rendering tests: update `model_test.go` to cover stats bar presence in both view modes

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Solution Overview

1. Add `CompletedAt string` to `model.Task`; populate it in the TUI's `doneSelected()`.
2. Add `Stats` struct + `ComputeStats([]Task, time.Time) Stats` pure function in `internal/model`.
3. Cache `allTasks []model.Task` in the TUI `Model`; recompute after every mutation and view rebuild.
4. Add `statsBarView() string` + `statsBarHeight() int` methods; wire them into `View()` and `recomputeLayout()`.

## Technical Details

### Stats struct (internal/model/stats.go)

```go
type Stats struct {
    Total              int
    Open               int
    Done               int
    AvgDaysOpen        float64 // mean age of open tasks; 0 if none
    AvgDaysToComplete  float64 // mean (CompletedAt-CreatedAt); 0 if no done tasks with CompletedAt
}
```

### Stats bar format

```
Schedule view:  45 tasks  32 open  13 done  8 in tab  ~4d open  ~12d done
Tag view:       45 tasks  32 open  13 done  5 tag-done  8 in tab  ~4d open  ~12d done
```

- Avg fields omitted when there is no data (no open tasks / no done tasks with CompletedAt).
- Duration formatting: `Xh` (< 1d) | `Xd` | `Xw` (≥14d) — keep it terse.
- The line is dim-styled to visually separate it from the tab bar.

### Layout change

`recomputeLayout()` currently:
```
listH := m.height - 4 - m.activePanelHeight()
```
After this plan:
```
listH := m.height - 4 - m.activePanelHeight() - m.statsBarHeight()
```

`statsBarHeight()` always returns 1 (the bar is always shown).

### allTasks cache strategy

- Add `allTasks []model.Task` field to `Model`.
- Populate it inside `rebuildForTagView()` (already has the full list) and a new `reloadAllTasks()` helper called from `rebuildForScheduleView()` and after every `taskSavedMsg`.
- `ComputeStats` is called with `m.allTasks` and `time.Now()` to produce `m.stats`.

## What Goes Where

- **Implementation Steps**: all code changes, tests, layout wiring
- **Post-Completion**: manual visual testing in both view modes

## Implementation Steps

### Task 1: Add CompletedAt to Task struct and populate on completion

**Files:**
- Modify: `internal/model/task.go`
- Modify: `internal/tui/model.go` (doneSelected function)
- Modify: `internal/model/task_test.go` (if it exists) or create tests

- [ ] Add `CompletedAt string \`json:"completed_at,omitempty"\`` field to `model.Task` after `UpdatedAt`
- [ ] In `doneSelected()` (`model.go:~968`), add `t.CompletedAt = now()` alongside `t.UpdatedAt = now()`
- [ ] Write tests verifying `CompletedAt` is set when a task is marked done and empty otherwise
- [ ] Run tests: `go test ./...` — must pass before Task 2

### Task 2: Add Stats struct and ComputeStats function

**Files:**
- Create: `internal/model/stats.go`
- Create: `internal/model/stats_test.go`

- [ ] Create `Stats` struct with fields: `Total, Open, Done int`, `AvgDaysOpen, AvgDaysToComplete float64`
- [ ] Implement `ComputeStats(tasks []Task, now time.Time) Stats`:
  - Count total, open (status != "done"), done (status == "done")
  - AvgDaysOpen: mean of `now - CreatedAt` for all open tasks (parse CreatedAt as RFC3339)
  - AvgDaysToComplete: mean of `CompletedAt - CreatedAt` for done tasks that have CompletedAt set
- [ ] Implement `FormatDuration(days float64) string`: `"Xh"` (< 1d) | `"Xd"` | `"Xw"` (≥14d rounds to weeks)
- [ ] Write table-driven tests for `ComputeStats`: empty list, only open, only done, mixed, avg calculations
- [ ] Write tests for `FormatDuration` edge cases (0h, 6h, 23h, 1d, 13d, 14d, 100d)
- [ ] Run tests: `go test ./internal/model/...` — must pass before Task 3

### Task 3: Cache allTasks in TUI Model and compute stats on every rebuild

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] Add fields `allTasks []model.Task` and `stats model.Stats` to `Model` struct (after `activeTasks`)
- [ ] Add `reloadAllTasks() error` helper: calls `m.store.List(store.ListOptions{})`, stores result in `m.allTasks`, calls `m.stats = model.ComputeStats(m.allTasks, time.Now())`
- [ ] In `rebuildForTagView()`: capture the existing `allTasks` local var into `m.allTasks` then call `m.stats = model.ComputeStats(m.allTasks, time.Now())` (avoids a second store call)
- [ ] In `rebuildForScheduleView()`: call `m.reloadAllTasks()` after rebuilding lists
- [ ] In `taskSavedMsg` handler (where `reloadActive()` is called): also call `m.reloadAllTasks()`
- [ ] Write tests verifying stats are populated after a rebuild
- [ ] Run tests: `go test ./internal/tui/...` — must pass before Task 4

### Task 4: Add statsBarView and wire into layout

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] Add `statsBarHeight() int` — always returns 1
- [ ] Add `statsBarView() string`:
  - Schedule view: `"  45 tasks  32 open  13 done  8 in tab  ~4d open  ~12d done"`
  - Tag view: prepend `"N tag-done  "` before tab count; derive tag-done from `m.allTasks` filtered by current tag and `status == "done"`
  - Omit avg fields when `AvgDaysOpen == 0` / `AvgDaysToComplete == 0`
  - Style with `lipgloss` dim color consistent with existing help bar
- [ ] Update `recomputeLayout()`: change `m.height - 4 - m.activePanelHeight()` to subtract `m.statsBarHeight()` as well
- [ ] Update `View()`: insert `m.statsBarView()` between active panel (if any) and tab bar header
- [ ] Write tests for `statsBarView()` output in schedule view and tag view (check key substrings present)
- [ ] Run tests: `go test ./...` — must pass

### Task 5: Verify acceptance criteria

- [ ] All stats appear correctly in schedule view (with and without active tasks)
- [ ] All stats appear correctly in tag view (including tag-done count)
- [ ] Stats update after marking a task done, adding, or deleting a task
- [ ] `CompletedAt` is persisted in the JSON file when a task is marked done
- [ ] AvgDaysToComplete omitted until at least one done task has `CompletedAt` set
- [ ] Layout does not overflow: list height reduced by 1 for the stats bar
- [ ] Run full test suite: `go test ./...`

### Task 6: [Final] Update documentation

- [ ] Update `CLAUDE.md` if new patterns were introduced (stats caching, `CompletedAt` lifecycle)
- [ ] Move this plan to `docs/plans/completed/`

## Post-Completion

**Manual visual verification:**
- Run `monolog` in a terminal and confirm stats bar visible in both view modes
- Mark a task done and confirm counts update immediately
- Narrow the terminal window and confirm stats bar doesn't crash (may truncate gracefully)
- Confirm avg fields absent when no completed tasks have `CompletedAt` (fresh install / old data)
