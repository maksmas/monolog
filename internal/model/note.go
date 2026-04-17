package model

import (
	"fmt"
	"regexp"
	"time"
)

// noteSeparatorRegex matches the exact separator line produced by AppendNote:
// a line consisting solely of "--- YYYY-MM-DD HH:MM:SS ---".
var noteSeparatorRegex = regexp.MustCompile(`(?m)^--- \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} ---$`)

// AppendNote appends a timestamped note to the body string.
// It formats the separator as "--- YYYY-MM-DD HH:MM:SS ---" followed by the
// note text. When the body is empty, no leading blank line is inserted.
func AppendNote(body, text string, now time.Time) string {
	separator := fmt.Sprintf("--- %s ---", now.Format("2006-01-02 15:04:05"))
	if body == "" {
		return separator + "\n" + text
	}
	return body + "\n\n" + separator + "\n" + text
}

// CountNotes returns the number of note separator lines in body, counting
// lines that exactly match the "--- YYYY-MM-DD HH:MM:SS ---" format produced
// by AppendNote. Lines that merely contain dashes or mismatched formats are
// ignored.
func CountNotes(body string) int {
	if body == "" {
		return 0
	}
	return len(noteSeparatorRegex.FindAllStringIndex(body, -1))
}
