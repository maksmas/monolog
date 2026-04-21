# TUI Settings Screen

## Overview

Add an in-TUI settings modal accessible via `,` in normal mode. The modal lets users change the date format and color theme with arrow-key navigation. Changes are saved immediately to `config.json` and applied live (theme change takes effect instantly; date format takes effect from that point forward in the running session and on next launch for CLI commands).

Problem solved: currently theme and date format are only configurable by manually editing `config.json`. This gives users a discoverable, keyboard-driven way to change them.

Integrates with the existing modal pattern (`modeHelp`, `modalBox`) and `config.go`'s persistence layer.

## Context (from discovery)

- Files involved: `internal/config/config.go`, `internal/tui/model.go`, `internal/tui/theme.go`, `internal/git/git.go`, `cmd/helpers.go`
- Patterns: `modeHelp` → add `modeSettings`; `helpModalContent()` + `modalBox()` → add `settingsModalContent()`; `buildStyles(theme)` already takes a `Theme` arg, so re-calling it on theme change is a one-liner
- `config.Theme()` currently reads config.json lazily on every call; will be replaced by a `Load()`-then-package-var pattern for consistency with `dateFormat`
- Only one date format exists today (`02-01-2006`); adding `01-02-2006` (MM-DD-YYYY) and `2006-01-02` (YYYY-MM-DD)
- `s` key is taken (sync); using `,` for settings (VS Code convention, not taken in normal mode)

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Testing Strategy

- **Unit tests**: `config_test.go` for Load/Save/Set*/AllFormats; `model_test.go` for settings mode state transitions
- No e2e tests — TUI tests use state-based assertions, not terminal rendering

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Solution Overview

1. Expand `config.go`: add two more date formats, extract `configPath()` helper, add `Load()` / `SetDateFormat()` / `SetTheme()` / `Save()` / `AllFormats()`.
2. Call `config.Load(repoPath)` in `cmd/helpers.go#openStore` so all commands (TUI + CLI) respect the stored format on startup.
3. Add `modeSettings` to the TUI with cursor-driven row selection and left/right value cycling. Save applies changes live and writes `config.json`.

## Technical Details

### config.json additions

`"date_format"` key written by `git.Init` and by `config.Save()`:

```json
{
  "default_schedule": "today",
  "editor": "$EDITOR",
  "theme": "default",
  "date_format": "02-01-2006"
}
```

### New config.go API

```go
// FormatInfo describes one supported date format for UI display.
type FormatInfo struct {
    Layout string // Go layout string (e.g. "02-01-2006")
    Label  string // Human-readable label (e.g. "DD-MM-YYYY")
}

func Load(monologDir string) error          // reads file, sets package-level vars
func SetDateFormat(layout string) error     // validates + sets dateFormat var
func SetTheme(name string)                  // sets themeVar
func Save(monologDir string) error          // read-modify-write config.json
func AllFormats() []FormatInfo              // ordered list of supported formats
```

`Theme()` is updated to read `themeVar` instead of re-reading the file each call.

### Settings modal state (added to Model)

```go
settingsCursor   int    // 0 = date format row, 1 = theme row
settingsFmt      string // in-flight layout string (not yet saved)
settingsTheme    string // in-flight theme name (not yet saved)
```

### Settings rows

| Row | Values (cycle with ←/→) |
|-----|--------------------------|
| Date format | DD-MM-YYYY → MM-DD-YYYY → YYYY-MM-DD → (wraps) |
| Theme | default → dracula → (wraps) |

## What Goes Where

- **Implementation Steps**: all code changes, tests, docs
- **Post-Completion**: manual smoke-test in a real terminal to verify live theme swap

## Implementation Steps

### Task 1: Expand config.go with multiple formats and persistence

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] Add `FormatInfo` exported struct (Layout + Label fields)
- [ ] Add `01-02-2006` (MM-DD-YYYY) and `2006-01-02` (YYYY-MM-DD) entries to `supported` map
- [ ] Extract `configPath(monologDir string) string` helper; refactor `Theme()` to use it
- [ ] Add `themeVar string` package-level var (default `""`)
- [ ] Update `Theme()` to read `themeVar` instead of re-reading the file; env var override stays
- [ ] Add `Load(monologDir string) error`: reads config.json, sets `dateFormat` and `themeVar` (unknown values silently ignored, file-absent is not an error)
- [ ] Add `SetDateFormat(layout string) error`: validates against `supported`, sets `dateFormat`
- [ ] Add `SetTheme(name string)`: sets `themeVar` (no validation — unknown names fall back in `Theme()` callers)
- [ ] Add `Save(monologDir string) error`: reads existing JSON into `map[string]any`, merges `"theme"` and `"date_format"` keys, writes back (preserves unknown keys like `"default_schedule"`)
- [ ] Add `AllFormats() []FormatInfo`: returns `supported` entries in a stable order (DD-MM-YYYY, MM-DD-YYYY, YYYY-MM-DD)
- [ ] Write tests: `Load` sets vars from file; `Load` with missing file is a no-op; `SetDateFormat` rejects unknown layouts; `Save` round-trips; `AllFormats` returns all three entries
- [ ] Run tests — must pass before task 2

### Task 2: Wire config.Load at startup

**Files:**
- Modify: `cmd/helpers.go`
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`
- Modify: `internal/tui/model.go` (remove any in-tui redundant re-reads if applicable)

- [ ] In `cmd/helpers.go#openStore`: after resolving `repoPath`, call `config.Load(repoPath)`; log or ignore error (missing config is non-fatal)
- [ ] In `internal/git/git.go#Init`: add `"date_format": "02-01-2006"` to the initial `config.json` template
- [ ] Update `internal/git/git_test.go` to assert `date_format` key exists in config.json after `Init`
- [ ] Write tests: `openStore` picks up a config.json date format (use `t.Setenv("MONOLOG_DIR", ...)` pattern to point at a temp dir with a custom config.json)
- [ ] Run tests — must pass before task 3

### Task 3: Add settings modal UI

**Files:**
- Modify: `internal/tui/model.go`

- [ ] Add `modeSettings mode` constant (after `modeSearch`)
- [ ] Add `settingsCursor int`, `settingsFmt string`, `settingsTheme string` fields to `Model` struct
- [ ] Add `openSettings()` method: captures `config.DateFormat()` and `config.Theme()` into in-flight vars, sets `m.mode = modeSettings`, resets cursor to 0
- [ ] Add `,` key case in `updateNormal` that calls `openSettings()` and returns
- [ ] Add `updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd)`:
  - `up`/`k`: decrement cursor (min 0)
  - `down`/`j`: increment cursor (max 1)
  - `right`/`l`: cycle value forward on focused row
  - `left`/`h`: cycle value backward on focused row
  - `enter`: save (Task 4 wires this)
  - `esc`: discard in-flight state, return to `modeNormal`
- [ ] Add `settingsModalContent() string` renderer:
  - Two rows: "Date format" and "Theme"
  - Focused row rendered with `helpKeyStyle`; current value in `[brackets]`
  - Footer hint: `↑↓ navigate  ←→ change  Enter save  Esc cancel`
- [ ] Add `modeSettings` case in `modalView()` calling `settingsModalContent()`
- [ ] Add `modeSettings` case in `updateModel` dispatching to `updateSettings`
- [ ] Add `modeSettings` case in status bar rendering: show footer hint text
- [ ] Add `,: settings` to the normal-mode help bar entry list
- [ ] Write tests: `openSettings` captures current values; cursor wraps at 0 and 1; cycling date format cycles through the three formats; Esc restores mode to normal without calling Save
- [ ] Run tests — must pass before task 4

### Task 4: Wire save and live apply

**Files:**
- Modify: `internal/tui/model.go`

- [ ] On `enter` in `updateSettings`: call `config.SetDateFormat(m.settingsFmt)`, call `config.SetTheme(m.settingsTheme)`, call `config.Save(m.repoPath)`
- [ ] On successful save: rebuild `m.styles = buildStyles(resolvedTheme)` and `m.theme = resolvedTheme` (look up `themes[config.Theme()]`, fall back to `defaultTheme`); set `m.mode = modeNormal`; set `m.statusMsg = "Settings saved"`
- [ ] On save error: set `m.statusMsg = "settings: " + err.Error()`, stay in `modeSettings`
- [ ] Write tests: entering settings, cycling both rows, pressing Enter calls `config.Save` equivalent and transitions mode; theme is updated in `m.theme`; save error stays in settings mode with status message
- [ ] Run tests — must pass before task 5

### Task 5: Verify acceptance criteria

- [ ] Verify `,` opens settings modal from normal mode
- [ ] Verify ↑↓ moves between date format and theme rows
- [ ] Verify ←→ cycles each row through all options, wrapping
- [ ] Verify Enter saves to config.json and applies theme live
- [ ] Verify Esc cancels with no change to config.json
- [ ] Verify `config.Load` is called in `openStore` so CLI commands also use saved format
- [ ] Run full test suite: `go test ./...`
- [ ] Run `go vet ./...`

### Task 6: [Final] Update documentation

**Files:**
- Modify: `CLAUDE.md`
- Move: `docs/plans/20260421-tui-settings-screen.md` → `docs/plans/completed/`

- [ ] Update CLAUDE.md: extend the TUI color themes bullet to mention the settings modal (`,` key, dateformat + theme rows, `config.Load` at startup)
- [ ] Note `FormatInfo` and new config.go API in the config section of CLAUDE.md
- [ ] Move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- Open TUI, press `,` — settings modal appears
- Change theme to dracula, press Enter — TUI recolors immediately
- Relaunch — dracula theme persists
- Change date format to MM-DD-YYYY, press Enter — task schedules render in new format
- Run `monolog ls` — dates appear in the new format (via `config.Load` in `openStore`)
- Press `,`, Esc — config.json unchanged
