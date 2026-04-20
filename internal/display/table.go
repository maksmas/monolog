package display

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
)

// separatorLine is the horizontal rule printed after each task block in full mode.
const separatorLine = "────────────────────────────────────────────────────────────"

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

// FormatTasksFull writes tasks as multi-line detail blocks to w.
// Each block shows a header line, indented metadata, and an optional indented
// body (3-space indent), followed by a horizontal separator rule. Body lines
// are indented here, unlike `monolog show` which prints the body flush-left.
// When tasks is empty it writes "No tasks.\n" (same as FormatTasks).
// layout is the configured date format (Go layout), e.g. config.DateFormat().
func FormatTasksFull(w io.Writer, tasks []model.Task, now time.Time, layout string) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}

	for i, task := range tasks {
		activeMarker := "  "
		if task.IsActive() {
			activeMarker = "* "
		}

		marker := strconv.Itoa(i + 1)
		if task.Status == "done" {
			marker = "x"
		}

		// Header: active marker + position/done indicator + short ID + full title (no truncation)
		fmt.Fprintf(w, "%s%-4s %-8s  %s\n",
			activeMarker,
			marker,
			ShortID(task.ID),
			task.Title,
		)

		// Metadata — 10-char label field (%-10s) giving the same visual alignment as
		// show.go, which hand-pads its labels to 10 chars using fixed string literals.
		fmt.Fprintf(w, "   %-10s %s\n", "Status:", task.Status)

		bucket := schedule.Bucket(task.Schedule, now)
		displayDate := schedule.FormatDisplay(task.Schedule, layout)
		if bucket == task.Schedule {
			fmt.Fprintf(w, "   %-10s %s\n", "Schedule:", bucket)
		} else {
			fmt.Fprintf(w, "   %-10s %s (%s)\n", "Schedule:", bucket, displayDate)
		}

		if task.Recurrence != "" {
			fmt.Fprintf(w, "   %-10s %s\n", "Recur:", task.Recurrence)
		}

		if vt := VisibleTags(task.Tags); len(vt) > 0 {
			fmt.Fprintf(w, "   %-10s %s\n", "Tags:", strings.Join(vt, ", "))
		}

		fmt.Fprintf(w, "   %-10s %s\n", "Created:", task.CreatedAt)

		if task.UpdatedAt != "" {
			fmt.Fprintf(w, "   %-10s %s\n", "Updated:", task.UpdatedAt)
		}

		if task.CompletedAt != "" {
			fmt.Fprintf(w, "   %-10s %s\n", "Completed:", task.CompletedAt)
		}

		if task.NoteCount > 0 {
			fmt.Fprintf(w, "   %-10s %d\n", "Notes:", task.NoteCount)
		}

		// Body (indented, blank line before)
		if task.Body != "" {
			fmt.Fprintln(w)
			for _, line := range strings.Split(task.Body, "\n") {
				fmt.Fprintf(w, "   %s\n", line)
			}
		}

		// Separator
		fmt.Fprintln(w, separatorLine)
	}
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

		marker := strconv.Itoa(i + 1)
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
