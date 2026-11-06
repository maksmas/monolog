package tui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// chordMods are the modifiers that turn a key into a chord. uv's Keystroke()
// already renders a chord from BaseCode (ctrl+я → "ctrl+z"), so commandKey
// does not rewrite such keys from BaseCode.
const chordMods = tea.ModCtrl | tea.ModAlt | tea.ModMeta | tea.ModHyper | tea.ModSuper

// preferTypedLatin drops the kitty base-layout key when the typed key is ASCII
// and either is a Latin letter or sits on a PC-101 letter key, so on a Latin
// layout (Dvorak, AZERTY, QWERTZ, Colemak) shortcuts follow what the user
// typed, not the key underneath it: Dvorak `d` (physical h) is "d", and
// Dvorak `'` (physical q) types `'` instead of quitting. Update applies it to
// every key press, before mode dispatch: besides commandKey, uv's Keystroke()
// renders every chord from BaseCode, so without it QWERTZ ctrl+z (physical y)
// would arrive as "ctrl+y" in every context, swapping undo and redo.
//
// Non-Latin layouts keep BaseCode: Russian `в` and ctrl+`я` are not ASCII,
// and the punctuation the Russian `/` key types (`.`) sits on a punctuation
// key, so they still resolve to "d", "ctrl+z" and "/". Two limits follow:
//   - ASCII punctuation a Latin layout moves onto another punctuation key
//     still follows the physical key, since the Russian `/` key shows that
//     such punctuation cannot be trusted as typed: Dvorak `/` (physical `[`)
//     reads "[" (scroll the detail panel up) and cannot open search.
//   - Layouts that put ASCII punctuation on letter keys (Greek `;` on q,
//     Hebrew `/` on q and `'` on w) get the typed punctuation, not the letter
//     shortcut: physical q does not quit there (ctrl+c still does), and
//     Hebrew physical q opens search.
func preferTypedLatin(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if isASCIILetter(msg.Code) || (msg.Code > ' ' && msg.Code <= '~' && isASCIILetter(msg.BaseCode)) {
		msg.BaseCode = 0
	}
	return msg
}

// isModifierOnly reports whether code is a modifier or lock key pressed on
// its own (kitty reports these under ReportAllKeysAsEscapeCodes).
func isModifierOnly(code rune) bool {
	switch {
	case code >= tea.KeyLeftShift && code <= tea.KeyIsoLevel5Shift:
		return true
	case code == tea.KeyCapsLock, code == tea.KeyNumLock, code == tea.KeyScrollLock:
		return true
	}
	return false
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// commandKey resolves a key press to its US-layout (PC-101) key using the
// kitty keyboard protocol's BaseCode, so letter and punctuation shortcuts
// fire on a non-Latin layout: Russian `в` (the `d` key) becomes "d",
// shift+`п` becomes "G", `б` becomes ",". Only Text is rewritten: String()
// returns Text when it is set and otherwise renders the keystroke from
// BaseCode, and no command handler reads anything but String() and the
// Esc/Enter codes, which have no BaseCode.
//
// Apply it ONLY where a key is a command (normal mode after the detail-panel
// early return, grab, settings, delete-confirm). Text inputs — add, retag,
// reschedule, search query, note textarea — must receive the raw key, or a
// Russian user could not type Russian.
//
// A meta/hyper/super chord loses its Text: the decoder keeps the associated
// text kitty reports under those modifiers, and String() would then return
// the bare letter, so super+d would complete the task. Cleared, it reads
// "super+d" and matches no shortcut. ctrl/alt chords already arrive without
// Text and are left as they are.
//
// Keys without a usable BaseCode (no protocol, US layout, special keys, a
// typed ASCII key whose BaseCode preferTypedLatin dropped) and ctrl/alt
// chords are returned unchanged. Shift plus a non-letter base clears Text, so
// String() becomes e.g. "shift+/" and matches no shortcut: Russian shift+`/`
// types ",", which would otherwise open settings. The exception is a shifted
// key that types its own base key (AZERTY shift+`&` types "1" on the `1`
// key), which keeps Text so shifted digits still switch tabs. The result is
// idempotent.
func commandKey(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if msg.Mod&(tea.ModMeta|tea.ModHyper|tea.ModSuper) != 0 {
		msg.Text = ""
		return msg
	}
	base := msg.BaseCode
	if base == 0 || base == msg.Code || !unicode.IsPrint(base) || msg.Mod&chordMods != 0 {
		return msg
	}
	switch {
	case !msg.Mod.Contains(tea.ModShift):
		msg.Text = string(base)
	case unicode.IsLetter(base):
		msg.Text = string(unicode.ToUpper(base))
	case msg.Text == string(base):
		// The shifted key types the base key itself (AZERTY shift+& → "1").
	default:
		// The shifted symbol depends on the layout; keep only the keystroke.
		msg.Text = ""
	}
	return msg
}
