# Monolog

A CLI personal backlog tool. Tasks are stored as individual JSON files in a git repo for conflict-free cross-device sync.

## Install

```bash
go install github.com/mmaksmas/monolog@latest
```

Requires Go 1.22+.

## Quick start

```bash
monolog init                        # create a new monolog repo at ~/.monolog
monolog add "Review PR #42"         # add a task (defaults to today)
monolog add "Write tests" -s week   # schedule for this week
monolog ls                          # list today's open tasks
monolog done 01J5K                  # mark done by ID prefix
monolog sync                        # push/pull with a git remote
```

Set `MONOLOG_DIR` to store data somewhere other than `~/.monolog`.

## Commands

### `monolog init [--remote <url>]`

Initialize a monolog repo. Optionally set a git remote for sync.

### `monolog add <title>`

| Flag | Description |
|------|-------------|
| `-s, --schedule` | `today` (default), `tomorrow`, `week`, `month`, `someday`, or ISO date |
| `-t, --tags` | Comma-separated tags |

### `monolog ls`

Lists today's open tasks by default. Each row includes a compact dates column: relative for recent tasks (`5m`, `3h`, `2d`), `MM-DD` for older same-year tasks, and `YY-MM-DD` for cross-year. Done tasks show `created→done` (e.g. `5d→1h`). Active tasks are marked with a leading `*`.

| Flag | Description |
|------|-------------|
| `-a, --all` | Show all open tasks across all schedules |
| `-s, --schedule` | Filter by schedule value |
| `-t, --tag` | Filter by tag |
| `-d, --done` | Show completed tasks |
| `--active` | Show only active tasks (lifts the default today filter unless `--schedule` is also given) |

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

### `monolog bump`

Promote tasks to today: `tomorrow` becomes `today`, past ISO dates become `today`. Intended for daily use (manually or via cron).

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

## TUI (`monolog tui`)

| Key | Action |
|-----|--------|
| `c` | Open the add-task modal (title + tags fields, Tab to switch) |
| `a` | Toggle active on the focused task |
| `d` | Mark focused task as done |
| `t` | Retag focused task |
| `m` | Grab/ungrab for reordering |

Active tasks render in green in the list and appear in a dedicated panel above the tab bar. The panel auto-hides when no tasks are active.

## How it works

Each task is a JSON file in `.monolog/tasks/<ULID>.json`. Every mutation auto-commits to git. You reference tasks by typing the first few characters of their ID (prefix matching). Ordering uses fractional positions with automatic rebalancing.
