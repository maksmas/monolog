# Auto-tag from title prefix

## Overview
- When creating a task, if the title starts with `"tagname: ..."`, automatically add `tagname` as a tag — but only if that tag already exists on another task
- This provides a quick shorthand for tagging without using `--tags` or the tags input field
- Requires a tag collection mechanism (listing all unique tags across tasks), which will also be useful for future search/filter/autocomplete features
- Title is kept as-is (prefix is NOT stripped)

## Context (from discovery)
- Two task creation paths: CLI (`cmd/add.go`) and TUI (`internal/tui/model.go` `createCmd`)
- Tags are `[]string` on `model.Task`, parsed via `model.SanitizeTags()`
- Store already has `List()` with tag filtering via `ListOptions{Tag}`
- No existing mechanism to collect all unique tags across tasks

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
- **Unit tests**: required for every task
- Store.Tags() tested with real filesystem (consistent with existing store_test.go pattern)
- Title prefix parsing tested as a pure function with table-driven tests
- CLI and TUI integration tested via existing command test patterns

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## Solution Overview
1. Add `Tags()` method to `Store` that scans all tasks and returns a sorted, deduplicated list of tag strings — for use where no task list is already loaded (TUI, future features)
2. Add `CollectTags(tasks []model.Task) []string` helper in `model` package — extracts unique sorted tags from an already-loaded task slice. `Store.Tags()` uses this internally
3. Add `ParseTitleTag(title string, knownTags []string) string` function to `model` package that checks if the title starts with a known tag prefix pattern (`tag: ...`) and returns the matching tag (or empty string). Excludes the reserved `active` tag
4. Wire into `cmd/add.go` using `model.CollectTags(existing)` (avoids double file scan — tasks already loaded for position calc) and TUI `createCmd` using `m.store.Tags()`

## Technical Details
- **Title prefix pattern**: `^tagname:\s+` where `tagname` matches a known tag (case-sensitive, exact match). Only single-word tags match (no spaces or colons in tag name)
- **Tag merging**: auto-tag is appended only if not already present in the explicit tags list (no duplicates)
- **Known tags source**: `Store.Tags()` returns all unique tags from all tasks (any status). In CLI `add`, tags are extracted from the already-loaded `existing` slice to avoid a second full directory scan
- **Reserved tag protection**: `ParseTitleTag` never returns `model.ActiveTag` — consistent with `SanitizeTags` stripping it
- **Title unchanged**: the prefix remains in the title

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): `Store.Tags()`, `ParseTitleTag()`, CLI wiring, TUI wiring, tests
- **Post-Completion**: no external system changes needed

## Implementation Steps

### Task 1: Add `CollectTags()` helper and `Store.Tags()` method

**Files:**
- Modify: `internal/model/task.go`
- Modify: `internal/model/task_test.go`
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

- [x] Write TDD tests for `model.CollectTags(tasks)`: returns empty for empty slice, returns sorted unique tags, excludes tasks with no tags, deduplicates across tasks
- [x] Implement `CollectTags(tasks []model.Task) []string` in `model` package
- [x] Write TDD tests for `Store.Tags()`: returns empty for no tasks, returns sorted unique tags across all stored tasks
- [x] Implement `Tags() ([]string, error)` on `Store` — calls `List` then `CollectTags`
- [x] Run tests — must pass before task 2

### Task 2: Add `ParseTitleTag()` function

**Files:**
- Modify: `internal/model/task.go`
- Modify: `internal/model/task_test.go`

- [x] Write TDD tests for `ParseTitleTag(title, knownTags)`: returns matching tag for `"jean: create integration"` when `"jean"` is known, returns empty string when tag is unknown, returns empty for titles without colon, handles edge cases (empty title, colon at start, no space after colon, tag with spaces, tag with colon)
- [x] Write TDD test: returns empty string when prefix matches reserved `active` tag
- [x] Implement `ParseTitleTag(title string, knownTags []string) string` — split on first `:`, take left side as candidate, verify at least one space follows the colon, look up candidate in knownTags, reject `ActiveTag`
- [x] Run tests — must pass before task 3

### Task 3: Wire auto-tag into CLI `add` command

**Files:**
- Modify: `cmd/add.go`
- Modify: `cmd/add_test.go`

- [x] Write TDD test: creating task with title `"jean: do thing"` when a task with tag `"jean"` exists → new task gets `"jean"` tag automatically
- [x] Write TDD test: creating task with title `"jean: do thing"` when no task has `"jean"` tag → no auto-tag added
- [x] Write TDD test: creating task with title `"jean: do thing"` and explicit `--tags jean` → no duplicate tag
- [x] Wire into `newAddCmd` RunE: use `model.CollectTags(existing)` (tasks already loaded for position calc), then `model.ParseTitleTag()`, merge result into `task.Tags`
- [x] Run tests — must pass before task 4

### Task 4: Wire auto-tag into TUI `createCmd`

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] Write TDD test: TUI task creation with title prefix matching a known tag → auto-tag applied
- [x] Write TDD test: TUI task creation with unknown prefix → no auto-tag
- [x] Wire into `createCmd`: call `m.store.Tags()`, then `model.ParseTitleTag()`, merge into tags slice before creating task
- [x] Run tests — must pass before task 5

### Task 5: Verify acceptance criteria

- [x] Verify: CLI `monolog add "jean: create integration"` auto-tags when `jean` tag exists on another task
- [x] Verify: no auto-tag when tag is unknown
- [x] Verify: TUI add modal behaves the same way
- [x] Verify: no duplicate tags when prefix matches explicit tag
- [x] Run full test suite: `go test ./...`
- [x] Run lint: `go vet ./...`

### Task 6: [Final] Update documentation

- [x] Update CLAUDE.md if new patterns discovered
- [x] Move this plan to `docs/plans/completed/`

## Post-Completion
**Manual verification:**
- Create a task with a tag (`monolog add "test task" -t jean`)
- Create another task with title prefix (`monolog add "jean: another task"`) and verify auto-tag
- Verify same behavior in TUI
- Test with multiple known tags to ensure correct prefix matching
