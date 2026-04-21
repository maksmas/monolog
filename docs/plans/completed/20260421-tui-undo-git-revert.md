# TUI Undo via Git Revert

## Overview

Add multi-level undo to the TUI (`u` / `ctrl+z`) backed by `git revert`. Every TUI mutation already auto-commits, so we can capture the resulting commit SHA and push it onto an in-memory stack (capped at 10). Pressing either key pops the top SHA and runs `git revert <sha> --no-edit`, creating a new commit that reverses the change — no history rewrite, sync-safe. (`cmd+z` is handled at the macOS/terminal-emulator level and is not available to terminal apps.)

Covered actions: done, reschedule, retag, toggle-active, add (create), delete, grab-move, YAML edit, note.

**Acceptance criteria:**
- `u` and `ctrl+z` undo the last mutation in normal mode
- Up to 10 actions are undoable; the 11th oldest is silently dropped
- Each undo creates a `Revert "..."` commit; status bar shows what was undone
- Undo with empty stack shows "nothing to undo"
- Sync with a remote clears the stack (rebase may have rewritten SHAs); sync without a remote preserves it
- Failed revert (conflict) restores the SHA to the top of the stack and aborts

## Context (from discovery)

- Files involved: `internal/git/git.go`, `internal/tui/model.go`
- Every mutation goes through `saveCmd`, `createCmd`, `deleteCmd`, `doneSelected`, grab-move (inline in `updateGrab`), or `openEdit` — each calls `git.AutoCommit` and returns a `taskSavedMsg`
- `taskSavedMsg` handler at line ~894 is the single choke point for pushing SHAs; `msg.err != nil` causes an early return — `clearUndo` must be evaluated **before** that check
- `syncCmd` returns `taskSavedMsg`; `SyncResult.HasRemote` tells us if a rebase was attempted (only then do we clear the stack — local-only syncs preserve it)
- Grab-move with rebalance: `git.AutoCommit` only stages the grabbed task's file; rebalanced siblings are written to disk but committed separately. Reverting a move commit therefore only restores the grabbed task's position, not its siblings'. **This is a pre-existing limitation of the move commit, not introduced by this feature.** Documented here; not fixed.
- Each task lives in its own JSON file → revert conflicts between unrelated tasks are nearly impossible

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Solution Overview

1. Add `HeadSHA`, `AutoCommitSHA`, and `Revert` helpers to `internal/git/git.go`
2. Add `sha string` and `clearUndo bool` fields to `taskSavedMsg`
3. Replace `git.AutoCommit` → `git.AutoCommitSHA` in all TUI mutation paths; include the SHA in the returned `taskSavedMsg`
4. In `taskSavedMsg` handler: **first** check `clearUndo` and clear stack (runs even on error); then if no error, push non-empty SHA (cap 10)
5. Wire `u` and `ctrl+z` → `undoCmd()`: pop stack, get original commit subject, run `git.Revert`; on failure push SHA back and abort; on success return status with original subject
6. `syncCmd` sets `clearUndo: true` only when `res.HasRemote` is true

## Technical Details

### New git helpers

```go
// HeadSHA returns the SHA of the current HEAD commit.
func HeadSHA(repoPath string) (string, error)

// AutoCommitSHA stages files, commits, then returns the resulting HEAD SHA.
func AutoCommitSHA(repoPath, message string, files ...string) (string, error)

// CommitSubject returns the one-line subject of the given commit.
func CommitSubject(repoPath, sha string) (string, error)

// Revert creates a new commit that reverses the named commit (git revert --no-edit).
// On conflict it runs git revert --abort before returning the error.
func Revert(repoPath, sha string) error
```

### taskSavedMsg additions

```go
type taskSavedMsg struct {
    status    string
    err       error
    focusID   string
    sha       string  // commit SHA to push onto undoStack; empty → skip push
    clearUndo bool    // true → clear the entire undoStack before push (evaluated even on error)
}
```

### Model additions

```go
undoStack []string  // SHAs of undoable commits, newest last; max 10
```

### taskSavedMsg handler (revised ordering)

```
case taskSavedMsg:
    if msg.clearUndo {            // <-- FIRST, even on error
        m.undoStack = nil
    }
    if msg.err != nil {           // early-return on error (original behavior)
        ...
        return m, nil
    }
    if msg.sha != "" {            // push SHA onto stack, cap 10
        m.undoStack = append(m.undoStack, msg.sha)
        if len(m.undoStack) > 10 {
            m.undoStack = m.undoStack[1:]
        }
    }
    ... (existing reload / focusID logic)
```

### undoCmd flow

```
if undoStack empty → statusMsg = "nothing to undo", return nil
pop sha from undoStack
subject = git.CommitSubject(repoPath, sha)   // e.g. "reschedule: foo -> tomorrow"
err = git.Revert(repoPath, sha)
if err → push sha back onto undoStack        // restore for retry
         return taskSavedMsg{err: ...}
return taskSavedMsg{status: "Undone: " + subject, sha: ""}   // no SHA → not re-pushed
```

### Undo stack lifecycle

| Event | Stack effect |
|---|---|
| Any successful mutation | push SHA (cap 10, drop oldest) |
| `u` or `ctrl+z` pressed | pop top SHA; on success → not re-pushed; on failure → restore SHA |
| Sync with remote completes (success or fail) | clear entire stack |
| Sync with no remote (local-only) | stack unchanged |
| Undo with empty stack | show "nothing to undo", no-op |

## Implementation Steps

### Task 1: Add git helpers

**Files:**
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`

- [x] add `HeadSHA(repoPath string) (string, error)` — runs `git rev-parse HEAD`
- [x] add `AutoCommitSHA(repoPath, message string, files ...string) (string, error)` — stages, commits, returns `HeadSHA`
- [x] add `CommitSubject(repoPath, sha string) (string, error)` — runs `git log -1 --format=%s <sha>`
- [x] add `Revert(repoPath, sha string) error` — runs `git revert <sha> --no-edit`; on non-zero exit runs `git revert --abort` before returning error
- [x] write tests for `HeadSHA` (success, non-git dir → error)
- [x] write tests for `AutoCommitSHA` (returns correct SHA matching `git rev-parse HEAD`)
- [x] write tests for `CommitSubject` (returns correct subject line)
- [x] write tests for `Revert` (reverts file change; commit message starts with `Revert`; bad SHA → error)
- [x] run `go test ./internal/git/` — must pass before Task 2

### Task 2: Thread SHA through taskSavedMsg

**Files:**
- Modify: `internal/tui/model.go`

- [x] add `sha string` and `clearUndo bool` fields to `taskSavedMsg` (line ~346)
- [x] update `taskSavedMsg` handler (line ~894): move `clearUndo` check **before** the `msg.err` early-return; push `msg.sha` onto `undoStack` (cap 10) after error check
- [x] update `saveCmd` (~line 2300): replace `git.AutoCommit` with `git.AutoCommitSHA`, set `sha` field in returned `taskSavedMsg`
- [x] update `createCmd` (~line 2315): same replacement
- [x] update `deleteCmd` (~line 2421): same replacement
- [x] update `doneSelected` (~line 1188): replace `git.AutoCommit` with `git.AutoCommitSHA`, set `sha`
- [x] update grab-move path (~line 2233): replace `git.AutoCommit` with `git.AutoCommitSHA`, set `sha`; add code comment noting that rebalanced sibling files are not part of this commit and will not be reverted by undo
- [x] update `openEdit` path (~line 1903): replace `git.AutoCommit` with `git.AutoCommitSHA`, set `sha`
- [x] update `syncCmd` (~line 2401): set `clearUndo: true` only when `res.HasRemote` is true (local-only sync preserves stack)
- [x] run `go test ./internal/tui/` — must pass before Task 3

### Task 3: Undo stack + key bindings + help modal

**Files:**
- Modify: `internal/tui/model.go`

- [x] add `undoStack []string` field to `Model` struct (already added in Task 2)
- [x] add `"u"` and `"ctrl+z"` cases in `updateNormal` (~line 941): call `undoCmd()`
- [x] implement `undoCmd() tea.Cmd`:
  - if `undoStack` empty: set `m.statusMsg = "nothing to undo"`, return nil
  - pop top SHA
  - in the returned func: call `git.CommitSubject` to get subject; call `git.Revert`; on error push SHA back and return `taskSavedMsg{err: ...}`; on success return `taskSavedMsg{status: "Undone: " + subject, sha: ""}` (no SHA)
- [x] update `helpModalContent` (~line 3097) to add a row: `u / ctrl+z  undo last action`
- [x] run `go test ./internal/tui/` — must pass before Task 4

### Task 4: Tests

**Files:**
- Modify: `internal/tui/model_test.go`

- [x] write test: after a successful mutation, `undoStack` has length 1 with a non-empty SHA
- [x] write test: failed mutation (`msg.err != nil`) → `undoStack` remains empty (no SHA pushed)
- [x] write test: pressing `u` with a non-empty stack decrements stack length and reloads tasks
- [x] write test: pressing `u` with empty stack → status "nothing to undo", stack unchanged
- [x] write test: after sync with `HasRemote` true, `undoStack` is cleared (even if sync errors)
- [x] write test: after sync with `HasRemote` false (local-only), `undoStack` is preserved
- [x] write test: stack is capped at 10 after 15 mutations (oldest 5 dropped)
- [x] run `go test ./internal/tui/` — must pass before Task 5

### Task 5: Verify acceptance criteria

- [x] verify all listed action types (done, reschedule, retag, toggle-active, add, delete, move, edit, note) push a SHA to `undoStack`
- [x] verify 10 sequential mutations fill the stack, 11th drops the oldest
- [x] verify `u` / `ctrl+z` walk back through all 10, each restoring task state and showing correct "Undone: ..." status
- [x] verify sync with remote clears the stack; sync without remote preserves it
- [x] run full test suite: `go test ./...`
- [x] run `go vet ./...`

### Task 6: Update documentation

**Files:**
- Modify: `CLAUDE.md`
- Move: `docs/plans/20260421-tui-undo-git-revert.md` → `docs/plans/completed/`

- [x] add undo design note to CLAUDE.md Key Design Decisions: `undoStack []string` on Model (max 10 SHAs); `u`/`ctrl+z` triggers `git revert <sha> --no-edit`; stack clears on remote sync (rebase may rewrite SHAs); grab-move rebalance positions not captured in the move commit so not undone by undo
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- Open the TUI, do a done/reschedule/delete, press `u`, verify the task reverts and `git log` shows `Revert "..."` commit
- Do 12 mutations; verify only the last 10 are undoable
- Sync with a remote; verify `u` reports "nothing to undo"
- Move a task with grab (`m`), press `u`, verify the grabbed task position reverts (sibling rebalanced positions are not reverted — expected/documented)
