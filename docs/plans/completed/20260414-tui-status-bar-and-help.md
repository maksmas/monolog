# TUI Status Bar Updates + Help Overlay

## Context

The TUI status bar had two label issues:
- `c add` — user wants `c` to read as "create", not "add"
- `r resched` — "resched" is ambiguous; user wants `r date` (shorter, clearer intent)

Additionally, the status bar is dense and hard to scan for new users. A dedicated `h` help overlay shows all keybindings with full descriptions, with hotkeys highlighted bold+light-red.

## Files Modified

- `internal/tui/tui.go` — added `helpKeyStyle`
- `internal/tui/model.go` — `modeHelp` constant, `h` key handler, updated `helpLine()` labels, `modalView()` help content
- `internal/tui/model_test.go` — 8 new tests

## Solution

1. Updated `helpLine()` labels: `r resched` → `r date`, `c add` → `c create`, added `h help`
2. Added `modeHelp` constant after `modeConfirmDelete`
3. `h` key in `updateNormal()` enters `modeHelp`
4. Any key in `modeHelp` in `Update()` dispatch returns to `modeNormal`
5. `modeHelp` case in `helpLine()` shows "press any key to close"
6. `helpModalContent()` builds keybinding list; `modeHelp` case in `modalView()` renders it
7. `helpKeyStyle` in `tui.go`: bold + ANSI color 9 (light red)
