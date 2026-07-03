# Monolog

A keyboard-driven personal backlog that lives in a git repo you own — no cloud, no account, plain-text data.

<!-- Hero demo generated via `make demo` (vhs docs/demo.tape); the GIF lands during the first release. -->
![monolog TUI demo](docs/img/demo.gif)

## Why monolog

- **Git-backed, conflict-free sync.** One JSON file per task means two devices editing different tasks rebase cleanly — no merge conflicts, no sync server.
- **Plain-text data you fully own.** Your backlog is just JSON files in a git repo on your disk. Grep it, script it, back it up, or walk away with it any time.
- **Fast, keyboard-driven TUI.** The whole tool is built for the keyboard: switch tabs, reschedule, retag, search, and complete tasks without ever reaching for the mouse.
- **No cloud, no account, no subscription.** Nothing phones home. There is no service to sign up for and nothing to pay.

## Install

### Homebrew (macOS)

```bash
brew install mmaksmas/tap/monolog
```

This is a Homebrew cask and is macOS-only. On Linux, use the prebuilt binary or `go install` below.

### Prebuilt binary

Download a release archive from the [GitHub Releases page](https://github.com/mmaksmas/monolog/releases). Builds are available for macOS (darwin) and Linux, on both amd64 and arm64.

### From source

```bash
go install github.com/mmaksmas/monolog@latest
```

Requires Go 1.26+.

## Quick start

```bash
monolog init                        # create a new monolog repo at ~/.monolog
monolog add "Review PR #42"         # add a task (defaults to today)
monolog add "Write tests" -s week   # schedule for this week
monolog ls                          # list today's open tasks
monolog done 01J5K                  # mark done by ID prefix
monolog done rp                     # ...or by title initials ("Review PR #42")
monolog sync                        # push/pull with a git remote
monolog                             # launch the interactive TUI
```

## Highlights

- **Interactive TUI** — the default when you run `monolog` with no subcommand; tabs for each schedule bucket, an add modal, reschedule/retag/edit, and grab-to-reorder.
- **Tag view** — press `v` (or launch with `--tags`/`-T`) to pivot tabs from schedule buckets to tags.
- **Fuzzy search** — `/` fuzzy-matches across titles and bodies of all tasks and jumps you straight to a match.
- **Recurring tasks** — completing a task with a recurrence rule auto-spawns the next occurrence (`monthly:N`, `weekly:<day>`, `workdays`, `days:N`).
- **Color themes** — two built-in themes plus drop-in user themes from `<MONOLOG_DIR>/.monolog/themes/*.json`.
- **Gmail import** — opt-in import of labeled emails as tasks, with archive-on-done.
- **Telegram bot** — opt-in long-polling bot to capture, browse, and complete tasks from your phone.
- **Clickable URLs** — the TUI wraps URLs in OSC 8 hyperlinks so Cmd/Ctrl-click opens the browser.
- **Notes** — append timestamped notes to any task from the CLI or the TUI detail panel.

## Concepts

**Schedules / buckets.** Every task has a schedule: `today` (the default), `tomorrow`, `week`, `month`, `someday`, or a specific date. Buckets become the tabs in the TUI and the default filters on the CLI. Dates are entered and displayed in a configurable format (default `DD-MM-YYYY`); see [Date format](#date-format).

**Tags & the reserved `active` tag.** Tasks carry comma-separated tags. The reserved `active` tag marks a task as part of your current working set — active tasks render in green and get their own panel in the TUI. See [Active tasks](#active-tasks).

**Storage & git sync.** Each task is a single JSON file at `<repo>/.monolog/tasks/<ULID>.json`. IDs are [ULIDs](https://github.com/oklog/ulid) — time-sortable and globally unique, so you can address a task by typing just a prefix. Every mutation auto-commits to git, and because each task is its own file, two devices editing different tasks rebase without conflicts. Ordering uses fractional positions with automatic rebalancing. The default data directory is `~/.monolog` (override with `MONOLOG_DIR`).

## Commands

### `monolog init [--remote <url>]`

Initialize a monolog repo. Optionally set a git remote for sync.

### `monolog add <title>`

| Flag | Description |
|------|-------------|
| `-s, --schedule` | `today` (default), `tomorrow`, `week`, `month`, `someday`, or a date (default format `DD-MM-YYYY`; legacy ISO `YYYY-MM-DD` is still accepted) |
| `-t, --tags` | Comma-separated tags |
| `-b, --body` | Body text for the task |
| `--recur` | Recurrence rule: `monthly:N` \| `weekly:<day>` \| `workdays` \| `days:N` (e.g. `monthly:1`, `weekly:mon`, `workdays`, `days:7` — see [Recurring tasks](#recurring-tasks)) |

If the title starts with `tag: ...` and that tag already exists on another task, it is automatically added as a tag. For example, if a task already has the tag `jean`, running `monolog add "jean: create integration"` will auto-tag the new task with `jean`. The title is kept as-is. Duplicate tags are not created if the same tag is also passed via `--tags`.

### `monolog ls`

Lists today's open tasks by default. Each row includes a compact dates column: relative for recent tasks (`5m`, `3h`, `2d`), a short date for older same-year tasks (`DD-MM` under the default format), and a year-qualified short date for cross-year (`DD-MM-YY` under the default). Done tasks show `created→done` (e.g. `5d→1h`). Active tasks are marked with a leading `*`.

| Flag | Description |
|------|-------------|
| `-a, --all` | Show all open tasks across all schedules |
| `-s, --schedule` | Filter by schedule value |
| `-t, --tag` | Filter by tag |
| `-d, --done` | Show completed tasks |
| `--active` | Show only active tasks (lifts the default today filter unless `--schedule` is also given) |
| `-f, --full` | Show each task as a multi-line detail block (title + metadata + body), piped through `$PAGER` when stdout is a TTY |

### `monolog done <id-prefix>`

Mark a task as done.

### `monolog edit <id-prefix>`

| Flag | Description |
|------|-------------|
| `--title` | New title |
| `--body` | New body text |
| `--schedule` | New schedule |
| `--tags` | New comma-separated tags |
| `--active=true\|false` | Mark a task as active or inactive |
| `--recur` | New recurrence rule (pass `""` to clear — see [Recurring tasks](#recurring-tasks)) |

At least one flag is required.

### `monolog rm <id-prefix>`

Delete a task permanently.

### `monolog mv <id-prefix>`

Reorder a task within its schedule group. Exactly one flag required:

| Flag | Description |
|------|-------------|
| `--top` | Move to top |
| `--bottom` | Move to bottom |
| `--before <id>` | Insert before another task |
| `--after <id>` | Insert after another task |

### `monolog note <id-prefix> <text>`

Append a timestamped note to a task. The note is stored inside the task's body using `--- DD-MM-YYYY HH:MM:SS ---` separators (the date portion follows the configured display format; see [Date format](#date-format)). Empty text is rejected.

### `monolog show <id-prefix>`

Print full task detail to stdout: title, ID, status, schedule, recurrence (when set), tags, dates, note count, and body (including notes).

### `monolog log`

Show tasks completed in the last 7 days, with `created→done` compact dates.

### `monolog sync`

Commit local changes, pull with rebase, and push. Warns if no remote is configured.

### `monolog email`

Gmail integration subcommands — see [Email integration](#email-integration).

| Subcommand | Description |
|------------|-------------|
| `monolog email auth` | Run the OAuth flow once to authorize Gmail access. Saves the refresh token under `$XDG_CONFIG_HOME/monolog/gmail_token.json` and flips `enabled=true` in `config.json`. |
| `monolog email sync` | Fetch all messages carrying the configured label, import new ones as tasks, and commit them in a single batch. Prints `created N task(s)`. |
| `monolog email status` | Show auth state (token expiry or "not authorized"), `enabled` flag, label, sync interval, max-per-sync cap, and the resolved client-secrets / token paths. |

### `monolog telegram`

Telegram bot integration subcommands — see [Telegram integration](#telegram-integration).

| Subcommand | Description |
|------------|-------------|
| `monolog telegram serve` | Run the long-poll loop that lets allow-listed Telegram users capture, browse, and complete tasks from their phone. Intended to run as a systemd service on an always-on host. |
| `monolog telegram status` | Print the configured `enabled` flag, allow-list, pull interval, browse cap, and whether `MONOLOG_TELEGRAM_TOKEN` is set in the env. The token VALUE is never printed. |

### `monolog --version`

Print the monolog version.

## Active tasks

A task can be marked "active" to indicate it's part of your current working set. Active state is stored as the reserved `active` tag.

```bash
monolog edit 01J5K --active=true    # activate a task
monolog edit 01J5K --active=false   # deactivate
monolog ls --active                 # list all active tasks (ignores schedule filter)
monolog ls --active --schedule week # active tasks scheduled for this week only
```

Marking a task done automatically deactivates it. Editing tags with `--tags` preserves the active state.

## Recurring tasks

A task can carry a recurrence rule so that completing it auto-spawns the next occurrence. Four grammar forms are supported:

| Form | Example | Meaning |
|------|---------|---------|
| `monthly:N` | `monthly:1` | Nth of each month, clamped to month-end for short months (e.g. `monthly:31` in February becomes the 28th or 29th) |
| `weekly:<day>` | `weekly:mon`, `weekly:Monday`, `weekly:1` | Every given weekday. Accepts three-letter, full name, or numeric (Mon=1..Sun=7), case-insensitive |
| `workdays` | `workdays` | Every Monday–Friday (skips weekends) |
| `days:N` | `days:3` | N days after each completion |

Aliases are canonicalized on storage, so `weekly:Monday` and `weekly:1` both end up stored as `weekly:mon`.

```bash
monolog add "pay rent" --recur monthly:1       # 1st of every month
monolog add "standup" --recur workdays         # every weekday
monolog done <id>                              # completes this occurrence AND spawns the next
monolog edit <id> --recur ""                   # stop the chain (clear the rule)
```

When a recurring task is completed, monolog creates a new task with a fresh ID, the same title/body/tags (minus `active`)/recurrence, and `schedule` set to the next occurrence date. Bidirectional notes link the pair: the new task carries `Spawned from <old-id>` and the completed one gets `Spawned follow-up: <new-id> (scheduled <date>)`. Both files are committed in a single git commit with message `done: <title> (recurring, next <date>)`.

Stopping the chain is edit-based. Clearing the recurrence with `--recur ""` (or removing the `recurrence:` line in the TUI YAML editor) before completing prevents the next spawn. Deleting the task stops the chain too.

## TUI (interactive mode)

Running `monolog` with no subcommand launches the interactive TUI. Tabs across the top show `[Today] [Tomorrow] [Week] [Month] [Someday] [Done]`. Use `--tags` / `-T` to start in tag view, where tabs represent tags instead of schedule buckets.

| Key | Action |
|-----|--------|
| `←`/`→`, `Tab`/`Shift+Tab` | Switch tabs |
| `1`–`6` | Jump to tab by number |
| `↑`/`↓` | Move within list |
| `Enter` | Toggle detail/notes panel for the focused task |
| `c` | Open the add-task modal (title + tags + recur fields, Tab to cycle). The Recur field shows a grammar hint (`monthly:N \| weekly:<day> \| workdays \| days:N`) and inline autocomplete as you type — Tab accepts the highlighted suggestion (replaces the field), Enter always submits the modal, Esc dismisses the dropdown. |
| `d` | Mark focused task as done (if it has a recurrence rule, auto-spawns the next occurrence — see [Recurring tasks](#recurring-tasks)) |
| `a` | Toggle active on the focused task |
| `r` | Reschedule (modal with 1–5 presets or 6 for custom date in the configured format or relative shorthands `Nd`/`Nw`/`Nm`, e.g. `3d`, `2w`, `1m`) |
| `t` | Retag focused task |
| `e` | Edit in `$EDITOR` (YAML round-trip; the YAML includes a `recurrence:` field you can set or clear, with a `# recurrence rules:` grammar header comment at the top) |
| `m` | Grab/ungrab for reordering (↑/↓ reorder, ←/→ move between tabs, g/G top/bottom, +d/e/r/t/a/c/x/s actions) |
| `v` | Toggle between schedule view and tag view |
| `/` | Fuzzy search (type to filter, ↑/↓ or Ctrl+j/k to move, Enter to jump, Esc to cancel) |
| `x` | Delete task (with confirmation) |
| `s` | Sync (commit, pull --rebase, push). When [email integration](#email-integration) is enabled, also runs `email sync` in parallel via `tea.Batch`; the bottom-bar hint widens to `sync (git+email)`. |
| `u` / `Ctrl+z` | Undo the last mutation (multi-level, backed by `git revert`) |
| `Ctrl+y` | Redo the last undone action |
| `,` | Settings modal (date format, theme) |
| `h` | Help modal |
| `q` | Quit |

Active tasks render in green in the list and appear in a dedicated panel above the tab bar. The panel auto-hides when no tasks are active.

The running TUI also auto-refreshes when another process mutates the store — a Raycast capture, a second terminal running `monolog add`, or an external `git pull`. Set `MONOLOG_NO_WATCH=1` to disable the file watcher.

## Configuration

Monolog reads runtime settings from environment variables and an optional `config.json`.

| Variable | Purpose |
|----------|---------|
| `MONOLOG_DIR` | Data directory (default `~/.monolog`) |
| `MONOLOG_THEME` | TUI color theme (`default`, `dracula`, or a user theme name); takes precedence over `config.json` |
| `MONOLOG_NO_LINKS` | Set to `1` to disable OSC 8 clickable URLs in the TUI |
| `MONOLOG_NO_WATCH` | Set to `1` to disable the external-change file watcher |

Persistent settings live in `<MONOLOG_DIR>/.monolog/config.json`:

```json
{
  "theme": "default",
  "date_format": "02-01-2006"
}
```

The TUI settings modal (`,`) writes `theme` and `date_format`; the optional `email` and `telegram` blocks (documented below) are hand-edited or written by their respective `auth`/`serve` flows. Unknown keys are preserved on save.

## Themes

Monolog ships with two built-in color themes (`default` and `dracula`) and loads additional user-authored themes from `<MONOLOG_DIR>/.monolog/themes/*.json` at startup. The filename (minus `.json`) becomes the theme name and appears in the TUI settings cycle (`,` key).

On the first TUI launch in a new repo, monolog writes `themes/example.json` containing a full dump of the `default` theme. Copy it, tweak colors, and restart:

```bash
cp ~/.monolog/.monolog/themes/example.json ~/.monolog/.monolog/themes/mytheme.json
# edit mytheme.json — change any of the 19 color roles
# then select it from the TUI settings modal (,) or set
# "theme": "mytheme" in <MONOLOG_DIR>/.monolog/config.json
```

A theme file is plain JSON with one `{"light", "dark"}` pair per color role. All 19 fields are required; values are hex (`#ff79c6`) or ANSI codes (`"240"`):

```json
{
  "active_border":         {"light": "28",      "dark": "28"},
  "modal_border":          {"light": "62",      "dark": "62"},
  "hotkey":                {"light": "9",       "dark": "9"},
  "active_tab_bg":         {"light": "62",      "dark": "62"},
  "active_tab_fg":         {"light": "231",     "dark": "231"},
  "tab_fg":                {"light": "244",     "dark": "244"},
  "borders":               {"light": "240",     "dark": "240"},
  "normal_text":           {"light": "#1a1a1a", "dark": "#dddddd"},
  "selected_text":         {"light": "#EE6FF8", "dark": "#EE6FF8"},
  "dim_text":              {"light": "#A49FA5", "dark": "#777777"},
  "grab_text":             {"light": "#D97706", "dark": "#FFB454"},
  "active_normal":         {"light": "#16A34A", "dark": "#22C55E"},
  "active_selected":       {"light": "#15803D", "dark": "#4ADE80"},
  "search_done":           {"light": "244",     "dark": "244"},
  "search_active":         {"light": "76",      "dark": "76"},
  "search_count":          {"light": "240",     "dark": "240"},
  "search_meta":           {"light": "244",     "dark": "244"},
  "search_preview_border": {"light": "240",     "dark": "240"},
  "search_preview_dim":    {"light": "240",     "dark": "240"}
}
```

Rules:

- Reload is edit-and-restart — there is no file watcher.
- Missing fields, invalid JSON, or empty color values cause the file to be skipped with a warning on stderr. The TUI continues with whatever loaded cleanly.
- Files named `default.json` or `dracula.json` are rejected — built-ins are immutable.
- If the configured theme disappears from disk, monolog falls back to `default` and shows `theme "<name>" not found, using default` in the status bar without rewriting `config.json`, so restoring the file brings the selection back.
- `MONOLOG_THEME` env var takes precedence over the `config.json` setting.

## Email integration

Monolog can import Gmail messages labeled `monolog` (or any other label you configure) as tasks, and archive the email when the task is completed. The feature is opt-in: until you run `monolog email auth` it stays silent — no on-launch sync, no `s`-keybinding overload, no extra git activity.

### One-time GCP setup

You bring your own Google Cloud OAuth credentials. This keeps your account out of any shared client and means monolog never sees a third-party server.

1. Create a project at [console.cloud.google.com](https://console.cloud.google.com).
2. Enable the **Gmail API** under "APIs & Services" → "Library".
3. Configure the OAuth consent screen. "External" user type is fine; add your own email address as a test user.
4. Create OAuth 2.0 credentials of type **Desktop app**.
5. Download the credentials JSON to `~/.config/monolog/gmail_credentials.json` (or wherever `email.client_secrets_path` points).
6. Run `monolog email auth` — a browser tab opens for consent, the refresh token is saved to `~/.config/monolog/gmail_token.json` (mode `0600`), and `enabled` flips to `true` in `config.json` automatically.

OAuth scope is `gmail.modify` — the smallest scope that allows listing labeled messages, reading metadata + snippet, and removing the `INBOX` label. Monolog cannot send mail, read full message bodies, or touch other mailboxes.

The token and client-secrets files live OUTSIDE your monolog git repo (under `$XDG_CONFIG_HOME/monolog/`, default `~/.config/monolog/`) so OAuth secrets never get committed when you sync across devices.

### Configuration

A new optional `email` block in `<MONOLOG_DIR>/.monolog/config.json`:

```json
{
  "theme": "default",
  "date_format": "02-01-2006",
  "email": {
    "enabled": true,
    "label": "monolog",
    "sync_interval_minutes": 5,
    "max_per_sync": 100,
    "client_secrets_path": "~/.config/monolog/gmail_credentials.json"
  }
}
```

All keys are optional. Defaults: `enabled=false`, `label="monolog"`, `sync_interval_minutes=5`, `max_per_sync=100`, `client_secrets_path=$XDG_CONFIG_HOME/monolog/gmail_credentials.json`.

`monolog email auth` writes the block for you on first run; you only need to hand-edit `config.json` to tweak the label or interval.

### How import works

1. Create a Gmail label called `monolog` (or whatever you set `email.label` to).
2. Apply the label to any email you want imported as a task.
3. Run `monolog email sync` (CLI) or press `s` in the TUI (which now runs both git sync and email sync via `tea.Batch`). The TUI also runs an initial sync on launch and re-syncs every `sync_interval_minutes`.
4. Each new message becomes a task with:
   - **Title** — the subject with chained `Re:` / `Fwd:` / `Fw:` prefixes stripped (case-insensitive). Empty subjects render as `(no subject)`.
   - **Body** — `From: <name>\nhttps://mail.google.com/mail/#all/<msg-id>\n\n<snippet>`. The Gmail URL omits `/u/0/` so it works regardless of which Google account is currently signed in. Snippet is HTML-unescaped and hard-capped at 200 characters with a `…` suffix only when truncation occurs.
   - **Tags** — `["email"]`. The reserved `active` tag is never auto-applied.
   - **Schedule** — `today`.
   - **Source** — `"gmail"`. **SourceID** — the Gmail message ID.
5. All new tasks from a single sync run are written to disk and committed in **one** git commit: `email: imported N task(s) (label=<label>)`. Per-task write failures warn to stderr but don't abort the batch — the run still commits whatever succeeded.
6. Dedup is built per-sync from a directory scan: the set of `SourceID`s on existing tasks (open and done) where `Source=="gmail"`. Completed-and-archived emails are self-suppressing — they won't re-import even though the `monolog` label stays on them. Deleting a task in monolog without archiving in Gmail WILL re-import it on the next sync.
7. Soft cap: when more than `max_per_sync` new messages are pending, the first N (newest first) are imported and the rest are silently deferred to the next run. No error, no warning — just keep syncing.

### Archive on done

When you complete a gmail-sourced task (CLI `monolog done <id>` or TUI `d` key), monolog calls `users.messages.modify` with `removeLabelIds=["INBOX"]` so the email is archived in Gmail. The trigger label (e.g. `monolog`) is intentionally retained — archive only, not unlabel. A 5-second context timeout keeps the CLI from hanging on flaky network. Archive failure is non-fatal: the task stays done, an error is printed, and the next sync run will simply re-import the still-labeled-and-still-in-inbox email if you delete the task.

The TUI shows a one-line flash (`email archived` or `archive failed: <err>`) on the status bar. While a sync is in flight, a small `↻` indicator appears at the right of the stats bar.

### Manual config edits

The TUI settings modal (`,`) does NOT yet expose email config — edit `config.json` directly to tweak the label, interval, or paths. The `enabled` flag is the one exception: `monolog email auth` toggles it on for you.

### Token storage

Tokens auto-refresh transparently. The refreshed `access_token` is written back to `$XDG_CONFIG_HOME/monolog/gmail_token.json` (mode `0600`) so subsequent processes (CLI invocations, TUI launches) pick up the new value without re-authorizing. If the refresh token itself is revoked, `monolog email status` reports the error and you can re-run `monolog email auth`.

## Telegram integration

Monolog ships a long-polling Telegram bot that lets you capture, browse, and complete tasks from your phone. The bot is a normal monolog client — it owns a clone of the tasks git repo and uses the same `store` + `git` paths as the CLI. The feature is opt-in: until you populate the `"telegram"` block in `config.json` and run `monolog telegram serve`, no token, listener, or extra git activity exists.

### One-time BotFather setup

1. DM [@BotFather](https://t.me/BotFather) on Telegram and send `/newbot`. Pick a name and unique username; BotFather replies with an HTTP API token.
2. DM [@userinfobot](https://t.me/userinfobot) and note your numeric Telegram user ID — the allow-list is keyed on IDs, not usernames.
3. Edit `<MONOLOG_DIR>/.monolog/config.json` and add the `"telegram"` block:

   ```json
   {
     "telegram": {
       "enabled": true,
       "allowed_user_ids": [12345678],
       "pull_interval_seconds": 30,
       "browse_limit": 20
     }
   }
   ```

   Defaults if omitted: `enabled=false`, `allowed_user_ids=[]` (rejects everyone — explicit allow-list is the only auth), `pull_interval_seconds=30`, `browse_limit=20`.

4. Run `monolog telegram serve --token <token>` for an ad-hoc local run, or follow the systemd deployment below for an always-on bot.

### Interaction model

Send any free text to **capture** a task scheduled for today. Hashtags anywhere on the first line become tags (`buy milk #shopping #urgent`). A leading `tagname: ...` auto-applies an existing tag (same rule as the CLI). Multi-line messages put the first line as the title and the rest as the body — hashtags inside the body survive untouched.

Each captured task replies with a summary card carrying three inline buttons:

- **Done** — complete the task. If it carries a recurrence rule the next occurrence spawns automatically with a `↻ next: <date>` line on the strike-through reply.
- **Active** — toggle the reserved `active` tag.
- **Details** — expand to the full body + metadata. The expanded view swaps `Details` for **Collapse** so you can fold it back without cluttering the chat.

Slash commands cover the read-only side:

| Command | Action |
|---------|--------|
| `/today` | Today's bucket |
| `/week` | The week bucket (strictly after tomorrow through +7 days) |
| `/active` | All tasks tagged `active` |
| `/all` | Every open task, capped at `browse_limit` with a `+N more — open laptop` footer when over the cap |
| `/help`, `/start` | Static cheatsheet covering capture, browse, buttons, and reply-to-note |

**Notes via reply**: reply to any task summary message with text and the bot appends a timestamped note to that task. The reply path uses the same `store.Resolve` lookup as the CLI (ULID prefix or title-initials), so ambiguous prefixes surface the usual conflict error.

### Access control

The `allowed_user_ids` array is the only authentication layer. Updates from any user ID not in the list are silently dropped — the bot does not reply, does not log, and does not reveal its existence to drive-by queries. Callback queries from non-allowed users get a silent `AnswerCallback` so Telegram stops the loading spinner on their button, but no message or edit is sent.

Rotating: edit the array in `config.json`, commit, and either restart the bot or wait for the next pull tick to pick up the change.

### Read-only mode on sync conflict

Every write path (capture, Done, Active, note-reply) ends with `git sync` (commit → pull --rebase → push). When the rebase fails the bot flips into read-only mode: subsequent writes reply with `⚠️ sync conflict, change not saved — resolve on laptop` and browse output prepends a `⚠️ read-only — sync conflict pending` banner. The state heals automatically on the next clean pull (every `pull_interval_seconds`), or you can restart the bot after resolving the conflict on the laptop.

### Token precedence

- `--token <value>` flag wins when non-empty (intended for ad-hoc local runs).
- `MONOLOG_TELEGRAM_TOKEN` environment variable is the fallback.

For systemd / long-running deployments prefer the env var: a flag value is visible to any user with `ps aux` on the host. The bot empty-token guards at startup so a misconfigured `EnvironmentFile=` fails loudly instead of silently spinning on 401s.

### Deployment

See [`docs/deploy/README.md`](docs/deploy/README.md) for the full one-time EC2 setup checklist: bot user creation, SSH deploy key, systemd unit, env file with `MONOLOG_TELEGRAM_TOKEN` / `GIT_SSH_COMMAND` / `MONOLOG_DIR`, and the smoke-test sequence.

Build and ship from the laptop with:

```sh
make build-bot-linux-arm64                        # cross-compile to dist/monolog-linux-arm64
make deploy-bot EC2_HOST=ec2-user@<elastic-ip>    # scp + restart systemd unit
```

The bot loses long-poll position briefly during restart; Telegram queues updates by `update_id` so nothing is dropped.

## Date format

User-facing dates default to `DD-MM-YYYY` — this covers CLI `--schedule` input, TUI reschedule/YAML-edit input, the task-list date column, the detail panel, recurrence commit messages and cross-reference notes, note separators inside task bodies, and error messages. Legacy ISO input (`YYYY-MM-DD`) is still accepted silently so older scripts keep working. The TUI reschedule modal's custom date input also accepts relative shorthands (`Nd`, `Nw`, `Nm` — e.g. `3d` = 3 days, `2w` = 2 weeks, `1m` = 1 month from today) regardless of the configured date format.

On-disk storage always stays ISO (`"schedule": "2026-04-15"` in the JSON) regardless of the display format — this keeps `.monolog/` repos portable and sync-safe.

The format is selectable from the TUI settings modal (`,`) and persisted as the `date_format` key in `config.json`.

## Task lookup

Commands that take a task identifier (`done`, `edit`, `rm`, `mv`, `note`, `show`) resolve it in two steps:

1. **ULID prefix** — type the first few characters of the task ID (e.g. `01J5K`).
2. **Title initials** — if no ULID matches and you typed at least 2 characters, monolog computes the first letter of each word in every open task's title and looks for a prefix match.

```bash
monolog done 01J5K   # ULID prefix
monolog done flb     # matches "Fix login bug" (f-l-b)
monolog done fl      # also matches, if unambiguous
monolog done FL      # case-insensitive
```

If multiple tasks share the same initials prefix, monolog reports the ambiguity and lists the conflicting titles.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for build/test commands, the testing rule (every change ships tests; all tests pass before merge), and the planning workflow.

## License

Monolog is released under the MIT License. See [LICENSE](LICENSE).
