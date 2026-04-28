// Package config owns user-facing presentation/input settings that callers
// pass through to helpers in lower-level packages. Today this covers the
// date format used for displaying and parsing schedules, note separators,
// and error-message placeholders, as well as the active TUI color theme.
//
// On-disk storage stays ISO (2006-01-02) regardless of the configured
// format — config only affects the presentation/input boundary.
//
// Call Load(monologDir) once at program startup to populate package-level
// vars from config.json. Individual SetDateFormat / SetTheme setters apply
// in-session changes; Save(monologDir) persists the current state back to
// config.json, preserving any keys it does not own.
//
// DateFormat, DateFormatLabel, and DateRegex panic if the currently
// configured layout is not present in the supported table — this is
// unreachable via the public API and only a test helper can put the
// package into that state.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// formatEntry describes a supported date format: its user-facing label and
// a regex fragment matching a date rendered in the corresponding Go layout.
type formatEntry struct {
	Label string
	Regex string
}

// FormatInfo describes one supported date format for settings UI display.
type FormatInfo struct {
	Layout string // Go layout string (e.g. "02-01-2006")
	Label  string // Human-readable label (e.g. "DD-MM-YYYY")
}

// supported lists every Go layout we accept as the date format, paired with
// its user-facing label and a regex fragment that matches a date rendered
// in that layout.
var supported = map[string]formatEntry{
	"02-01-2006": {Label: "DD-MM-YYYY", Regex: `\d{2}-\d{2}-\d{4}`},
	"01-02-2006": {Label: "MM-DD-YYYY", Regex: `\d{2}-\d{2}-\d{4}`},
	"2006-01-02": {Label: "YYYY-MM-DD", Regex: `\d{4}-\d{2}-\d{2}`},
}

// dateFormat is the currently configured date format as a Go layout.
var dateFormat = "02-01-2006"

// EmailConfig captures the configurable email-integration settings stored
// under the optional "email" block in config.json. The zero value
// represents the "feature disabled" state.
//
// Callers (TUI, cmd/email, cmd/done) read this struct once via EmailConfig
// and pass values into the email package — internal/email/ MUST NOT import
// internal/config so it stays as testable as schedule/display/model.
type EmailConfig struct {
	Enabled           bool
	Label             string
	SyncInterval      time.Duration
	MaxPerSync        int
	ClientSecretsPath string
}

// emailCfg holds the in-session email config populated by Load. Defaults
// match the documented behavior: feature disabled, "monolog" label, 5min
// poll interval, soft cap of 100 messages per sync, and a token at
// $XDG_CONFIG_HOME/monolog/gmail_credentials.json (resolved at access time
// to honor environment overrides).
var emailCfg = EmailConfig{
	Enabled:      false,
	Label:        "monolog",
	SyncInterval: 5 * time.Minute,
	MaxPerSync:   100,
	// ClientSecretsPath is resolved lazily in EmailConfig() so that
	// late changes to XDG_CONFIG_HOME / HOME (in tests) are honored.
	ClientSecretsPath: "",
}

// defaultClientSecretsPath returns the default path for the OAuth client
// secrets JSON: $XDG_CONFIG_HOME/monolog/gmail_credentials.json, falling
// back to $HOME/.config/monolog/gmail_credentials.json. Returns an empty
// string only if neither variable can be resolved.
func defaultClientSecretsPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "monolog", "gmail_credentials.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "monolog", "gmail_credentials.json")
}

// Email returns the current email-integration settings. Defaults are
// applied for any keys missing from the file: enabled=false, label="monolog",
// sync_interval_minutes=5, max_per_sync=100, and ClientSecretsPath resolved
// from $XDG_CONFIG_HOME/monolog/gmail_credentials.json (or
// ~/.config/monolog/gmail_credentials.json).
//
// Named Email rather than EmailConfig because Go does not permit a function
// to share its name with the type it returns; callers spell this as
// config.Email() and bind into an EmailConfig value.
func Email() EmailConfig {
	out := emailCfg
	if out.ClientSecretsPath == "" {
		out.ClientSecretsPath = defaultClientSecretsPath()
	}
	return out
}

// DateFormat returns the currently configured date format as a Go layout
// (e.g. "02-01-2006" for DD-MM-YYYY). Panics if the configured layout is
// not present in the supported table — this is unreachable via the public
// API and only a test helper can put the package into a bad state.
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

// AllFormats returns the supported date formats in display order.
// The slice is safe to range over without copying.
func AllFormats() []FormatInfo {
	return []FormatInfo{
		{Layout: "02-01-2006", Label: "DD-MM-YYYY"},
		{Layout: "01-02-2006", Label: "MM-DD-YYYY"},
		{Layout: "2006-01-02", Label: "YYYY-MM-DD"},
	}
}

// SetDateFormat updates the in-session date format. Returns an error if
// layout is not in the supported table.
func SetDateFormat(layout string) error {
	if _, ok := supported[layout]; !ok {
		return &unsupportedFormatError{layout: layout}
	}
	dateFormat = layout
	return nil
}

type unsupportedFormatError struct{ layout string }

func (e *unsupportedFormatError) Error() string {
	return "config: unsupported date format: " + e.layout
}

// configPath returns the absolute path to config.json given the top-level
// monolog data directory (the value of MONOLOG_DIR or ~/.monolog).
func configPath(monologDir string) string {
	return filepath.Join(monologDir, ".monolog", "config.json")
}

// emailBlock mirrors the on-disk JSON shape for the optional "email" block.
// Pointer fields distinguish "absent" from "explicit zero" so we can apply
// defaults only for missing keys (e.g. omitted "enabled" stays false; an
// explicit "enabled": false also stays false).
type emailBlock struct {
	Enabled             *bool   `json:"enabled,omitempty"`
	Label               *string `json:"label,omitempty"`
	SyncIntervalMinutes *int    `json:"sync_interval_minutes,omitempty"`
	MaxPerSync          *int    `json:"max_per_sync,omitempty"`
	ClientSecretsPath   *string `json:"client_secrets_path,omitempty"`
}

// resetEmailCfgToDefaults sets emailCfg back to the documented defaults.
// Called by Load before applying any "email" block from disk so a
// previously-loaded value is not preserved across configurations.
func resetEmailCfgToDefaults() {
	emailCfg = EmailConfig{
		Enabled:           false,
		Label:             "monolog",
		SyncInterval:      5 * time.Minute,
		MaxPerSync:        100,
		ClientSecretsPath: "",
	}
}

// applyEmailBlock overlays a parsed JSON block onto emailCfg, applying
// defaults for omitted fields.
func applyEmailBlock(b emailBlock) {
	if b.Enabled != nil {
		emailCfg.Enabled = *b.Enabled
	}
	if b.Label != nil && *b.Label != "" {
		emailCfg.Label = *b.Label
	}
	if b.SyncIntervalMinutes != nil && *b.SyncIntervalMinutes > 0 {
		emailCfg.SyncInterval = time.Duration(*b.SyncIntervalMinutes) * time.Minute
	}
	if b.MaxPerSync != nil && *b.MaxPerSync > 0 {
		emailCfg.MaxPerSync = *b.MaxPerSync
	}
	if b.ClientSecretsPath != nil && *b.ClientSecretsPath != "" {
		emailCfg.ClientSecretsPath = *b.ClientSecretsPath
	}
}

// Load reads config.json from monologDir and applies stored settings to
// package-level vars. A missing file or unknown field values are silently
// ignored; malformed JSON falls back to defaults. Call once at startup.
func Load(monologDir string) error {
	resetEmailCfgToDefaults()

	data, err := os.ReadFile(configPath(monologDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg struct {
		DateFormat string      `json:"date_format"`
		Email      *emailBlock `json:"email,omitempty"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil // malformed JSON, keep defaults
	}
	if cfg.DateFormat != "" {
		if _, ok := supported[cfg.DateFormat]; ok {
			dateFormat = cfg.DateFormat
		}
	}
	if cfg.Email != nil {
		applyEmailBlock(*cfg.Email)
	}
	return nil
}

// Save writes the given theme and dateFormat values into config.json at
// monologDir. It reads the existing file first so that keys it does not own
// (e.g. "default_schedule", "editor") are preserved. A missing file is
// treated as an empty object.
func Save(monologDir, theme, dateFormatLayout string) error {
	p := configPath(monologDir)

	existing := make(map[string]any)
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &existing) // ignore parse errors, overwrite anyway
	}

	existing["theme"] = theme
	existing["date_format"] = dateFormatLayout

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(out, '\n'), 0o644)
}

// SaveEmail writes the given EmailConfig as the "email" block in config.json
// at monologDir, preserving any keys it does not own (existing
// read-modify-write pattern, mirroring Save). The block is written even when
// ec is the zero value so callers can explicitly persist the disabled state.
//
// The on-disk schema is:
//
//	{
//	  "email": {
//	    "enabled": true,
//	    "label": "monolog",
//	    "sync_interval_minutes": 5,
//	    "max_per_sync": 100,
//	    "client_secrets_path": "~/.config/monolog/gmail_credentials.json"
//	  }
//	}
//
// SaveEmail also updates the in-session emailCfg so subsequent EmailConfig()
// calls reflect the persisted state without requiring a Load.
func SaveEmail(monologDir string, ec EmailConfig) error {
	p := configPath(monologDir)

	existing := make(map[string]any)
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &existing) // ignore parse errors, overwrite anyway
	}

	block := map[string]any{
		"enabled":               ec.Enabled,
		"label":                 ec.Label,
		"sync_interval_minutes": int(ec.SyncInterval / time.Minute),
		"max_per_sync":          ec.MaxPerSync,
		"client_secrets_path":   ec.ClientSecretsPath,
	}
	existing["email"] = block

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, append(out, '\n'), 0o644); err != nil {
		return err
	}

	emailCfg = ec
	return nil
}

// Theme returns the name of the active TUI color theme. Resolution order:
//  1. MONOLOG_THEME env var (if non-empty)
//  2. "theme" field in <MONOLOG_DIR>/.monolog/config.json
//  3. "default" (fallback when file is absent or the key is missing)
func Theme() string {
	if env := os.Getenv("MONOLOG_THEME"); env != "" {
		return env
	}

	// Locate config.json using the same MONOLOG_DIR / UserHomeDir logic as
	// cmd.monologDir.
	base := os.Getenv("MONOLOG_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "default"
		}
		base = filepath.Join(home, ".monolog")
	}
	p := configPath(base)

	data, err := os.ReadFile(p)
	if err != nil {
		return "default"
	}
	var cfg struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "default"
	}
	if cfg.Theme == "" {
		return "default"
	}
	return cfg.Theme
}
