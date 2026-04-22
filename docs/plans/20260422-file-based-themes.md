# File-based TUI Color Themes

## Overview

Let monolog users author custom TUI color themes as JSON files on disk. Files
in `<MONOLOG_DIR>/.monolog/themes/*.json` are discovered at startup and
merged into the package-level `themes` map alongside the built-in `default`
and `dracula` themes. Reload is edit-and-restart — no file watcher.

A single `example.json` (a full dump of `defaultTheme`) is written to the
themes directory the first time it is created, so users have a copy-and-edit
starting point without authoring 19 fields from scratch.

The settings modal requires no UI changes: `settingsThemeNames()` already
sorts the `themes` map alphabetically, so user themes appear in the `,`
cycle automatically.

## Context (from discovery)

- **Existing theme code**: `internal/tui/theme.go` — defines `Theme` struct
  (19 `lipgloss.AdaptiveColor` fields), `defaultTheme`, `draculaTheme`, and
  the package-level `themes` map.
- **Style construction**: `internal/tui/tui.go:buildStyles(Theme)` —
  already theme-driven; no changes needed.
- **Theme selection**: `internal/tui/model.go:429` — `activeTheme, ok :=
  themes[config.Theme()]`; `if !ok { activeTheme = defaultTheme }`. The
  fallback path already exists but is silent. We extend it to set a status
  message.
- **TUI entrypoint**: `internal/tui/tui.go:Run(s, repoPath, opts)` — ideal
  place to call `initThemes(repoPath)` + `bootstrapExampleTheme(repoPath)`
  before constructing the Model.
- **Settings modal**: `internal/tui/model.go:3127 settingsThemeNames()` —
  ranges the `themes` map and sorts; user themes auto-appear once loaded.
- **Config loader**: `internal/config/config.go:Theme()` — returns the theme
  name from `MONOLOG_THEME` env or `config.json`, falling back to
  `"default"`. Unchanged by this plan.
- **Plan file convention**: existing plans use `YYYYMMDD-<name>.md`
  (e.g. `20260420-tui-reschedule-relative-dates.md`); this plan follows.
- **Tests idiom**: tests live next to code (`*_test.go`), use
  `t.Setenv`/`t.TempDir()`, and run with `go test ./...`.

## Development Approach

- **Testing approach**: TDD-flavored regular — write loader tests alongside
  each piece of code in the same task, not in a separate pass.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** — tests are not
  optional, they are a required part of the checklist.
  - unit tests for all new functions (loader, bootstrap, round-trip).
  - update the fallback-path test when the status message is added.
  - cover both success and rejection scenarios.
- **CRITICAL: all tests must pass before starting next task**.
- `go test ./...` after each task; `go vet ./...` before the final
  verification task.

## Testing Strategy

- **Unit tests**: required per task (see Development Approach).
- **No e2e tests**: this project has no Playwright/Cypress harness; TUI
  behavior is covered through Model unit tests already in
  `internal/tui/model_test.go` patterns.
- **Test isolation for package-level map**: any test that calls
  `initThemes` must snapshot the global `themes` map and restore it via
  `t.Cleanup`. Add a helper in `theme_load_test.go`.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with `➕` prefix.
- Document blockers with `⚠️` prefix.

## Solution Overview

**Three new pieces** in `internal/tui/theme_load.go`:

1. **Serialization wrapper** — `themeFile` struct with snake_case JSON tags
   (one field per color role) and `colorPair{Light, Dark string}` helper.
   Converts to/from the in-memory `Theme{lipgloss.AdaptiveColor}` via two
   pure functions: `(themeFile).toTheme() (Theme, error)` and
   `themeToFile(Theme) themeFile`.

2. **Loader** — `loadUserThemes(monologDir string) (map[string]Theme,
   []error)`. Scans `<monologDir>/.monolog/themes/*.json`, parses each
   through `themeFile`, rejects empty color strings and missing fields
   (using `json.Decoder.DisallowUnknownFields`'s *inverse* — we allow
   extras but check required fields via a presence bitmap populated during
   unmarshal, see "Missing-field detection" below). Missing dir → empty
   map, nil errors. Returns per-file errors alongside the map.

3. **Init/bootstrap** — two entry points called from `tui.Run`:
   - `initThemes(monologDir string) []error` — loads user themes and
     merges into the package-level `themes` map; rejects collisions with
     built-ins (`default`, `dracula`).
   - `bootstrapExampleTheme(monologDir string) error` — if
     `<monologDir>/.monolog/themes/` does not exist, create it and write
     `example.json` (round-trip through `themeFile` from `defaultTheme`).
     No-op if the directory already exists.

**Missing-field detection**: the clean way is to unmarshal into
`map[string]json.RawMessage` first to check every required key is present
(non-empty raw message), then unmarshal into `themeFile` for typed access.
This keeps the schema definition in one struct while allowing us to enforce
presence separately. Empty `{"light": "", "dark": "..."}` is caught
post-decode by walking the pairs once and returning the first field with
either half empty.

**Fallback status message**: in `newModel`, when `themes[config.Theme()]`
misses, set `m.statusMsg = fmt.Sprintf("theme %q not found, using default",
config.Theme())`. This surfaces automatically on first render because the
status bar already consumes `m.statusMsg`.

## Technical Details

**File schema** (the 19 fields, in the order they appear in the `Theme`
struct):

```json
{
  "active_border":         {"light": "28",      "dark": "28"},
  "modal_border":          {"light": "62",      "dark": "62"},
  "hotkey":                {"light": "9",       "dark": "9"},
  "active_tab_bg":         {"light": "62",      "dark": "62"},
  "active_tab_fg":         {"light": "231",     "dark": "231"},
  "tab_fg":                {"light": "244",     "dark": "244"},
  "borders":               {"light": "240",     "dark": "240"},
  "normal_text":           {"light": "#1a1a1a", "dark": "#dddddd"},
  "selected_text":         {"light": "#EE6FF8", "dark": "#EE6FF8"},
  "dim_text":              {"light": "#A49FA5", "dark": "#777777"},
  "grab_text":             {"light": "#D97706", "dark": "#FFB454"},
  "active_normal":         {"light": "#16A34A", "dark": "#22C55E"},
  "active_selected":       {"light": "#15803D", "dark": "#4ADE80"},
  "search_done":           {"light": "244",     "dark": "244"},
  "search_active":         {"light": "76",      "dark": "76"},
  "search_count":          {"light": "240",     "dark": "240"},
  "search_meta":           {"light": "244",     "dark": "244"},
  "search_preview_border": {"light": "240",     "dark": "240"},
  "search_preview_dim":    {"light": "240",     "dark": "240"}
}
```

**Error wording**:
- `theme "mytheme": invalid JSON: <stdlib-detail>`
- `theme "mytheme": missing field "active_border"`
- `theme "mytheme": empty color for field "active_border"`
- `theme "mytheme": collides with built-in, skipping`

**Stderr output**: `initThemes` and `bootstrapExampleTheme` return errors;
`tui.Run` prints them on stderr with a `monolog:` prefix before calling
`tea.NewProgram`. The alt-screen takes over after that, so the warnings
remain visible after the user quits.

**Runtime fallback site**: `internal/tui/model.go:429-432` gets an extended
body:

```go
activeTheme, ok := themes[config.Theme()]
if !ok {
    activeTheme = defaultTheme
}
// …
m := &Model{ /* … */ }
if !ok {
    m.statusMsg = fmt.Sprintf("theme %q not found, using default",
        config.Theme())
}
```

(Status message has to be set after `m` is constructed; we keep the
original `ok` for that check.)

## What Goes Where

- **Implementation Steps**: all code, tests, and the CLAUDE.md update.
- **Post-Completion**: informational only — no deploy, no external
  systems.

## Implementation Steps

### Task 1: Serialization types and conversion

**Files:**
- Create: `internal/tui/theme_load.go`
- Create: `internal/tui/theme_load_test.go`

- [ ] add `colorPair struct { Light, Dark string }` with `json:"light"` /
      `json:"dark"` tags
- [ ] add `themeFile` struct — one `colorPair` field per color role, all
      19 fields, snake_case json tags in `Theme` struct order
- [ ] add `(themeFile).toTheme() (Theme, error)` — returns the first
      empty-color error encountered (field name in the error)
- [ ] add `themeToFile(Theme) themeFile` — pure conversion in the other
      direction
- [ ] write tests: `TestThemeFile_RoundTrip` — `themeToFile(defaultTheme)`
      → `toTheme()` → deep-equal `defaultTheme`
- [ ] write tests: `TestThemeFile_ToTheme_EmptyLight` and
      `TestThemeFile_ToTheme_EmptyDark` — each returns error naming the
      first-empty field
- [ ] run tests — must pass before task 2

### Task 2: Loader with missing-field and invalid-JSON handling

**Files:**
- Modify: `internal/tui/theme_load.go`
- Modify: `internal/tui/theme_load_test.go`

- [ ] add `loadUserThemes(monologDir string) (map[string]Theme, []error)`:
      - return `(map[string]Theme{}, nil)` if
        `<monologDir>/.monolog/themes/` is absent
      - list the directory; skip entries that aren't `.json` files silently
      - for each `.json` file: read, decode into
        `map[string]json.RawMessage`, check every required key is present,
        then unmarshal into `themeFile`, call `toTheme()`
      - filename (sans `.json`) = theme name
      - errors use the wording from "Technical Details / Error wording"
- [ ] write test: `TestLoadUserThemes_MissingDir` — no dir → empty map,
      nil errors
- [ ] write test: `TestLoadUserThemes_EmptyDir` — empty dir → empty map,
      nil errors
- [ ] write test: `TestLoadUserThemes_Valid` — one complete theme file
      loads with correct `lipgloss.AdaptiveColor` values
- [ ] write test: `TestLoadUserThemes_InvalidJSON` — malformed file
      absent from map, matching error present
- [ ] write test: `TestLoadUserThemes_MissingField` — 18/19 fields →
      skip + error naming missing field
- [ ] write test: `TestLoadUserThemes_EmptyColorString` →
      skip + error naming empty field
- [ ] write test: `TestLoadUserThemes_ExtraFields` — unknown keys
      tolerated, theme still loads
- [ ] write test: `TestLoadUserThemes_IgnoresNonJSON` — `readme.txt`
      alongside `ok.json` → only `ok` loaded, no error
- [ ] run tests — must pass before task 3

### Task 3: initThemes merge with built-in collision rejection

**Files:**
- Modify: `internal/tui/theme_load.go`
- Modify: `internal/tui/theme_load_test.go`

- [ ] add `initThemes(monologDir string) []error`:
      - call `loadUserThemes`
      - for each loaded theme: if name is `default` or `dracula`, skip
        with a collision error; otherwise add to package-level `themes`
        map
      - append per-file load errors to the returned slice
- [ ] add test helper `snapshotThemes(t *testing.T)` in
      `theme_load_test.go` — captures the current `themes` map and
      registers `t.Cleanup` to restore it
- [ ] write test: `TestInitThemes_MergesUserAndBuiltin` — user theme
      appears in `themes` alongside `default` and `dracula`
- [ ] write test: `TestInitThemes_SkipsCollisionWithBuiltin` — user
      `default.json` → error returned, compiled `defaultTheme` unchanged
      in map
- [ ] write test: `TestInitThemes_PropagatesLoadErrors` — invalid file
      plus valid file in the same dir → valid one loaded, invalid one
      reported
- [ ] run tests — must pass before task 4

### Task 4: Example bootstrap

**Files:**
- Modify: `internal/tui/theme_load.go`
- Modify: `internal/tui/theme_load_test.go`

- [ ] add `bootstrapExampleTheme(monologDir string) error`:
      - if `<monologDir>/.monolog/themes/` exists (any `os.Stat` success),
        return nil (no-op, even if `example.json` is missing)
      - otherwise `os.MkdirAll` the themes dir, marshal
        `themeToFile(defaultTheme)` with `json.MarshalIndent(.., "", "
          ")`, write as `example.json`
- [ ] write test: `TestBootstrapExampleTheme_CreatesOnFreshDir` — starts
      with no themes dir → dir + `example.json` exist after call, and
      `loadUserThemes` loads `example` equal to `defaultTheme`
- [ ] write test: `TestBootstrapExampleTheme_NoOpWhenDirExists` —
      pre-create the themes dir (empty, no example); call bootstrap;
      `example.json` must NOT be created
- [ ] write test: `TestBootstrapExampleTheme_NoOpWhenExampleExists` —
      pre-create `example.json` with tweaked content; call bootstrap;
      content unchanged
- [ ] run tests — must pass before task 5

### Task 5: Wire into tui.Run

**Files:**
- Modify: `internal/tui/tui.go`

- [ ] in `Run`, before `newModel`, call `bootstrapExampleTheme(repoPath)`;
      print any error to `os.Stderr` as `monolog: <err>` and continue
- [ ] then call `initThemes(repoPath)`; print each returned error to
      `os.Stderr` with the same prefix; continue regardless
- [ ] tests: no direct test for `Run` (it starts a TTY program), but add a
      smoke test `TestRunWiring_DoesNotPanic` is **out of scope** —
      task-level tests in tasks 2-4 cover the functions called; the
      integration is a 4-line wiring that we inspect-by-reading
- [ ] write test: `TestTuiRun_BootstrapOnFreshRepo` — a lightweight test
      that sets up a tempdir, calls `bootstrapExampleTheme` and
      `initThemes` directly (mirroring what `Run` does), then asserts the
      package-level map now contains `example`. This validates the
      sequencing without needing a TTY.
- [ ] run tests — must pass before task 6

### Task 6: Status message on missing-theme fallback

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] at `internal/tui/model.go:429-432`, keep the existing fallback; add
      after `m` is constructed: `if !ok { m.statusMsg = fmt.Sprintf("theme
      %q not found, using default", config.Theme()) }`
- [ ] write test: `TestNewModel_MissingThemeFallsBack` — set
      `MONOLOG_THEME=nonexistent` via `t.Setenv`, call `newModel` against
      a tempdir-backed store, assert `m.theme == defaultTheme` and
      `m.statusMsg` contains the expected message
- [ ] write test: `TestNewModel_ExistingThemeNoStatus` — default theme
      resolves cleanly, `m.statusMsg` is empty
- [ ] run tests — must pass before task 7

### Task 7: Verify acceptance criteria

- [ ] verify `go test ./...` passes from repo root
- [ ] verify `go vet ./...` passes
- [ ] manually walk the acceptance path:
      fresh tempdir → `monolog` binary starts → `themes/` created →
      `example.json` present → edit a field in `example.json` →
      `config.json` `"theme": "example"` → restart → colors apply
- [ ] verify fallback: rename `example.json` → restart → status line
      shows `theme "example" not found, using default`; restore file →
      restart → colors return, no rewrite of `config.json`
- [ ] verify built-in collision: create `default.json` → restart →
      stderr shows collision warning, in-app `default` theme still works

### Task 8: Documentation and plan archival

**Files:**
- Modify: `CLAUDE.md`
- Move: `docs/plans/20260422-file-based-themes.md` →
        `docs/plans/completed/20260422-file-based-themes.md`

- [ ] update the **TUI color themes** bullet in `CLAUDE.md` — add a
      sentence describing the `<MONOLOG_DIR>/.monolog/themes/*.json` file
      source, the example bootstrap, the built-in collision rule, and the
      missing-theme status-bar fallback. Keep existing content about
      `MONOLOG_THEME` env precedence.
- [ ] move this plan file to `docs/plans/completed/` (create dir if
      needed: `mkdir -p docs/plans/completed`)

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes,
informational only*

**Manual verification**:
- Confirm the example theme renders identically to the built-in `default`
  (selecting `example` should be a visual no-op).
- Try a terminal that does not support truecolor (Linux VT) — hex values
  in the example should degrade gracefully; ANSI-code-only themes should
  look identical.
- Try `MONOLOG_THEME=mytheme` env override with a file-based theme —
  confirm env still takes precedence over `config.json`'s `"theme"`.

**No external system updates**: this is a self-contained TUI change with
no consumers outside monolog.
