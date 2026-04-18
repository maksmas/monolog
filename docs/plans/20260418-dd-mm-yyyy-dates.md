# Switch user-facing dates to a configurable format (default DD-MM-YYYY)

## Overview

Change every date the user reads or types from `YYYY-MM-DD` (ISO) to the
configured format — defaulting to `DD-MM-YYYY` (day-month-year). This covers
CLI `--schedule` input, TUI reschedule/YAML edit input, the task-list date
column, the TUI detail panel, recurrence commit messages and cross-reference
notes, note separators inside task bodies, and all user-facing error
messages/placeholders.

A new `internal/config` package introduces a centralized `DateFormat` setting.
For now there is no settings file — the value is a package-level variable
initialised to the DD-MM-YYYY default, with accessors for the Go layout, the
user-facing label, and a regex fragment matching the format. When real
settings persistence is added later, only `internal/config` changes; no
callers need to be touched.

On-disk storage stays ISO (`2006-01-02`). Tasks continue to serialize as JSON
with `"schedule": "2026-04-15"` regardless of `DateFormat`. This keeps the
change backwards-compatible with any already-synced `.monolog/` repos and
sidesteps migration work — the change is purely on the presentation + input
parsing boundary.

Per explicit guidance: **no backward compat for note separators**. Once the
format flips, `CountNotes` only recognizes separators in the configured
format. Existing note separators written in ISO by older versions will stop
contributing to the `NoteCount` badge. This is acceptable for a personal tool
with known data — anyone affected can manually re-save or accept the stale
count. (Flagging here so the user can push back if this is not what they
meant.)

## Context (from discovery)

- **Central date type**: `internal/schedule/schedule.go` defines
  `IsoLayout = "2006-01-02"`, `Parse(input, now) (isoString, error)`, and
  `IsISODate(s)`. These are the canonical entry points used by `cmd/add.go`,
  `cmd/edit.go`, `cmd/ls.go`, `cmd/mv.go`, TUI reschedule modal, TUI YAML edit
  (`applyEditedYAML`), and `internal/recurrence/spawn.go`.
- **Display sites**:
  - `internal/display/dates.go` `shortDate()` renders `MM-DD` (same year) or
    `YY-MM-DD` (cross-year) for the task-list date column. Tests in
    `internal/display/dates_test.go` assert these exact strings.
  - `internal/tui/model.go:2503-2505` renders `Schedule: <bucket> (<iso>)` or
    `Schedule: <iso>` in the detail panel header.
  - `internal/tui/model.go:1630-1636` marshals the task into the YAML edit
    buffer with `Schedule: <iso>`.
  - `internal/tui/model.go:1230, 1243` shows `"YYYY-MM-DD"` as the reschedule
    custom-date placeholder and error message.
  - `cmd/ls.go:58` returns an error mentioning `"ISO date (YYYY-MM-DD)"`.
  - `internal/schedule/schedule.go:69` returns the same wording.
- **Recurrence spawn** (`internal/recurrence/spawn.go`): commit message
  `"done: <title> (recurring, next <date>)"` and the `Spawned follow-up`
  cross-reference note both format the next-date with
  `schedule.IsoLayout`. These two user-facing date renderings must use the
  configured format. The stored `Schedule` field on the spawned task stays
  ISO.
- **Note separators** (`internal/model/note.go`): `AppendNote` writes
  `--- 2006-01-02 15:04:05 ---`. `CountNotes` regex recognizes exactly that.
  Both functions gain a dependency on the configured format.
- **Recurrence internals**: `internal/recurrence/recurrence.go` computes
  `Rule.Next(time.Time) time.Time` and formats via `schedule.IsoLayout` only
  at the spawn call site — internal math is on `time.Time`, no string format
  inside the package.
- **Tests asserting date strings**:
  - `internal/display/dates_test.go` (expects `"04-06"`, `"03-28"`,
    `"25-03-28"`, `"27-01-15"`, `"04-13"`, `"03-01"` — all must flip)
  - `internal/schedule/schedule_test.go` (error wording, Parse round-trip)
  - `internal/model/note_test.go` (separator regex coverage — must switch
    to the new format; no legacy branch)
  - `cmd/done_recurring_test.go` and `cmd/recurring_acceptance_test.go`
    (commit message + `Spawned follow-up` note assertions must use the
    configured format; parses of `spawn.Schedule` via `"2006-01-02"` stay
    unchanged — stored format)
  - `internal/recurrence/recurrence_test.go` (formats `Rule.Next()` with
    `"2006-01-02"` for equality — this tests the `time.Time` math, not
    user-facing output, and can stay as-is)

## Development Approach

- **Testing approach**: Regular (code first, then update/extend tests in the
  same task). Many existing tests hard-code date strings in the old format
  and must be updated alongside the implementation.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in
  that task. Tests cover both success and error scenarios.
- **CRITICAL: all tests must pass before starting the next task** — no
  exceptions.
- **CRITICAL: update this plan file when scope changes during implementation**.
- Run `go test ./...` and `go vet ./...` after each task.
- On-disk format stays ISO. Do not touch `IsoLayout`, the `schedule` JSON
  field, or any test that parses `task.Schedule` from a file with the ISO
  layout.
- Callers pass `config.DateFormat()` through to helpers that take a layout
  parameter. Helpers do NOT call `config.*` themselves — that keeps `model`
  and `schedule` packages free of a config dependency and makes them easy to
  test with arbitrary layouts.

## Testing Strategy

- **Unit tests** are required in every task touching code. Cover both
  success and error scenarios for new helpers; update existing tests that
  assert specific rendered strings.
- Where helpers now take a layout parameter, tests must exercise at least
  two layouts (the DD-MM-YYYY default and one alternative such as
  `01-02-2006` for MM-DD-YYYY) to confirm the parameter is actually wired
  through rather than ignored.
- **No e2e/UI test harness exists** in this project. Manual smoke testing in
  the terminal is in Post-Completion.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

Thin, config-driven display/input layer on top of unchanged storage:

1. New package `internal/config` owns the date-format setting:
   - `DateFormat() string` — Go layout (default `"02-01-2006"`).
   - `DateFormatLabel() string` — user-facing label derived from the layout
     (default `"DD-MM-YYYY"`).
   - `DateRegex() string` — regex fragment matching the current layout
     (default `\d{2}-\d{2}-\d{4}`).
   - Internally drives label and regex from the layout via a small lookup
     over the supported Go layout tokens (`02`, `01`, `2006`, `06`, etc.) so
     adding MM-DD-YYYY or YYYY-MM-DD later is a one-line addition to a
     supported-formats table.
2. `schedule.FormatDisplay(iso, layout string) string` and
   `schedule.Parse(input string, now time.Time, layout string) (iso,
   error)` take layout as an explicit parameter. `Parse` accepts bucket
   names, input in the passed layout, and legacy ISO (silent tolerance so
   pre-existing scripts keep working).
3. `model.AppendNote(body, text, now, layout) string` and
   `model.CountNotes(body, regex) int` take the relevant parameter. The
   regex comes from `config.DateRegex()` at the call site — no legacy
   branch.
4. Every call site resolves the layout via `config.DateFormat()` (or the
   regex via `config.DateRegex()`) and passes it through. That keeps
   `model`, `schedule`, and `display` independent of `config`.
5. User-facing error wording and TUI placeholders use
   `config.DateFormatLabel()` rather than hardcoded `"YYYY-MM-DD"`.

## Technical Details

- **New package `internal/config`**:
  ```go
  package config

  // supported lists every Go layout we'll accept as the date format,
  // paired with its user-facing label and a regex fragment that matches
  // a date rendered in that layout. Adding a new format is a one-line
  // addition here. Today only DD-MM-YYYY is supported.
  var supported = map[string]struct {
      Label string
      Regex string
  }{
      "02-01-2006": {Label: "DD-MM-YYYY", Regex: `\d{2}-\d{2}-\d{4}`},
  }

  var dateFormat = "02-01-2006" // default

  func DateFormat() string        // "02-01-2006"
  func DateFormatLabel() string   // "DD-MM-YYYY"
  func DateRegex() string         // `\d{2}-\d{2}-\d{4}`
  ```
  A flat lookup is deliberate: a token-walking derivation over
  `2006`/`06`/`01`/`02` + arbitrary separators is more machinery than a
  single-supported-format default needs. When YYYY-MM-DD or MM/DD/YYYY
  are wanted later, add a row to `supported` and point the new value at
  it. Each accessor looks up `dateFormat` in `supported` and returns the
  matching field. An unknown `dateFormat` (only reachable via the
  test-only setter with a bad value) panics at init-time or returns the
  layout string itself — pick one at implementation time.
- **`schedule.FormatDisplay(iso, layout)`**: parses `iso` with `IsoLayout`;
  on success returns `t.Format(layout)`; on failure returns the input
  unchanged (covers empty, bucket names, and unparseable junk so callers
  don't have to guard).
- **`schedule.Parse(input, now, layout)`**:
  1. bucket-name switch (unchanged)
  2. try `time.Parse(layout, input)`; on success return
     `t.Format(IsoLayout)`
  3. try `time.Parse(IsoLayout, input)` (legacy, silent); on success
     return input
  4. return a sentinel error `schedule.ErrInvalid` wrapping the input
     value — callers (`cmd/add.go`, `cmd/edit.go`, `cmd/ls.go`, TUI
     reschedule) format the user-facing message themselves using
     `config.DateFormatLabel()` so `internal/schedule` does not need to
     know about `internal/config`. No `schedule.FormatLabel` helper;
     avoids duplicating the label logic in two packages.
- **Compact date output** (`internal/display/dates.go`): `shortDate(now,
  t, layout string) string`. Parse `layout` into fields, swap year-to-YY
  for the cross-year case (e.g. map `"02-01-2006"` → `"02-01-06"`), then
  `t.Format(<layout>)` or `t.Format(<shortLayout>)`. Simpler alternative:
  same approach as before but driven by `layout` (derive day/month/year
  ordering from `layout`). Implementation choice left to task 2.
- **Note separator**: `AppendNote(body, text, now, layout)` writes
  `"--- %s ---"` with `now.Format(layout + " 15:04:05")`.
  `CountNotes(body, regex)` uses `regex` (the date fragment) inside
  `^--- <regex> \d{2}:\d{2}:\d{2} ---$` — no alternation.
- **Recurrence spawn** (`internal/recurrence/spawn.go`): keep one ISO
  string for `newTask.Schedule` (computed with `schedule.IsoLayout`) and
  one display string for the commit message and `Spawned follow-up`
  note (computed with the injected layout). The spawn function signature
  gains a `dateFormat string` parameter, passed by both callers
  (`cmd/done.go` and the TUI `d` key in `internal/tui/model.go`).

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, doc strings
  inside the repo.
- **Post-Completion** (no checkboxes): manual TUI smoke test, visual check
  of a real `.monolog/` repo to confirm stored JSON stays ISO.

## Implementation Steps

### Task 1: Create internal/config package with DateFormat accessors

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [x] create `internal/config/config.go` with a package-level
      `dateFormat` variable defaulting to `"02-01-2006"`, a
      `supported` map (one entry today: `"02-01-2006"` → label
      `"DD-MM-YYYY"`, regex `\d{2}-\d{2}-\d{4}`), and three exported
      accessors that look up `dateFormat` in the map:
      `DateFormat()`, `DateFormatLabel()`, `DateRegex()`
- [x] add a non-exported setter (internal test helper, e.g.
      `setDateFormatForTest(layout string, restore func())`) so tests
      can temporarily swap the format. No public mutation API yet
- [x] write tests for default accessors: `DateFormat() == "02-01-2006"`,
      `DateFormatLabel() == "DD-MM-YYYY"`, `DateRegex() ==
      \d{2}-\d{2}-\d{4}`
- [x] write a test that adds a second entry to `supported` at test
      time (via the test helper) for an alternative layout and verifies
      the accessors return the right label and regex for the second
      entry. This guards against the accessors being hardcoded to the
      default
- [x] write a test for the unknown-layout error path (setter called
      with a layout not in `supported`) — decide panic vs. fallback at
      implementation time and test whichever is chosen (chose panic)
- [x] run `go test ./internal/config/...` — must pass before next task

### Task 2: Extend internal/schedule with FormatDisplay + layout-aware Parse

**Files:**
- Modify: `internal/schedule/schedule.go`
- Modify: `internal/schedule/schedule_test.go`
- Modify (all are `schedule.Parse` callers; must all be updated in this
  task or the build breaks):
  - `cmd/add.go` (line 32)
  - `cmd/edit.go` (line 42)
  - `cmd/ls.go` (only passes validation via `schedule.Parse` indirectly
    through the add path; still, any direct call to update)
  - `cmd/add_test.go` (line 21)
  - `cmd/ls_test.go` (line 358)
  - `internal/tui/model.go` (lines 1258, 1656, 1995, 2012, 2176 —
    reschedule modal, applyEditedYAML, grab-mode moves, add modal)
  - `internal/tui/model_test.go` (lines 25, 3449)

- [x] add `FormatDisplay(iso, layout string) string` — parses `iso` with
      `IsoLayout`; on success returns `t.Format(layout)`; on
      failure/empty/bucket returns `iso` unchanged
- [x] change `Parse` signature to `Parse(input string, now time.Time,
      layout string) (string, error)`. Order: bucket switch, then
      `time.Parse(layout, input)`, then legacy `time.Parse(IsoLayout,
      input)`, then error
- [x] introduce a sentinel `var ErrInvalid = errors.New(...)` (or a
      simple named error type) so callers can format the user-facing
      message themselves with `config.DateFormatLabel()`. Parse
      returns the sentinel wrapped with the offending input via
      `fmt.Errorf("%w: %q", ErrInvalid, input)`
- [x] update the package doc comment to describe the new canonical
      input format as configurable + note that legacy ISO input is
      still tolerated silently
- [x] update every caller enumerated above to pass `config.DateFormat()`
      as the new third argument. Callers in `cmd/` and
      `internal/tui/model.go` that previously surfaced the Parse
      error verbatim now wrap it into a user-facing message
      referencing `config.DateFormatLabel()` (cmd/add.go and cmd/edit.go
      do this explicitly; TUI call sites keep the existing `m.err = err`
      path — Task 5 replaces that with a user-facing wrapper as part of
      the placeholder/error-wording sweep)
- [x] write tests for `FormatDisplay` with DD-MM-YYYY (default) and
      one alternative layout added via the test helper from Task 1;
      cover empty and unparseable passthrough
- [x] update `TestParse` (or add cases) to exercise: DD-MM-YYYY input,
      ISO input (legacy path), invalid input returns `ErrInvalid`
      (use `errors.Is` to assert), explicit alternative layout to
      prove the parameter is wired through and not ignored
- [x] update all caller tests (listed above) that fail because of the
      signature change — pass the layout explicitly or via
      `config.DateFormat()`
- [x] run `go test ./...` — compile must succeed and all tests must
      pass before next task

### Task 3: Switch compact task-list date column to DD-MM / DD-MM-YY

**Files:**
- Modify: `internal/display/dates.go`
- Modify: `internal/display/dates_test.go`
- Modify: any caller of `FormatRelDate` / `FormatTaskDates` that needs to
  pass the layout (TUI table, `cmd/ls.go` printer)

- [ ] change `shortDate` to take the configured layout and render by
      building a short-form layout from the full layout via
      `strings.Replace(layout, "2006", "06", 1)`, then delegating to
      `t.Format`. Same-year output uses a same-year short layout that
      drops the year token entirely (derived once for each supported
      layout — for the default `"02-01-2006"`, same-year is
      `"02-01"`). Caveat: this short-form transform assumes the year
      is last; for year-leading layouts (e.g. `"2006-01-02"`),
      `"06-01-02"` is ambiguous with a DD-MM-YY date. Only the
      default DD-MM-YYYY is supported today, so the ambiguity does
      not bite. When a year-leading format is added to `supported`,
      revisit this function — a cross-year test for such a layout
      should be added at that time
- [ ] change `FormatRelDate` and `FormatTaskDates` to accept the layout
      and thread it to `shortDate`
- [ ] update the doc comments on `FormatRelDate` so the formats described
      (`MM-DD`, `YY-MM-DD`) are replaced with the new ones computed from
      the configured layout (describe the default and note the layout
      parameter controls it)
- [ ] update every caller of `FormatRelDate` / `FormatTaskDates` to pass
      `config.DateFormat()`. Callers live in `internal/display/table.go`,
      `cmd/ls.go`, and any TUI panels that render timestamps
- [ ] update every asserted string in `TestFormatRelDate` and
      `TestFormatTaskDates` that currently matches the old format
      (`"04-06"` → `"06-04"`, `"03-28"` → `"28-03"`, `"25-03-28"` →
      `"28-03-25"`, `"04-13"` → `"13-04"`, `"27-01-15"` →
      `"15-01-27"`, `"03-01"` → `"01-03"`)
- [ ] add at least one test case that passes an alternative layout
      (e.g. `"2006-01-02"`) and asserts the date renders in that layout,
      proving the param is used
- [ ] run `go test ./internal/display/...` — must pass before next task

### Task 4: Switch note separator to the configured format + add dateFormat to recurrence spawn

**Files:**
- Modify: `internal/model/note.go`
- Modify: `internal/model/note_test.go`
- Modify: `internal/recurrence/spawn.go` (AppendNote callers at
  lines 48 and 115; `CompleteAndSpawn` signature)
- Modify: `cmd/done.go` (caller of `CompleteAndSpawn`)
- Modify: `internal/tui/model.go` (AppendNote at line 1134; caller of
  `CompleteAndSpawn` on the `d`-key handler)
- Modify: `cmd/note.go` (AppendNote caller at line 39)
- Modify: `internal/store/store.go` (CountNotes callers in
  `Create`/`Update`)

Rationale: `AppendNote` signature changes AND `CompleteAndSpawn`
signature changes in this task together. Splitting them across tasks
would leave an intermediate state where `internal/recurrence/spawn.go`
calls `AppendNote` with three args against a four-arg signature —
broken build.

- [ ] change `AppendNote` signature to `AppendNote(body, text string,
      now time.Time, layout string) string` and format the separator
      with `now.Format(layout + " 15:04:05")`
- [ ] change `CountNotes` signature to `CountNotes(body, dateRegex
      string) int` and build the full matching regex as
      `^--- <dateRegex> \d{2}:\d{2}:\d{2} ---$` (anchored per line)
- [ ] drop backward-compat for the old `YYYY-MM-DD HH:MM:SS`
      separator. A note body written by an older monolog version will
      stop contributing to `NoteCount` after this change. Document
      this decision in the function doc comment so future maintainers
      don't "fix" it. No migration script is shipped — the user
      accepted that trade-off
- [ ] change `CompleteAndSpawn` (or equivalent exported spawn entry)
      to take a new `dateFormat string` parameter and thread it down
      to: (a) `AppendNote` calls for both cross-reference notes and
      (b) the commit message's next-date formatting, while keeping
      `newTask.Schedule` formatted via `schedule.IsoLayout`
- [ ] update `cmd/done.go` to pass `config.DateFormat()` to
      `CompleteAndSpawn`
- [ ] update the TUI `d`-key handler in `internal/tui/model.go` to
      pass `config.DateFormat()` to `CompleteAndSpawn`
- [ ] update every AppendNote/CountNotes call site to pass the
      configured layout / regex:
      - `cmd/note.go` (AppendNote) → `config.DateFormat()`
      - `internal/tui/model.go:1134` (detail-panel textarea append) →
        `config.DateFormat()`
      - `internal/recurrence/spawn.go:48, 115` (both cross-ref
        AppendNote calls) → layout threaded from the
        `CompleteAndSpawn` parameter
      - `internal/store/store.go` (Create/Update paths recomputing
        `NoteCount`) → `config.DateRegex()`
- [ ] update `TestAppendNote` and existing `CountNotes` tests to pass
      the DD-MM-YYYY layout and expect new-format separators
- [ ] add test cases that pass an alternative layout (via the Task 1
      test helper to add a second entry to `supported`) to confirm
      the parameter is wired through
- [ ] verify the "separator-lookalike line must NOT count" cases still
      fail for the right reasons under the new regex
- [ ] run `go test ./internal/model/... ./internal/store/...
      ./internal/recurrence/... ./cmd/...` — must pass before next
      task

### Task 5: Update remaining TUI + CLI user-facing strings and display formatting

Note: `CompleteAndSpawn` signature + commit-message formatting landed
in Task 4 together with `AppendNote`. This task covers every remaining
user-facing date rendering: error wording, TUI placeholders, detail
panels, YAML marshal, and the `monolog show` output.

**Files:**
- Modify: `cmd/ls.go`
- Modify: `cmd/show.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`
- Modify: `cmd/done_recurring_test.go`
- Modify: `cmd/recurring_acceptance_test.go`
- Modify: `cmd/show_test.go`

- [ ] `cmd/ls.go`: update the `invalid schedule` error message to use
      `config.DateFormatLabel()` instead of the hardcoded
      `"ISO date (YYYY-MM-DD)"`
- [ ] `cmd/show.go` (line 46): render `task.Schedule` through
      `schedule.FormatDisplay(task.Schedule, config.DateFormat())` in
      the `Schedule:  <bucket> (<date>)` line so `monolog show`
      matches the rest of the UI
- [ ] `internal/tui/model.go` reschedule modal: replace
      `m.input.Placeholder = "YYYY-MM-DD"` with
      `m.input.Placeholder = config.DateFormatLabel()`; replace the
      `"invalid date %q (want YYYY-MM-DD)"` error with one that
      uses `config.DateFormatLabel()`. Replace the
      `schedule.IsISODate(date)` gate with a direct call to
      `schedule.Parse(date, now, config.DateFormat())` and rely on
      its validation + normalization (cleaner, aligns with how the
      add modal works, and automatically accepts the new format)
- [ ] `internal/tui/model.go` detail panel header (~line 2503-2505):
      render `task.Schedule` through `schedule.FormatDisplay(t.Schedule,
      config.DateFormat())` so the user sees DD-MM-YYYY rather than ISO
- [ ] `internal/tui/model.go` `marshalTaskForEdit` (~line 1630):
      convert `t.Schedule` to the configured layout via
      `schedule.FormatDisplay` before writing it into the YAML buffer.
      `applyEditedYAML` already calls `schedule.Parse` which (after
      Task 2) accepts the configured layout — no change needed on the
      parse side
- [ ] update `cmd/done_recurring_test.go` assertions for the commit
      message (`"done: <title> (recurring, next <date>)"`) and the
      `Spawned follow-up` note body to expect DD-MM-YYYY. Parses of
      `spawn.Schedule` via `"2006-01-02"` stay unchanged — stored
      format
- [ ] update `cmd/recurring_acceptance_test.go` if it asserts the
      same commit-message/note text
- [ ] update `cmd/show_test.go` to assert the new DD-MM-YYYY output
      on the `Schedule:` line
- [ ] update `internal/tui/model_test.go` for any custom-date
      reschedule test cases (inputs that were `"2026-04-13"` become
      DD-MM-YYYY equivalents; error-message assertions follow the
      new wording)
- [ ] run `go test ./...` — must pass before next task

### Task 6: Verify acceptance criteria end-to-end

- [ ] verify `./monolog add "buy milk" --schedule 15-04-2026`
      round-trips: `monolog ls` shows the task with the correct bucket,
      stored JSON has `"schedule": "2026-04-15"`, the task-list date
      column shows the new compact format
- [ ] verify legacy input `--schedule 2026-04-15` still works silently
      (no error, same stored value)
- [ ] verify `--schedule 32-04-2026` (invalid day) produces an error
      mentioning `DD-MM-YYYY`
- [ ] verify the TUI reschedule custom-date modal accepts `15-04-2026`
      and rejects junk input with the new error wording
- [ ] verify the TUI YAML editor shows the schedule as `DD-MM-YYYY` and
      round-trips on save
- [ ] verify recurrence spawn: complete a `weekly:mon` task and check
      the commit message says `next DD-MM-YYYY` and the `Spawned
      follow-up` note uses DD-MM-YYYY
- [ ] run full suite: `go test ./... && go vet ./...`
- [ ] grep the codebase for any remaining `"YYYY-MM-DD"` user-facing
      strings and confirm each is either a storage-format reference in
      a comment, part of the legacy-parse tolerance in `schedule.Parse`,
      or otherwise intentional

### Task 7: Update docs and move plan

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [ ] update `README.md`: every `YYYY-MM-DD` reference in help/usage
      blocks for `--schedule` switches → `DD-MM-YYYY`. Specifically
      update line 90 ("notes are stored inside the task's body using
      `--- YYYY-MM-DD HH:MM:SS ---` separators") to reference the new
      separator format. Mention that the on-disk `schedule` field
      stays ISO, and that the display format will be configurable in
      the future (today it's a compile-time default in
      `internal/config`)
- [ ] update `CLAUDE.md`: rewrite the "Schedule values" and "Task
      notes" bullets under "Key Design Decisions" to describe:
      - user-facing/input format is configurable via `internal/config`
        (default DD-MM-YYYY); on-disk stays ISO
      - `schedule.Parse` accepts both the configured format and legacy
        ISO input (silent backward compat for scripts)
      - `CountNotes` only recognizes separators in the currently
        configured format — no backward compat, so changing the
        format loses historical note counts
- [ ] move this plan: `mkdir -p docs/plans/completed && git mv
      docs/plans/20260418-dd-mm-yyyy-dates.md docs/plans/completed/`

## Post-Completion

**Manual verification:**

- Run `./monolog` on a real `.monolog/` repo that already has tasks
  created under the old format. Confirm:
  - The task list renders the new compact column correctly for
    pre-existing tasks (their stored ISO `schedule` converts through
    `FormatDisplay` on display).
  - Opening the TUI YAML editor on a pre-existing task shows the
    schedule in DD-MM-YYYY; saving without edits does not change the
    stored JSON.
  - **Pre-existing notes with ISO separators no longer count toward
    `[N]` badges** — this is the intentional trade-off for dropping
    backward compat on the regex. If this turns out to hurt more
    than expected, a one-off rewrite script over `.monolog/tasks/*.json`
    can re-format separators.
  - New notes added via `monolog note <id> "text"` or the TUI detail
    panel use the new separator format.
- Confirm `git log` of the `.monolog/` repo after a recurrence
  completion shows the commit message with the new date format.

**Future settings wiring** (not part of this plan, documented for
context):

- When settings persistence lands, `internal/config` grows a loader
  that reads `date_format` from the settings file on startup and
  updates the package-level variable. No caller signatures change.
- Valid values are the Go layout strings in the derivation table
  (today: `02-01-2006`; extensible to e.g. `2006-01-02` or
  `01/02/2006`).
