# Layout-independent TUI shortcuts (Bubble Tea v2 + kitty keyboard protocol)

## Overview
- With a non-Latin keyboard layout active (macOS Russian ЙЦУКЕН), every letter shortcut in the TUI is dead: bubbletea v1.3.10 delivers the character the layout produced (`d` key → `в`) and `internal/tui` matches on `msg.String()`, so `case "d":` never fires.
- Fix: migrate `internal/tui` to Bubble Tea v2 and enable the kitty keyboard protocol, which reports the PC-101 base-layout key (`Key.BaseCode`) alongside the typed text. A pure `commandKey` helper rewrites a key to its base-layout equivalent **only in command contexts**, so shortcuts work on any layout while text inputs still receive Cyrillic.
- Target terminal: kitty (the user's only terminal). Terminals without the protocol keep today's behavior (`BaseCode == 0` → no rewrite). No Cyrillic fallback table (YAGNI).
- Delivered as a commit series (two-part v2 port, paste routing, `commandKey`, wiring, verification, docs, review fixes); squash at merge if two commits are wanted. Review follow-up: a typed Latin letter now wins over the base-layout key (`preferTypedLatin`), so Latin layouts that move letters (Dvorak/AZERTY/QWERTZ) keep their letter shortcuts and chords; ASCII punctuation typed on a PC-101 letter key also wins (Dvorak `'` on physical `q` no longer quits), at the cost that Greek/Hebrew physical `q` (typing `;`/`/`) does not quit (ctrl+c does); punctuation moved onto another punctuation key still follows the physical key (Dvorak `/` reads `[`), because the Russian `/` key types `.`; shifted keys that type their own base key keep it (AZERTY shift+`&` → `1`); `commandKey` rewrites only `Text`, and the CapsLock-as-shift rule was dropped (the probe showed it never arrives with Cyrillic).

## Context (from discovery)
- All bubbletea/bubbles/lipgloss imports are confined to `internal/tui`. (`cmd/pager.go` imports `charmbracelet/x/term`, which is not part of this migration and stays as is.)
- Key dispatch: `Update` routes `tea.KeyMsg` by mode (`internal/tui/model.go:1210`); `updateNormal` (`:1231`, clears `statusMsg` at `:1232`, detail-panel early-return at `:1238-1268`, main switch `:1270`), `updateGrab` (`:2321`), `updateModal` (`:1421`, dispatches to reschedule/retag/add/`updateConfirmDelete`), `updateConfirmDelete` (`:2138`, `y`/`Y` at `:2139`, anything else cancels at `:2148-2150`), `updateSettings` (`:3606`), help dismiss (`modeHelp` in `Update`: any key closes), `updateSearch` (`internal/tui/search.go:66`).
- Key-API sites to port (compiler-driven; this list is for orientation, not exhaustive): ~37 uses of `msg.Type` / `msg.Runes` / `msg.Alt` / `tea.Key*` in `model.go` + `search.go`; v1 ctrl constants `tea.KeyCtrlC` (`search.go:68`) and `KeyCtrlN/P/C/Z/Y` in tests (`model_test.go:8843`, `:8849`, `:8903`, `toKeyMsg`); `tea.KeyEnter` code checks at `model.go:1244`, `:1684`, `:1783`, `:1896`, `:2336`, `search.go:71`.
- Program/view: `tea.NewProgram(m, tea.WithAltScreen())` at `internal/tui/tui.go:59`; `View() string` at `model.go:3348` with an early `return m.renderSearch()` at `:3350-3352`; `Init()` at `model.go:988`; `tea.ExecProcess` for the `e` key / `$EDITOR` at `model.go:2280`.
- bubbles: `list` is used only for `list.Item` / `list.DefaultItemStyles` (fields unchanged in v2; rendering is the custom `vlist.go`); `textinput` (`model.go:415-432`; `.Width =` at `:1668`, `:1762`, `:1817-1818`, `:3198-3209`; `textinput.Blink` is unchanged in v2); `textarea` (`model.go:437-465`, `FocusedStyle`/`BlurredStyle` overrides at `:448-451`, `:461-464`; `ta.KeyMap.InsertNewline.SetKeys` at `:444`, `:460` is unchanged — monolog never references `DefaultKeyMap`).
- Colors: `Theme` (`internal/tui/theme.go`) has 19 `lipgloss.AdaptiveColor` fields, 62 uses in total; `theme_load.go` converts the on-disk `colorPair{Light,Dark}` to/from `AdaptiveColor` (`:89`, `:121`); `buildStyles(Theme)` (`tui.go:136`), `initStyles(Theme)` (`model.go:304`), `applySettings` (`model.go:3664`) rebuilds styles on theme switch.
- Tests: keys are sent through `key()` / `typeString` / `toKeyMsg()` (`internal/tui/model_test.go:55-75`) plus ~19 `tea.KeyMsg{}` literals; 9 `.View()` calls; `.Width =` at `model_test.go:8924`, `:8932`. The suite uses lipgloss v1's `SetColorProfile`/`DefaultRenderer` via `muesli/termenv` (~26 sites in `model_test.go`, e.g. `:760`, `:1652`, `:2031`, `:2051`, `:2595`, `:4596`, `:9271`, `:9305`, `:9779`; 6 in `theme_test.go`, e.g. `:77`, `:103`), relies on the implicit Ascii profile under `go test` (comment at `model_test.go:757-759`), and type-asserts `lipgloss.AdaptiveColor` (`theme_test.go:57`).
- Verified in v2 sources: `charm.land/bubbletea/v2` v2.1.1, `charm.land/bubbles/v2` v2.2.1, `charm.land/lipgloss/v2` v2.0.6.
  - `Key.BaseCode` is filled from the kitty CSI-u alternate-key field.
  - The decoder's case-2 fallthrough also sets `ShiftedCode = BaseCode` (uv `decoder.go:1421-1441`), so `ShiftedCode` is useless for us.
  - The decoder clears `Text` when ctrl/alt (anything above shift) is held (`decoder.go:1449-1453`).
  - Without associated text, `Text` falls back to `string(ShiftedCode)`, i.e. lowercase Latin (`decoder.go:1513-1518`), so `ReportAssociatedText` is load-bearing for typing shifted Cyrillic.
  - uv `Key.Keystroke()` already uses `BaseCode` when it is set (`key.go:433-440`), so ctrl+`я` → `"ctrl+z"` without help. `String()` for space is `"space"`.
  - `KeyboardEnhancements.ReportAlternateKeys` only affects keys sent as escape codes, so plain letters need `ReportAllKeysAsEscapeCodes` too.
  - In that mode kitty also reports **modifier keys pressed alone** (decoded as `KeyLeftShift` etc., `decoder.go:1308`), and v2 does not filter them.
  - `tea.PasteMsg{Content}` exists, and bubbles v2 textinput/textarea consume it natively.
  - v2 pops and pushes the kitty keyboard stack around `ExecProcess` (`cursed_renderer.go:160-171`, `:190-195`).
  - bubbles v2 textarea/textinput default to `DefaultDarkStyles()`.
- `lipgloss/v2/compat.AdaptiveColor` is **ruled out**: its package `init` calls `lipgloss.HasDarkBackground(os.Stdin, os.Stdout)`, which would query the terminal on every CLI invocation (`cmd` imports `internal/tui`), including Raycast and Claude-skill `monolog add` runs. lipgloss v2 still detects the color profile at package load (`writer.go:14`: env + terminfo, plus `tmux info` under `$TMUX`). That is not a terminal query, so it is acceptable.
- lipgloss v2 removed `DefaultRenderer` / `ColorProfile` / `SetColorProfile` (lipgloss `UPGRADE_GUIDE_V2.md:216-240`), and `Style.Render` always emits SGR.

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions, with one declared exception: Tasks 2–3 are a single compile unit (v1 and v2 cannot coexist), so the package builds and the suite runs green only at the end of Task 3
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change: `go test ./internal/tui/` then `go test ./...` and `go vet ./...`
- maintain backward compatibility: user theme JSON format, config keys, and every existing shortcut string are unchanged

## Testing Strategy
- **unit tests**: required for every task (see Development Approach above)
- **e2e tests**: the project has no UI e2e suite; TUI behavior is covered by model tests that drive `Update` with key messages via the `key()` helper
- the existing ~12k-line `model_test.go` is the main safety net. Behavioral assertions stay unchanged; the edits are limited to the test fixture: key constructors, `.View().Content`, and the lipgloss renderer/profile removal (strip ANSI where a test relied on the Ascii profile)
- layout tests are driven through `Update` (not the handlers directly), so the dispatch wiring is covered, and at least one test feeds real kitty byte sequences through the v2 decoder, so the tests check the decoder rather than the plan's model of it

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview
- **Protocol on**: a single `View()` wrapper returns `tea.View{Content: m.render(), AltScreen: true, KeyboardEnhancements: {ReportAllKeysAsEscapeCodes, ReportAlternateKeys, ReportAssociatedText}}`. All existing render branches, including the search early return, move into `m.render() string`, so no branch can drop the flags. In kitty with Russian active, pressing the `d` key yields `Code='в', Text="в", BaseCode='d'`.
- **Modifier-only filter** (migration commit): `Update`'s key case returns early for modifier-only codes (`tea.KeyLeftShift … tea.KeyIsoLevel5Shift`, `KeyCapsLock`, `KeyNumLock`, `KeyScrollLock`). Otherwise a bare Shift press would dismiss help, cancel delete-confirm before `Y` arrives, and clear `statusMsg`, on every layout.
- **One normalization point**: `commandKey(tea.KeyPressMsg) tea.KeyPressMsg` rewrites a key to its US-layout equivalent for unmodified letters/punctuation and shift(or CapsLock)+letter; everything else is returned unchanged. Because it returns a key (not a string), every existing `switch msg.String() { case "d": … }` keeps working untouched. Ctrl/alt combos need no rewrite: uv's `Keystroke()` already uses `BaseCode`.
- **Where it applies** (only command contexts):
  - at the dispatch points: `Update` → `m.updateGrab(commandKey(msg))` and `m.updateSettings(commandKey(msg))`; `updateModal` → `m.updateConfirmDelete(commandKey(msg))`
  - inside `updateNormal` *after* the detail-panel early-return, so note text stays Cyrillic
  - help dismiss accepts any key, so it needs nothing
  - **never** in add / retag / reschedule / search query / note textarea, where the user types Russian. Reschedule's `1`–`6` digits are identical on ЙЦУКЕН.
- **Colors without `compat`**:
  - `theme.go` defines its own `AdaptiveColor{Light, Dark string}` with `Resolve(isDark bool) color.Color` = `lipgloss.LightDark(isDark)(lipgloss.Color(c.Light), lipgloss.Color(c.Dark))`. The user theme JSON format is unchanged.
  - `buildStyles`/`initStyles` take `isDark`. A single `restyle()` rebuilds all styles, including `textarea.DefaultStyles(isDark)` / `textinput.DefaultStyles(isDark)`, then re-applies monolog's CursorLine/Base overrides.
  - `Init` requests the terminal background (`tea.RequestBackgroundColor`); `tea.BackgroundColorMsg` sets `m.isDark` and calls `restyle()`, the same path `applySettings` uses. The default is dark until the reply arrives.
- **Paste**: v2 delivers paste as `tea.PasteMsg` (no longer a `KeyMsg` with `Paste=true`). The "route to focused input + post-process" tails of `updateAdd` and `updateRetag` are extracted into helpers taking a `tea.Msg`, shared by keys and paste, so paste also gets:
  - the title textarea height dance (`model.go:1916-1918`)
  - title-prefix auto-tag (`:1921-1930`)
  - `refreshSuggestions` / `refreshRecurSuggestions` (`:1785`, `:1908`, `:1912`)

## Technical Details
- `commandKey` rules, given `k := msg.Key()`:
  - no-op when `k.BaseCode == 0`, `!unicode.IsPrint(k.BaseCode)`, or `k.BaseCode == k.Code`
  - no-op when `k.Mod` contains ctrl, alt, super, hyper or meta (uv `Keystroke()` already renders these from `BaseCode`; pinned by a test)
  - review follow-up: a super/hyper/meta key additionally has its `Text` cleared, since kitty's associated text survives under those modifiers and `String()` would otherwise return the bare letter (super+`d` completing a task); pinned by `TestLayout_SuperTextDoesNotRunShortcut` / `TestLayout_DeleteConfirmSuperYDoesNotDelete`
  - shift or CapsLock held:
    - letter base → `Code = base`, `Text = string(unicode.ToUpper(base))`, drop `ModShift`/`ModCapsLock` from `Mod` (shift+`п` → `"G"`; Russian+CapsLock `п` → `"G"`, matching English+CapsLock `g`)
    - non-letter base → `Code = base`, `Text = ""`, keep `ModShift`, so `String()` falls back to the keystroke (`"shift+/"`, `"shift+,"`), which no shortcut matches. Leaving the key unchanged is **not** safe: the Task 1 probe showed Russian shift+`/` produces `Text=","`, which would open settings.
  - otherwise → `Code = base`, `Text = string(base)`, `ShiftedCode = 0` (`в` → `"d"`, `х` → `"["`, `б` → `","`)
- Expected `String()` mappings to keep stable through the migration: `"enter"`, `"esc"`, `"tab"`, `"shift+tab"`, `"up"`/`"down"`/`"left"`/`"right"`, `"pgup"`/`"pgdown"`, `"home"`/`"end"`, `"ctrl+c"`, `"ctrl+z"`, `"ctrl+y"`, `"backspace"`, `"space"` (v2 renders space as `"space"`; v1 rendered `" "` — audit every `case " "`).
- Key-field port (compiler-driven):
  - `msg.Type == tea.KeyEsc` → `msg.Code == tea.KeyEscape`
  - `msg.Type == tea.KeyRunes` → `msg.Text != ""` (no ctrl/alt in `Mod`)
  - `string(msg.Runes)` → `msg.Text`
  - `msg.Alt` → `msg.Mod.Contains(tea.ModAlt)`
  - `tea.KeySpace` / `tea.KeyBackspace` → `msg.Code == …`
  - v1 ctrl constants (`tea.KeyCtrlC` etc.) → `msg.String() == "ctrl+c"`
- **Known limitation**: under `ReportAllKeysAsEscapeCodes`, keypad Enter arrives as `KeyKpEnter`, so `msg.Code == tea.KeyEnter` checks ignore it, and shift+enter / ctrl+enter no longer match the `"enter"` string. Accepted; not handled.
- **Known limitation**: with the detail panel open on the Russian layout, the `[`/`]` keys produce `х`/`ъ`, which go into the note (text context), so body scroll is unavailable there.
- Test helpers:
  - `toKeyMsg(s)`: single rune → `tea.KeyPressMsg{Code: r, Text: string(r)}`; named keys → `{Code: tea.KeyUp}` etc.; `"ctrl+x"` → `{Code: 'x', Mod: tea.ModCtrl}`
  - `ruKey(text string, code, base rune, mod tea.KeyMod)` builds an event shaped like decoder output: `ShiftedCode == BaseCode`, `Text` empty under ctrl/alt, `Text` uppercase Cyrillic under shift

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): the probe, code changes, tests, docs in this repo
- **Post-Completion** (no checkboxes): manual run in kitty with the Russian layout

## Implementation Steps

### Task 1: Probe kitty's base-layout key reporting (gate, not committed)

**Files:**
- Create: `<scratchpad>/kbprobe/main.go` (throwaway, outside the repo)

- [x] write a ~30-line v2 program that sets the three `KeyboardEnhancements` flags and prints `Code`, `Text`, `ShiftedCode`, `BaseCode`, `Mod`, `String()`, `Keystroke()` for every `KeyPressMsg`, and the `KeyboardEnhancementsMsg` flags
- [x] user runs it in kitty with the Russian layout and presses: `d`, shift+`g`, ctrl+`z`, `/`, shift+`/`, `[`, `,`, Enter, keypad Enter, shift+Tab, alt+Enter, Space, **Shift alone**, **CapsLock then `d`**, and pastes some text
- [x] record each raw byte sequence too (for the decoder test in Task 6) — not captured; see probe results
- [x] record results in this plan under ⚠️ if anything deviates from Technical Details. Specifically: `Text` for shift+`п` must be `"П"`, Shift alone must produce a `KeyLeftShift` event, and Space must be `"space"`
- [x] **gate**: continue only if `BaseCode` is the Latin key for letters and ctrl-combos; otherwise stop and revisit the approach

**Probe results (2026-11-06, kitty, macOS Russian layout)** — gate passed. Recorded `KeyPressMsg` fields, used as ground truth for the Task 6 decoder test:

| key | Code | Text | ShiftedCode | BaseCode | Mod | String() | Keystroke() |
|---|---|---|---|---|---|---|---|
| `d` | `в` | `"в"` | `d` | `d` | 0 | `"в"` | `"d"` |
| shift+`g` | `п` | `"П"` | `g` | `g` | shift | `"П"` | `"shift+g"` |
| ctrl+`z` | `я` | `""` | `z` | `z` | ctrl | `"ctrl+z"` | `"ctrl+z"` |
| `/` | `.` | `"."` | `/` | `/` | 0 | `"."` | `"/"` |
| shift+`/` | `.` | `","` | `/` | `/` | shift | `","` | `"shift+/"` |
| `[` | `х` | `"х"` | `[` | `[` | 0 | `"х"` | `"["` |
| `,` | `б` | `"б"` | `,` | `,` | 0 | `"б"` | `","` |
| Enter | `\r` | `""` | 0 | 0 | 0 | `"enter"` | |
| shift+Tab | `\t` | `""` | 0 | 0 | shift | `"shift+tab"` | |
| alt+Enter | `\r` | `""` | 0 | 0 | alt | `"alt+enter"` | |
| Space | ` ` | `" "` | 0 | 0 | 0 | `"space"` | |
| Shift / Ctrl / Alt / Cmd alone | `KeyLeftShift` / `KeyLeftCtrl` / `KeyLeftAlt` / `KeyLeftSuper` | `""` | 0 | 0 | own mod | `"leftshift"` … | |
| paste | `tea.PasteMsg{Content: "paste some text"}` | | | | | | |

- `KeyboardEnhancementsMsg.Flags = 29` (1|4|8|16): all requested flags granted.
- ⚠️ shift+`/` yields `Text=","`, so the original "shift + non-letter → no-op" rule would have opened settings; the rule is changed to clear `Text` (see Technical Details).
- CapsLock: macOS switched the input source to ABC (`d` arrived as plain Latin with no `BaseCode`, `Mod=0`), so CapsLock+Cyrillic does not occur on this setup; the CapsLock-as-shift rule is kept as harmless and unit-tested only.
- Keypad Enter: not available on this keyboard; it stays a documented limitation.
- Raw `cat -v` bytes were not captured; the decoder test builds kitty CSI-u sequences from the spec and asserts the decoded fields equal the table above.

### Task 2: Port internal/tui to Bubble Tea v2 (mechanical, compile unit part 1)

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `internal/tui/tui.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/search.go`
- Modify: `internal/tui/vlist.go`
- Modify: `internal/tui/email.go`, `internal/tui/launch.go` (imports)
- Modify: `internal/tui/model_test.go`, `internal/tui/email_test.go`, other `*_test.go` importing charm

- [x] swap imports to `charm.land/bubbletea/v2`, `charm.land/bubbles/v2/{list,textarea,textinput}`, `charm.land/lipgloss/v2`; `go mod tidy` drops the v1 modules and `muesli/termenv`
- [x] `tui.go`: drop `tea.WithAltScreen()`. Rename the current `View()` body (including the `renderSearch` early return) to `render() string`; the new `View()` is the single wrapper from Solution Overview
- [x] port every key site per the Technical Details port list (compiler-driven), including the v1 ctrl constants; audit every `case " "` for the v2 `"space"` string
- [x] add the modifier-only early return in `Update`'s key case (before any mode dispatch)
- [x] bubbles API: every `textinput` `.Width =` → `SetWidth` (incl. `:1817-1818`, `:3198-3209`, tests `:8924`, `:8932`); `textarea` `FocusedStyle`/`BlurredStyle` → `Styles()`/`SetStyles()` (`StyleState`)
- [x] `tea.ExecProcess` (`model.go:2280`): confirm it compiles unchanged and the editor callback message still arrives
- [x] port `toKeyMsg` / `typeString` / `key()` and the `tea.KeyMsg{}` literals in tests to `tea.KeyPressMsg`; `.View()` call sites read `.Content`
- [x] tests: the package does not build until Task 3 lands (declared exception); `go build ./internal/tui/` errors must be limited to color/theme sites
- ➕ lipgloss v2 `Width`/`Height` include the border (v1 excluded it): detail panel `Width(pw-2)` → `Width(pw)`, `modalBox` `innerWidth+4` → `innerWidth+6`, search preview goes through `renderPreviewBox` (`+2` on both axes) so rendered sizes match v1
- ➕ the `muesli/termenv` imports were dropped from `model_test.go` / `theme_test.go` so `go mod tidy` removes the module; the `SetColorProfile`/`DefaultRenderer` lines that used it stay for Task 3 to delete
- ⚠️ remaining errors at end of Task 2 (all color/theme): `lipgloss.AdaptiveColor` in `theme.go` (57) / `theme_load.go` (3) / `theme_test.go` / `theme_load_test.go`; `lipgloss.SetColorProfile` / `DefaultRenderer` / `ColorProfile` + `termenv` in `model_test.go` / `theme_test.go`. With a temporary local `AdaptiveColor` stub and those lines removed, the package built and the suite ran with only 10 SGR/color-profile failures (Task 3 fixtures), so no key/view port regressions
- note: Tab checks use `msg.String() == "tab"` (not `Code == KeyTab`) so shift+tab keeps v1's "not Tab" meaning in the add modal / suggestion nav
- note: `go get` also bumped `sahilm/fuzzy` v0.1.1 → v0.1.3 (required by bubbles v2); `internal/search` tests pass

### Task 3: Theme colors and lipgloss v2 test fixtures (compile unit part 2)

**Files:**
- Modify: `internal/tui/theme.go`
- Modify: `internal/tui/theme_load.go`
- Modify: `internal/tui/tui.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/theme_test.go`, `internal/tui/theme_load_test.go`, `internal/tui/model_test.go`
- Modify: `internal/tui/email_test.go`, `internal/tui/launch_test.go` (➕ see notes below)

- [x] `theme.go`: define `AdaptiveColor{Light, Dark string}` + `Resolve(isDark bool) color.Color` via `lipgloss.LightDark`; theme definitions change only their type name
- [x] `theme_load.go`: `colorPair` ↔ local `AdaptiveColor` (JSON format unchanged)
- [x] `buildStyles(t, isDark)` / `initStyles(t, isDark)`; inline `m.theme.X` uses in render paths resolve through `m.isDark`; add `Model.isDark` (default `true`) and `restyle()`. `restyle()` applies `textarea.DefaultStyles(isDark)` / `textinput.DefaultStyles(isDark)` to every input, re-applies the CursorLine/Base overrides (`model.go:448-451`, `:461-464`), and is used by both `applySettings` and the new message handler
- [x] `Init` batches `tea.RequestBackgroundColor`; `Update` handles `tea.BackgroundColorMsg` → `m.isDark = msg.IsDark()`; `restyle()`
- [x] test fixtures: delete the `SetColorProfile`/`DefaultRenderer` lines in `model_test.go` and `theme_test.go`; wrap rendered output in `ansi.Strip` where a test relied on the Ascii profile (e.g. `:2031`, `:2051`, `:2595`, `:4596`, rune-length checks like `TestActivePanel_ShowsFullLongTitle` at `:2050-2066`); rewrite the `theme_test.go` color assertions against the local `AdaptiveColor` / resolved colors
- [x] write tests: `AdaptiveColor.Resolve` picks light/dark; `buildStyles` produces different foregrounds for a pair with distinct light/dark values; a light `BackgroundColorMsg` flips `m.isDark` and rebuilds styles; the textarea CursorLine/Base overrides survive `restyle()`
- [x] write tests: `View()` sets `AltScreen` and all three `KeyboardEnhancements` flags in both normal mode and search mode
- [x] write tests: a `KeyLeftShift` event through `Update` leaves help open; in delete-confirm, a `KeyLeftShift` event then `Y` deletes the task; a `KeyLeftShift` event in normal mode keeps `statusMsg`
- [x] run `go test ./...` and `go vet ./...` — the full existing suite must pass here (end of the compile unit)
- ➕ `email_test.go`: `Init` now always batches `tea.RequestBackgroundColor`, so `TestInit_EmailDisabledReturnsNil` became `TestInit_EmailDisabledOnlyRequestsBackgroundColor` and the enabled batch length is 3; the request is identified by function pointer (running the batch would block on the email `tea.Tick`)
- ➕ every text assertion on rendered output (`helpLine`, `detailPanelView`, `statsBarView`, `modalView`, `activePanelView`, `renderSearch`, `helpModalContent`, `View().Content`) in tests that do not inspect escape sequences now goes through `ansi.Strip`: v2 always emits SGR, which split multi-word phrases across style spans and made negative `!Contains` checks vacuous, not just the 10 failing ones
- note: the two TrueColor SGR literals were v1 rounding artifacts (`#4ADE80` → `74;222;128`, `#FFB454` → `255;180;84`); tests now derive them from the theme via `fgSGR`

### Task 4: Route tea.PasteMsg to the focused input

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/search.go` (if the search input needs its own forward)
- Modify: `internal/tui/model_test.go`

- [x] extract the "update focused input + post-process" tails of `updateAdd` (height dance, auto-tag, `refreshSuggestions` / `refreshRecurSuggestions`) and `updateRetag` (`refreshSuggestions`) into helpers that take a `tea.Msg`; the key paths call them unchanged
- [x] in `Update`, handle `tea.PasteMsg` by mode:
  - add modal → the add helper
  - retag → the retag helper
  - reschedule → `m.input`, only on the custom-date sub-step (ignored on sub-step 0, which has no input)
  - search → its textinput, then re-rank as for a typed key
  - normal mode with the detail panel open → `noteArea`
  - anything else → ignore
- [x] write tests:
  - a multi-line paste into the add-modal title keeps both lines visible
  - a paste into the tag field refreshes suggestions
  - a paste into the note textarea (panel open)
  - a paste into the search query re-ranks results
  - a paste into reschedule sub-step 0 is ignored
- [x] write test: paste in normal mode with no panel open is a no-op (no mutation, no panic)
- [x] run tests — must pass
- [x] commit 1: `refactor(tui): migrate to Bubble Tea v2`
- ➕ the title pre-grow on paste is the pasted content's own visual height (`visualLines`, factored out of `textareaVisualHeight`; `\r` counted as a newline since the textarea sanitizer converts it), not the keystroke path's `+1` — a 3-line paste otherwise scrolls the first line out of view
- note: helpers are `updateAddInput` / `updateRetagInput` (model.go), `updateSearchInput` (search.go); `routePaste` dispatches `tea.PasteMsg` by mode. Extra tests beyond the list: recur-field paste, auto-tag on paste, reschedule custom-date paste, command modes (help/grab/settings/delete-confirm) ignore paste

### Task 5: Add the commandKey normalizer

**Files:**
- Create: `internal/tui/keys.go`
- Create: `internal/tui/keys_test.go`

- [x] implement `commandKey(tea.KeyPressMsg) tea.KeyPressMsg` per the Technical Details rules, with a short doc comment stating *where* it may be applied and why (text inputs must see the raw key)
- [x] write table tests, rewrite cases: `в`/base `d` → `"d"`; shift+`п`/base `g` → `"G"`; CapsLock+`п`/base `g` → `"G"`; `х`/base `[` → `"["`; `б`/base `,` → `","`; `н`/base `y` → `"y"`
- [x] write table tests, shifted punctuation: shift+`.`/Text `","`/base `/` → `"shift+/"` (the probe's real event — must NOT be `","`); shift+`б`/Text `"Б"`/base `,` → `"shift+,"`
- [x] write table tests, unchanged cases: ctrl+`я`/base `z` returned unchanged and its `String()` is `"ctrl+z"` (pins the uv `Keystroke()` behavior); `BaseCode == 0` (no protocol); `BaseCode == Code` (English layout `d`); Enter / Esc / arrows; non-printable `BaseCode`
- [x] run tests — must pass
- note: `ruKey` lives in `keys_test.go` (same package) so Task 6 reuses it; CapsLock + non-letter base keeps `Text = string(base)` (US CapsLock does not shift punctuation) — same `String()` as the Technical Details rule

### Task 6: Apply commandKey in command contexts only

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [x] wire `commandKey` at the dispatch points: `Update` → `updateGrab(commandKey(msg))`, `updateSettings(commandKey(msg))`; `updateModal` → `updateConfirmDelete(commandKey(msg))`. Inside `updateNormal`, apply it immediately **after** the `detailOpen` early-return block
- [x] add the `ruKey` test helper (decoder-shaped events per Technical Details) — already added in `keys_test.go` by Task 5 (same package); reused here
- [x] write tests (layout works, all through `Update`): `в`+base `d` in normal mode marks the selected task done; shift+`п`+base `g` in grab mode moves to the bottom; `н`+base `y` confirms a delete; `б`+base `,` opens settings; `д`+base `l` in the settings modal cycles the date format
- [x] write tests (text stays raw, through `Update`): `в`+base `d` in the add modal yields title `в`; shift+`п` in the add modal yields `П`; with the detail panel open it lands in the note textarea as `в`; in the search overlay it lands in the query as `в`; in the retag modal it lands as `в`
- [x] write a decoder-level test: feed the kitty byte sequences recorded in Task 1 (e.g. `в` with base `d`, shift+`п` with associated text) through the v2 input decoder and assert the resulting `KeyPressMsg` fields and the `commandKey(...).String()` result
- [x] run `go test ./...` and `go vet ./...` — must pass
- [x] commit 2: `feat(tui): layout-independent shortcuts via kitty base-layout keys`
- ➕ decoder test lives in `keys_test.go` (`TestCommandKey_KittyDecoder`, `decodeKitty` helper) and imports `github.com/charmbracelet/ultraviolet` directly, so go.mod now lists it as a direct dependency (test-only import). Sequences are built from the kitty spec; all 14 decode to exactly the probe-table fields. Extra: `TestLayout_DecodedKittyBytesMarkDone` drives decoded bytes through `Update`, and modifier-alone sequences decode to codes `isModifierOnly` filters
- note: extra layout tests beyond the list: `ч`→delete-confirm, shift+`н`→`Y`, `ь`→grab, the probe shift+`/` event does not open settings, `.` (base `/`) opens search, ctrl+`я` undoes, reschedule custom-date keeps `в`

### Task 7: Verify acceptance criteria
- [x] verify every letter/punctuation shortcut in normal, grab, settings and delete-confirm resolves via `BaseCode` (tests from Task 6 + Task 1 probe results) — `TestLayout_EveryCommandShortcutMatchesLatin` (differential sweep through `Update`: every shortcut in the 4 contexts, Russian key vs US key, full state snapshot; fails 35/39 with the wiring reverted), `TestCommandKey_EveryRussianKeyResolvesToUS` (whole ЙЦУКЕН layout incl. shifted letters), `TestCommandKey_KittyDecoder`, `TestLayout_*` from Task 6
- [x] verify text inputs (add, retag, reschedule, search, note) receive Cyrillic unchanged — `TestLayout_AddModalKeepsRussianText`, new `TestLayout_AddTagAndRecurFieldsKeepRussianText`, `TestLayout_RetagKeepsRussianText`, `TestLayout_RescheduleCustomDateKeepsRussianText`, `TestLayout_SearchQueryKeepsRussianText`, `TestLayout_DetailPanelNoteKeepsRussianText`
- [x] verify paste works in every input, with post-processing — the 11 `TestPaste_*` tests (add title height + auto-tag, tag/recur suggestions, retag suggestions, note, search re-rank, reschedule custom date; picker/normal/command modes ignore paste)
- [x] verify modifier-only presses do nothing in any mode — new `TestModifierOnly_NoOpInEveryMode` (15 bare modifier/lock keys × 13 modes/inputs, full snapshot + nil cmd), new `TestIsModifierOnly_CoversWholeModifierRange`, `TestModifierOnly_*`, `TestKittyDecoder_ModifierAloneIsFiltered`
- [x] verify no terminal query happens on CLI commands: `go list -deps ./cmd/...` does not include `charm.land/lipgloss/v2/compat` — `grep -c` prints 0
- [x] run full test suite: `go test ./...` and `go vet ./...` — both pass

### Task 8: [Final] Update documentation
- [x] CLAUDE.md **Dependencies**: charmbracelet stack → `charm.land/{bubbletea,bubbles,lipgloss}/v2`
- [x] CLAUDE.md **Key Design Decisions**: add a "Layout-independent shortcuts" bullet covering:
  - the kitty keyboard protocol flags (and why `ReportAssociatedText` is load-bearing)
  - the modifier-only filter
  - `commandKey`, its dispatch points, and the command-context-only rule; why text inputs get raw keys
  - the shift+punctuation no-op and CapsLock-as-shift
  - `PasteMsg` routing through the shared helpers
  - the known limitations (keypad Enter; `[`/`]` go into the note with the detail panel open on Russian)
  - `compat` ruled out because of its init-time terminal query; lipgloss v2's package-load color-profile detection is acceptable
- [x] CLAUDE.md **TUI color themes** bullet: `AdaptiveColor` is now the package-local type resolved via `isDark` / `BackgroundColorMsg` / `restyle()`
- [x] README.md: one line noting shortcuts work on non-Latin layouts in terminals with the kitty keyboard protocol
- [x] move this plan to `docs/plans/completed/` (done by orchestrator at completion)
- note: the new CLAUDE.md bullet also records the contract for new code (wrap command contexts in `commandKey`, add text inputs to `routePaste`, add shortcuts to the differential sweep), the v2 `"space"`/`"tab"` key strings, the lipgloss v2 border-inside-`Width` compensation and the `ansi.Strip`/`fgSGR` test conventions; the Settings modal bullet now says theme apply goes through `restyle()`; Dependencies also names `x/ansi` and the test-only `ultraviolet`

## Post-Completion
*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification**:
- run `go build -o monolog && ./monolog` in kitty with the Russian layout:
  - `в` (done), `й` (quit), shift+`п` (bottom), `ч` (delete) + `н` (confirm), `м` (view toggle), `б` (settings), the `/` key (search), ctrl+`я` (undo)
  - press Shift alone with help open and in delete-confirm: nothing should close
- type a Russian title in the add modal and a Russian note in the detail panel; paste multi-line text into both
- press `e`, type in `$EDITOR`, save and quit: the editor must behave normally (no CSI-u garbage), and TUI shortcuts must still work afterwards
- switch the macOS appearance / kitty theme to light and confirm the theme colors adapt on next launch
- quit and confirm kitty's keyboard mode is restored (typing in the shell is normal)
