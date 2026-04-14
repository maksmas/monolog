# TUI Tag View Mode

## Overview
- Add a tag-based view mode to the TUI as an alternative to the current schedule-based view
- In tag view, tabs become tags (alphabetically ordered), each showing tasks with that tag grouped by schedule bucket
- Active tag gets a dedicated first tab; untagged tasks get a special last tab
- Toggle between schedule view and tag view with `v` key; also available via `--tags` CLI flag
- Tasks can be reordered within a tag tab (up/down changes priority/bucket) but cannot be moved across tag tabs (no left/right grab)
- Reordering in the active tab updates the global Position field, keeping the active-tasks panel in schedule view in sync

## Context (from discovery)
- Files/components involved: `internal/tui/model.go` (main TUI model, ~1558 lines), `internal/tui/tui.go` (styles), `internal/model/task.go` (Task struct, `CollectTags`), `main.go` + `cmd/` (CLI flag)
- Related patterns: tabs defined as `[]tab` slice, `reloadTab` filters by bucket/status, `reloadAll` iterates all tabs, grab mode uses `moveGrabWithin`/`moveGrabAcross`/`commitGrab`, `activePanelView` renders active tasks above tab bar
- Dependencies: `schedule` package for bucket classification, `model.CollectTags` for tag extraction, `store.ListOptions` supports tag filtering

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility — schedule view remains the default

## Testing Strategy
- **Unit tests**: required for every task (see Development Approach above)
- Test tag tab generation (ordering, active-first, untagged-last)
- Test tag view reload logic (filtering by tag, bucket grouping, sort order)
- Test view mode toggling preserves state correctly
- Test grab mode restrictions in tag view (no cross-tab movement)
- Test bucket separator rendering

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## Solution Overview

### Dual-mode model approach
Add a `viewMode` enum (`viewSchedule` / `viewTag`) to the existing `Model`. When the user presses `v` (or launches with `--tags`), the model:
1. Sets `viewMode = viewTag`
2. Rebuilds `m.tabs` from the current set of tags: `[active, alpha-sorted-tags..., untagged]`
3. Rebuilds `m.lists` to match the new tab count
4. Calls `reloadAll()` which now branches on `viewMode`

In tag view, `reloadTab` filters tasks by tag (or by "has no tags" for the untagged tab) and sorts them by bucket order then by position within each bucket. Bucket names are rendered as non-selectable separator items in the list.

### Key design decisions
- **Tabs are dynamic in tag view**: rebuilt on every `reloadAll` to reflect tag changes (add/retag)
- **Bucket separators**: rendered as special `item` entries with a distinct style (dimmed, no selection highlight). The list delegate skips them for actions.
- **Grab mode in tag view**: `moveGrabAcross` (left/right) is disabled. Only `moveGrabWithin` (up/down) works, allowing priority reorder and cross-bucket movement within the same tag. `commitGrab` derives the new schedule from the bucket separator the task lands under.
- **Active tab sync**: reordering in the active tab writes to the task's global `Position`, so the active panel in schedule view reflects the same order.
- **Active panel hidden in tag view**: `activePanelView` returns empty string when `viewMode == viewTag`.
- **`v` toggle preserves cursor**: when switching modes, the currently selected task ID is remembered and refocused in the new view if present.

## Technical Details

### New types and fields on Model
```go
type viewMode int
const (
    viewSchedule viewMode = iota
    viewTag
)

// On Model:
viewMode    viewMode
tagTabs     []tagTab    // populated when viewMode == viewTag
```

### tagTab struct
```go
type tagTab struct {
    label string // display label (tag name, or "Active", or "Untagged")
    tag   string // tag to filter by; empty string means "untagged"
    isActive  bool // true for the active tab (filters by model.ActiveTag)
    isUntagged bool // true for the special untagged tab
}
```

### Bucket separator item
A special `item` variant where `task.ID` is empty and `task.Title` holds the bucket label (e.g. "── Today ──"). The delegate renders these as dimmed, unselectable separators. `selectedTask()` skips separators. Navigation (j/k/up/down) skips over separators.

### Tag view reload flow
1. `reloadTagTabs()` — scans all tasks, builds tag list, creates tagTabs slice
2. `reloadTab(idx)` in tag mode — filters tasks by tag, groups by bucket, inserts separator items, sorts by bucket order then position
3. Bucket order: today → tomorrow → week → month → someday (same as `reschedulePresets`)

### Grab mode in tag view
- `moveGrabWithin`: works as-is (swaps adjacent items), but must skip separator items
- `moveGrabAcross`: no-op in tag view (return early)
- `commitGrab`: in tag view, derives the task's new schedule from whichever bucket separator is above the task's final position in the list. If the task moved across a bucket boundary, its schedule is updated accordingly.

### CLI flag
- Add `--tags` / `-T` flag to root command, passed through to `tui.Run` as an option
- When set, the TUI starts in tag view mode

## Implementation Steps

### Task 1: Add viewMode field and tag tab types

**Files:**
- Modify: `internal/tui/model.go`

- [x] Add `viewMode` type (`viewSchedule`/`viewTag`) and constants
- [x] Add `tagTab` struct with `label`, `tag`, `isActive`, `isUntagged` fields
- [x] Add `viewMode` and `tagTabs []tagTab` fields to `Model`
- [x] Write tests for viewMode type (basic sanity — default is viewSchedule)
- [x] Run tests — must pass before next task

### Task 2: Implement tag tab generation

**Files:**
- Modify: `internal/tui/model.go`

- [x] Add `buildTagTabs(tasks []model.Task) []tagTab` method — collects all tags, sorts alphabetically, prepends active tab, appends untagged tab
- [x] Handle edge case: no tags exist → only active + untagged tabs
- [x] Handle edge case: no tasks have the active tag → active tab still present (just empty)
- [x] Write tests for `buildTagTabs` — verify ordering (active first, alpha middle, untagged last)
- [x] Write tests for edge cases (no tags, all tasks untagged, tasks with multiple tags)
- [x] Run tests — must pass before next task

### Task 3: Implement bucket separator items

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/tui.go`

- [x] Define a sentinel for separator items (e.g. `task.ID == ""` or a dedicated `isSeparator` field on `item`)
- [x] Add separator rendering in `itemDelegate.Render` — dimmed style, no selection highlight, full-width label like "── Today ──"
- [x] Add separator style to `tui.go`
- [x] Make `selectedTask()` skip separator items (return nil if separator is selected)
- [x] Write tests for separator item identification
- [x] Write tests for `selectedTask` returning nil on separator
- [x] Run tests — must pass before next task

### Task 4: Implement tag view reload logic

**Files:**
- Modify: `internal/tui/model.go`

- [x] Add `rebuildForTagView()` method — calls `buildTagTabs`, rebuilds `m.tabs` (converting tagTabs to generic tabs), recreates `m.lists` with correct count, calls `reloadAll()`
- [x] Add `rebuildForScheduleView()` method — restores `defaultTabs`, recreates lists, calls `reloadAll()`
- [x] Modify `reloadTab(idx)` to branch on `viewMode`: in tag mode, filter tasks by tag, group by bucket, insert separators, sort by bucket order then position
- [x] For the "untagged" tab: filter to tasks where `len(display.VisibleTags(task.Tags)) == 0`
- [x] For the "active" tab: filter to tasks with `ActiveTag`
- [x] Bucket grouping: iterate buckets in order (today/tomorrow/week/month/someday), collect tasks per bucket sorted by position, insert separator + tasks
- [x] Hide active panel in tag view: modify `activePanelView()` to return `""` when `viewMode == viewTag`
- [x] Write tests for tag view reload — verify tasks appear under correct tags
- [x] Write tests for bucket grouping and separator insertion order
- [x] Write tests for untagged tab filtering
- [x] Write tests for active panel hidden in tag view
- [x] Run tests — must pass before next task

### Task 5: Implement view mode toggle (`v` key)

**Files:**
- Modify: `internal/tui/model.go`

- [x] Add `v` key handler in `updateNormal` — toggles `viewMode`, remembers current selected task ID, calls `rebuildForTagView()`/`rebuildForScheduleView()`, refocuses task by ID
- [x] Ensure tab bar rendering in `View()` works with dynamic tabs (it already uses `m.tabs` — should be fine)
- [x] Update `recomputeLayout()` to account for hidden active panel in tag view
- [x] Update help line to show `v` key hint in both modes
- [x] Write tests for view mode toggle (schedule → tag → schedule round-trip)
- [x] Write tests for cursor preservation across toggle
- [x] Run tests — must pass before next task

### Task 6: Adapt grab mode for tag view

**Files:**
- Modify: `internal/tui/model.go`

- [x] In `updateGrab`, when `viewMode == viewTag`: disable `left`/`right` keys (no cross-tab movement)
- [x] In `moveGrabWithin`: skip separator items — if the target index is a separator, continue in the same direction to the next non-separator item
- [x] In `commitGrab` for tag view: derive the task's new schedule bucket from the nearest separator above the task's final position. Use `schedule.Parse` to convert bucket name to ISO date
- [x] Update grab mode help line for tag view (no ←/→ bucket hint)
- [x] Write tests for grab mode skipping separators
- [x] Write tests for `commitGrab` deriving correct schedule from separator position
- [x] Write tests for left/right keys being no-op in tag view grab mode
- [x] Run tests — must pass before next task

### Task 7: Add `--tags` CLI flag

**Files:**
- Modify: `internal/tui/tui.go`
- Modify: `main.go` or `cmd/root.go` (whichever holds the root command)

- [x] Add `Options` struct to `tui` package (or add `startInTagView bool` parameter to `Run`)
- [x] In `newModel`, if `startInTagView` is true, call `rebuildForTagView()` after initial load
- [x] Add `--tags` / `-T` persistent flag to root command, pass value to `tui.Run`
- [x] Write tests for `newModel` with tag view start option
- [x] Run tests — must pass before next task

### Task 8: Verify acceptance criteria

- [x] Verify tag view shows tags as tabs (active first, alpha middle, untagged last)
- [x] Verify tasks with multiple tags appear in all relevant tabs
- [x] Verify tasks with no tags appear only in untagged tab
- [x] Verify bucket separators display correctly within tag tabs
- [x] Verify task ordering within tag tabs: by bucket, then by position
- [x] Verify grab mode up/down works, skips separators, derives correct schedule
- [x] Verify grab mode left/right is disabled in tag view
- [x] Verify reordering in active tab updates global Position
- [x] Verify `v` key toggles between views with cursor preservation
- [x] Verify `--tags` flag starts TUI in tag view
- [x] Verify active panel hidden in tag view, visible in schedule view
- [x] Run full test suite: `go test ./...`
- [x] Run vet: `go vet ./...`

### Task 9: [Final] Update documentation

- [x] Update CLAUDE.md if new patterns discovered
- [x] Move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- Launch TUI in schedule view, press `v` to switch to tag view, verify tabs
- Create tasks with various tag combinations, verify they appear in correct tabs
- Grab-move a task within a tag tab across bucket boundaries, verify schedule updates
- Reorder tasks in the active tab, switch to schedule view, verify active panel order matches
- Launch with `monolog --tags`, verify starts in tag view
- Test with no tasks, single task, tasks with no tags
