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

// URLRegexp returns the compiled regexp used to detect URLs. Callers
// that need to locate URL spans in a string (for layout decisions such
// as wrapping a title without splitting a URL across lines) should use
// this rather than re-compiling the pattern. The regexp is the raw
// matcher — callers that want the link-target substring should pass
// the match through StripURLTrailingPunct.
func URLRegexp() *regexp.Regexp { return urlRE }

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
		// Already linkified — skip to avoid double-wrapping.
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
