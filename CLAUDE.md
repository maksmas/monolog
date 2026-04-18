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
internal/recurrence/    → Recurrence-rule parsing, next-occurrence math, shared done+spawn flow
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
- **Tag autocomplete**: both the add-task and retag modals show inline autocomplete suggestions as the user types in the tag field. `model.FilterTags(known, field)` is a pure function that extracts the fragment after the last comma, filters known tags by case-insensitive prefix, excludes already-entered tags, and caps at 5 results. Suggestions are navigated with Up/Down, accepted with Tab/Enter (replaces fragment and appends `", "`), and dismissed with Esc. State lives in `Model.suggestions`/`Model.suggestionIdx`, shared between modals since only one is open at a time.
- **Task notes**: notes are stored as timestamped entries appended to the existing `Body` field using `--- YYYY-MM-DD HH:MM:SS ---` separators — no schema migration needed. A `NoteCount int` field (JSON `note_count`, omitempty) tracks the count for fast badge rendering (`[N]` in the task list) without body parsing. `model.AppendNote()` formats and appends note text. `NoteCount` is recalculated from `Body` inside both `Store.Create` and `Store.Update` via `model.CountNotes()` — the store layer is the single source of truth, so any path that writes a task (new or existing, including recurring-task spawns whose body already carries the `Spawned from` note) keeps the count consistent without caller bookkeeping. CLI: `monolog note <id> "text"` adds a note, `monolog show <id>` prints full task detail.
- **Detail panel**: pressing `Enter` in the TUI toggles a right-side panel showing task metadata (title, schedule, tags, dates) and the full body with notes. An inline textarea at the bottom accepts new notes (`Enter` submits, `Alt+Enter` inserts a newline, `Esc` closes the panel). Navigating tasks with `Up/Down` while the panel is open refreshes its content. Panel width is ~45% of the terminal.
- **Fuzzy search**: `/` in TUI normal mode opens a full-screen search overlay that fuzzy-matches across both title and body of all tasks (open and done), powered by `github.com/sahilm/fuzzy`. Title matches are weighted 2x over body matches. Enter commits to the selected task, switching `activeTab` to the target bucket/tag and focusing the task; Esc cancels with no side effects. Pure ranker lives in `internal/tui/search_match.go`; overlay UI in `internal/tui/search.go`.
- **Recurring tasks**: `Task.Recurrence string` (JSON `recurrence,omitempty`) holds a rule in one of four forms — `monthly:N` (1..31, clamped to month-end for short months), `weekly:<day>` (accepts three-letter `mon|tue|...|sun`, full names `monday|...|sunday`, or numeric `1..7` with Mon=1, all case-insensitive), `workdays` (Mon-Fri), or `days:N` (N days after completion). Parsing and next-date math live in `internal/recurrence/` (`Parse(string) (Rule, error)`, `Canonicalize(string) (string, error)`, `Rule.Next(time.Time) time.Time`, `Rule.String()`); `Rule.String()` emits the canonical form (`weekly:mon`, `workdays`) so alias inputs round-trip through edit to canonical storage. All three callers (`cmd/add`, `cmd/edit`, and the TUI add modal / YAML editor) validate-and-normalize through `recurrence.Canonicalize`. Spawn is driven by `recurrence.CompleteAndSpawn(s *store.Store, task *model.Task, now time.Time, w io.Writer, dateFormat string)` (shared by `cmd/done.go` and the TUI `d` key), where `w` receives any spawn-warning messages and `dateFormat` is the configured user-facing layout passed in from `config.DateFormat()`. When a completed task has a non-empty `Recurrence`, a new task is created with a fresh ULID, copied Title/Body/Source/Recurrence, tags minus `active`, `Schedule = Rule.Next(now)` (stored ISO); the old task receives the done-state transition AND the back-reference note in a single `Store.Update`, and both files are committed in one git commit whose message formats the next date with `dateFormat` (e.g. `"done: <title> (recurring, next 25-04-2026)"` under the default). Bidirectional cross-references: the new task gets a `"Spawned from <old-ULID>"` note, the old task gets a `"Spawned follow-up: <new-ULID> (scheduled <date>)"` note formatted in `dateFormat`. Stop semantics are edit-based — clearing `Recurrence` (CLI `--recur ""` or removing the `recurrence:` line in the TUI YAML editor, which now exposes it through `editableFields`) before completing prevents the next spawn; there is no SeriesID or series concept. Any spawn failure (invalid stored rule, list/create error) is warned to the caller's stderr and skipped while the underlying completion still succeeds — the repo is never left with an uncommitted half-spawn. CLI surface: `--recur` flag on `add` and `edit`; TUI surface: a third input `addFocusRecur` in the add modal and a `recurrence` line in the YAML editor.

## Dependencies

Minimal: cobra (CLI framework), oklog/ulid/v2 (ID generation), charmbracelet stack (bubbletea, bubbles, lipgloss for TUI), sahilm/fuzzy (TUI fuzzy search ranker), yaml.v3 (YAML editor round-trip in TUI edit). Go stdlib for everything else.

## Development Rules

- Every change must include tests. All tests must pass before merging.
- Completed plan lives in `docs/plans/completed/`.
