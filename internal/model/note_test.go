package model

import (
	"strings"
	"testing"
	"time"
)

// ddmmyyyy is the default display layout used across tests (DD-MM-YYYY).
const ddmmyyyy = "02-01-2006"

// ddmmyyyyRegex is the regex fragment matching dates rendered in ddmmyyyy.
const ddmmyyyyRegex = `\d{2}-\d{2}-\d{4}`

// isoLayout / isoRegex exercise an alternative layout to prove the
// layout/regex parameters are wired through rather than ignored.
const isoLayout = "2006-01-02"
const isoRegex = `\d{4}-\d{2}-\d{2}`

func TestAppendNote(t *testing.T) {
	ts := time.Date(2026, 4, 16, 14, 32, 7, 0, time.UTC)

	tests := []struct {
		name   string
		body   string
		text   string
		now    time.Time
		layout string
		want   string
	}{
		{
			name:   "empty body",
			body:   "",
			text:   "first note",
			now:    ts,
			layout: ddmmyyyy,
			want:   "--- 16-04-2026 14:32:07 ---\nfirst note",
		},
		{
			name:   "existing body",
			body:   "original body text",
			text:   "a note",
			now:    ts,
			layout: ddmmyyyy,
			want:   "original body text\n\n--- 16-04-2026 14:32:07 ---\na note",
		},
		{
			name:   "multi-line note text",
			body:   "some body",
			text:   "line one\nline two\nline three",
			now:    ts,
			layout: ddmmyyyy,
			want:   "some body\n\n--- 16-04-2026 14:32:07 ---\nline one\nline two\nline three",
		},
		{
			name:   "appending second note to body with existing note",
			body:   "--- 16-04-2026 10:00:00 ---\nearlier note",
			text:   "later note",
			now:    ts,
			layout: ddmmyyyy,
			want:   "--- 16-04-2026 10:00:00 ---\nearlier note\n\n--- 16-04-2026 14:32:07 ---\nlater note",
		},
		{
			name:   "different timestamp",
			body:   "",
			text:   "midnight note",
			now:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			layout: ddmmyyyy,
			want:   "--- 01-01-2026 00:00:00 ---\nmidnight note",
		},
		{
			name:   "alternative ISO layout",
			body:   "",
			text:   "iso layout note",
			now:    ts,
			layout: isoLayout,
			want:   "--- 2026-04-16 14:32:07 ---\niso layout note",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AppendNote(tc.body, tc.text, tc.now, tc.layout)
			if got != tc.want {
				t.Errorf("AppendNote(%q, %q, ..., %q)\ngot:  %q\nwant: %q", tc.body, tc.text, tc.layout, got, tc.want)
			}
		})
	}
}

func TestAppendNote_TimestampFormat(t *testing.T) {
	// Verify the separator line uses the expected format (DD-MM-YYYY default).
	ts := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	got := AppendNote("", "note", ts, ddmmyyyy)
	if !strings.HasPrefix(got, "--- 31-12-2026 23:59:59 ---") {
		t.Errorf("unexpected timestamp format in output: %q", got)
	}
}

func TestAppendNote_EmptyBody_NoLeadingBlankLine(t *testing.T) {
	ts := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	got := AppendNote("", "note", ts, ddmmyyyy)
	if strings.HasPrefix(got, "\n") {
		t.Errorf("empty body should not produce a leading blank line, got: %q", got)
	}
}

func TestCountNotes(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		dateRegex string
		want      int
	}{
		{
			name:      "empty body",
			body:      "",
			dateRegex: ddmmyyyyRegex,
			want:      0,
		},
		{
			name:      "body with no notes",
			body:      "just some plain body text\nwith multiple lines",
			dateRegex: ddmmyyyyRegex,
			want:      0,
		},
		{
			name:      "one note",
			body:      "--- 16-04-2026 14:32:07 ---\nfirst note",
			dateRegex: ddmmyyyyRegex,
			want:      1,
		},
		{
			name:      "two notes",
			body:      "--- 16-04-2026 10:00:00 ---\nearlier note\n\n--- 16-04-2026 14:32:07 ---\nlater note",
			dateRegex: ddmmyyyyRegex,
			want:      2,
		},
		{
			name:      "three notes with body text before",
			body:      "original body\n\n--- 01-01-2026 00:00:00 ---\nn1\n\n--- 02-01-2026 00:00:00 ---\nn2\n\n--- 03-01-2026 00:00:00 ---\nn3",
			dateRegex: ddmmyyyyRegex,
			want:      3,
		},
		{
			name:      "unrelated dashes in body",
			body:      "--- not a separator\nsome --- content ---\n------\n--- DD-MM-YYYY HH:MM:SS ---",
			dateRegex: ddmmyyyyRegex,
			want:      0,
		},
		{
			name:      "near-match with wrong date format (single-digit)",
			body:      "--- 2026-4-16 14:32:07 ---\ntext",
			dateRegex: ddmmyyyyRegex,
			want:      0,
		},
		{
			name:      "near-match missing seconds",
			body:      "--- 16-04-2026 14:32 ---\ntext",
			dateRegex: ddmmyyyyRegex,
			want:      0,
		},
		{
			name:      "near-match with trailing whitespace",
			body:      "--- 16-04-2026 14:32:07 --- \ntext",
			dateRegex: ddmmyyyyRegex,
			want:      0,
		},
		{
			name:      "separator not on own line",
			body:      "prefix --- 16-04-2026 14:32:07 --- suffix",
			dateRegex: ddmmyyyyRegex,
			want:      0,
		},
		{
			name:      "mix of valid and invalid separators",
			body:      "--- 16-04-2026 14:32:07 ---\nvalid\n\n--- bad ---\n\n--- 17-04-2026 09:00:00 ---\nalso valid",
			dateRegex: ddmmyyyyRegex,
			want:      2,
		},
		{
			name:      "old ISO-format separator is NOT recognized under DD-MM-YYYY regex",
			body:      "--- 2026-04-16 14:32:07 ---\nlegacy note",
			dateRegex: ddmmyyyyRegex,
			want:      0,
		},
		{
			name:      "ISO regex recognizes ISO separators",
			body:      "--- 2026-04-16 14:32:07 ---\niso note",
			dateRegex: isoRegex,
			want:      1,
		},
		{
			name:      "ISO regex does NOT recognize DD-MM-YYYY separators",
			body:      "--- 16-04-2026 14:32:07 ---\nddmm note",
			dateRegex: isoRegex,
			want:      0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CountNotes(tc.body, tc.dateRegex)
			if got != tc.want {
				t.Errorf("CountNotes(%q, %q) = %d, want %d", tc.body, tc.dateRegex, got, tc.want)
			}
		})
	}
}
