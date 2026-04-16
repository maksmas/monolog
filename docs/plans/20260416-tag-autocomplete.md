# Tag Autocomplete in TUI

## Overview
- Add inline autocomplete suggestions to the tag input field in the add-task and retag modals
- Solves two problems: users forget which tags already exist, and they make typos typing them manually
- Suggestions appear as a dropdown below the tag field, filtered by prefix match as the user types

## Context (from discovery)
- Files involved: `internal/tui/model.go` (Model struct, add/retag modal logic, view rendering), `internal/tui/model_test.go`, `internal/model/task.go` (CollectTags, SanitizeTags)
- `Model.knownTags []string` is already populated in `openAdd()` via `model.CollectTags()` — this is the data source for suggestions
- The retag modal (`modeRetag`) uses `m.input` (shared textinput) for comma-separated tags but does not populate `knownTags`
- Add modal uses dedicated `m.tagInput` for tags, retag modal uses shared `m.input`
- Both modals parse tags with `model.SanitizeTags()` (comma-split, trim, dedup)
- Suggestion filtering logic (prefix match, exclude already-entered tags) is pure and belongs in `internal/model/`

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy
- **Unit tests in `internal/model/task_test.go`**: pure `FilterTags` function — prefix matching, case insensitivity, exclusion of already-entered tags, empty fragment
- **Unit tests in `internal/tui/model_test.go`**: suggestion appearance on keystroke, Up/Down navigation, Tab/Enter acceptance, Esc clearing, retag modal suggestions

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## Solution Overview
- Add a pure `FilterTags(known []string, field string) []string` function in `internal/model/` that extracts the current fragment (text after last comma), filters known tags by case-insensitive prefix, and excludes tags already present in the field
- Add `suggestions []string` and `suggestionIdx int` to the `Model` struct
- After every keystroke in the tag field, call `FilterTags` to refresh suggestions
- Intercept Up/Down/Tab/Enter/Esc in tag-focused mode when suggestions are visible
- Render suggestions between the tags line and the hint line in the modal, indented to align with the tag value
- Apply the same logic to the retag modal by populating `knownTags` in `openRetag()`

## Technical Details
- **Fragment extraction**: split field value by `,`, take last element, trim whitespace → that's the current fragment
- **Filtering**: case-insensitive `strings.HasPrefix` against `knownTags`, skip tags already in the field (parsed from earlier comma segments)
- **Max suggestions**: 5 (cap the slice after filtering)
- **Accepting a suggestion**: replace everything after the last comma with the selected tag + `", "`, reset `suggestionIdx` to -1, recompute suggestions (which will be empty since fragment is now empty)
- **Suggestion rendering**: 7-char indent (`"       "`), `>` marker + bold for highlighted item, dim for others
- **Both modals share** the same `suggestions`/`suggestionIdx` state on Model since only one modal is open at a time

## Implementation Steps

### Task 1: Add `FilterTags` function in model package

**Files:**
- Modify: `internal/model/task.go`

- [ ] Add `FilterTags(known []string, field string) []string` — extracts fragment after last comma, filters `known` by case-insensitive prefix match, excludes tags already in the field, caps at 5 results
- [ ] Write tests for `FilterTags`: prefix matching, case insensitivity, excludes entered tags, empty fragment returns nil, caps at 5
- [ ] Run tests: `go test ./internal/model/`

### Task 2: Add suggestion state and update logic in add modal

**Files:**
- Modify: `internal/tui/model.go`

- [ ] Add `suggestions []string` and `suggestionIdx int` to `Model` struct
- [ ] In `openAdd()`: clear suggestions, set `suggestionIdx = -1`
- [ ] In `closeModal()`: clear suggestions, reset `suggestionIdx`
- [ ] In `updateAdd()` when tag field is focused: after forwarding keystroke to `tagInput`, call `model.FilterTags(m.knownTags, m.tagInput.Value())` to refresh `m.suggestions`, reset `suggestionIdx` to -1 (or 0 if suggestions non-empty)
- [ ] Intercept Up/Down in `updateAdd()` when suggestions are visible: move `suggestionIdx` within bounds, do not forward to textinput
- [ ] Intercept Tab in `updateAdd()`: if suggestions visible and `suggestionIdx >= 0`, accept suggestion (replace fragment, append `", "`); otherwise fall through to existing focus-switch behavior
- [ ] Intercept Enter in `updateAdd()`: if suggestions visible and `suggestionIdx >= 0`, accept suggestion instead of submitting; otherwise submit as today
- [ ] Intercept Esc in `updateAdd()`: if suggestions visible, clear them and return; otherwise close modal as today
- [ ] Write tests: suggestions appear when typing prefix, Up/Down navigates, Tab accepts, Enter accepts vs submits, Esc clears vs closes
- [ ] Run tests: `go test ./internal/tui/`

### Task 3: Render suggestions in the add modal view

**Files:**
- Modify: `internal/tui/model.go`

- [ ] In `modalView()` `modeAdd` case: if `m.suggestions` is non-empty, render suggestion lines between the tags line and the hint line
- [ ] Each suggestion indented 7 chars; highlighted one prefixed with `> ` and styled bold, others prefixed with `  ` and styled dim
- [ ] Write test: `modalView` in modeAdd renders suggestion lines when suggestions are set, renders no extra lines when empty
- [ ] Run tests: `go test ./internal/tui/`

### Task 4: Add autocomplete to retag modal

**Files:**
- Modify: `internal/tui/model.go`

- [ ] In `openRetag()`: populate `m.knownTags` using `model.CollectTags()` (same pattern as `openAdd()`), clear suggestions
- [ ] In `updateRetag()`: after forwarding keystroke to `m.input`, call `model.FilterTags(m.knownTags, m.input.Value())` to refresh suggestions
- [ ] Intercept Up/Down/Tab/Enter/Esc in `updateRetag()` with same suggestion logic as add modal
- [ ] In `modalView()` `modeRetag` case: render suggestions below the tag input line, same styling
- [ ] Write tests: retag modal shows suggestions, accepts them, clears on Esc
- [ ] Run tests: `go test ./internal/tui/`

### Task 5: Verify acceptance criteria

- [ ] Run full test suite: `go test ./...`
- [ ] Run vet: `go vet ./...`
- [ ] Verify all add-modal tests still pass (no regressions)
- [ ] Verify retag-modal tests still pass (no regressions)

### Task 6: [Final] Update documentation
- [ ] Update CLAUDE.md if new patterns discovered
- [ ] Move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- Launch `monolog` TUI, press `c`, type tags — verify suggestions appear as you type
- Verify Up/Down navigates suggestions, Tab/Enter accepts, Esc dismisses
- Verify accepted tag replaces fragment and appends `", "`
- Verify already-entered tags are excluded from suggestions
- Verify no suggestions appear when fragment is empty or no matches
- Press `t` on a task to retag — verify same autocomplete works
- Verify existing auto-tag-from-title-prefix still works alongside autocomplete
