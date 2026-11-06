package tui

import (
	"fmt"
	"image/color"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/maksmas/monolog/internal/config"
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
			ac, ok := field.Interface().(AdaptiveColor)
			if !ok {
				t.Errorf("%s theme field %s has type %s, want AdaptiveColor", name, typ.Field(i).Name, field.Type())
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
	s := buildStyles(defaultTheme, true)

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
	s := buildStyles(draculaTheme, true)

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

// fgSGR returns the truecolor foreground SGR parameters ("38;2;r;g;b") that
// lipgloss v2 emits for a "#rrggbb" color, so color assertions can be derived
// from theme values instead of hand-converted literals.
func fgSGR(t *testing.T, hex string) string {
	t.Helper()
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil || len(hex) != 7 {
		t.Fatalf("fgSGR(%q): not a #rrggbb color", hex)
	}
	return fmt.Sprintf("38;2;%d;%d;%d", v>>16, (v>>8)&0xff, v&0xff)
}

func TestAdaptiveColor_ResolvePicksLightOrDark(t *testing.T) {
	c := AdaptiveColor{Light: "#112233", Dark: "#aabbcc"}
	if got, want := c.Resolve(true), lipgloss.Color("#aabbcc"); got != want {
		t.Errorf("Resolve(true) = %v, want dark %v", got, want)
	}
	if got, want := c.Resolve(false), lipgloss.Color("#112233"); got != want {
		t.Errorf("Resolve(false) = %v, want light %v", got, want)
	}
}

// TestBuildStyles_ResolvesForBackground checks that the same theme yields
// different foregrounds on light and dark backgrounds for a role whose pair
// differs (default NormalText is #1a1a1a / #dddddd; Borders is 240 / 240).
func TestBuildStyles_ResolvesForBackground(t *testing.T) {
	theme := defaultTheme
	theme.Borders = AdaptiveColor{Light: "#000000", Dark: "#ffffff"}

	dark := buildStyles(theme, true)
	light := buildStyles(theme, false)
	if got, want := dark.helpTextStyle.GetForeground(), lipgloss.Color("#ffffff"); got != want {
		t.Errorf("dark helpTextStyle foreground = %v, want %v", got, want)
	}
	if got, want := light.helpTextStyle.GetForeground(), lipgloss.Color("#000000"); got != want {
		t.Errorf("light helpTextStyle foreground = %v, want %v", got, want)
	}

	darkBase, _, _ := initStyles(defaultTheme, true)
	lightBase, _, _ := initStyles(defaultTheme, false)
	if darkBase.NormalTitle.GetForeground() == lightBase.NormalTitle.GetForeground() {
		t.Errorf("initStyles NormalTitle foreground identical on light and dark: %v", darkBase.NormalTitle.GetForeground())
	}
	if got, want := lightBase.NormalTitle.GetForeground(), defaultTheme.NormalText.Resolve(false); got != want {
		t.Errorf("light NormalTitle foreground = %v, want %v", got, want)
	}
}

// TestBackgroundColorMsg_LightRestyles drives a light-background report
// through Update: isDark flips and every style set is rebuilt from the light
// half of each pair; a dark report flips it back.
func TestBackgroundColorMsg_LightRestyles(t *testing.T) {
	m := newTestModel(t)
	if !m.isDark {
		t.Fatal("precondition: model should default to a dark background")
	}
	if got, want := m.baseStyles.NormalTitle.GetForeground(), defaultTheme.NormalText.Resolve(true); got != want {
		t.Fatalf("initial NormalTitle foreground = %v, want dark %v", got, want)
	}

	next, cmd := m.Update(tea.BackgroundColorMsg{Color: color.White})
	m = next.(*Model)
	if cmd != nil {
		t.Errorf("BackgroundColorMsg returned a cmd, want nil")
	}
	if m.isDark {
		t.Fatal("isDark still true after a white BackgroundColorMsg")
	}
	if got, want := m.baseStyles.NormalTitle.GetForeground(), defaultTheme.NormalText.Resolve(false); got != want {
		t.Errorf("NormalTitle foreground = %v, want light %v", got, want)
	}
	if got, want := m.activeStyles.NormalTitle.GetForeground(), defaultTheme.ActiveNormal.Resolve(false); got != want {
		t.Errorf("active NormalTitle foreground = %v, want light %v", got, want)
	}
	if got, want := m.grabStyles.SelectedTitle.GetForeground(), defaultTheme.GrabText.Resolve(false); got != want {
		t.Errorf("grab SelectedTitle foreground = %v, want light %v", got, want)
	}
	// The bubbles input defaults are background-aware too.
	if got, want := m.input.Styles(), textinput.DefaultStyles(false); got.Blurred.Text.GetForeground() != want.Blurred.Text.GetForeground() {
		t.Errorf("input blurred text foreground = %v, want light default %v",
			got.Blurred.Text.GetForeground(), want.Blurred.Text.GetForeground())
	}

	next, _ = m.Update(tea.BackgroundColorMsg{Color: color.Black})
	m = next.(*Model)
	if !m.isDark {
		t.Fatal("isDark false after a black BackgroundColorMsg")
	}
	if got, want := m.baseStyles.NormalTitle.GetForeground(), defaultTheme.NormalText.Resolve(true); got != want {
		t.Errorf("NormalTitle foreground = %v, want dark %v", got, want)
	}
}

// TestRestyle_KeepsFlatTextareaOverrides pins that restyle re-applies the
// flattened cursor-line/base styles on both textareas after loading the
// background-aware bubbles defaults, which do paint a cursor-line background.
func TestRestyle_KeepsFlatTextareaOverrides(t *testing.T) {
	if _, ok := textarea.DefaultStyles(true).Focused.CursorLine.GetBackground().(lipgloss.NoColor); ok {
		t.Fatal("precondition: textarea default focused CursorLine should have a background")
	}
	m := newTestModel(t)
	for _, isDark := range []bool{true, false} {
		next, _ := m.Update(tea.BackgroundColorMsg{Color: map[bool]color.Color{true: color.Black, false: color.White}[isDark]})
		m = next.(*Model)
		for name, ta := range map[string]textarea.Model{"titleArea": m.titleArea, "noteArea": m.noteArea} {
			st := ta.Styles()
			for state, s := range map[string]textarea.StyleState{"focused": st.Focused, "blurred": st.Blurred} {
				if _, ok := s.CursorLine.GetBackground().(lipgloss.NoColor); !ok {
					t.Errorf("isDark=%v %s %s CursorLine background = %v, want none", isDark, name, state, s.CursorLine.GetBackground())
				}
				if _, ok := s.CursorLine.GetForeground().(lipgloss.NoColor); !ok {
					t.Errorf("isDark=%v %s %s CursorLine foreground = %v, want none", isDark, name, state, s.CursorLine.GetForeground())
				}
			}
			// Non-overridden fields still come from the background-aware defaults.
			if got, want := st.Blurred.Text.GetForeground(), textarea.DefaultStyles(isDark).Blurred.Text.GetForeground(); got != want {
				t.Errorf("isDark=%v %s blurred Text foreground = %v, want %v", isDark, name, got, want)
			}
		}
	}
}

// TestApplySettings_RestylesWithCurrentBackground checks that switching theme
// in the settings modal resolves the new theme against the known background
// rather than resetting to dark.
func TestApplySettings_RestylesWithCurrentBackground(t *testing.T) {
	origFmt := config.DateFormat()
	t.Cleanup(func() { _ = config.SetDateFormat(origFmt) })
	m := newTestModel(t)
	next, _ := m.Update(tea.BackgroundColorMsg{Color: color.White})
	m = next.(*Model)

	m.settingsFmt = "02-01-2006"
	m.settingsTheme = "dracula"
	m.applySettings()

	if m.theme != draculaTheme {
		t.Fatalf("theme not switched to dracula")
	}
	if got, want := m.baseStyles.NormalTitle.GetForeground(), draculaTheme.NormalText.Resolve(false); got != want {
		t.Errorf("NormalTitle foreground after settings = %v, want dracula light %v", got, want)
	}
}
