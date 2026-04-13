# Monolog interactive TUI mode

## Context

Today, running `monolog` with no subcommand prints help. We're adding an interactive TUI that launches in this case, giving the user a single-screen view to manage their backlog: see tasks across schedules, complete them, edit, reorder, reschedule, add, delete, and sync — without dropping back to the shell.

The existing CLI subcommands stay unchanged. The TUI is a new surface over the same `internal/store`, `internal/ordering`, and `internal/git` packages — all mutations still go through the same auto-commit path, so git history stays consistent regardless of how the user interacts.

A secondary win: the conflict-resolution logic this work introduces also benefits the existing `monolog sync` CLI command.

## Approach

**Library**: Bubble Tea + bubbles + lipgloss (charmbracelet stack). Adds new direct deps but is the de-facto choice for Go TUIs.

**Layout**: tabs by schedule across the top — `[Today] [Tomorrow] [Week] [Someday] [Done]` — each tab a `bubbles/list` of tasks. Status/help bar at the bottom.

**Interaction**:
- `↑/↓` move within list, `←/→` switch tabs, `1`–`5` jump to tab N
- `e` edit (shell out to `$EDITOR` with YAML), `r` reschedule (modal), `t` retag (modal), `d` done, `a` add (quick title), `x` delete (confirm), `m` grab, `s` sync, `q` quit, `?` help
- **Grab mode**: `↑/↓` reorder within tab, `←/→` move task to prev/next bucket (reschedules), `g`/`G` to top/bottom, `Enter` drops (single git commit), `Esc` cancels
- Grab+`→` into Done tab sets `status="done"`; grab+`←` out of Done sets `status="open"`
- New bucket placement after grab+arrow: bottom (`ordering.NextPosition`)
- Reordering not allowed inside Done tab (sort is by `UpdatedAt` desc, not position)

**Mutations**: each TUI action calls `store.Update/Create/Delete` then `git.AutoCommit(repoPath, msg, files...)` — same pattern as the existing CLI commands.

**Sync auto-conflict-resolution**: new `git.ResolveConflicts(repo)` that picks the version with the later `UpdatedAt` for each conflicted task file, stages it, and continues the rebase. Both the TUI's `s` key and the CLI's `monolog sync` use this.

## Critical files

**New**:
- `internal/tui/tui.go` — `Run(s *store.Store, repoPath string) error`, lipgloss styles, keymap
- `internal/tui/model.go` — Bubble Tea Model: state, Init/Update/View, tabs, list, modals, $EDITOR shell-out
- `internal/tui/model_test.go` — table-driven Update() tests

**Modified**:
- `cmd/root.go` — set `RunE` on the root command (with `Args: cobra.NoArgs`) that invokes `tui.Run`. Existing subcommands unaffected.
- `internal/git/git.go` — add `ResolveConflicts(repoPath string) (int, error)`; rewire `cmd/sync.go` to call it on rebase conflicts (`git rebase --continue` or `--abort`).
- `cmd/sync.go` — handle conflict path, print `auto-resolved K conflicts` in summary.
- `go.mod` — add `github.com/charmbracelet/bubbletea`, `bubbles`, `lipgloss`, `gopkg.in/yaml.v3`.

**Unchanged but reused**:
- `internal/store/store.go` — `List/Get/GetByPrefix/Create/Update/Delete`
- `internal/ordering/ordering.go` — `NextPosition/PositionTop/PositionBetween/NeedsRebalance/Rebalance`
- `internal/git/git.go` — `AutoCommit/SyncCommit/PullRebase/Push/HasChanges/HasRemote`
- `internal/model/task.go` — Task struct + JSON tags
- `internal/display/table.go` — `ShortID` (used in TUI list rendering)

## Implementation notes

**Model state** (sketch — final shape decided in code):
```go
type Model struct {
    store        *store.Store
    repoPath     string
    tabs         []tab          // {label, schedule, status} for each
    activeTab    int
    lists        []list.Model   // bubbles/list per tab; lazy-loaded
    mode         mode           // normal | grabbing | modalReschedule | modalRetag | modalAdd | modalConfirm
    grabSource   int
    statusMsg    string
    width, height int
}
```

**Async mutations**: each save dispatches a `tea.Cmd` that calls store + AutoCommit and returns `taskSavedMsg{taskID, err}` or similar. View reloads the affected tab from store on success; surfaces error in status bar on failure.

**$EDITOR shell-out** (`e` key): Bubble Tea's `tea.ExecProcess` handles alt-screen suspend/restore. Marshal task to YAML in temp file, exec `$VISUAL || $EDITOR || vi`, on exit re-read and parse. Validate `schedule` value before saving (must be `today`/`tomorrow`/`week`/`someday`/ISO date).

**Reschedule modal**: instant on `1`–`4` (today/tomorrow/week/someday); `5` focuses an ISO-date textinput.

**Add modal**: quick title input only. New task goes into the active tab's bucket at `NextPosition`. To add body/tags, use `e` after.

**Delete modal**: `Delete "<title>"? (y/N)` — `y` confirms.

**Grab mode UX**: on `m`, set `mode = grabbing`; the row visibly highlights. `↑/↓` reorder *in memory only* (no store write per step). `←/→` move across tabs in memory (also no store write). `Enter` writes the final state once: `store.Update` + one `git.AutoCommit`. `Esc` discards in-memory changes and reloads the tab. After drop, run `ordering.NeedsRebalance` and `Rebalance` if needed (matches `cmd/mv.go`).

**Done-tab sort**: when populating the Done tab list, sort `[]Task` by `UpdatedAt` descending in the TUI before feeding to `bubbles/list`. No change to `store.ListOptions`.

**Sync flow** (TUI `s` and CLI `sync` both):
1. `SyncCommit`
2. `PullRebase` — capture stderr; detect conflict (`CONFLICT (content)` or non-zero exit with unmerged paths)
3. If conflict: `ResolveConflicts(repo)` → on success run `git rebase --continue`; on failure run `git rebase --abort` and return error
4. `Push`

**`ResolveConflicts` implementation**:
- `git diff --name-only --diff-filter=U` to list unmerged paths
- For each path under `.monolog/tasks/`: read ours (`git show :2:<path>`) and theirs (`git show :3:<path>`); empty = deleted side
- If both sides present: parse both as `model.Task`, write the one with later `UpdatedAt` (tiebreak: full-content equality → no-op; else "ours")
- If one side deleted, other modified: write the modified one (preserve data)
- `git add <path>` after each
- Return count resolved; error if any non-task path is unmerged

**Tests**:
- `internal/tui/model_test.go` — table-driven on `Model.Update`. Construct a Model with a fake/temp store, fire `tea.KeyMsg{...}` values, assert resulting state and that `store` reflects the change. Cover: tab switching, complete (`d`), reschedule modal save, grab mode (m → ↑ → → → Enter), delete confirm, add modal.
- `internal/git/git_test.go` — extend with `TestResolveConflicts_PicksLaterUpdatedAt`, `TestResolveConflicts_DeleteVsModify_ModifyWins`, `TestResolveConflicts_NonTaskPathErrors`. Use temp git repos with hand-crafted conflicts (two branches modifying same task file with known timestamps, then `git merge` to produce conflict state).
- `cmd/sync_test.go` — extend to cover the conflict-then-resolve path end-to-end against a temp repo with a remote bare clone.
- `cmd/root_test.go` — assert that `monolog` with no args triggers TUI entry (mock or check that `tui.Run` is wired; full TUI behavior is covered in `internal/tui/`).

**Stay close to existing patterns**:
- Test helpers like `initTestRepo` and `readTasks` from `cmd/add_test.go` are reusable for `model_test.go`.
- Auto-commit messages should mirror the verbs the CLI uses (`done: <title>`, `reschedule: <title> -> <bucket>`, `move: <title>`, `edit: <title>`, `add: <title>`, `rm: <title>`).

## Verification

End-to-end manual test in a scratch repo (`MONOLOG_DIR=/tmp/monolog-test`):

1. `monolog init` then `monolog add "buy milk"` × a few across schedules
2. Run `monolog` (no args) → TUI launches on Today tab
3. `↓` `↓` to select a task, `d` → task moves to Done tab; verify `git log` shows `done: …`
4. `←/→` cycle tabs; `1`–`5` jump
5. Select a task, `r`, press `2` → moves to Tomorrow tab
6. `m` (grab), `↑` × 2, `→` (move to next bucket), `Enter` → verify position + schedule changed in single commit
7. `e` → `$EDITOR` opens with YAML; change title and body; save+exit → TUI redraws with new title
8. `a` "test task" → appears at bottom of current tab
9. `x` → confirm `y` → task gone; `git log` shows `rm: …`
10. `s` → sync runs; status bar shows result. (Requires a remote configured.)
11. **Conflict path**: clone the test repo to a 2nd dir, edit same task on both, push from one, sync from the other → `auto-resolved 1 conflicts` and the later edit wins.
12. `q` exits cleanly; terminal restored.

Automated:
- `go test ./...` — all green
- `go vet ./...`
- `go build -o monolog` — binary builds

## Out of scope (deferred)

- Multi-select / bulk operations
- Search / filter within a tab
- Custom themes
- Config file (TUI uses defaults: ↑/↓/←/→, $EDITOR fallback to vi)
- Adding `CompletedAt` field to model (Done sort uses `UpdatedAt`, acceptable tradeoff for v1)
- `SortBy` option in `store.ListOptions` (Done re-sort happens in TUI; promote to store if a 2nd caller appears)
- Vim-style J/K shift-to-move keys (grab mode covers this)
- Conflict resolution for non-task files (future config files would need separate strategy)
