# Compact date display in `ls` and TUI

## Overview

Show compact dates for each task in `monolog ls` and in the interactive TUI so the user can see at-a-glance how old a task is and (for completed tasks) when it was finished.

- **Open tasks**: display `CreatedAt` only.
- **Done tasks**: display both `CreatedAt` and `UpdatedAt` (UpdatedAt = completion time for done tasks).
- **Format**: relative (`now`, `3h`, `2d`) for anything under 7 days; `MM-DD` for older.
- **ls placement**: new column just before the `tags` column.
- **TUI placement**: appended to the existing `item.Description()` line (which already shows shortID, schedule, tags).

No schema changes — `Task.CreatedAt` and `Task.UpdatedAt` are already stored as RFC3339 UTC strings.

## Context (from discovery)

- **Task model** (`internal/model/task.go`): `CreatedAt`, `UpdatedAt` are `string` (RFC3339, written with `time.Now().UTC().Format(time.RFC3339)` — see `cmd/add.go:48`, `cmd/done.go:38`, `cmd/edit.go:63`, `cmd/mv.go`, etc.).
- **ls rendering** (`internal/display/table.go`): single function `FormatTasks`; fixed-width `fmt.Fprintf` line with marker, shortID, title, schedule, tags. One test file already covers it (`table_test.go`).
- **TUI rendering** (`internal/tui/model.go:85-103`): `item.Description()` joins shortID, schedule (non-today), tags with `"  "`. Done tab already sorts by `UpdatedAt` descending (`model.go:157-160`).
- **CLAUDE.md** — "every change must include tests; all tests must pass before merging".

## Development Approach

- **testing approach**: TDD (tests first) — write tests for `FormatRelDate` / display changes, then implement
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - write unit tests for new functions/methods
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change

## Testing Strategy

- **unit tests**: required for every task.
  - `FormatRelDate(now, ts string) string` — table-driven covering: empty input, malformed input, `now` (<1 min), minutes, hours, days <7, exactly 7 days, older (MM-DD), far-future / clock-skew case.
  - `display.FormatTasks` — assert output contains the expected compact date fields, both for an open task and a done task (with both dates). Use a fixed "now" clock injected into the formatter.
  - `internal/tui` item rendering — assert `Description()` contains the expected compact date string(s) for open vs done tasks.
- No e2e/UI automation in this project; the TUI has unit tests on `Model` behavior and item rendering is plain string assembly.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

1. Add a single pure helper `FormatRelDate(now time.Time, ts string) string` in `internal/display/`. Keep it isolated and easy to test; inject `now` so tests are deterministic.
2. Modify `display.FormatTasks` to render the new column before `tags`:
   - Open task: one date (`created`).
   - Done task: two dates joined with `→` (`created → done`), e.g. `2d→now`.
   - Empty / unparseable timestamp → empty string (graceful).
3. Modify `internal/tui/model.go`'s `item.Description()` to append the same compact date(s) at the end of the existing `shortID | schedule | tags | dates` description line, using the same helper.
4. All callers pass `time.Now()` at render time (no stored state).

## Technical Details

**Helper signature (in `internal/display/dates.go`):**
```go
// FormatRelDate returns a compact representation of ts relative to now.
// <1 minute  -> "now"
//  <1 hour   -> "Nm"        (e.g. "5m")
//  <1 day    -> "Nh"        (e.g. "3h", 23h59m -> "23h")
//  <7 days   -> "Nd"        (e.g. "2d", 6d23h -> "6d")
//  >=7 days  -> "MM-DD"     (same year) or "YY-MM-DD" (different year)
// Empty or unparseable ts -> "".
// Future timestamps within 1 minute tolerance -> "now"; further future -> "MM-DD".
func FormatRelDate(now time.Time, ts string) string
```

Parsing: `time.Parse(time.RFC3339, ts)`; on error return `""`.
Boundaries (inclusive lower, exclusive upper):
- `[0, 1m)` → `"now"`
- `[1m, 1h)` → `"Nm"` (integer minutes, floored)
- `[1h, 24h)` → `"Nh"` (integer hours, floored)
- `[24h, 7*24h)` → `"Nd"` (integer days, floored)
- `[7*24h, ∞)` → date format

**Task-dates helper** (internal to `display`):
```go
// FormatTaskDates returns the compact date column value for a task:
//   open: "<created>"
//   done: "<created>→<updated>"
//   both empty -> ""
func FormatTaskDates(now time.Time, t model.Task) string
```

**Testability — `now` is a parameter all the way down.**
- `display.FormatTasks` gains a `now time.Time` parameter: `FormatTasks(w io.Writer, tasks []model.Task, now time.Time)`. Two call sites update: `cmd/ls.go:57` and `cmd/log.go:50` pass `time.Now()`.
- The TUI `item` struct gains a `now time.Time` field that is set by the code that builds items (in `setTabTasks`, which wraps `[]model.Task` into `[]list.Item`). `item.Description()` passes `i.now` to `display.FormatTaskDates` — no call to `time.Now()` inside `Description`. Tests construct `item{task: ..., now: fixedNow}` directly.
- Rationale: avoids a package-level clock var, keeps `now` explicit at every boundary, matches the rest of the codebase's plain-function style.

**ls row** (new column before tags):
```
1    01J5K…   buy milk         today    2d      [shopping]
x    01J5K…   write report     today    5d→1h   [work]
```
Width: 8 chars (fits `MM-DD` = 5 and `Nd→MM-DD` = 8). Format string in `table.go` becomes:
```go
"%-4s %-8s  %-40s %-10s %-8s %s\n"
```

**TUI description line** (append to existing):
```
01J5K…  tomorrow  [shopping]  2d
01J5K…            [work]      5d→1h
```
Simple append with the same `"  "` separator.

## What Goes Where

- **Implementation Steps** (checkboxes): `internal/display/` helper + tests, update `table.go` + tests, update `internal/tui/model.go` item description + tests.
- **Post-Completion**: manual check in a scratch repo that dates render as expected across tabs.

## Implementation Steps

### Task 1: Add `FormatRelDate` helper

**Files:**
- Create: `internal/display/dates.go`
- Create: `internal/display/dates_test.go`

- [ ] write table-driven tests in `dates_test.go` covering all boundaries:
  - empty string → `""`
  - malformed string → `""`
  - `0s`, `30s`, `59s` → `"now"`
  - `1m`, `5m`, `59m` → `"1m"`, `"5m"`, `"59m"`
  - `1h`, `3h`, `23h59m` → `"1h"`, `"3h"`, `"23h"`
  - `24h` → `"1d"` (boundary: hours→days)
  - `2d`, `6d`, `6d23h59m` → `"2d"`, `"6d"`, `"6d"`
  - `7*24h` → `"MM-DD"` (boundary: days→date)
  - older same year → `"MM-DD"` e.g. `"03-28"`
  - older different year → `"YY-MM-DD"` e.g. `"25-03-28"`
  - future ≤1 min → `"now"`
  - future >1 min → `"MM-DD"`
- [ ] implement `FormatRelDate(now time.Time, ts string) string` in `dates.go`
- [ ] run `go test ./internal/display/` — must pass before next task

### Task 2: Add `FormatTaskDates` helper and render in ls

**Files:**
- Modify: `internal/display/dates.go` (add `FormatTaskDates`)
- Modify: `internal/display/dates_test.go` (tests for `FormatTaskDates`)
- Modify: `internal/display/table.go` (new column before tags, `now` parameter)
- Modify: `internal/display/table_test.go` (pass fixed `now`, assert new column content)
- Modify: `cmd/ls.go` (pass `time.Now()` to `FormatTasks`)
- Modify: `cmd/log.go` (pass `time.Now()` to `FormatTasks`)

- [ ] write tests for `FormatTaskDates` with a fixed `now`: open task → single `"<created>"`, done task → `"<created>→<updated>"`, task with both timestamps empty → `""`, task with only CreatedAt empty and done → `"→<updated>"` (graceful)
- [ ] implement `FormatTaskDates(now time.Time, t model.Task) string`
- [ ] change signature: `FormatTasks(w io.Writer, tasks []model.Task, now time.Time)`; update format string to include the 8-char dates column immediately before tags; call `FormatTaskDates(now, task)` per row
- [ ] update `cmd/ls.go` and `cmd/log.go` call sites to pass `time.Now()`
- [ ] update existing `FormatTasks` tests: pass a fixed `now`, set `CreatedAt`/`UpdatedAt` on fixture tasks relative to that `now` so expected column values are deterministic; add two new cases (open with recent CreatedAt → e.g. `"2d"`, done with both timestamps → e.g. `"5d→1h"`)
- [ ] run `go test ./internal/display/ ./cmd/` — must pass before next task

### Task 3: Show compact dates in TUI item description

**Files:**
- Modify: `internal/tui/model.go` (`item` struct + `Description`, `setTabTasks` / wherever items are built)
- Modify: `internal/tui/model_test.go` (new test for item rendering with fixed `now`)

- [ ] add `now time.Time` field to the `item` struct (alongside `task`)
- [ ] update every place that constructs an `item{...}` to set `now: time.Now()` (single call site to check: the `tasks → []list.Item` conversion around `model.go:163`)
- [ ] update `item.Description()` to append `display.FormatTaskDates(i.now, i.task)` when non-empty, using the existing `"  "` separator — no `time.Now()` call inside `Description`
- [ ] preserve existing description ordering (shortID, schedule, tags, then dates)
- [ ] write a test: construct `item{task: Task{CreatedAt: <2d before fixed now>, Status: "open"}, now: fixedNow}`, call `Description()`, assert suffix `"  2d"`; and a done variant asserting `"  <created>→<updated>"`
- [ ] run `go test ./internal/tui/` — must pass before next task

### Task 4: Verify acceptance criteria

- [ ] run `go vet ./...`
- [ ] run full suite: `go test ./...`
- [ ] build: `go build -o monolog`
- [ ] quick manual smoke in a scratch `MONOLOG_DIR`: `monolog add "x"`, `monolog ls` → compact date column renders; `monolog` (TUI) → dates render in list rows; `monolog done <id>`, `ls --done` → `created→done` format visible

### Task 5: Final — update documentation and move plan

- [ ] if README documents `ls` output format, update the example row to include the dates column (check `README.md`; only edit if it shows a sample row)
- [ ] move this plan to `docs/plans/completed/20260413-compact-dates-display.md`
- [ ] final sanity check: `go test ./...` green

## Post-Completion

**Manual verification**:
- visually confirm dates are readable at narrow terminal widths (resize to ~80 cols); note that adding the 8-char dates column pushes minimum line width from ~70 → ~80 chars. Acceptable tradeoff for this tool; revisit if users complain.
- confirm that timestamps near boundaries (e.g. 23h, 6d 23h) flip from relative to `MM-DD` at the right moment — this is covered by unit tests but one eyeball check in real output is cheap insurance

**External system updates**: none — no consumers outside this repo.
