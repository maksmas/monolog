# Clickable URLs in TUI via OSC 8 Hyperlinks

## Overview

Users frequently paste URLs into task titles and notes, but today those URLs are plain text — to follow one, you copy-paste it into a browser. This plan makes URLs clickable directly in the TUI: Cmd/Ctrl-click on a visible URL opens it in the default browser, with no new keybindings, no schema changes, and no changes to how URLs are entered.

The mechanism is **OSC 8 terminal hyperlinks** — ANSI escape sequences (`\x1b]8;;<URL>\x1b\\<TEXT>\x1b]8;;\x1b\\`) that tell the terminal "this text is a link to URL X". Supported by every modern terminal we care about (iTerm2, WezTerm, Ghostty, Kitty, Alacritty, Windows Terminal, GNOME Terminal). Terminals without support render URLs as plain text — identical to today's behavior, so there is no regression.

Scope is limited to the TUI: detail panel (header title + body/notes) and the task list title column. CLI output (`monolog show`, `monolog ls`) is explicitly out of scope — trivial to add later if desired.

## Context (from discovery)

Files/components involved:
- `internal/display/` — already owns terminal-formatting helpers (`dates.go`, `table.go`). Natural home for the new `links.go` helper. TUI imports display; display never imports TUI.
- `internal/tui/model.go:2607` — `detailPanelView()` renders the right-side panel. Title rendered at line 2624 via `titleStyle.Render(truncateTitle(task.Title, iw))`. Body wrapped at line 2674 via `wrapText(task.Body, iw)`.
- `internal/tui/model.go:270` — `renderListItem()` renders each task row in the list. Title wrapped at line 294 via `wrapText(i.Title(), textWidth)`, then styled per selection/active state and concatenated with a description line.
- `internal/tui/model_test.go` — existing TUI test style uses direct model/view calls and string assertions on rendered output. Good pattern to follow for a smoke test.

Related patterns found:
- Pure-function helpers with table-driven tests dominate `internal/display/` and `internal/model/`. `Linkify` fits this mold.
- `wrapText` and `truncateTitle` always run *before* styling. Our `Linkify` call should run *after* wrapping/truncation — on the final visible fragment — so OSC 8 sequences never dangle across a truncation boundary.

Dependencies identified:
- No new third-party packages. Go stdlib `regexp` and `os` only.
- `lipgloss` styling wraps the linkified string; OSC 8 is zero-width in terms of visual columns, so column math is unaffected.

## Development Approach

- **testing approach**: Regular (code first, then tests) — matches existing codebase style.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - write unit tests for new functions
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and edge-case scenarios (no-URL input, punctuation, multiple URLs)
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change: `go test ./...`
- maintain backward compatibility — no change to stored JSON, CLI flags, or TUI keybindings

## Testing Strategy

- **unit tests**: required for every task.
- **display package tests**: pure table-driven tests in `internal/display/links_test.go`.
- **TUI tests**: one smoke test in `internal/tui/model_test.go` asserting that rendered output contains OSC 8 bytes when a task title contains a URL. Follows existing pattern of building a `Model` and calling render paths directly.
- **manual verification**: at the end, launch the TUI on a task with a URL in its title/body and confirm Cmd/Ctrl-click opens the browser. Cannot be automated.
- **acceptance**: `go test ./...` and `go vet ./...` pass.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

**Single pure helper.** A new function `display.Linkify(s string) string` finds `http://` and `https://` URLs in an arbitrary string and wraps each match in an OSC 8 hyperlink escape. Non-URL text passes through unchanged. The function respects `MONOLOG_NO_LINKS=1` as a no-op escape hatch in case some terminal misbehaves.

**Three integration points** in the TUI — all apply `Linkify` to the final visible text (post-wrap, post-truncate), never before:

1. Detail panel title header (`detailPanelView`, line 2624): wrap `truncateTitle(task.Title, iw)` with `display.Linkify` before passing to `titleStyle.Render`.
2. Detail panel body (`detailPanelView`, line 2674): apply `display.Linkify` to each element of `bodyLines` after `wrapText`.
3. Task list row title (`renderListItem`, line 294): apply `display.Linkify` to each element of `titleLines` after `wrapText`, before the bullet/indent prefix is added.

**Truncation safety.** By linkifying on already-wrapped/truncated strings, the OSC 8 opener and closer always bracket a complete fragment — there is no path where a truncation cut can strand an opener without a closer. The `<URL>` target in the escape is the *original* full URL even when the visible text is shorter, so a truncated-but-clickable URL still navigates to the right page.

**What does not change:** no new keybindings, no schema/model changes, no YAML-editor changes (editing still sees raw text, which is correct), no search overlay or tag-view changes, no CLI output changes.

## Technical Details

**OSC 8 format** (used by `Linkify`):
```
\x1b]8;;<URL>\x1b\\<TEXT>\x1b]8;;\x1b\\
```
- `\x1b]8;;` opens the hyperlink attribute with an empty params section.
- `<URL>` is the target (same as `<TEXT>` in our case — we link the URL to itself).
- `\x1b\\` (ST terminator) closes the escape. We prefer ST over BEL (`\x07`) for slightly wider terminal compatibility.
- The closer `\x1b]8;;\x1b\\` has an empty URL, which ends the hyperlink.

**URL regex:**
```go
var urlRE = regexp.MustCompile(`https?://\S+`)
```
After each match, strip trailing `.`, `,`, `;`, `:`, `!`, `?`, `)` characters so links don't eat sentence punctuation. Example: `"See https://example.com/foo."` → link wraps `https://example.com/foo`, the trailing `.` stays outside the link.

**Escape hatch:**
```go
if os.Getenv("MONOLOG_NO_LINKS") == "1" {
    return s
}
```
Single env read at the top of `Linkify`. Cheap; only active when the user opts out.

**Zero-width property.** OSC 8 escapes consume no visual columns. `lipgloss` width calculations using the ANSI-aware tooling in `github.com/charmbracelet/x/ansi` (transitively pulled in by lipgloss) ignore them. Styling applied by `lipgloss.Style.Render` wraps the already-linkified string — foreground colors, bold, etc. compose with the hyperlink attribute cleanly because every terminal that supports OSC 8 treats it as orthogonal to SGR styling.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, and the final move of this plan to `docs/plans/completed/`.
- **Post-Completion** (no checkboxes): one-time manual verification in the user's terminal that clicking a rendered URL opens the browser — cannot be automated.

## Implementation Steps

### Task 1: Add `display.Linkify` helper with unit tests

**Files:**
- Create: `internal/display/links.go`
- Create: `internal/display/links_test.go`

- [x] create `internal/display/links.go` with `Linkify(s string) string`
- [x] implement URL regex (`https?://\S+`) and trailing-punctuation stripping (`.,;:!?)`)
- [x] wrap each URL match in OSC 8: `\x1b]8;;<URL>\x1b\\<URL>\x1b]8;;\x1b\\`
- [x] respect `MONOLOG_NO_LINKS=1` env (no-op short-circuit at the top of the function)
- [x] write tests for: no URLs, single `https://` URL, single `http://` URL, multiple URLs in one string, URL at end of string, URL followed by `.`, URL followed by `)`, URL followed by `,`, URL inside surrounding text
- [x] write test for `MONOLOG_NO_LINKS=1` — input returned unchanged (use `t.Setenv`)
- [x] write test that the OSC 8 closer appears exactly once per URL (no double-wrapping)
- [x] run `go test ./internal/display/` — must pass before next task
- [x] run `go vet ./...` — must pass before next task

### Task 2: Wire `Linkify` into the detail panel

**Files:**
- Modify: `internal/tui/model.go` (inside `detailPanelView`, around lines 2624 and 2674)
- Modify: `internal/tui/model_test.go`

- [x] at line ~2624, wrap the truncated title with `display.Linkify` before passing to `titleStyle.Render`
- [x] at line ~2674, apply `display.Linkify` to each element of `bodyLines` after `wrapText` (loop, or a small helper local to the function)
- [x] add TUI test: render `detailPanelView()` for a task whose `Body` contains `"https://example.com"` and assert the rendered string contains `"\x1b]8;;https://example.com"`
- [x] add TUI test: render `detailPanelView()` for a task whose `Title` is `"Fix https://example.com/bug"` and assert the header section contains an OSC 8 opener with the URL
- [x] add TUI test: with `t.Setenv("MONOLOG_NO_LINKS", "1")`, confirm the rendered string does NOT contain `"\x1b]8;;"`
- [x] run `go test ./...` — must pass before next task
- [x] run `go vet ./...` — must pass before next task

### Task 3: Wire `Linkify` into the task list row

**Files:**
- Modify: `internal/tui/model.go` (inside `renderListItem`, around line 294)
- Modify: `internal/tui/model_test.go`

- [ ] at line ~294, after `titleLines := wrapText(i.Title(), textWidth)`, apply `display.Linkify` to each element of `titleLines` before the bullet/indent prefix is added
- [ ] do NOT apply `Linkify` to separator items (the early return at `i.isSeparator` already skips the main path — confirm still correct)
- [ ] do NOT apply `Linkify` to the description line — descriptions contain metadata only, no URLs
- [ ] add TUI test: call `renderListItem` with an `item` whose `task.Title` contains `"https://example.com"` and assert the returned string contains `"\x1b]8;;https://example.com"`
- [ ] add TUI test: title that would be truncated by `wrapText` — confirm the linkified output still contains a closing `"\x1b]8;;\x1b\\"` (no dangling opener)
- [ ] add TUI test: active-task styled row with a URL title — confirm the output contains both the active-color SGR and the OSC 8 opener (styling + link compose)
- [ ] run `go test ./...` — must pass before next task
- [ ] run `go vet ./...` — must pass before next task

### Task 4: Verify acceptance criteria

- [ ] verify URLs in the TUI detail panel body are clickable (visual check — run `./monolog` against a repo with a task whose body contains `https://` and Cmd/Ctrl-click)
- [ ] verify URLs in the detail panel title header are clickable
- [ ] verify URLs in the task list title column are clickable
- [ ] verify `MONOLOG_NO_LINKS=1 ./monolog` renders URLs as plain (non-clickable) text
- [ ] verify existing keybindings and behaviors are unchanged
- [ ] run full test suite: `go test ./...`
- [ ] run `go vet ./...`

### Task 5: Update documentation and move plan to completed

- [ ] update `CLAUDE.md` with a short bullet under Key Design Decisions describing clickable URLs via OSC 8 and the `MONOLOG_NO_LINKS` escape hatch
- [ ] move this plan to `docs/plans/completed/20260419-clickable-urls-osc8.md`

## Post-Completion

*Items requiring manual intervention or external systems — informational only*

**Manual verification** (cannot be automated):
- In the user's actual terminal (iTerm2 / Ghostty / etc.), visually confirm that Cmd-click or Ctrl-click on a URL in a task title opens the default browser.
- Confirm the same in the detail panel body for URLs inside note entries.
- Confirm that a task title with no URL renders identically to today (visual diff check).

**Known limitations** (not blockers, but worth noting for future work):
- CLI output (`monolog show`, `monolog ls`) remains plain text. Users who want clickable URLs from the CLI can add `display.Linkify` calls in those command render paths in a follow-up.
- Fuzzy-search overlay results are not linkified. Low value — users generally press Enter to jump into the task, where the detail panel handles URLs.
- Terminals without OSC 8 support (rare today) continue to display URLs as plain text. This is the documented fallback, not a bug.
