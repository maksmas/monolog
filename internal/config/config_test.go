package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
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
