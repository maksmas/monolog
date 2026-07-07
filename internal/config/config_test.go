package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// setDateFormatForTest swaps the package-level dateFormat for the duration
// of a test and returns a restore function. The caller is responsible for
// calling the restore function (typically via t.Cleanup). If layout is not
// already present in supported, the caller may pass a non-nil entry to
// register it for the duration of the test as well — pass nil to leave
// supported unchanged.
func setDateFormatForTest(layout string, entry *formatEntry) func() {
	prevFormat := dateFormat
	var hadPrev bool
	var prevEntry formatEntry
	if entry != nil {
		prevEntry, hadPrev = supported[layout]
		supported[layout] = *entry
	}
	dateFormat = layout
	return func() {
		dateFormat = prevFormat
		if entry != nil {
			if hadPrev {
				supported[layout] = prevEntry
			} else {
				delete(supported, layout)
			}
		}
	}
}

func TestDefaultAccessors(t *testing.T) {
	if got, want := DateFormat(), "02-01-2006"; got != want {
		t.Errorf("DateFormat() = %q, want %q", got, want)
	}
	if got, want := DateFormatLabel(), "DD-MM-YYYY"; got != want {
		t.Errorf("DateFormatLabel() = %q, want %q", got, want)
	}
	if got, want := DateRegex(), `\d{2}-\d{2}-\d{4}`; got != want {
		t.Errorf("DateRegex() = %q, want %q", got, want)
	}
}

func TestDateRegexCompiles(t *testing.T) {
	re, err := regexp.Compile(DateRegex())
	if err != nil {
		t.Fatalf("DateRegex() did not compile: %v", err)
	}
	if !re.MatchString("18-04-2026") {
		t.Errorf("DateRegex() did not match %q", "18-04-2026")
	}
	if re.MatchString("2026-04-18") {
		t.Errorf("DateRegex() unexpectedly matched ISO %q", "2026-04-18")
	}
}

func TestAccessorsRespectAlternativeLayout(t *testing.T) {
	restore := setDateFormatForTest("2006-01-02", &formatEntry{
		Label: "YYYY-MM-DD",
		Regex: `\d{4}-\d{2}-\d{2}`,
	})
	t.Cleanup(restore)

	if got, want := DateFormat(), "2006-01-02"; got != want {
		t.Errorf("DateFormat() = %q, want %q", got, want)
	}
	if got, want := DateFormatLabel(), "YYYY-MM-DD"; got != want {
		t.Errorf("DateFormatLabel() = %q, want %q", got, want)
	}
	if got, want := DateRegex(), `\d{4}-\d{2}-\d{2}`; got != want {
		t.Errorf("DateRegex() = %q, want %q", got, want)
	}
}

func TestAccessorsRestoredAfterTest(t *testing.T) {
	// Self-contained verification that setDateFormatForTest's returned
	// restore func actually restores the prior format and removes any
	// temporarily registered supported entry. Previously this test relied
	// on the implicit ordering of a sibling test's cleanup running first,
	// which would have silently become meaningless under t.Run reordering
	// or sharding.
	before := dateFormat
	_, hadPrev := supported["2006-01-02"]

	restore := setDateFormatForTest("2006-01-02", &formatEntry{
		Label: "YYYY-MM-DD",
		Regex: `\d{4}-\d{2}-\d{2}`,
	})
	if got := DateFormat(); got != "2006-01-02" {
		t.Fatalf("DateFormat() during override = %q, want %q", got, "2006-01-02")
	}

	restore()

	if got := DateFormat(); got != before {
		t.Errorf("DateFormat() after restore = %q, want %q", got, before)
	}
	if _, present := supported["2006-01-02"]; present != hadPrev {
		t.Errorf("restore did not roll back supported[%q] (present=%v, hadPrev=%v)",
			"2006-01-02", present, hadPrev)
	}
}

func TestUnknownLayoutPanics(t *testing.T) {
	restore := setDateFormatForTest("bogus-layout", nil)
	t.Cleanup(restore)

	assertPanics(t, "DateFormat", func() { _ = DateFormat() })
	assertPanics(t, "DateFormatLabel", func() { _ = DateFormatLabel() })
	assertPanics(t, "DateRegex", func() { _ = DateRegex() })
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("%s did not panic for unsupported layout", name)
		}
	}()
	fn()
}

// resetDateFormat restores the package-level dateFormat to the default at
// test cleanup. Always call this when a test modifies dateFormat directly or
// via SetDateFormat.
func resetDateFormat(t *testing.T) {
	t.Helper()
	prev := dateFormat
	t.Cleanup(func() { dateFormat = prev })
}

// writeConfigJSON writes a config.json with the given content into
// tmpDir/.monolog/config.json and sets MONOLOG_DIR=tmpDir for the test.
func writeConfigJSON(t *testing.T, tmpDir string, content map[string]string) {
	t.Helper()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir .monolog: %v", err)
	}
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(monologDir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv("MONOLOG_DIR", tmpDir)
}

func TestThemeDefaultWhenNoFile(t *testing.T) {
	// Point MONOLOG_DIR at a temp dir with no .monolog/ subdirectory.
	t.Setenv("MONOLOG_DIR", t.TempDir())
	t.Setenv("MONOLOG_THEME", "")
	if got := Theme(); got != "default" {
		t.Errorf("Theme() = %q, want %q", got, "default")
	}
}

func TestThemeReadFromFile(t *testing.T) {
	t.Setenv("MONOLOG_THEME", "") // ensure env var does not interfere
	writeConfigJSON(t, t.TempDir(), map[string]string{"theme": "dracula"})
	if got := Theme(); got != "dracula" {
		t.Errorf("Theme() = %q, want %q", got, "dracula")
	}
}

func TestThemeEnvVarOverridesFile(t *testing.T) {
	// File says "dracula", env var says "default" — env var must win.
	writeConfigJSON(t, t.TempDir(), map[string]string{"theme": "dracula"})
	t.Setenv("MONOLOG_THEME", "default")
	if got := Theme(); got != "default" {
		t.Errorf("Theme() = %q, want %q when env var set", got, "default")
	}
}

func TestThemeEnvVarAloneIsRespected(t *testing.T) {
	// No config file at all, only env var.
	t.Setenv("MONOLOG_DIR", t.TempDir())
	t.Setenv("MONOLOG_THEME", "dracula")
	if got := Theme(); got != "dracula" {
		t.Errorf("Theme() = %q, want %q", got, "dracula")
	}
}

func TestThemeDefaultWhenMalformedJSON(t *testing.T) {
	t.Setenv("MONOLOG_THEME", "")
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir .monolog: %v", err)
	}
	// Write clearly invalid JSON.
	if err := os.WriteFile(filepath.Join(monologDir, "config.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv("MONOLOG_DIR", tmpDir)
	if got := Theme(); got != "default" {
		t.Errorf("Theme() = %q, want %q for malformed JSON", got, "default")
	}
}

func TestThemeDefaultWhenKeyMissingInFile(t *testing.T) {
	t.Setenv("MONOLOG_THEME", "")
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir .monolog: %v", err)
	}
	// Write a config.json that has no "theme" key.
	data := []byte(`{"default_schedule":"today","editor":"$EDITOR"}`)
	if err := os.WriteFile(filepath.Join(monologDir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv("MONOLOG_DIR", tmpDir)
	if got := Theme(); got != "default" {
		t.Errorf("Theme() = %q, want %q when key absent", got, "default")
	}
}

// --- Load ---

func TestLoadSetsDateFormatFromFile(t *testing.T) {
	resetDateFormat(t)
	tmpDir := t.TempDir()
	writeConfigJSON(t, tmpDir, map[string]string{"date_format": "2006-01-02"})
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := DateFormat(), "2006-01-02"; got != want {
		t.Errorf("DateFormat() = %q, want %q after Load", got, want)
	}
}

func TestLoadIgnoresMissingFile(t *testing.T) {
	resetDateFormat(t)
	tmpDir := t.TempDir() // no .monolog dir at all
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load returned error for missing file: %v", err)
	}
	if got, want := DateFormat(), "02-01-2006"; got != want {
		t.Errorf("DateFormat() = %q, want default %q", got, want)
	}
}

func TestLoadIgnoresUnknownDateFormat(t *testing.T) {
	resetDateFormat(t)
	tmpDir := t.TempDir()
	writeConfigJSON(t, tmpDir, map[string]string{"date_format": "bogus-layout"})
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := DateFormat(), "02-01-2006"; got != want {
		t.Errorf("DateFormat() = %q, want default %q after unknown layout", got, want)
	}
}

func TestLoadIgnoresMalformedJSON(t *testing.T) {
	resetDateFormat(t)
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(monologDir, "config.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load returned error for malformed JSON: %v", err)
	}
	if got, want := DateFormat(), "02-01-2006"; got != want {
		t.Errorf("DateFormat() = %q, want default %q", got, want)
	}
}

// --- SetDateFormat ---

func TestSetDateFormatAcceptsSupported(t *testing.T) {
	resetDateFormat(t)
	for _, f := range AllFormats() {
		if err := SetDateFormat(f.Layout); err != nil {
			t.Errorf("SetDateFormat(%q) error: %v", f.Layout, err)
		}
		if got := DateFormat(); got != f.Layout {
			t.Errorf("DateFormat() = %q, want %q", got, f.Layout)
		}
	}
}

func TestSetDateFormatRejectsUnknown(t *testing.T) {
	resetDateFormat(t)
	if err := SetDateFormat("bogus"); err == nil {
		t.Error("SetDateFormat(bogus) expected error, got nil")
	}
	if got, want := DateFormat(), "02-01-2006"; got != want {
		t.Errorf("DateFormat() = %q after rejection, want unchanged %q", got, want)
	}
}

// --- Save ---

func TestSaveWritesThemeAndDateFormat(t *testing.T) {
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := Save(tmpDir, "dracula", "2006-01-02"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(monologDir, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := cfg["theme"]; got != "dracula" {
		t.Errorf("theme = %v, want dracula", got)
	}
	if got := cfg["date_format"]; got != "2006-01-02" {
		t.Errorf("date_format = %v, want 2006-01-02", got)
	}
}

func TestSavePreservesUnknownKeys(t *testing.T) {
	tmpDir := t.TempDir()
	writeConfigJSON(t, tmpDir, map[string]string{
		"default_schedule": "today",
		"editor":           "$EDITOR",
		"theme":            "default",
	})
	if err := Save(tmpDir, "dracula", "02-01-2006"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".monolog", "config.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg["default_schedule"] != "today" {
		t.Errorf("default_schedule lost after Save, got %v", cfg["default_schedule"])
	}
	if cfg["editor"] != "$EDITOR" {
		t.Errorf("editor lost after Save, got %v", cfg["editor"])
	}
}

func TestSaveCreatesFilWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No config.json exists yet.
	if err := Save(tmpDir, "default", "02-01-2006"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(monologDir, "config.json")); err != nil {
		t.Errorf("config.json not created: %v", err)
	}
}

// --- EmailConfig ---

// resetEmailCfg restores the package-level emailCfg to its defaults at
// test cleanup. Always call this when a test modifies emailCfg directly
// or via Load/SaveEmail.
func resetEmailCfg(t *testing.T) {
	t.Helper()
	prev := emailCfg
	t.Cleanup(func() { emailCfg = prev })
}

func TestEmailConfigDefaultsWhenNoBlock(t *testing.T) {
	resetEmailCfg(t)
	resetDateFormat(t)
	tmpDir := t.TempDir()
	// Write a config.json with no "email" block at all.
	writeConfigJSON(t, tmpDir, map[string]string{"theme": "default"})
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Email()
	if got.Enabled {
		t.Errorf("Enabled = true, want false (default)")
	}
	if got.Label != "monolog" {
		t.Errorf("Label = %q, want %q", got.Label, "monolog")
	}
	if got.SyncInterval != 5*time.Minute {
		t.Errorf("SyncInterval = %v, want %v", got.SyncInterval, 5*time.Minute)
	}
	if got.MaxPerSync != 100 {
		t.Errorf("MaxPerSync = %d, want 100", got.MaxPerSync)
	}
	if got.ClientSecretsPath == "" {
		t.Errorf("ClientSecretsPath empty, want a default path")
	}
}

func TestEmailConfigDefaultsWhenFileMissing(t *testing.T) {
	resetEmailCfg(t)
	tmpDir := t.TempDir() // no config.json at all
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Email()
	if got.Enabled {
		t.Errorf("Enabled = true on missing file, want false")
	}
	if got.Label != "monolog" {
		t.Errorf("Label = %q, want %q", got.Label, "monolog")
	}
}

func TestEmailConfigUsesXDGConfigHomeForDefaultPath(t *testing.T) {
	resetEmailCfg(t)
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	got := Email()
	want := filepath.Join(tmp, "monolog", "gmail_credentials.json")
	if got.ClientSecretsPath != want {
		t.Errorf("ClientSecretsPath = %q, want %q", got.ClientSecretsPath, want)
	}
}

func TestLoadAppliesFullEmailBlock(t *testing.T) {
	resetEmailCfg(t)
	resetDateFormat(t)
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	doc := map[string]any{
		"email": map[string]any{
			"enabled":               true,
			"label":                 "todo",
			"sync_interval_minutes": 15,
			"max_per_sync":          25,
			"client_secrets_path":   "/tmp/creds.json",
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(monologDir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Email()
	if !got.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if got.Label != "todo" {
		t.Errorf("Label = %q, want %q", got.Label, "todo")
	}
	if got.SyncInterval != 15*time.Minute {
		t.Errorf("SyncInterval = %v, want %v", got.SyncInterval, 15*time.Minute)
	}
	if got.MaxPerSync != 25 {
		t.Errorf("MaxPerSync = %d, want 25", got.MaxPerSync)
	}
	if got.ClientSecretsPath != "/tmp/creds.json" {
		t.Errorf("ClientSecretsPath = %q, want %q", got.ClientSecretsPath, "/tmp/creds.json")
	}
}

func TestLoadAppliesPartialEmailBlock(t *testing.T) {
	// Only "enabled" set — other fields should keep defaults.
	resetEmailCfg(t)
	resetDateFormat(t)
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	doc := map[string]any{
		"email": map[string]any{
			"enabled": true,
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(monologDir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Email()
	if !got.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if got.Label != "monolog" {
		t.Errorf("Label = %q, want default %q", got.Label, "monolog")
	}
	if got.SyncInterval != 5*time.Minute {
		t.Errorf("SyncInterval = %v, want default %v", got.SyncInterval, 5*time.Minute)
	}
	if got.MaxPerSync != 100 {
		t.Errorf("MaxPerSync = %d, want default 100", got.MaxPerSync)
	}
}

func TestLoadResetsEmailBlockBetweenCalls(t *testing.T) {
	// First call loads enabled=true; second call points at a directory with
	// no email block at all and must NOT carry the previous value forward.
	resetEmailCfg(t)
	resetDateFormat(t)

	enabledDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(enabledDir, ".monolog"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	doc := map[string]any{"email": map[string]any{"enabled": true}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(enabledDir, ".monolog", "config.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Load(enabledDir); err != nil {
		t.Fatalf("Load (enabled): %v", err)
	}
	if !Email().Enabled {
		t.Fatalf("expected Enabled=true after first Load")
	}

	// Second Load against a fresh dir without an email block.
	emptyDir := t.TempDir()
	writeConfigJSON(t, emptyDir, map[string]string{"theme": "default"})
	if err := Load(emptyDir); err != nil {
		t.Fatalf("Load (empty): %v", err)
	}
	if Email().Enabled {
		t.Errorf("Enabled = true after Load with no email block, want false (carry-over leak)")
	}
}

func TestSaveEmailRoundtripsThroughLoad(t *testing.T) {
	resetEmailCfg(t)
	resetDateFormat(t)
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := EmailConfig{
		Enabled:           true,
		Label:             "inbox",
		SyncInterval:      10 * time.Minute,
		MaxPerSync:        50,
		ClientSecretsPath: "/tmp/secrets.json",
	}
	if err := SaveEmail(tmpDir, want); err != nil {
		t.Fatalf("SaveEmail: %v", err)
	}

	// In-session value reflects the save without needing Load.
	if got := Email(); got != want {
		t.Errorf("Email() after SaveEmail = %+v, want %+v", got, want)
	}

	// Reset and Load to verify on-disk representation roundtrips.
	resetEmailCfgToDefaults()
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Email(); got != want {
		t.Errorf("Email() after Load = %+v, want %+v", got, want)
	}
}

func TestSaveEmailPreservesUnknownTopLevelKeys(t *testing.T) {
	resetEmailCfg(t)
	tmpDir := t.TempDir()
	writeConfigJSON(t, tmpDir, map[string]string{
		"default_schedule": "today",
		"editor":           "$EDITOR",
		"theme":            "dracula",
		"date_format":      "2006-01-02",
	})
	ec := EmailConfig{
		Enabled:           true,
		Label:             "monolog",
		SyncInterval:      5 * time.Minute,
		MaxPerSync:        100,
		ClientSecretsPath: "/tmp/x.json",
	}
	if err := SaveEmail(tmpDir, ec); err != nil {
		t.Fatalf("SaveEmail: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, ".monolog", "config.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg["default_schedule"] != "today" {
		t.Errorf("default_schedule lost after SaveEmail, got %v", cfg["default_schedule"])
	}
	if cfg["editor"] != "$EDITOR" {
		t.Errorf("editor lost after SaveEmail, got %v", cfg["editor"])
	}
	if cfg["theme"] != "dracula" {
		t.Errorf("theme lost after SaveEmail, got %v", cfg["theme"])
	}
	if cfg["date_format"] != "2006-01-02" {
		t.Errorf("date_format lost after SaveEmail, got %v", cfg["date_format"])
	}
	emailMap, ok := cfg["email"].(map[string]any)
	if !ok {
		t.Fatalf("email block missing or wrong type: %T %v", cfg["email"], cfg["email"])
	}
	if emailMap["enabled"] != true {
		t.Errorf("email.enabled = %v, want true", emailMap["enabled"])
	}
}

func TestSavePreservesEmailBlock(t *testing.T) {
	// Write an email block via SaveEmail, then run the date/theme Save and
	// verify the email block survives — the read-modify-write pattern in
	// Save must not strip foreign keys.
	resetEmailCfg(t)
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveEmail(tmpDir, EmailConfig{
		Enabled:           true,
		Label:             "monolog",
		SyncInterval:      5 * time.Minute,
		MaxPerSync:        100,
		ClientSecretsPath: "/tmp/c.json",
	}); err != nil {
		t.Fatalf("SaveEmail: %v", err)
	}
	if err := Save(tmpDir, "dracula", "2006-01-02"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(monologDir, "config.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := cfg["email"]; !ok {
		t.Errorf("email block lost after Save, expected preserved")
	}
}

// --- TelegramConfig ---

// resetTelegramCfg restores the package-level telegramCfg to its defaults
// at test cleanup. Always call this when a test modifies telegramCfg
// directly or via Load/SaveTelegram.
func resetTelegramCfg(t *testing.T) {
	t.Helper()
	prev := telegramCfg
	t.Cleanup(func() { telegramCfg = prev })
}

func TestTelegramDefaults(t *testing.T) {
	resetTelegramCfg(t)
	resetDateFormat(t)
	tmpDir := t.TempDir()
	// Write a config.json with no "telegram" block at all.
	writeConfigJSON(t, tmpDir, map[string]string{"theme": "default"})
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Telegram()
	if got.Enabled {
		t.Errorf("Enabled = true, want false (default)")
	}
	if got.AllowedUserIDs != nil {
		t.Errorf("AllowedUserIDs = %v, want nil", got.AllowedUserIDs)
	}
	if got.PullInterval != 30*time.Second {
		t.Errorf("PullInterval = %v, want %v", got.PullInterval, 30*time.Second)
	}
	if got.BrowseLimit != 20 {
		t.Errorf("BrowseLimit = %d, want 20", got.BrowseLimit)
	}
}

func TestLoadTelegramBlock(t *testing.T) {
	resetTelegramCfg(t)
	resetDateFormat(t)
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	doc := map[string]any{
		"telegram": map[string]any{
			"enabled":               true,
			"allowed_user_ids":      []int64{123, 456},
			"pull_interval_seconds": 45,
			"browse_limit":          50,
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(monologDir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Telegram()
	if !got.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if len(got.AllowedUserIDs) != 2 || got.AllowedUserIDs[0] != 123 || got.AllowedUserIDs[1] != 456 {
		t.Errorf("AllowedUserIDs = %v, want [123 456]", got.AllowedUserIDs)
	}
	if got.PullInterval != 45*time.Second {
		t.Errorf("PullInterval = %v, want %v", got.PullInterval, 45*time.Second)
	}
	if got.BrowseLimit != 50 {
		t.Errorf("BrowseLimit = %d, want 50", got.BrowseLimit)
	}
}

func TestSaveTelegramRoundTrip(t *testing.T) {
	resetTelegramCfg(t)
	resetDateFormat(t)
	tmpDir := t.TempDir()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := TelegramConfig{
		Enabled:        true,
		AllowedUserIDs: []int64{789},
		PullInterval:   60 * time.Second,
		BrowseLimit:    15,
	}
	if err := SaveTelegram(tmpDir, want); err != nil {
		t.Fatalf("SaveTelegram: %v", err)
	}

	// In-session value reflects the save without needing Load.
	got := Telegram()
	if got.Enabled != want.Enabled || got.PullInterval != want.PullInterval || got.BrowseLimit != want.BrowseLimit {
		t.Errorf("Telegram() after SaveTelegram = %+v, want %+v", got, want)
	}
	if len(got.AllowedUserIDs) != 1 || got.AllowedUserIDs[0] != 789 {
		t.Errorf("AllowedUserIDs = %v, want [789]", got.AllowedUserIDs)
	}

	// Reset and Load to verify on-disk representation roundtrips.
	resetTelegramCfgToDefaults()
	if err := Load(tmpDir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got = Telegram()
	if got.Enabled != want.Enabled || got.PullInterval != want.PullInterval || got.BrowseLimit != want.BrowseLimit {
		t.Errorf("Telegram() after Load = %+v, want %+v", got, want)
	}
	if len(got.AllowedUserIDs) != 1 || got.AllowedUserIDs[0] != 789 {
		t.Errorf("AllowedUserIDs after Load = %v, want [789]", got.AllowedUserIDs)
	}
}

func TestSaveTelegramPreservesForeignKeys(t *testing.T) {
	resetTelegramCfg(t)
	resetEmailCfg(t)
	tmpDir := t.TempDir()
	writeConfigJSON(t, tmpDir, map[string]string{
		"default_schedule": "today",
		"editor":           "$EDITOR",
		"theme":            "dracula",
		"date_format":      "2006-01-02",
	})
	// First add an email block so we verify both email and date_format survive.
	if err := SaveEmail(tmpDir, EmailConfig{
		Enabled:           true,
		Label:             "monolog",
		SyncInterval:      5 * time.Minute,
		MaxPerSync:        100,
		ClientSecretsPath: "/tmp/x.json",
	}); err != nil {
		t.Fatalf("SaveEmail: %v", err)
	}

	tc := TelegramConfig{
		Enabled:        true,
		AllowedUserIDs: []int64{1},
		PullInterval:   30 * time.Second,
		BrowseLimit:    20,
	}
	if err := SaveTelegram(tmpDir, tc); err != nil {
		t.Fatalf("SaveTelegram: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".monolog", "config.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg["default_schedule"] != "today" {
		t.Errorf("default_schedule lost, got %v", cfg["default_schedule"])
	}
	if cfg["editor"] != "$EDITOR" {
		t.Errorf("editor lost, got %v", cfg["editor"])
	}
	if cfg["theme"] != "dracula" {
		t.Errorf("theme lost, got %v", cfg["theme"])
	}
	if cfg["date_format"] != "2006-01-02" {
		t.Errorf("date_format lost, got %v", cfg["date_format"])
	}
	if _, ok := cfg["email"]; !ok {
		t.Errorf("email block lost after SaveTelegram")
	}
	telegramMap, ok := cfg["telegram"].(map[string]any)
	if !ok {
		t.Fatalf("telegram block missing or wrong type: %T %v", cfg["telegram"], cfg["telegram"])
	}
	if telegramMap["enabled"] != true {
		t.Errorf("telegram.enabled = %v, want true", telegramMap["enabled"])
	}
}

func TestApplyTelegramBlockValueClamps(t *testing.T) {
	resetTelegramCfg(t)

	// Zero pull_interval_seconds and browse_limit fall back to defaults;
	// explicit enabled:false stays false; nil AllowedUserIDs preserved.
	resetTelegramCfgToDefaults()
	zero := 0
	enabled := false
	applyTelegramBlock(telegramBlock{
		Enabled:             &enabled,
		PullIntervalSeconds: &zero,
		BrowseLimit:         &zero,
	})
	got := Telegram()
	if got.Enabled {
		t.Errorf("Enabled = true after explicit false, want false")
	}
	if got.PullInterval != 30*time.Second {
		t.Errorf("PullInterval = %v after zero clamp, want default %v", got.PullInterval, 30*time.Second)
	}
	if got.BrowseLimit != 20 {
		t.Errorf("BrowseLimit = %d after zero clamp, want default 20", got.BrowseLimit)
	}

	// Negative values also fall back.
	resetTelegramCfgToDefaults()
	neg := -5
	applyTelegramBlock(telegramBlock{
		PullIntervalSeconds: &neg,
		BrowseLimit:         &neg,
	})
	got = Telegram()
	if got.PullInterval != 30*time.Second {
		t.Errorf("PullInterval = %v after negative clamp, want default %v", got.PullInterval, 30*time.Second)
	}
	if got.BrowseLimit != 20 {
		t.Errorf("BrowseLimit = %d after negative clamp, want default 20", got.BrowseLimit)
	}

	// Empty AllowedUserIDs is preserved (means "no one allowed").
	resetTelegramCfgToDefaults()
	empty := []int64{}
	applyTelegramBlock(telegramBlock{AllowedUserIDs: &empty})
	got = Telegram()
	if got.AllowedUserIDs == nil || len(got.AllowedUserIDs) != 0 {
		t.Errorf("AllowedUserIDs = %v, want empty slice", got.AllowedUserIDs)
	}
}

func TestLoadResetsTelegramBlockBetweenCalls(t *testing.T) {
	// First call loads enabled=true; second call points at a directory
	// with no telegram block at all and must NOT carry the previous value
	// forward.
	resetTelegramCfg(t)
	resetDateFormat(t)

	enabledDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(enabledDir, ".monolog"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	doc := map[string]any{"telegram": map[string]any{"enabled": true}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(enabledDir, ".monolog", "config.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Load(enabledDir); err != nil {
		t.Fatalf("Load (enabled): %v", err)
	}
	if !Telegram().Enabled {
		t.Fatalf("expected Enabled=true after first Load")
	}

	emptyDir := t.TempDir()
	writeConfigJSON(t, emptyDir, map[string]string{"theme": "default"})
	if err := Load(emptyDir); err != nil {
		t.Fatalf("Load (empty): %v", err)
	}
	if Telegram().Enabled {
		t.Errorf("Enabled = true after Load with no telegram block, want false (carry-over leak)")
	}
}

// --- AllFormats ---

func TestAllFormatsReturnsThreeEntries(t *testing.T) {
	fmts := AllFormats()
	if len(fmts) != 3 {
		t.Fatalf("AllFormats() returned %d entries, want 3", len(fmts))
	}
	labels := map[string]bool{}
	for _, f := range fmts {
		labels[f.Label] = true
		if _, ok := supported[f.Layout]; !ok {
			t.Errorf("AllFormats() returned unsupported layout %q", f.Layout)
		}
	}
	for _, want := range []string{"DD-MM-YYYY", "MM-DD-YYYY", "YYYY-MM-DD"} {
		if !labels[want] {
			t.Errorf("AllFormats() missing label %q", want)
		}
	}
}
