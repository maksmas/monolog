// Package tui implements the interactive terminal UI for monolog.
// It is launched when `monolog` is invoked without a subcommand, and offers
// tab-organized viewing and editing of tasks across schedule buckets.
package tui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/maksmas/monolog/internal/store"
)

// watchDebounce is the coalescing window for filesystem events on the
// tasks directory. 250ms is short enough to feel instant after an external
// `monolog add` and long enough to fold a Create+Write event pair into a
// single reload.
const watchDebounce = 250 * time.Millisecond

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

	// Best-effort filesystem watcher so external mutations (Raycast,
	// a second terminal, an external git pull) auto-refresh the TUI.
	// A failure here is non-fatal — the TUI runs without auto-refresh
	// instead of refusing to launch.
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	w, werr := newTaskWatcher(tasksDir, watchDebounce)
	if werr != nil {
		fmt.Fprintf(os.Stderr, "monolog: watcher: %v\n", werr)
	}
	m.watcher = w
	defer func() { _ = w.Stop() }()

	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}

// View returns the screen content plus the program-level terminal settings.
// The kitty keyboard protocol flags make the terminal report the base-layout
// key (Key.BaseCode) alongside the typed text, so shortcuts can be resolved on
// non-Latin layouts. ReportAllKeysAsEscapeCodes is needed because
// ReportAlternateKeys only affects keys already sent as escape codes, and
// ReportAssociatedText keeps shifted non-Latin text (e.g. "П") intact.
func (m *Model) View() tea.View {
	return tea.View{
		Content:   m.render(),
		AltScreen: true,
		KeyboardEnhancements: tea.KeyboardEnhancements{
			ReportAllKeysAsEscapeCodes: true,
			ReportAlternateKeys:        true,
			ReportAssociatedText:       true,
		},
	}
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
// color roles are sourced from t, resolved for the terminal background
// (isDark), so the palette stays cohesive and theme-swappable.
func buildStyles(t Theme, isDark bool) tuiStyles {
	c := func(a AdaptiveColor) color.Color { return a.Resolve(isDark) }
	return tuiStyles{
		tabStyle: lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(c(t.TabFg)),

		activeTabStyle: lipgloss.NewStyle().
			Padding(0, 2).
			Bold(true).
			Foreground(c(t.ActiveTabFg)).
			Background(c(t.ActiveTabBg)),

		statusStyle: lipgloss.NewStyle().
			Foreground(c(t.TabFg)).
			Padding(0, 1),

		helpStyle: lipgloss.NewStyle().
			Foreground(c(t.Borders)).
			Padding(0, 1),

		separatorStyle: lipgloss.NewStyle().
			Foreground(c(t.Borders)).
			Bold(true).
			PaddingLeft(2),

		helpKeyStyle: lipgloss.NewStyle().
			Bold(true).
			Foreground(c(t.Hotkey)),

		helpTextStyle: lipgloss.NewStyle().
			Foreground(c(t.Borders)),

		searchDoneStyle: lipgloss.NewStyle().Foreground(c(t.SearchDone)),

		searchActiveStyle: lipgloss.NewStyle().Foreground(c(t.SearchActive)),

		searchCountStyle: lipgloss.NewStyle().Foreground(c(t.SearchCount)),

		searchMetaStyle: lipgloss.NewStyle().
			Foreground(c(t.SearchMeta)).
			Padding(0, 1),

		searchPreviewBorderStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c(t.SearchPreviewBorder)).
			Padding(0, 1),

		searchPreviewDimStyle: lipgloss.NewStyle().
			Foreground(c(t.SearchPreviewDim)).
			Italic(true),
	}
}
