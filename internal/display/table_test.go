package display

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mmaksmas/monolog/internal/model"
)

// fixedNow is a deterministic reference time for all table tests.
var fixedNow = time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

func TestFormatTasks_Empty(t *testing.T) {
	var buf bytes.Buffer
	FormatTasks(&buf, nil, fixedNow)
	output := buf.String()
	if output != "No tasks.\n" {
		t.Errorf("expected 'No tasks.\\n', got %q", output)
	}
}

func TestFormatTasks_SingleTask(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Buy milk",
			Schedule:  "today",
			Status:    "open",
			Position:  1000,
			CreatedAt: fixedNow.Add(-2 * 24 * time.Hour).Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	output := buf.String()

	// Should contain short ID (first 8 chars)
	if !strings.Contains(output, "01ABCDEF") {
		t.Errorf("output should contain short ID '01ABCDEF', got:\n%s", output)
	}
	// Should contain title
	if !strings.Contains(output, "Buy milk") {
		t.Errorf("output should contain title 'Buy milk', got:\n%s", output)
	}
	// Should contain schedule
	if !strings.Contains(output, "today") {
		t.Errorf("output should contain schedule 'today', got:\n%s", output)
	}
	// Should contain position indicator as leading column
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(output), "\n")[0])
	if !strings.HasPrefix(line, "1 ") {
		t.Errorf("output should start with position indicator '1 ', got: %s", line)
	}
}

func TestFormatTasks_WithTags(t *testing.T) {
	tasks := []model.Task{
		{
			ID:       "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:    "Review PR",
			Schedule: "today",
			Status:   "open",
			Position: 1000,
			Tags:     []string{"work", "urgent"},
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	output := buf.String()

	if !strings.Contains(output, "work") {
		t.Errorf("output should contain tag 'work', got:\n%s", output)
	}
	if !strings.Contains(output, "urgent") {
		t.Errorf("output should contain tag 'urgent', got:\n%s", output)
	}
}

func TestFormatTasks_MultipleTasks(t *testing.T) {
	tasks := []model.Task{
		{
			ID:       "01AAAAAAAAAAAAAAAAAAAAAAAA",
			Title:    "First task",
			Schedule: "today",
			Status:   "open",
			Position: 1000,
		},
		{
			ID:       "01BBBBBBBBBBBBBBBBBBBBBBBB",
			Title:    "Second task",
			Schedule: "today",
			Status:   "open",
			Position: 2000,
		},
		{
			ID:       "01CCCCCCCCCCCCCCCCCCCCCCCC",
			Title:    "Third task",
			Schedule: "tomorrow",
			Status:   "open",
			Position: 3000,
			Tags:     []string{"work"},
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Should have 3 task lines (no header)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d:\n%s", len(lines), output)
	}

	// Position indicators should be sequential — check leading column
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "1 ") {
		t.Errorf("first line should start with position '1 ', got: %s", lines[0])
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[1]), "2 ") {
		t.Errorf("second line should start with position '2 ', got: %s", lines[1])
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[2]), "3 ") {
		t.Errorf("third line should start with position '3 ', got: %s", lines[2])
	}
}

func TestFormatTasks_NoTags(t *testing.T) {
	tasks := []model.Task{
		{
			ID:       "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:    "No tags task",
			Schedule: "today",
			Status:   "open",
			Position: 1000,
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	output := buf.String()

	// Should still produce valid output without tags
	if !strings.Contains(output, "No tags task") {
		t.Errorf("output should contain title, got:\n%s", output)
	}
}

func TestFormatTasks_DoneStatus(t *testing.T) {
	tasks := []model.Task{
		{
			ID:       "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:    "Completed task",
			Schedule: "today",
			Status:   "done",
			Position: 1000,
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	output := buf.String()

	// Done tasks should show "x" as the leading marker column
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(output), "\n")[0])
	if !strings.HasPrefix(line, "x ") {
		t.Errorf("done task line should start with 'x ', got: %s", line)
	}
}

func TestShortID(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"01ABCDEFGHIJKLMNOPQRSTUVWX", "01ABCDEF"},
		{"01AB", "01AB"},    // shorter than 8
		{"", ""},            // empty
		{"12345678", "12345678"}, // exactly 8
	}

	for _, tt := range tests {
		got := ShortID(tt.id)
		if got != tt.want {
			t.Errorf("ShortID(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestFormatTasks_OpenWithRecentDate(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Recent task",
			Schedule:  "today",
			Status:    "open",
			Position:  1000,
			CreatedAt: fixedNow.Add(-2 * 24 * time.Hour).Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	output := buf.String()

	// Should contain the compact date "2d" for a task created 2 days ago
	if !strings.Contains(output, "2d") {
		t.Errorf("output should contain date '2d' for task created 2 days ago, got:\n%s", output)
	}
}

func TestPadRight_MaxWidthDates(t *testing.T) {
	// Worst-case dates column: cross-year created + cross-year done = "YY-MM-DD→YY-MM-DD" = 17 runes.
	maxDates := "25-01-15→26-04-13"
	padded := padRight(maxDates, 17)
	if runeLen := utf8.RuneCountInString(padded); runeLen != 17 {
		t.Errorf("padRight(%q, 17) has %d runes, want 17", maxDates, runeLen)
	}
	if padded != maxDates {
		t.Errorf("padRight(%q, 17) = %q, want no padding for exact-width input", maxDates, padded)
	}

	// Shorter date string should be padded to 17 runes.
	short := "2d→1h"
	padded = padRight(short, 17)
	if runeLen := utf8.RuneCountInString(padded); runeLen != 17 {
		t.Errorf("padRight(%q, 17) has %d runes, want 17", short, runeLen)
	}
}

func TestFormatTasks_CrossYearDoneDatesAlignment(t *testing.T) {
	// Cross-year created + cross-year completed done task — max-width dates column.
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Cross-year done task",
			Schedule:  "today",
			Status:    "done",
			Position:  1000,
			CreatedAt: time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
			UpdatedAt: time.Date(2025, 12, 20, 14, 0, 0, 0, time.UTC).Format(time.RFC3339),
		},
	}

	// Use a now in 2026 so both timestamps are cross-year.
	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	output := buf.String()

	// Should contain the max-width dates "25-01-15→25-12-20".
	want := "25-01-15→25-12-20"
	if !strings.Contains(output, want) {
		t.Errorf("output should contain %q, got:\n%s", want, output)
	}
}

func TestTruncatePad(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"shorter pads", "abc", 5, "abc  "},
		{"exact width unchanged", "abcde", 5, "abcde"},
		{"longer truncated with ellipsis", "abcdefgh", 5, "abcd…"},
		{"multibyte truncation keeps rune boundary", "abcdé fgh", 5, "abcd…"},
		{"width 1 only ellipsis when too long", "abc", 1, "…"},
	}
	for _, tt := range tests {
		got := truncatePad(tt.in, tt.width)
		if got != tt.want {
			t.Errorf("%s: truncatePad(%q, %d) = %q, want %q", tt.name, tt.in, tt.width, got, tt.want)
		}
		if runeLen := utf8.RuneCountInString(got); runeLen != tt.width {
			t.Errorf("%s: truncatePad(%q, %d) has %d runes, want %d", tt.name, tt.in, tt.width, runeLen, tt.width)
		}
	}
}

func TestFormatTasks_LongTitleAlignment(t *testing.T) {
	// A very long title must not push subsequent columns past their fixed positions.
	longTitle := strings.Repeat("x", 100)
	tasks := []model.Task{
		{
			ID:       "01AAAAAAAAAAAAAAAAAAAAAAAA",
			Title:    "short",
			Schedule: "today",
			Status:   "open",
			Position: 1000,
		},
		{
			ID:       "01BBBBBBBBBBBBBBBBBBBBBBBB",
			Title:    longTitle,
			Schedule: "today",
			Status:   "open",
			Position: 2000,
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	output := buf.String()
	// Split on newline but do NOT TrimSpace the whole output, because
	// lines start with a 2-char active marker ("  " for non-active) and
	// TrimSpace would strip the leading spaces on the first line.
	lines := strings.Split(output, "\n")
	// Drop empty trailing element from the final newline.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), output)
	}

	// Schedule ("today") should appear at the same rune offset on both lines.
	// Compare rune offsets, not byte offsets, because "…" is a multi-byte rune.
	runeIndex := func(s, sub string) int {
		i := strings.Index(s, sub)
		if i < 0 {
			return -1
		}
		return utf8.RuneCountInString(s[:i])
	}
	idx0 := runeIndex(lines[0], "today")
	idx1 := runeIndex(lines[1], "today")
	if idx0 == -1 || idx1 == -1 {
		t.Fatalf("'today' missing from a line: %q / %q", lines[0], lines[1])
	}
	if idx0 != idx1 {
		t.Errorf("schedule column misaligned: short-title line has 'today' at rune %d, long-title line at %d\n%s", idx0, idx1, output)
	}

	// Long title must be truncated with ellipsis.
	if !strings.Contains(lines[1], "…") {
		t.Errorf("long title should be truncated with '…', got: %s", lines[1])
	}
	if strings.Contains(lines[1], strings.Repeat("x", titleColWidth+1)) {
		t.Errorf("long title was not truncated to %d runes, got: %s", titleColWidth, lines[1])
	}
}

func TestFormatTasks_DoneWithBothDates(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Done task",
			Schedule:  "today",
			Status:    "done",
			Position:  1000,
			CreatedAt: fixedNow.Add(-5 * 24 * time.Hour).Format(time.RFC3339),
			UpdatedAt: fixedNow.Add(-1 * time.Hour).Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	output := buf.String()

	// Should contain the compact dates "5d→1h" for a done task
	if !strings.Contains(output, "5d\u21921h") {
		t.Errorf("output should contain '5d\u21921h' for done task, got:\n%s", output)
	}
}

func TestFormatTasks_ActiveMarker(t *testing.T) {
	tasks := []model.Task{
		{
			ID:       "01AAAAAAAAAAAAAAAAAAAAAAAA",
			Title:    "Active task",
			Schedule: "today",
			Status:   "open",
			Position: 1000,
			Tags:     []string{model.ActiveTag, "work"},
		},
		{
			ID:       "01BBBBBBBBBBBBBBBBBBBBBBBB",
			Title:    "Inactive task",
			Schedule: "today",
			Status:   "open",
			Position: 2000,
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow)
	// Split on newline directly (do not TrimSpace — it strips the leading
	// "  " active-marker prefix on inactive rows). Drop trailing empty
	// elements from the final newline, matching the LongTitleAlignment pattern.
	lines := strings.Split(buf.String(), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), buf.String())
	}

	// Active task line should start with "* "
	if !strings.HasPrefix(lines[0], "* ") {
		t.Errorf("active task line should start with '* ', got: %q", lines[0])
	}

	// Non-active task line should start with "  " (two spaces, not a star)
	if !strings.HasPrefix(lines[1], "  ") {
		t.Errorf("non-active task line should start with '  ' (two spaces), got: %q", lines[1])
	}

	// The star marker should not appear on the non-active line's prefix
	if strings.HasPrefix(lines[1], "* ") {
		t.Errorf("non-active task should not have '* ' prefix, got: %q", lines[1])
	}
}
