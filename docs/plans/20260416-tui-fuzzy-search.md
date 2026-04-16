# TUI Fuzzy Search

## Overview

Add a fuzzy-search overlay to the TUI for quickly jumping to any task in the backlog — open or done. Triggered by `/` in normal mode, it presents a full-screen split-pane modal: input + results on the left, a live body preview on the right. Selecting a result dismisses the overlay and positions the cursor on the target task in its correct schedule/tag tab.

The primary job is **jump-to-task**: the user remembers a fragment of a title or body and wants to reach the task with a few keystrokes, then act on it with the existing normal-mode shortcuts (`d`, `e`, `r`, `a`, etc.). Peek preview lets the user verify they picked the right match before committing.

## Context (from discovery)

- **Files involved**:
  - `internal/tui/model.go` (2427 lines) — add `modeSearch` mode, `searchState` field on `Model`, route `/` in `updateNormal`, dispatch in `Update`, wire into `View()`.
  - `internal/tui/model_test.go` (4728 lines) — extend with search-flow integration tests.
  - `internal/tui/search.go` **(new)** — rendering, openSearch/closeSearch/commitSearch, key routing, layout split.
  - `internal/tui/search_match.go` **(new)** — pure ranking function.
  - `internal/tui/search_match_test.go` **(new)** — unit tests for the ranker.
  - `go.mod` / `go.sum` — add `github.com/sahilm/fuzzy`.

- **Related patterns found**:
  - Modes are an `iota` in `model.go:31-39`; modal state is held on `Model` and reset via `closeModal()` at line 936.
  - `Update` dispatches by mode at lines 820-832; modal keys go through `updateModal`.
  - `View()` at line 2069 branches on `m.mode == modeNormal || m.mode == modeGrab` vs `m.modalView()`.
  - `focusTaskByID` at line 1878 already scans every tab's list and switches `activeTab` — reusable for commit.
  - `recomputeLayout` at line 2043 adjusts modal input widths on resize — extend for the search input.
  - Existing style system uses `lipgloss` with precomputed styles; active tasks render green; done tasks already have their own styling in the Done tab.

- **Dependencies identified**:
  - Need to add `github.com/sahilm/fuzzy` (small, well-maintained, returns match positions).
  - Reuse `github.com/charmbracelet/bubbles/textinput` (already a dep, used for other modals).

## Development Approach

- **Testing approach**: Regular (code first, then tests) — matches the established pattern in this codebase where model_test.go is written against finished mode logic.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **Every task MUST include new/updated tests** — project rule (CLAUDE.md): every change needs tests.
- **All tests must pass before starting the next task** — no exceptions.
- Run `go test ./...` and `go vet ./...` after each change.
- Keep scope tight — no multi-term parsing, no tag filters, no negation (YAGNI).
- Update this plan file when scope changes during implementation.

## Testing Strategy

- **Unit tests** for the pure ranker (`rankSearch`): table-driven, cover empty query, title weighting, case-insensitivity, tie-break, limit truncation, done-task inclusion, match-position output.
- **Integration tests** in `model_test.go`: full mode transitions, typing → re-rank, Esc cancel, Enter commit in both `viewSchedule` and `viewTag`, empty-results no-op, grab-mode non-interference.
- **Manual check** (not automated): verify wide (120+ cols) split pane vs narrow (<80 cols) stacked fallback since lipgloss rendering has no snapshot coverage in this project.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep plan in sync with actual work done.

## Solution Overview

Introduce a new `modeSearch` TUI mode with dedicated state (`searchState`) held on `Model`. Opening the search takes a one-shot snapshot of every task via `store.ListAll()` (or equivalent — all open + all done), precomputes lowercased title/body strings, and hands that snapshot to a pure `rankSearch` function on every keystroke.

Rendering builds a full-screen split view: a 1-line input bar, a results list on the left (~40% width), a preview pane on the right showing the focused task's body, a meta line (schedule + status + created date), and a help footer. A width threshold collapses to stacked layout for narrow terminals.

Committing (Enter) switches `activeTab` to the tab containing the target task and calls `focusTaskByID`; a thin `focusTaskAcrossTabs` helper handles the tab-routing based on `viewMode`. Cancel (Esc) resets state and returns control to the prior tab/cursor with no side effects.

The ranker is a pure function so it tests without any Bubble Tea machinery — all TUI concerns live in `search.go`, all matching logic in `search_match.go`.

## Technical Details

### Data structures (added to `model.go`)

```go
const modeSearch mode = iota + 7  // follows modeHelp

type searchDoc struct {
    task    model.Task
    titleLC string
    bodyLC  string
}

type searchResult struct {
    docIdx   int
    score    int
    titleHit []int  // matched rune indices (or nil)
    bodyHit  []int  // matched rune indices (or nil)
}

type searchState struct {
    input    textinput.Model
    haystack []searchDoc
    results  []searchResult
    cursor   int
    // previousTab/previousViewMode are NOT stored — Esc leaves those untouched,
    // Enter overwrites activeTab via focusTaskAcrossTabs.
}
```

`Model` gains one field: `search searchState`.

### Matching pipeline (`rankSearch`)

```go
// rankSearch is pure: given a query, a list of precomputed docs, and a
// truncation limit, it returns ranked matches with highlight positions.
//
//   - Empty query: returns all docs sorted by CreatedAt desc (no scoring,
//     no highlights), truncated to limit.
//   - Non-empty query: matches title and body separately using sahilm/fuzzy
//     against the precomputed lowercased strings. For each doc, takes
//     max(titleScore*2, bodyScore). Drops docs that match neither.
//     Sorts by (score desc, CreatedAt desc), truncates to limit.
func rankSearch(query string, docs []searchDoc, limit int) []searchResult
```

Weighting: sahilm/fuzzy returns a bounded integer score (higher = better). Multiplying title score by 2 keeps the comparison in int space.

Limit: 200. Users cannot realistically navigate more.

### Entry / exit

- `/` in `updateNormal` → `m.openSearch()` which sets `m.mode = modeSearch`, snapshots the haystack, empties input, positions cursor at 0, kicks off an empty-query rank (all tasks by CreatedAt desc).
- `Esc` / `Ctrl+C` → `m.closeSearch()` resets state, returns to `modeNormal`, leaves `activeTab` and vlist cursor unchanged.
- `Enter` → `m.commitSearch()`:
  1. Pick `haystack[results[cursor].docIdx].task`.
  2. In `viewSchedule`: tab index derived from `task.Status == "done"` (Done tab) or the schedule bucket classifier.
  3. In `viewTag`: pick first tag (preferring `active` if `IsActive()`), fall back to untagged tab. Match by tab `tag` field.
  4. `m.activeTab = <target>`; `m.focusTaskByID(task.ID)`.
  5. `m.closeSearch()`.
  6. If no matching tab exists (edge: tag filtered out), fall back to the current tab — the task will simply not be focused, but this is a no-op rather than a crash.

### Layout

```
 [ input bar (1 line)                     count counter (right) ]
 [ results list (~40% W) │ body preview (rest)                  ]
 [ meta line (1 line: schedule · status · created date)         ]
 [ help line (1 line)                                           ]
```

Width threshold: `if m.width < 80` → stacked (input top, results middle, preview bottom, meta + help at bottom). Single branch in `renderSearch`.

Styles:
- Selected row: reversed background.
- Matched chars in results: bold (iterate `titleHit` and bold those rune indices).
- Done tasks: dim + `✓` prefix.
- Active tasks: green (reuse existing `activeStyles`).

### Keybindings inside `modeSearch`

| Key | Action |
|---|---|
| printable / backspace | edit query → re-rank |
| ↓ / ctrl+j / ctrl+n | cursor down |
| ↑ / ctrl+k / ctrl+p | cursor up |
| pgdn / pgup | page cursor |
| enter | commit + close |
| esc / ctrl+c | cancel + close |

Single-letter shortcuts (`d`, `e`, `r`, etc.) are **not** bound — they are absorbed by the text input.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, plan file move.
- **Post-Completion** (no checkboxes): manual wide/narrow terminal verification.

## Implementation Steps

### Task 1: Add `sahilm/fuzzy` dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [x] run `go get github.com/sahilm/fuzzy`
- [x] run `go mod tidy`
- [x] verify `go build ./...` succeeds with the new dep
- [x] (no tests — dependency-only change)

### Task 2: Implement pure ranker `rankSearch`

**Files:**
- Create: `internal/tui/search_match.go`
- Create: `internal/tui/search_match_test.go`

- [x] define `searchDoc` and `searchResult` types (either here or in model.go; pick wherever cleanest — prefer search_match.go so the test file can import just this package file)
- [x] implement `rankSearch(query string, docs []searchDoc, limit int) []searchResult`:
  - empty query branch → copy all docs, sort by CreatedAt desc, truncate
  - non-empty branch → build lowercased title/body slices, run `fuzzy.Find` against each, merge per doc with `max(titleScore*2, bodyScore)`, keep winning highlight set, sort by (score desc, CreatedAt desc), truncate
- [x] write table-driven tests covering:
  - empty query returns all docs in CreatedAt-desc order
  - title 2x weight beats body match (same letters, same positions)
  - case-insensitive (`FIX` matches title `"Fix login bug"`)
  - no matches returns empty slice (not nil panic)
  - tie-break by CreatedAt desc when scores equal
  - limit truncates correctly (len(result) ≤ limit)
  - done tasks appear in results (status is not filtered)
  - match positions (`titleHit` / `bodyHit`) returned correctly
- [x] run `go test ./internal/tui/ -run TestRankSearch` — must pass before Task 3

### Task 3: Add `modeSearch` + `searchState` + entry/exit plumbing

**Files:**
- Modify: `internal/tui/model.go`

- [x] add `modeSearch` constant to the mode iota block
- [x] add `search searchState` field to `Model` struct
- [x] initialize `searchState.input` in the Model constructor (wherever `input`/`tagInput` are initialized today — look for `textinput.New()` calls) with prompt `"> "` and an appropriate placeholder
- [x] route `/` in `updateNormal`: add `case "/":` returning `m.openSearch()`
- [x] ensure `/` is NOT handled in `updateGrab` (verify grab keeps its current key set; add a guard test later in Task 6)
- [x] implement placeholder `openSearch` / `closeSearch` (full bodies come in Task 4; stub them so the model compiles)
- [x] dispatch `modeSearch` in the main `Update` switch at lines 820-832: `case modeSearch: return m.updateSearch(msg)` (stub `updateSearch` returning `(m, nil)` for now)
- [x] hook `View()` at line 2081: when `m.mode == modeSearch`, return `m.renderSearch()` (stub returning `""` for now) bypassing normal tab/list rendering
- [x] write integration test: `/` in modeNormal sets `m.mode == modeSearch`, `len(m.search.haystack) > 0`
- [x] write integration test: `/` in modeGrab does NOT change mode (still modeGrab after key)
- [x] run `go test ./internal/tui/...` — must pass before Task 4

### Task 4: Implement `openSearch` / `closeSearch` / `updateSearch` logic

**Files:**
- Create: `internal/tui/search.go`
- Modify: `internal/tui/model.go` (replace stubs, wire in)

- [x] move the stubs from Task 3 into `search.go`
- [x] `openSearch`:
  - set `m.mode = modeSearch`
  - snapshot haystack: call `m.store.List(...)` with filter that returns all open + all done tasks (or whatever existing store method returns everything; extend store if no such method exists — see ➕ marker below)
  - precompute `titleLC = strings.ToLower(task.Title)` and `bodyLC = strings.ToLower(task.Body)` for each
  - reset `m.search.input.SetValue("")`, `Focus()`, `cursor = 0`
  - call the ranker with empty query to populate initial results
  - call `m.recomputeLayout()`
- [x] `closeSearch`:
  - `m.mode = modeNormal`
  - blur input, clear value, nil out haystack/results (free memory)
- [x] `updateSearch(msg tea.KeyMsg)`:
  - Esc / Ctrl+C → closeSearch
  - Enter → commitSearch (Task 5)
  - Down / Ctrl+J / Ctrl+N → cursor++ clamped to `len(results)-1`
  - Up / Ctrl+K / Ctrl+P → cursor-- clamped to 0
  - PgDn / PgUp → cursor += / -= page height, clamped
  - default: delegate to `m.search.input.Update(msg)`, then if input value changed, re-rank
- [x] extend `recomputeLayout` at line 2057 switch: add `case modeSearch:` setting `m.search.input.Width = m.width - <prompt+counter reservation>`
- [x] write integration test: typing a character updates `m.search.input.Value()` and `m.search.results` changes
- [x] write integration test: Esc returns to `modeNormal` with prior `activeTab` and list cursor unchanged
- [x] write integration test: Down/Up move `m.search.cursor` with proper clamping
- [x] run `go test ./internal/tui/...` — must pass before Task 5

➕ **If `store.List(...)` cannot return all tasks (open + done) in one call**: add a `store.ListAll()` method (thin wrapper over existing list logic) — keep the store change minimal, add a matching store test.

### Task 5: Implement `commitSearch` + `focusTaskAcrossTabs`

**Files:**
- Modify: `internal/tui/search.go`
- Modify: `internal/tui/model.go`

- [x] implement `focusTaskAcrossTabs(task model.Task)`:
  - in `viewSchedule`: map `task.Status == "done"` → Done tab index; otherwise classify `task.Schedule` via `schedule.Classify` (or equivalent existing classifier) to the correct bucket tab; set `m.activeTab`
  - in `viewTag`: choose target tag — if `task.IsActive()`, use the Active tab; else first non-active tag; else untagged tab; find the matching `tagTab` index and set `m.activeTab`
  - call `m.focusTaskByID(task.ID)` — reuses existing logic at line 1878
- [x] implement `commitSearch`:
  - early return if `len(m.search.results) == 0` (no-op)
  - resolve target task from `m.search.haystack[results[cursor].docIdx].task`
  - call `m.closeSearch()` FIRST so mode is normal and vlist rendering returns
  - call `m.focusTaskAcrossTabs(task)`
- [x] write integration test (schedule view): seed tasks with different buckets, open search, select a "Tomorrow" task → commit → assert `activeTab` points to Tomorrow tab and cursor is on that task
- [x] write integration test (tag view): seed tasks with multiple tags, switch to viewTag, open search, select a tagged task → commit → assert correct tag tab is active and cursor is on that task
- [x] write integration test: Enter when `len(results) == 0` is a no-op (still in modeSearch, no crash)
- [x] write integration test: selecting a done task (schedule view) switches to Done tab and focuses it
- [x] run `go test ./internal/tui/...` — must pass before Task 6

### Task 6: Implement `renderSearch` (split pane + stacked fallback)

**Files:**
- Modify: `internal/tui/search.go`

- [x] define lipgloss styles for: selected row, match highlight (bold), dim done rows, `✓` prefix, preview pane border, meta line, help line
- [x] implement `renderSearch(m *Model) string`:
  - input bar: `> ` + `m.search.input.View()` + right-aligned `{visibleCount}/{totalCount}`
  - results list: iterate `m.search.results`, render each task with highlight using `titleHit` rune indices; dim + `✓` prefix for done; green for active; selected row gets reversed background
  - preview pane: title as heading, body as wrapped text (use `lipgloss.NewStyle().Width(w).Render`); show `(no body)` dim if empty
  - meta line: schedule bucket name, status (`open`/`done`), `created {date}`
  - help line: `renderHelpBar` with the in-search key hints
  - width branch: `if m.width < 80` → stacked join; else split horizontal for results+preview
- [x] hook into `View()`: the branch added in Task 3 now returns a real rendering
- [x] manual test (skipped - not automatable): manually verify by running `./monolog` in wide terminal (120+ cols) and narrow terminal (60 cols); no automated test for rendering since the project doesn't use snapshot tests
- [x] add a light assertion test: call `m.renderSearch()` with `m.width = 120, m.height = 40` and assert the returned string is non-empty and contains the input prompt `">"` — guards against nil pointer / panic regressions
- [x] add a second assertion test with `m.width = 60` to exercise the stacked branch
- [x] run `go test ./internal/tui/...` — must pass before Task 7

### Task 7: Help-line hints + existing help modal

**Files:**
- Modify: `internal/tui/model.go` (existing help screen / `helpLine`)

- [x] add `/` to the normal-mode help line hints (wherever the current key set is listed)
- [x] add a brief "Search" section to the `modeHelp` screen listing entry key and in-search keys
- [x] write integration test: the help screen text contains `"/"` and `"search"` (case-insensitive)
- [x] run `go test ./internal/tui/...` — must pass before Task 8

### Task 8: Verify acceptance criteria

- [ ] open search, type, select, press Enter → cursor lands on task in correct tab (schedule view)
- [ ] repeat in tag view
- [ ] Esc cancels without side effects
- [ ] done tasks appear and are visually distinct
- [ ] `/` in grab mode does nothing (grab remains intact)
- [ ] run full test suite: `go test ./...`
- [ ] run linter: `go vet ./...`
- [ ] manually verify wide terminal (120+ cols) — split pane renders
- [ ] manually verify narrow terminal (<80 cols) — stacked layout renders

### Task 9: Update documentation and finalize

**Files:**
- Modify: `CLAUDE.md`
- Move: `docs/plans/20260416-tui-fuzzy-search.md` → `docs/plans/completed/`

- [ ] add a bullet in `CLAUDE.md` under "Key Design Decisions" documenting the search mode (entry key, match scope, matching algorithm)
- [ ] move this plan file into `docs/plans/completed/`

## Post-Completion

*Manual verification — no checkboxes*

- Smoke-test on a real terminal with a backlog of 200+ tasks to confirm ranker latency is unnoticeable on each keystroke (expected: well under 10ms).
- Verify color/style degradation on a non-256-color terminal if available.
- Open an issue if users want multi-term queries, tag filters, or negation — these are deliberately out of scope for v1.
