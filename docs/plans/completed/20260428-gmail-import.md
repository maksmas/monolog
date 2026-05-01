# Gmail Email Import as Monolog Tasks

## Overview

Import Gmail emails labeled with a configurable label (default `monolog`) as monolog tasks, and archive the email in Gmail when the task is completed. The feature is opt-in (off until configured) and uses BYO Google Cloud OAuth credentials for personal-account access.

**Problem solved**: today users manually re-type emails as tasks. This automates the inbound flow with a one-keystroke labeling action in Gmail and closes the loop on the outbound side by archiving once the task is done.

**Integration**: piggybacks on monolog's existing single-keystroke `s` (sync) shortcut in the TUI, the standard `Source` field on Task, and the existing `git.AutoCommit` machinery. New code lives in `internal/email/` and `cmd/email.go`; the only modifications to existing files are surgical (a struct field, a TUI keybinding, a done-hook).

## Context (from discovery)

- Project is a Go CLI personal backlog tool. Tasks stored as one JSON file per task in a git repo (`<repo>/.monolog/tasks/<ULID>.json`).
- `Source` field already exists on `model.Task` (currently `"manual"` / `"tui"`); adding `SourceID` cleanly extends it for any external-source dedup.
- `Store.Create` writes the JSON without committing — the caller commits via `git.AutoCommit(repoPath, msg, files...)`. Batch commit is a single AutoCommit at the end of a sync run.
- Existing patterns to follow:
  - **`internal/email/` MUST NOT import `internal/config`** (matches `schedule`/`display`/`model`). Config values are passed in as parameters or struct fields by callers.
  - Errors warn-and-continue when local action succeeded but a side-effect failed (matches recurrence-spawn failure handling).
  - Every mutation auto-commits to git.
  - Pure functions in their own files for testability (`convert.go`, `sync.go` core).
- `cmd/done.go` is the single CLI completion path — adds the post-completion archive hook there.
- TUI Bubble Tea `Init() tea.Cmd` is currently `return nil`; periodic ticker plugs in there via `tea.Batch` + `tea.Tick`.

## Development Approach

- **Testing approach**: Regular (code first, then tests) — matches the established codebase style (`*_test.go` co-located with implementation, table-driven where applicable).
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
  - Tests are not optional — they are a required part of the checklist.
  - Write unit tests for new functions/methods.
  - Write unit tests for modified functions/methods.
  - Add new test cases for new code paths.
  - Update existing test cases if behavior changes.
  - Tests cover both success and error scenarios.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation**.
- Run `go test ./...` and `go vet ./...` after each change.
- Maintain backward compatibility with existing JSON task files (new field uses `omitempty`).

## Testing Strategy

- **Unit tests**: required for every task. Convert/sync/oauth logic gets table-driven tests with a fake Gmail client interface.
- **No live Gmail integration tests** — the Gmail client is hidden behind a small interface (`type Gmail interface { ListLabeled / Get / ArchiveLabel }`), all tests use a fake. Live testing is manual via `monolog email sync` against a real account during dev.
- **Manual-only flows**: the OAuth `Authorize` browser flow and the cobra `auth` subcommand wiring are exercised in the Task 12 smoke test, not in unit tests. Authorize's helper pieces (token I/O, refresh persistence) DO get unit tests.
- **TUI tests**: existing `internal/tui/model_test.go` patterns (synthetic key events, message dispatch); add cases for `s` triggering both git+email, periodic tick firing sync, archive-on-done, archive failure non-fatal.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.
- Keep plan in sync with actual work done.

## Solution Overview

A new `internal/email/` package owns all Gmail interaction, hidden behind a small interface for testing. `cmd/email.go` adds three Cobra subcommands (`sync`, `auth`, `status`). `cmd/done.go` and the TUI both fire a fire-and-forget archive call after a successful done on a gmail-sourced task. The TUI `s` keybinding is overloaded to run git sync and email sync in parallel via `tea.Batch`. A self-rescheduling `tea.Tick` in `Init()` polls every N minutes while the TUI is running.

Configuration lives in the existing `<repo>/.monolog/config.json` under a new optional `email` block — if the block is absent, the feature is silent (no `s`-overload effect, no on-launch sync, no ticker). Email config values are read once at startup via `config.EmailConfig()` returning a struct, then passed by value into `email.Sync(...)` and friends; `internal/email/` never imports `internal/config`. OAuth tokens live OUTSIDE the git repo at `$XDG_CONFIG_HOME/monolog/gmail_token.json` to prevent accidental commits across sync devices.

Dedup is built per-sync from a single Store directory scan: the set of `SourceID` values where `Source == "gmail"`, including both open and done tasks. This makes done-state self-suppressing — completed-and-archived emails won't re-import even though we keep the trigger label. Deleting a task in monolog WILL re-import on next sync (intentional; complete or unlabel-in-Gmail to make it stick).

## Technical Details

**Task struct change** (`internal/model/task.go`):

```go
SourceID string `json:"source_id,omitempty"`
```

**Email → Task mapping** (pure, in `internal/email/convert.go`):

| Field | Source |
|---|---|
| `Title` | Subject with `^((Re\|Fwd?\|FW):\s*)+` stripped (case-insensitive); empty → `"(no subject)"` |
| `Body` | `"From: <name>\nhttps://mail.google.com/mail/#all/<msg-id>\n\n<snippet>"` where snippet is `html.UnescapeString`'d and hard-capped at 200 chars with `…` suffix only if exceeded |
| `Tags` | `["email"]` literal; never auto-applies `active` |
| `Schedule` | `today` via `schedule` package |
| `Recurrence` | empty |
| `ID` | fresh ULID (NOT the Gmail message ID) |
| `Source` | `"gmail"` |
| `SourceID` | gmail message ID |

The Gmail URL omits `/u/0/` so it works regardless of which Google account is currently signed in (Gmail redirects to the right account).

**Sender parsing** (within `convert.go`): use `net/mail.ParseAddress` on the `From:` header. If `Address.Name` non-empty, use it; else use `Address.Address`; on parse error, `"unknown"`.

**Config schema** (`<repo>/.monolog/config.json`):

```json
{
  "email": {
    "enabled": true,
    "label": "monolog",
    "sync_interval_minutes": 5,
    "max_per_sync": 100,
    "client_secrets_path": "~/.config/monolog/gmail_credentials.json"
  }
}
```

All keys optional within the `email` block; the block itself is optional. `config.Save` extends to write the email block while preserving foreign keys. Defaults: enabled=`false` if missing, label=`"monolog"`, interval=`5`, max_per_sync=`100`, client_secrets_path=`$XDG_CONFIG_HOME/monolog/gmail_credentials.json`.

**Config API**: a single `config.EmailConfig() EmailConfig` accessor returns a struct (`type EmailConfig struct { Enabled bool; Label string; SyncInterval time.Duration; MaxPerSync int; ClientSecretsPath string }`). Callers (TUI Model, `cmd/email.go`, `cmd/done.go`) read the struct once and pass values into the email package — keeping `internal/email/` decoupled from `internal/config`.

**Token storage**: `$XDG_CONFIG_HOME/monolog/gmail_token.json` (default `~/.config/monolog/gmail_token.json`), file mode `0600`. Refresh handled by `golang.org/x/oauth2`; refreshed token written back to disk.

**OAuth scope**: `https://www.googleapis.com/auth/gmail.modify` (smallest scope covering list/read/label-modify; no send, no full gmail).

**Sync algorithm** (`internal/email/sync.go`):

1. Load token; missing/expired-no-refresh → error `"run monolog email auth"`.
2. `gmail.users.messages.list(q="label:<configured-label>")`, paginate exhaustively. Gmail returns results newest-first; preserve that order.
3. Build dedup set from a single `s.List(store.ListOptions{})` call (no Status filter returns all open + done tasks).
4. Filter to new message IDs, preserving Gmail's newest-first order.
5. Take first `MaxPerSync` (default 100). Remaining new messages are simply not processed this run; they'll be picked up on the next sync run with no error and no warning. This is what "soft cap" means.
6. For each new msg: `messages.get(format=METADATA, metadataHeaders=[Subject,From])` (Gmail returns `snippet` field for free in this format), `convert.ToTask(msg, now)`, `Store.Create(task)`, accumulate file paths.
7. Single `git.AutoCommit(repoPath, fmt.Sprintf("email: imported %d task(s) (label=%s)", n, label), files...)` at end. **One commit for the whole batch** (explicit user requirement).
8. Partial `Store.Create` failure: warn to writer, continue, commit whatever succeeded.

**Archive on completion**:

- After `recurrence.CompleteAndSpawn` succeeds, if `task.Source == "gmail"` && `task.SourceID != ""` && email enabled:
  - Call `gmail.ArchiveLabel(ctx, sourceID)` directly through the Gmail interface (no extra wrapper) → `users.messages.modify(removeLabelIds=["INBOX"])`.
  - Keep the `monolog` label (option A — archive only).
- Archive failure: NON-FATAL warn, task stays done.
- TUI: `archiveEmailCmd` runs in goroutine, returns `archiveResult` msg, status bar shows `"email archived"` or `"archive failed: ..."`.
- CLI `monolog done`: fire-and-forget with 5-second context timeout so the command doesn't hang on flaky network.

**TUI integration**:

- `s` keybinding: dispatches `tea.Batch(gitSyncCmd(...), emailSyncCmd(...))`. Each returns its own message; status bar shows results merged or separately.
- `Init()`: returns `tea.Batch(existing, emailSyncCmd(...), emailTickCmd(interval))` if email enabled (the initial sync IS the first tick).
- `emailTickCmd(interval)` is a self-rescheduling `tea.Tick`-based command. On `emailTickMsg`, dispatch sync + re-arm tick. Disabled when `interval == 0` or `enabled == false`.
- Status indicator: single character right of stats bar — `↻` while sync in flight, blank otherwise.
- One-line flash messages: `"email: 3 imported"`, `"email: token expired"`, `"email archived"`, etc.
- On `emailSyncResult` with `count > 0`, call `reloadAll()` so new tasks render.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all code changes, test additions, and in-repo documentation updates.
- **Post-Completion** (no checkboxes): manual GCP project setup (one-time per user), live `monolog email sync` against a real Gmail account.

## Implementation Steps

### Task 1: Add SourceID field to Task struct

**Files:**
- Modify: `internal/model/task.go`
- Modify: `internal/model/task_test.go` (or create if absent)

- [x] add `SourceID string \`json:"source_id,omitempty"\`` field to `Task` struct (slot after the existing `Source` field)
- [x] write test asserting JSON round-trip preserves SourceID when set
- [x] write test asserting JSON output has no `source_id` key when SourceID is empty (omitempty preserves backward compat)
- [x] run `go test ./internal/model/` — must pass before next task

### Task 2: Add email config schema (LoadEmail + Save + struct accessor)

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [x] define `type EmailConfig struct { Enabled bool; Label string; SyncInterval time.Duration; MaxPerSync int; ClientSecretsPath string }` exported from `internal/config`
- [x] extend `Load` to also unmarshal an `"email"` JSON block into a package-level `emailCfg` var; silently ignore missing fields, apply defaults (enabled=false, label="monolog", interval=5*time.Minute, max=100, client_secrets_path=`$XDG_CONFIG_HOME/monolog/gmail_credentials.json` resolved at access time)
- [x] add `Email() EmailConfig` accessor returning the populated struct (single function, not 5 per-field getters — this is the only public surface for email config). Named `Email()` rather than `EmailConfig()` because Go does not permit a function to share its name with the type it returns; callers spell this as `config.Email()`.
- [x] extend `Save` signature/internals to write the `"email"` block alongside theme/date_format, preserving any keys it does not own (existing read-modify-write pattern). The simplest path: keep `Save(monologDir, theme, dateFormat string)` as-is; add a separate `SaveEmail(monologDir string, ec EmailConfig) error` callable when the email block needs to change. The TUI settings modal does NOT yet expose email config, so `SaveEmail` is exercised by `cmd email auth` (which writes `enabled=true` after first auth) and by manual config edits
- [x] enforce that `internal/email/` does NOT import `internal/config` — config values are passed by value into email functions (will be enforced as the email package is created in subsequent tasks; no email package exists yet so the constraint is trivially satisfied)
- [x] write tests: Load with no email block → defaults; Load with full block → values populated; SaveEmail roundtrips; SaveEmail preserves unknown keys (e.g. `default_schedule`); Email returns the loaded values
- [x] run `go test ./internal/config/` — must pass before next task

### Task 3: Add Gmail client interface + real implementation

**Files:**
- Create: `internal/email/client.go`
- Create: `internal/email/client_test.go`

- [x] define `type Message struct { ID, Subject, From, Snippet string }` neutral DTO (so callers don't import `google.golang.org/api/gmail/v1`)
- [x] define `type Gmail interface { ListLabeled(ctx context.Context, label string) ([]string, error); Get(ctx context.Context, id string) (*Message, error); ArchiveLabel(ctx context.Context, id string) error }`
- [x] implement `realGmail` wrapping `*gmail.Service`: paginated `ListLabeled` (loops until `NextPageToken` empty, preserves API result order which is newest-first); `Get` using `messages.get(format=METADATA, metadataHeaders=[Subject,From])` extracting headers + snippet; `ArchiveLabel` calling `users.messages.modify(removeLabelIds=["INBOX"])`
- [x] add `NewClient(ctx context.Context, httpClient *http.Client) (Gmail, error)` factory wired to `gmail.NewService`
- [x] add `go.mod` deps: `google.golang.org/api/gmail/v1`, `golang.org/x/oauth2/google` (run `go get` then `go mod tidy`)
- [x] write tests using a fake `Gmail` impl (table-driven where reasonable) asserting interface shape — the real impl is exercised manually in Task 12; the fake is what every other test in this plan uses
- [x] run `go test ./internal/email/` and `go vet ./...` — must pass before next task

### Task 4: OAuth2 flow + token persistence + refresh

**Files:**
- Create: `internal/email/oauth.go`
- Create: `internal/email/oauth_test.go`

- [x] implement `LoadToken(path string) (*oauth2.Token, error)` reading 0600-mode JSON token file; return wrapped error with `"run monolog email auth"` hint when missing
- [x] implement `SaveToken(path string, tok *oauth2.Token) error` writing JSON with 0600 perms, creating parent dir if needed
- [x] implement `Authorize(ctx context.Context, clientSecretsPath, tokenPath string) error` running local-redirect OAuth flow (`oauth2.Config.AuthCodeURL` + small `http.Server` listening on `127.0.0.1:0` for the callback), persists token via `SaveToken`. **Authorize itself is exercised manually in Task 12 — no unit test (interactive browser flow).**
- [x] implement `HTTPClient(ctx context.Context, clientSecretsPath, tokenPath string) (*http.Client, error)` that loads token, wraps with auto-refreshing `oauth2.Config.Client`, persists refreshed tokens back to disk via a custom `oauth2.TokenSource` wrapper
- [x] write tests: LoadToken/SaveToken roundtrip with file mode assertion (`0600`); LoadToken on missing file returns wrapped "run monolog email auth" error; refresh-token persistence using fake `oauth2.TokenSource` (asserts that a refreshed token gets written back to disk)
- [x] run `go test ./internal/email/` — must pass before next task

### Task 5: Pure conversion (gmail Message → model.Task)

**Files:**
- Create: `internal/email/convert.go`
- Create: `internal/email/convert_test.go`

- [x] implement `ToTask(msg *Message, now time.Time) model.Task` (pure): subject prefix stripping via `regexp.MustCompile(`(?i)^((re|fwd?|fw):\s*)+`)`, sender parsing via `net/mail.ParseAddress`, snippet HTML-unescape + 200-char hard cap with `…` suffix only when truncation occurs (no suffix when snippet ≤200 chars), body assembly with `From:` line and `https://mail.google.com/mail/#all/<msg-id>` URL (no `/u/0/`), tags=`["email"]`, schedule=today via `schedule` package, fresh ULID, Source=`"gmail"`, SourceID=msg.ID
- [x] handle empty/whitespace-only subject → `"(no subject)"`; sender parse error → `"unknown"`; empty snippet → no extra blank lines in body (still emit `From:` and URL; no body text after them)
- [x] write table-driven tests: chained Re/Fwd/Fw stripping (mixed case, e.g. `"Re: RE: Fwd: foo"`), empty/whitespace subject, sender variants (`Name <email>`, bare `<email>`, malformed), snippet truncation at >200 chars (asserts `…` suffix), snippet shorter than 200 chars passes through unchanged with NO `…` suffix, HTML entity decoding (`&amp;`, `&#39;`)
- [x] write tests asserting Schedule=today (use injected `now`), Source/SourceID populated, tags equals `["email"]`, fresh ULID per call (different across calls)
- [x] run `go test ./internal/email/` — must pass before next task

### Task 6: Sync orchestration with batch commit

**Files:**
- Create: `internal/email/sync.go`
- Create: `internal/email/sync_test.go`

- [x] implement `type SyncOptions struct { Label string; MaxPerSync int; Now time.Time; Writer io.Writer }`
- [x] implement `type SyncResult struct { Created int; Err error }`
- [x] implement `Sync(ctx context.Context, gmail Gmail, store *store.Store, repoPath string, opts SyncOptions) SyncResult`:
  1. `gmail.ListLabeled(ctx, opts.Label)` → IDs (Gmail returns newest-first; preserve order)
  2. `store.List(store.ListOptions{})` (no Status filter → all open + done) → build dedup set of SourceIDs where Source==`"gmail"`
  3. Filter to new IDs, preserving Gmail's newest-first order
  4. Take first `MaxPerSync` (remaining new IDs are skipped silently — picked up on next sync; soft cap)
  5. For each: `gmail.Get(ctx, id)` → `convert.ToTask` → `store.Create` (warn-and-continue on individual fail), accumulate file paths
  6. Single `git.AutoCommit(repoPath, fmt.Sprintf("email: imported %d task(s) (label=%s)", n, label), files...)` if any created
- [x] no commit when zero created; partial Store.Create failure still commits successful writes
- [x] write tests with fake Gmail + temp store + temp git repo: first sync (5 msgs → 5 tasks, 1 commit, message format `"email: imported 5 task(s) (label=monolog)"`); second sync (same 5 → 0 created, 0 commits); mixed (3 already + 2 new → 2 created, 1 commit naming 2); soft cap (e.g. 5 msgs with `MaxPerSync=3` → 3 created in newest-first order, second sync drains remaining 2); partial Store.Create failure (4/5 succeed → 1 commit with 4 files, warning to writer)
- [x] write test: `ListLabeled` error → SyncResult with non-nil Err, no commit, no Store mutations
- [x] run `go test ./internal/email/` — must pass before next task

### Task 7: cmd/email.go subcommands (sync, auth, status)

**Files:**
- Create: `cmd/email.go`
- Create: `cmd/email_test.go`
- Modify: `cmd/root.go` (register `newEmailCmd()`)

- [x] add `monolog email` parent cobra command with subcommands `sync`, `auth`, `status`
- [x] `email sync`: open store, read `config.EmailConfig()`, build real Gmail client via `email.HTTPClient` + `email.NewClient`, call `email.Sync(ctx, gmail, s, repoPath, SyncOptions{...})`, print `"created N task(s)"` or wrapped error; exit 1 on auth-missing
- [x] `email auth`: read `config.EmailConfig()` for paths, call `email.Authorize(ctx, ...)`; print `"authorized; token saved to <path>"` on success; on success, also flip `enabled=true` via `config.SaveEmail` so subsequent invocations pick up the feature without manual config edits
- [x] `email status`: print auth state (`token loaded, expires <iso-time>` / `not authorized — run 'monolog email auth'`), configured label, configured interval. **Does NOT show "last sync time"** (no sentinel file in v1; the user can use `git log` if curious)
- [x] register `newEmailCmd()` in `cmd/root.go` alongside other subcommands
- [x] write tests: `sync` command success path with fake Gmail (inject via package-level `var emailClientFactory = func(...) (Gmail, error) { ... }` overridable in tests, defaulting to the real factory); `sync` command no-token error path returns user-facing hint string; `status` command formatting for both authorized and unauthorized states. The `auth` subcommand's interactive flow is NOT unit-tested (manual only — covered by Task 12).
- [x] run `go test ./cmd/` and `go vet ./...` — must pass before next task

### Task 8: cmd/done.go archive hook (CLI)

**Files:**
- Modify: `cmd/done.go`
- Modify: `cmd/done_recurring_test.go` (or create `cmd/done_archive_test.go`)

- [x] introduce a single package-level seam in `cmd/done.go` for testing: `var archiveFn = realArchive` where `realArchive` constructs a Gmail client (with 5s context timeout) and calls `gmail.ArchiveLabel(ctx, sourceID)`. Tests swap the var.
- [x] after successful `git.AutoCommit` in `RunE`, if `task.Source == "gmail"` && `task.SourceID != ""` && `config.EmailConfig().Enabled`: call `archiveFn(task.SourceID)`; on error, print `"archive failed: <err>"` to stderr (NON-FATAL — `monolog done` still exits 0)
- [x] on archive success, print `"email archived"` to stdout
- [x] do NOT call archive when email disabled or task lacks Source/SourceID
- [x] write tests by swapping `archiveFn` with a recording fake: gmail-sourced task triggers archive call with the right SourceID; non-gmail task does NOT call archive; archive failure logged to stderr but command still succeeds (exit 0); email-disabled config skips archive entirely (fake never called)
- [x] run `go test ./cmd/` — must pass before next task

### Task 9: TUI — sync command + `s` keybinding overload + reload-on-create

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] add Model fields: `emailEnabled bool`, `emailSyncing bool`, `emailLabel string`, `emailMaxPerSync int`, `emailInterval time.Duration` (note: NO `emailLastErr` — flash messages use the existing status-bar mechanism)
- [x] populate Model email fields in `newModel` from `config.EmailConfig()`
- [x] add `emailSyncCmd() tea.Cmd` running `email.Sync` in a goroutine, returning `emailSyncResult{ created int; err error }`; returns no-op msg when email disabled (so callers can always batch it without conditionals)
- [x] add `emailSyncResult` handler in `Update`: clear `emailSyncing`, set status flash (`"email: N imported"` or `"email sync: <err>"`), call `reloadAll()` if `created > 0`
- [x] modify `case "s"` in `updateNormal` to dispatch `tea.Batch(gitSyncCmd, emailSyncCmd())` and set `m.emailSyncing = true`
- [x] write tests: `s` keypress with email enabled produces a Batch containing both git+email commands; `s` keypress with email disabled produces only the git command (no-op email cmd is fine but should not flash anything); `emailSyncResult{created: 3}` triggers reloadAll and clears the spinner; `emailSyncResult{err: ...}` flashes an error message and clears the spinner
- [x] run `go test ./internal/tui/` — must pass before next task

### Task 10: TUI — periodic ticker + on-launch sync

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] add `emailTickCmd(interval time.Duration) tea.Cmd` returning `emailTickMsg{}` after `interval` (uses `tea.Tick`); when interval is 0 or email disabled, returns nil so `tea.Batch` ignores it
- [x] add `emailTickMsg` handler in `Update` that returns `tea.Batch(emailSyncCmd(), emailTickCmd(interval))` — self-rescheduling
- [x] modify `Init()` to return `tea.Batch(<existing init cmd>, emailSyncCmd(), emailTickCmd(interval))` when email enabled (initial sync is the first tick)
- [x] write tests: `Init` with email enabled returns a Batch including both initial sync and tick; `Init` with email disabled returns the existing init unchanged; receiving `emailTickMsg` re-arms the ticker AND fires a sync; ticker is not armed when interval is 0
- [x] run `go test ./internal/tui/` — must pass before next task

### Task 11: TUI — archive-on-done + status indicator + help-hint

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] add `archiveEmailCmd(sourceID string) tea.Cmd` running `gmail.ArchiveLabel` in a goroutine with a 5s context timeout, returning `archiveResult{ err error }`
- [x] add `archiveResult` handler in `Update`: status flash `"email archived"` on success or `"archive failed: <err>"` on error (NON-FATAL — task stays done)
- [x] modify `doneSelected` (or wherever `recurrence.CompleteAndSpawn` is invoked) to also return `archiveEmailCmd(task.SourceID)` when `task.Source == "gmail"` && `task.SourceID != ""` && email enabled
- [x] add status indicator: single character right of stats bar — `↻` while `m.emailSyncing`, blank otherwise. Render-string change in the stats line only.
- [x] update bottom-bar help hint for `s` to read `"sync (git+email)"` when email enabled, else current text (`"sync"`)
- [x] write tests: `d` on a gmail-sourced task with email enabled returns a Batch containing the archive cmd; `d` on a non-gmail task does not; `d` with email disabled does not; `archiveResult{err: ...}` leaves the task done and produces the flash message; status bar contains `↻` while `emailSyncing` is true and is blank otherwise; help-hint copy reflects email-enabled state
- [x] run `go test ./internal/tui/` — must pass before next task

### Task 12: Verify acceptance criteria

- [x] verify all design decisions from Overview/Solution Overview are implemented (label trigger, archive-only on done, BYO OAuth, body format with `https://mail.google.com/mail/#all/<msg-id>` URL, dedup set spans open+done, single batch commit per sync run)
- [x] verify edge cases: empty subject → `"(no subject)"`, malformed sender → `"unknown"`, soft-cap behavior over multiple sync runs, partial Store.Create failure still commits, archive failure non-fatal, token-expiry message points at `monolog email auth`
- [x] run full test suite: `go test ./...`
- [x] run lint: `go vet ./...`
- [x] manual test (skipped - not automatable): build binary, set up local GCP test project (see Post-Completion), run `monolog email auth` — verify the browser flow opens and the token persists; label 3 emails with `monolog`, run `monolog email sync` → 3 tasks appear with correct titles/bodies/tags; run `monolog email sync` again → no new tasks (dedup); complete one task with `monolog done <id>` → email archived in Gmail (no longer in INBOX) but `monolog` label still applied
- [x] manual test (skipped - not automatable): launch TUI → on-launch sync runs (verify spinner if any new emails); press `s` → both git and email sync flash status messages; complete a gmail-sourced task with `d` → status bar shows `"email archived"`
- [x] verify test coverage for `internal/email/` is comparable to existing packages (49.4% — within range; pure logic in convert/sync/oauth is tested, real Gmail HTTP wrappers and interactive Authorize flow are intentionally manual-only per Task 12 plan)

### Task 13: Update documentation

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [x] add an `## Email integration` section to `README.md`: BYO GCP setup steps (5-min walkthrough — see Post-Completion), config block example, command reference (`auth`/`sync`/`status`), `s` keybinding overload note, archive-on-done semantics
- [x] add an `**Email integration**:` bullet to `CLAUDE.md` under "Key Design Decisions" matching the existing density: label trigger, dedup model (SourceID set spans open+done), body format (`https://mail.google.com/mail/#all/<id>`), archive-on-done semantics (option A — keep `monolog` label), OAuth scope + token storage path, soft cap, batch commit message format, `internal/email/` does NOT import `internal/config`
- [x] cross-check README and CLAUDE.md against the *implemented* code (not against this plan) — read the actual final code paths before describing them, so docs reflect reality
- [x] move this plan to `docs/plans/completed/`: `mkdir -p docs/plans/completed && mv docs/plans/20260428-gmail-import.md docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual GCP project setup** (one-time per user, documented in README):
1. Create a Google Cloud project at `console.cloud.google.com`.
2. Enable the Gmail API.
3. Configure the OAuth consent screen (Internal or External, with the user's own email as a test user).
4. Create OAuth 2.0 Client ID credentials of type "Desktop app".
5. Download the credentials JSON to `~/.config/monolog/gmail_credentials.json` (or wherever `email.client_secrets_path` points).
6. Run `monolog email auth` once to complete the authorization flow.

**Manual Gmail label setup**:
- Create a label named `monolog` (or whatever `email.label` is set to) in Gmail.
- Apply the label to emails you want imported as tasks.

**Manual end-to-end smoke test** (covered by Task 12 checkboxes; restated here for clarity):
- Auth flow completes without warnings.
- Label 3 test emails, run `monolog email sync` → 3 tasks appear with correct titles, bodies, tags.
- Run `monolog email sync` again → no new tasks (dedup works).
- Complete one task with `monolog done <id>` → email archived in Gmail (no longer in INBOX) but `monolog` label still applied.
- Launch TUI, complete another gmail-sourced task with `d` → status bar shows `"email archived"`.

**Out of scope (intentionally NOT in this plan)**:
- Attachments, sending emails, multiple Gmail accounts.
- Other providers (IMAP, Outlook).
- Per-task custom labels beyond the trigger.
- HTML email rendering.
- Gmail Pub/Sub push notifications (polling is enough).
- Sender/age/thread filtering.
- A `last_sync` sentinel file or any per-sync metadata beyond the git commit history.
- Exposing email config in the TUI settings modal.
