package display

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mmaksmas/monolog/internal/model"
)

// titleColWidth is the fixed rune width for the title column in `ls` output.
// Longer titles are truncated with a trailing "…".
const titleColWidth = 40

// padRight pads s with spaces to the given width based on rune count;
// assumes single-column characters. Multi-byte runes like → (U+2192,
// 3 bytes, 1 rune) are counted as one column, which is correct on most
// terminals but may misalign on CJK terminals where ambiguous-width
// characters occupy two columns.
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// truncatePad truncates s to width runes (with a trailing ellipsis) if it is
// too long, or pads it with spaces to exactly width runes otherwise. Width
// must be >= 1. Like padRight, it assumes single-column characters.
func truncatePad(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n == width {
		return s
	}
	if n < width {
		return s + strings.Repeat(" ", width-n)
	}
	// Truncate: keep width-1 runes and append "…".
	runes := []rune(s)
	return string(runes[:width-1]) + "…"
}

// ShortID returns the first 8 characters of a task ID, or the full ID if shorter.
func ShortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// FormatTasks writes tasks as a clean terminal table to w.
// Each line shows: position indicator, short ID, title, schedule, dates, tags.
// The now parameter is used to compute compact relative dates.
func FormatTasks(w io.Writer, tasks []model.Task, now time.Time) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}

	for i, task := range tasks {
		marker := fmt.Sprintf("%d", i+1)
		if task.Status == "done" {
			marker = "x"
		}

		tags := ""
		if len(task.Tags) > 0 {
			tags = "[" + strings.Join(task.Tags, ", ") + "]"
		}

		dates := FormatTaskDates(now, task)

		// Title is truncated/padded to titleColWidth runes so subsequent columns
		// stay aligned even for long titles. 17-rune pad on dates: worst case is
		// "YY-MM-DD→YY-MM-DD" (17 runes). See TestPadRight_MaxWidthDates.
		fmt.Fprintf(w, "%-4s %-8s  %s %-10s %s %s\n",
			marker,
			ShortID(task.ID),
			truncatePad(task.Title, titleColWidth),
			task.Schedule,
			padRight(dates, 17),
			tags,
		)
	}
}
