package tui

import "github.com/charmbracelet/lipgloss"

// Theme defines all color roles used by the TUI. Each field is a
// lipgloss.AdaptiveColor so the palette adapts to light/dark terminal
// backgrounds automatically.
type Theme struct {
	// Chrome colors
	ActiveBorder lipgloss.AdaptiveColor // border of the detail panel
	ModalBorder  lipgloss.AdaptiveColor // border of modal dialogs
	Hotkey       lipgloss.AdaptiveColor // hotkey highlights in help bar
	ActiveTabBg  lipgloss.AdaptiveColor // background of the active tab
	ActiveTabFg  lipgloss.AdaptiveColor // foreground of the active tab
	TabFg        lipgloss.AdaptiveColor // foreground of inactive tabs and status bar
	Borders      lipgloss.AdaptiveColor // dim text, separators

	// Task list colors
	NormalText     lipgloss.AdaptiveColor // normal task title
	SelectedText   lipgloss.AdaptiveColor // selected task title (pink)
	DimText        lipgloss.AdaptiveColor // dim/description text
	GrabText       lipgloss.AdaptiveColor // grab-mode selected task (orange)
	ActiveNormal   lipgloss.AdaptiveColor // active-tagged task, not selected
	ActiveSelected lipgloss.AdaptiveColor // active-tagged task, selected

	// Search overlay colors
	SearchDone          lipgloss.AdaptiveColor // done task rows in results
	SearchActive        lipgloss.AdaptiveColor // active-tagged rows in results
	SearchCount         lipgloss.AdaptiveColor // N/M counter text
	SearchMeta          lipgloss.AdaptiveColor // meta line (schedule · status · created)
	SearchPreviewBorder lipgloss.AdaptiveColor // border of the preview pane
	SearchPreviewDim    lipgloss.AdaptiveColor // "(no body)" placeholder
}

// defaultTheme reflects the current hardcoded palette extracted from tui.go
// and model.go. It is the fallback when no theme is configured.
var defaultTheme = Theme{
	ActiveBorder: lipgloss.AdaptiveColor{Light: "28", Dark: "28"},
	ModalBorder:  lipgloss.AdaptiveColor{Light: "62", Dark: "62"},
	Hotkey:       lipgloss.AdaptiveColor{Light: "9", Dark: "9"},
	ActiveTabBg:  lipgloss.AdaptiveColor{Light: "62", Dark: "62"},
	ActiveTabFg:  lipgloss.AdaptiveColor{Light: "231", Dark: "231"},
	TabFg:        lipgloss.AdaptiveColor{Light: "244", Dark: "244"},
	Borders:      lipgloss.AdaptiveColor{Light: "240", Dark: "240"},

	NormalText:     lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"},
	SelectedText:   lipgloss.AdaptiveColor{Light: "#EE6FF8", Dark: "#EE6FF8"},
	DimText:        lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"},
	GrabText:       lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#FFB454"},
	ActiveNormal:   lipgloss.AdaptiveColor{Light: "#16A34A", Dark: "#22C55E"},
	ActiveSelected: lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"},

	SearchDone:          lipgloss.AdaptiveColor{Light: "244", Dark: "244"},
	SearchActive:        lipgloss.AdaptiveColor{Light: "76", Dark: "76"},
	SearchCount:         lipgloss.AdaptiveColor{Light: "240", Dark: "240"},
	SearchMeta:          lipgloss.AdaptiveColor{Light: "244", Dark: "244"},
	SearchPreviewBorder: lipgloss.AdaptiveColor{Light: "240", Dark: "240"},
	SearchPreviewDim:    lipgloss.AdaptiveColor{Light: "240", Dark: "240"},
}

// draculaTheme uses the Dracula color palette.
// Reference: https://draculatheme.com/contribute
var draculaTheme = Theme{
	ActiveBorder: lipgloss.AdaptiveColor{Light: "#6272a4", Dark: "#bd93f9"},
	ModalBorder:  lipgloss.AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	Hotkey:       lipgloss.AdaptiveColor{Light: "#ff5555", Dark: "#ff5555"},
	ActiveTabBg:  lipgloss.AdaptiveColor{Light: "#44475a", Dark: "#44475a"},
	ActiveTabFg:  lipgloss.AdaptiveColor{Light: "#f8f8f2", Dark: "#f8f8f2"},
	TabFg:        lipgloss.AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	Borders:      lipgloss.AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},

	NormalText:     lipgloss.AdaptiveColor{Light: "#282a36", Dark: "#f8f8f2"},
	SelectedText:   lipgloss.AdaptiveColor{Light: "#ff79c6", Dark: "#ff79c6"},
	DimText:        lipgloss.AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	GrabText:       lipgloss.AdaptiveColor{Light: "#ffb86c", Dark: "#ffb86c"},
	ActiveNormal:   lipgloss.AdaptiveColor{Light: "#50fa7b", Dark: "#50fa7b"},
	ActiveSelected: lipgloss.AdaptiveColor{Light: "#69ff94", Dark: "#69ff94"},

	SearchDone:          lipgloss.AdaptiveColor{Light: "#6272a4", Dark: "#6272a4"},
	SearchActive:        lipgloss.AdaptiveColor{Light: "#50fa7b", Dark: "#50fa7b"},
	SearchCount:         lipgloss.AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	SearchMeta:          lipgloss.AdaptiveColor{Light: "#6272a4", Dark: "#6272a4"},
	SearchPreviewBorder: lipgloss.AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	SearchPreviewDim:    lipgloss.AdaptiveColor{Light: "#6272a4", Dark: "#6272a4"},
}

// themes is the registry of all built-in themes keyed by name.
var themes = map[string]Theme{
	"default": defaultTheme,
	"dracula": draculaTheme,
}

