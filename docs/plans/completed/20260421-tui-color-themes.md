# TUI Color Theme Support

## Overview
Add named color theme support to the TUI so users can switch between "default" (current palette) and "dracula". Each theme defines light+dark variants via `lipgloss.AdaptiveColor`. Theme is read from the existing `<MONOLOG_DIR>/.monolog/config.json` with a `MONOLOG_THEME` env-var override.

## Context (from discovery)
- `internal/tui/tui.go` — 18 package-level `var` style definitions using hardcoded ANSI codes (lines 30–118)
- `internal/tui/model.go` — `initStyles()` at lines 240–266 using hardcoded AdaptiveColor pairs; 4 inline color literals at lines 2440, 2725, 2733, 3022
- `internal/config/config.go` — package-level var, no file reading; adding `Theme()` here keeps callers unchanged
- `internal/git/git.go` — `git.Init` writes `<MONOLOG_DIR>/.monolog/config.json`; needs `"theme": "default"` added to the template
- No existing theme abstraction; styles are scattered across two files

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include tests before moving on**
- **CRITICAL: all tests must pass before starting next task**

## Solution Overview
1. New `internal/tui/theme.go` — `Theme` struct with `lipgloss.AdaptiveColor` fields per color role; `defaultTheme`, `draculaTheme`, registry, `ThemeByName`
2. `internal/config/config.go` — `Theme()` reads `<MONOLOG_DIR>/.monolog/config.json` (JSON, stdlib); env var override
3. `internal/tui/tui.go` — 18 package-level style vars extracted into a `tuiStyles` struct, built by `buildStyles(Theme)`
4. `internal/tui/model.go` — `initStyles(Theme)` replaces hardcoded colors; 4 inline literals replaced via `m.theme.*`; `Model` gains `theme` + `styles` fields

## Technical Details

### Theme struct fields (all `lipgloss.AdaptiveColor`)
Chrome: `ActiveBorder`, `ModalBorder`, `Hotkey`, `ActiveTab`, `TabFg`, `Borders`
Task list: `NormalText`, `SelectedText`, `DimText`, `GrabText`, `ActiveNormal`, `ActiveSelected`
Search: `SearchDone`, `SearchActive`, `SearchCount`, `SearchMeta`, `SearchPreviewBorder`, `SearchPreviewDim`

### Default theme (current colors)
- `ActiveBorder`: `"28"` (dark teal) — both light/dark
- `ModalBorder`: `"62"` (blue) — both
- `Hotkey`: `"9"` (light red) — both
- `ActiveTab bg`: `"62"`, fg `"231"` — both (tab bg/fg are separate roles)
- `Borders` / `DimText`: `"240"`
- `NormalText`: `{Light: "#1a1a1a", Dark: "#dddddd"}`
- `SelectedText`: `{Light: "#EE6FF8", Dark: "#EE6FF8"}`
- `DimText`: `{Light: "#A49FA5", Dark: "#777777"}`
- `GrabText`: `{Light: "#D97706", Dark: "#FFB454"}`
- `ActiveNormal`: `{Light: "#16A34A", Dark: "#22C55E"}`
- `ActiveSelected`: `{Light: "#15803D", Dark: "#4ADE80"}`

### Dracula theme
- `ActiveBorder`: `{Dark: "#bd93f9", Light: "#6272a4"}` (purple)
- `ModalBorder`: `{Dark: "#6272a4", Light: "#44475a"}` (comment blue)
- `Hotkey`: `{Dark: "#ff5555", Light: "#ff5555"}` (red)
- `ActiveTab bg`: `"#44475a"`, fg `"#f8f8f2"` — current line + foreground
- `Borders`: `{Dark: "#6272a4", Light: "#44475a"}`
- `NormalText`: `{Dark: "#f8f8f2", Light: "#282a36"}`
- `SelectedText`: `{Dark: "#ff79c6", Light: "#ff79c6"}` (pink)
- `DimText`: `{Dark: "#6272a4", Light: "#44475a"}` (comment)
- `GrabText`: `{Dark: "#ffb86c", Light: "#ffb86c"}` (orange)
- `ActiveNormal`: `{Dark: "#50fa7b", Light: "#50fa7b"}` (green)
- `ActiveSelected`: `{Dark: "#69ff94", Light: "#69ff94"}` (bright green)

### Config JSON
`<MONOLOG_DIR>/.monolog/config.json` gains a `"theme"` key. Unknown theme names fall back to `"default"` silently. `MONOLOG_THEME` env var overrides the file.

## What Goes Where
- **Implementation Steps**: code changes + tests
- **Post-Completion**: manual visual verification in both themes

## Implementation Steps

### Task 1: Theme struct, built-in themes, registry

**Files:**
- Create: `internal/tui/theme.go`
- Create: `internal/tui/theme_test.go`

- [x] define `Theme` struct in `internal/tui/theme.go` with `lipgloss.AdaptiveColor` fields for all color roles
- [x] define `defaultTheme` var with current palette extracted from `tui.go` and `model.go`
- [x] define `draculaTheme` var with dracula colors
- [x] define `themes = map[string]Theme{"default": defaultTheme, "dracula": draculaTheme}`
- [x] implement `ThemeByName(name string) (Theme, bool)`
- [x] write tests: `ThemeByName` finds known themes, returns false for unknown
- [x] write tests: `defaultTheme` and `draculaTheme` have non-zero `NormalText` fields (smoke test)
- [x] run tests — must pass before task 2

### Task 2: Config Theme() accessor

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/git/git.go`
- Modify: `internal/config/config_test.go` (create if absent)

- [x] add `Theme() string` to `internal/config/config.go` — reads `<MONOLOG_DIR>/.monolog/config.json`, returns `"theme"` field; defaults to `"default"` if file absent or key missing
- [x] `MONOLOG_THEME` env var takes precedence over file
- [x] use `os.Getenv("MONOLOG_DIR")` + `os.UserHomeDir()` fallback (same logic as `monologDir()` in `cmd/init.go`) to locate config.json
- [x] update `git.Init` config.json template in `internal/git/git.go` to include `"theme": "default"`
- [x] write tests for `Theme()`: returns `"default"` when no file, returns value from file, env var overrides
- [x] run tests — must pass before task 3

### Task 3: Refactor tui.go styles into a theme-driven struct

**Files:**
- Modify: `internal/tui/tui.go`
- Modify: `internal/tui/model.go` (minor — usage sites)

- [x] define `tuiStyles` struct in `tui.go` holding all fields previously in the `var` block (tabBorder, tabStyle, activeTabStyle, statusStyle, helpStyle, separatorStyle, helpKeyStyle, helpTextStyle, suggestionBoldStyle, suggestionDimStyle, searchSelectedStyle, searchMatchStyle, searchDoneStyle, searchActiveStyle, searchCountStyle, searchMetaStyle, searchPreviewBorderStyle, searchResultsStyle, searchPreviewTitleStyle, searchPreviewDimStyle)
- [x] implement `buildStyles(t Theme) tuiStyles` that constructs all styles from theme fields
- [x] remove the 18-entry `var` block from `tui.go`
- [x] add `styles tuiStyles` field to `Model` struct in `model.go`
- [x] update `newModel()` to call `buildStyles(theme)` and store in `m.styles`
- [x] update all render sites in `model.go` and `search.go` that reference the old package-level vars to use `m.styles.*` instead
- [x] run `go build ./...` to catch missed references
- [x] write tests: `buildStyles(defaultTheme)` returns a styles struct where `helpKeyStyle` has non-nil foreground (smoke-tests the builder path)
- [x] run tests — must pass before task 4

### Task 4: Wire theme through Model and fix inline color literals

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/tui.go` (Run signature and newModel)

- [x] add `theme Theme` field to `Model`
- [x] update `newModel()` to call `config.Theme()` → `ThemeByName` → fallback to `defaultTheme`; pass theme to `initStyles` and `buildStyles`
- [x] update `initStyles()` signature to `initStyles(t Theme)` and replace hardcoded `AdaptiveColor` literals with `t.*` fields
- [x] replace inline color at line 2440 (`"28"`) with `m.theme.ActiveBorder`
- [x] replace inline color at line 2725 (`"240"`) with `m.theme.Borders`
- [x] replace inline color at line 2733 (`"62"`) with `m.theme.ModalBorder`
- [x] replace inline color at line 3022 (`"62"`) with `m.theme.ModalBorder`
- [x] run `go vet ./...` — zero warnings
- [x] run tests — must pass before task 5

### Task 5: Verify acceptance criteria

- [x] verify `ThemeByName("default")` and `ThemeByName("dracula")` both return `ok=true`
- [x] verify unknown theme name silently falls back to default in `newModel()`
- [x] verify `config.Theme()` returns `"default"` on a fresh repo with no config changes
- [x] run full test suite: `go test ./...`
- [x] run `go vet ./...`

### Task 6: [Final] Update documentation

- [x] update `CLAUDE.md` — add theme support to Key Design Decisions
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual visual verification:**
- Run `monolog` with no config → default theme renders correctly
- Set `"theme": "dracula"` in `~/.monolog/.monolog/config.json` → dracula colors appear
- Set `MONOLOG_THEME=dracula` → env var overrides file
- Test in both light and dark terminal backgrounds to confirm `AdaptiveColor` switching
