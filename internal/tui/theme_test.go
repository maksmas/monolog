package tui

import "testing"

func TestThemeByName_KnownThemes(t *testing.T) {
	for _, name := range []string{"default", "dracula"} {
		theme, ok := ThemeByName(name)
		if !ok {
			t.Errorf("ThemeByName(%q) returned ok=false, want true", name)
		}
		// Smoke-test: NormalText must be non-zero (both Light and Dark set).
		if theme.NormalText.Light == "" || theme.NormalText.Dark == "" {
			t.Errorf("ThemeByName(%q): NormalText has empty Light or Dark", name)
		}
	}
}

func TestThemeByName_Unknown(t *testing.T) {
	_, ok := ThemeByName("nonexistent-theme")
	if ok {
		t.Error("ThemeByName(\"nonexistent-theme\") returned ok=true, want false")
	}
}

func TestDefaultTheme_NonZeroFields(t *testing.T) {
	theme := defaultTheme

	if theme.NormalText.Light == "" || theme.NormalText.Dark == "" {
		t.Error("defaultTheme.NormalText has empty Light or Dark")
	}
	if theme.ActiveBorder.Light == "" || theme.ActiveBorder.Dark == "" {
		t.Error("defaultTheme.ActiveBorder has empty Light or Dark")
	}
	if theme.ModalBorder.Light == "" || theme.ModalBorder.Dark == "" {
		t.Error("defaultTheme.ModalBorder has empty Light or Dark")
	}
}

func TestDraculaTheme_NonZeroFields(t *testing.T) {
	theme := draculaTheme

	if theme.NormalText.Light == "" || theme.NormalText.Dark == "" {
		t.Error("draculaTheme.NormalText has empty Light or Dark")
	}
	if theme.ActiveBorder.Light == "" || theme.ActiveBorder.Dark == "" {
		t.Error("draculaTheme.ActiveBorder has empty Light or Dark")
	}
	if theme.ModalBorder.Light == "" || theme.ModalBorder.Dark == "" {
		t.Error("draculaTheme.ModalBorder has empty Light or Dark")
	}
}
