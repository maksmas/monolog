package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// writeThemeFile is a test helper that writes a JSON payload to
// <monologDir>/.monolog/themes/<name>.json, creating parent dirs as
// needed.
func writeThemeFile(t *testing.T, monologDir, name string, payload any) {
	t.Helper()
	dir := filepath.Join(monologDir, ".monolog", "themes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir themes: %v", err)
	}
	var data []byte
	switch v := payload.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		var err error
		data, err = json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestThemeFile_RoundTrip(t *testing.T) {
	got, err := themeToFile(defaultTheme).toTheme()
	if err != nil {
		t.Fatalf("toTheme: %v", err)
	}
	if !reflect.DeepEqual(got, defaultTheme) {
		t.Fatalf("round-trip mismatch\n got: %+v\nwant: %+v", got, defaultTheme)
	}
}

func TestThemeFile_ToTheme_EmptyLight(t *testing.T) {
	tf := themeToFile(defaultTheme)
	tf.ActiveBorder.Light = ""
	_, err := tf.toTheme()
	if err == nil {
		t.Fatal("expected error for empty light value, got nil")
	}
	if !strings.Contains(err.Error(), `"active_border"`) {
		t.Fatalf("error should name the empty field: %v", err)
	}
}

func TestThemeFile_ToTheme_EmptyDark(t *testing.T) {
	tf := themeToFile(defaultTheme)
	tf.SelectedText.Dark = ""
	_, err := tf.toTheme()
	if err == nil {
		t.Fatal("expected error for empty dark value, got nil")
	}
	if !strings.Contains(err.Error(), `"selected_text"`) {
		t.Fatalf("error should name the empty field: %v", err)
	}
}

func TestLoadUserThemes_MissingDir(t *testing.T) {
	dir := t.TempDir()
	got, errs := loadUserThemes(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestLoadUserThemes_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".monolog", "themes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, errs := loadUserThemes(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestLoadUserThemes_Valid(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "mytheme", themeToFile(defaultTheme))

	got, errs := loadUserThemes(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	loaded, ok := got["mytheme"]
	if !ok {
		t.Fatalf("mytheme not in loaded map: %v", got)
	}
	if !reflect.DeepEqual(loaded, defaultTheme) {
		t.Fatalf("theme mismatch\n got: %+v\nwant: %+v", loaded, defaultTheme)
	}
	// Spot-check one value survived as an AdaptiveColor with Light+Dark set.
	if loaded.ActiveBorder != (lipgloss.AdaptiveColor{Light: "28", Dark: "28"}) {
		t.Fatalf("active_border wrong: %+v", loaded.ActiveBorder)
	}
}

func TestLoadUserThemes_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "broken", "{not json}")

	got, errs := loadUserThemes(dir)
	if _, present := got["broken"]; present {
		t.Fatal("broken theme should not be in the loaded map")
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "invalid JSON") ||
		!strings.Contains(errs[0].Error(), `"broken"`) {
		t.Fatalf("error should mention invalid JSON and theme name: %v", errs[0])
	}
}

func TestLoadUserThemes_MissingField(t *testing.T) {
	dir := t.TempDir()
	tf := themeToFile(defaultTheme)
	raw, err := json.Marshal(tf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	delete(asMap, "active_border")
	writeThemeFile(t, dir, "partial", asMap)

	got, errs := loadUserThemes(dir)
	if _, present := got["partial"]; present {
		t.Fatal("partial theme should not be in the loaded map")
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), `missing field "active_border"`) {
		t.Fatalf("error should name missing field: %v", errs[0])
	}
}

func TestLoadUserThemes_EmptyColorString(t *testing.T) {
	dir := t.TempDir()
	tf := themeToFile(defaultTheme)
	tf.Hotkey.Light = ""
	writeThemeFile(t, dir, "empty", tf)

	got, errs := loadUserThemes(dir)
	if _, present := got["empty"]; present {
		t.Fatal("empty-color theme should not be in the loaded map")
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), `empty color for field "hotkey"`) {
		t.Fatalf("error should name empty field: %v", errs[0])
	}
}

func TestLoadUserThemes_ExtraFields(t *testing.T) {
	dir := t.TempDir()
	raw, err := json.Marshal(themeToFile(defaultTheme))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	asMap["future_field"] = json.RawMessage(`"ignored"`)
	writeThemeFile(t, dir, "extra", asMap)

	got, errs := loadUserThemes(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if _, present := got["extra"]; !present {
		t.Fatalf("extra-fields theme should still load: %v", got)
	}
}

// snapshotThemes saves the current state of the package-level themes map
// and registers a cleanup that restores it after the test. Every test that
// calls initThemes (which mutates the global) must invoke this helper
// first so the test suite stays order-independent.
func snapshotThemes(t *testing.T) {
	t.Helper()
	saved := make(map[string]Theme, len(themes))
	for k, v := range themes {
		saved[k] = v
	}
	t.Cleanup(func() {
		for k := range themes {
			if _, keep := saved[k]; !keep {
				delete(themes, k)
			}
		}
		for k, v := range saved {
			themes[k] = v
		}
	})
}

func TestInitThemes_MergesUserAndBuiltin(t *testing.T) {
	snapshotThemes(t)
	dir := t.TempDir()
	writeThemeFile(t, dir, "mytheme", themeToFile(defaultTheme))

	errs := initThemes(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if _, ok := themes["mytheme"]; !ok {
		t.Fatal("mytheme not merged into themes map")
	}
	// Built-ins must still be present.
	if _, ok := themes["default"]; !ok {
		t.Fatal("default built-in missing after merge")
	}
	if _, ok := themes["dracula"]; !ok {
		t.Fatal("dracula built-in missing after merge")
	}
}

func TestInitThemes_SkipsCollisionWithBuiltin(t *testing.T) {
	snapshotThemes(t)
	dir := t.TempDir()

	// Write a "default.json" with a tweaked color so we can detect if
	// it accidentally overrode the built-in.
	tf := themeToFile(defaultTheme)
	tf.ActiveBorder.Dark = "#ff0000"
	writeThemeFile(t, dir, "default", tf)

	errs := initThemes(dir)
	if len(errs) != 1 {
		t.Fatalf("expected 1 collision error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "collides with built-in") ||
		!strings.Contains(errs[0].Error(), `"default"`) {
		t.Fatalf("error should mention collision and theme name: %v", errs[0])
	}
	// Compiled defaultTheme must still be in place, unchanged.
	if themes["default"].ActiveBorder.Dark != defaultTheme.ActiveBorder.Dark {
		t.Fatalf("built-in default was overridden: %+v", themes["default"])
	}
}

func TestBootstrapExampleTheme_CreatesOnFreshDir(t *testing.T) {
	dir := t.TempDir()
	if err := bootstrapExampleTheme(dir); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	path := filepath.Join(dir, ".monolog", "themes", "example.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("example.json not created: %v", err)
	}
	// Loader round-trip: file must parse back to defaultTheme.
	got, errs := loadUserThemes(dir)
	if len(errs) != 0 {
		t.Fatalf("loader errors: %v", errs)
	}
	example, ok := got["example"]
	if !ok {
		t.Fatalf("example theme not loaded: %v", got)
	}
	if !reflect.DeepEqual(example, defaultTheme) {
		t.Fatalf("example != defaultTheme:\n got: %+v\nwant: %+v",
			example, defaultTheme)
	}
}

func TestBootstrapExampleTheme_NoOpWhenDirExists(t *testing.T) {
	dir := t.TempDir()
	themesPath := filepath.Join(dir, ".monolog", "themes")
	if err := os.MkdirAll(themesPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := bootstrapExampleTheme(dir); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// Directory was pre-existing → example.json must NOT have been created.
	if _, err := os.Stat(filepath.Join(themesPath, "example.json")); !os.IsNotExist(err) {
		t.Fatalf("example.json should not exist: err=%v", err)
	}
}

func TestBootstrapExampleTheme_NoOpWhenExampleExists(t *testing.T) {
	dir := t.TempDir()
	themesPath := filepath.Join(dir, ".monolog", "themes")
	if err := os.MkdirAll(themesPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sentinel := []byte(`{"user edited": true}`)
	examplePath := filepath.Join(themesPath, "example.json")
	if err := os.WriteFile(examplePath, sentinel, 0o644); err != nil {
		t.Fatalf("seed example: %v", err)
	}

	if err := bootstrapExampleTheme(dir); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	got, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	if string(got) != string(sentinel) {
		t.Fatalf("example.json was overwritten:\n got: %s\nwant: %s", got, sentinel)
	}
}

func TestInitThemes_PropagatesLoadErrors(t *testing.T) {
	snapshotThemes(t)
	dir := t.TempDir()
	writeThemeFile(t, dir, "good", themeToFile(defaultTheme))
	writeThemeFile(t, dir, "bad", "{not json}")

	errs := initThemes(dir)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error from bad file, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), `"bad"`) {
		t.Fatalf("error should name the bad theme: %v", errs[0])
	}
	if _, ok := themes["good"]; !ok {
		t.Fatal("good theme should still be loaded despite bad sibling")
	}
}

// TestTuiRun_BootstrapOnFreshRepo validates the sequencing that tui.Run
// performs: bootstrap the example file first, then initThemes to pick it
// up. This runs the same pair of calls without touching bubbletea so it's
// safe in headless CI.
func TestTuiRun_BootstrapOnFreshRepo(t *testing.T) {
	snapshotThemes(t)
	dir := t.TempDir()

	if err := bootstrapExampleTheme(dir); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if errs := initThemes(dir); len(errs) != 0 {
		t.Fatalf("initThemes errors: %v", errs)
	}
	if _, ok := themes["example"]; !ok {
		t.Fatalf("example theme should be loaded after wiring sequence: %v", themes)
	}
}

func TestLoadUserThemes_IgnoresNonJSON(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "ok", themeToFile(defaultTheme))

	// Drop a non-json file alongside.
	themes := filepath.Join(dir, ".monolog", "themes")
	if err := os.WriteFile(filepath.Join(themes, "readme.txt"),
		[]byte("hello"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	got, errs := loadUserThemes(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if _, present := got["ok"]; !present {
		t.Fatalf("ok theme should load: %v", got)
	}
	if _, present := got["readme"]; present {
		t.Fatal("readme.txt should not have been loaded as a theme")
	}
}
