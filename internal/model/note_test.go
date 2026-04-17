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

func TestCountNotes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{
			name: "empty body",
			body: "",
			want: 0,
		},
		{
			name: "body with no notes",
			body: "just some plain body text\nwith multiple lines",
			want: 0,
		},
		{
			name: "one note",
			body: "--- 2026-04-16 14:32:07 ---\nfirst note",
			want: 1,
		},
		{
			name: "two notes",
			body: "--- 2026-04-16 10:00:00 ---\nearlier note\n\n--- 2026-04-16 14:32:07 ---\nlater note",
			want: 2,
		},
		{
			name: "three notes with body text before",
			body: "original body\n\n--- 2026-01-01 00:00:00 ---\nn1\n\n--- 2026-01-02 00:00:00 ---\nn2\n\n--- 2026-01-03 00:00:00 ---\nn3",
			want: 3,
		},
		{
			name: "unrelated dashes in body",
			body: "--- not a separator\nsome --- content ---\n------\n--- YYYY-MM-DD HH:MM:SS ---",
			want: 0,
		},
		{
			name: "near-match with wrong date format",
			body: "--- 2026-4-16 14:32:07 ---\ntext",
			want: 0,
		},
		{
			name: "near-match missing seconds",
			body: "--- 2026-04-16 14:32 ---\ntext",
			want: 0,
		},
		{
			name: "near-match with trailing whitespace",
			body: "--- 2026-04-16 14:32:07 --- \ntext",
			want: 0,
		},
		{
			name: "separator not on own line",
			body: "prefix --- 2026-04-16 14:32:07 --- suffix",
			want: 0,
		},
		{
			name: "mix of valid and invalid separators",
			body: "--- 2026-04-16 14:32:07 ---\nvalid\n\n--- bad ---\n\n--- 2026-04-17 09:00:00 ---\nalso valid",
			want: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CountNotes(tc.body)
			if got != tc.want {
				t.Errorf("CountNotes(%q) = %d, want %d", tc.body, got, tc.want)
			}
		})
	}
}
