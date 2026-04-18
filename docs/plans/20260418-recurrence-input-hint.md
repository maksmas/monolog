# Recurrence Input Hint & Autocomplete

## Overview

Today, a user entering a recurrence rule has no in-UI reminder of the grammar. The TUI add modal's Recur field shows only the single example `e.g. monthly:1` in its placeholder, and the YAML editor shows nothing at all. Users who forget the syntax must reach for `monolog add --help`, CLAUDE.md, or the source.

This plan adds two complementary affordances:

1. **Static hint line** showing the full grammar — always visible in the TUI add modal under the Recur field, embedded as a header comment in the YAML editor buffer, and expanded in the CLI `--recur` flag help text.
2. **Inline autocomplete** in the TUI add modal — as the user types in the Recur field, a suggestion dropdown appears using a similar navigation model to the existing tag autocomplete (Up/Down to move, **Tab to accept**, Esc to dismiss). Unlike the tag field, Enter does NOT accept a suggestion on the Recur field — Enter is reserved for submitting the form. This is a deliberate divergence from tag autocomplete: on the Recur field the modal is typically one-line-away from submission, so keeping Enter as submit avoids an extra keypress for users who don't need suggestions.

Together these give discoverability at a glance (hint) and low-friction input for users who know the shape (autocomplete).

## Context (from discovery)

Files/components involved:
- `internal/recurrence/recurrence.go` — grammar: `monthly:N`, `weekly:<day>` (with aliases), `workdays`, `days:N`. Canonical weekdays are three-letter lowercase (`mon`..`sun`).
- `internal/tui/model.go` — add modal state (`recurInput` at line 144, `addFocusRecur` at line 49), modal view renderer (`modalBox` call at line 2817), shared suggestion state (`suggestions`, `suggestionIdx`) currently used by tag autocomplete.
- `internal/tui/model.go` — YAML edit marshal/apply (`marshalTaskForEdit` around line 1623, `applyEditedYAML` around line 1641).
- `cmd/add.go:95` and `cmd/edit.go:116` — current `--recur` help strings.
- `model.FilterTags` — reference pattern for autocomplete: pure function returning ≤5 case-insensitive prefix matches.

Related patterns found:
- Tag autocomplete renders suggestions below the tag input, accepts with Tab/Enter, dismisses with Esc — this is the pattern to mirror for recurrence (with one semantic difference: recurrence is a single value, so accepting replaces the full input rather than appending to a comma-separated list).
- `FilterTags` is a pure function in `internal/model/` with its own unit tests — the recurrence suggester should follow the same shape.

Dependencies identified:
- `recurrence.Canonicalize` already validates input at submit time — autocomplete does not need its own validator, it only needs to surface valid completions.
- No new third-party dependencies required.

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
- maintain backward compatibility — no change to stored JSON, canonical grammar, or CLI flags

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above).
- **TUI tests**: follow the existing style in `internal/tui/model_test.go` — use the `key(t, m, ...)` helper to drive keypresses and assert on model state and rendered view strings. No e2e tests in this project.
- **acceptance**: the final task runs `go test ./...` and `go vet ./...` to confirm nothing regressed.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

**Hint** is a static string. It lives in exactly one place — a package-level constant (e.g. `recurrence.GrammarHint = "monthly:N | weekly:<day> | workdays | days:N"`) — and is referenced from:
- the TUI add-modal view,
- the YAML edit buffer (prepended as a `# ` header comment),
- the CLI `--recur` flag help strings on `add` and `edit`.

Keeping the hint text in `internal/recurrence` keeps grammar documentation colocated with the parser, so adding a new rule updates the hint automatically via a single edit site.

**Autocomplete** is driven by a new pure function `recurrence.Suggest(fragment string) []string`. It returns up to 5 case-insensitive prefix matches from a static completion set derived from the grammar:
- Top-level kinds: `monthly:N`, `weekly:`, `workdays`, `days:N`.
- After `weekly:<prefix>`: `weekly:mon`..`weekly:sun` filtered by the post-colon fragment.
- After `monthly:<anything>`: the single template `monthly:N` (no numeric suggestions — user types N).
- After `days:<anything>`: the single template `days:N`.

`monthly:N` and `days:N` are surfaced as literal templates. Accepting one places `monthly:` / `days:` followed by a literal `N` placeholder — the user deletes `N` and types their number. This is a deliberate trade-off: it teaches the shape, but the user must edit. (Alternative considered: suggest `monthly:1..monthly:31` — rejected as noisy. Chosen per discovery.)

The suggestion widget reuses the existing TUI suggestion plumbing: the `suggestions []string` and `suggestionIdx int` fields on `Model`. These are already shared between the tag-input and retag modals (only one active at a time). The recur field is in the same add modal as tags, so we extend the "which input owns the suggestions" logic to consider `addFocus == addFocusRecur`.

Two behavioral differences from tag autocomplete:
1. Accepting a suggestion for recurrence **replaces** the full input text (it is a single value), rather than appending after a comma. The accept handler branches on which field is focused.
2. Only **Tab** accepts a recurrence suggestion. Enter is reserved for submitting the modal, because the recur field is the last in the Tab cycle — users who do not need autocomplete should be able to press Enter directly to create the task without first dismissing a dropdown.

## Technical Details

### New package-level additions to `internal/recurrence`

```go
// GrammarHint is the one-line human-readable form reference shown in hints
// and help text. Keep in sync with Parse.
const GrammarHint = "monthly:N | weekly:<day> | workdays | days:N"

// Suggest returns up to maxSuggestions completion candidates for the given
// partial input, matching case-insensitively on prefix. Returns an empty
// slice when input is empty or no candidate prefix-matches.
func Suggest(input string) []string
```

- `maxSuggestions = 5` to match the tag-autocomplete convention.
- Completion set ordering: deterministic — alphabetical within weekday block; top-level order `monthly:N`, `weekly:`, `workdays`, `days:N`.
- Empty input returns empty slice (same as `FilterTags`; the dropdown should not appear without user intent).
- The function is pure — no dependency on `Model` or `time`.

### TUI add modal (`internal/tui/model.go`)

1. **Suggestion owner**: extend the block that currently refreshes `m.suggestions` on tag-field input changes. When `addFocus == addFocusRecur`, call `recurrence.Suggest(m.recurInput.Value())` instead of `model.FilterTags(...)`. When focus leaves the recur field, clear suggestions.
2. **Accept handler**: when Tab/Enter is pressed with a non-empty suggestion list and the recur field is focused, replace `m.recurInput.Value()` with the selected suggestion rather than appending. Preserve existing tag-append behavior when tag field is focused.
3. **Esc** and **focus cycling** behavior already handle dismissal correctly for tags; verify they work for recur with no changes.
4. **View**: in `modalBox(...)` for the add modal, after the `Recur:` input line, render `(monthly:N | weekly:<day> | workdays | days:N)` as a dim-styled hint line, and below it the suggestion list (if non-empty). Reuse the existing `sugLines` rendering path where possible.

### TUI YAML editor

In `marshalTaskForEdit`, emit a header comment block at the top of the generated YAML buffer describing the grammar, e.g.:
```
# recurrence rules: monthly:N | weekly:<day> | workdays | days:N
```
Comments are ignored by `yaml.Unmarshal` so `applyEditedYAML` needs no change. Place the comment only when the task has a `recurrence:` line (i.e., non-empty `Recurrence`) OR always — decide during implementation. Default: always, to help users who want to *set* a recurrence via edit.

### CLI flag help

Expand `--recur` help from:
```
Recurrence rule: monthly:N, weekly:<day>, workdays, days:N
```
to:
```
Recurrence rule: monthly:N | weekly:<day> | workdays | days:N (e.g. monthly:1, weekly:mon, workdays, days:7)
```
on both `cmd/add.go:95` and `cmd/edit.go:116`.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): pure-function suggester, TUI wiring, CLI help strings, YAML header, tests.
- **Post-Completion** (no checkboxes): manual smoke test — open TUI, press `a`, Tab Tab, type `mon`, verify dropdown; type `weekly:`, verify weekdays; press Tab/Enter, verify input replaced.

## Implementation Steps

### Task 1: Add `recurrence.GrammarHint` and `recurrence.Suggest`

**Files:**
- Modify: `internal/recurrence/recurrence.go`
- Create: `internal/recurrence/suggest.go`
- Create: `internal/recurrence/suggest_test.go`

- [x] add `GrammarHint` string constant in `internal/recurrence/recurrence.go` (or `suggest.go`) with value `"monthly:N | weekly:<day> | workdays | days:N"`
- [x] create `internal/recurrence/suggest.go` with `Suggest(input string) []string`; implement static completion set: `monthly:N`, `weekly:`, `workdays`, `days:N` at top level; for `weekly:<prefix>` return filtered `weekly:<day>` canonical three-letter forms; for `monthly:*` and `days:*` return the single template form
- [x] ensure ordering is deterministic; cap results at 5; case-insensitive prefix match
- [x] empty input returns empty slice (no dropdown when field is blank)
- [x] write table-driven tests covering: empty input, `m` → `monthly:N`, `w` → `weekly:`+`workdays`, `week` → `weekly:`, `weekly:` → seven weekdays, `weekly:t` → `weekly:tue`+`weekly:thu`, `weekly:MON` (case), `monthly:1` → still `monthly:N` template (user has not finished), `days:` → `days:N`, `xyz` → empty
- [x] verify the 5-result cap: `weekly:` returns exactly 5 (not 7) and preserves deterministic ordering
- [x] run tests - must pass before task 2: `go test ./internal/recurrence/`

### Task 2: Wire suggestions into the TUI add modal

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] locate the block that refreshes `m.suggestions` on tag-field input changes; extend it so `addFocus == addFocusRecur` triggers `recurrence.Suggest(m.recurInput.Value())` instead of `model.FilterTags(...)`
- [x] clear `m.suggestions` and reset `m.suggestionIdx` when focus leaves the recur field (Tab cycle into/out of recur)
- [x] accept behavior: on the recur field, **Tab** (not Enter) accepts a selected suggestion — `m.recurInput.SetValue(selected)` then clears suggestions. Do NOT append (recurrence is a single value). **Enter on the recur field continues to submit the modal**, even when the dropdown is visible — this is a deliberate divergence from tag autocomplete (see Solution Overview).
- [x] adjust the existing Tab-cycle logic on the recur field so that when the dropdown is visible, Tab accepts the suggestion instead of cycling focus back to the title field. When the dropdown is empty (no input or no matches), Tab retains its current cycle-focus behavior.
- [x] verify Up/Down navigation already works via the shared `suggestionIdx` path (no change expected)
- [x] verify Esc dismiss already works (no change expected)
- [x] write TUI test: open add modal, Tab Tab (focus recur), type `week`, assert `m.suggestions` contains `weekly:` (and/or workdays), press Tab to accept, assert `m.recurInput.Value() == "weekly:"`
- [x] write TUI test: focused recur with `weekly:` typed → suggestions include all 7 weekdays canonical forms (capped as needed); Tab accepts `weekly:mon` and replaces input
- [x] write TUI test: with the recur dropdown visible, **Enter submits the modal** (creates the task with the current typed value, ignoring the highlighted suggestion) — guards the divergence from tag behavior
- [x] write TUI test: with the recur field focused and dropdown empty (no matches), Tab cycles focus back to title (current behavior preserved)
- [x] write TUI test: tag-field suggestions still behave the old way — both Tab and Enter accept (append with `, `) — regression guard
- [x] write TUI test: Tab cycling away from recur (when dropdown empty) clears `m.suggestions` / resets `m.suggestionIdx`
- [x] run tests - must pass before task 3: `go test ./internal/tui/`

### Task 3: Render hint line and suggestions in the TUI add modal view

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] in the modal-rendering code path that builds the add-modal string (around `modalBox(fmt.Sprintf("Add task to %s:...`), insert a dim-styled hint line under the Recur input showing `recurrence.GrammarHint` wrapped in parentheses
- [x] render the suggestion list under the hint when `addFocus == addFocusRecur` and `len(m.suggestions) > 0`, following the same visual style (`>` marker on selected item) used for tag suggestions
- [x] confirm the existing width calculation still fits; if the modal now overflows, shrink input widths proportionally or allow modal to grow vertically
- [x] write TUI view test: rendered add modal contains the literal hint substring `"monthly:N | weekly:<day> | workdays | days:N"`
- [x] write TUI view test: with recur focused and input `weekly:`, rendered view contains at least one weekday suggestion (`weekly:mon`)
- [x] write TUI view test: with tag focused, rendered view does NOT contain the recurrence hint-line suggestions (guards against leaking suggestions across fields)
- [x] run tests - must pass before task 4: `go test ./internal/tui/`

### Task 4: Add grammar comment to YAML edit buffer

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] in `marshalTaskForEdit`, prepend a `# recurrence rules: <GrammarHint>` header comment line above the marshaled YAML body
- [ ] confirm `applyEditedYAML` still parses correctly (yaml.v3 ignores `#` comments) — no change expected, but add a regression test
- [ ] write test: `marshalTaskForEdit` output contains `# recurrence rules: monthly:N | weekly:<day> | workdays | days:N`
- [ ] write test: round-trip — marshal, prepend comment stays, apply with no user edits → original Task preserved including Recurrence field
- [ ] write test: user removes the comment line in editor → apply still works
- [ ] run tests - must pass before task 5: `go test ./internal/tui/`

### Task 5: Expand CLI `--recur` help text

**Files:**
- Modify: `cmd/add.go`
- Modify: `cmd/edit.go`
- Modify: `cmd/add_test.go` (or new test file `cmd/recur_help_test.go` — choose per codebase convention)

- [ ] replace the `--recur` flag help string on `cmd/add.go` with a longer form including explicit examples, e.g. `"Recurrence rule: monthly:N | weekly:<day> | workdays | days:N (e.g. monthly:1, weekly:mon, workdays, days:7)"`
- [ ] replace the same on `cmd/edit.go`, preserving the `(pass "" to clear)` suffix
- [ ] have the help strings reference `recurrence.GrammarHint` where practical (string concat) so the grammar stays in one place
- [ ] write test: running `monolog add --help` output contains all four grammar forms
- [ ] write test: running `monolog edit --help` output contains all four grammar forms AND the `pass "" to clear` note
- [ ] run tests - must pass before task 6: `go test ./cmd/`

### Task 6: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented: hint visible in TUI add modal, YAML editor, and CLI help; autocomplete works in TUI add modal
- [ ] verify edge cases: empty input shows no dropdown; invalid partial input (e.g. `xyz`) shows no dropdown; Tab accepts suggestion and replaces input (does not append); Enter on recur field submits (does not accept suggestion); Esc dismisses dropdown
- [ ] run full test suite: `go test ./...`
- [ ] run lint: `go vet ./...`
- [ ] verify no regression in tag autocomplete: `go test ./internal/tui/ -run Tag`
- [ ] manual TUI smoke test: `go build -o monolog && ./monolog` → `a` → Tab Tab → type `m`, `w`, `weekly:`, `weekly:t`; accept one; submit; confirm task created with canonical recurrence

### Task 7: Update documentation and move plan

- [ ] update `CLAUDE.md` to mention the new recurrence autocomplete / hint behavior alongside the existing Tag autocomplete bullet (one sentence)
- [ ] move this plan to `docs/plans/completed/`: `mkdir -p docs/plans/completed && git mv docs/plans/20260418-recurrence-input-hint.md docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification**:
- open the TUI, press `a`, Tab to tags, Tab to recur, and explore the dropdown by typing `m`, `w`, `week`, `weekly:`, `weekly:t`. Confirm hint line is always visible.
- edit an existing task with `e` in TUI and confirm the YAML buffer opens with the grammar comment at the top.
- `monolog add --help` and `monolog edit --help` — confirm the new help strings read well.
