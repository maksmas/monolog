package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// themeFile mirrors Theme for JSON encoding. Fields use snake_case tags to
// match the wider config.json style; names, types and order must match Theme
// exactly so the two convert directly (Theme(tf), themeFile(t)).
type themeFile struct {
	ActiveBorder AdaptiveColor `json:"active_border"`
	ModalBorder  AdaptiveColor `json:"modal_border"`
	Hotkey       AdaptiveColor `json:"hotkey"`
	ActiveTabBg  AdaptiveColor `json:"active_tab_bg"`
	ActiveTabFg  AdaptiveColor `json:"active_tab_fg"`
	TabFg        AdaptiveColor `json:"tab_fg"`
	Borders      AdaptiveColor `json:"borders"`

	NormalText     AdaptiveColor `json:"normal_text"`
	SelectedText   AdaptiveColor `json:"selected_text"`
	DimText        AdaptiveColor `json:"dim_text"`
	GrabText       AdaptiveColor `json:"grab_text"`
	ActiveNormal   AdaptiveColor `json:"active_normal"`
	ActiveSelected AdaptiveColor `json:"active_selected"`

	SearchDone          AdaptiveColor `json:"search_done"`
	SearchActive        AdaptiveColor `json:"search_active"`
	SearchCount         AdaptiveColor `json:"search_count"`
	SearchMeta          AdaptiveColor `json:"search_meta"`
	SearchPreviewBorder AdaptiveColor `json:"search_preview_border"`
	SearchPreviewDim    AdaptiveColor `json:"search_preview_dim"`
}

// themeFileFields enumerates every (json-key, accessor) pair. Keeping this
// next to themeFile means missing-key detection and empty-value detection
// both use the same schema with no drift risk.
func themeFileFields(tf *themeFile) []themeField {
	return []themeField{
		{"active_border", &tf.ActiveBorder},
		{"modal_border", &tf.ModalBorder},
		{"hotkey", &tf.Hotkey},
		{"active_tab_bg", &tf.ActiveTabBg},
		{"active_tab_fg", &tf.ActiveTabFg},
		{"tab_fg", &tf.TabFg},
		{"borders", &tf.Borders},
		{"normal_text", &tf.NormalText},
		{"selected_text", &tf.SelectedText},
		{"dim_text", &tf.DimText},
		{"grab_text", &tf.GrabText},
		{"active_normal", &tf.ActiveNormal},
		{"active_selected", &tf.ActiveSelected},
		{"search_done", &tf.SearchDone},
		{"search_active", &tf.SearchActive},
		{"search_count", &tf.SearchCount},
		{"search_meta", &tf.SearchMeta},
		{"search_preview_border", &tf.SearchPreviewBorder},
		{"search_preview_dim", &tf.SearchPreviewDim},
	}
}

type themeField struct {
	key   string
	color *AdaptiveColor
}

// toTheme converts the on-disk form to the in-memory Theme. Returns an
// error naming the first field that has an empty light or dark value.
func (tf themeFile) toTheme() (Theme, error) {
	for _, f := range themeFileFields(&tf) {
		if f.color.Light == "" || f.color.Dark == "" {
			return Theme{}, fmt.Errorf("empty color for field %q", f.key)
		}
	}
	return Theme(tf), nil
}

// themesDir returns the filesystem location of user theme files, given the
// top-level monolog data directory.
func themesDir(monologDir string) string {
	return filepath.Join(monologDir, ".monolog", "themes")
}

// loadUserThemes scans the themes directory under monologDir and returns
// every valid theme file as a Theme indexed by filename (sans .json).
// Per-file errors are collected and returned alongside — a single bad file
// never hides the rest. A missing directory returns an empty map and nil
// errors (no error to report yet; bootstrap handles creation).
func loadUserThemes(monologDir string) (map[string]Theme, []error) {
	dir := themesDir(monologDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]Theme{}, nil
		}
		return map[string]Theme{}, []error{fmt.Errorf("themes dir: %w", err)}
	}

	// Sort entries so error ordering is deterministic across platforms —
	// ReadDir returns entries in directory order, which is not portable.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	out := make(map[string]Theme)
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		themeName := strings.TrimSuffix(name, ".json")
		theme, err := loadThemeFile(filepath.Join(dir, name), themeName)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out[themeName] = theme
	}
	return out, errs
}

// loadThemeFile reads a single theme file and returns the parsed Theme.
// Errors are prefixed with the theme name so callers can report them
// without extra context.
func loadThemeFile(path, themeName string) (Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Theme{}, fmt.Errorf("theme %q: read: %w", themeName, err)
	}

	// Decode twice: first into a raw map so we can enforce that every
	// required key is present (empty-map values aren't rejected by the
	// typed decode), then into the typed struct for value extraction.
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Theme{}, fmt.Errorf("theme %q: invalid JSON: %w", themeName, err)
	}
	var tf themeFile
	for _, f := range themeFileFields(&tf) {
		if _, ok := raw[f.key]; !ok {
			return Theme{}, fmt.Errorf("theme %q: missing field %q", themeName, f.key)
		}
	}
	if err := json.Unmarshal(data, &tf); err != nil {
		return Theme{}, fmt.Errorf("theme %q: invalid JSON: %w", themeName, err)
	}
	theme, err := tf.toTheme()
	if err != nil {
		return Theme{}, fmt.Errorf("theme %q: %w", themeName, err)
	}
	return theme, nil
}

// initThemes loads user theme files from monologDir and merges them into
// the package-level themes map alongside the built-ins. Returns every
// error accumulated during loading (parse errors, missing fields, and
// collisions with built-in theme names). Built-ins always win — a user
// file named "default.json" or "dracula.json" is skipped with an error.
func initThemes(monologDir string) []error {
	user, errs := loadUserThemes(monologDir)

	// Iterate user themes in deterministic order so collision errors are
	// reported in a predictable sequence.
	names := make([]string, 0, len(user))
	for n := range user {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		if _, isBuiltin := builtinThemes[name]; isBuiltin {
			errs = append(errs,
				fmt.Errorf("theme %q: collides with built-in, skipping", name))
			continue
		}
		themes[name] = user[name]
	}
	return errs
}

// builtinThemes lists the theme names that are compiled in and therefore
// cannot be overridden from disk. Keep in sync with the themes map in
// theme.go.
var builtinThemes = map[string]struct{}{
	"default": {},
	"dracula": {},
}

// bootstrapExampleTheme ensures users have a starting-point theme file
// to copy and edit. If the themes directory does not exist, it is created
// and example.json is written with the full defaultTheme. If the directory
// already exists (even empty, even with example.json missing) the call is
// a no-op — deleting example.json cleanly is a deliberate user choice we
// must respect.
func bootstrapExampleTheme(monologDir string) error {
	dir := themesDir(monologDir)
	if _, err := os.Stat(dir); err == nil {
		return nil // directory exists; nothing to do
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat themes dir: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create themes dir: %w", err)
	}
	data, err := json.MarshalIndent(themeFile(defaultTheme), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal example theme: %w", err)
	}
	path := filepath.Join(dir, "example.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write example theme: %w", err)
	}
	return nil
}
