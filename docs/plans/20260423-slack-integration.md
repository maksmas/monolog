# Slack Integration: Auto-Ingest Saved Messages

## Overview

When the user saves a message for later in Slack (native `:bookmark:` / "Saved items" feature), it should automatically appear as a monolog task. When that task is completed in monolog (`d` in TUI, `monolog done <id>` in CLI), the corresponding Slack message should be unsaved via `stars.remove`.

**Problem solved:** today the user juggles Slack's "Saved items" list alongside mlog. Two inboxes, two completion models. This integration makes mlog the single source of truth — Slack-saved messages flow in automatically, completion in mlog propagates back.

**Integration points:**
- TUI: polls in background on startup and every `slack.poll_interval_seconds` (default 60); `s` key triggers git-sync + slack-poll in parallel; `d` key triggers unsave for slack-sourced tasks.
- CLI: `monolog slack-login` / `slack-logout` / `slack-status` / `slack-sync` commands; `monolog done <id>` also triggers unsave.
- No daemon, no cron dependency. `slack-sync` command exists for headless / cron-user cases.

## Context (from discovery)

**Files/components involved:**
- `internal/model/task.go` — add `SourceID` field
- `internal/store/store.go` — add batched create (`CreateBatch` or similar) to avoid one-git-commit-per-task on bulk ingest
- `internal/config/config.go` — add Slack config block accessors + token reader
- `internal/git/git.go` — `Init` needs to write `slack_token` into `.gitignore`
- `internal/recurrence/spawn.go` — clear `SourceID` on spawned tasks (defensive)
- `internal/tui/model.go` — tick scheduling, poll integration on `s` key, unsave on `d` key, Slack status bar plumbing
- `cmd/` — four new commands: `slack_login.go`, `slack_logout.go`, `slack_status.go`, `slack_sync.go`; existing `cmd/done.go` gets the CLI unsave
- `internal/slack/` — **new package** (client + ingest)

**Patterns observed in existing code:**
- Every mutation commits to git via `git.AutoCommit` (CLI) or `git.AutoCommitSHA` (TUI for undo).
- TUI async work: return a `tea.Cmd` that spawns a goroutine; goroutine sends a `tea.Msg` back (e.g. `taskSavedMsg`); `Update` handles on main goroutine. Existing example: `syncCmd` at `internal/tui/model.go:2474`.
- Task struct (`internal/model/task.go:14`) already has `Source string json:"source"` — values today are `"tui"` and `"manual"`. New `"slack"` value slots in cleanly.
- Config: `config.Load(monologDir)` reads `config.json` once per process start. Unknown keys preserved on `config.Save` rewrite (read-modify-write).
- No third-party HTTP clients or SDKs; mlog uses stdlib `net/http` exclusively.

**Dependencies identified:** none new. Stdlib `net/http` + `encoding/json` + `os/exec` (for opening the browser in `slack-login`).

## Development Approach

- **Testing approach:** Regular (code first, then tests in the same task). Matches the project convention from CLAUDE.md: "Every change must include tests. All tests must pass before merging."
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
  - Unit tests for all new functions/methods.
  - Update existing tests when behavior changes.
  - Cover success and error paths.
- **CRITICAL: all tests must pass before starting the next task** — no exceptions. Run `go test ./...` and `go vet ./...` after each task.
- Update this plan file when scope changes during implementation.
- Keep changes focused; no drive-by refactors.

## Testing Strategy

- **Unit tests**: required for every task.
- **Integration tests**: `internal/slack/` uses `httptest.Server` to simulate Slack's API — no real network. CLI commands wire the client via an injectable factory so tests point at the test server.
- **TUI tests**: use the existing `key()` helper pattern in `internal/tui/model_test.go` to synthesize `slackTickMsg` / `slackFetchedMsg` and assert scheduling + store writes. No real HTTP in TUI tests.
- **No e2e tests in this project** (CLI tool, no browser UI), so no Playwright/Cypress step.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- If implementation deviates from this plan, update the plan before proceeding.

## Solution Overview

**Architecture:**

```
Slack API (stars.list, stars.remove, auth.test)
         ^
         |
+--------+----------+
| internal/slack    |   Client + Ingest + Options (pure)
|  - Client         |
|  - SavedItem      |
|  - Ingest(store,  |
|    items, synced, |
|    opts)          |
+--------+----------+
         ^
         |
+--------+----------+         +------------+
| cmd/slack_*.go    |<--------| config.Slack()
| cmd/slack_sync    |         | config.SlackToken()
+-------------------+         +------------+
         ^
         |
+--------+----------+
| internal/tui      |   slackTickCmd / slackPollNowCmd / slackUnsaveCmd
|  Model.slackSynced|   map[string]bool  seeded at newModel, maintained
|  slackErrSilenced |   string (rate-limit rule)
+-------------------+
```

**Key design decisions:**
- **Source/SourceID dedup** — reuse existing `Source` field (today: `"tui"`, `"manual"`); add sibling `SourceID` (omitempty); Slack tasks have `Source="slack"` and `SourceID="<channel_id>/<message_ts>"`. In-memory `map[string]bool` seeded at TUI startup from existing tasks; avoids scanning all files every tick.
- **First-run "sync everything"** — no watermark, ingest all `stars.list` pages. Batch into ONE git commit (`slack: ingest N items`) via a new `store.CreateBatch` to avoid 500 commits on first run.
- **Self-rescheduling tick** — next tick is scheduled only *after* the current poll's result message lands in `Update`. A slow poll never causes concurrent polls to stack.
- **Writes stay on main goroutine** — `Store` is not mutex-guarded. Goroutines only do network I/O + return messages; all `store.Create*` calls run inside `Update`.
- **Unsave is best-effort** — no retry queue. First failure shown in status bar; identical consecutive failures suppressed until a success resets the state. CLI unsave synchronous with 5s timeout; TUI unsave asynchronous.
- **No keyring** — token in env var `MONOLOG_SLACK_TOKEN` (preferred) or git-ignored file `<MONOLOG_DIR>/.monolog/slack_token` (mode 0600).

## Technical Details

**⚠️ Verify API surface before coding.** Slack's "Saved items" / "Later" feature has gone through several API iterations. As of plan authoring, `stars.list` / `stars.remove` / `stars.add` remain the documented endpoints for user-token saved-message access — but **before starting Task 6, hit `api.slack.com/methods` and capture golden JSON fixtures from real `curl` calls against the target workspace** (see Task 6 checklist). If Slack has transitioned the saved-for-later feature to `saved.*` / `later.*` / `bookmarks.*`, update the client accordingly — the plan structure (client + ingest + commands) stays the same, only endpoint names and response shapes change.

**`stars.list` response (abridged, as documented at plan time):**
```json
{
  "ok": true,
  "items": [
    {
      "type": "message",
      "channel": "C0123",
      "message": {
        "ts": "1712345678.000100",
        "text": "remember to review the PR",
        "user": "U0456",
        "thread_ts": "1712345670.000001",
        "permalink": "https://myteam.slack.com/archives/C0123/p1712345678000100"
      },
      "date_create": 1712345679
    }
  ],
  "response_metadata": { "next_cursor": "..." }
}
```

Notes:
- `items[].type` can be `"message"`, `"file"`, `"channel"`, etc. — filter to `"message"` only.
- `items[].message.permalink` is present; if absent, construct as `https://<workspace>.slack.com/archives/<channel>/p<ts_no_dot>`.
- If `thread_ts != ts`, it's a threaded reply; we include only the reply text (decision from brainstorm).
- Files/attachments in `message.files[]` are skipped.
- Channel name (for tag): `stars.list` returns only channel ID. Resolve via `conversations.info` once per unique channel per poll cycle (in-cycle cache). Same for author display name via `users.info`.

**`stars.remove` request:** `POST https://slack.com/api/stars.remove` with form body `channel=C0123&channel_timestamp=1712345678.000100` and `Authorization: Bearer xoxp-...`. (Current Slack accepts either `timestamp` or `channel_timestamp`; use `channel_timestamp` which targets the saved message specifically.)

**Idempotent errors treated as success:** `not_starred`, `already_unstarred`, `message_not_found`.

**`auth.test` response:** returns `team` (workspace subdomain) and `url` (full workspace URL). Extract subdomain for `config.slack.workspace`.

**Task field population (ingest):**
```
Title:     firstLine(text) truncated to 80 chars (rune-safe)
Body:      text + "\n\n— @" + authorName + " in #" + channelName + ", " + displayTime + "\n\n" + permalink
Tags:      ["slack"] if channel_as_tag=false
           ["slack", channelName] if channel_as_tag=true
Schedule:  "today"
Source:    "slack"
SourceID:  channel + "/" + ts
Position:  next position in today bucket (same as TUI add flow)
```

**Config schema additions:**
```json
{
  "date_format": "02-01-2006",
  "theme": "default",
  "slack": {
    "enabled": true,
    "poll_interval_seconds": 60,
    "workspace": "myteam",
    "channel_as_tag": true
  }
}
```

Unknown top-level keys preserved on rewrite.

**TUI message types (new):**
- `slackTickMsg{}` — ticker fires; triggers a poll.
- `slackFetchedMsg{items []slack.SavedItem, err error}` — poll result; `Update` ingests.
- `slackIngestedMsg{newCount int}` — ingest complete; `Update` refreshes list + updates status bar.
- `slackUnsaveDoneMsg{err error}` — unsave result; `Update` updates status bar.

## What Goes Where

- **Implementation Steps** (checkboxes): code + tests entirely in this repo.
- **Post-Completion** (informational): manual verification steps that can't be automated (actual Slack workspace login, real-world polling behavior over hours).

## Implementation Steps

### Task 1: Add `SourceID` field to Task model

**Files:**
- Modify: `internal/model/task.go`
- Modify: `internal/model/task_test.go` (file already exists)

- [x] Add `SourceID string \`json:"source_id,omitempty"\`` to `Task` struct alongside existing `Source`.
- [x] Write test: `Task` with `Source="slack"` and `SourceID="C123/1712345.678"` round-trips through JSON marshal/unmarshal with the correct key name and omitempty behavior when empty.
- [x] Write test: existing tasks without the field unmarshal with empty `SourceID`.
- [x] Run `go test ./internal/model/...` and `go vet ./...` — must pass before Task 2.

### Task 2: Clear `SourceID` on recurrence spawn

**Files:**
- Modify: `internal/recurrence/spawn.go`
- Modify: `internal/recurrence/spawn_test.go`

- [x] At the new-task construction site (currently `Source: old.Source,` at `spawn.go:60`), leave `Source` copy as-is but explicitly do not copy `SourceID` (defaults to empty).
- [x] Write test: given an old task with `Source="slack"` and `SourceID="C/ts"` and a recurrence rule, the spawned task has `Source="slack"` (provenance copied) and `SourceID=""` (cleared).
- [x] Run `go test ./internal/recurrence/...` — must pass before Task 3.

### Task 3: Add `store.CreateBatch` for bulk ingest

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

- [x] Add `Store.CreateBatch(tasks []model.Task, commitMessage string) error` — writes all N task files atomically-ish (each via existing `writeTask`), then calls `git.AutoCommit` once with the batch message covering all `.monolog/tasks/*.json` paths.
- [x] On any write failure mid-loop OR on `git.AutoCommit` failure after all N writes succeeded, attempt to remove all files written in this batch (best-effort rollback, rollback errors discarded) before returning the original error. Goal: a failed batch leaves no `.json` files in the working tree that `git status` would pick up.
- [x] On empty input slice: no commit, no error, return nil.
- [x] Tests: success path (N tasks → N files + 1 commit with exact message); empty batch (no commit, no error); write failure mid-batch (leaked files cleaned up); `git.AutoCommit` failure after all writes (files rolled back); verify commit message matches caller-provided.
- [x] Run `go test ./internal/store/...` — must pass before Task 4.

### Task 4: Config accessors for Slack block + token reader

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [x] Add `SlackConfig` struct (exported): `Enabled bool`, `PollIntervalSeconds int`, `Workspace string`, `ChannelAsTag bool`.
- [x] Add `config.Slack() SlackConfig` with defaults: `Enabled=false` (no token ⇒ disabled), `PollIntervalSeconds=60`, `ChannelAsTag=true`. Extend `Load` to parse the `slack` block into a package-level `slackCfg` var (same pattern as `dateFormat`).
- [x] Add `config.SlackToken() (token string, source string, err error)` — parameterless, reads per call (matches the `Theme()` pattern at `config.go:167`). Returns env var `MONOLOG_SLACK_TOKEN` if set (source="env"), else reads `<MONOLOG_DIR>/.monolog/slack_token` (resolving `MONOLOG_DIR` the same way `Theme()` does), trimming whitespace (source="file"). Returns `("", "", nil)` if neither (disabled state).
- [x] Add `config.SetSlackWorkspace(monologDir, workspace string) error` — read-modify-write preserving other keys; updates `slack.workspace`. Follows the pattern of `Save`.
- [x] Add `config.SetSlackEnabled(monologDir string, enabled bool) error` — toggles `slack.enabled`.
- [x] Tests: `Slack()` returns defaults when block absent; returns parsed values when present; `SlackToken` prefers env var; falls back to file; returns empty on neither; `SetSlackWorkspace` round-trips through Save and preserves unknown keys (e.g. a dummy `"editor"` key added by the test).
- [x] Run `go test ./internal/config/...` — must pass before Task 5.

### Task 5: Add `slack_token` to `.gitignore` on git init + upsert helper

**Files:**
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`

- [x] Update `git.Init` (`git.go:21`) to write `.gitignore` with two lines: `# monolog gitignore\nslack_token\n` instead of the current single line.
- [x] Add `git.EnsureGitignoreEntry(repoPath, entry string) error` — idempotent: reads existing `.gitignore`, checks if `entry` (whole line) is present, appends with newline if missing; no-op otherwise. Used by `slack-login` to upgrade repos initialized before this feature.
- [x] Tests: `Init` creates `.gitignore` containing `slack_token`; `EnsureGitignoreEntry` appends new entry; calling twice is idempotent; preserves existing entries.
- [x] Run `go test ./internal/git/...` — must pass before Task 6.

### Task 6: Slack HTTP client (`internal/slack/client.go`)

**Files:**
- Create: `internal/slack/client.go`
- Create: `internal/slack/client_test.go`

- [x] Define `type Client struct { Token string; BaseURL string; HTTP *http.Client }` where `BaseURL` defaults to `https://slack.com/api` (injectable for tests) and `HTTP` defaults to `&http.Client{Timeout: 10*time.Second}`.
- [x] Define `type SavedItem struct { Channel, ChannelName, TS, Text, AuthorID, AuthorName, Permalink string; ThreadTS string; HasFiles bool }`.
- [x] Implement `Client.AuthTest(ctx) (workspace string, err error)` — `POST /auth.test`, parse `ok` + `team` (subdomain via URL parsing on `url` field, falling back to `team`).
- [x] Implement `Client.ListSaved(ctx) ([]SavedItem, error)` — `POST /stars.list?limit=100`, follow `response_metadata.next_cursor` until empty, filter `type=="message"`, resolve channel names via in-cycle cache calling `conversations.info`, resolve author names via `users.info` (both cached across paginated calls in the same `ListSaved` invocation). Treat `has_more=false` / empty cursor as end of pages. Set `Permalink` from `message.permalink` when present, else construct from workspace (passed via client config or stored on client — use a `Client.Workspace` field).
- [x] Implement `Client.Unsave(ctx, channel, ts string) error` — `POST /stars.remove` form-encoded with `channel`, `channel_timestamp`. Treat errors `not_starred`, `already_unstarred`, `message_not_found` as success (return nil). Return a typed error `ErrMissingScope` when the API returns `missing_scope` / `invalid_auth` so callers can show a distinct status message.
- [x] Define a single typed sentinel `var ErrMissingScope = errors.New("slack: missing scope")` — callers match via `errors.Is` and show the distinct "run monolog slack-login" remedy. All other API errors are returned as `fmt.Errorf("slack %s: %s", method, rawError)` — one remedy ("try again"), no taxonomy needed. (Collapse from the previously-proposed 4-error set; YAGNI.)
- [x] Honor `429` rate-limit with the `Retry-After` header: return the raw error wrapped (no retry, no sentinel; polling cadence is already slow enough for Tier 3).
- [x] fixtures hand-crafted from documented API shapes (no real token available in execution environment)
- [x] Tests (`httptest.Server` serving testdata fixtures):
  - `AuthTest` extracts workspace subdomain from a real `url` like `https://myteam.slack.com/`.
  - `ListSaved` paginates across 2 pages and returns the concatenated items.
  - `ListSaved` filters out `type != "message"`.
  - `ListSaved` resolves channel name and author name via cached `conversations.info` / `users.info` (test server counts requests, asserts 1 per unique ID even when the same channel appears 5 times).
  - `ListSaved` on mid-pagination error returns `(partial items, error)` — caller can decide whether to use partial data; `Ingest` will skip.
  - `ListSaved` constructs permalink when `message.permalink` absent.
  - `Unsave` sends correct form body with `channel_timestamp`.
  - `Unsave` returns nil for `not_starred`, `already_unstarred`, `message_not_found`.
  - `Unsave` returns `ErrMissingScope` (matchable via `errors.Is`) for Slack error `missing_scope` and `invalid_auth`.
  - All methods return a descriptive error when `ok=false` with an unrecognized Slack error.
- [x] Run `go test ./internal/slack/...` — must pass before Task 7.

### Task 7: Ingest function (`internal/slack/ingest.go`)

**Files:**
- Create: `internal/slack/ingest.go`
- Create: `internal/slack/ingest_test.go`

- [x] Define `type Options struct { ChannelAsTag bool; DateFormat string; Now func() time.Time }`. `Now` defaulted in caller to `time.Now` (tests inject).
- [x] Implement `BuildTask(item SavedItem, opts Options) model.Task` — pure function producing the Task struct per the mapping in Technical Details. Title rules:
  - First non-blank line of `item.Text` (skip leading empty lines).
  - If resulting title exceeds 80 runes, truncate at 79 runes and append `…` (single rune, total 80).
  - Rune-safe everywhere (no byte slicing of multi-byte chars).
  - If text is entirely empty/whitespace after trimming, title falls back to `"(empty message)"` to avoid blank-title tasks.
- [x] Body composition: pass `item.Text` through **verbatim** — no Slack-markup decoding in the first cut. `<@U0456>` / `<#C0123|name>` / `<https://…|label>` / HTML entities render literally. Document this as a known limitation. Follow text with blank line + `— @<authorName> in #<channelName>, <displayTime>` + blank line + permalink.
- [x] Implement `Ingest(s *store.Store, items []SavedItem, synced map[string]bool, opts Options) (newCount int, err error)`:
  - For each item, compute `key = item.Channel + "/" + item.TS`.
  - Skip if `synced[key]`.
  - Skip items where `item.Channel` starts with `D` (DM) or `G`/`mpdm-` (group DM / multi-party DM) — log to stderr `slack: skipping DM bookmark <ts>` and move on. DMs are out of scope for v1 (tag semantics unclear, author-as-channel-name is confusing).
  - Call `BuildTask(item, opts)`, assign a new ULID via `model.NewID`, compute position using `ordering.NextPosition(todayTasks)` for the FIRST new item and increment by the ordering-package default spacing (1000) for each subsequent item in the batch.
  - Accumulate new tasks in a slice.
  - If slice non-empty, call `store.CreateBatch(tasks, fmt.Sprintf("slack: ingest %d items", len(tasks)))`.
  - After successful commit, populate `synced[key]=true` for each ingested key.
  - Return `(len(tasks), nil)`; on commit failure, return `(0, err)` without updating `synced`.
- [x] **Missing channel-name fallback** (`conversations.info` returns `channel_not_found` / `not_in_channel`): use the raw channel ID (e.g. `C0123`) as channel name. Ugly but truthful. Same for author fallback (use user ID). Handled inside `Client.ListSaved` so `Ingest` never sees empty names.
- [x] Tests:
  - `BuildTask` — title: 80-rune boundary (no ellipsis), 81-rune (ellipsis at 80), multi-byte emoji at boundary, leading-blank-line message, whitespace-only message (`"(empty message)"`); body format matches spec verbatim including Slack markup passthrough; `channel_as_tag=true` → `["slack", channelName]`; `=false` → `["slack"]`; threaded reply includes only reply text; attachments dropped (HasFiles=true but nothing leaks into body).
  - `Ingest` — dedup: items whose key is in `synced` are skipped; new items passed through; `synced` map updated after successful commit; empty-input returns `(0, nil)` with no commit.
  - `Ingest` — DM skip: items with `C` channel prefixed `D`/`G`/`mpdm-` are skipped silently.
  - `Ingest` — commit failure path: `synced` unchanged when `store.CreateBatch` errors.
  - `Ingest` — positions: first ingested task gets `NextPosition(today)`, subsequent tasks increment by 1000.
- [x] Run `go test ./internal/slack/...` — must pass before Task 8.

### Task 8: `monolog slack-login` wizard

**Files:**
- Create: `cmd/slack_login.go`
- Create: `cmd/slack_login_test.go`
- Modify: `cmd/root.go` (register command)

- [x] Implement `cmd/slack-login` using cobra. No positional args, no flags (minimal first cut).
- [x] Wizard flow (in `RunE`):
  1. Print multi-line instructions: create a Slack app, add user scopes `stars:read`, `stars:write`, install to workspace, copy User OAuth Token.
  2. Attempt to open `https://api.slack.com/apps?new_app=1` via `exec.Command` with `open` (darwin), `xdg-open` (linux), or `cmd /c start` (windows); on failure, just print the URL.
  3. Prompt `Paste User OAuth Token (xoxp-...): ` reading from stdin with `bufio.Scanner` (trimmed). **Token is echoed to the terminal** — we do not add `golang.org/x/term` to keep dep count minimal. Print a one-line hint before the prompt: `(token will be visible in this terminal — close scrollback afterward if that's a concern)`.
  4. If empty, exit with error.
  5. Create `slack.Client{Token: token}` and call `AuthTest(ctx)` — on failure, print error and exit non-zero.
  6. On success, write token to `<MONOLOG_DIR>/.monolog/slack_token` with mode `0600`.
  7. Call `git.EnsureGitignoreEntry(repoPath, "slack_token")` to upgrade older repos.
  8. Call `config.SetSlackWorkspace(monologDir, workspace)` to persist subdomain.
  9. Call `config.SetSlackEnabled(monologDir, true)`.
  10. Print success message: `Connected to <workspace>.slack.com. Polling will start next time you open the TUI.`
- [x] Make the browser opener and the token reader injectable via package-level vars (like a `var openBrowser = defaultOpenBrowser` and `var readToken = defaultReadToken`) so tests can substitute them.
- [x] Tests:
  - Happy path: injected reader returns a dummy token; `httptest` Slack server returns `ok:true` with `url:"https://myteam.slack.com/"`; after run, token file written (0600), config has `slack.workspace="myteam"`, `slack.enabled=true`, `.gitignore` contains `slack_token`.
  - Empty token input → error, no file written, config unchanged.
  - `auth.test` returns `invalid_auth` → error, no file written, config unchanged.
- [x] Run `go test ./cmd/...` — must pass before Task 9.

### Task 9: `monolog slack-logout` and `monolog slack-status` commands

**Files:**
- Create: `cmd/slack_logout.go`
- Create: `cmd/slack_logout_test.go`
- Create: `cmd/slack_status.go`
- Create: `cmd/slack_status_test.go`
- Modify: `cmd/root.go` (register both)

- [x] `slack-logout`: delete `<MONOLOG_DIR>/.monolog/slack_token` if present; call `config.SetSlackEnabled(monologDir, false)`; print `Slack disconnected.` If file doesn't exist, print `Already disconnected.` (exit 0).
- [x] `slack-status`: print workspace (from `config.Slack().Workspace`), token source (from `config.SlackToken()` return: "env" / "file" / "none"), enabled state, and a count of on-disk tasks with `Source=="slack"` as `ingested so far: N`. Do NOT print a "last sync" line (the TUI-session-only state isn't worth the noise — trimmed on reviewer YAGNI feedback).
- [x] Tests for each: logout removes the file and updates config; status prints the expected fields from a fixture config + fixture task set.
- [x] Run `go test ./cmd/...` — must pass before Task 10.

### Task 10: `monolog slack-sync` CLI command (headless)

**Files:**
- Create: `cmd/slack_sync.go`
- Create: `cmd/slack_sync_test.go`
- Modify: `cmd/root.go` (register)

- [x] `slack-sync`: construct `slack.Client` from `config.SlackToken` (error out with clear message if none); construct dedup cache by scanning existing tasks with `Source=="slack"`; call `Client.ListSaved`; call `slack.Ingest`; print `Ingested N new task(s).` or `No new items.`.
- [x] Exit 0 even when 0 items (so cron jobs don't log errors). Exit non-zero only on API errors or ingest errors.
- [x] Inject `slack.Client` / base URL via the cobra command's context to allow tests to point at `httptest`.
- [x] Tests:
  - End-to-end against httptest: 3 items in `stars.list`, empty store → 3 tasks created with correct fields, 1 batched commit.
  - Second run with no new items → exit 0 with `No new items.` and no commit.
  - Second run with 1 of 3 items removed from Slack but 1 new added → only the new one is ingested (existing 2 still in store are dedup'd via scan; the removed one isn't forgotten; the new one creates a 4th task).
  - No token configured → exit non-zero with clear error `Slack not configured. Run monolog slack-login.`
  - `MONOLOG_DIR` points at a non-git directory → exit non-zero with error `monolog: not a git repo — run monolog init first` (propagated from `store.CreateBatch` → `git.AutoCommit` failure).
- [x] Run `go test ./cmd/...` — must pass before Task 11.

### Task 11: TUI — background polling (tick + startup + `s` key)

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] Add Model fields: `slackSynced map[string]bool`, `slackClient *slack.Client` (nil when disabled), `slackLastErr string` (for rate-limited error suppression), `slackPollInterval time.Duration`.
- [x] In `newModel` (or equivalent init), after tasks load: walk tasks once and populate `slackSynced` from every task with `Source=="slack" && SourceID!=""`. Read `config.Slack()`; if `Enabled && token != ""`, build `slackClient`; else leave nil.
- [x] Add `(m *Model) slackTickCmd(d time.Duration) tea.Cmd` wrapping `tea.Tick(d, func(time.Time) tea.Msg { return slackTickMsg{} })`. Only returns a real command when `slackClient != nil`; else returns `nil` (no-op).
- [x] Add `(m *Model) slackPollCmd() tea.Cmd` — goroutine calls `slackClient.ListSaved(ctx)` with a 30s timeout; returns `slackFetchedMsg{items, err}`.
- [x] In `Model.Init`, include `m.slackTickCmd(0)` in the batch so the first poll fires immediately on startup.
- [x] In `Update`, handle `slackTickMsg` → return `m.slackPollCmd()` (the next tick is scheduled from `slackFetchedMsg` after ingest finishes — self-rescheduling).
- [x] In `Update`, handle `slackFetchedMsg`:
  - On err: update status bar (first occurrence: show `Slack: <reason>`; identical error as `m.slackLastErr`: suppress). Re-schedule next tick via `m.slackTickCmd(m.slackPollInterval)`.
  - On success: clear `m.slackLastErr`; call `slack.Ingest(m.store, items, m.slackSynced, opts)` on main goroutine; on newCount>0 set status bar `Slack: N new` and call `m.reloadAll()`; on 0 be silent. Re-schedule next tick.
  - **Cursor preservation**: `m.reloadAll()` after ingest should preserve the user's current cursor position (focus the same task ID as before ingest, or first task of current tab if the focused task is gone). Prevents the cursor from jumping when new Slack items land mid-browse.
  - **Processing in non-normal modes**: ingest always runs regardless of current mode (search overlay, add modal, settings, detail panel). This matches the existing `taskSavedMsg` behavior — async events are never queued. Add-modal input state lives in `Model.addTitle` / `Model.addTags` / etc., which `reloadAll()` does NOT touch, so the user's in-progress typing is preserved. Add a test for this.
- [x] Modify the `s` key handler: `return m, tea.Batch(m.syncCmd(), m.slackPollCmd())` — parallel git sync + immediate slack poll. (Pollcmd resets the tick indirectly through its own fetchmsg path.)
- [x] Tests (using existing `key()` helper and synthetic messages):
  - `newModel` with `Source=="slack"` tasks present seeds `slackSynced` correctly.
  - Dispatching `slackTickMsg` when `slackClient == nil` returns no command (disabled).
  - Dispatching `slackFetchedMsg` with items ingests via store and updates `slackSynced`.
  - Dispatching `slackFetchedMsg` with error sets `slackLastErr` and status bar.
  - Second identical `slackFetchedMsg` error does NOT re-flash status bar (rate-limit).
  - `slackFetchedMsg` success after an error clears `slackLastErr`.
  - Pressing `s` returns a `tea.Batch` that includes both sync and slack poll commands (inspect via test-internal hook, or assert on a combined message sequence).
- [x] Run `go test ./internal/tui/...` — must pass before Task 12.

### Task 12: TUI + CLI — Slack unsave on completion

**Files:**
- Modify: `internal/tui/model.go` (doneSelected path)
- Modify: `internal/tui/model_test.go`
- Modify: `cmd/done.go`
- Modify: `cmd/task_commands_test.go` (or `cmd/done_test.go` if it exists; create if not)

- [x] TUI `doneSelected`: **chain the unsave after the done commit** (not parallel). Mechanism: `doneSelected` returns only the existing done-commit command. When `Update` handles the resulting `taskSavedMsg`, if the completed task had `Source=="slack" && SourceID!="" && m.slackClient!=nil`, it dispatches `slackUnsaveCmd(task)` as the next command via the normal `tea.Cmd` return. This ensures the git commit lands before the HTTP call goes out — simpler to reason about and avoids a race where the user hits `u` between commit and HTTP response. To carry the slack info across, extend `taskSavedMsg` with `slackUnsaveChannel string` and `slackUnsaveTS string` fields (populated by the done goroutine when the task was slack-sourced).
- [x] The unsave goroutine itself: parses `SourceID` into `channel, ts` (split on `/`), calls `slackClient.Unsave(ctx, channel, ts)` with 5s timeout; result becomes `slackUnsaveDoneMsg{err}`.
- [x] Handle `slackUnsaveDoneMsg` in `Update`: on nil err, silent; on `ErrMissingScope`, status bar `Slack unsave needs stars:write — run monolog slack-login`; on other error, status bar `Slack unsave failed: <reason>` (rate-limited the same way as poll errors — share `slackLastErr`? use a separate field `slackUnsaveLastErr` to avoid cross-silencing). Keep separate.
- [x] CLI `cmd/done.go`: after existing done logic, if `task.Source=="slack" && task.SourceID!=""` and `config.SlackToken` returns a token, build `slack.Client` and call `Unsave` synchronously with 5s timeout. Print any non-`nil`-non-`idempotent` error as a warning to stderr (`monolog: slack unsave failed: ...`), but exit 0 — completion itself succeeded.
- [x] Do not trigger unsave on `rm`, edit, schedule changes, or undo. Only on the `done` state transition.
- [x] Make CLI's Slack client injectable (same pattern as `slack-sync`) for tests.
- [x] Tests:
  - TUI: marking a `Source="slack"` task done dispatches `slackUnsaveCmd`; non-slack task does not.
  - TUI: `slackUnsaveDoneMsg{err: slack.ErrMissingScope}` sets the "needs stars:write" status.
  - TUI: `slackUnsaveDoneMsg{err: nil}` leaves status bar unchanged.
  - CLI: `monolog done <id>` on a slack task (with httptest Slack) calls `stars.remove` with correct `channel`+`ts`; on a non-slack task, no HTTP call is made.
  - CLI: slack unsave failure prints a stderr warning but exits 0.
  - Recurrence + slack: completing a recurring slack task spawns a new task with `SourceID=""` (covered by Task 2's test, but add integration coverage here).
- [x] Run `go test ./...` — must pass before Task 13.

### Task 13: Verify acceptance criteria

- [x] Every decision from the Overview is implemented: saved-message ingest with correct mapping; dedup via Source+SourceID; in-memory cache; first-run ingests all with a single commit; token via env var or git-ignored file; `slack-login` / `slack-logout` / `slack-status` / `slack-sync` commands; TUI poll on startup + tick + `s` key; unsave on TUI `d` and CLI `done` but not on `rm` / edit / undo; error rate-limiting; config schema additions.
- [x] Run full test suite: `go test ./...`.
- [x] Run `go vet ./...`.
- [x] Build binary: `go build -o monolog` and run `./monolog --help` and `./monolog slack-status` to smoke-test command registration.
- [x] Review diff for dead code, accidental debug logs, stale comments.

### Task 14: Documentation and plan archival

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Move: `docs/plans/20260423-slack-integration.md` → `docs/plans/completed/`

- [x] README: add a "Slack integration" section under features explaining `slack-login` setup and that saved items sync automatically when the TUI is open; mention `MONOLOG_SLACK_TOKEN` env var; mention `monolog slack-sync` for cron users.
- [x] CLAUDE.md: add a bullet under **Key Design Decisions** in the dense style of existing entries (name types, functions, files, exact behaviors). Cover: Source/SourceID dedup with in-memory cache keyed by `channel/ts`, `slack.Ingest` batching through `store.CreateBatch`, the chained unsave-after-done via `taskSavedMsg` extension, self-rescheduling tick pattern, token storage rules (env var preferred, git-ignored file fallback), and that only `d` / CLI `done` trigger unsave (not rm / edit / undo).
- [x] Move deferred until after review phases (will archive in the finalize step)

## Accepted Limitations

Decisions documented here so an implementer does not re-debate them mid-task:

- **Slack API deprecation risk** — `stars.*` is the documented endpoint at plan time; if Slack sunsets it in favor of `saved.*` / `later.*`, this integration will need a replacement client. Monitor `api.slack.com/changelog`.
- **Undo after unsave is a one-way trip** — if the user presses `d` on a slack-sourced task and then `u` to undo, the git revert restores the task in mlog but the Slack `stars.remove` has already executed. No automatic re-save. User can manually re-save in Slack if they care.
- **Config changes require TUI restart** — `poll_interval_seconds`, `enabled`, token changes, etc. are read by `newModel`. Changing them mid-session has no effect on the running TUI. Quit and relaunch to pick up new values.
- **TUI + CLI `slack-sync` should not run concurrently** — both will call `git.AutoCommit`; concurrent commits in the same working tree can race. Recommend running one or the other. Not guarded in code (cross-process mutex is out of scope for a personal tool).
- **Slack markup renders literally** — `<@U0456>`, `<#C0123|name>`, `<https://…|label>`, and HTML entities (`&lt;` / `&amp;`) appear verbatim in task bodies. A markup-to-plaintext pass is out of scope for v1; user can clean up via `monolog note` or in the YAML edit flow.
- **DMs are skipped** — bookmarks on DM channels (`D...`) and group DMs (`G...` / `mpdm-*`) are not ingested. Tag semantics are unclear (no channel name; author-as-channel is confusing). May be revisited if users hit the limitation.
- **Title format under 80 runes** — long titles are truncated at 79 runes with a trailing `…`. Rune-safe; no mid-codepoint splits.
- **No `stars:read` → `stars:write` split handling** — if the user grants only `stars:read` during login, `auth.test` still succeeds and the TUI polls fine; the first `d` on a slack-sourced task fails with `ErrMissingScope` and surfaces the "re-run slack-login" remedy.
- **First-run "sync everything" can be large** — on first configure with N existing saved items, one batched `slack: ingest N items` commit. No pagination-level throttling beyond Slack's own rate limit.

## Post-Completion

*No checkboxes; manual verification not automatable in tests.*

**Manual verification:**
- Create a real Slack app in the user's workspace with `stars:read` and `stars:write` user scopes; install; copy the `xoxp` token.
- Run `monolog slack-login`, paste token, verify "Connected to <workspace>.slack.com" appears and `slack-status` reflects the connection.
- Save 2–3 messages in Slack (bookmark icon), open `monolog` TUI, wait up to 60s, verify the messages appear as tasks with the expected title/body/tags/schedule.
- Press `d` on one of the Slack-sourced tasks; verify in Slack (refresh the "Later" sidebar) that the message is no longer saved.
- Disconnect the network temporarily; verify the status bar shows one `Slack: <reason>` flash per poll cycle (rate-limited), not a spam.
- Revoke the token in Slack; verify `Slack: auth failed` (or equivalent) appears and is not spammed on subsequent ticks.
- Run `monolog slack-logout`; verify the token file is removed and the TUI no longer polls (restart TUI to reset state).

**External dependencies:**
- Slack's Web API (`stars.list`, `stars.remove`, `auth.test`, `conversations.info`, `users.info`) — if Slack deprecates `stars.*` in favor of a new "Saved items" API, this integration needs a rewrite. Monitor `api.slack.com/changelog`.
