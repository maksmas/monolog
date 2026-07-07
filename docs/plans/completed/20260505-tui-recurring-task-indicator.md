# TUI: Surface Recurring Tasks Visibly

## Overview

Today, a task's `Recurrence` field is invisible inside the TUI. It is only displayed by the CLI commands `monolog show` (`cmd/show.go:60`) and `monolog ls` (`internal/display/table.go:114`). Inside the TUI a user cannot tell at a glance which tasks recur, and even after opening the detail panel the rule is not shown — only via the YAML editor.

This plan closes that gap with two minimal additions:

1. **List-line marker** — append `[↻]` to the existing description-line metadata in `item.Description()` (`internal/tui/model.go:263`) when `task.Recurrence != ""`. Order: immediately after the `[N]` notes badge, before schedule/tags/dates. The bracketed glyph matches the existing `[N]` / `[tag]` bracket convention and is deliberately distinct from the bare `↻` already used in the status bar during email sync.
2. **Detail panel `Recur:` line** — extend `Model.detailPanelView()` (`internal/tui/model.go:2953`) so the header block shows `Recur: <rule>` directly after the `Schedule:` line. Plain text, mirrors `monolog show`'s format. Emitted only when the field is non-empty.

That's the entire change: two render sites, one symbol, no schema migration, no theme additions, no new flags.

## Context (from discovery)

Files/components involved:
- `internal/tui/model.go:263` — `item.Description()` builds the dim second line as space-separated parts; existing parts are ShortID, `[N]` notes badge, schedule (when not today), `[tags]`, dates.
- `internal/tui/model.go:2953` — `Model.detailPanelView()` builds the right-side detail panel header with title, schedule, tags, dates. Recurrence is NOT currently rendered here, even though `task.Recurrence` is editable through the YAML editor.
- `internal/tui/model_test.go` — existing test patterns for view rendering and item metadata; new tests follow the same shape.
- `cmd/show.go:60` — reference format `Recur:     <rule>` (CLI uses padding for column alignment; TUI panel uses single-space `Recur: <rule>` to match the surrounding `Schedule: …` / `Tags: …` style).

Related patterns found:
- `[N]` notes badge in `item.Description()` is the closest precedent — single-property bracket marker, conditional on a non-zero field, placed early in the parts list.
- `detailPanelView` already conditionally renders `Tags: …` only when there are visible tags — same conditional shape applies to a new `Recur:` line.

Dependencies identified:
- No new dependencies. `Recurrence` is already on `model.Task` and is preserved by all mutations.
- No schema change — JSON storage already carries `recurrence,omitempty`.

## Development Approach

- **testing approach**: Regular (code first, then tests) — matches existing codebase style; each task ends by writing tests for just-added code.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change: `go test ./...`
- maintain backward compatibility — no change to stored JSON, canonical grammar, CLI flags, or theme contract

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above).
- **TUI tests**: follow the existing style in `internal/tui/model_test.go`. For `item.Description()`, construct an `item` directly and assert on its returned string. For the detail panel, build a `Model` via `newTestModel` (or whatever the existing pattern is in `model_test.go`), open the detail panel for a chosen task, and assert on the rendered substring.
- **acceptance**: the final task runs `go test ./...` and `go vet ./...` to confirm nothing regressed.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

**Marker symbol**: `[↻]` — a literal bracketed clockwise-arrow glyph. The bracket form matches the existing `[N]` / `[tag]` convention so the marker reads as "this task has the recurring property" rather than "currently syncing" (which is how the bare `↻` already reads in the status bar during email sync).

**List-line placement**: in the parts list assembled by `item.Description()`, insert `[↻]` immediately after the `[N]` notes badge. Both are bracketed single-task properties; clustering them keeps the visual rhythm consistent. A recurring task with two notes scheduled for tomorrow with the `work` tag renders as:

```
01J5K  [2]  [↻]  06-05-2026  [work]
```

When the task has no notes and no recurrence, both markers are omitted (existing behavior preserved for `[N]`).

**Detail panel placement**: the panel header currently renders Schedule, then Tags (conditional), then Dates. Insert `Recur: <rule>` between Schedule and Tags so the scheduling-related lines stay grouped. Conditional on `task.Recurrence != ""`.

Example (with recurrence set):
```
<title>
Schedule: 06-05-2026
Recur: weekly:mon
Tags: work, urgent
Created: 2 days ago  Completed: today
```

Example (without recurrence): the `Recur:` line is omitted; everything else is unchanged.

**Done tasks**: when a recurring task is completed, `recurrence.CompleteAndSpawn` preserves the `Recurrence` value on the original (now-done) task. The marker therefore appears on the done version too — which is correct: it tells the user "this task spawned a follow-up". No special-casing.

## Out of Scope

- **Search overlay** (`internal/tui/search.go`) — searches title/body; recurrence is unrelated to the search axis. No marker added there.
- **CLI `monolog ls`** — already shows recurrence. No change.
- **Stats bar / tab counts** — counting recurring tasks is a separate feature. YAGNI.
- **Dedicated recurrence filter / view** — also a future feature. YAGNI.
- **Color or new theme field for the marker** — uses the existing dim description-line color. The bracketed glyph is distinguishable on its own.

## Technical Details

### `internal/tui/model.go` — `item.Description()` (line ~263)

Insert one conditional after the existing `[N]` block:

```go
if i.task.NoteCount > 0 {
    parts = append(parts, fmt.Sprintf("[%d]", i.task.NoteCount))
}
if i.task.Recurrence != "" {
    parts = append(parts, "[↻]")
}
if i.task.Schedule != "" && schedule.Bucket(i.task.Schedule, i.now) != schedule.Today {
    ...
}
```

The marker is a literal string constant inside `Description()`. No package-level constant needed — the symbol is used in exactly one place. (If a future render path also wants the marker, promote it to a package-level `const recurMarker = "[↻]"` at that point.)

### `internal/tui/model.go` — `Model.detailPanelView()` (line ~2953)

After the Schedule append (around line 2980) and before the Tags conditional (around line 2983), insert:

```go
if task.Recurrence != "" {
    header = append(header, "Recur: "+task.Recurrence)
}
```

The line is plain text matching the surrounding `Schedule: …` / `Tags: …` lines — no styling, no theme field.

### Tests — `internal/tui/model_test.go`

Add tests that:
1. `item.Description()` includes `[↻]` when `Recurrence != ""` and omits it when empty.
2. When both `NoteCount > 0` AND `Recurrence != ""`, the order is `[N]` before `[↻]` (regression guard on placement).
3. `Model.detailPanelView()` (or the rendered detail panel string) contains the substring `Recur: weekly:mon` when the selected task has that recurrence, and omits `Recur:` entirely when the task has no recurrence.

If existing test helpers in `model_test.go` make detail-panel rendering awkward (e.g., requires a fully laid-out `Model`), assert on a smaller helper or refactor only as far as needed for the new test — do not introduce broad test-helper changes.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): list-line marker, detail-panel line, tests, doc + plan move.
- **Post-Completion** (no checkboxes): manual smoke test — open TUI, scan for `[↻]` on a known recurring task; press Enter to open the detail panel and confirm `Recur:` line.

## Implementation Steps

### Task 1: Add `[↻]` marker to `item.Description()`

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] in `item.Description()` (line ~263), after the `if i.task.NoteCount > 0` block and before the `if i.task.Schedule != ""` block, append `"[↻]"` to `parts` when `i.task.Recurrence != ""`
- [x] write a test asserting `Description()` contains `"[↻]"` when `Recurrence` is set (e.g. `"weekly:mon"`)
- [x] write a test asserting `Description()` does NOT contain `"[↻]"` when `Recurrence == ""`
- [x] write a test asserting that with `NoteCount=2` and `Recurrence="weekly:mon"`, the rendered description has `[2]` appearing before `[↻]` (use `strings.Index` on the returned string to assert order)
- [x] write a test asserting that with `NoteCount=0` and `Recurrence=""`, neither bracket marker appears (regression guard for empty-state)
- [x] run tests - must pass before task 2: `go test ./internal/tui/`

### Task 2: Add `Recur:` line to `detailPanelView`

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] in `Model.detailPanelView()` (line ~2953), after the Schedule line is appended to `header` and before the Tags conditional, append `"Recur: "+task.Recurrence` when `task.Recurrence != ""`
- [x] write a TUI test that builds a Model with a task whose `Recurrence == "weekly:mon"`, opens the detail panel for it, renders, and asserts the rendered string contains the substring `"Recur: weekly:mon"`
- [x] write a TUI test that builds the same Model with a task whose `Recurrence == ""`, opens the detail panel, and asserts the rendered string does NOT contain the substring `"Recur:"` (catches accidental empty-line emission)
- [x] write a TUI test that asserts the order in the rendered header: `Schedule:` line appears before `Recur:` line, which appears before `Tags:` line (when all three are present)
- [x] run tests - must pass before task 3: `go test ./internal/tui/`

### Task 3: Verify acceptance criteria

- [x] verify all requirements from Overview are implemented: marker visible in list, `Recur:` line visible in detail panel, both conditional on non-empty `Recurrence`
- [x] verify edge cases: done recurring task still shows `[↻]` (correct — original retains its rule); task with notes AND recurrence shows both badges in correct order; task with neither shows neither
- [x] run full test suite: `go test ./...`
- [x] run lint: `go vet ./...`
- [x] manual TUI smoke test: `go build -o monolog && ./monolog` → find a task with recurrence (or create one via `a` with the Recur field) → confirm `[↻]` appears in the list line → press Enter → confirm `Recur: <rule>` line in the detail panel header

### Task 4: Update documentation and move plan

- [x] update `CLAUDE.md` — extend the existing **Recurring tasks** bullet with one short sentence noting that the TUI list shows `[↻]` for recurring tasks and the detail panel shows a `Recur:` line. Keep it terse — match the surrounding bullet style.
- [x] move this plan: `git mv docs/plans/20260505-tui-recurring-task-indicator.md docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification**:
- create a task with a recurrence (`a` in TUI, fill Recur field with `weekly:mon`), confirm `[↻]` shows up in the list line description
- press Enter on that task to open the detail panel, confirm a `Recur: weekly:mon` line appears between Schedule and Tags
- complete the task with `d`, confirm both the new spawned task AND the now-done original still show `[↻]` in their respective tabs (intended behavior)
- on a non-recurring task: confirm neither the list marker nor the panel `Recur:` line appears
