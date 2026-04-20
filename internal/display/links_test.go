package display

import (
	"strings"
	"testing"
)

func TestLinkify(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     string
		contains []string // substrings expected in the output
	}{
		{
			name: "no urls unchanged",
			in:   "just a plain string",
			want: "just a plain string",
		},
		{
			name: "empty string unchanged",
			in:   "",
			want: "",
		},
		{
			name:     "single https url",
			in:       "https://example.com",
			contains: []string{"\x1b]8;;https://example.com\x1b\\https://example.com\x1b]8;;\x1b\\"},
		},
		{
			name:     "single http url",
			in:       "http://example.com",
			contains: []string{"\x1b]8;;http://example.com\x1b\\http://example.com\x1b]8;;\x1b\\"},
		},
		{
			name: "multiple urls",
			in:   "see https://a.example and https://b.example",
			contains: []string{
				"\x1b]8;;https://a.example\x1b\\https://a.example\x1b]8;;\x1b\\",
				"\x1b]8;;https://b.example\x1b\\https://b.example\x1b]8;;\x1b\\",
			},
		},
		{
			name:     "url at end of string",
			in:       "final: https://example.com/path",
			contains: []string{"\x1b]8;;https://example.com/path\x1b\\https://example.com/path\x1b]8;;\x1b\\"},
		},
		{
			name:     "trailing dot is stripped",
			in:       "See https://example.com/foo.",
			contains: []string{"\x1b]8;;https://example.com/foo\x1b\\https://example.com/foo\x1b]8;;\x1b\\."},
		},
		{
			name:     "trailing closing paren is stripped",
			in:       "(see https://example.com/foo)",
			contains: []string{"\x1b]8;;https://example.com/foo\x1b\\https://example.com/foo\x1b]8;;\x1b\\)"},
		},
		{
			name:     "trailing comma is stripped",
			in:       "https://example.com/foo, and more",
			contains: []string{"\x1b]8;;https://example.com/foo\x1b\\https://example.com/foo\x1b]8;;\x1b\\,"},
		},
		{
			name:     "trailing semicolon is stripped",
			in:       "https://example.com/foo;",
			contains: []string{"\x1b]8;;https://example.com/foo\x1b\\https://example.com/foo\x1b]8;;\x1b\\;"},
		},
		{
			name:     "trailing question mark is stripped",
			in:       "did you see https://example.com/foo?",
			contains: []string{"\x1b]8;;https://example.com/foo\x1b\\https://example.com/foo\x1b]8;;\x1b\\?"},
		},
		{
			name:     "trailing exclamation is stripped",
			in:       "https://example.com/foo!",
			contains: []string{"\x1b]8;;https://example.com/foo\x1b\\https://example.com/foo\x1b]8;;\x1b\\!"},
		},
		{
			name:     "url inside surrounding text",
			in:       "before https://example.com/x after",
			contains: []string{"\x1b]8;;https://example.com/x\x1b\\https://example.com/x\x1b]8;;\x1b\\"},
		},
		{
			name:     "multiple trailing punct stripped",
			in:       "https://example.com/foo).",
			contains: []string{"\x1b]8;;https://example.com/foo\x1b\\https://example.com/foo\x1b]8;;\x1b\\)."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Linkify(tc.in)
			if tc.want != "" && got != tc.want {
				t.Errorf("Linkify(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for _, sub := range tc.contains {
				if !strings.Contains(got, sub) {
					t.Errorf("Linkify(%q) = %q, missing substring %q", tc.in, got, sub)
				}
			}
		})
	}
}

func TestLinkifyNoLinksEnv(t *testing.T) {
	t.Setenv("MONOLOG_NO_LINKS", "1")
	in := "click https://example.com/foo for details"
	got := Linkify(in)
	if got != in {
		t.Errorf("Linkify with MONOLOG_NO_LINKS=1 = %q, want %q", got, in)
	}
	if strings.Contains(got, "\x1b]8;;") {
		t.Errorf("Linkify with MONOLOG_NO_LINKS=1 still contains OSC 8 escape: %q", got)
	}
}

func TestLinkifySingleCloserPerURL(t *testing.T) {
	// The closer is "\x1b]8;;\x1b\\" — one per URL, no double-wrapping.
	in := "https://a.example and https://b.example and https://c.example"
	got := Linkify(in)
	closer := "\x1b]8;;\x1b\\"
	count := strings.Count(got, closer)
	if count != 3 {
		t.Errorf("expected 3 OSC 8 closers, got %d in %q", count, got)
	}
	// And the opener prefix "\x1b]8;;http" should also appear exactly 3 times
	// (once per URL — the closer uses an empty URL so it matches "\x1b]8;;\x1b" not "\x1b]8;;http").
	opener := "\x1b]8;;http"
	if c := strings.Count(got, opener); c != 3 {
		t.Errorf("expected 3 OSC 8 openers, got %d in %q", c, got)
	}
}

func TestLinkifyPureStripLeavesUnchanged(t *testing.T) {
	// A string that matches the regex but whose entire match strips to empty
	// should not produce a broken escape. Our regex requires `https?://\S+`
	// so the match always has at least one char after `://`; `https://.`
	// strips the trailing dot leaving `https://` as the URL. That's not a
	// valid URL but the function should still produce a well-formed escape.
	in := "edge https://a."
	got := Linkify(in)
	if !strings.Contains(got, "\x1b]8;;https://a\x1b\\https://a\x1b]8;;\x1b\\.") {
		t.Errorf("edge case produced unexpected output: %q", got)
	}
}

// TestLinkifyIdempotentOnAlreadyLinkifiedInput pins behavior when Linkify
// is called on text that has already been linkified. The regex `https?://\S+`
// treats `\x1b`, `]`, `\\` as non-whitespace, so feeding a linkified string
// back through a naive Linkify would corrupt the existing OSC 8 escape. The
// function short-circuits when it detects an existing OSC 8 opener and
// returns the input unchanged, keeping Linkify safe to call on already-
// linkified text (e.g., if two render paths compose).
func TestLinkifyIdempotentOnAlreadyLinkifiedInput(t *testing.T) {
	raw := "click https://example.com/path for details"
	once := Linkify(raw)
	twice := Linkify(once)
	if twice != once {
		t.Errorf("Linkify should be idempotent on already-linkified input\n once = %q\n twice = %q", once, twice)
	}
	// And the OSC 8 sequence count should not double.
	opener := "\x1b]8;;http"
	if got, want := strings.Count(twice, opener), 1; got != want {
		t.Errorf("expected %d OSC 8 openers after double-linkify, got %d in %q", want, got, twice)
	}
}

// TestLinkifyUnicodeBeforeURL pins that a URL preceded by non-ASCII runes
// (with no separating whitespace) still gets linkified. The regex anchors
// on the literal `http://` / `https://` prefix, so leading unicode text
// is treated as ordinary non-URL prefix.
func TestLinkifyUnicodeBeforeURL(t *testing.T) {
	in := "\u65e5\u672c\u8a9ehttps://example.com"
	got := Linkify(in)
	want := "\u65e5\u672c\u8a9e\x1b]8;;https://example.com\x1b\\https://example.com\x1b]8;;\x1b\\"
	if got != want {
		t.Errorf("Linkify(%q)\n got  = %q\n want = %q", in, got, want)
	}
}

// TestLinkifyColonAdjacent pins behavior when a URL is directly preceded
// by a colon (a common case such as `See link: https://...` collapses to
// `link:https://...` in narrow contexts). The URL is still recognized and
// the colon stays outside the link because it is part of the leading text.
func TestLinkifyColonAdjacent(t *testing.T) {
	in := "link:https://example.com/path"
	got := Linkify(in)
	want := "link:\x1b]8;;https://example.com/path\x1b\\https://example.com/path\x1b]8;;\x1b\\"
	if got != want {
		t.Errorf("Linkify(%q)\n got  = %q\n want = %q", in, got, want)
	}
}

// TestFindURLSpans covers the helper used by TUI layout code to locate
// URL byte ranges without reaching for a raw regexp. Returns nil for
// URL-free input and one span per match otherwise.
func TestFindURLSpans(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want [][]int
	}{
		{"empty string returns nil", "", nil},
		{"no url returns nil", "just plain text", nil},
		{"single url at start", "https://a.example rest", [][]int{{0, 17}}},
		{
			name: "two urls with text between",
			in:   "see https://a.example and https://b.example",
			want: [][]int{{4, 21}, {26, 43}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FindURLSpans(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("FindURLSpans(%q) len = %d, want %d (got %v)", tc.in, len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i][0] != tc.want[i][0] || got[i][1] != tc.want[i][1] {
					t.Errorf("FindURLSpans(%q)[%d] = %v, want %v", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestStripURLTrailingPunct covers the helper extracted from Linkify. It
// must handle ASCII trailing-punct runs and leave multi-byte runes alone
// (we use utf8.DecodeLastRuneInString rather than naive byte indexing).
func TestStripURLTrailingPunct(t *testing.T) {
	cases := []struct {
		in, wantURL, wantTail string
	}{
		{"https://example.com", "https://example.com", ""},
		{"https://example.com.", "https://example.com", "."},
		{"https://example.com).", "https://example.com", ")."},
		{"https://example.com,)", "https://example.com", ",)"},
		// Multi-byte trailing rune that is NOT in the punct set: leave as-is.
		{"https://example.com/\u65e5", "https://example.com/\u65e5", ""},
		// Multi-byte rune followed by ASCII punct: strip only the ASCII tail.
		{"https://example.com/\u65e5.", "https://example.com/\u65e5", "."},
	}
	for _, tc := range cases {
		gotURL, gotTail := stripURLTrailingPunct(tc.in)
		if gotURL != tc.wantURL || gotTail != tc.wantTail {
			t.Errorf("stripURLTrailingPunct(%q) = (%q, %q), want (%q, %q)",
				tc.in, gotURL, gotTail, tc.wantURL, tc.wantTail)
		}
	}
}
