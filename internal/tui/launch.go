package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/maksmas/monolog/internal/launch"
	"github.com/maksmas/monolog/internal/store"
)

// launchTimeout bounds the launcher process. `kitty @ launch` returns as soon
// as the tab exists, so anything slower is a hung launcher.
const launchTimeout = 5 * time.Second

// runLaunch is the seam tests replace so no test ever execs a real launcher.
var runLaunch = launch.Run

// launchResult reports a finished launcher run for the task with taskID.
// title is pre-flattened for the status bar.
type launchResult struct {
	err    error
	taskID string
	title  string
}

// launchConfigured reports whether the config.json launch block names a
// launcher command; without one `o` only flashes "launch not configured".
func (m *Model) launchConfigured() bool {
	return len(m.launchCfg.Command) > 0
}

// launchSelected opens the selected task in Claude Code via the configured
// launcher. Every outcome is reported through statusMsg, never m.err, and
// clears any earlier m.err: helpLine renders m.err ahead of statusMsg, so a
// sticky error would hide every launch status.
func (m *Model) launchSelected() tea.Cmd {
	task := m.selectedTask()
	if task == nil {
		return nil
	}
	m.err = nil
	if !m.launchConfigured() {
		m.statusMsg = "launch not configured"
		return nil
	}
	dir, err := launch.ResolveDir(task.Tags, m.launchCfg.Dirs, m.launchCfg.DefaultDir)
	if err != nil {
		m.statusMsg = err.Error()
		return nil
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		m.statusMsg = "launch dir not found: " + dir
		return nil
	}
	prompt := launch.BuildPrompt(task.ID, task.Title, task.Body)
	argv := launch.BuildArgv(m.launchCfg.Command, dir, prompt)
	return launchCmd(argv, dir, task.ID, flattenTitle(task.Title))
}

// launchCmd runs the launcher off the main loop under launchTimeout and
// reports the outcome as a launchResult for taskID.
func launchCmd(argv []string, dir, taskID, title string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
		defer cancel()
		return launchResult{err: runLaunch(ctx, argv, dir), taskID: taskID, title: title}
	}
}

// handleLaunchResult activates the launched task once the launcher succeeded.
// The task is re-read by ID because the selection, or the task itself, may
// have changed while the launcher ran. A done or already-active task is left
// alone — toggling an active one would deactivate it.
func (m *Model) handleLaunchResult(msg launchResult) tea.Cmd {
	m.err = nil
	if msg.err != nil {
		m.statusMsg = "launch failed: " + msg.err.Error()
		return nil
	}
	t, err := m.store.Get(msg.taskID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		m.statusMsg = fmt.Sprintf("launched: %s (task gone, not activated)", msg.title)
	case err != nil:
		m.statusMsg = fmt.Sprintf("launched: %s (not activated: %v)", msg.title, err)
	case t.Status == "done" || t.IsActive():
		m.statusMsg = "launched: " + msg.title
	default:
		// If the user moved on while the launcher ran, activate without the
		// focus jump `a` does, so the cursor is not yanked back to Today.
		sel := m.selectedTask()
		return m.activateCmd(t, "launched", sel != nil && sel.ID == msg.taskID)
	}
	return nil
}
