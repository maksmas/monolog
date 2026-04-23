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
	"strings"
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

// SlackConfig holds user-facing Slack integration settings. Zero value is
// the disabled default: polling off, 60s interval, no workspace, tag
// channels into task tags.
type SlackConfig struct {
	Enabled             bool
	PollIntervalSeconds int
	Workspace           string
	ChannelAsTag        bool
}

// slackCfg is the currently configured Slack block, populated by Load from
// config.json (defaults when absent). Module-level to match the dateFormat
// pattern; reading and writing is single-goroutine at process start.
var slackCfg = SlackConfig{
	Enabled:             false,
	PollIntervalSeconds: 60,
	Workspace:           "",
	ChannelAsTag:        true,
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

// Load reads config.json from monologDir and applies stored settings to
// package-level vars. A missing file or unknown field values are silently
// ignored; malformed JSON falls back to defaults. Call once at startup.
func Load(monologDir string) error {
	// Reset slackCfg to defaults before we (potentially) overwrite it, so
	// repeated Load calls in tests start from a clean baseline.
	slackCfg = SlackConfig{
		Enabled:             false,
		PollIntervalSeconds: 60,
		Workspace:           "",
		ChannelAsTag:        true,
	}

	data, err := os.ReadFile(configPath(monologDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg struct {
		DateFormat string `json:"date_format"`
		Slack      *struct {
			Enabled             *bool   `json:"enabled"`
			PollIntervalSeconds *int    `json:"poll_interval_seconds"`
			Workspace           *string `json:"workspace"`
			ChannelAsTag        *bool   `json:"channel_as_tag"`
		} `json:"slack"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil // malformed JSON, keep defaults
	}
	if cfg.DateFormat != "" {
		if _, ok := supported[cfg.DateFormat]; ok {
			dateFormat = cfg.DateFormat
		}
	}
	if cfg.Slack != nil {
		if cfg.Slack.Enabled != nil {
			slackCfg.Enabled = *cfg.Slack.Enabled
		}
		if cfg.Slack.PollIntervalSeconds != nil && *cfg.Slack.PollIntervalSeconds > 0 {
			slackCfg.PollIntervalSeconds = *cfg.Slack.PollIntervalSeconds
		}
		if cfg.Slack.Workspace != nil {
			slackCfg.Workspace = *cfg.Slack.Workspace
		}
		if cfg.Slack.ChannelAsTag != nil {
			slackCfg.ChannelAsTag = *cfg.Slack.ChannelAsTag
		}
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

// Slack returns the currently configured Slack block. Call Load first to
// populate from config.json; before Load (or when the block is absent) the
// returned zero-ish value carries safe defaults: Enabled=false,
// PollIntervalSeconds=60, Workspace="", ChannelAsTag=true.
func Slack() SlackConfig {
	return slackCfg
}

// SlackToken resolves the active Slack user OAuth token. Resolution order:
//  1. MONOLOG_SLACK_TOKEN env var (source="env")
//  2. <MONOLOG_DIR>/.monolog/slack_token file, trimmed (source="file")
//  3. Neither → ("", "", nil); caller treats as disabled.
//
// File-read errors other than ENOENT are returned as-is so callers can
// surface a real problem (e.g. permission denied). Parameterless and read
// per call — matches the Theme() pattern.
func SlackToken() (token string, source string, err error) {
	if env := strings.TrimSpace(os.Getenv("MONOLOG_SLACK_TOKEN")); env != "" {
		return env, "env", nil
	}

	base := os.Getenv("MONOLOG_DIR")
	if base == "" {
		home, hErr := os.UserHomeDir()
		if hErr != nil {
			return "", "", nil
		}
		base = filepath.Join(home, ".monolog")
	}
	path := filepath.Join(base, ".monolog", "slack_token")
	data, rErr := os.ReadFile(path)
	if os.IsNotExist(rErr) {
		return "", "", nil
	}
	if rErr != nil {
		return "", "", rErr
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", "", nil
	}
	return tok, "file", nil
}

// SetSlackWorkspace writes slack.workspace into config.json via a
// read-modify-write that preserves all other top-level keys (and other slack
// fields). Also updates the in-memory slackCfg so Slack() reflects the
// change in the current process.
func SetSlackWorkspace(monologDir, workspace string) error {
	if err := mutateSlackConfig(monologDir, func(s map[string]any) {
		s["workspace"] = workspace
	}); err != nil {
		return err
	}
	slackCfg.Workspace = workspace
	return nil
}

// SetSlackEnabled toggles slack.enabled in config.json using the same
// read-modify-write approach as SetSlackWorkspace. Updates slackCfg in
// memory as well.
func SetSlackEnabled(monologDir string, enabled bool) error {
	if err := mutateSlackConfig(monologDir, func(s map[string]any) {
		s["enabled"] = enabled
	}); err != nil {
		return err
	}
	slackCfg.Enabled = enabled
	return nil
}

// mutateSlackConfig is a shared helper for the SetSlack* setters. It loads
// config.json (missing file treated as empty object), ensures a top-level
// "slack" object exists (creating one with defaults when absent), applies
// the caller's mutation, and writes the whole thing back while preserving
// unknown keys.
func mutateSlackConfig(monologDir string, mutate func(map[string]any)) error {
	p := configPath(monologDir)

	existing := make(map[string]any)
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &existing) // ignore parse errors, overwrite anyway
	}

	var slackBlock map[string]any
	if raw, ok := existing["slack"].(map[string]any); ok {
		slackBlock = raw
	} else {
		slackBlock = make(map[string]any)
	}
	mutate(slackBlock)
	existing["slack"] = slackBlock

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(out, '\n'), 0o644)
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
