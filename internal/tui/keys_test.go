package tui

import (
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// ruKey builds a key press shaped like the v2 kitty decoder's output for a
// non-Latin layout (see the probe table in the layout-shortcuts plan):
// ShiftedCode equals BaseCode (the decoder's fallthrough copies it), and Text
// is cleared under any modifier above shift. Pass the uppercase Cyrillic text
// for shifted letters, as kitty's associated text carries it.
func ruKey(text string, code, base rune, mod tea.KeyMod) tea.KeyPressMsg {
	if mod&chordMods != 0 {
		text = ""
	}
	return tea.KeyPressMsg{Code: code, Text: text, ShiftedCode: base, BaseCode: base, Mod: mod}
}

// ruLayout maps every US letter/punctuation key to what the macOS Russian
// (ЙЦУКЕН) layout types on it. `/` types `.` there (probe table); `.` types `ю`.
var ruLayout = map[rune]rune{
	'q': 'й', 'w': 'ц', 'e': 'у', 'r': 'к', 't': 'е', 'y': 'н', 'u': 'г',
	'i': 'ш', 'o': 'щ', 'p': 'з', '[': 'х', ']': 'ъ',
	'a': 'ф', 's': 'ы', 'd': 'в', 'f': 'а', 'g': 'п', 'h': 'р', 'j': 'о',
	'k': 'л', 'l': 'д', ';': 'ж', '\'': 'э',
	'z': 'я', 'x': 'ч', 'c': 'с', 'v': 'м', 'b': 'и', 'n': 'т', 'm': 'ь',
	',': 'б', '.': 'ю', '/': '.',
}

// ruShortcut builds the Russian-layout key press for a US shortcut spec: a
// lowercase letter or punctuation key unshifted, an uppercase letter as
// shift + its base key (kitty's associated text carries the uppercase
// Cyrillic).
func ruShortcut(t *testing.T, spec string) tea.KeyPressMsg {
	t.Helper()
	r := []rune(spec)
	if len(r) != 1 {
		t.Fatalf("ruShortcut(%q): want a single rune", spec)
	}
	base := unicode.ToLower(r[0])
	cy, ok := ruLayout[base]
	if !ok {
		t.Fatalf("ruShortcut(%q): %q not in ruLayout", spec, base)
	}
	if unicode.IsUpper(r[0]) {
		return ruKey(string(unicode.ToUpper(cy)), cy, base, tea.ModShift)
	}
	return ruKey(string(cy), cy, base, 0)
}

// TestCommandKey_EveryRussianKeyResolvesToUS sweeps commandKey over the whole
// ЙЦУКЕН layout: given the decoder-shaped event (BaseCode = the US key, as
// the probe and TestCommandKey_KittyDecoder show kitty sends), every
// unshifted key resolves to its US character and every shifted letter to the
// uppercase US letter — not just the keys the TestLayout_* tests drive. It
// checks commandKey's rules, not what a terminal reports.
func TestCommandKey_EveryRussianKeyResolvesToUS(t *testing.T) {
	for base := range ruLayout {
		spec := string(base)
		if got := commandKey(ruShortcut(t, spec)).String(); got != spec {
			t.Errorf("Russian %q (base %q): commandKey String() = %q, want %q",
				ruLayout[base], base, got, spec)
		}
		if !unicode.IsLetter(base) {
			continue
		}
		upper := string(unicode.ToUpper(base))
		if got := commandKey(ruShortcut(t, upper)).String(); got != upper {
			t.Errorf("Russian shift+%q (base %q): commandKey String() = %q, want %q",
				ruLayout[base], base, got, upper)
		}
	}
}

func TestCommandKey(t *testing.T) {
	tests := []struct {
		name string
		in   tea.KeyPressMsg
		want string // commandKey(in).String()
	}{
		// rewrites
		{"ru d", ruKey("в", 'в', 'd', 0), "d"},
		{"ru shift+g", ruKey("П", 'п', 'g', tea.ModShift), "G"},
		{"ru [", ruKey("х", 'х', '[', 0), "["},
		{"ru ,", ruKey("б", 'б', ',', 0), ","},
		{"ru y", ruKey("н", 'н', 'y', 0), "y"},
		{"ru / (probe)", ruKey(".", '.', '/', 0), "/"},
		{"ru d with numlock", ruKey("в", 'в', 'd', tea.ModNumLock), "d"},

		// shifted punctuation: the layout's shifted symbol must not leak
		{"ru shift+/ (probe)", ruKey(",", '.', '/', tea.ModShift), "shift+/"},
		{"ru shift+,", ruKey("Б", 'б', ',', tea.ModShift), "shift+,"},
		// ...unless it types the base key itself
		{"azerty shift+& (types 1)", ruKey("1", '&', '1', tea.ModShift), "1"},
		{"azerty & (physical 1)", ruKey("&", '&', '1', 0), "1"},

		// unchanged
		{"ru ctrl+z", ruKey("", 'я', 'z', tea.ModCtrl), "ctrl+z"},
		{"ru alt+d", ruKey("", 'в', 'd', tea.ModAlt), "alt+d"},
		{"ru super+d", ruKey("", 'в', 'd', tea.ModSuper), "super+d"},
		{"ru meta+d", ruKey("", 'в', 'd', tea.ModMeta), "meta+d"},
		{"ru hyper+d", ruKey("", 'в', 'd', tea.ModHyper), "hyper+d"},

		// meta/hyper/super Text (kitty associated text) is dropped
		{"super+d with text", tea.KeyPressMsg{Code: 'd', Text: "d", Mod: tea.ModSuper}, "super+d"},
		{"meta+d with text", tea.KeyPressMsg{Code: 'd', Text: "d", Mod: tea.ModMeta}, "meta+d"},
		{"hyper+d with text", tea.KeyPressMsg{Code: 'd', Text: "d", Mod: tea.ModHyper}, "hyper+d"},
		{"ru super+d with text", tea.KeyPressMsg{Code: 'в', Text: "в", BaseCode: 'd', Mod: tea.ModSuper}, "super+d"},
		{"super+shift+y with text", tea.KeyPressMsg{Code: 'y', Text: "Y", Mod: tea.ModSuper | tea.ModShift}, "shift+super+y"},
		{"no protocol d", tea.KeyPressMsg{Code: 'd', Text: "d"}, "d"},
		{"no protocol cyrillic", tea.KeyPressMsg{Code: 'в', Text: "в"}, "в"},
		{"english d", ruKey("d", 'd', 'd', 0), "d"},
		{"english shift+g", tea.KeyPressMsg{Code: 'g', Text: "G", ShiftedCode: 'G', BaseCode: 'g', Mod: tea.ModShift}, "G"},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "enter"},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}, "esc"},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, "up"},
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, "down"},
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "shift+tab"},
		{"space", tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, "space"},
		{"non-printable base", tea.KeyPressMsg{Code: 'в', Text: "в", BaseCode: '\x01'}, "в"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commandKey(tt.in)
			if s := got.String(); s != tt.want {
				t.Errorf("commandKey(%+v).String() = %q, want %q", tt.in, s, tt.want)
			}
			if again := commandKey(got); again != got {
				t.Errorf("commandKey not idempotent: %+v -> %+v", got, again)
			}
		})
	}
}

// TestCommandKey_UnchangedKeysAreIdentical pins that keys outside the rewrite
// rules come back field-for-field identical, not merely with the same String().
func TestCommandKey_UnchangedKeysAreIdentical(t *testing.T) {
	keys := []tea.KeyPressMsg{
		ruKey("", 'я', 'z', tea.ModCtrl),
		ruKey("", 'в', 'd', tea.ModAlt|tea.ModShift),
		{Code: 'd', Text: "d"},
		{Code: 'в', Text: "в"},
		{Code: 'd', Text: "d", ShiftedCode: 'd', BaseCode: 'd'},
		{Code: tea.KeyEnter},
		{Code: tea.KeyEscape},
		{Code: tea.KeyLeft},
		{Code: 'в', Text: "в", BaseCode: '\x01'},
	}
	for _, k := range keys {
		if got := commandKey(k); got != k {
			t.Errorf("commandKey(%+v) = %+v, want unchanged", k, got)
		}
	}
}

// TestPreferTypedLatin: a typed Latin letter drops BaseCode, so Latin layouts
// that move letters keep their shortcuts and chords; non-Latin keys and the
// Russian punctuation keys keep BaseCode. ruKey only builds the decoder shape
// here — the layout is named per row.
func TestPreferTypedLatin(t *testing.T) {
	tests := []struct {
		name string
		in   tea.KeyPressMsg
		want string // commandKey(preferTypedLatin(in)).String()
		base rune   // BaseCode after preferTypedLatin
	}{
		{"dvorak d (physical h)", ruKey("d", 'd', 'h', 0), "d", 0},
		{"dvorak shift+d (physical h)", ruKey("D", 'd', 'h', tea.ModShift), "D", 0},
		{"azerty q (physical a)", ruKey("q", 'q', 'a', 0), "q", 0},
		{"qwertz y (physical z)", ruKey("y", 'y', 'z', 0), "y", 0},
		{"qwertz ctrl+z (physical y)", ruKey("", 'z', 'y', tea.ModCtrl), "ctrl+z", 0},
		{"azerty ctrl+w (physical z)", ruKey("", 'w', 'z', tea.ModCtrl), "ctrl+w", 0},

		// ASCII punctuation on a letter key types the punctuation
		{"dvorak ' (physical q)", ruKey("'", '\'', 'q', 0), "'", 0},
		{"dvorak shift+' (physical q)", ruKey("\"", '\'', 'q', tea.ModShift), "\"", 0},
		{"dvorak . (physical e)", ruKey(".", '.', 'e', 0), ".", 0},
		{"dvorak , (physical w)", ruKey(",", ',', 'w', 0), ",", 0},
		{"azerty , (physical m)", ruKey(",", ',', 'm', 0), ",", 0},
		// documented limit: punctuation moved onto a punctuation key still
		// follows the physical key (the Russian / key needs that rule)
		{"dvorak / (physical [) — limit", ruKey("/", '/', '[', 0), "[", '['},

		{"ru d", ruKey("в", 'в', 'd', 0), "d", 'd'},
		{"ru ctrl+z", ruKey("", 'я', 'z', tea.ModCtrl), "ctrl+z", 'z'},
		{"ru / (probe)", ruKey(".", '.', '/', 0), "/", '/'},
		{"ru shift+/ (probe)", ruKey(",", '.', '/', tea.ModShift), "shift+/", '/'},
		{"ru ,", ruKey("б", 'б', ',', 0), ",", ','},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := preferTypedLatin(tt.in)
			if got.BaseCode != tt.base {
				t.Errorf("BaseCode = %q, want %q", got.BaseCode, tt.base)
			}
			if s := commandKey(got).String(); s != tt.want {
				t.Errorf("commandKey(preferTypedLatin(%+v)).String() = %q, want %q", tt.in, s, tt.want)
			}
		})
	}
}

// decodeKitty feeds raw terminal bytes through the ultraviolet decoder that
// Bubble Tea v2 reads input with, and converts the result the way the program
// does (tea's translateInputEvent: KeyPressMsg(uv.KeyPressEvent)). The whole
// sequence must decode to exactly one key press.
func decodeKitty(t *testing.T, seq string) tea.KeyPressMsg {
	t.Helper()
	var d uv.EventDecoder
	n, ev := d.Decode([]byte(seq))
	if n != len(seq) {
		t.Fatalf("Decode(%q) consumed %d of %d bytes", seq, n, len(seq))
	}
	press, ok := ev.(uv.KeyPressEvent)
	if !ok {
		t.Fatalf("Decode(%q) = %T %+v, want uv.KeyPressEvent", seq, ev, ev)
	}
	return tea.KeyPressMsg(press)
}

// TestCommandKey_KittyDecoder runs kitty keyboard protocol sequences through
// the real v2 input decoder, so the tests check the decoder rather than the
// plan's model of it. The probe (Task 1 of the layout-shortcuts plan) recorded
// decoded fields but not raw bytes, so each sequence is built from the kitty
// spec — CSI code:shifted:base ; modifiers ; text u, with modifiers 1+bits
// (shift=1, alt=2, ctrl=4) — for the flags monolog requests
// (ReportAllKeysAsEscapeCodes | ReportAlternateKeys | ReportAssociatedText),
// and must decode to exactly the fields of the probe table.
func TestCommandKey_KittyDecoder(t *testing.T) {
	tests := []struct {
		name      string
		seq       string
		want      tea.KeyPressMsg // decoded fields == probe table row
		str       string          // decoded String(), probe column
		keystroke string          // decoded Keystroke(), probe column ("" = not recorded)
		command   string          // commandKey(preferTypedLatin(decoded)).String(), as Update resolves it
	}{
		{"d", "\x1b[1074::100;1;1074u",
			tea.KeyPressMsg{Code: 'в', Text: "в", ShiftedCode: 'd', BaseCode: 'd'},
			"в", "d", "d"},
		{"d, empty modifier field", "\x1b[1074::100;;1074u",
			tea.KeyPressMsg{Code: 'в', Text: "в", ShiftedCode: 'd', BaseCode: 'd'},
			"в", "d", "d"},
		{"shift+g", "\x1b[1087:1055:103;2;1055u",
			tea.KeyPressMsg{Code: 'п', Text: "П", ShiftedCode: 'g', BaseCode: 'g', Mod: tea.ModShift},
			"П", "shift+g", "G"},
		{"ctrl+z", "\x1b[1103::122;5u",
			tea.KeyPressMsg{Code: 'я', ShiftedCode: 'z', BaseCode: 'z', Mod: tea.ModCtrl},
			"ctrl+z", "ctrl+z", "ctrl+z"},
		{"/", "\x1b[46::47;1;46u",
			tea.KeyPressMsg{Code: '.', Text: ".", ShiftedCode: '/', BaseCode: '/'},
			".", "/", "/"},
		{"shift+/", "\x1b[46:44:47;2;44u",
			tea.KeyPressMsg{Code: '.', Text: ",", ShiftedCode: '/', BaseCode: '/', Mod: tea.ModShift},
			",", "shift+/", "shift+/"},
		{"[", "\x1b[1093::91;1;1093u",
			tea.KeyPressMsg{Code: 'х', Text: "х", ShiftedCode: '[', BaseCode: '['},
			"х", "[", "["},
		{",", "\x1b[1073::44;1;1073u",
			tea.KeyPressMsg{Code: 'б', Text: "б", ShiftedCode: ',', BaseCode: ','},
			"б", ",", ","},
		{"y", "\x1b[1085::121;1;1085u",
			tea.KeyPressMsg{Code: 'н', Text: "н", ShiftedCode: 'y', BaseCode: 'y'},
			"н", "y", "y"},
		{"enter", "\x1b[13u",
			tea.KeyPressMsg{Code: tea.KeyEnter},
			"enter", "", "enter"},
		{"shift+tab", "\x1b[9;2u",
			tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift},
			"shift+tab", "", "shift+tab"},
		{"alt+enter", "\x1b[13;3u",
			tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt},
			"alt+enter", "", "alt+enter"},
		{"space", "\x1b[32;1;32u",
			tea.KeyPressMsg{Code: tea.KeySpace, Text: " "},
			"space", "", "space"},
		{"shift alone", "\x1b[57441;2u",
			tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift},
			"leftshift", "", "leftshift"},

		// Keys the test helpers (namedKeys, runeKey) fake: their decoded
		// String() must equal the helper's, or the suite tests a shape kitty
		// never sends.
		{"esc", "\x1b[27u",
			tea.KeyPressMsg{Code: tea.KeyEscape},
			"esc", "", "esc"},
		{"backspace", "\x1b[127u",
			tea.KeyPressMsg{Code: tea.KeyBackspace},
			"backspace", "", "backspace"},
		{"tab", "\x1b[9u",
			tea.KeyPressMsg{Code: tea.KeyTab},
			"tab", "", "tab"},
		{"us g", "\x1b[103;1;103u",
			tea.KeyPressMsg{Code: 'g', Text: "g"},
			"g", "", "g"},
		{"us shift+g", "\x1b[103:71;2;71u",
			tea.KeyPressMsg{Code: 'g', Text: "G", ShiftedCode: 'G', Mod: tea.ModShift},
			"G", "", "G"},

		// Latin layouts that move letters: the typed letter wins over the
		// physical key (preferTypedLatin), for plain keys and chords alike.
		{"dvorak d (physical h)", "\x1b[100::104;1;100u",
			tea.KeyPressMsg{Code: 'd', Text: "d", ShiftedCode: 'h', BaseCode: 'h'},
			"d", "h", "d"},
		{"dvorak ' (physical q)", "\x1b[39::113;1;39u",
			tea.KeyPressMsg{Code: '\'', Text: "'", ShiftedCode: 'q', BaseCode: 'q'},
			"'", "q", "'"},
		{"azerty shift+& (types 1)", "\x1b[38:49:49;2;49u",
			tea.KeyPressMsg{Code: '&', Text: "1", ShiftedCode: '1', BaseCode: '1', Mod: tea.ModShift},
			"1", "shift+1", "1"},
		{"qwertz ctrl+z (physical y)", "\x1b[122::121;5u",
			tea.KeyPressMsg{Code: 'z', ShiftedCode: 'y', BaseCode: 'y', Mod: tea.ModCtrl},
			"ctrl+y", "ctrl+y", "ctrl+z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeKitty(t, tt.seq)
			if got != tt.want {
				t.Errorf("decoded %q = %+v, want %+v", tt.seq, got, tt.want)
			}
			if s := got.String(); s != tt.str {
				t.Errorf("decoded String() = %q, want %q", s, tt.str)
			}
			if tt.keystroke != "" {
				if s := got.Keystroke(); s != tt.keystroke {
					t.Errorf("decoded Keystroke() = %q, want %q", s, tt.keystroke)
				}
			}
			if s := commandKey(preferTypedLatin(got)).String(); s != tt.command {
				t.Errorf("commandKey(preferTypedLatin(decoded)).String() = %q, want %q", s, tt.command)
			}
		})
	}
}

// TestKittyDecoder_ModifierAloneIsFiltered pins that the decoder reports a
// bare modifier press with a code isModifierOnly recognizes, so Update drops it.
func TestKittyDecoder_ModifierAloneIsFiltered(t *testing.T) {
	for _, seq := range []string{
		"\x1b[57441;2u", // left shift
		"\x1b[57442;5u", // left ctrl
		"\x1b[57443;3u", // left alt
		"\x1b[57444;9u", // left super (Cmd)
	} {
		if k := decodeKitty(t, seq); !isModifierOnly(k.Code) {
			t.Errorf("decoded %q = %+v, want a modifier-only code", seq, k)
		}
	}
}
