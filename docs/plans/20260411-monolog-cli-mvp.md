# Monolog — CLI Backlog Tool (MVP)

## Overview

Monolog is a CLI tool that provides a unified personal backlog. The MVP focuses on
manual task management with git-based storage. Future phases will add integrations
(Slack, email, Jira) to pull tasks from external sources.

**Core problem:** Personal backlogs are scattered across Slack messages, emails, Jira
tickets, and mental notes. Monolog gives them one home — a git repo you can sync
across devices.

**MVP scope:** Manual task CRUD, manual ordering, scheduling (today/tomorrow/someday),
git-based persistence with sync.

## Tech Spec

### Language & Tooling

- **Language:** Go
- **CLI framework:** [cobra](https://github.com/spf13/cobra) — industry standard for Go CLIs
- **Build:** standard `go build`, single binary output
- **Minimum Go version:** 1.22+

### Data Model

Each task is a single JSON file stored in `<repo>/.monolog/tasks/<id>.json`.

```json
{
  "id": "01J5K3...",
  "title": "Review PR #42",
  "body": "Optional longer description or notes",
  "source": "manual",
  "status": "open",
  "position": 1500,
  "schedule": "today",
  "created_at": "2026-04-11T10:00:00Z",
  "updated_at": "2026-04-11T10:00:00Z",
  "tags": ["work"]
}
```

| Field        | Type     | Description                                                        |
|-------------|----------|--------------------------------------------------------------------|
| `id`        | string   | ULID — sortable, unique, no coordination needed                    |
| `title`     | string   | Short one-line summary (required)                                  |
| `body`      | string   | Optional longer description                                       |
| `source`    | string   | Origin: `manual`, future: `slack`, `jira`, `email`                 |
| `status`    | string   | `open` or `done`                                                   |
| `position`  | float64  | Ordering key. Lower = higher in list. Fractional for easy inserts  |
| `schedule`  | string   | `today`, `tomorrow`, `week`, `someday`, or ISO date `2026-04-15`   |
| `created_at`| RFC3339  | Creation timestamp                                                 |
| `updated_at`| RFC3339  | Last modification timestamp                                        |
| `tags`      | []string | Optional freeform tags                                             |

**Why one file per task:** Eliminates merge conflicts when syncing across devices.
Two devices can create/edit different tasks and `git pull --rebase` just works.

**Why ULID for IDs:** Time-sortable (creation order is preserved), globally unique
without a counter file, and short enough to type partial prefixes for CLI commands.

**Why fractional positions:** Inserting task B between tasks at position 1000 and 2000
gives B position 1500 — no need to renumber everything. Periodic rebalancing if
positions get too close (gap < 1).

### Directory Structure

```
~/.monolog/                     # Default repo location (configurable)
├── .git/
├── .monolog/
│   ├── tasks/
│   │   ├── 01J5K3A1B2C3.json
│   │   ├── 01J5K3D4E5F6.json
│   │   └── ...
│   └── config.json             # Local config (default schedule, etc.)
└── .gitignore
```

The monolog data lives inside a dedicated git repository. The user inits it once,
optionally adds a remote, and syncs with `monolog sync`.

### CLI Commands (MVP)

```
monolog init                        # Initialize a new monolog repo
monolog add "task title"            # Add a task (default: schedule=today, bottom of list)
monolog add "title" -s tomorrow     # Add scheduled for tomorrow
monolog add "title" -s week         # Add scheduled for next week
monolog add "title" -s someday      # Add to someday/icebox
monolog add "title" -t work,urgent  # Add with tags

monolog ls                          # List today's tasks, ordered by position
monolog ls --all                    # List ALL open tasks across all schedules
monolog ls --schedule tomorrow      # List tasks scheduled for tomorrow
monolog ls --schedule someday       # List someday tasks
monolog ls --tag work               # Filter by tag
monolog ls --done                   # List completed tasks

monolog done <id-prefix>            # Mark task as done
monolog edit <id-prefix>            # Edit task title/body/schedule/tags
monolog rm <id-prefix>              # Delete a task

monolog mv <id-prefix> --top        # Move task to top of its schedule group
monolog mv <id-prefix> --bottom     # Move to bottom
monolog mv <id-prefix> --after <id> # Move after another task
monolog mv <id-prefix> --before <id># Move before another task

monolog bump                        # Auto-promote: move yesterday's undone tasks to today

monolog sync                        # git pull --rebase && git push
monolog log                         # Show recently completed tasks
```

**ID prefix matching:** Users type the first few characters of a ULID (e.g., `01J5K`)
and monolog resolves it. Error if ambiguous.

### Schedule Semantics

| Schedule     | Meaning                                                    |
|-------------|-------------------------------------------------------------|
| `today`     | Active work for today. Default view.                        |
| `tomorrow`  | Queued for tomorrow. `monolog bump` promotes these at dawn. |
| `week`      | Planned for this week, not yet today.                       |
| `someday`   | Icebox. Out of sight until explicitly pulled forward.       |
| ISO date    | Scheduled for a specific future date. Promoted on that day. |

`monolog bump` (intended to run daily, manually or via cron):
- Tasks with `schedule: "tomorrow"` → become `today`
- Tasks with a past ISO date → become `today`
- Tasks with `schedule: "today"` that are still open → remain `today` (they carry over)

### Git Sync Model

```
monolog init                          # git init + directory scaffold
monolog init --remote <url>           # git init + add origin + initial push
monolog sync                          # git add -A && git commit && git pull --rebase && git push
```

- Every mutation (`add`, `done`, `edit`, `rm`, `mv`) auto-commits with a descriptive message
- `sync` is the only command that talks to remotes
- Conflicts on one-file-per-task are near impossible; if they happen, standard git resolution applies

### Config

`<repo>/.monolog/config.json`:
```json
{
  "default_schedule": "today",
  "editor": "$EDITOR"
}
```

Environment variable `MONOLOG_DIR` overrides the default `~/.monolog` path.

## Context

- **Project directory:** `/Users/mmaksmas/IdeaProjects/monolog` (empty, greenfield)
- **No existing code or dependencies**
- **Target:** single Go binary, installable via `go install`

## Development Approach

- **Testing approach:** TDD (tests first, then implementation)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Project scaffold and Go module

**Files:**
- Create: `go.mod`
- Create: `main.go`
- Create: `cmd/root.go`

- [x] Run `go mod init github.com/mmaksmas/monolog`
- [x] Create `main.go` with entrypoint calling cobra root command
- [x] Create `cmd/root.go` with root cobra command (version flag, help text)
- [x] Add cobra dependency
- [x] Verify `go build` produces working binary with `--help`
- [x] Write test for root command execution
- [x] Run tests — must pass before next task

### Task 2: Data model and task storage

**Files:**
- Create: `internal/model/task.go`
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`

- [x] Define `Task` struct in `internal/model/task.go` with JSON tags
- [x] Add ULID generation dependency (`github.com/oklog/ulid/v2`)
- [x] Write tests for Create, Get, Update, Delete (failing — Store not yet implemented)
- [x] Implement `Store` in `internal/store/store.go` — manages the tasks directory
- [x] Implement `Store.Create(task)` — writes task JSON file
- [x] Implement `Store.Get(id)` — reads a single task file
- [x] Implement `Store.Update(task)` — overwrites task file, updates `updated_at`
- [x] Implement `Store.Delete(id)` — removes task file
- [x] Run tests — Create/Get/Update/Delete must pass
- [x] Write tests for GetByPrefix (exact, ambiguous, not found — failing)
- [x] Implement `Store.GetByPrefix(prefix)` — prefix-match lookup
- [x] Run tests — GetByPrefix must pass
- [x] Write tests for List with various filter combinations (failing)
- [x] Implement `Store.List(filters)` — reads all tasks, applies filters (schedule, status, tag), sorts by position
- [x] Run tests — all must pass before next task

### Task 3: Position management

**Files:**
- Create: `internal/ordering/ordering.go`
- Create: `internal/ordering/ordering_test.go`

- [x] Write tests for NextPosition, PositionBetween, PositionTop (empty list, single item, multiple items — failing)
- [x] Implement `NextPosition(tasks)` — returns position after the last task
- [x] Implement `PositionBetween(a, b)` — returns midpoint for inserting between two tasks
- [x] Implement `PositionTop(tasks)` — returns position before the first task
- [x] Run tests — position functions must pass
- [x] Write tests for Rebalance (tight gaps, already balanced — failing)
- [x] Implement `Rebalance(tasks)` — evenly redistributes positions (gap threshold < 1)
- [x] Run tests — all must pass before next task

### Task 4: `monolog init` command

**Files:**
- Create: `internal/git/git.go`
- Create: `cmd/init.go`
- Create: `cmd/init_test.go`

- [x] Write tests for git.Init (creates expected directory structure — failing)
- [x] Implement `git.Init(path)` — runs `git init`, creates `.monolog/tasks/` and `.monolog/config.json`
- [x] Write `.gitignore` during init
- [x] Run tests — basic init must pass
- [x] Write tests for init with remote flag (failing)
- [x] Implement `git.Init` with `--remote` flag — adds origin and does initial commit+push
- [x] Run tests — init with remote must pass
- [x] Create `cmd/init.go` with `monolog init [--remote <url>]` cobra command
- [x] Write tests for init command integration
- [x] Run tests — all must pass before next task

### Task 5: `monolog add` command

**Files:**
- Create: `cmd/add.go`
- Create: `cmd/add_test.go`

- [ ] Write tests for add command (default schedule, custom schedule, tags — failing)
- [ ] Create `cmd/add.go` — accepts title as positional arg
- [ ] Support `-s` / `--schedule` flag (default: `today`)
- [ ] Support `-t` / `--tags` flag (comma-separated)
- [ ] Run tests — add command logic must pass
- [ ] Write tests for auto-commit behavior (failing)
- [ ] Implement auto-commit after adding: `git add <file> && git commit -m "add: <title>"`
- [ ] Run tests — all must pass before next task

### Task 6: `monolog ls` command

**Files:**
- Create: `cmd/ls.go`
- Create: `cmd/ls_test.go`
- Create: `internal/display/table.go`

- [ ] Write tests for display formatting (table output — failing)
- [ ] Create `internal/display/table.go` — formats tasks as a clean terminal table (position indicator, short ID, title, schedule, tags)
- [ ] Run tests — display formatting must pass
- [ ] Write tests for ls command with different filters (failing)
- [ ] Create `cmd/ls.go` — default shows today's open tasks sorted by position
- [ ] Support `--all` flag to show all open tasks
- [ ] Support `--schedule <value>` filter
- [ ] Support `--tag <value>` filter
- [ ] Support `--done` flag to show completed tasks
- [ ] Run tests — all must pass before next task

### Task 7: `monolog done`, `monolog edit`, `monolog rm` commands

**Files:**
- Create: `cmd/done.go`
- Create: `cmd/edit.go`
- Create: `cmd/rm.go`
- Create: `cmd/task_commands_test.go`

- [ ] Write tests for done (status change, auto-commit, error on bad prefix — failing)
- [ ] Implement `monolog done <id-prefix>` — sets status to `done`, auto-commits
- [ ] Run tests — done must pass
- [ ] Write tests for rm (file deletion, auto-commit, error on bad prefix — failing)
- [ ] Implement `monolog rm <id-prefix>` — deletes task file, auto-commits
- [ ] Run tests — rm must pass
- [ ] Write tests for edit with inline flags (failing)
- [ ] Implement `monolog edit <id-prefix>` — supports inline flags (`--title`, `--schedule`, `--tags`), auto-commits
- [ ] Run tests — all must pass before next task

### Task 8: `monolog mv` command (reordering)

**Files:**
- Create: `cmd/mv.go`
- Create: `cmd/mv_test.go`

- [ ] Write tests for mv --top and --bottom (failing)
- [ ] Implement `monolog mv <id> --top` — moves task to top of its schedule group
- [ ] Implement `monolog mv <id> --bottom` — moves to bottom
- [ ] Run tests — top/bottom must pass
- [ ] Write tests for mv --before and --after (failing)
- [ ] Implement `monolog mv <id> --before <id>` — inserts before target
- [ ] Implement `monolog mv <id> --after <id>` — inserts after target
- [ ] Run tests — before/after must pass
- [ ] Write tests for rebalance trigger and auto-commit (failing)
- [ ] Trigger rebalance if position gap drops below threshold
- [ ] Auto-commit after move
- [ ] Run tests — all must pass before next task

### Task 9: `monolog bump` and `monolog log`

**Files:**
- Create: `cmd/bump.go`
- Create: `cmd/log.go`
- Create: `cmd/bump_test.go`

- [ ] Write tests for bump (tomorrow→today, past ISO→today, today stays, week stays — failing)
- [ ] Implement `monolog bump` — promotes `tomorrow` → `today`, past ISO dates → `today`
- [ ] Auto-commit all changes in one commit: `"bump: promote N tasks to today"`
- [ ] Run tests — bump must pass
- [ ] Write tests for log output (last 7 days, empty — failing)
- [ ] Implement `monolog log` — lists recently completed tasks (last 7 days by default)
- [ ] Run tests — all must pass before next task

### Task 10: `monolog sync` (git remote sync)

**Files:**
- Create: `cmd/sync.go`
- Create: `cmd/sync_test.go`

- [ ] Write tests for sync (with changes, nothing to commit, no remote — failing)
- [ ] Implement `monolog sync` — `git add -A && git commit -m "sync" && git pull --rebase && git push`
- [ ] Handle case where nothing to commit (skip commit step)
- [ ] Handle case where no remote configured (warn and skip)
- [ ] Surface git errors clearly to user
- [ ] Run tests — all must pass before next task

### Task 11: Verify acceptance criteria

- [ ] Verify full workflow: init → add tasks → list → reorder → done → sync
- [ ] Verify schedule filtering works correctly
- [ ] Verify prefix matching with ambiguous and exact inputs
- [ ] Verify bump logic across schedule types
- [ ] Run full test suite: `go test ./...`
- [ ] Verify `go build` produces clean binary

### Task 12: [Final] Documentation and polish

- [ ] Write README.md with install instructions, quick start, command reference
- [ ] Add CLAUDE.md with project conventions
- [ ] Move this plan to `docs/plans/completed/`

## Technical Details

### Position Assignment Strategy

New tasks get `position = max(existing) + 1000`. This leaves ample room for
insertions. When inserting between two tasks, use the midpoint. If the gap
between any two adjacent tasks drops below 1.0, rebalance all tasks in that
schedule group to positions `1000, 2000, 3000, ...`.

### ID Prefix Resolution

When user types `01J5K`, scan all task filenames in the directory. If exactly
one matches the prefix, use it. If zero or multiple match, return a clear error
with suggestions.

### Auto-commit Messages

| Command | Commit message format           |
|---------|---------------------------------|
| add     | `add: <title>`                  |
| done    | `done: <title>`                 |
| edit    | `edit: <title>`                 |
| rm      | `rm: <title>`                   |
| mv      | `mv: <title> to <position>`     |
| bump    | `bump: promote N tasks to today` |

## Post-Completion

**Future phases (not in MVP scope):**

- **Phase 2 — Integrations:** Jira import (`monolog pull jira`), Slack starred messages, email parsing
- **Phase 3 — Smart scheduling:** Auto-suggest priorities based on deadlines, workload balancing
- **Phase 4 — TUI:** Interactive terminal UI with real-time reordering (bubbletea)

**Distribution:**
- `go install github.com/mmaksmas/monolog@latest`
- Homebrew tap (later)
