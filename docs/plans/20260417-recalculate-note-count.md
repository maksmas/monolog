# Recalculate NoteCount on Task Update

## Overview
Today `Task.NoteCount` is incremented manually by the code paths that add notes (`cmd/note.go`, `internal/tui/model.go`). Any other path that mutates `Body` — a direct `edit --body`, a future CLI to delete a note, or a body edited in the TUI — leaves `NoteCount` stale. Make `store.Update` the single source of truth: it recounts note separators in `Body` and overwrites `NoteCount` before writing. The manual increments at add-note sites become unnecessary and can be removed.

Problem it solves: badge counts drift from reality whenever `Body` is edited through any path other than `AppendNote`.

Integration: adds one small helper in `internal/model` and a one-line call in `Store.Update`. No schema change, no migration.

## Context (from discovery)
- `Task.NoteCount` is defined at `internal/model/task.go:26`
- Note separator format is `--- YYYY-MM-DD HH:MM:SS ---`, produced by `model.AppendNote` in `internal/model/note.go`
- Manual `NoteCount++` sites: `cmd/note.go:40`, `internal/tui/model.go:1123`
- `Body` can be overwritten by `cmd/edit.go:61` (`--body` flag) and via TUI edit flows, with no compensating count update
- `Store.Update` (internal/store/store.go:62) is the single write chokepoint and already imports `model`
- No existing `CountNotes` helper — needs to be added

## Development Approach
- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- run tests after each change

## Testing Strategy
- **unit tests**: required for every task — `CountNotes` helper, `Store.Update` behavior, and regression tests around edit/note flows
- **e2e tests**: project has no UI e2e harness; TUI paths covered by existing table tests in `internal/tui/model_test.go`

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Solution Overview
1. Add `model.CountNotes(body string) int` — counts occurrences of a line starting with `--- ` and ending with ` ---` using a compiled regex anchored to line boundaries. Pure, easy to test.
2. In `Store.Update`, call `task.NoteCount = model.CountNotes(task.Body)` right before marshalling. This guarantees that every persisted task has a consistent count regardless of caller.
3. Remove the now-redundant `task.NoteCount++` lines in `cmd/note.go` and `internal/tui/model.go` (the increment becomes a no-op once Update recalculates, but keeping it hides the fact that Update is authoritative). Callers continue to call `AppendNote` + `Update`.
4. Rely on lazy correction: existing on-disk tasks with stale counts fix themselves the next time they're updated. No backfill.

Key decision: centralizing in `Store.Update` adds a tiny coupling from `store` to `model`'s note format, but `store` already imports `model` heavily. The win is that future mutation paths cannot forget to keep `NoteCount` in sync.

## Technical Details
- **Regex**: `^--- \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} ---$` with multiline mode (`(?m)`), matching the exact format produced by `AppendNote`. Strict matching avoids counting user-authored lines that happen to contain dashes.
- **Placement**: recalculation happens in `Store.Update` only, not `Store.Create`. New tasks start with `NoteCount: 0` and `Body: ""`; no recalculation needed until they're updated.
- **JSON tag stays `note_count,omitempty`**: a recalculated zero simply omits the field, which matches existing behavior.

## What Goes Where
- **Implementation Steps**: helper, store change, call-site cleanup, tests
- **Post-Completion**: none — lazy correction means no manual steps

## Implementation Steps

### Task 1: Add `CountNotes` helper

**Files:**
- Modify: `internal/model/note.go`
- Modify: `internal/model/note_test.go`

- [x] add `CountNotes(body string) int` in `internal/model/note.go` using a package-level compiled regex
- [x] write tests: empty body → 0; one note → 1; multiple notes → N; body with unrelated `---` lines → unchanged count; body with near-match but wrong date format → 0
- [x] run `go test ./internal/model/` — must pass before task 2

### Task 2: Recalculate `NoteCount` in `Store.Update`

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

- [x] in `Store.Update`, set `task.NoteCount = model.CountNotes(task.Body)` before calling `writeTask`
- [x] write a test: create a task with `NoteCount: 99` and a `Body` containing two note separators, call `Update`, read back, assert `NoteCount == 2`
- [x] write a test: update a task whose body has no notes — `NoteCount` ends at 0 even if the in-memory value was non-zero
- [x] write a test: `Create` is NOT affected — creating a task with `NoteCount: 0` and empty body stays at 0 (regression guard)
- [x] run `go test ./internal/store/` — must pass before task 3

### Task 3: Remove redundant increments at add-note sites

**Files:**
- Modify: `cmd/note.go`
- Modify: `internal/tui/model.go`

- [ ] delete `task.NoteCount++` at `cmd/note.go:40`
- [ ] delete `t.NoteCount++` at `internal/tui/model.go:1123`
- [ ] verify existing tests in `cmd/note_test.go` (which assert `NoteCount == 1` and `== 2` after adding notes) still pass — they should, because `Update` now recalculates
- [ ] verify existing TUI note-add tests in `internal/tui/model_test.go` still pass
- [ ] run `go test ./...` — must pass before task 4

### Task 4: Regression test — editing body does not leave count stale

**Files:**
- Modify: `cmd/task_commands_test.go` (or add to `cmd/edit_test.go` if it exists; check first)

- [ ] add a test: create a task, add two notes via `monolog note`, then run `monolog edit --body ""` to clear the body, assert reloaded task has `NoteCount == 0`
- [ ] add a test: create a task, manually set a body containing a well-formed separator line via `edit --body`, assert `NoteCount == 1` after save
- [ ] run `go test ./cmd/` — must pass before task 5

### Task 5: Verify acceptance criteria
- [ ] `go test ./...` green
- [ ] `go vet ./...` clean
- [ ] `go build -o monolog` succeeds
- [ ] manually spot-check: add a note, edit the body with `--body "some new text"`, confirm `monolog show` reports `Notes: 0`

### Task 6: Update documentation and archive plan
- [ ] update the "Task notes" bullet in `CLAUDE.md` to mention that `NoteCount` is recalculated from `Body` inside `Store.Update` (single source of truth)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
Lazy correction: users with existing repos will see stale counts self-heal the next time each task is edited. No migration command required.
