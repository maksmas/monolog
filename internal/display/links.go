package display

import (
	"os"
	"regexp"
	"strings"
)

// urlRE matches http:// and https:// URLs up to the next whitespace.
// Trailing sentence punctuation is stripped post-match so links don't
// swallow the trailing "." / ")" / "," etc.
var urlRE = regexp.MustCompile(`https?://\S+`)

// trailingPunct is the set of characters stripped from the end of a URL
// match so the link target stops before sentence punctuation.
const trailingPunct = ".,;:!?)"

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
// Setting MONOLOG_NO_LINKS=1 in the environment disables the wrapping
// entirely — useful if a terminal misbehaves.
func Linkify(s string) string {
	if os.Getenv("MONOLOG_NO_LINKS") == "1" {
		return s
	}
	return urlRE.ReplaceAllStringFunc(s, func(match string) string {
		url := match
		tail := ""
		for len(url) > 0 && strings.ContainsRune(trailingPunct, rune(url[len(url)-1])) {
			tail = string(url[len(url)-1]) + tail
			url = url[:len(url)-1]
		}
		if url == "" {
			return match
		}
		return "\x1b]8;;" + url + "\x1b\\" + url + "\x1b]8;;\x1b\\" + tail
	})
}
