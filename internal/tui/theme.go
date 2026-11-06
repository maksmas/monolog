package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// AdaptiveColor is a light/dark color pair. lipgloss v2 dropped its own
// AdaptiveColor, and the compat package's replacement queries the terminal
// in its package init, which would run on every CLI command since cmd
// imports this package. Instead the pair is resolved explicitly against
// Model.isDark, which the TUI learns from the terminal's BackgroundColorMsg.
// Values are any string lipgloss.Color accepts (ANSI index or hex). The json
// tags are the on-disk theme file format (see themeFile).
type AdaptiveColor struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// Resolve returns the Dark color on a dark background, the Light one otherwise.
func (c AdaptiveColor) Resolve(isDark bool) color.Color {
	return lipgloss.LightDark(isDark)(lipgloss.Color(c.Light), lipgloss.Color(c.Dark))
}

// Theme defines all color roles used by the TUI. Each field is an
// AdaptiveColor resolved against the terminal background (see restyle).
type Theme struct {
	// Chrome colors
	ActiveBorder AdaptiveColor // border of the detail panel
	ModalBorder  AdaptiveColor // border of modal dialogs
	Hotkey       AdaptiveColor // hotkey highlights in help bar
	ActiveTabBg  AdaptiveColor // background of the active tab
	ActiveTabFg  AdaptiveColor // foreground of the active tab
	TabFg        AdaptiveColor // foreground of inactive tabs and status bar
	Borders      AdaptiveColor // dim text, separators

	// Task list colors
	NormalText     AdaptiveColor // normal task title
	SelectedText   AdaptiveColor // selected task title (pink)
	DimText        AdaptiveColor // dim/description text
	GrabText       AdaptiveColor // grab-mode selected task (orange)
	ActiveNormal   AdaptiveColor // active-tagged task, not selected
	ActiveSelected AdaptiveColor // active-tagged task, selected

	// Search overlay colors
	SearchDone          AdaptiveColor // done task rows in results
	SearchActive        AdaptiveColor // active-tagged rows in results
	SearchCount         AdaptiveColor // N/M counter text
	SearchMeta          AdaptiveColor // meta line (schedule · status · created)
	SearchPreviewBorder AdaptiveColor // border of the preview pane
	SearchPreviewDim    AdaptiveColor // "(no body)" placeholder
}

// defaultTheme reflects the current hardcoded palette extracted from tui.go
// and model.go. It is the fallback when no theme is configured.
var defaultTheme = Theme{
	ActiveBorder: AdaptiveColor{Light: "28", Dark: "28"},
	ModalBorder:  AdaptiveColor{Light: "62", Dark: "62"},
	Hotkey:       AdaptiveColor{Light: "9", Dark: "9"},
	ActiveTabBg:  AdaptiveColor{Light: "62", Dark: "62"},
	ActiveTabFg:  AdaptiveColor{Light: "231", Dark: "231"},
	TabFg:        AdaptiveColor{Light: "244", Dark: "244"},
	Borders:      AdaptiveColor{Light: "240", Dark: "240"},

	NormalText:     AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"},
	SelectedText:   AdaptiveColor{Light: "#EE6FF8", Dark: "#EE6FF8"},
	DimText:        AdaptiveColor{Light: "#A49FA5", Dark: "#777777"},
	GrabText:       AdaptiveColor{Light: "#D97706", Dark: "#FFB454"},
	ActiveNormal:   AdaptiveColor{Light: "#16A34A", Dark: "#22C55E"},
	ActiveSelected: AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"},

	SearchDone:          AdaptiveColor{Light: "244", Dark: "244"},
	SearchActive:        AdaptiveColor{Light: "76", Dark: "76"},
	SearchCount:         AdaptiveColor{Light: "240", Dark: "240"},
	SearchMeta:          AdaptiveColor{Light: "244", Dark: "244"},
	SearchPreviewBorder: AdaptiveColor{Light: "240", Dark: "240"},
	SearchPreviewDim:    AdaptiveColor{Light: "240", Dark: "240"},
}

// draculaTheme uses the Dracula color palette.
// Reference: https://draculatheme.com/contribute
var draculaTheme = Theme{
	ActiveBorder: AdaptiveColor{Light: "#6272a4", Dark: "#bd93f9"},
	ModalBorder:  AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	Hotkey:       AdaptiveColor{Light: "#ff5555", Dark: "#ff5555"},
	ActiveTabBg:  AdaptiveColor{Light: "#44475a", Dark: "#44475a"},
	ActiveTabFg:  AdaptiveColor{Light: "#f8f8f2", Dark: "#f8f8f2"},
	TabFg:        AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	Borders:      AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},

	NormalText:     AdaptiveColor{Light: "#282a36", Dark: "#f8f8f2"},
	SelectedText:   AdaptiveColor{Light: "#ff79c6", Dark: "#ff79c6"},
	DimText:        AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	GrabText:       AdaptiveColor{Light: "#ffb86c", Dark: "#ffb86c"},
	ActiveNormal:   AdaptiveColor{Light: "#50fa7b", Dark: "#50fa7b"},
	ActiveSelected: AdaptiveColor{Light: "#69ff94", Dark: "#69ff94"},

	SearchDone:          AdaptiveColor{Light: "#6272a4", Dark: "#6272a4"},
	SearchActive:        AdaptiveColor{Light: "#50fa7b", Dark: "#50fa7b"},
	SearchCount:         AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	SearchMeta:          AdaptiveColor{Light: "#6272a4", Dark: "#6272a4"},
	SearchPreviewBorder: AdaptiveColor{Light: "#44475a", Dark: "#6272a4"},
	SearchPreviewDim:    AdaptiveColor{Light: "#6272a4", Dark: "#6272a4"},
}

// themes is the registry of all built-in themes keyed by name.
var themes = map[string]Theme{
	"default": defaultTheme,
	"dracula": draculaTheme,
}
