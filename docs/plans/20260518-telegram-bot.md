# Telegram Bot for Mobile Access

## Overview

Add a Telegram bot that lets the user capture, browse, and complete tasks from their phone when the laptop is off. The bot is a new monolog subcommand (`monolog telegram serve`) deployed to an always-on EC2 t4g.nano, holding its own clone of the tasks git repo and acting as another monolog client. Communication with Telegram is via long polling (no webhooks, no public HTTP endpoint). Writes go through the same `internal/store` + `internal/git` paths the CLI uses; the git repo remains the single source of truth across phone and laptop.

**Problem solved**: today the only way to interact with the backlog is the laptop CLI/TUI. Quick capture from anywhere, "I finished that errand"-marking, and a glance at today's list are all currently impossible on phone.

**Integration**: mirrors the existing `internal/email/` Gmail integration in shape — neutral `Bot` interface for testability, pure conversion functions, sync orchestrator with options-passed-by-value, env-var secrets, optional `"telegram"` config block. No changes to `internal/store`, `internal/model`, `internal/recurrence`, or the TUI. Laptop side gets nothing new (existing `s` sync + fsnotify watcher already handle the read side).

## Context (from discovery)

**Files / packages involved:**
- New: `internal/telegram/` package (bot.go, convert.go, handler.go, sync.go + tests)
- New: `cmd/telegram.go` (serve + status subcommands)
- New: `docs/deploy/` for EC2 setup docs, systemd unit, Makefile target
- Modify: `internal/config/config.go` — add `TelegramConfig`, `Telegram()`, `SaveTelegram()`, `telegramBlock`
- Modify: `cmd/root.go` — register `newTelegramCmd`
- Modify: `Makefile` (if exists; otherwise create) — `deploy-bot` target
- Modify: `CLAUDE.md` — add Telegram integration bullet, update dependency list
- Modify: `go.mod` / `go.sum` — `github.com/go-telegram-bot-api/telegram-bot-api/v5`

**Patterns reused (no changes needed):**
- `internal/email/` — Bot-interface + pure-convert + Sync-orchestrator template
- `internal/git/Sync` — already does commit → pull-rebase → resolve-conflicts → push, with `ResolveConflicts` picking the later `UpdatedAt` on task-file conflicts. Reused for write flow; no new `git.PullPush` helper needed.
- `internal/git/PullRebase` — used by the background pull ticker
- `internal/git/AutoCommit` — used for the specific commit message (e.g. `"add: buy milk"`) before calling `git.Sync` (which is a no-op commit-wise after our explicit AutoCommit)
- `recurrence.CompleteAndSpawn` — reused unchanged for Done callback
- `model.ParseTitleTag`, `model.CollectTags`, `model.AppendNote`, `model.CountNotes`, `store.Resolve` — reused unchanged
- `store.Create`, `store.Update`, `store.List(ListOptions{...})` — reused unchanged

**Dependencies identified:**
- One new Go dependency: `github.com/go-telegram-bot-api/telegram-bot-api/v5` (stable, small, native long-polling + inline-keyboard support)

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task), matching existing `internal/email/` style
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change: `go test ./...`
- maintain backward compatibility — no changes to on-disk task JSON schema

## Testing Strategy

- **unit tests on pure functions** in `internal/telegram/convert.go`: `ParseInlineTags`, `ParseCapture`, `FormatTaskRow`, `FormatDetailView`, `BuildSummaryKeyboard`, `BuildDetailKeyboard`, `ParseCallback`. No network, no fakes. Style match to `internal/email/convert_test.go`.
- **handler tests** with an in-memory `Bot` fake: feed `Update` values, assert expected `SendMessage` / `EditMessage` / `AnswerCallback` calls and store-side effects. Style match to `internal/email/sync_test.go`'s fake `Gmail`.
- **integration tests** wire the handler to a real `store.Store` in `t.TempDir()` git repo (created via `git.Init`), exercise capture → done → recurring spawn → archive note, assert commits land via `git log`.
- **NO real Telegram API in CI** — token-gated and slow. Manual smoke test on EC2 after deploy.
- The project has no UI-based e2e tests (Playwright/Cypress); not applicable.
- Run tests with `go test ./...` and `go vet ./...` after each task.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

A long-running Go process (`monolog telegram serve`) on EC2 t4g.nano:

1. **Reads** the optional `"telegram"` config block from `<repo>/.monolog/config.json` for allow-listed user IDs, pull interval, browse limit.
2. **Reads** the bot token from the `--token` flag on `monolog telegram serve`, falling back to the `MONOLOG_TELEGRAM_TOKEN` env var when the flag is empty. Uses `GIT_SSH_COMMAND` (env) to point git at the EC2-local SSH deploy key. The flag is the convenient path for ad-hoc / local runs; the env-var fallback is the recommended systemd path because command-line args are visible in `ps aux`.
3. **Starts a background pull ticker** running `git.PullRebase(repoPath)` every `pull_interval` (default 30s).
4. **Opens a Telegram long-poll loop** (`getUpdates` with `offset` tracking) and dispatches updates:
   - **Plain text from allowed user** → `handleCapture`: parse `tagname:` prefix and `#hashtags`, `store.Create`, `git.AutoCommit("add: <title>", file)`, `git.Sync(repoPath)`, reply with summary card.
   - **Slash command** (`/today`, `/week`, `/active`, `/all`, `/help`, `/start`) → list tasks via `store.List` and send one message per task with inline-keyboard buttons; `/all` capped at `browse_limit` with `+N more — open laptop` footer.
   - **Callback query** (button tap) — `done:<ULID>` runs `recurrence.CompleteAndSpawn` and edits the message; `active:<ULID>` toggles the reserved `active` tag and edits the message; `view:<ULID>` / `collapse:<ULID>` swap between summary and detail views.
   - **Reply to a task message** → resolve task by 5-char prefix in the replied-to text via `store.Resolve`, append note via `model.AppendNote`, commit + sync.
   - **Update from non-allow-listed user** → silently drop.
5. **On `git.Sync` non-recoverable failure** (rare; `ResolveConflicts` auto-resolves task-file conflicts via `UpdatedAt`), bot enters read-only mode: writes are rejected with `⚠️ sync conflict, change not saved — resolve on laptop`; the next clean `PullRebase` clears the flag automatically.
6. **On `ctx.Done()`** (SIGTERM from systemd): break out of the polling loop and return.

`internal/telegram/` does NOT import `internal/config` — values flow in via `config.Telegram() config.TelegramConfig` read once by `cmd/telegram.go` and passed by value to `telegram.Serve`.

## Technical Details

**`config.TelegramConfig`** (in `internal/config/config.go`):

```go
type TelegramConfig struct {
    Enabled        bool
    AllowedUserIDs []int64
    PullInterval   time.Duration
    BrowseLimit    int
}
```

Defaults: `Enabled=false`, `AllowedUserIDs=nil`, `PullInterval=30s`, `BrowseLimit=20`. JSON shape (under optional `"telegram"` key):

```json
"telegram": {
  "enabled": true,
  "allowed_user_ids": [123456789],
  "pull_interval_seconds": 30,
  "browse_limit": 20
}
```

`SaveTelegram(monologDir, tc)` does read-modify-write so other top-level keys (`theme`, `date_format`, `email`) are preserved — mirrors `SaveEmail`.

**`internal/telegram/Bot` interface** (neutral DTOs, no `tgbotapi` types leak out):

```go
type Update struct {
    UpdateID  int64
    Message   *Message       // nil for callback-only updates
    Callback  *CallbackQuery // nil for plain messages
}
type Message struct {
    ChatID    int64
    MessageID int
    UserID    int64
    Text      string
    ReplyTo   *Message // nil when not a reply
}
type CallbackQuery struct {
    ID        string
    UserID    int64
    ChatID    int64
    MessageID int
    Data      string
}
type InlineButton struct {
    Text         string
    CallbackData string
}
type InlineKeyboard [][]InlineButton // rows of buttons

type Bot interface {
    GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error)
    SendMessage(ctx context.Context, chatID int64, html string, kb InlineKeyboard) (msgID int, err error)
    EditMessage(ctx context.Context, chatID int64, msgID int, html string, kb InlineKeyboard) error
    AnswerCallback(ctx context.Context, callbackID string, toast string) error
}
```

`realBot` wraps `*tgbotapi.BotAPI`. Tests use an in-memory fake recording call sequences.

**Callback data format**: `"done:<ULID>"`, `"active:<ULID>"`, `"view:<ULID>"`, `"collapse:<ULID>"`. Full 26-char ULID + 8-char prefix verb fits well under Telegram's 64-byte limit. No bot-side state table — callback is self-describing.

**Capture parsing** (`ParseCapture(text)`): returns `(title, body, tags)`. Order of operations:
1. Split on first `\n` → first line = title-candidate, rest = body.
2. `ParseInlineTags(title-candidate)` extracts `#hashtag` tokens from anywhere in the title-candidate, returns the title with hashtags stripped (and surrounding whitespace collapsed) plus the deduped tag list.
3. The resulting stripped title still goes through `store.Create`, which applies `model.ParseTitleTag` for `tagname: ...` prefix auto-tagging against existing tags on disk. (We do not duplicate that logic — it lives in store.Create's call site or in helpers.go's add command. Plan task 5 confirms which.)
4. Default schedule = `today` (via `schedule.Parse(schedule.Today, now, "")`, mirroring `email.ToTask`).

**Message rendering** (ParseMode=HTML):

Summary row:
```
<code>01J5K</code>  fix login bug
<i>work, urgent  ·  ↻</i>
```
Buttons (below message): `[ ✅ Done ] [ ⭐ Active ] [ 📄 Details ]`.

Detail view (after `view:` tap, replaces summary in-place via `EditMessage`):
```
<code>01J5K</code>  fix login bug
Schedule: 18-05-2026 (today)
Tags: work, urgent
Recur: weekly:mon
Created: 17-05-2026
Notes: 3

<full body with notes, newlines preserved>
```
Buttons: `[ ⬆ Collapse ] [ ✅ Done ] [ ⭐ Active ]`.

Done-strike-through (after `done:` tap):
```
✅ <s><code>01J5K</code>  fix login bug</s>
```
Plus an appended line if `CompleteAndSpawn` created a follow-up: `↻ next: 25-04-2026` (using `config.DateFormat()`). No buttons.

**Title/body HTML escaping**: all user-supplied strings pass through `html.EscapeString` before being inserted into the HTML template, except the literal `<code>`, `<i>`, `<s>` tags we control. Body+notes have newlines preserved (Telegram renders them); URLs auto-linkify on the client side — `display.Linkify` (OSC 8) is NOT applied here. 4096-char message cap on detail view: truncate body with `… (open laptop for full body)`.

**Empty bucket reply**: single message `<b>Today</b> — nothing 🎉` (no per-task rows, no buttons).

**Read-only mode banner**: when the bot's in-memory `readOnly` flag is set (after a `git.Sync` error), `/today` (and other browse commands) prepend `⚠️ <i>read-only — sync conflict pending</i>\n\n` to their reply. Writes (capture, done, active, note) are rejected with `⚠️ sync conflict, change not saved — resolve on laptop` and do NOT mutate the store. The flag is cleared on the next successful background `PullRebase`.

**Sync orchestration** (`telegram.Serve`):
- Two goroutines + a main loop:
  - **Pull ticker goroutine**: `time.Ticker` firing every `PullInterval`. Calls `git.PullRebase(repoPath)`. On success clears `readOnly`. On error logs to stderr (does not flip readOnly on its own — only writes can set it; the ticker only clears it).
  - **Update loop**: `bot.GetUpdates(ctx, offset, 30*time.Second)` in a `for` loop. Dispatches each update to `Handle(...)`. Tracks `offset = lastUpdateID + 1`.
- Handler write flow (capture / done / active / note):
  1. Acquire write mutex (single-threaded write path; phone is one user, prevents internal races).
  2. If `readOnly` flag set → reply with conflict message, do not proceed.
  3. `store.Create` / `store.Update`.
  4. `git.AutoCommit(repoPath, "<verb>: <title>", file)`.
  5. `git.Sync(repoPath)`. On error: set `readOnly` flag, reply with conflict message; the local AutoCommit will be rebased on the next clean pull.
  6. Send / edit Telegram message reflecting result.
- `ctx.Done()` ends both the ticker and the update loop; deferred `bot.Stop()` cleans up the underlying long-poll HTTP connection.

**Status command** (`monolog telegram status`):
```
enabled: true
allowed_user_ids: [123456789]
pull_interval: 30s
browse_limit: 20
token_env: MONOLOG_TELEGRAM_TOKEN (set)
```
No "last sync time" (use `git log` for that history, same as email status).

## What Goes Where

- **Implementation Steps**: all code in `internal/telegram/`, `internal/config/`, `cmd/`, plus the `Makefile` target, systemd unit, and `CLAUDE.md` update — all live in this repo.
- **Post-Completion**: EC2 provisioning steps (launch instance, SSH key creation, deploy-key registration in GitHub, first `git clone` on the box, copying the systemd unit, first `systemctl start`) — these run on the user's AWS account and aren't automated by this plan beyond the documentation file.

## Implementation Steps

### Task 1: Add `TelegramConfig`, `Telegram()`, `SaveTelegram()` to config package

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [x] add `TelegramConfig` struct with `Enabled bool`, `AllowedUserIDs []int64`, `PullInterval time.Duration`, `BrowseLimit int`
- [x] add `defaultTelegramConfig()` returning the documented defaults (disabled, nil, 30s, 20)
- [x] add package-level `telegramCfg` var initialized from `defaultTelegramConfig()`
- [x] add `Telegram() TelegramConfig` accessor (named like `Email()` — function-name collision rule with struct name)
- [x] add `telegramBlock` struct with pointer fields: `Enabled *bool`, `AllowedUserIDs *[]int64`, `PullIntervalSeconds *int`, `BrowseLimit *int`
- [x] add `resetTelegramCfgToDefaults()` and `applyTelegramBlock(b telegramBlock)` with value-clamps matching `applyEmailBlock` (zero/negative interval and browse_limit silently fall back to defaults; empty AllowedUserIDs is allowed and means "no one allowed, drops all updates")
- [x] extend the inline struct in `Load` to include `Telegram *telegramBlock` and call `applyTelegramBlock` when present; call `resetTelegramCfgToDefaults()` at the top of `Load` (matches the email reset)
- [x] add `SaveTelegram(monologDir string, tc TelegramConfig) error` — read-modify-write mirroring `SaveEmail`; writes block as `{"enabled":..., "allowed_user_ids":..., "pull_interval_seconds":..., "browse_limit":...}`; updates in-session `telegramCfg`
- [x] write tests: `TestTelegramDefaults`, `TestLoadTelegramBlock`, `TestSaveTelegramRoundTrip`, `TestSaveTelegramPreservesForeignKeys` (verifies `email` and `date_format` keys survive), `TestApplyTelegramBlockValueClamps` (zero interval / browse_limit fall back; explicit `enabled:false` stays false)
- [x] run `go test ./internal/config/...` and `go vet ./...` — must pass before next task

### Task 2: Pure parsing helpers for capture and callback data

**Files:**
- Create: `internal/telegram/convert.go`
- Create: `internal/telegram/convert_test.go`

- [x] create package `telegram` with header comment mirroring `email`'s "MUST NOT import internal/config" rule
- [x] add `ParseInlineTags(text string) (cleaned string, tags []string)` — extracts `#word` tokens (regex `#([A-Za-z0-9_-]+)`), returns deduped tag list and text with hashtag tokens removed plus whitespace collapsed
- [x] add `ParseCapture(text string) (title, body string, tags []string)` — splits on first `\n`, runs `ParseInlineTags` on the title portion, leaves body untouched (URLs and other content survive)
- [x] add `ParseCallback(data string) (action, ulid string, err error)` — splits on first `:`, validates action is one of `done|active|view|collapse`, validates ULID is exactly 26 chars (basic shape check — store layer does the actual lookup)
- [x] write tests for `ParseInlineTags`: empty, no hashtags, single hashtag, multiple hashtags, duplicate hashtags (deduped), hashtag-only text (empty result), unicode in body, leading/trailing whitespace, hashtags at start/middle/end of title
- [x] write tests for `ParseCapture`: single-line, multi-line, leading newline (empty title), trailing whitespace, tag extracted from title not body (body hashtags survive untouched)
- [x] write tests for `ParseCallback`: each valid action, malformed (no colon), unknown action, wrong-length ULID, empty input
- [x] run `go test ./internal/telegram/...` — must pass before next task

### Task 3: Pure formatting helpers (rows, detail view, keyboards)

**Files:**
- Modify: `internal/telegram/convert.go`
- Modify: `internal/telegram/convert_test.go`

- [x] add `FormatTaskRow(t model.Task, dateFormat string) string` — HTML-formatted summary row: `<code>{prefix5}</code>  {escaped-title}\n<i>{tag-list}  ·  {recur-marker}{notes-badge}</i>` (omit the `<i>` block entirely if no tags + no recur + no notes; recur marker = `↻` when `t.Recurrence != ""`; notes badge = `[N]` when `t.NoteCount > 0`)
- [x] add `FormatDetailView(t model.Task, dateFormat string) string` — HTML-formatted full detail mirroring TUI `detailPanelView`: ID line + Schedule / Tags / Recur (conditional) / Created / Notes lines + blank line + body (HTML-escaped, newlines preserved); apply 4096-char cap on the whole message with `… (open laptop for full body)` truncation on the body only
- [x] add `BuildSummaryKeyboard(taskID string) InlineKeyboard` — single row with three buttons: `[✅ Done | done:<ID>]`, `[⭐ Active | active:<ID>]`, `[📄 Details | view:<ID>]`
- [x] add `BuildDetailKeyboard(taskID string) InlineKeyboard` — single row: `[⬆ Collapse | collapse:<ID>]`, `[✅ Done | done:<ID>]`, `[⭐ Active | active:<ID>]`
- [x] add `FormatDoneRow(t model.Task, nextDate string) string` — strike-through row: `✅ <s><code>{prefix5}</code>  {escaped-title}</s>` plus `\n↻ next: {nextDate}` only when `nextDate != ""`
- [x] add `FormatEmptyBucket(label string) string` — `<b>{label}</b> — nothing 🎉`
- [x] add `htmlEscape(s string) string` helper using `html.EscapeString` (small wrapper to keep call sites readable)
- [x] add `Schedule` rendering helper that maps stored ISO date → display string `"{DD-MM-YYYY} ({bucket})"` when the date matches today/tomorrow/week/month, else just the formatted date; bucket names use existing `schedule` package
- [x] write tests for `FormatTaskRow`: title with HTML metacharacters (`<`, `>`, `&`), no tags, with tags, with recur marker, with notes count, all combined, ULID prefix correctness
- [x] write tests for `FormatDetailView`: with and without recurrence, with and without notes, body shorter than 4096, body longer than 4096 (truncation triggers), schedule rendering for today/week/specific-date
- [x] write tests for `BuildSummaryKeyboard` and `BuildDetailKeyboard`: button count, callback data shape, button text
- [x] write tests for `FormatDoneRow` (with and without next date), `FormatEmptyBucket`, `htmlEscape`
- [x] run `go test ./internal/telegram/...` — must pass before next task

### Task 4: Bot interface + real client wrapping go-telegram-bot-api

**Files:**
- Create: `internal/telegram/bot.go`
- Create: `internal/telegram/bot_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [x] add dependency `github.com/go-telegram-bot-api/telegram-bot-api/v5` via `go get`
- [x] in `bot.go` define neutral DTOs: `Update`, `Message`, `CallbackQuery`, `InlineButton`, `InlineKeyboard` (per Technical Details above)
- [x] define `Bot` interface with `GetUpdates`, `SendMessage`, `EditMessage`, `AnswerCallback`
- [x] implement `realBot` struct wrapping `*tgbotapi.BotAPI`
- [x] implement `NewClient(token string) (Bot, error)` returning the production `realBot`; rejects empty token
- [x] implement `realBot.GetUpdates`: build `tgbotapi.NewUpdate(offset)` config with `Timeout = int(timeout/time.Second)`, call `bot.GetUpdates`, translate each `tgbotapi.Update` into our neutral `Update` (extract `Message`, `CallbackQuery`, `ReplyToMessage`); abort cleanly on `ctx.Done()` by setting `bot.StopReceivingUpdates()` and returning `ctx.Err()`
- [x] implement `realBot.SendMessage`: build `tgbotapi.NewMessage(chatID, html)`, set `ParseMode = "HTML"`, attach inline keyboard markup if kb non-empty, return `MessageID`
- [x] implement `realBot.EditMessage`: build `tgbotapi.NewEditMessageTextAndMarkup(...)` with ParseMode HTML
- [x] implement `realBot.AnswerCallback`: build `tgbotapi.NewCallback(callbackID, toast)` and send
- [x] in `bot_test.go` add a small `fakeBot` type implementing `Bot` (records all calls into slices) and verify the basic shape (returned `MessageID` increments, `GetUpdates` returns queued updates, no real network)
- [x] run `go test ./internal/telegram/...` and `go vet ./...` — must pass before next task

### Task 5: Capture handler + access control + write flow

**Files:**
- Create: `internal/telegram/handler.go`
- Create: `internal/telegram/handler_test.go`
- Create: `internal/telegram/sync.go` (write-flow wrapper only — main Serve loop comes in task 10)

- [ ] in `handler.go` define `Handler` struct holding: `bot Bot`, `store *store.Store`, `repoPath string`, `cfg TelegramConfig`, `dateFormat string`, `now func() time.Time`, plus internal `mu sync.Mutex` (write serialization) and `readOnly atomic.Bool`
- [ ] add `NewHandler(...)` constructor binding all fields; `now` defaults to `time.Now` when nil
- [ ] add `(*Handler) Handle(ctx context.Context, u Update) error` dispatcher: ignores updates from users not in `cfg.AllowedUserIDs` (silent drop, returns nil); routes by `u.Message != nil` (text path) vs `u.Callback != nil` (callback path — stub for now, implemented in task 7); within message path branches on first character `'/'` for slash command vs plain capture vs `u.Message.ReplyTo != nil` for note-reply (stub for now, implemented in task 8)
- [ ] add `(*Handler) handleCapture(ctx, m *Message) error`: respects `readOnly` (replies with conflict message, returns); parses via `ParseCapture`; constructs `model.Task` with fresh `model.NewID`, today schedule via `schedule.Parse(schedule.Today, h.now(), "")`, RFC3339 timestamps, `Tags` from `ParseCapture`; calls `model.CollectTags`-aware `store.Create` (passes inline tags + relies on existing `model.ParseTitleTag` auto-tag rule in store.Create — verify exact call site by reading store.Create; if store.Create doesn't apply ParseTitleTag, do it here before calling Create)
- [ ] in `sync.go` add unexported helper `(*Handler) commitAndSync(message string, file string) error`: calls `git.AutoCommit(h.repoPath, message, file)` then `git.Sync(h.repoPath)`; on Sync error sets `readOnly=true` and returns the error wrapped (caller decides what to reply); on success ensures `readOnly=false`
- [ ] after a successful `store.Create`, call `commitAndSync("add: "+task.Title, taskRelPath)` (relative path: `.monolog/tasks/<ID>.json`); on success send summary message via `FormatTaskRow` + `BuildSummaryKeyboard`; on commitAndSync failure send conflict message — but the local commit is still on the bot's disk, and the next clean pull will rebase it
- [ ] write tests: fake Bot, real `store.Store` in `t.TempDir()` git repo (use `git.Init(tmpDir, "")` then construct store via existing helpers in cmd/helpers.go or directly), allowed user captures plain text → assert one message sent with HTML containing escaped title and buttons, store has one task with Source unset (Source=="" since not from email), schedule="today" (resolved ISO), tags from #hashtags
- [ ] test: non-allowed user → no message sent, no task created (silent drop)
- [ ] test: multi-line capture → title=first line, body=rest, hashtags in title extracted (not in body)
- [ ] test: title with HTML metacharacters → store has raw title, sent HTML escapes them
- [ ] test: `readOnly=true` → capture replies with conflict message, store unchanged
- [ ] test: git.Sync failure (use a TempDir repo with intentionally broken remote / unreachable origin) → `readOnly` set, conflict reply sent
- [ ] run `go test ./internal/telegram/...` — must pass before next task

### Task 6: Browse commands (`/today`, `/week`, `/active`, `/all`)

**Files:**
- Modify: `internal/telegram/handler.go`
- Modify: `internal/telegram/handler_test.go`

- [ ] add `(*Handler) handleSlash(ctx, m *Message) error` dispatching by command word (lowercased, stripped `/`): `today`, `week`, `active`, `all`, `help`, `start`; unknown commands reply with one-line `unknown command — try /help`
- [ ] add `(*Handler) handleBrowse(ctx, m *Message, bucket string) error` parameterized by bucket name; queries `store.List(store.ListOptions{Status: "open", Schedule: bucket})` for `today` / `week`; for `active` uses `Tags: []string{"active"}` filter; for `all` uses `Status: "open"` with no schedule filter
- [ ] sort/order: reuse existing `store.List` sort order (position-based within bucket)
- [ ] `/all` cap: take first `cfg.BrowseLimit` tasks; if exceeded, send a footer message `<i>+N more — open laptop</i>` after the per-task messages
- [ ] empty bucket → single `FormatEmptyBucket` message, no rows
- [ ] read-only banner: if `readOnly` set, prepend `⚠️ <i>read-only — sync conflict pending</i>` as a header message before the per-task messages (or merged into the empty-bucket message)
- [ ] write tests for each command using fake Bot + temp-repo store: pre-populate 3 tasks with mixed schedules and tags, assert message count and HTML content for `/today`, `/week`, `/active`, `/all`
- [ ] test: `/all` with cap=2 and 5 tasks → 2 task messages + 1 `+3 more` footer
- [ ] test: empty `/today` → single "nothing 🎉" message
- [ ] test: `readOnly=true` → first message includes the banner
- [ ] test: unknown command (`/foo`) → reply `unknown command — try /help`
- [ ] run `go test ./internal/telegram/...` — must pass before next task

### Task 7: Callback handlers (Done, Active, Details, Collapse)

**Files:**
- Modify: `internal/telegram/handler.go`
- Modify: `internal/telegram/handler_test.go`

- [ ] add `(*Handler) handleCallback(ctx, cq *CallbackQuery) error` dispatcher: parse via `ParseCallback`; on parse error answer callback with toast `"invalid"`; access-check `cq.UserID` (silent drop unknown users — but for callbacks we DO answer the callback with empty toast to silence the loading spinner)
- [ ] resolve task by full ULID via `store.Resolve(ulid)` (full ULID is unambiguous; covers task-not-found case if user deletes via laptop between message render and tap)
- [ ] **Done** (`done:<ULID>`): respect `readOnly`; load task via store.Resolve; if already `Status=="done"` → AnswerCallback toast `"already done"`, no message edit; else call `recurrence.CompleteAndSpawn(h.store, &task, h.now(), io.Discard, h.dateFormat)` (writer set to io.Discard — bot doesn't surface spawn warnings to user; on the rare warning case it's logged via a side `bytes.Buffer` and emitted on stderr); commit with `"done: <title> (recurring, next <date>)"` for recurring or `"done: <title>"` for non-recurring (match cmd/done.go's message); commitAndSync; on success EditMessage with `FormatDoneRow` (passes next-date string if spawn occurred) + empty keyboard
- [ ] **Active** (`active:<ULID>`): respect `readOnly`; load task; toggle reserved `active` tag (add if absent, remove if present); store.Update; commitAndSync with `"active: <title>"` or `"inactive: <title>"`; EditMessage with refreshed `FormatTaskRow` + same `BuildSummaryKeyboard` (button label stays "Active"; the row's `<i>` line now shows / hides the tag)
- [ ] **View** (`view:<ULID>`): load task; EditMessage to `FormatDetailView` + `BuildDetailKeyboard`; AnswerCallback empty toast
- [ ] **Collapse** (`collapse:<ULID>`): load task; EditMessage to `FormatTaskRow` + `BuildSummaryKeyboard`; AnswerCallback empty toast
- [ ] task-not-found case (ULID resolves to nothing): AnswerCallback `"task not found"`, no message edit
- [ ] write tests with fake Bot + temp-repo store: Done on non-recurring task → store has Status=done, file committed, message edited to strike-through with no buttons
- [ ] test: Done on recurring task (set Recurrence="workdays" on test task) → original task done, new task spawned with ULID != original, message edited with `↻ next: <date>` line, both files in single commit
- [ ] test: Done on already-done task → toast "already done", store unchanged, message unchanged
- [ ] test: Active toggle add → `active` tag added, message edited; second toggle → tag removed, message edited again
- [ ] test: View → detail HTML rendered, keyboard has Collapse + Done + Active; Collapse → summary HTML, keyboard has 3 summary buttons
- [ ] test: invalid callback data → toast "invalid", no store mutation
- [ ] test: ULID not found → toast "task not found", no store mutation
- [ ] test: readOnly=true blocks Done and Active but allows View / Collapse (read-only ops)
- [ ] run `go test ./internal/telegram/...` — must pass before next task

### Task 8: Reply-to-note + `/help` + `/start` + unknown text fallback

**Files:**
- Modify: `internal/telegram/handler.go`
- Modify: `internal/telegram/handler_test.go`

- [ ] in `Handle`, before the slash-vs-capture branch: if `m.ReplyTo != nil` route to `handleNoteReply`
- [ ] add `(*Handler) handleNoteReply(ctx, m *Message) error`: respect `readOnly`; extract first whitespace-bounded token from `m.ReplyTo.Text` as the task-prefix; call `store.Resolve(prefix)`; on ambiguous match reply with conflict-style message listing the conflicts (matches existing `store.Resolve` behavior — surface its error verbatim with `htmlEscape`); on success load task, call `model.AppendNote(task.Body, m.Text, h.now(), h.dateFormat)` to compute new body, `store.Update` task, `commitAndSync` with `"note: <title>"`, reply with `📝 note added` (single message, no buttons)
- [ ] add `handleHelp(ctx, m *Message) error` printing a fixed HTML cheatsheet covering: free-text=capture (with `#tags` and `tagname:` prefix), `/today` `/week` `/active` `/all`, inline buttons (Done/Active/Details), reply-to-message=note
- [ ] route `/help` and `/start` to `handleHelp`
- [ ] unknown slash command → reply `unknown command — try /help` (already added in task 6, double-check)
- [ ] write tests: reply with text to a task message whose text starts with `01J5K` → task body has new note appended, commit made
- [ ] test: reply to a non-task message (e.g. the help message) where first token is not a valid prefix → reply with resolve error, store unchanged
- [ ] test: `/help` → message contains expected command list
- [ ] test: `/start` → same message as `/help`
- [ ] run `go test ./internal/telegram/...` — must pass before next task

### Task 9: `telegram.Serve` main loop (long polling + pull ticker + graceful shutdown)

**Files:**
- Modify: `internal/telegram/sync.go`
- Create: `internal/telegram/sync_test.go`

- [ ] add `ServeOptions` struct: `RepoPath string`, `Bot Bot`, `Store *store.Store`, `Cfg TelegramConfig`, `DateFormat string`, `Now func() time.Time`, `Writer io.Writer` (stderr for warnings)
- [ ] add `Serve(ctx context.Context, opts ServeOptions) error` main loop:
  1. Validate options (non-nil bot/store, RepoPath non-empty); return error on missing required fields
  2. On startup run `git.PullRebase(opts.RepoPath)` once; if it fails, log to opts.Writer and continue (don't fail Serve — bot can still serve stale data)
  3. Construct `Handler` via `NewHandler`
  4. Start pull ticker goroutine: `time.Ticker(cfg.PullInterval)`; on each tick run `PullRebase` and on success clear the handler's `readOnly` flag via an exported `(*Handler) ClearReadOnly()`; on error log via opts.Writer (non-fatal)
  5. Update loop: `offset := int64(0)`; loop while `ctx.Err() == nil`: `updates, err := bot.GetUpdates(ctx, offset, 30*time.Second)`; on ctx-cancellation error return nil; on other error log + small sleep with ctx-respecting backoff (e.g. `select { case <-time.After(2*time.Second): case <-ctx.Done(): return nil }`); for each update: `offset = max(offset, u.UpdateID+1)`, run handler.Handle in a goroutine? — **NO, serialize**: handle inline to keep simple write ordering (the mutex would serialize them anyway; goroutines just add complexity)
  6. On `ctx.Done()` return nil; defer ticker.Stop(); defer the pull-ticker goroutine exit (use a sub-ctx + done channel pattern, or pass the same ctx and have it select on ctx.Done)
- [ ] expose `(*Handler) ClearReadOnly()` and `(*Handler) IsReadOnly() bool` (the latter for the browse banner — already used internally; just give it a method form)
- [ ] write tests for `Serve`:
  - test: feed fake bot with a queue of one capture update, call `Serve` in a goroutine, cancel ctx after first update processed, assert: store has one task, fake bot.SendMessage was called once, no errors
  - test: pull ticker fires twice during a short Serve run (use a tiny interval like 50ms, ctx with 200ms timeout, count fake `PullRebase` calls — for this we need to make `PullRebase` swappable, or use a real temp git repo with a fake remote → simpler: refactor the sync.go ticker to call an injectable `pullFunc func() error` defaulting to `git.PullRebase`)
  - ➕ if needed: introduce `pullFunc` and `syncFunc` injection points at the `Handler` level so tests don't need real git operations; this matches the `emailAuthorize` swappable-var pattern used by cmd/email.go
- [ ] test: ctx cancel during getUpdates wait → Serve returns nil cleanly within 100ms
- [ ] test: getUpdates error → backoff sleeps with ctx respect, retries on next tick
- [ ] run `go test ./internal/telegram/...` — must pass before next task

### Task 10: `cmd/telegram.go` (serve + status subcommands)

**Files:**
- Create: `cmd/telegram.go`
- Create: `cmd/telegram_test.go`
- Modify: `cmd/root.go`

- [ ] add `cmd/telegram.go` with `newTelegramCmd() *cobra.Command` mirroring `newEmailCmd`; subcommands `serve` and `status`
- [ ] `newTelegramServeCmd()`: open store via `openStore` (returns store + repoPath); read `config.Telegram()`; if `!Enabled` return error `"telegram integration is disabled — edit config.json to enable"`; resolve token via the precedence chain (flag wins, then env): bind a string flag `--token` (`-t`) to a local var, after parsing use `tokenFlag` if non-empty else `os.Getenv("MONOLOG_TELEGRAM_TOKEN")`, error if both are empty with message `"telegram token required: pass --token or set MONOLOG_TELEGRAM_TOKEN"`; call `telegram.NewClient(token)`; install signal handler bridging SIGINT/SIGTERM → ctx cancel (use `signal.NotifyContext`); call `telegram.Serve(ctx, ServeOptions{...})`; on Serve error return wrapped error
- [ ] document the `--token` flag in cobra's `Long` help: emphasize that for systemd / long-running deployments, prefer `MONOLOG_TELEGRAM_TOKEN` env (e.g. via `EnvironmentFile=`) because `--token <value>` is visible in `ps aux`
- [ ] `newTelegramStatusCmd()`: print `enabled`, `allowed_user_ids`, `pull_interval`, `browse_limit`, plus a single line indicating whether `MONOLOG_TELEGRAM_TOKEN` is set or not (NEVER print the token value); mirrors `email status` shape
- [ ] in `cmd/root.go` add `rootCmd.AddCommand(newTelegramCmd())` after the `newEmailCmd` line
- [ ] add `telegramServeFactory` and `telegramServeFunc` swappable seams in cmd/telegram.go so `telegram_test.go` can stub `telegram.Serve` and `telegram.NewClient` (mirrors `emailClientFactory` pattern)
- [ ] write `telegram_test.go`: cobra wiring tests via the stub seam — `monolog telegram serve` exits cleanly when ctx is cancelled, returns error when both `--token` flag and `MONOLOG_TELEGRAM_TOKEN` env are unset, accepts the flag when env is unset, accepts env when flag is unset, returns error when integration is disabled in config; `monolog telegram status` prints expected lines from a fixture config
- [ ] run `go test ./cmd/...` and `go vet ./...` — must pass before next task

### Task 11: Deployment artifacts (Makefile target, systemd unit, deploy docs)

**Files:**
- Create or Modify: `Makefile`
- Create: `docs/deploy/monolog-bot.service`
- Create: `docs/deploy/env.example`
- Create: `docs/deploy/README.md`

- [ ] check if `Makefile` exists at repo root; create one if absent, otherwise extend
- [ ] add `make build-bot-linux-arm64` target: `GOOS=linux GOARCH=arm64 go build -o dist/monolog-linux-arm64 .`
- [ ] add `make deploy-bot` target with placeholder vars (read from env or `.env.deploy` file gitignored) for `EC2_HOST`, `EC2_USER`, `BIN_DIR=/opt/monolog-bot`; depends on `build-bot-linux-arm64`; scps the binary, restarts the systemd unit via ssh
- [ ] create `docs/deploy/monolog-bot.service` systemd unit: User=monolog-bot, EnvironmentFile=/etc/monolog-bot/env, ExecStart=/opt/monolog-bot/monolog-linux-arm64 telegram serve, Restart=on-failure, RestartSec=5s, WorkingDirectory=/home/monolog-bot/tasks-repo (or similar — document MONOLOG_DIR usage so the bot finds the right repo)
- [ ] create `docs/deploy/env.example` documenting required env vars: `MONOLOG_TELEGRAM_TOKEN=`, `GIT_SSH_COMMAND="ssh -i /etc/monolog-bot/id_ed25519 -o StrictHostKeyChecking=no"`, `MONOLOG_DIR=/home/monolog-bot/tasks-repo`
- [ ] create `docs/deploy/README.md` walking through one-time EC2 setup: launch t4g.nano + Amazon Linux 2023 ARM64, create monolog-bot user, generate ed25519 SSH deploy key, add as GitHub deploy key (write access), git clone the tasks repo to /home/monolog-bot/tasks-repo, install systemd unit, write env file with 0600 perms, enable + start unit. Each step a numbered checklist for the user to follow manually.
- [ ] document the security-group requirement (SSH from user IP only, outbound HTTPS open)
- [ ] **no automated tests** for this task — these are config files. Add a single sanity test: `go test -run TestDeployArtifactsExist` in a new `cmd/telegram_deploy_test.go` that asserts the three files exist (so future renames don't break the documented references silently)
- [ ] run `go test ./...` and `go vet ./...` — must pass before next task

### Task 12: Verify acceptance criteria

- [ ] verify capture works end-to-end: plain text → task created → git commit lands → git push happens (in test repo with fake remote)
- [ ] verify browse works: `/today` `/week` `/active` `/all` each return expected formatting
- [ ] verify Done callback runs CompleteAndSpawn correctly (recurring spawns; back-reference note added)
- [ ] verify Active callback toggles the reserved tag
- [ ] verify View/Collapse swap the message representation without sending new messages
- [ ] verify reply-to-message appends a note
- [ ] verify readOnly mode blocks writes and shows banner on browse, and clears on next clean pull
- [ ] verify silent-drop for non-allowed users
- [ ] verify ctx cancellation gracefully exits Serve
- [ ] verify no real network calls in CI: `go test ./...` runs offline
- [ ] run `go test ./...` (full suite)
- [ ] run `go vet ./...`
- [ ] verify test coverage is reasonable on `internal/telegram/`: `go test -cover ./internal/telegram/...` and check >70% (matches other internal packages)

### Task 13: Update CLAUDE.md and move plan to completed

**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/plans/20260518-telegram-bot.md` → move to `docs/plans/completed/`

- [ ] add new bullet in "Key Design Decisions" titled **Telegram integration** describing: opt-in via config.json `"telegram"` block (defaults disabled), env-var secrets (MONOLOG_TELEGRAM_TOKEN + GIT_SSH_COMMAND), free-text=capture with #hashtag + tagname: support, slash browse commands, callback-button actions (Done/Active/Details/Collapse), reply=note, read-only-on-conflict mode, allow-list user filter, EC2 long-polling deployment topology. Match length and depth of the existing "Email integration" bullet.
- [ ] add `internal/telegram/` to the directory listing in the "Architecture" section
- [ ] update the "Dependencies" line to include `github.com/go-telegram-bot-api/telegram-bot-api/v5 (Telegram bot integration; only used by internal/telegram/)`
- [ ] note the design rule: `internal/telegram/` MUST NOT import `internal/config` (same rule as `internal/email/`)
- [ ] `mkdir -p docs/plans/completed && mv docs/plans/20260518-telegram-bot.md docs/plans/completed/`
- [ ] run final `go test ./...` and `go vet ./...` — must pass

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification on real EC2 + Telegram** (once code is merged):

- Launch EC2 t4g.nano, Amazon Linux 2023 ARM64; security group SSH-from-your-IP + outbound-HTTPS only
- Create `monolog-bot` system user; install systemd unit from `docs/deploy/monolog-bot.service`
- Generate ed25519 SSH key on the instance; add public key as **deploy key with write access** to the tasks GitHub repo
- `git clone git@github.com:<you>/<tasks-repo>.git /home/monolog-bot/tasks-repo` as the bot user (one-time, manual)
- Create `/etc/monolog-bot/env` (0600, owned by bot user) with `MONOLOG_TELEGRAM_TOKEN=<from-BotFather>`, `GIT_SSH_COMMAND="ssh -i /etc/monolog-bot/id_ed25519 -o StrictHostKeyChecking=no"`, `MONOLOG_DIR=/home/monolog-bot/tasks-repo`
- Pre-populate `<MONOLOG_DIR>/.monolog/config.json` with the `"telegram"` block including your own Telegram user ID in `allowed_user_ids` (find via @userinfobot DM)
- `systemctl enable --now monolog-bot`
- Smoke-test from your phone: `/start`, send a capture, tap Done, run `/today`, reply-to-note, check `git log` on the laptop after pulling
- Verify graceful restart: `systemctl restart monolog-bot` — bot resumes long polling within seconds, no message loss (Telegram queues updates by offset until next getUpdates)

**External system updates** (none required after Day 1):

- No consuming projects depend on monolog; this is a single-user tool
- No CI/CD pipelines to update
- Adding GitHub Actions for `make deploy-bot` is deliberately deferred — manual `make deploy-bot` from laptop is fine for v1

**Security review considerations**:

- Bot token in env file with 0600 perms — never committed; never logged; redacted in `status` output
- SSH deploy key on EC2 only — separate from laptop SSH keys; can be rotated by removing the GitHub deploy key
- Allow-list filter is the ONLY auth mechanism for bot writes — if a user ID leaks (unlikely), they could spam capture; rotate by editing config.json + restart. No DM relay, no group chat support (single-user assumption holds).
