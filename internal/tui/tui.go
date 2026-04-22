// Package tui implements the interactive terminal UI for monolog.
// It is launched when `monolog` is invoked without a subcommand, and offers
// tab-organized viewing and editing of tasks across schedule buckets.
package tui

import (
	"fmt"
	"os"

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
	// Bootstrap example theme file and load any user-authored themes
	// before building the Model so the settings modal and theme lookup
	// see them. Errors are informational — missing or bad theme files
	// must never prevent the TUI from starting.
	if err := bootstrapExampleTheme(repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "monolog: %v\n", err)
	}
	for _, err := range initThemes(repoPath) {
		fmt.Fprintf(os.Stderr, "monolog: %v\n", err)
	}

	m, err := newModel(s, repoPath, opts)
	if err != nil {
		return err
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// Theme-independent package-level styles. These have no theme colors and do not
// need to be rebuilt when the theme changes, so they live here rather than in
// tuiStyles.

// suggestionBoldStyle highlights the currently selected autocomplete suggestion.
var suggestionBoldStyle = lipgloss.NewStyle().Bold(true)

// suggestionDimStyle dims non-selected autocomplete suggestions.
var suggestionDimStyle = lipgloss.NewStyle().Faint(true)

// searchSelectedStyle reverses foreground/background for the highlighted
// result row so it is obvious regardless of theme.
var searchSelectedStyle = lipgloss.NewStyle().Reverse(true)

// searchResultsStyle wraps the results list in matching padding so it
// visually balances the preview pane.
var searchResultsStyle = lipgloss.NewStyle().Padding(0, 1)

// searchPreviewTitleStyle styles the title heading inside the preview pane.
var searchPreviewTitleStyle = lipgloss.NewStyle().Bold(true)

// tuiStyles holds all theme-driven lipgloss styles used across the TUI. An
// instance is built once per Model via buildStyles and stored in Model.styles.
// Moving styles off package-level vars lets buildStyles swap in theme colors
// without mutating shared state.
type tuiStyles struct {
	tabStyle       lipgloss.Style
	activeTabStyle lipgloss.Style
	statusStyle    lipgloss.Style
	helpStyle      lipgloss.Style

	// separatorStyle renders bucket heading items in tag view — dimmed and
	// left-padded to align with normal item titles.
	separatorStyle lipgloss.Style

	// helpKeyStyle highlights hotkeys in the help modal and status bar: bold,
	// light red (or the theme's Hotkey color).
	helpKeyStyle lipgloss.Style

	// helpTextStyle is the foreground-only component used for descriptions and
	// separators in the status bar (no padding — padding applied by renderHelpBar).
	helpTextStyle lipgloss.Style

	// --- search overlay styles ---------------------------------------------
	// Kept here alongside the rest of the TUI styling so the palette stays
	// discoverable in one place.

	// searchDoneStyle dims rows for done tasks so open work stands out.
	searchDoneStyle lipgloss.Style

	// searchActiveStyle paints rows green when the task carries the active tag,
	// mirroring the in-list treatment.
	searchActiveStyle lipgloss.Style

	// searchCountStyle is used for the "N/M" counter on the right of the input
	// bar. Dim so it doesn't distract from the query.
	searchCountStyle lipgloss.Style

	// searchMetaStyle renders the meta line (schedule · status · created).
	searchMetaStyle lipgloss.Style

	// searchPreviewBorderStyle wraps the preview pane in a subtle border so the
	// split is clear even on terminals without vertical rule characters.
	searchPreviewBorderStyle lipgloss.Style

	// searchPreviewDimStyle is the "(no body)" placeholder style in the preview.
	searchPreviewDimStyle lipgloss.Style
}

// buildStyles constructs a tuiStyles value driven by the given theme. All
// color roles are sourced from t so the palette stays cohesive and
// theme-swappable.
func buildStyles(t Theme) tuiStyles {
	return tuiStyles{
		tabStyle: lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(t.TabFg),

		activeTabStyle: lipgloss.NewStyle().
			Padding(0, 2).
			Bold(true).
			Foreground(t.ActiveTabFg).
			Background(t.ActiveTabBg),

		statusStyle: lipgloss.NewStyle().
			Foreground(t.TabFg).
			Padding(0, 1),

		helpStyle: lipgloss.NewStyle().
			Foreground(t.Borders).
			Padding(0, 1),

		separatorStyle: lipgloss.NewStyle().
			Foreground(t.Borders).
			Bold(true).
			PaddingLeft(2),

		helpKeyStyle: lipgloss.NewStyle().
			Bold(true).
			Foreground(t.Hotkey),

		helpTextStyle: lipgloss.NewStyle().
			Foreground(t.Borders),

		searchDoneStyle: lipgloss.NewStyle().Foreground(t.SearchDone),

		searchActiveStyle: lipgloss.NewStyle().Foreground(t.SearchActive),

		searchCountStyle: lipgloss.NewStyle().Foreground(t.SearchCount),

		searchMetaStyle: lipgloss.NewStyle().
			Foreground(t.SearchMeta).
			Padding(0, 1),

		searchPreviewBorderStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.SearchPreviewBorder).
			Padding(0, 1),

		searchPreviewDimStyle: lipgloss.NewStyle().
			Foreground(t.SearchPreviewDim).
			Italic(true),
	}
}
