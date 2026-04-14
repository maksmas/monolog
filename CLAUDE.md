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
internal/store/         → File-based CRUD, prefix-match ID lookup, filtering/sorting
internal/ordering/      → Fractional position math and rebalancing
internal/display/       → Terminal table formatting and compact date helpers
internal/git/           → Git operations (init, auto-commit wrapper)
```

Commands in `cmd/` handle CLI parsing and delegate to `internal/` packages. Every mutation auto-commits to git.

## Key Design Decisions

- **One JSON file per task** (`<repo>/.monolog/tasks/<ULID>.json`): eliminates merge conflicts on sync. Two devices editing different tasks rebase cleanly.
- **ULID IDs**: time-sortable, globally unique, users type partial prefixes (e.g. `01J5K`) which resolve via directory scan.
- **Fractional positions**: inserting between positions 1000 and 2000 gives 1500. Rebalance all positions in a schedule group when any gap drops below 1.0. Default spacing is 1000.
- **Schedule values**: `today` (default), `tomorrow`, `week`, `month`, `someday`, or ISO date (`2026-04-15`). `monolog bump` promotes `tomorrow` and past dates to `today`.
- **Runtime data directory**: `~/.monolog/` by default, override with `MONOLOG_DIR` env var.

## Dependencies

Minimal: cobra (CLI framework), oklog/ulid/v2 (ID generation), Go stdlib for everything else.

## Development Rules

- Every change must include tests. All tests must pass before merging.
- Completed plan lives in `docs/plans/completed/`.
