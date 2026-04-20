package display

import (
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// urlRE matches http:// and https:// URLs up to the next whitespace.
// Trailing sentence punctuation is stripped post-match so links don't
// swallow the trailing "." / ")" / "," etc.
var urlRE = regexp.MustCompile(`https?://\S+`)

// trailingPunct is the set of characters stripped from the end of a URL
// match so the link target stops before sentence punctuation.
const trailingPunct = ".,;:!?)"

// osc8Opener is the byte prefix of an OSC 8 hyperlink escape. If the
// input to Linkify already contains one we assume the string has already
// been linkified and return it unchanged — feeding a linkified string
// back through Linkify would otherwise corrupt the existing escapes
// (the regex treats `\x1b`, `]`, `\\` as non-whitespace).
const osc8Opener = "\x1b]8;;"

// FindURLSpans returns the [start, end) byte offsets of every URL match in
// s, using the same pattern Linkify uses. Callers need this for layout
// decisions (e.g., wrapping or truncating a title without splitting a URL
// across the cut) where reaching for a raw regexp is unnecessary surface
// area. Returns nil when s contains no URLs.
func FindURLSpans(s string) [][]int { return urlRE.FindAllStringIndex(s, -1) }

// StripURLTrailingPunct splits a raw URL match into the link target and
// any trailing sentence punctuation (`.,;:!?)`) that should render
// outside the link. Works correctly on any ASCII trailing-punct run;
// uses utf8.DecodeLastRuneInString so it is safe on multi-byte runes.
func StripURLTrailingPunct(match string) (url, tail string) {
	url = match
	for len(url) > 0 {
		r, size := utf8.DecodeLastRuneInString(url)
		if r == utf8.RuneError || !strings.ContainsRune(trailingPunct, r) {
			break
		}
		tail = string(r) + tail
		url = url[:len(url)-size]
	}
	return url, tail
}

// Linkify wraps every http:// and https:// URL found in s with an OSC 8
// terminal hyperlink escape sequence, making the URL clickable in
// terminals that support the protocol (iTerm2, WezTerm, Ghostty, Kitty,
// Alacritty, Windows Terminal, GNOME Terminal, ...). Terminals without
// support simply render the URL as plain text, so there is no regression.
//
// Trailing sentence punctuation (., ,, ;, :, !, ?, )) is kept outside the
// hyperlink so `"See https://example.com."` links `https://example.com`
// and leaves the final `.` unlinked.
//
// Linkify expects raw text input. If s already contains an OSC 8 opener
// (`\x1b]8;;`) the function returns s unchanged — re-linkifying would
// otherwise corrupt the existing escapes.
//
// Setting MONOLOG_NO_LINKS=1 in the environment disables the wrapping
// entirely — useful if a terminal misbehaves.
func Linkify(s string) string {
	if os.Getenv("MONOLOG_NO_LINKS") == "1" {
		return s
	}
	if strings.Contains(s, osc8Opener) {
		// Already linkified — skip to avoid double-wrapping. Trade-off:
		// if raw user-entered text ever legitimately contains the literal
		// byte sequence `\x1b]8;;` we silently pass it through unlinkified.
		// That payload is not representable through any normal input path
		// (the ESC byte isn't on a keyboard, and the TUI's textinput /
		// textarea widgets filter control bytes), so we accept the simpler
		// check over a stricter "confirm opener is followed by ST" parse.
		return s
	}
	return urlRE.ReplaceAllStringFunc(s, func(match string) string {
		url, tail := StripURLTrailingPunct(match)
		if url == "" {
			return match
		}
		return "\x1b]8;;" + url + "\x1b\\" + url + "\x1b]8;;\x1b\\" + tail
	})
}
