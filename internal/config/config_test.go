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

// writeConfigJSON writes a config.json with the given theme value into
// tmpDir/.monolog/config.json and sets MONOLOG_DIR=tmpDir for the test.
func writeConfigJSON(t *testing.T, tmpDir, theme string) {
	t.Helper()
	monologDir := filepath.Join(tmpDir, ".monolog")
	if err := os.MkdirAll(monologDir, 0o755); err != nil {
		t.Fatalf("mkdir .monolog: %v", err)
	}
	data, err := json.Marshal(map[string]string{"theme": theme})
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
	writeConfigJSON(t, t.TempDir(), "dracula")
	if got := Theme(); got != "dracula" {
		t.Errorf("Theme() = %q, want %q", got, "dracula")
	}
}

func TestThemeEnvVarOverridesFile(t *testing.T) {
	// File says "dracula", env var says "default" — env var must win.
	writeConfigJSON(t, t.TempDir(), "dracula")
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
