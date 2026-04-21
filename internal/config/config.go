// Package config owns user-facing presentation/input settings that callers
// pass through to helpers in lower-level packages. Today this is just the
// date format used for displaying and parsing schedules, note separators,
// and error-message placeholders.
//
// On-disk storage stays ISO (2006-01-02) regardless of the configured
// format — config only affects the presentation/input boundary.
//
// For now there is no settings file: the value is a package-level variable
// initialised to the DD-MM-YYYY default. When real settings persistence is
// added later, only this package changes; no callers need to be touched.
//
// DateFormat, DateFormatLabel, and DateRegex panic if the currently
// configured layout is not present in the supported table — this is
// unreachable via the public API today and only a test helper can put the
// package into that state.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// formatEntry describes a supported date format: its user-facing label and
// a regex fragment matching a date rendered in the corresponding Go layout.
type formatEntry struct {
	Label string
	Regex string
}

// supported lists every Go layout we accept as the date format, paired with
// its user-facing label and a regex fragment that matches a date rendered
// in that layout. Adding a new format is a one-line addition here.
var supported = map[string]formatEntry{
	"02-01-2006": {Label: "DD-MM-YYYY", Regex: `\d{2}-\d{2}-\d{4}`},
}

// dateFormat is the currently configured date format as a Go layout.
var dateFormat = "02-01-2006"

// DateFormat returns the currently configured date format as a Go layout
// (e.g. "02-01-2006" for DD-MM-YYYY). Panics if the configured layout is
// not present in the supported table — this is unreachable via the public
// API today and only a test helper can put the package into a bad state.
func DateFormat() string {
	if _, ok := supported[dateFormat]; !ok {
		panic("config: unsupported date format: " + dateFormat)
	}
	return dateFormat
}

// DateFormatLabel returns the user-facing label for the currently
// configured date format (e.g. "DD-MM-YYYY"). See DateFormat for panic
// semantics.
func DateFormatLabel() string {
	entry, ok := supported[dateFormat]
	if !ok {
		panic("config: unsupported date format: " + dateFormat)
	}
	return entry.Label
}

// DateRegex returns a regex fragment that matches a date rendered in the
// currently configured format (e.g. `\d{2}-\d{2}-\d{4}` for DD-MM-YYYY).
// See DateFormat for panic semantics.
func DateRegex() string {
	entry, ok := supported[dateFormat]
	if !ok {
		panic("config: unsupported date format: " + dateFormat)
	}
	return entry.Regex
}

// configJSON is the minimal shape of <MONOLOG_DIR>/.monolog/config.json that
// Theme() cares about. Other fields are ignored.
type configJSON struct {
	Theme string `json:"theme"`
}

// monologConfigPath returns the path to config.json under the monolog data
// directory, using the same MONOLOG_DIR / UserHomeDir logic as cmd.monologDir.
func monologConfigPath() string {
	base := os.Getenv("MONOLOG_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".monolog")
	}
	return filepath.Join(base, ".monolog", "config.json")
}

// Theme returns the name of the active TUI color theme. Resolution order:
//  1. MONOLOG_THEME env var (if non-empty)
//  2. "theme" field in <MONOLOG_DIR>/.monolog/config.json
//  3. "default" (fallback when file is absent or the key is missing)
func Theme() string {
	if env := os.Getenv("MONOLOG_THEME"); env != "" {
		return env
	}
	p := monologConfigPath()
	if p == "" {
		return "default"
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "default"
	}
	var cfg configJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "default"
	}
	if cfg.Theme == "" {
		return "default"
	}
	return cfg.Theme
}
