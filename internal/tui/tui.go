// Package tui implements the interactive terminal UI for monolog.
// It is launched when `monolog` is invoked without a subcommand, and offers
// tab-organized viewing and editing of tasks across schedule buckets.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mmaksmas/monolog/internal/store"
)

// Options configures how the TUI is launched.
type Options struct {
	StartInTagView bool // when true the TUI opens in tag-view mode
}

// Run launches the interactive TUI. Blocks until the user quits.
func Run(s *store.Store, repoPath string, opts Options) error {
	m, err := newModel(s, repoPath, opts)
	if err != nil {
		return err
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// Styles used across the TUI. Kept in one place so the palette stays cohesive.
var (
	tabBorder = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	tabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(lipgloss.Color("244"))

	activeTabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("62"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Padding(0, 1)

	// separatorStyle renders bucket heading items in tag view — dimmed and
	// left-padded to align with normal item titles.
	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Bold(true).
			PaddingLeft(2)

	// helpKeyStyle highlights hotkeys in the help modal and status bar: bold, light red.
	helpKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("9"))

	// helpTextStyle is the foreground-only component used for descriptions and
	// separators in the status bar (no padding — padding applied by renderHelpBar).
	helpTextStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)
