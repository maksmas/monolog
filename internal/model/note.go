package model

import (
	"fmt"
	"regexp"
	"time"
)

// AppendNote appends a timestamped note to the body string.
// It formats the separator as "--- <date> HH:MM:SS ---" using the given
// date layout (Go layout string, e.g. "02-01-2006" for DD-MM-YYYY),
// followed by the note text. When the body is empty, no leading blank
// line is inserted.
//
// The layout parameter is explicit rather than pulled from internal/config
// so that this package stays free of a config dependency and is easy to
// unit-test with arbitrary layouts.
func AppendNote(body, text string, now time.Time, layout string) string {
	separator := fmt.Sprintf("--- %s ---", now.Format(layout+" 15:04:05"))
	if body == "" {
		return separator + "\n" + text
	}
	return body + "\n\n" + separator + "\n" + text
}

// CountNotes returns the number of note separator lines in body, counting
// lines that exactly match the "--- <date> HH:MM:SS ---" format where
// <date> is matched by the supplied dateRegex fragment (typically produced
// by config.DateRegex for the currently configured layout). Lines that
// merely contain dashes or mismatched formats are ignored.
//
// Backward compatibility note: this function only recognizes separators in
// the currently configured format. Separators written by older monolog
// versions that used a different date layout will stop contributing to the
// count after the format changes. This is an intentional trade-off — no
// migration is performed.
func CountNotes(body, dateRegex string) int {
	if body == "" {
		return 0
	}
	// Anchored per line, exact match on "--- <date> HH:MM:SS ---".
	re := regexp.MustCompile(`(?m)^--- ` + dateRegex + ` \d{2}:\d{2}:\d{2} ---$`)
	return len(re.FindAllStringIndex(body, -1))
}
