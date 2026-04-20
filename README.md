# Monolog

A CLI personal backlog tool. Tasks are stored as individual JSON files in a git repo for conflict-free cross-device sync.

## Install

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

Set `MONOLOG_DIR` to store data somewhere other than `~/.monolog`.

## Commands

### `monolog init [--remote <url>]`

Initialize a monolog repo. Optionally set a git remote for sync.

### `monolog add <title>`

| Flag | Description |
|------|-------------|
| `-s, --schedule` | `today` (default), `tomorrow`, `week`, `month`, `someday`, or a date (default format `DD-MM-YYYY`; legacy ISO `YYYY-MM-DD` is still accepted) |
| `-t, --tags` | Comma-separated tags |
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
| `r` | Reschedule (modal with 1–5 presets or 6 for custom date) |
| `t` | Retag focused task |
| `e` | Edit in `$EDITOR` (YAML round-trip; the YAML includes a `recurrence:` field you can set or clear, with a `# recurrence rules:` grammar header comment at the top) |
| `m` | Grab/ungrab for reordering (↑/↓ reorder, ←/→ move between tabs, g/G top/bottom, +d/e/r/t/a/c/x/s actions) |
| `v` | Toggle between schedule view and tag view |
| `/` | Fuzzy search (type to filter, ↑/↓ or Ctrl+j/k to move, Enter to jump, Esc to cancel) |
| `x` | Delete task (with confirmation) |
| `s` | Sync (commit, pull --rebase, push) |
| `q` | Quit |

Active tasks render in green in the list and appear in a dedicated panel above the tab bar. The panel auto-hides when no tasks are active.

## How it works

Each task is a JSON file in `.monolog/tasks/<ULID>.json`. Every mutation auto-commits to git. Ordering uses fractional positions with automatic rebalancing.

## Date format

User-facing dates default to `DD-MM-YYYY` — this covers CLI `--schedule` input, TUI reschedule/YAML-edit input, the task-list date column, the detail panel, recurrence commit messages and cross-reference notes, note separators inside task bodies, and error messages. Legacy ISO input (`YYYY-MM-DD`) is still accepted silently so older scripts keep working.

On-disk storage always stays ISO (`"schedule": "2026-04-15"` in the JSON) regardless of the display format — this keeps `.monolog/` repos portable and sync-safe.

The format is a compile-time default today, owned by `internal/config`. A future release will expose it as a configurable setting; at that point adding e.g. `YYYY-MM-DD` or `MM/DD/YYYY` will only touch the config package.

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
