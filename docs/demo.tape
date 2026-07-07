# monolog demo tape (charmbracelet VHS script).
#
# Renders docs/img/demo.gif via `make demo` (or `vhs docs/demo.tape`).
# Requires vhs: `brew install vhs`.
#
# PREREQUISITE: the `monolog` binary must be on PATH for the recording.
# Before running, from the repo root:
#     make build && export PATH=$PWD:$PATH
# (or `go install github.com/maksmas/monolog@latest`). The gif render itself is
# a manual Post-Completion step in the OSS release plan.
#
# The tape isolates MONOLOG_DIR to a throwaway temp dir so the author's real
# ~/.monolog backlog is NEVER recorded.

Output docs/img/demo.gif

Set FontSize 16
Set Width 1200
Set Height 700
Set Padding 20

# --- Setup: isolate the data dir and seed a few tasks (hidden from the gif) ---
Hide
Type "export MONOLOG_DIR=$(mktemp -d)"
Enter
Type "monolog init"
Enter
Sleep 1s
Type "monolog add 'Ship v0.1.0 release'"
Enter
Sleep 300ms
Type "monolog add 'work: Review PR #42' -t work"
Enter
Sleep 300ms
Type "monolog add 'Write demo tape' -s week"
Enter
Sleep 300ms
Type "monolog add 'Buy groceries' -s tomorrow"
Enter
Sleep 300ms
Type "monolog add 'work: Prep standup notes' -t work"
Enter
Sleep 300ms
Type "clear"
Enter
Show

# --- Launch the interactive TUI ---
Type "monolog"
Enter
Sleep 2s

# Navigate the Today list.
Down
Sleep 800ms
Down
Sleep 800ms
Up
Sleep 800ms

# Switch across schedule tabs.
Right
Sleep 1s
Right
Sleep 1s

# Toggle tag view (tabs become tags).
Type "v"
Sleep 1500ms
Right
Sleep 1s
Type "v"
Sleep 1s

# Fuzzy search.
Type "/"
Sleep 800ms
Type "review"
Sleep 1500ms
Escape
Sleep 800ms

# Mark the focused task done.
Type "d"
Sleep 1500ms

# Quit.
Type "q"
Sleep 1s
