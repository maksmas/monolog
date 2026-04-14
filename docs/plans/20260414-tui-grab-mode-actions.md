# Enable All Normal-Mode Actions During Grab Mode

## Overview
- In TUI grab mode, action keys (e/r/t/d/a/c/x/s) are currently ignored — the user must drop the task first, re-select it, and then act
- This change makes all normal-mode actions available during grab: pressing an action key commits the grab (saves position) and then opens the action
- Seamless workflow: grab → reposition → press `e` → position saved, editor opens

## Context (from discovery)
- `internal/tui/model.go` — all TUI logic lives here; `updateGrab` (line 907) handles grab-mode keys
- `updateNormal` (line 408) has the full set of action handlers: `d`/`e`/`r`/`t`/`c`/`x`/`a`/`s`
- `commitGrab()` (line 1019) persists position + schedule/status and returns a `taskSavedMsg`
- `commitGrab` currently does NOT set `focusID` in the returned message — after reload the cursor may land on the wrong task if it moved tabs
- `taskSavedMsg` handler (line 377) reloads all data and optionally focuses a task by ID

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change

## Testing Strategy
- **Unit tests**: required for every task (existing test patterns in `model_test.go`)
- Test pattern: `newTestModel` creates a real git-backed store; send key messages via `m.Update(tea.KeyMsg{...})`

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Solution Overview

**Mechanism**: Add a `pendingAction` callback field to Model. When an action key is pressed in grab mode:
1. Store the action as `pendingAction`
2. Call `commitGrab()` to persist the position (returns async `taskSavedMsg`)
3. When `taskSavedMsg` arrives successfully, dispatch the pending action

**Why a callback field instead of tea.Sequence**: `openEdit` uses `tea.ExecProcess` which suspends the TUI — it needs the model fully reloaded first. A pending action field lets us dispatch after reload + focus, which `tea.Sequence` cannot guarantee since it doesn't wait for `Update` processing.

**focusID fix**: `commitGrab` needs to return the task's ID in `taskSavedMsg.focusID` so after reload the cursor lands on the committed task. Without this, actions like `openEdit` would operate on the wrong task.

**Actions enabled in grab mode**: `d` (done), `e` (edit), `r` (reschedule), `t` (retag), `c` (add), `x` (delete), `a` (active), `s` (sync). Keys `q`/`Esc` keep their current grab-mode behavior (cancel). Tab-jump keys (`1-6`) stay excluded since `left`/`right` already move the task across tabs.

## Implementation Steps

### Task 1: Add pendingAction field and dispatch logic

**Files:**
- Modify: `internal/tui/model.go`

- [ ] Add `pendingAction func() tea.Cmd` field to the `Model` struct (after `grabIndex`)
- [ ] In `taskSavedMsg` handler (inside `Update`), after reload + focus, check `pendingAction`: if non-nil, clear it and return `m, action()`; if the msg has an error, clear `pendingAction` without dispatching
- [ ] In `commitGrab()`, include the task ID as `focusID` in the returned `taskSavedMsg`
- [ ] In `closeModal()`, clear `pendingAction` to nil (safety — modal cancel shouldn't leave a stale action)
- [ ] Write test: grab a task, verify `pendingAction` is nil by default
- [ ] Write test: set `pendingAction`, simulate successful `taskSavedMsg`, verify action dispatched and `pendingAction` cleared
- [ ] Write test: set `pendingAction`, simulate error `taskSavedMsg`, verify action NOT dispatched and `pendingAction` cleared
- [ ] Run tests — must pass before task 2

### Task 2: Wire action keys in updateGrab

**Files:**
- Modify: `internal/tui/model.go`

- [ ] In `updateGrab`, add a switch branch after the existing `g`/`G` handling for keys: `e`, `r`, `t`, `d`, `a`, `c`, `x`, `s`
- [ ] For each key, set `m.pendingAction` to the corresponding action function and return `m, m.commitGrab()`
- [ ] Verify that `commitGrab()` resets `m.mode` and `m.grabTask` before the async cmd runs (already does — no change needed)
- [ ] Write test: in grab mode, press `e` — verify mode returns to normal, `pendingAction` is set, and commitGrab cmd is returned
- [ ] Write test: in grab mode, press `r` — verify mode returns to normal and after taskSavedMsg the reschedule modal opens
- [ ] Write test: in grab mode, press `t` — verify retag modal opens after commit
- [ ] Write test: in grab mode, press `d` — verify task is marked done after commit
- [ ] Write test: in grab mode, press `a` — verify active toggle fires after commit
- [ ] Run tests — must pass before task 3

### Task 3: Update help line

**Files:**
- Modify: `internal/tui/model.go`

- [ ] Update `helpLine()` grab-mode text to include the available actions alongside the existing grab controls
- [ ] Keep it concise — e.g. `"GRAB  ↑/↓ reorder  ←/→ bucket  g/G top/bottom  enter drop  esc cancel  +d/e/r/t/a/c/x/s"`
- [ ] Write test: verify `helpLine()` in grab mode contains the action key hints
- [ ] Run tests — must pass before task 4

### Task 4: Verify acceptance criteria
- [ ] Verify: pressing `e` in grab mode saves position and opens editor
- [ ] Verify: pressing `r` in grab mode saves position and opens reschedule modal
- [ ] Verify: pressing `t` in grab mode saves position and opens retag modal
- [ ] Verify: pressing `d`/`a`/`c`/`x`/`s` all work in grab mode
- [ ] Verify: `Esc` still cancels grab without saving
- [ ] Verify: grab mode after cross-tab move + action key correctly saves to new tab
- [ ] Run full test suite: `go test ./...`
- [ ] Run lint: `go vet ./...`

### Task 5: [Final] Update documentation
- [ ] Update CLAUDE.md if new patterns discovered
- [ ] Move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification**:
- Launch TUI, grab a task, reposition it, press `e` — verify editor opens with the task at its new position
- Grab a task, move it to another tab with `→`, press `r` — verify reschedule modal opens and the task was saved in the new tab
- Grab a task, press `Esc` — verify nothing is saved (existing behavior preserved)
