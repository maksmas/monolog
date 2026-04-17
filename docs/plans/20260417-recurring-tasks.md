# Recurring Tasks

## Overview

Add recurring-task support to Monolog. Users set a recurrence rule on a task at creation or edit time; when the task is completed via `done`, a new copy is auto-spawned with a computed next schedule date. Two notes cross-link old and new (bidirectional traceability) without introducing a SeriesID concept.

Primary motivator: personal recurring chores (pay bills on the 1st, weekly check-ins, daily habits).

Grammar covers both calendar-anchored and interval-from-completion modes:
- `monthly:N` (N=1..31, clamped to month-end for short months)
- `weekly:<day>` where `<day>` accepts: three-letter `mon|tue|wed|thu|fri|sat|sun`, full names `monday|tuesday|...|sunday`, or numeric `1..7` (Mon=1, Sun=7). All forms case-insensitive.
- `workdays` (Mon-Fri)
- `days:N` (every N days from completion)

Integrates cleanly with the existing one-JSON-file-per-task storage and the auto-commit-on-mutation pattern. No new storage concept, no daemon, no cron — spawn only happens when the user completes the current occurrence.

## Context (from discovery)

- Project: Go CLI personal backlog tool; tasks are JSON files under `.monolog/tasks/`. Every mutation auto-commits via `internal/git.AutoCommit(repoPath, message, files...)` — variadic, already supports multi-file commits.
- `internal/model/task.go` defines the `Task` struct with `omitempty` JSON tags. `model.AppendNote(body, text, now)` and `model.CountNotes(body)` already exist in `internal/model/note.go`. `NoteCount` is recalculated inside `Store.Update`, so any body mutation keeps the count consistent automatically.
- `internal/schedule/` has `Parse`, `IsoLayout = "2006-01-02"`, and `Bucket` — reusable for formatting the next-occurrence date on the spawn.
- `cmd/done.go` currently sets `Status = "done"`, calls `SetActive(false)`, updates timestamp, calls `Store.Update`, then commits one file. Spawn logic will graft in after `Store.Update` and before commit, and the commit will bundle both files.
- `cmd/add.go` uses `cobra.Flags().StringVarP` for `--schedule`, `--tags`. Adding `--recur` follows the same pattern.
- `cmd/edit.go` checks `cmd.Flags().Changed("...")` per field. The same pattern applies for `--recur`, with the extra rule that an explicitly-set empty string clears the recurrence.
- `internal/tui/model.go` has `addFocus addField` enum with `addFocusTitle`, `addFocusTags`. Add-modal extension requires a new enum value `addFocusRecur` plus a new `textinput.Model` field. The TUI edit path is YAML round-trip (yaml.v3) — once `Recurrence` is on the Task struct, it appears in the YAML editor with zero extra TUI work.
- No existing references to "recur" or "Recurrence" anywhere in the codebase — clean slate.

## Development Approach

- **Testing approach**: regular (code + tests together, per task)
- Complete each task fully before moving to the next
- Make small, focused changes
- **Every task MUST include new/updated tests** for code changes in that task
  - Tests are a required deliverable, not optional
  - Cover happy path, error cases, and boundary cases
- **All tests must pass before starting the next task** — no exceptions
- Run `go test ./...` and `go vet ./...` after each change
- Update this plan file if scope changes during implementation

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach)
- **E2E tests**: project has no Playwright/Cypress layer. CLI integration is covered by existing `cmd/*_test.go` tests that exercise cobra commands end-to-end against a temp repo. TUI tests in `internal/tui/model_test.go` exercise the Bubble Tea model via message dispatch. New TUI recurrence input follows existing TUI test patterns.
- Commands:
  - `go test ./...` — full suite
  - `go test ./internal/recurrence/` — new package in isolation
  - `go vet ./...` — lint

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## Solution Overview

**Data model**: single new field `Task.Recurrence string` (JSON `recurrence,omitempty`). Empty = not recurring.

**Parsing & math**: new package `internal/recurrence/` with pure `Parse(string) (Rule, error)` and `Rule.Next(completedAt time.Time) time.Time`. Zero external deps (stdlib `time` only). Month-end clamp lives here.

**Spawn trigger**: only `cmd/done.go`. When a completed task's `Recurrence` is non-empty and parses, build a new task with fresh ULID, copy Title/Body/Tags (minus `active`)/Recurrence, set `Schedule = Rule.Next(now).Format(schedule.IsoLayout)`, place at end of target bucket, write, then append cross-reference notes on both tasks and commit both files in one git commit.

**Failure mode**: if stored recurrence fails to parse (hand-edited JSON, future schema change), warn to stderr, skip spawn, keep the completion itself successful. Completion is the user's immediate intent; a broken spawn must not eat it.

**Stop semantics**: edit-based only. Clearing `Recurrence` (CLI `--recur ""` or TUI YAML edit) before completing prevents the next spawn. `rm` also stops the chain.

**CLI surface**: `--recur` flag on `add` and `edit`. Validated via `recurrence.Parse` at command-invocation time (fail fast on typos, not at `done` time two weeks later).

**TUI surface**: one new input in the add modal; edit already flows through YAML so nothing extra.

## Technical Details

### `Task` struct addition

```go
type Task struct {
    // ... existing fields ...
    Recurrence string `json:"recurrence,omitempty"`
}
```

Field placement: after `Tags`, before `NoteCount`. `omitempty` keeps existing task JSON files unchanged on disk until the user sets a rule.

### `internal/recurrence/` API

```go
// Rule is an opaque parsed recurrence spec.
type Rule interface {
    Next(completedAt time.Time) time.Time
    String() string // round-trips to the original grammar, for debugging
}

// Parse validates the grammar and returns a Rule. Empty string returns
// (nil, nil) — callers treat nil Rule as "not recurring".
func Parse(s string) (Rule, error)
```

Internally: four private struct types implementing `Rule`:
- `monthlyRule{day int}` — clamps to `daysInMonth(year, month)` when `day > days-in-target-month`
- `weeklyRule{weekday time.Weekday}`
- `workdaysRule{}`
- `daysRule{n int}`

All `Next()` methods return the first matching date **strictly after** `completedAt` (date-only comparison — the time-of-day component is ignored; we round `completedAt` to midnight UTC to match `schedule.Parse` conventions).

### Grammar rules

- `monthly:N` — N must be 1..31; reject `monthly:0`, `monthly:32`, non-numeric.
- `weekly:<day>` — `<day>` parses as **case-insensitive** one of:
  - three-letter: `mon|tue|wed|thu|fri|sat|sun`
  - full name: `monday|tuesday|wednesday|thursday|friday|saturday|sunday`
  - numeric: `1|2|3|4|5|6|7` (Monday=1 ... Sunday=7, matching ISO-8601 day-of-week numbering)

  Internally normalized to `time.Weekday` before storage on the Rule; `Rule.String()` emits the canonical three-letter lowercase form (`weekly:mon`) regardless of the input alias, so round-trips through edit always land on the canonical spelling.
- `workdays` — exact literal, no colon. Case-insensitive (`WorkDays`, `WORKDAYS` all accepted). `Rule.String()` emits `workdays`.
- `days:N` — N must be >= 1; reject `days:0`, negative, non-numeric.
- Anything else: return error.

### Spawn flow pseudocode

Shipped implementation lives in `internal/recurrence/spawn.go` as
`recurrence.CompleteAndSpawn`, reused by both `cmd/done.go` and the TUI
`doneSelected()` path so there is a single source of truth. Sketch:

```
// mutate the old task in-memory to its done state first
task.Status = "done"
task.SetActive(false)
task.UpdatedAt = now
if task.CompletedAt == "" { task.CompletedAt = now }

// optional spawn — never blocks completion; warnings route through the
// warn io.Writer the caller passed in
if task.Recurrence != "" {
    rule, err := recurrence.Parse(task.Recurrence)
    if err != nil {
        warn("recurrence %q invalid: %v; skipping spawn", task.Recurrence, err)
    } else {
        res, err := spawn(store, *task, rule, now)   // writes the new file
        if err != nil {
            warn("recurrence %q spawn failed: %v; skipping spawn", task.Recurrence, err)
        } else {
            task.Body = AppendNote(task.Body,
                "Spawned follow-up: <new-id> (scheduled <date>)", now)
            commitFiles = append(commitFiles, newFile)
            commitMsg = "done: <title> (recurring, next <date>)"
            spawnedID = res.newID
        }
    }
}

// single Update combines the done transition AND the back-ref note.
// NoteCount is recalculated inside Store.Update.
if err := store.Update(*task); err != nil {
    // rollback: spawn file is on disk but nothing was committed yet
    if spawnedID != "" { store.Delete(spawnedID) }
    return err
}

git.AutoCommit(repoPath, commitMsg, commitFiles...)   // 1 or 2 files
```

Key invariants:
- Exactly one `Store.Update` on the old task (iteration 1 consolidated
  the earlier two-phase flow), so `NoteCount` stays consistent through a
  single write.
- On post-spawn Update failure, the newly written spawn file is deleted
  best-effort so the repo is never left with an orphan that never made
  it into a commit (iteration 2 fix).
- Recurrence anchoring uses `midnightLocal` (completedAt's own zone) to
  avoid an off-by-one for users west of UTC completing tasks late in
  the evening (iteration 2 fix).

### Git commit message

Spawn case: `"done: <title> (recurring, next <YYYY-MM-DD>)"`.
Non-spawn case: unchanged — `"done: <title>"`.

### CLI flag validation

- `add --recur <rule>`: if non-empty, call `recurrence.Parse` before `s.Create`; on error, return the error to cobra. Empty `--recur` is allowed and means non-recurring.
- `edit --recur <rule>`: if `cmd.Flags().Changed("recur")`, set the field to the provided value; if the value is non-empty, validate via `Parse`. Empty value explicitly clears.

### TUI changes

- `addField` enum: add `addFocusRecur` after `addFocusTags`.
- `Model`: add `recurInput textinput.Model`.
- Initialize in `newModel`, reset in `openAddModal`, wire into focus cycling (Tab key moves title → tags → recur → title).
- Render: add a third input line in the add modal.
- Submit: validate via `recurrence.Parse`; show form error on invalid input.
- Edit modal: no code changes — the YAML round-trip already serializes the new `Recurrence` field.

### What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, test additions, doc updates
- **Post-Completion** (no checkboxes): nothing external — fully self-contained

## Implementation Steps

### Task 1: Add `internal/recurrence/` package (pure logic, no Task dep)

**Files:**
- Create: `internal/recurrence/recurrence.go`
- Create: `internal/recurrence/recurrence_test.go`

- [x] create `internal/recurrence/recurrence.go` with `Rule` interface, `Parse(string)`, and four concrete private types (`monthlyRule`, `weeklyRule`, `workdaysRule`, `daysRule`)
- [x] implement `monthlyRule.Next` with month-end clamping via `time.Date` normalization (e.g., `Date(y, m+1, day, ...)` — if day overflows, recompute to last day of target month)
- [x] implement `weeklyRule.Next`, `workdaysRule.Next`, `daysRule.Next` — all returning first matching date strictly after `completedAt` date
- [x] in `Parse`: normalize the `weekly:` argument by lowercasing and matching against three-letter, full-name, and numeric (1-7) alias tables; normalize `workdays` by lowercasing before the exact-literal compare
- [x] implement `Rule.String()` on each type returning the original grammar form (round-trip fidelity)
- [x] write tests: `TestParse_Valid` table-driven covering every valid form
- [x] write tests: `TestParse_Invalid` table-driven covering empty string returns (nil, nil), `monthly:0`, `monthly:32`, `days:0`, `days:-1`, `weekly:xyz`, unknown rules, missing colons, extra colons
- [x] write tests: `TestMonthlyNext` covering normal case, Feb 28 clamp (non-leap year, `monthly:31` completed Jan 15 → Feb 28; completed Feb 15 → Feb 28 then next call Mar 31), Feb 29 leap-year case (2028), completion on target date → next month
- [x] write tests: `TestWeeklyNext` — same weekday completion → next week; mid-week completion → upcoming target
- [x] write tests: `TestWorkdaysNext` — Friday completion → Monday; Saturday/Sunday completion → Monday; Mon-Thu → next day
- [x] write tests: `TestParse_WeeklyAliases` — table-driven covering `mon`, `MON`, `Mon`, `monday`, `MONDAY`, `Monday`, `1` all parse to the same Rule (Monday); same for each other weekday; all return `weekly:<3-letter-lowercase>` via `Rule.String()`
- [x] write tests: `TestParse_WorkdaysAliases` — `workdays`, `Workdays`, `WORKDAYS` all parse; `Rule.String()` returns `workdays`
- [x] write tests: `TestParse_WeeklyInvalidAliases` — `weekly:0`, `weekly:8`, `weekly:xyz`, `weekly:monda`, `weekly:` rejected
- [x] write tests: `TestDaysNext` — simple N-day add
- [x] run `go test ./internal/recurrence/` — all tests must pass
- [x] run `go vet ./...` — must pass before Task 2

### Task 2: Add `Recurrence` field to `model.Task`

**Files:**
- Modify: `internal/model/task.go`
- Modify: `internal/model/task_test.go` (if exists; otherwise extend closest existing test file for model)

- [x] add `Recurrence string \`json:"recurrence,omitempty"\`` to `Task` struct between `Tags` and `NoteCount`
- [x] write test confirming an existing task JSON without `recurrence` still round-trips (decode → encode) without introducing the key (omitempty semantics)
- [x] write test confirming a task with non-empty `Recurrence` serializes and deserializes correctly
- [x] run `go test ./internal/model/...` — all tests must pass
- [x] run `go test ./...` — full suite must still pass (backward compatibility)

### Task 3: Wire spawn logic into `cmd/done.go`

**Files:**
- Modify: `cmd/done.go`
- Create: `cmd/done_recurring_test.go`
- Create (during review iteration 1): `internal/recurrence/spawn.go` — the
  spawn-and-complete flow was extracted out of `cmd/done.go` into the
  `recurrence` package so the TUI could share it. Unexported `spawn`
  helper plus public `CompleteAndSpawn` entrypoint.
- Create (during review iteration 1): `internal/recurrence/spawn_test.go`
  — direct tests for the `tagsWithoutActive` helper.

- [x] add spawn branch in `cmd/done.go` after the existing `s.Update(task)` call, gated on non-empty `Recurrence`
- [x] on `recurrence.Parse` error: write warning to `cmd.ErrOrStderr()`, skip spawn, keep the done result; commit only the old file
- [x] on parse success: build new `model.Task` (fresh ULID via `model.NewID`, copy Title/Body/Source/Recurrence, tags minus `active` via a small helper, Schedule from `rule.Next(now).Format(schedule.IsoLayout)`, Position from `ordering.NextPosition` over fresh `s.List()`, CreatedAt/UpdatedAt = now, Status = "open")
- [x] append `"Spawned from <old-ULID>"` note to new task body via `model.AppendNote` before `s.Create`
- [x] after `s.Create(newTask)`: append `"Spawned follow-up: <new-ULID> (scheduled YYYY-MM-DD)"` note to old task body, call `s.Update(task)` again
- [x] commit message: `"done: <title> (recurring, next YYYY-MM-DD)"` for spawn case; unchanged otherwise
- [x] pass both file paths to `git.AutoCommit` when spawn succeeds
- [x] extract `tagsWithoutActive(tags []string) []string` helper (either inline in `cmd/done.go` or in `internal/model/` if it'd be reused elsewhere — default to inline since nothing else needs it)
- [x] write test: spawn happy path — create a task with `Recurrence: "monthly:1"`, mark done, assert new task file exists, has expected Schedule, Body contains "Spawned from", Tags don't include `active`, has fresh ULID, same Recurrence
- [x] write test: non-recurring — no new file, behavior unchanged
- [x] write test: invalid recurrence (hand-craft a task JSON with `"recurrence":"bogus"`) — task still marked done, stderr contains warning, no new file created
- [x] write test: bidirectional notes — old task body ends with "Spawned follow-up: \<new-ULID\> (scheduled ...)", new task body ends with "Spawned from \<old-ULID\>"
- [x] write test: `active` tag is stripped on the spawned task even when the original had it (note: the original's active is already cleared by existing SetActive(false) path)
- [x] write test: single git commit contains both files (assert via `git log --name-only` on the test repo, or by checking the commit SHA diff)
- [x] write test: `NoteCount` on both old and new task reflects appended notes after Store.Update recalculation
- [x] run `go test ./cmd/...` — all tests must pass
- [x] run `go test ./...` — full suite must pass before Task 4

### Task 4: Add `--recur` flag to `cmd/add.go`

**Files:**
- Modify: `cmd/add.go`
- Modify: `cmd/add_test.go`

- [x] add `var recur string` and `cmd.Flags().StringVar(&recur, "recur", "", "Recurrence rule: monthly:N, weekly:<day>, workdays, days:N")`
- [x] on non-empty `recur`: call `recurrence.Parse(recur)` before building the task; return error on invalid grammar
- [x] assign `task.Recurrence = recur` in the task-build block
- [x] write test: `monolog add --recur monthly:1 "pay bills"` creates task with Recurrence = "monthly:1"
- [x] write test: each valid grammar form accepted (table-driven)
- [x] write test: invalid grammar rejected with non-zero exit and useful error message (table-driven)
- [x] write test: empty `--recur` (omitted) creates task with empty Recurrence (unchanged behavior)
- [x] run `go test ./cmd/...` — all tests must pass

### Task 5: Add `--recur` flag to `cmd/edit.go`

**Files:**
- Modify: `cmd/edit.go`
- Modify: `cmd/edit_test.go`

- [x] add `--recur` flag; include `"recur"` in the no-op-check list alongside title/body/schedule/tags/active
- [x] on `cmd.Flags().Changed("recur")`: if value non-empty, validate via `recurrence.Parse`; set `task.Recurrence = value` (empty explicitly clears)
- [x] write test: `edit --recur weekly:mon` sets the field
- [x] write test: `edit --recur ""` clears a previously-set rule
- [x] write test: `edit --recur bogus` returns error and does not mutate the task
- [x] write test: `edit` without `--recur` preserves existing rule (unchanged behavior)
- [x] run `go test ./cmd/...` — all tests must pass

### Task 6: Extend TUI add modal with Recurrence input

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] add `addFocusRecur addField` to the enum after `addFocusTags`
- [x] add `recurInput textinput.Model` field to `Model`
- [x] initialize `recurInput` in `newModel` (placeholder "e.g. monthly:1", reasonable width)
- [x] reset `recurInput` value and placeholder in the add-modal open path
- [x] update Tab/Shift+Tab focus cycling to include recur between tags and title (wrap-around)
- [x] update the add-modal render to show the recur input as a third line with label "Recurrence:"
- [x] on submit: validate recur value via `recurrence.Parse` if non-empty; on error, show form error and keep modal open
- [x] persist `task.Recurrence = m.recurInput.Value()` when building the new task for `Store.Create`
- [x] write test: opening add modal → typing title → Tab → Tab → typing "monthly:1" → Enter creates a task with correct Recurrence
- [x] write test: invalid recur value on submit shows error and does not create a task
- [x] write test: focus cycling order (title → tags → recur → title) via Tab key
- [x] run `go test ./internal/tui/...` — all tests must pass
- [x] run `go vet ./...` — must pass before Task 7

### Task 7: CLI `show` command — display Recurrence

**Files:**
- Modify: `cmd/show.go`
- Modify: `cmd/show_test.go`

- [x] add a "Recurrence:" line to the `show <id>` output when `task.Recurrence` is non-empty (placed between Schedule and Tags)
- [x] write test confirming the line appears when set, is omitted when empty
- [x] run `go test ./cmd/...` — all tests must pass

### Task 8: Verify acceptance criteria

- [x] manual smoke test: in a throwaway `MONOLOG_DIR`, run `monolog add --recur monthly:1 "pay rent"`, `monolog done <id>`, verify a new task is created with next month's first as schedule, body has "Spawned from", old task's body has "Spawned follow-up", and `git log -1 --name-only` shows both files in one commit (automated as `TestAcceptance_AddRecurDoneSpawnsAndSingleCommit` in `cmd/recurring_acceptance_test.go`)
- [x] manual smoke: edit to clear (`monolog edit <id> --recur ""`), mark done, confirm no new spawn (automated as `TestAcceptance_EditClearRecurPreventsSpawn`)
- [x] manual smoke: TUI — open add modal, tab to Recurrence, type `workdays`, create task; confirm it appears in list and completing it spawns the next weekday (spawn-and-ls portion automated as `TestAcceptance_WorkdaysSpawnsNextWeekday`; TUI keystroke/input plumbing covered by `TestAdd_TabTabSetsRecurrenceOnCreatedTask`, `TestAdd_FocusCyclingTitleTagsRecurTitle`, and siblings in `internal/tui/model_test.go`)
- [x] manual smoke: CLI alias — `monolog add --recur weekly:Monday "..."` and `monolog add --recur weekly:1 "..."` both succeed and store as `weekly:mon` (verify via `monolog show <id>`) (automated as `TestAcceptance_WeeklyAliasesCanonicalize` with subtests `full_name`, `numeric`, `upper_short`)
- [x] run `go test ./...` — full suite must pass
- [x] run `go vet ./...` — clean
- [x] verify no existing test was skipped or weakened to accommodate changes (reviewed `git diff main -- '**/*_test.go'`: only additions and one rename+retarget of `TestAdd_SuggestionsClearedOnTabToTitle` → `TestAdd_SuggestionsClearedOnTabAwayFromTags` that asserts the same invariants against the new focus target `addFocusRecur`; no deletions of assertions)

### Task 9: Update CLAUDE.md and finalize plan

**Files:**
- Modify: `CLAUDE.md`
- Move: `docs/plans/20260417-recurring-tasks.md` → `docs/plans/completed/20260417-recurring-tasks.md`

- [x] add a bullet under "Key Design Decisions" in CLAUDE.md summarizing: grammar (four forms: `monthly:N`, `weekly:<day>` with three-letter/full-name/numeric aliases case-insensitive, `workdays`, `days:N`), canonical round-trip via `Rule.String()`, spawn-on-done semantics, bidirectional note cross-refs, edit-to-stop, month-end clamp, no SeriesID (Model A), no auto-spawn when recurrence is invalid (warn-and-skip)
- [x] ensure all checkboxes in this plan are marked `[x]`
- [x] deferred to finalize step (review phases need plan path intact) — `mkdir -p docs/plans/completed` (already exists) and `git mv docs/plans/20260417-recurring-tasks.md docs/plans/completed/`

## Post-Completion

Nothing external. No consuming projects, no deployment configs, no third-party services. The feature is self-contained within the monolog binary and its git-backed task repo.

**Follow-up ideas (not in this plan — future work):**
- Done-tab filtering to hide old recurring-series occurrences (user explicitly deferred this decision)
- `done --no-recur` one-shot flag for "complete this last occurrence without spawning"
- Nth-weekday-of-month grammar (e.g., `monthly:2nd-tue`) if a real need surfaces
- Two-device sync dedup (scan for duplicate "Spawned from \<same-ULID\>" notes) — extremely unlikely for a single-user tool
- `monolog ls --recur` filter to list all tasks with active recurrence rules
