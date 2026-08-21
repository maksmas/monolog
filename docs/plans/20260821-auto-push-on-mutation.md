# Auto-push on mutation

## Overview

Every monolog mutation auto-commits **locally only**. Nothing pushes to the remote until the
user explicitly presses `s` in the TUI or runs `monolog sync`. Because the Telegram bot is a
separate clone on the user's always-on personal server that only ever *pulls*, a task created
on the laptop is invisible to the bot until the user remembers to sync manually.

Verified before planning:

- `s` in the TUI **does** push — `updateNormal` case `"s"` → `Model.syncCmd()` →
  `git.Sync()` → `Push()` → `git push`. The key works; it is simply never automatic.
- The live repo at `~/.monolog` was **3 commits ahead of `origin/main`** with a clean working
  tree and a correctly configured `origin`/upstream — exactly the reported symptom, with no
  second underlying bug (no missing remote, no broken upstream, no silent push failure).
- The user has already filed this against the CLI too ("mlog: creating a task from CLI doesnt
  push to GH, no update in tg bot"), so the fix covers both surfaces.

**Fix (two halves)**:

1. **Laptop → remote (the reported bug)**: after a mutation commits, push in the background.
   If the push is rejected as non-fast-forward (common — the bot pushes its own captures),
   fall back to `pull --rebase --autostash` + existing conflict auto-resolution + one retry
   push.
2. **Remote → bot, on demand**: the bot already pulls periodically — a best-effort
   `PullRebase` at startup (`internal/telegram/sync.go:229`) plus a `runPullTicker` goroutine
   calling `pullOnce()` every `cfg.PullInterval` (default **30s**). That leaves up to one full
   interval of staleness, so a command can still miss a task pushed seconds earlier. Adding a
   freshness-gated pull *before serving a command* closes that window to zero. The ticker is
   **retained** as the background safety net (it heals `readOnly` and retries stuck-rebase
   recovery even when nobody is sending commands), so the two mechanisms share one `lastPull`
   clock rather than double-fetching.

Benefits: tasks filed on the laptop reach the bot immediately on the next command (instead of
up to 30s later, or never without a manual sync); the failure path stays non-fatal so offline
use is unaffected; and because a plain push does not rewrite history, the TUI's undo/redo
stacks survive in the common case.

## Context (from discovery)

Files/components involved:

- `internal/git/git.go` — `AutoCommit`/`AutoCommitSHA` (commit, no push), `Push`,
  `PullRebase`, `HasRemote`, `Sync` (`git.go:236`), `Revert`/`RevertSHA`,
  `IsRebasing`/`ResolveConflicts`/`RebaseContinue`/`RebaseAbort`, private `run` helper.
  `Init` also pushes, once, via `push -u` (`git.go:89`).
- `internal/tui/model.go` — six mutation call sites using `git.AutoCommitSHA`
  (lines 1424, 2118, 2453, 2526, 2602, 2733), `syncCmd` (2626), `taskSavedMsg` (382) and its
  `Update` handler (989), `emailSyncResult` handler (1057), `applySettings` (3374).
  Already imports `internal/config` (`model.go:21`) and snapshots `config.Theme()` /
  `config.Email()` inside `newModel` (:454, :463).
- `cmd/` — six CLI mutation sites using `git.AutoCommit`: `add.go:91`, `edit.go:107`,
  `done.go:74`, `mv.go:119` (inside the `rebalanceAndCommit` helper, not a `RunE`),
  `rm.go:38`, `note.go:48`. Each prints its success line *after* the commit.
- `internal/email/sync.go:128` — a **seventh** `git.AutoCommit`, the batch commit at the end
  of `email.Sync`. Reached from `cmd/email.go` and from the TUI, whose email path returns
  `emailSyncResult`, not `taskSavedMsg`.
- `internal/config/config.go` — `Load`, `Save` (already read-modify-write over a
  `map[string]any`, :354-360), `SaveEmail`, `SaveTelegram`, the `emailBlock`/`telegramBlock`
  pattern, `Theme()` (:472).
- `internal/telegram/sync.go` — `pullOnce` (:117-141) is a third hand-rolled copy of the
  rebase-with-resolution dance, with `pullFunc`/`syncFunc` seams (:31-32) and a
  handler-wide `sync.Mutex` around *all* git work (`handler.go:40`). `Serve` does a
  best-effort startup pull (:229) and spawns `runPullTicker` (:239, :273) which calls
  `pullOnce()` every `cfg.PullInterval`. Write paths already sync via `commitAndSync` (which
  runs `syncFunc` = `git.Sync`); **browse commands read purely local state**, which is the
  staleness gap Task 10 closes.
- **Stale EC2 wording** — the bot runs on the user's own always-on server, not EC2. The
  tooling is already host-neutral (`DEPLOY_HOST`, `BOT_ARCH`, arch-neutral binary install), so
  this is a pure docs-wording problem in 9 places: `README.md:469,475`, `Makefile:16,71`,
  `CLAUDE.md:76` (Deployment topology), `cmd/telegram.go:49`, `docs/deploy/README.md:43,210`,
  `docs/deploy/env.example:15`, `internal/telegram/bot_test.go:307`.
- `docs/claude-skill/SKILL.md` — :11 and :125 promise that skill-authored captures stay local.

Related patterns found:

- **Non-fatal side effect after a commit**: `cmd/done.go` archives a Gmail message *after*
  printing "Done:" (:78 then :85), warns on failure, and uses a `var archiveFn = realArchive`
  seam. Auto-push copies this shape — including the ordering.
- **Post-commit work dispatched from the `Update` handler**: `taskSavedMsg.archiveSourceID`
  makes `Update` kick off `archiveEmailCmd` after the reload.
- **Injectable seams for network calls**: `runEmailSync`/`emailClientBuilder`
  (`internal/tui/email.go:115,127`), `pullFunc`/`syncFunc` (`internal/telegram/sync.go:31-32`),
  `archiveFn` (`cmd/done.go:24`).
- **One mutex around all git work**: `internal/telegram/handler.go:40`.
- **Optional config block with defaults**: `emailBlock`/`telegramBlock` +
  `resetXToDefaults()` in `Load`.
- **Env escape hatch, exact `== "1"` match**: `internal/display/links.go:70`,
  `internal/tui/watcher.go:34`.
- **`clearHistory` on rewritten history — including on error**: `syncCmd` sets
  `clearHistory: true` whenever `res.HasRemote`, *even on the error return*
  (`internal/tui/model.go:2630-2637`).
- **Bare-repo test fixtures**: `internal/git/git_test.go:122`, `:286`.

Dependencies identified: none new. Go stdlib (`os/exec`, `context`, `sync`, `time`) only.

## Development Approach

- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change (`go test ./...`, `go vet ./...`)
- maintain backward compatibility: `git.Sync`, `git.Push`, `git.PullRebase`, `git.AutoCommit`
  and `git.AutoCommitSHA` keep their exported signatures; `monolog sync` and the TUI `s` key
  keep their current behavior

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above)
- **e2e tests**: the project has no UI-based e2e harness (Playwright/Cypress), so this is n/a.
  The equivalent integration coverage is `internal/git` tests driving **real `git` binaries
  against `git init --bare` fixture remotes** — the only way to exercise a genuine
  non-fast-forward rejection, so the rejection path must be covered that way rather than with
  a fake.
- TUI and CLI layers are tested against injected seams — `autoPushFn` in `cmd/`, `runAutoPush`
  in `internal/tui` (Task 7 introduces it; both are needed because the two layers are tested
  independently) — so no test outside `internal/git` touches the network.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

New `git.AutoPush(repoPath, timeout) (PushResult, error)` in `internal/git/autopush.go`:

1. Acquire the repo-wide mutex (Task 2) for the whole call.
2. `IsRebasing` → if the repo is already mid-rebase, return a distinct sentinel error and do
   nothing. A stuck rebase must be surfaced clearly, not retried on every mutation.
3. `HasRemote` false → `PushResult{Skipped: true}, nil`. Local-only repos are supported.
4. No upstream configured for the current branch → `PushResult{Skipped: true}, nil`. Without
   this, a repo whose remote was added by hand after `init` warns on *every* mutation.
5. `git push` under a `context.WithTimeout`. Success → `PushResult{Pushed: true}`.
6. Failure → classify the combined output. If it is **not** a non-fast-forward rejection
   (DNS, auth, timeout, protected branch), return the error unchanged — do not rebase, because
   the remote state is unknown.
7. If it **is** a rejection → set `Rebased` *before* attempting recovery, run
   `pull --rebase --autostash` with the existing conflict auto-resolution, then retry the push
   **once** (no loop). Return `Rebased: true, Resolved: n` alongside either success or the
   error.

Key design decisions:

- **Push first, rebase only on rejection** (chosen over unconditionally reusing `git.Sync`).
  A plain `git push` does not rewrite local history, so `undoStack`/`redoStack` stay valid in
  the common case; only the rejected path has to clear them. Reusing `Sync` per mutation would
  wipe undo/redo on *every* keystroke and add a network pull to every mutation.
- **`Rebased` survives the error return.** If the rebase succeeds but the retry push fails
  (another client raced in, network dropped between the two pushes), local SHAs have *already*
  been rewritten. Reporting `Rebased: false` there would leave `undoStack` holding dead SHAs;
  `revertStackCmd` treats a `CommitSubject` miss as non-retriable and silently drops the entry
  (`internal/tui/model.go:2693-2697`), so undo history would corrupt in exactly the scenario
  the feature exists for. `syncCmd` already solves this by setting `clearHistory` on its error
  return (`model.go:2630-2637`); `AutoPush` mirrors it.
- **One repo-wide mutex, not a push-only mutex.** Serializing `AutoPush` against itself is not
  enough: the coalescing design *guarantees* that a mutation's `AutoCommitSHA` can run while a
  push is in flight, each in its own `tea.Cmd` goroutine. If that push is mid-rebase, the
  concurrent commit either contends on `.git/index.lock` — leaving the user with a written task
  file and a `commit:` error — or commits onto a detached rebase HEAD. Pressing `s` during an
  auto-push rebase is two concurrent rebases in one worktree. `internal/telegram/handler.go:40`
  already solves this with one mutex around all git work; Task 2 does the same in
  `internal/git`. The lock is **per-process**: a Raycast `monolog add` racing the TUI still
  degrades to an `index.lock` error, which stays a warning.
- **`--autostash` on the rebase fallback.** `pull --rebase` refuses to run with a modified
  tracked file. `Sync` sidesteps this by committing everything first (`HasChanges` →
  `SyncCommit`); `AutoPush` deliberately does not commit unrelated files, so it must autostash
  instead. This is reachable in normal use: `applySettings` (`model.go:3374`) writes tracked
  `.monolog/config.json` via `config.Save` and never commits it, so without `--autostash`
  every rejected push after a settings change fails until the user presses `s`.
- **Reject-classification by output matching, narrowly.** git signals non-fast-forward on
  stderr with a generic exit code, so there is no status to switch on. Match
  `non-fast-forward`, `fetch first`, and `Updates were rejected` — **not** a bare
  `! [rejected]`, which also covers `(stale info)`, `(would clobber existing tag)`, and
  protected-branch rejections that no rebase can fix.
- **Rebase-with-resolution is extracted once for `Sync` + `AutoPush`.** Task 1 lifts the
  shared block out of `Sync` so the manual and automatic paths cannot drift.
  `internal/telegram/sync.go#pullOnce` remains a third copy — it has its own injectable seams
  and error strings, and folding it in is out of scope for this plan.
- **Non-fatal everywhere.** A failed push never fails the mutation, never changes an exit code,
  and never rolls anything back. The commit is durable locally; the next auto-push or `s`
  catches up.
- **Dispatched after reload in the TUI**, via the existing `archiveSourceID` precedent, so the
  new task appears in the list immediately instead of after up to 10s of network I/O.
- **In the CLI, the push runs *after* the success line is printed**, matching `cmd/done.go`'s
  archive ordering. Substituting `commitAndPush` for `AutoCommit` in place would insert up to
  the full timeout of network I/O between the store write and the user-visible output — a
  visible hang in `monolog add` from Raycast or the Claude skill, exactly where the tool must
  feel instant. The CLI also uses a **shorter timeout** than the TUI, since a human is waiting
  on the process to exit.
- **Coalescing, not debouncing.** The Model tracks `pushInFlight`/`pushPending`; mutations
  landing during an in-flight push set `pushPending`, and exactly one follow-up push fires when
  the current one returns.
- **Silent on the happy path.** A status flash on every successful push would immediately
  overwrite `"Added: <title>"`. Follows the watcher precedent: flash only on error, and on a
  rebase (history was rewritten and remote tasks may have arrived).
- **On by default.** The bug is that pushing does not happen; an opt-in default would ship the
  broken behavior. `"auto_push": true` in config.json, plus `MONOLOG_NO_AUTOPUSH=1`.
- **Bot-side pull is freshness-gated and runs before every state-dependent command, with the
  ticker retained** (user decision). Pulling on none leaves up to `PullInterval` (30s) of
  staleness. Pulling ungated on every update would add a fetch per button tap. Gating
  `pullOnce` behind a `commandPullMaxAge` (5s) window gives a deliberate `/today` fresh data
  while a burst — tapping done on three tasks — costs one fetch, not three. The gate shares
  its `lastPull` clock with the ticker so the two paths cannot double-fetch.
- **Removing the ticker was considered and rejected** (user decision). Per-command pulls would
  cover freshness, but the ticker uniquely heals `readOnly` and retries the stuck-rebase
  recovery in `pullOnce` while the bot is idle. Without it, a `readOnly` bot that receives only
  a *write* would reject it and never heal, since the write path bails before pulling. Keeping
  both means `cfg.PullInterval` stays the ticker interval with unchanged semantics and no
  config migration.
- **Capture is the one command that does not pre-pull.** A new task gets a fresh ULID and cannot
  conflict with anything, `commitAndSync` already pulls *after* the write, and message-send is
  the most latency-sensitive path. Commands that read or act on **existing** state (browse,
  `done:`/`active:`/`view:` callbacks, note-replies) do pre-pull, because a task may have been
  completed or edited on the laptop since the last tick.
- A failed pre-pull serves local state rather than erroring — a command should never fail
  because the network blipped; the existing `readOnly` banner already communicates a pending
  conflict.
- **The Claude skill's captures push like any other mutation** (user decision). `SKILL.md`'s
  "stays on this machine until they sync" promise is rewritten rather than preserved. The
  distinction that survives: the skill still must **never run `monolog sync`** — that is a
  pull-and-rebase against the user's remote — while the implicit push inside `add`/`note` is
  now expected. `cmd/skill_test.go:244` pins `sync` in `skillProhibitedSubcommands` and keeps
  enforcing that; it does not pin the prose, so the rewrite is safe.

Fit with the existing system: one new function in `internal/git`, one mutex, one config
accessor, one shared `cmd` helper, one `tea.Cmd`. No new dependency, no new binary, no daemon.
The bot is untouched — its existing pull ticker picks the tasks up.

## Technical Details

**`internal/git/autopush.go`** (new)

```go
// PushResult summarizes what AutoPush did. Rebased/Resolved are meaningful
// even when AutoPush returns a non-nil error: the rebase may have rewritten
// local history before the retry push failed.
type PushResult struct {
    Pushed   bool // a push succeeded (possibly after a rebase)
    Rebased  bool // pull --rebase ran → local SHAs rewritten, undo/redo invalid
    Resolved int  // task-file conflicts auto-resolved during that rebase
    Skipped  bool // no remote or no upstream; no-op, not an error
}

// DefaultPushTimeout bounds the git push invocations in an AutoPush call.
// It does NOT bound the rebase fallback — see pullRebaseResolving.
const DefaultPushTimeout = 10 * time.Second

// CLIPushTimeout is the shorter budget used by one-shot CLI commands, where a
// human is waiting on the process to exit.
const CLIPushTimeout = 5 * time.Second

// ErrRebaseInProgress is returned when the repo is mid-rebase on entry.
var ErrRebaseInProgress = errors.New("repository is mid-rebase; resolve manually")

func AutoPush(repoPath string, timeout time.Duration) (PushResult, error)

// isNonFastForward reports whether combined git-push output indicates the push
// was rejected because the remote has commits we do not have.
func isNonFastForward(out string) bool

// hasUpstream reports whether the current branch has a configured upstream.
func hasUpstream(repoPath string) (bool, error)
```

**`internal/git/git.go`** (modified)

```go
// repoMu serializes every mutating git entry point in this package. The rebase
// fallback in AutoPush is not concurrency-safe against a plain commit, and the
// TUI runs each mutation and each push in its own goroutine. Per-process only:
// a second monolog process still degrades to .git/index.lock contention.
var repoMu sync.Mutex

// runOut is run's output-returning, context-aware sibling.
func runOut(ctx context.Context, dir, name string, args ...string) (string, error)

// pullRebaseResolving pulls with rebase (autostashing local modifications),
// auto-resolving task-file conflicts, and returns the number of resolved files.
// Extracted from Sync so AutoPush shares identical conflict semantics.
//
// Not context-aware: killing git mid-rebase would leave .git/rebase-merge
// behind, and nothing in this package recovers from that. Callers' timeouts
// therefore bound their pushes, not this call.
func pullRebaseResolving(repoPath string, autostash bool) (int, error)
```

`Sync` is refactored to call `pullRebaseResolving(repoPath, false)` — it commits pending
changes first, so it does not need the autostash. `AutoPush` passes `true`. `Sync`'s exported
signature, error strings, and `SyncResult` semantics are unchanged.

**`internal/config/config.go`** (modified)

- `autoPush bool` package var, default `true`.
- `func AutoPush() bool` — `false` when `os.Getenv("MONOLOG_NO_AUTOPUSH") == "1"` (exact match,
  matching `links.go:70` and `watcher.go:34`), else the loaded value.
- `Load`'s anonymous struct gains `AutoPush *bool` (`json:"auto_push,omitempty"`) — a
  **pointer** so an absent key keeps the `true` default while an explicit `false` disables.
- `resetAutoPushToDefaults()` at the top of `Load`, alongside the existing resets.
- **`Save` is not touched.** It is already read-modify-write over a `map[string]any`
  (`config.go:354-360`) — the reason `default_schedule` and `editor` survive today — so
  `auto_push` round-trips with zero changes. Writing the package var there would clobber an
  explicit on-disk `false` back to `true` in any process where `Load` did not run.

**`internal/git/git.go` `Init`** — the config.json template gains `"auto_push": true`.

**`cmd/helpers.go`** (modified)

```go
// autoPushFn is the seam tests replace to avoid real network I/O.
var autoPushFn = git.AutoPush

// pushAfter pushes when auto-push is enabled, warning failures to w and
// swallowing them: the commit is durable and the next push or `monolog sync`
// catches up. Call it AFTER the command's user-visible output, so network I/O
// never delays the success line (mirrors cmd/done.go's archive ordering).
func pushAfter(w io.Writer, repoPath string)
```

All six CLI mutation sites keep their existing `git.AutoCommit` call and gain a `pushAfter`
call below their `Fprintf`. `cmd/mv.go`'s commit lives in the `rebalanceAndCommit` helper
(:91-119) with no writer in scope, so the `pushAfter` call goes in the `RunE` body after the
helper returns, using `cmd.ErrOrStderr()`.

**`internal/tui/model.go`** (modified)

```go
// runAutoPush is the seam tests replace to avoid real network I/O, matching
// runEmailSync/emailClientBuilder in email.go.
var runAutoPush = git.AutoPush

type autoPushResult struct {
    rebased  bool
    resolved int
    err      error
}

func (m *Model) autoPushCmd() tea.Cmd // nil when disabled or already in flight
```

- `Model` gains `autoPushEnabled`, `pushInFlight`, `pushPending bool`. `autoPushEnabled` is
  snapshotted from `config.AutoPush()` **inside `newModel`**, next to the existing
  `ec := config.Email()` at `model.go:463` — `internal/tui` already imports `internal/config`
  (`model.go:21`), and the MUST-NOT-import rule in CLAUDE.md applies to `internal/email` and
  `internal/telegram`, not here. Threading it through `Options` (CLI launch flags only,
  `tui.go:25-27`) would silently default it **off** for every test constructing `Options{}`.
- In the `taskSavedMsg` handler, batch `m.autoPushCmd()` alongside `archiveCmd` when
  `msg.err == nil` and any of `msg.sha`/`msg.redoneSHA`/`msg.redoSHA` is non-empty. Undo and
  redo produce real commits that must reach the remote too.
- New `case autoPushResult` in `Update`:
  - clear `pushInFlight`; if `pushPending`, clear it and re-dispatch exactly one push
  - `rebased` → nil **both** stacks (SHAs rewritten), `reloadAll()` + `recomputeLayout()`
    (the rebase may have brought in remote tasks). **This runs whether or not `err` is nil.**
  - `err != nil` → `m.statusMsg = "push failed: <err>"`, local task state untouched
  - `rebased && err == nil` → flash `"Synced (auto-resolved N conflicts)"` / `"Synced"`
  - plain success → no flash, no reload

Processing flow (TUI add):

```
user submits add modal
  → addTaskCmd: store.Create + AutoCommitSHA  → taskSavedMsg{sha, focusID}
  → Update: push sha onto undoStack, reloadAll, focus new task, flash "Added: X"
  → Update batches autoPushCmd  (pushInFlight = true)
  → goroutine: git.AutoPush → git push
       ├─ ok                    → autoPushResult{}                     (silent; stacks intact)
       ├─ rejected → rebase, ok → autoPushResult{rebased, resolved}     (stacks cleared, reload)
       ├─ rejected → rebase, push fails → autoPushResult{rebased, err}  (stacks cleared, error)
       └─ offline               → autoPushResult{err}                   ("push failed: ...")
  → bot's next pull (≤ PullInterval, default 30s) sees the task
```

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this codebase - code changes, tests, documentation updates
- **Post-Completion** (no checkboxes): items requiring external action - manual testing, changes in consuming projects, deployment configs, third-party verifications

## Implementation Steps

### Task 1: Extract context-aware git helpers and shared rebase-with-resolution

**Files:**
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`

- [x] add `runOut(ctx context.Context, dir, name string, args ...string) (string, error)` — `exec.CommandContext` + `CombinedOutput`, returning the output string alongside the error so callers can classify failures
- [x] add private `pullRebaseResolving(repoPath string, autostash bool) (int, error)` containing the `PullRebase` → `IsRebasing` → `ResolveConflicts` → `RebaseContinue` / `RebaseAbort` block lifted from `Sync`, passing `--autostash` when requested
- [x] refactor `Sync` to call `pullRebaseResolving(repoPath, false)`, keeping its exported signature, error-wrapping strings, and `SyncResult` semantics unchanged
- [x] write tests for `runOut` (success returns output; failing command returns output + error; expired context returns an error)
- [x] write tests for `pullRebaseResolving` against a bare fixture remote (clean fast-forward returns 0; conflicting task file auto-resolved returns 1)
- [x] write a test that `autostash: true` succeeds with a modified tracked file present and restores that modification afterwards, while `autostash: false` fails on the same fixture
- [x] confirm the existing `Sync`/`ResolveConflicts` tests still pass unchanged (pure refactor — no behavior change)
- [x] run tests - must pass before task 2

### Task 2: Serialize all mutating git entry points behind one repo mutex

**Files:**
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`

- [ ] add package-level `repoMu sync.Mutex` with a comment stating why a push-only mutex is insufficient (concurrent `AutoCommitSHA` during an in-flight rebase → `index.lock` or a commit onto detached rebase HEAD) and that the lock is per-process
- [ ] acquire `repoMu` in `AutoCommit`, `AutoCommitSHA`, `Revert`, `RevertSHA`, and `Sync`, keeping every exported signature unchanged
- [ ] verify no lock is held across a call to another locking exported function (`RevertSHA` wraps `Revert` + `headSHA`, `AutoCommitSHA` wraps commit + `headSHA`) — restructure into unexported unlocked cores if a self-deadlock exists
- [ ] write a test that concurrent `AutoCommitSHA` calls from multiple goroutines all succeed and produce distinct commits (would flake on `index.lock` without the mutex)
- [ ] write a test that a concurrent `AutoCommitSHA` and `Sync` against a bare fixture remote both complete without error
- [ ] run tests with `-race` - must pass before task 3

### Task 3: Add git.AutoPush core (push, classify, skip, mid-rebase guard)

**Files:**
- Create: `internal/git/autopush.go`
- Create: `internal/git/autopush_test.go`

- [ ] create `internal/git/autopush.go` with `PushResult`, `DefaultPushTimeout` (10s), `CLIPushTimeout` (5s), and `ErrRebaseInProgress`
- [ ] implement `isNonFastForward(out string) bool` matching `non-fast-forward`, `fetch first`, and `Updates were rejected` case-insensitively — deliberately **not** a bare `! [rejected]`
- [ ] implement `hasUpstream(repoPath string) (bool, error)` via `git rev-parse --abbrev-ref @{upstream}`
- [ ] implement `AutoPush`: acquire `repoMu`; `IsRebasing` → `ErrRebaseInProgress`; `HasRemote` false or `hasUpstream` false → `PushResult{Skipped: true}, nil`; else `git push` via `runOut` under `context.WithTimeout` → `Pushed: true`
- [ ] return non-rejection push failures unchanged, with `Pushed: false` and no rebase attempted
- [ ] write a test for the happy path against a bare fixture remote (commit → AutoPush → commit present on remote, `Pushed` true, `Rebased` false)
- [ ] write tests for both skip cases: no remote, and a remote with no upstream on the current branch (`Skipped: true`, nil error, no output, no side effects)
- [ ] write a test that a repo left mid-rebase returns `ErrRebaseInProgress` and performs no push
- [ ] write table-driven tests for `isNonFastForward` (each accepted marker matches; auth/DNS/timeout output and a bare `! [rejected] (stale info)` do not)
- [ ] write a test that a bogus remote URL returns an error with `Pushed: false`
- [ ] run tests - must pass before task 4

### Task 4: Add the rebase fallback with autostash and a single bounded retry

**Files:**
- Modify: `internal/git/autopush.go`
- Modify: `internal/git/autopush_test.go`

- [ ] implement the rejection branch: set `Rebased: true` **before** recovery, call `pullRebaseResolving(repoPath, true)`, record `Resolved`, then retry the push exactly once (no loop)
- [ ] return `Rebased`/`Resolved` populated alongside a non-nil error when the retry push fails, so callers can tell that local history was rewritten
- [ ] return the rebase's own failure (with `Rebased: true`) when `pullRebaseResolving` fails, leaving the recovery decision to the caller
- [ ] write a test for the real rejection path: clone the bare remote twice, commit+push a different task file from clone B, commit in clone A, `AutoPush` from A → `Rebased: true`, `Pushed: true`, A's commit on the remote, B's task file present in A
- [ ] write a test for the conflicting-rejection path (both clones edit the same task file) → `Resolved: 1`, later-`UpdatedAt` version wins
- [ ] write a test for rebase-succeeded-but-retry-push-failed → `Rebased: true` **and** a non-nil error (drive it by making the remote unreachable between the two pushes, e.g. renaming the bare repo dir)
- [ ] write a test that a rejected push with a dirty tracked file still succeeds via autostash and leaves the dirty file modified afterwards
- [ ] run tests - must pass before task 5

### Task 5: Add auto_push config key with env override

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`

- [ ] add `autoPush` package var defaulting to `true`, plus `resetAutoPushToDefaults()` called at the top of `Load` alongside the existing email/telegram resets
- [ ] add `AutoPush *bool` (`json:"auto_push,omitempty"`, pointer so absent ≠ false) to `Load`'s anonymous config struct and apply it when non-nil
- [ ] add `func AutoPush() bool` returning `false` when `os.Getenv("MONOLOG_NO_AUTOPUSH") == "1"`, else the loaded value
- [ ] add `"auto_push": true` to the config.json template written by `git.Init`
- [ ] write tests for `Load` (absent key → true; explicit `false` → false; explicit `true` → true; malformed JSON → default true)
- [ ] write tests for the `MONOLOG_NO_AUTOPUSH=1` override via `t.Setenv` (overrides an on-disk `true`; unset → config value; a non-`1` value like `true`/`yes` does **not** disable, matching the existing escape hatches)
- [ ] write a test that `config.Save` round-trips an existing `auto_push` key untouched (asserting the no-change decision, and guarding against a future regression that starts writing it)
- [ ] update the `git.Init` test asserting config.json contents to expect the new key
- [ ] run tests - must pass before task 6

### Task 6: Wire auto-push into CLI mutation commands

**Files:**
- Modify: `cmd/helpers.go`
- Modify: `cmd/add.go`
- Modify: `cmd/edit.go`
- Modify: `cmd/done.go`
- Modify: `cmd/mv.go`
- Modify: `cmd/rm.go`
- Modify: `cmd/note.go`
- Modify: `cmd/helpers_test.go`

- [ ] add `var autoPushFn = git.AutoPush` and `pushAfter(w io.Writer, repoPath string)` to `cmd/helpers.go` — no-op when `config.AutoPush()` is false, else `autoPushFn(repoPath, git.CLIPushTimeout)`, warning failures to `w` and returning nothing
- [ ] add a `pushAfter(cmd.ErrOrStderr(), repoPath)` call **below** the success `Fprintf` in `add.go`, `edit.go`, `rm.go`, `note.go`, and below `done.go`'s existing archive block
- [ ] add the `pushAfter` call to `cmd/mv.go`'s `RunE` body after `rebalanceAndCommit` returns (its commit is inside the helper, which has no writer in scope)
- [ ] write tests for `pushAfter` with `autoPushFn` stubbed: called once with the repo path and `CLIPushTimeout` when enabled; not called when `config.AutoPush()` is false
- [ ] write tests for the failure path (stub returns an error → warning on `w`, no panic, commit still present in git log) and for `Skipped: true` producing no warning output
- [ ] write a table test over `add`/`edit`/`done`/`mv`/`rm`/`note` asserting the stubbed `autoPushFn` fires exactly once per command — the specific failure mode of a mechanical six-file edit
- [ ] write a test asserting the success line is printed even when the stubbed push blocks or fails (ordering regression guard)
- [ ] run tests - must pass before task 7

### Task 7: Wire auto-push into the TUI with coalescing

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`
- Modify: `internal/tui/email_test.go`

- [ ] add `var runAutoPush = git.AutoPush` seam and the `autoPushResult` msg type
- [ ] add `autoPushEnabled`/`pushInFlight`/`pushPending` to `Model`, snapshotting `config.AutoPush()` inside `newModel` next to `ec := config.Email()` (`model.go:463`) — **not** via `Options`
- [ ] implement `Model.autoPushCmd() tea.Cmd`: nil when disabled; when a push is in flight, set `pushPending` and return nil; else set `pushInFlight` and call `runAutoPush(repoPath, git.DefaultPushTimeout)` in the goroutine
- [ ] in the `taskSavedMsg` handler, batch `m.autoPushCmd()` alongside `archiveCmd` when `msg.err == nil` and any of `msg.sha`/`msg.redoneSHA`/`msg.redoSHA` is non-empty
- [ ] add `case autoPushResult`: clear `pushInFlight`; re-dispatch once if `pushPending`; when `rebased`, nil **both** stacks and `reloadAll()` + `recomputeLayout()` **regardless of `err`**; on `err` flash `"push failed: <err>"`; on `rebased && err == nil` flash `"Synced"` / `"Synced (auto-resolved N conflicts)"`; on plain success stay silent
- [ ] fix `internal/tui/email_test.go:669-677`, which asserts `archiveCmd().(archiveResult)` on the cmd returned by `Update` — `compactCmds` returns a lone cmd directly, so batching a second non-nil cmd makes it a `BatchMsg` and breaks the assertion; note in the test whether the model has auto-push enabled
- [ ] audit `model_test.go` for other assertions on the cmd returned by a `taskSavedMsg` `Update` and repair them the same way
- [ ] write tests that a successful mutation dispatches a push when enabled and dispatches none when disabled
- [ ] write tests for coalescing (a mutation during an in-flight push sets `pushPending` and fires exactly one follow-up push, not two)
- [ ] write tests for the handler: `err` flashes and preserves stacks; `rebased: true` clears both stacks and reloads; `rebased: true` **with** an error also clears both stacks; plain success leaves `statusMsg` untouched so `"Added: X"` survives
- [ ] write a test that undo and redo commits also trigger a push (`redoSHA`/`redoneSHA` paths)
- [ ] run tests with `-race` - must pass before task 8

### Task 8: Push Gmail-imported tasks

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `cmd/email.go`
- Modify: `internal/tui/email_test.go`
- Modify: `cmd/email_test.go`

- [ ] dispatch `m.autoPushCmd()` from the TUI's `emailSyncResult` handler (`model.go:1057`) when `Created > 0` — the email path returns `emailSyncResult`, not `taskSavedMsg`, so Task 7's trigger never fires for it
- [ ] add a `pushAfter(cmd.ErrOrStderr(), repoPath)` call after `email.Sync` returns in `cmd/email.go`, below its summary output
- [ ] keep the push in the callers, not in `internal/email` — that package MUST NOT import `internal/config` (CLAUDE.md) and its batch commit at `sync.go:128` stays as-is
- [ ] write a test that a TUI email sync creating tasks dispatches exactly one push, and that a zero-created sync dispatches none
- [ ] write a test that `monolog email sync` calls the stubbed `autoPushFn` once after a successful import
- [ ] run tests - must pass before task 9

### Task 9: Surface push state in the status bar and help

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] render a `↑` indicator in the status bar while `pushInFlight`, alongside the existing `↻` email-sync indicator (`model.go:2859-2860`) — the only feedback that a silent-on-success push exists, and the signal for "is it safe to close the laptop"
- [ ] update the `s` bottom-bar hint and the help overlay text to say the key is a full sync (pull + push) and that changes push automatically
- [ ] write a test asserting the indicator appears while `pushInFlight` and disappears afterwards
- [ ] write a test asserting the indicator is absent when auto-push is disabled
- [ ] update any existing status-bar/help snapshot assertions affected by the copy change
- [ ] run tests - must pass before task 10

### Task 10: Pull before serving a Telegram command (freshness-gated, ticker retained)

**Files:**
- Modify: `internal/telegram/handler.go`
- Modify: `internal/telegram/sync.go`
- Modify: `internal/telegram/sync_test.go`
- Modify: `internal/telegram/handler_test.go`

- [ ] add a `lastPull` timestamp to `Handler` (guarded by the existing `h.mu`, or an `atomic.Int64` of unix nanos) and a `commandPullMaxAge` constant (5s)
- [ ] add `Handler.pullIfStale(now time.Time) error` — calls the existing `pullOnce()` when `now.Sub(lastPull) > commandPullMaxAge`, else returns nil without touching the network
- [ ] update `lastPull` on **every** completed pull, including `runPullTicker`'s and the startup pull, so the ticker and on-demand paths share one clock and cannot double-fetch
- [ ] **leave `runPullTicker` and `cfg.PullInterval` untouched** — the ticker is retained as the background safety net that heals `readOnly` and retries stuck-rebase recovery while the bot is idle; `PullInterval` keeps its current meaning, so no config migration
- [ ] call `pullIfStale` before serving the browse commands (`/today`, `/week`, `/active`, `/all`) and before the callbacks and note-replies that act on existing tasks (`done:`, `active:`, `view:`, `collapse:`, reply=note) — every path that reads or mutates state that the laptop may have changed
- [ ] deliberately **skip** the pre-pull for plain capture (fresh ULID cannot conflict, `commitAndSync` already pulls after the write, and it is the most latency-sensitive path) — leave a comment saying so
- [ ] confirm no self-deadlock: `pullOnce` acquires `h.mu` itself, so `pullIfStale` must be called *outside* any handler section that already holds it, and the pull must complete before the reply is built (never hold `h.mu` across `SendMessage`)
- [ ] make a failed pull **non-fatal**: log to `opts.Writer` and serve from local state, matching the ticker's retry-on-next-tick behavior
- [ ] write a test that a command with a stale `lastPull` triggers exactly one `pullFunc` call, and that a second command within `commandPullMaxAge` triggers none
- [ ] write a test that a ticker pull refreshes `lastPull` such that an immediately following command skips its pull (shared-clock assertion)
- [ ] write a test that a `pullFunc` error still produces a normal reply and does not set `readOnly`
- [ ] write a test that plain capture does **not** pre-pull, while a `done:` callback does
- [ ] write a test that a task appearing in the remote between two commands shows up on the second once the gate expires
- [ ] run tests with `-race` - must pass before task 11

### Task 11: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] verify `MONOLOG_NO_AUTOPUSH=1` and `"auto_push": false` both fully disable pushing, and that a no-remote repo is unaffected (covered by Task 5 and Task 3 tests — confirm they assert it end-to-end)
- [ ] verify undo/redo still work after several auto-pushed mutations, and that a rebase-triggering push clears the stacks (covered by Task 7 tests — confirm coverage)
- [ ] verify no `internal/` import rules were violated: `internal/email` and `internal/telegram` still do not import `internal/config`
- [ ] run full test suite: `go test ./...`
- [ ] verify the end-to-end latency claim: a TUI-created task is visible to a Telegram browse command without waiting for the pull ticker
- [ ] run the suite with `-race`: `go test -race ./...` (this plan adds a mutex and concurrent goroutine paths)
- [ ] run lint: `go vet ./...`
- [ ] e2e tests: n/a (no UI e2e harness in this project — see Testing Strategy)

### Task 12: [Final] Update documentation

- [ ] add an **Auto-push on mutation** entry to CLAUDE.md's Key Design Decisions: the push-then-rebase-on-rejection algorithm, the narrow rejection classifier, `--autostash`, the single bounded retry, `repoMu`'s scope and its per-process limitation, the `ErrRebaseInProgress` guard, the `auto_push` key and `MONOLOG_NO_AUTOPUSH=1` escape hatch, the non-fatal contract, the CLI push-after-output ordering, and the undo/redo interaction (plain push preserves stacks; a rebase clears them **even when the retry push then fails**)
- [ ] note in CLAUDE.md that `pullRebaseResolving` is the shared rebase-with-resolution path for `Sync` and `AutoPush`, and that `internal/telegram/sync.go#pullOnce` remains a deliberate third copy
- [ ] update CLAUDE.md's Architecture line "Every mutation auto-commits to git" — it now also pushes
- [ ] rewrite `docs/claude-skill/SKILL.md:11` ("Nothing you capture reaches the user's other devices until they sync themselves") and the consequence sentence at `:125` ("what you capture stays on this machine until they sync") — both are now false. Keep **"Never run `monolog sync`"** intact and state the distinction: the implicit push inside `add`/`note` is expected; a pull-and-rebase against the user's remote is still theirs to run
- [ ] confirm `cmd/skill_test.go` still passes — `sync` stays in `skillProhibitedSubcommands` (:244); the prose is not pinned, but re-run the suite to be sure
- [ ] update `README.md:69` ("Every mutation auto-commits to git"), add `MONOLOG_NO_AUTOPUSH` to the env-var table (:266-284), and add `auto_push` to the config.json key list (:275-284)
- [ ] document the bot's pull cadence in CLAUDE.md's Telegram entry: startup pull + retained `runPullTicker` at `PullInterval`, **plus** the new freshness-gated `pullIfStale` before state-dependent commands, the `commandPullMaxAge` constant, the shared `lastPull` clock, and the deliberate capture exception
- [ ] update `docs/deploy/README.md` if it tells the user to sync manually for the bot to see changes
- [ ] **correct the stale EC2 wording** — the bot runs on the user's own always-on server; EC2 is one example host, not the deployment target. Generalize `README.md:469` ("full one-time EC2 setup checklist" → host-agnostic) and `:475` (`EC2_HOST=ec2-user@<elastic-ip>` → `DEPLOY_HOST=<user>@<host>`), `Makefile:16,71` comments, `CLAUDE.md:76` Deployment topology ("an EC2 t4g.nano" → "an always-on host (personal server, VPS, or Pi)" and "EC2 bootstrap" → "host bootstrap"; keep the `EC2_HOST` alias note, it is still true), `cmd/telegram.go:49` help text, `docs/deploy/README.md:43` (keep the EC2 row as one example among hosts) and `:210`, `docs/deploy/env.example:15` ("EC2-local SSH deploy key" → "host-local"), and the `internal/telegram/bot_test.go:307` comment
- [ ] leave `DEPLOY_HOST`/`EC2_HOST`/`BOT_ARCH` behavior untouched — the tooling is already host-neutral, so this task is wording only, no code or Makefile logic changes
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification:**

- Push the 3 commits currently sitting unpushed in `~/.monolog` before testing, so the first
  auto-push is not conflated with the existing backlog.
- Live end-to-end against the real `monolog-tasks` remote and the EC2 bot: create a task in the
  TUI, confirm it appears on GitHub without pressing `s`, then confirm `/today` in Telegram
  lists it within one `PullInterval`.
- Same round trip from the CLI (`monolog add`) and from a Raycast capture.
- Offline / captive-portal check: confirm `monolog add` still prints its success line promptly
  and the 5s CLI timeout is not felt as a hang, and that the TUI stays responsive.
- Cross-process race: run `monolog add` in a second terminal while the TUI is mid-push and
  confirm the worst case is an `index.lock` warning, not a lost task.

**Known remaining gap (out of scope for this plan):**

- This plan covers **laptop → remote** (Tasks 1-9) and **remote → bot** (Task 10). The third
  leg — a task captured *in Telegram* reaching the **laptop** — still requires pressing `s` or
  running `monolog sync`: the fsnotify watcher only sees local file writes, not new remote
  commits, and auto-push only pulls when a push is rejected. The natural follow-up is a
  periodic background pull ticker in the TUI mirroring the email ticker, or a `pullIfStale`
  equivalent on TUI focus. Deliberately not attempted here: it is a separate feature with its
  own conflict-handling and undo-stack-invalidation questions.
- `internal/telegram/sync.go#pullOnce` stays a third hand-rolled copy of the
  rebase-with-resolution dance. Folding it into `pullRebaseResolving` would need a
  seam-friendly shape and is deliberately not attempted here.

**External system updates:**

- No bot-side change is needed — `telegram.Serve`'s existing `PullInterval` ticker picks up the
  pushed commits. No redeploy required.
- Machines with an existing `~/.monolog/.monolog/config.json` will not have the `auto_push`
  key; the pointer-based default means they get auto-push enabled without editing anything.
- Behavior change for the Claude skill: its unprompted `--tags claude -s someday` captures now
  reach the remote (and the phone) without a manual sync. They stay out of the today/week views
  by virtue of the `someday` schedule, so the triage flow (`monolog ls --tag claude -s someday`)
  is unchanged.
