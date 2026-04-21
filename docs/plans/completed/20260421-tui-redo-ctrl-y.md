# TUI Redo via Ctrl+Y

## Overview

Add multi-level redo to the TUI (`ctrl+y`), mirroring the existing undo mechanism. Every successful undo creates a revert commit whose SHA is captured onto a `redoStack`; pressing `ctrl+y` pops that SHA and runs `git revert <revert-sha> --no-edit`, effectively re-applying the original change. Any new mutation clears the `redoStack` (standard redo semantics — after a new edit, the redo history is invalid). Remote sync clears both stacks. Capped at 10 entries like undo.

**Acceptance criteria:**
- `ctrl+y` redoes the last undone action in normal mode
- Up to 10 undones are redoable; the 11th oldest is silently dropped
- Each redo creates a `Revert "Revert ..."` commit; status bar shows what was redone
- Redo with empty stack shows "nothing to redo"
- Performing a new mutation (any action that pushes to `undoStack`) clears `redoStack`
- Sync with a remote clears both `undoStack` and `redoStack`
- Failed redo (conflict) restores the SHA to the top of `redoStack` and aborts
- Pressing `u`/`ctrl+z` after a redo undoes the redo (symmetric behavior)

## Context (from discovery)

- Files involved: `internal/git/git.go`, `internal/tui/model.go`, `internal/tui/model_test.go`, `internal/git/git_test.go`
- Existing undo uses `taskSavedMsg` fields: `sha`, `clearUndo`, `restoreUndoSHA`
- `undoCmd` at model.go:2476 runs the revert but does NOT currently capture the revert commit's SHA — needs extension to surface it via a new `redoSHA` field
- `git.headSHA` is unexported (by design); adding a `git.RevertSHA` helper keeps the symmetry with `git.AutoCommitSHA` without exporting `headSHA`
- `undoStackCap = 10` — reuse the same constant for `redoStack` (symmetric behavior)
- The `case "u", "ctrl+z"` block in `updateNormal` (~line 1070) is the pattern to mirror for `ctrl+y`
- `helpModalContent` (~line 3195) has the undo row; add a redo row next to it

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Solution Overview

1. Add `git.RevertSHA(repoPath, sha string) (string, error)` — mirrors `AutoCommitSHA`: runs `git revert <sha> --no-edit`, returns the new HEAD SHA on success
2. Add three new fields to `taskSavedMsg`: `redoSHA` (push to redoStack), `redoneSHA` (push to undoStack without clearing redoStack), `restoreRedoSHA` (push back to redoStack on redo failure)
3. Rename `clearUndo` → `clearHistory` (clears both stacks, since a remote sync invalidates both sets of SHAs)
4. Add `redoStack []string` to `Model`
5. Update `undoCmd` to call `RevertSHA`, capture the revert commit SHA, and return it in `redoSHA`
6. Handler logic: `sha` pushes to undoStack AND clears redoStack; `redoneSHA` pushes to undoStack preserving redoStack; `redoSHA` pushes to redoStack; `restoreRedoSHA` restores to redoStack on error
7. Implement `redoCmd()` — pops from redoStack, calls `RevertSHA`, returns `redoneSHA` on success / `restoreRedoSHA` on failure
8. Wire `ctrl+y` in `updateNormal` with empty-stack guard (mirroring the `u`/`ctrl+z` handler)
9. Update `helpModalContent` with redo row

## Technical Details

### New git helper

```go
// RevertSHA reverts the named commit with --no-edit and returns the resulting
// HEAD SHA (the revert commit). On conflict, runs git revert --abort before
// returning the error. Mirror of AutoCommitSHA for the revert path.
func RevertSHA(repoPath, sha string) (string, error)
```

Implementation reuses the existing `Revert` function body (minus the abort) plus `headSHA`. Alternatively: keep the existing `Revert` as-is and add a thin wrapper `RevertSHA` that calls `Revert` then `headSHA`. The wrapper approach is simpler and avoids duplicating the abort logic.

### taskSavedMsg additions

```go
type taskSavedMsg struct {
    status         string
    err            error
    focusID        string
    sha            string  // new mutation SHA → push to undoStack AND clear redoStack
    clearHistory   bool    // renamed from clearUndo; clear both stacks (remote sync)
    restoreUndoSHA string  // push back to undoStack (undo failure)
    redoSHA        string  // push to redoStack (undo succeeded, captures revert SHA)
    redoneSHA      string  // push to undoStack (redo succeeded, preserves redoStack)
    restoreRedoSHA string  // push back to redoStack (redo failure)
}
```

### Model addition

```go
redoStack []string  // SHAs of revert commits, newest last; capped at undoStackCap
```

### taskSavedMsg handler (revised ordering)

```
case taskSavedMsg:
    if msg.clearHistory {
        m.undoStack = nil
        m.redoStack = nil
    }
    if msg.restoreUndoSHA != "" {
        // append to undoStack, cap
    }
    if msg.restoreRedoSHA != "" {
        // append to redoStack, cap
    }
    if msg.err != nil { return ... }
    if msg.sha != "" {
        // push to undoStack, cap
        m.redoStack = nil  // NEW mutation invalidates redo history
    }
    if msg.redoneSHA != "" {
        // push to undoStack, cap (preserves redoStack)
    }
    if msg.redoSHA != "" {
        // push to redoStack, cap
    }
    ... (existing reload / focusID logic)
```

### undoCmd flow (updated)

```
pop sha from undoStack (synchronous)
→ subject = git.CommitSubject(sha)
→ revertSHA, err = git.RevertSHA(sha)
→ on CommitSubject err → taskSavedMsg{err} (no restore; stale SHA)
→ on RevertSHA err → taskSavedMsg{err, restoreUndoSHA: sha}
→ on success → taskSavedMsg{status: "Undone: "+subject, redoSHA: revertSHA}
```

### redoCmd flow (new)

```
if redoStack empty → updateNormal shows "nothing to redo", returns nil
pop revertSHA from redoStack (synchronous)
→ subject = git.CommitSubject(revertSHA)
→ newSHA, err = git.RevertSHA(revertSHA)
→ on CommitSubject err → taskSavedMsg{err} (no restore; stale SHA)
→ on RevertSHA err → taskSavedMsg{err, restoreRedoSHA: revertSHA}
→ on success → taskSavedMsg{status: "Redone: "+subject, redoneSHA: newSHA}
```

### Stack lifecycle

| Event | undoStack | redoStack |
|---|---|---|
| Normal mutation (`msg.sha`) | push (cap) | **clear** |
| Undo success | (popped before goroutine) | push revert SHA (cap) |
| Undo failure | restore SHA | unchanged |
| Redo success | push new SHA (cap) | (popped before goroutine) |
| Redo failure | unchanged | restore SHA |
| Sync with remote (`clearHistory`) | clear | clear |
| Sync local-only | unchanged | unchanged |

### Key bindings

- `u` / `ctrl+z` → undo (existing)
- `ctrl+y` → redo (new)

## Implementation Steps

### Task 1: Add git.RevertSHA helper

**Files:**
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`

- [x] add `RevertSHA(repoPath, sha string) (string, error)` — calls `Revert(repoPath, sha)` then `headSHA(repoPath)`
- [x] write test for `RevertSHA` success case (returns SHA matching `git rev-parse HEAD` after revert)
- [x] write test for `RevertSHA` bad-input case (bad SHA → error propagated from `Revert`)
- [x] run `go test ./internal/git/` — must pass before Task 2

### Task 2: Rename clearUndo → clearHistory; add redoStack fields

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] rename `taskSavedMsg.clearUndo` → `clearHistory` (field + all references)
- [x] add `redoSHA`, `redoneSHA`, `restoreRedoSHA string` fields to `taskSavedMsg`
- [x] add `redoStack []string` field to `Model`
- [x] update `taskSavedMsg` handler:
  - `clearHistory` nils both `undoStack` and `redoStack` (before error early-return)
  - `restoreRedoSHA` appends to `redoStack` with cap (before error early-return)
  - `sha` push now also sets `m.redoStack = nil` (new mutation invalidates redo)
  - `redoneSHA` appends to `undoStack` with cap (does NOT clear redoStack)
  - `redoSHA` appends to `redoStack` with cap
- [x] update `syncCmd` to use `clearHistory` instead of `clearUndo`
- [x] update existing tests that reference `clearUndo` to use `clearHistory`
- [x] run `go test ./internal/tui/` — must pass before Task 3

### Task 3: Update undoCmd to capture revert SHA

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] change `undoCmd` goroutine from `git.Revert` to `git.RevertSHA`
- [x] on success, return `taskSavedMsg{status: "Undone: "+subject, redoSHA: revertSHA}` (new `redoSHA` field)
- [x] existing `restoreUndoSHA` path on failure unchanged
- [x] write test: after a mutation + successful undo, `redoStack` has 1 entry (the revert SHA)
- [x] write test: after undo failure, `redoStack` is unchanged (only `undoStack` is restored via `restoreUndoSHA`)
- [x] run `go test ./internal/tui/` — must pass before Task 4

### Task 4: Implement redoCmd + ctrl+y key binding

**Files:**
- Modify: `internal/tui/model.go`

- [x] add `case "ctrl+y"` in `updateNormal` (~line 1070) with empty-stack guard: if `len(m.redoStack) == 0`, set `m.statusMsg = "nothing to redo"` and return `m, nil`; else `return m, m.redoCmd()`
- [x] implement `redoCmd() tea.Cmd`:
  - defensive guard for empty redoStack → return nil
  - pop top revertSHA from redoStack (synchronous)
  - goroutine: `git.CommitSubject(revertSHA)` → `git.RevertSHA(revertSHA)`
  - on CommitSubject error → `taskSavedMsg{err}` (no restore)
  - on RevertSHA error → `taskSavedMsg{err, restoreRedoSHA: revertSHA}`
  - on success → `taskSavedMsg{status: "Redone: "+subject, redoneSHA: newSHA}`
- [x] update `helpModalContent` to add row: `"  " + k("ctrl+y") + "    redo last undone action\n"` immediately after the undo row
- [x] run `go test ./internal/tui/` — must pass before Task 5

### Task 5: Redo tests

**Files:**
- Modify: `internal/tui/model_test.go`

- [x] write test: `ctrl+y` with empty `redoStack` sets status "nothing to redo", stack unchanged
- [x] write test: after `done` + `u`, pressing `ctrl+y` pops redoStack; task returns to done state (mirror of undo's walk-back test)
- [x] write test: `ctrl+y` success pushes new SHA onto `undoStack` AND pops `redoStack`; does NOT clear redoStack beyond the pop
- [x] write test: performing a new mutation (e.g. `taskSavedMsg{sha: "newsha"}`) clears `redoStack`
- [x] write test: failed redo (`taskSavedMsg{err, restoreRedoSHA: "abc"}`) pushes "abc" back to redoStack
- [x] write test: redoStack capped at `undoStackCap` (10) after many undos
- [x] write test: `clearHistory: true` clears both undoStack and redoStack
- [x] run `go test ./internal/tui/` — must pass before Task 6

### Task 6: Verify acceptance criteria

- [x] verify all acceptance criteria from Overview
- [x] verify sync-with-remote clears both stacks (rename propagated correctly)
- [x] verify undo → redo → undo cycle works (symmetric)
- [x] run full test suite: `go test ./...`
- [x] run `go vet ./...`

### Task 7: Update documentation

**Files:**
- Modify: `CLAUDE.md`
- Move: `docs/plans/20260421-tui-redo-ctrl-y.md` → `docs/plans/completed/`

- [x] extend the Undo bullet in CLAUDE.md Key Design Decisions to document redo:
  - `redoStack []string` on Model (same cap)
  - `ctrl+y` redoes via `git.RevertSHA`
  - new mutation clears redoStack; sync clears both (hence `clearHistory` rename)
  - new `taskSavedMsg` fields: `redoSHA`, `redoneSHA`, `restoreRedoSHA`
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- Open the TUI, do a done/reschedule, press `u`, then `ctrl+y` — verify the action is re-applied
- Undo 3 times, redo 3 times, verify all restored
- Undo 2 times, perform a new mutation, press `ctrl+y` — verify "nothing to redo"
- Sync with a remote, verify both `u` and `ctrl+y` report "nothing to undo/redo"
