package display

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
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

// VisibleTags returns a copy of tags with the reserved ActiveTag filtered out,
// so the internal active marker is not shown to the user.
func VisibleTags(tags []string) []string {
	var out []string
	for _, tag := range tags {
		if tag != model.ActiveTag {
			out = append(out, tag)
		}
	}
	return out
}

// FormatTasks writes tasks as a clean terminal table to w.
// Each line shows: position indicator, short ID, title, schedule, dates, tags.
// The now parameter is used to compute compact relative dates. layout is the
// configured date format (Go layout) that controls how far dates are rendered
// (callers typically pass config.DateFormat()).
func FormatTasks(w io.Writer, tasks []model.Task, now time.Time, layout string) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}

	for i, task := range tasks {
		activeMarker := "  "
		if task.IsActive() {
			activeMarker = "* "
		}

		marker := fmt.Sprintf("%d", i+1)
		if task.Status == "done" {
			marker = "x"
		}

		tags := ""
		if vt := VisibleTags(task.Tags); len(vt) > 0 {
			tags = "[" + strings.Join(vt, ", ") + "]"
		}

		dates := FormatTaskDates(now, task, layout)

		// Render the schedule column through FormatDisplay so stored ISO dates
		// (e.g. "2030-04-15") appear in the configured user-facing format
		// (default DD-MM-YYYY → "15-04-2030"). Legacy bucket strings ("today",
		// "tomorrow", etc.) pass through unchanged because FormatDisplay only
		// reformats inputs that parse as IsoLayout.
		scheduleCell := schedule.FormatDisplay(task.Schedule, layout)

		// Title is truncated/padded to titleColWidth runes so subsequent columns
		// stay aligned even for long titles. 17-rune pad on dates: worst case is
		// "YY-MM-DD→YY-MM-DD" (17 runes). See TestPadRight_MaxWidthDates.
		fmt.Fprintf(w, "%s%-4s %-8s  %s %-10s %s %s\n",
			activeMarker,
			marker,
			ShortID(task.ID),
			truncatePad(task.Title, titleColWidth),
			scheduleCell,
			padRight(dates, 17),
			tags,
		)
	}
}
