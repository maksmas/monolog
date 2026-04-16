package model

import (
	"strings"
	"testing"
	"time"
)

func TestAppendNote(t *testing.T) {
	ts := time.Date(2026, 4, 16, 14, 32, 7, 0, time.UTC)

	tests := []struct {
		name string
		body string
		text string
		now  time.Time
		want string
	}{
		{
			name: "empty body",
			body: "",
			text: "first note",
			now:  ts,
			want: "--- 2026-04-16 14:32:07 ---\nfirst note",
		},
		{
			name: "existing body",
			body: "original body text",
			text: "a note",
			now:  ts,
			want: "original body text\n\n--- 2026-04-16 14:32:07 ---\na note",
		},
		{
			name: "multi-line note text",
			body: "some body",
			text: "line one\nline two\nline three",
			now:  ts,
			want: "some body\n\n--- 2026-04-16 14:32:07 ---\nline one\nline two\nline three",
		},
		{
			name: "appending second note to body with existing note",
			body: "--- 2026-04-16 10:00:00 ---\nearlier note",
			text: "later note",
			now:  ts,
			want: "--- 2026-04-16 10:00:00 ---\nearlier note\n\n--- 2026-04-16 14:32:07 ---\nlater note",
		},
		{
			name: "different timestamp",
			body: "",
			text: "midnight note",
			now:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			want: "--- 2026-01-01 00:00:00 ---\nmidnight note",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AppendNote(tc.body, tc.text, tc.now)
			if got != tc.want {
				t.Errorf("AppendNote(%q, %q, ...)\ngot:  %q\nwant: %q", tc.body, tc.text, got, tc.want)
			}
		})
	}
}

func TestAppendNote_TimestampFormat(t *testing.T) {
	// Verify the separator line uses the expected format.
	ts := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	got := AppendNote("", "note", ts)
	if !strings.HasPrefix(got, "--- 2026-12-31 23:59:59 ---") {
		t.Errorf("unexpected timestamp format in output: %q", got)
	}
}

func TestAppendNote_EmptyBody_NoLeadingBlankLine(t *testing.T) {
	ts := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	got := AppendNote("", "note", ts)
	if strings.HasPrefix(got, "\n") {
		t.Errorf("empty body should not produce a leading blank line, got: %q", got)
	}
}
