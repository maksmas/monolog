# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Monolog is a CLI personal backlog tool written in Go. Tasks are stored as individual JSON files in a git repo for conflict-free cross-device sync. See `docs/plans/completed/20260411-monolog-cli-mvp.md` for the MVP spec.

## Build & Test Commands

```bash
go build -o monolog          # Build binary
go test ./...                # Run all tests
go test ./internal/store/    # Run tests for a single package
go test -run TestCreate ./internal/store/  # Run a single test
go vet ./...                 # Lint
```

## Architecture

```
main.go                 → Entrypoint, calls cobra root command
cmd/                    → One file per CLI command (add.go, ls.go, etc.)
internal/model/task.go  → Task struct with JSON tags, ULID generation
internal/store/         → File-based CRUD, prefix-match ID lookup, initials resolution, filtering/sorting
internal/ordering/      → Fractional position math and rebalancing
internal/schedule/      → Bucket names ↔ ISO dates, bucket classification
internal/display/       → Terminal table formatting and compact date helpers
internal/git/           → Git operations (init, auto-commit, sync with conflict resolution)
internal/tui/           → Bubble Tea interactive TUI (default when run with no subcommand)
```

Commands in `cmd/` handle CLI parsing and delegate to `internal/` packages. Running `monolog` with no subcommand launches the interactive TUI. Every mutation auto-commits to git.

## Key Design Decisions

- **One JSON file per task** (`<repo>/.monolog/tasks/<ULID>.json`): eliminates merge conflicts on sync. Two devices editing different tasks rebase cleanly.
- **ULID IDs**: time-sortable, globally unique, users type partial prefixes (e.g. `01J5K`) which resolve via directory scan.
- **Title-initials lookup**: CLI commands (`done`, `edit`, `rm`, `mv`) resolve identifiers via `store.Resolve()`, which tries ULID prefix first, then falls back to matching the first letters of each word in open task titles (e.g. `flb` → "Fix login bug"). Minimum 2 characters, case-insensitive, prefix-matching on initials. Ambiguous matches list conflicting titles. Implemented via `model.Initials()` and `store.Resolve()`.
- **Fractional positions**: inserting between positions 1000 and 2000 gives 1500. Rebalance all positions in a schedule group when any gap drops below 1.0. Default spacing is 1000.
- **Schedule values**: `today` (default), `tomorrow`, `week`, `month`, `someday`, or ISO date (`2026-04-15`). Stored on disk as ISO dates; bucket names are input shorthand and display-time predicates.
- **Runtime data directory**: `~/.monolog/` by default, override with `MONOLOG_DIR` env var.
- **Active tasks**: the reserved `active` tag marks tasks in the current working set. Active tasks render in green in the TUI and appear in a dedicated panel above the tab bar. `done` auto-deactivates; `edit --tags` preserves active state.
- **Auto-tag from title prefix**: when creating a task, if the title starts with `"tagname: ..."` and `tagname` is a tag that already exists on another task, it is automatically added as a tag. Title is kept as-is. The reserved `active` tag is excluded. Implemented via `model.ParseTitleTag()` and `model.CollectTags()`.
- **Tag view mode**: the TUI has two view modes (`viewSchedule` / `viewTag`), toggled with `v`. In tag view, tabs become tags (active first, alphabetical middle, untagged last), each showing tasks grouped by schedule bucket with separator items. Grab mode in tag view restricts movement to within a single tag tab (no left/right). Launch directly into tag view with `monolog --tags` / `-T`.
- **CompletedAt lifecycle**: `Task.CompletedAt` (RFC3339, omitempty) is set when `doneSelected()` fires. It is never cleared (tasks cannot be reopened today). Existing tasks without `CompletedAt` are excluded from avg-time-to-complete calculations. Used by `model.ComputeStats()`.
- **Stats bar**: the TUI always shows a one-line stats bar (total/open/done/in-tab/avg) above the tab bar. Stats are cached in `Model.stats` (a `model.Stats` value) and refreshed via `reloadAllTasks()` at the end of every `reloadAll()` call. `model.ComputeStats([]Task, time.Time)` and `model.FormatDuration(days float64)` in `internal/model/stats.go` are pure functions, easily unit-tested.

## Dependencies

Minimal: cobra (CLI framework), oklog/ulid/v2 (ID generation), charmbracelet stack (bubbletea, bubbles, lipgloss for TUI), yaml.v3 (YAML editor round-trip in TUI edit). Go stdlib for everything else.

## Development Rules

- Every change must include tests. All tests must pass before merging.
- Completed plan lives in `docs/plans/completed/`.
