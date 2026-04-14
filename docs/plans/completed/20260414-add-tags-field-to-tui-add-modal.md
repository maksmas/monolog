# Add Tags Field to TUI Add Modal

## Overview
- Add a tags text input to the interactive task creation modal so users can specify tags at creation time
- Currently the add modal (`c` key) only accepts a title — tags must be added after creation via the retag modal (`t` key), which is a friction point
- Uses two side-by-side fields (title + tags) on a single screen with Tab to switch focus, consistent with the existing retag UX for comma-separated tags

## Context (from discovery)
- Files involved: `internal/tui/model.go` (modal logic, view, createCmd), `internal/tui/model_test.go`
- The `Model` struct uses a single shared `textinput.Model` for all modals — this change adds a second `textinput.Model` dedicated to the tags field in the add modal
- `sanitizeTags()` already exists in `model.go:1299` and handles comma-separated parsing
- `createCmd()` at `model.go:1026` builds the task but never sets `Tags` — needs to accept and apply tags
- The retag modal already demonstrates the tag-editing pattern (placeholder, sanitize, visibleTags)

## Development Approach
- **Testing approach**: TDD (tests first)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy
- **Unit tests**: required for every task (see Development Approach above)
- Key scenarios to cover:
  - Tab routes subsequent typing to the correct field (test observable behavior, not internal state)
  - Enter on either field creates the task with both title and tags
  - Empty tags field creates task with no tags (existing behavior preserved)
  - Esc cancels from either field
  - Tags are sanitized (trimmed, empties dropped) via `sanitizeTags`
  - `createCmd` passes tags through to the created task
  - Add from Done tab with tags — task lands in Today with correct tags
  - `modalView` renders both field labels

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## Solution Overview
- Add a second `textinput.Model` (`tagInput`) to the `Model` struct, used only during `modeAdd`
- Track which field has focus via an `addFocus` field using named constants (`addFocusTitle`, `addFocusTags`)
- Tab key toggles focus between the two inputs
- Enter from either field triggers task creation with both values
- Esc from either field cancels the modal (consistent with other modals)
- `createCmd` gains a `tags []string` parameter
- Modal view shows both fields with labels, active field visually indicated by the blinking cursor

## Technical Details
- New fields on `Model`: `tagInput textinput.Model`, `addFocus addField`
- Named constants: `const (addFocusTitle addField = iota; addFocusTags)` — mirrors the existing `mode` pattern
- `openAdd()` resets both inputs, focuses title (addFocus=addFocusTitle)
- `updateAdd()` intercepts Tab **before** forwarding to `textinput.Update` to prevent the input from consuming it; then handles Enter (create with title+tags) and routes remaining keys to the focused input only
- On Enter in `updateAdd`: calls `sanitizeTags(m.tagInput.Value())` to parse the raw comma-separated string, then passes the result to `createCmd(title, tags)` — mirrors the pattern in `updateRetag` (line 568)
- `closeModal()` updated to also call `m.tagInput.Blur()` and `m.tagInput.SetValue("")`, and reset `addFocus`
- `createCmd(title string)` → `createCmd(title string, tags []string)`
- `modalView()` for `modeAdd` renders both fields with labels
- Help line for `modeAdd` updated to show `tab switch field  enter save  esc cancel`

## Implementation Steps

### Task 1: Write tests for two-field add modal behavior

**Files:**
- Modify: `internal/tui/model_test.go`

- [x] Write test: Tab in add modal routes subsequent typing to the tags field (verify created task has tags, not internal state)
- [x] Write test: Enter with title and tags creates task with both values (type title, Tab, type "foo, bar", Enter → task.Tags == ["foo", "bar"])
- [x] Write test: Enter with title and empty tags creates task with no tags (backward compat)
- [x] Write test: Tags with extra whitespace/commas are sanitized (e.g. " a , , b " → ["a", "b"])
- [x] Write test: Esc cancels from tags field (same as title field)
- [x] Write test: Add from Done tab with tags — task lands in Today with correct tags
- [x] Write test: `modalView` in modeAdd contains both "Title:" and "Tags:" labels
- [x] Run tests — expected to fail (TDD red phase)

### Task 2: Add `tagInput` and `addFocus` fields to Model

**Files:**
- Modify: `internal/tui/model.go`

- [x] Define `addField` type and constants (`addFocusTitle`, `addFocusTags`)
- [x] Add `tagInput textinput.Model` and `addFocus addField` fields to `Model` struct
- [x] Initialize `tagInput` in `newModel` (placeholder "tag1, tag2", CharLimit 512)
- [x] Update `closeModal()` to call `m.tagInput.Blur()`, `m.tagInput.SetValue("")`, and reset `addFocus`
- [x] Update `openAdd()` to reset both inputs, set `addFocus = addFocusTitle`, focus title input
- [x] Run tests — Task 1 tests expected to still fail (fields exist but behavior not yet wired)

### Task 3: Update `updateAdd` to handle Tab and pass tags to `createCmd`

**Files:**
- Modify: `internal/tui/model.go`

- [x] Intercept `tea.KeyTab` in `updateAdd` **before** forwarding to `textinput.Update` — toggle `addFocus`, call Focus/Blur on the appropriate input
- [x] On Enter: read title from `input`, call `sanitizeTags(m.tagInput.Value())` to parse tags, then call `createCmd(title, tags)`
- [x] Update `createCmd` signature to accept `tags []string` and set `task.Tags`
- [x] Route remaining key events to focused input only; unfocused input must not receive updates
- [x] Run tests — Task 1 tests should now pass (TDD green phase)

### Task 4: Update modal view and help line

**Files:**
- Modify: `internal/tui/model.go`

- [x] Update `modalView()` `modeAdd` case to render both fields with labels ("Title:" and "Tags:")
- [x] Update `helpLine()` for `modeAdd` to show `tab switch field  enter save  esc cancel`
- [x] Run tests — Task 1 modalView test should now pass

### Task 5: Verify acceptance criteria

- [x] Verify: creating task with tags in TUI populates `Tags` field in JSON
- [x] Verify: creating task without tags preserves existing behavior (no tags in JSON)
- [x] Verify: Tab switches between fields
- [x] Verify: Esc cancels from either field
- [x] Run full test suite: `go test ./...`
- [x] Run vet: `go vet ./...`

### Task 6: [Final] Update documentation
- [x] Update CLAUDE.md if new patterns discovered (no new patterns — tags field follows existing modal/sanitize patterns)
- [x] Move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- Launch `monolog` TUI, press `c`, verify both fields appear
- Type a title, Tab to tags, type comma-separated tags, Enter — verify task created with tags
- Press `c`, type title only, Enter — verify task created without tags
- Press `c`, Tab, Esc — verify modal closes cleanly
