package tui

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestUnknownThemeFallback verifies that when MONOLOG_THEME is set to an
// unrecognised value, newModel silently falls back to defaultTheme (no panic,
// no error) and the model's theme equals defaultTheme.
func TestUnknownThemeFallback(t *testing.T) {
	t.Setenv("MONOLOG_THEME", "totally-unknown-theme-xyz")

	m := newTestModel(t)
	if m.theme != defaultTheme {
		t.Errorf("unknown theme should fall back to defaultTheme; got %+v", m.theme)
	}
}

func TestThemes_KnownThemes(t *testing.T) {
	for _, name := range []string{"default", "dracula"} {
		theme, ok := themes[name]
		if !ok {
			t.Errorf("themes[%q] not found, want true", name)
		}
		// Smoke-test: NormalText must be non-zero (both Light and Dark set).
		if theme.NormalText.Light == "" || theme.NormalText.Dark == "" {
			t.Errorf("themes[%q]: NormalText has empty Light or Dark", name)
		}
	}
}

func TestThemes_Unknown(t *testing.T) {
	_, ok := themes["nonexistent-theme"]
	if ok {
		t.Error("themes[\"nonexistent-theme\"] returned ok=true, want false")
	}
}

// TestTheme_AllFieldsNonEmpty uses reflect to iterate every AdaptiveColor field
// in the Theme struct and asserts both Light and Dark are non-empty for both
// built-in themes. This catches any new field added to Theme without a
// corresponding value in a theme definition.
func TestTheme_AllFieldsNonEmpty(t *testing.T) {
	namedThemes := map[string]Theme{
		"default": defaultTheme,
		"dracula": draculaTheme,
	}
	for name, theme := range namedThemes {
		v := reflect.ValueOf(theme)
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			ac, ok := field.Interface().(lipgloss.AdaptiveColor)
			if !ok {
				continue
			}
			if ac.Light == "" {
				t.Errorf("%s theme field %s: Light is empty", name, typ.Field(i).Name)
			}
			if ac.Dark == "" {
				t.Errorf("%s theme field %s: Dark is empty", name, typ.Field(i).Name)
			}
		}
	}
}

// TestBuildStyles_DefaultTheme is a smoke test confirming that buildStyles
// produces a valid tuiStyles struct when given the defaultTheme: the
// helpKeyStyle must have a non-zero foreground color, proving the builder
// path (Foreground(t.Hotkey)) executed and the struct field was populated.
func TestBuildStyles_DefaultTheme(t *testing.T) {
	// Force a consistent color profile so lipgloss does not skip colors on CI.
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	s := buildStyles(defaultTheme)

	// helpKeyStyle must render differently from an empty style — it has Bold and
	// a Foreground color applied.
	plain := lipgloss.NewStyle().Render("x")
	styled := s.helpKeyStyle.Render("x")
	if styled == plain {
		t.Error("buildStyles(defaultTheme): helpKeyStyle rendered same as empty style, expected Bold+Foreground to differ")
	}

	// searchPreviewBorderStyle must have a border: its rendered width for a
	// single character should be wider than the character itself.
	bordered := s.searchPreviewBorderStyle.Render("x")
	if lipgloss.Width(bordered) <= 1 {
		t.Errorf("buildStyles(defaultTheme): searchPreviewBorderStyle width %d <= 1, expected border to add columns", lipgloss.Width(bordered))
	}
}

// TestBuildStyles_DraculaTheme confirms that buildStyles also works correctly
// for the dracula theme — the tabStyle foreground must differ from an empty
// style since draculaTheme.TabFg is a non-zero color.
func TestBuildStyles_DraculaTheme(t *testing.T) {
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	s := buildStyles(draculaTheme)

	// tabStyle uses t.TabFg which is non-zero for dracula, so it must differ
	// from an empty style.
	plain := lipgloss.NewStyle().Render("x")
	styled := s.tabStyle.Render("x")
	if styled == plain {
		t.Error("buildStyles(draculaTheme): tabStyle rendered same as empty style, expected Foreground(TabFg) to differ")
	}

	// searchPreviewBorderStyle must still add border columns.
	bordered := s.searchPreviewBorderStyle.Render("x")
	if lipgloss.Width(bordered) <= 1 {
		t.Errorf("buildStyles(draculaTheme): searchPreviewBorderStyle width %d <= 1, expected border to add columns", lipgloss.Width(bordered))
	}
}
