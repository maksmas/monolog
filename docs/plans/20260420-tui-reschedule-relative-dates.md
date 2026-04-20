# TUI Reschedule Relative Dates

## Overview

Add relative date shorthands (`Nd`, `Nw`, `Nm`) to the TUI reschedule modal's custom date input. Users can type `3d`, `2w`, or `1m` to schedule a task N days, N weeks, or N months from today. This reduces friction for the common case of "push this out a few days" without having to know or type an absolute date.

## Context (from discovery)

- Files/components involved:
  - `internal/schedule/schedule.go` — Parse, bucket logic; extension point for new helper
  - `internal/schedule/schedule_test.go` — unit tests for schedule parsing
  - `internal/tui/model.go` — `updateReschedule` (lines 1237–1285), modal view (line 2956)
  - `internal/tui/model_test.go` — existing reschedule tests at lines 210–304
- Key patterns: `schedule.Parse` is the canonical input-to-ISO converter; all callers pass `config.DateFormat()` through it
- Scope boundary: relative shorthands live in a new `schedule.ParseRelative` helper and are called only from `updateReschedule` in the TUI — no change to `schedule.Parse` signature or behavior

## Development Approach

- **Testing approach**: Regular (code first, tests after)
- Complete each task fully before moving to the next
- All tests must pass before starting next task
- Run `go test ./...` after each task

## Testing Strategy

- **Unit tests**: `internal/schedule/schedule_test.go` for `ParseRelative`, `internal/tui/model_test.go` for end-to-end reschedule modal behavior with new inputs
- **Manual**: open TUI, press `r` on a task, press `6`, type `3d` → should land in the correct bucket

## Solution Overview

Add `schedule.ParseRelative(input string, now time.Time) (string, bool)` that recognises `Nd` / `Nw` / `Nm` patterns (integer N ≥ 1, letters case-insensitive) and returns the resulting ISO date + `true`, or `"", false` if the input doesn't match. The TUI's `updateReschedule` calls this before `schedule.Parse`; on a hit it feeds the ISO directly to `applyReschedule`. Placeholder text and the modal header are updated to advertise the new syntax.

## Technical Details

- Pattern: `^(\d+)[dDwWmM]$` — match leading integer, single suffix letter
- `d`/`D` → `AddDate(0, 0, N)`
- `w`/`W` → `AddDate(0, 0, N*7)`
- `m`/`M` → `AddDate(0, N, 0)` (stdlib handles month-end clamping)
- No conflict with bucket names (they are full words: `today`, `tomorrow`, `week`, `month`, `someday`) or date formats
- Error message when invalid: unchanged — falls through to `schedule.Parse` which returns `ErrInvalid`
- Placeholder hint updated from `config.DateFormatLabel()` to include the shorthand examples

## What Goes Where

**Implementation Steps** — all within this codebase.

**Post-Completion**:
- Manual TUI smoke test: `r` → `6` → type `3d`, `2w`, `1m` → verify correct bucket placement

## Implementation Steps

### Task 1: Add `ParseRelative` helper to `internal/schedule`

**Files:**
- Modify: `internal/schedule/schedule.go`
- Modify: `internal/schedule/schedule_test.go`

- [ ] add `ParseRelative(input string, now time.Time) (string, bool)` to `schedule.go`; match `^(\d+)[dDwWmM]$`, dispatch to AddDate, return ISO + true on hit, `"", false` on miss
- [ ] write `TestParseRelative` table-driven test covering: `3d`, `2w`, `1m`, `10D`, `0d` (edge: N=0 is valid — today), uppercase variants, empty string, `3x` (no match), full word `day` (no match)
- [ ] run `go test ./internal/schedule/` — must pass

### Task 2: Wire into TUI reschedule modal

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] in `updateReschedule` (around line 1270), call `schedule.ParseRelative(date, time.Now())` before `schedule.Parse`; on match feed ISO to `applyReschedule` with `date` as `displayLabel`
- [ ] update the modal placeholder: change from `config.DateFormatLabel()` to `config.DateFormatLabel() + " or 3d/2w/1m"` (line 1254)
- [ ] update modal body text at line 2956 from `"Custom date:\n\n"` to `"Custom date (or Nd/Nw/Nm):\n\n"`
- [ ] add `TestReschedule_RelativeDate_Days`, `_Weeks`, `_Months` tests mirroring the pattern of `TestReschedule_CustomDate` (lines 237–264): open reschedule modal, press `6`, send keystrokes for `3d` / `2w` / `1m`, press Enter, verify task schedule lands in expected bucket
- [ ] run `go test ./internal/tui/` — must pass

### Task 3: Verify acceptance criteria

- [ ] verify `3d` → task scheduled 3 days from today, lands in correct bucket
- [ ] verify `2w` → 14 days out
- [ ] verify `1m` → 1 month out
- [ ] verify invalid input (e.g. `3x`) still shows the existing error message
- [ ] run full test suite: `go test ./...`
- [ ] run `go vet ./...`

### Task 4: [Final] Update documentation

- [ ] update CLAUDE.md `schedule.Parse` bullet to mention `ParseRelative` is called by the TUI before `Parse` for shorthand input
- [ ] move this plan to `docs/plans/completed/`
