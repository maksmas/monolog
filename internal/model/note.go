package model

import (
	"fmt"
	"time"
)

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
