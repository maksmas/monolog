package display

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/maksmas/monolog/internal/model"
)

// fixedNow is a deterministic reference time for all table tests.
var fixedNow = time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

func TestFormatTasks_Empty(t *testing.T) {
	var buf bytes.Buffer
	FormatTasks(&buf, nil, fixedNow, ddmmyyyy)
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
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
		{"01AB", "01AB"},         // shorter than 8
		{"", ""},                 // empty
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()

	// Should contain the compact date "2d" for a task created 2 days ago
	if !strings.Contains(output, "2d") {
		t.Errorf("output should contain date '2d' for task created 2 days ago, got:\n%s", output)
	}
}

func TestPadRight_MaxWidthDates(t *testing.T) {
	// Worst-case dates column: cross-year created + cross-year done = "DD-MM-YY→DD-MM-YY" = 17 runes.
	maxDates := "15-01-25→13-04-26"
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()

	// Should contain the max-width dates "15-01-25→20-12-25" (DD-MM-YY).
	want := "15-01-25→20-12-25"
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
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
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
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

// TestFormatTasks_AlternativeLayout proves the layout parameter flows all
// the way from FormatTasks through FormatTaskDates into the rendered output.
func TestFormatTasks_AlternativeLayout(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Old task",
			Schedule:  "today",
			Status:    "open",
			Position:  1000,
			CreatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow, yyyymmdd)
	output := buf.String()

	// With layout "2006-01-02" same-year strips the year -> "03-01".
	if !strings.Contains(output, "03-01") {
		t.Errorf("output should contain '03-01' (MM-DD) under YYYY-MM-DD layout, got:\n%s", output)
	}
	// Must NOT contain the default DD-MM rendering "01-03".
	// (We can't just check absence of "01-03" because the schedule column
	// could incidentally contain those characters; check the dates column
	// rendering directly.)
	dates := FormatTaskDates(fixedNow, tasks[0], yyyymmdd)
	if dates != "03-01" {
		t.Errorf("FormatTaskDates under YYYY-MM-DD = %q, want %q", dates, "03-01")
	}
}

// TestFormatTasks_ISOScheduleRendersInConfiguredLayout verifies that stored
// ISO schedules are rendered through schedule.FormatDisplay in the configured
// user-facing layout (the plan's stated goal — do not leak ISO storage format
// into the schedule column).
func TestFormatTasks_ISOScheduleRendersInConfiguredLayout(t *testing.T) {
	tasks := []model.Task{
		{
			ID:       "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:    "Dated task",
			Schedule: "2030-04-15",
			Status:   "open",
			Position: 1000,
		},
	}

	// Under the default DD-MM-YYYY layout the schedule must render as
	// 15-04-2030, NOT as the stored 2030-04-15.
	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()
	if !strings.Contains(output, "15-04-2030") {
		t.Errorf("output should contain schedule in DD-MM-YYYY (15-04-2030), got:\n%s", output)
	}
	if strings.Contains(output, "2030-04-15") {
		t.Errorf("output should NOT contain stored ISO schedule 2030-04-15, got:\n%s", output)
	}

	// Under an alternative layout the schedule must render in that layout,
	// proving the parameter is wired through (not hardcoded).
	buf.Reset()
	FormatTasks(&buf, tasks, fixedNow, "01/02/2006")
	output = buf.String()
	if !strings.Contains(output, "04/15/2030") {
		t.Errorf("output should contain schedule in MM/DD/YYYY (04/15/2030), got:\n%s", output)
	}
}

// TestFormatTasks_BucketSchedulePassesThrough verifies that legacy bucket
// strings (e.g. "today") render unchanged in the schedule column — they are
// not valid ISO dates, so FormatDisplay returns them as-is.
func TestFormatTasks_BucketSchedulePassesThrough(t *testing.T) {
	tasks := []model.Task{
		{
			ID:       "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:    "Bucket task",
			Schedule: "tomorrow",
			Status:   "open",
			Position: 1000,
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()
	if !strings.Contains(output, "tomorrow") {
		t.Errorf("output should contain legacy bucket schedule 'tomorrow' unchanged, got:\n%s", output)
	}
}

func TestVisibleTags(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil input", nil, nil},
		{"empty input", []string{}, nil},
		{"no active tag", []string{"work", "urgent"}, []string{"work", "urgent"}},
		{"only active tag", []string{"active"}, nil},
		{"active with others", []string{"active", "work", "personal"}, []string{"work", "personal"}},
		{"active in middle", []string{"work", "active", "personal"}, []string{"work", "personal"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VisibleTags(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("VisibleTags(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("VisibleTags(%v)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// ---- FormatTasksFull tests ----

func TestFormatTasksFull_Empty(t *testing.T) {
	var buf bytes.Buffer
	FormatTasksFull(&buf, nil, fixedNow, ddmmyyyy)
	if buf.String() != "No tasks.\n" {
		t.Errorf("empty slice: expected 'No tasks.\\n', got %q", buf.String())
	}
}

func TestFormatTasksFull_SingleTask(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Fix login bug",
			Schedule:  "today",
			Status:    "open",
			Position:  1000,
			CreatedAt: fixedNow.Add(-24 * time.Hour).Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	FormatTasksFull(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()

	// Title must appear untruncated
	if !strings.Contains(output, "Fix login bug") {
		t.Errorf("output should contain full title, got:\n%s", output)
	}
	// Short ID
	if !strings.Contains(output, "01ABCDEF") {
		t.Errorf("output should contain short ID '01ABCDEF', got:\n%s", output)
	}
	// Status metadata line
	if !strings.Contains(output, "Status:") {
		t.Errorf("output should contain 'Status:' label, got:\n%s", output)
	}
	// Schedule metadata line
	if !strings.Contains(output, "Schedule:") {
		t.Errorf("output should contain 'Schedule:' label, got:\n%s", output)
	}
	// Separator line
	if !strings.Contains(output, separatorLine) {
		t.Errorf("output should contain separator line, got:\n%s", output)
	}
}

func TestFormatTasksFull_WithBody(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Task with body",
			Schedule:  "today",
			Status:    "open",
			Position:  1000,
			CreatedAt: fixedNow.Format(time.RFC3339),
			Body:      "Line one\nLine two",
		},
	}

	var buf bytes.Buffer
	FormatTasksFull(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()

	// Body lines should appear indented
	if !strings.Contains(output, "   Line one") {
		t.Errorf("output should contain indented body line 'Line one', got:\n%s", output)
	}
	if !strings.Contains(output, "   Line two") {
		t.Errorf("output should contain indented body line 'Line two', got:\n%s", output)
	}
	// Separator must follow
	if !strings.Contains(output, separatorLine) {
		t.Errorf("output should contain separator line, got:\n%s", output)
	}
}

func TestFormatTasksFull_Omitempty(t *testing.T) {
	// Recur/Tags/Updated/Completed/NoteCount should be absent when unset.
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Minimal task",
			Schedule:  "today",
			Status:    "open",
			Position:  1000,
			CreatedAt: fixedNow.Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	FormatTasksFull(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()

	for _, absent := range []string{"Recur:", "Tags:", "Updated:", "Completed:", "Notes:"} {
		if strings.Contains(output, absent) {
			t.Errorf("output should NOT contain %q when field is unset, got:\n%s", absent, output)
		}
	}
}

func TestFormatTasksFull_ActiveMarker(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01AAAAAAAAAAAAAAAAAAAAAAAA",
			Title:     "Active task",
			Schedule:  "today",
			Status:    "open",
			Position:  1000,
			CreatedAt: fixedNow.Format(time.RFC3339),
			Tags:      []string{model.ActiveTag, "work"},
		},
		{
			ID:        "01BBBBBBBBBBBBBBBBBBBBBBBB",
			Title:     "Inactive task",
			Schedule:  "today",
			Status:    "open",
			Position:  2000,
			CreatedAt: fixedNow.Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	FormatTasksFull(&buf, tasks, fixedNow, ddmmyyyy)
	lines := strings.Split(buf.String(), "\n")

	// First non-empty line should be the active task header starting with "* "
	var firstHeader, secondHeader string
	for _, l := range lines {
		if strings.Contains(l, "Active task") {
			firstHeader = l
		}
		if strings.Contains(l, "Inactive task") {
			secondHeader = l
		}
	}

	if !strings.HasPrefix(firstHeader, "* ") {
		t.Errorf("active task header should start with '* ', got: %q", firstHeader)
	}
	if strings.HasPrefix(secondHeader, "* ") {
		t.Errorf("inactive task header should NOT start with '* ', got: %q", secondHeader)
	}
	// Inactive header must start with two spaces (not a star).
	if !strings.HasPrefix(secondHeader, "  ") {
		t.Errorf("inactive task header should start with '  ' (two spaces), got: %q", secondHeader)
	}
}

// TestFormatTasksFull_ISOScheduleRendersInConfiguredLayout verifies that the
// layout parameter is wired through: a stored ISO schedule should render in the
// configured user-facing format, not as the raw ISO storage string.
func TestFormatTasksFull_ISOScheduleRendersInConfiguredLayout(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Future task",
			Schedule:  "2030-04-15",
			Status:    "open",
			Position:  1000,
			CreatedAt: fixedNow.Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	FormatTasksFull(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()

	// Under DD-MM-YYYY the stored ISO "2030-04-15" must render as "15-04-2030".
	if !strings.Contains(output, "15-04-2030") {
		t.Errorf("output should contain schedule in DD-MM-YYYY (15-04-2030), got:\n%s", output)
	}
	if strings.Contains(output, "2030-04-15") {
		t.Errorf("output should NOT contain raw ISO schedule 2030-04-15, got:\n%s", output)
	}
}

// TestFormatTasksFull_DoneStatus verifies that a done task renders the "x"
// marker, shows "done" in the Status line, and shows a Completed: line.
func TestFormatTasksFull_DoneStatus(t *testing.T) {
	completedAt := fixedNow.Add(-1 * time.Hour).Format(time.RFC3339)
	tasks := []model.Task{
		{
			ID:          "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:       "Done task",
			Schedule:    "today",
			Status:      "done",
			Position:    1000,
			CreatedAt:   fixedNow.Add(-24 * time.Hour).Format(time.RFC3339),
			CompletedAt: completedAt,
		},
	}

	var buf bytes.Buffer
	FormatTasksFull(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()
	lines := strings.Split(output, "\n")

	// Find header line (contains title)
	var headerLine string
	for _, l := range lines {
		if strings.Contains(l, "Done task") {
			headerLine = l
			break
		}
	}
	// Header should start with "  x " (inactive done task)
	if !strings.HasPrefix(headerLine, "  x ") {
		t.Errorf("done task header should start with '  x ', got: %q", headerLine)
	}
	// Status metadata must say "done"
	if !strings.Contains(output, "Status:") {
		t.Errorf("output should contain 'Status:' label, got:\n%s", output)
	}
	// Completed: line must be present
	if !strings.Contains(output, "Completed:") {
		t.Errorf("output should contain 'Completed:' for done task, got:\n%s", output)
	}
	// Status value must be "done"
	if !strings.Contains(output, "done") {
		t.Errorf("output should contain 'done' as status value, got:\n%s", output)
	}
}

// TestFormatTasksFull_NoteCount verifies that a positive NoteCount renders a
// "Notes:" line with the correct count.
func TestFormatTasksFull_NoteCount(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Task with notes",
			Schedule:  "today",
			Status:    "open",
			Position:  1000,
			CreatedAt: fixedNow.Format(time.RFC3339),
			NoteCount: 3,
		},
	}

	var buf bytes.Buffer
	FormatTasksFull(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()

	if !strings.Contains(output, "Notes:") {
		t.Errorf("output should contain 'Notes:' label for NoteCount>0, got:\n%s", output)
	}
	if !strings.Contains(output, "3") {
		t.Errorf("output should contain note count '3', got:\n%s", output)
	}
}

// TestFormatTasksFull_SingleTask_CreatedLine verifies that the Created: metadata
// line is always present (it is not omitempty).
func TestFormatTasksFull_SingleTask_CreatedLine(t *testing.T) {
	tasks := []model.Task{
		{
			ID:        "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:     "Task with created",
			Schedule:  "today",
			Status:    "open",
			Position:  1000,
			CreatedAt: fixedNow.Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	FormatTasksFull(&buf, tasks, fixedNow, ddmmyyyy)
	output := buf.String()

	if !strings.Contains(output, "Created:") {
		t.Errorf("output should always contain 'Created:' metadata line, got:\n%s", output)
	}
}

// searchRowIDCol is the column at which the title cell starts in
// FormatSearchResults output: a 2-character status cell, the ID prefix, and
// the two-space separator after it.
const searchRowIDCol = 2 + searchIDWidth + 2

// searchLines renders tasks through FormatSearchResults and splits the output
// into its (newline-free) rows.
func searchLines(t *testing.T, tasks []model.Task, layout string) []string {
	t.Helper()
	var buf bytes.Buffer
	FormatSearchResults(&buf, tasks, layout)
	out := strings.TrimSuffix(buf.String(), "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestFormatSearchResults_Empty(t *testing.T) {
	var buf bytes.Buffer
	FormatSearchResults(&buf, nil, ddmmyyyy)
	if got := buf.String(); got != "No matches.\n" {
		t.Errorf("expected %q, got %q", "No matches.\n", got)
	}

	buf.Reset()
	FormatSearchResults(&buf, []model.Task{}, ddmmyyyy)
	if got := buf.String(); got != "No matches.\n" {
		t.Errorf("empty slice: expected %q, got %q", "No matches.\n", got)
	}
}

// TestFormatSearchResults_LongTitleNotTruncated is the whole point of the
// formatter: `ls` cuts titles at titleColWidth (40) runes, which makes it unfit
// for dedupe. Search output must show the title in full.
func TestFormatSearchResults_LongTitleNotTruncated(t *testing.T) {
	long := "Implement the date impact assessment migration for the reporting pipeline"
	if utf8.RuneCountInString(long) <= titleColWidth {
		t.Fatalf("fixture must be longer than %d runes, got %d", titleColWidth, utf8.RuneCountInString(long))
	}

	tasks := []model.Task{
		{ID: "01ABCDEFGHIJKLMNOPQRSTUVWX", Title: long, Schedule: "today", Status: "open"},
	}

	var buf bytes.Buffer
	FormatSearchResults(&buf, tasks, ddmmyyyy)
	output := buf.String()
	if !strings.Contains(output, long) {
		t.Errorf("output should contain the full untruncated title, got:\n%s", output)
	}
	if strings.Contains(output, "…") {
		t.Errorf("output should not contain a truncation ellipsis, got:\n%s", output)
	}
}

// TestFormatSearchResults_OverCapTitleDoesNotWidenOtherRows pins the
// searchTitleCapWidth pad cap: an over-long title pushes only its own trailing
// columns right, it does not tax every other row in the set.
func TestFormatSearchResults_OverCapTitleDoesNotWidenOtherRows(t *testing.T) {
	long := strings.Repeat("x", 150)
	tasks := []model.Task{
		{ID: "01AAAAAAAAAAAAAAAAAAAAAAAA", Title: long, Schedule: "today", Status: "open"},
		{ID: "01BBBBBBBBBBBBBBBBBBBBBBBB", Title: "Short one", Schedule: "week", Status: "open"},
	}

	lines := searchLines(t, tasks, ddmmyyyy)
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows, got %d: %q", len(lines), lines)
	}

	// The long title is still printed in full.
	if !strings.Contains(lines[0], long) {
		t.Errorf("row 0 should contain the full 150-rune title, got: %q", lines[0])
	}
	// The short row's schedule sits at the capped offset, not 150 runes out.
	idx := strings.Index(lines[1], "week")
	if idx < 0 {
		t.Fatalf("row 1 should contain schedule 'week', got: %q", lines[1])
	}
	if maxCol := searchRowIDCol + searchTitleCapWidth + 1; idx > maxCol {
		t.Errorf("row 1 schedule at column %d (max %d) — the over-long title widened every row: %q", idx, maxCol, lines[1])
	}
}

// TestFormatSearchResults_ColumnsAlignAcrossMixedTitles verifies titles shorter
// than the cap are padded to the widest title in the set, so the schedule
// column starts at the same offset on every row.
func TestFormatSearchResults_ColumnsAlignAcrossMixedTitles(t *testing.T) {
	widest := "A somewhat longer title here"
	tasks := []model.Task{
		{ID: "01AAAAAAAAAAAAAAAAAAAAAAAA", Title: "Tiny", Schedule: "today", Status: "open"},
		{ID: "01BBBBBBBBBBBBBBBBBBBBBBBB", Title: widest, Schedule: "today", Status: "open"},
		{ID: "01CCCCCCCCCCCCCCCCCCCCCCCC", Title: "Mid length", Schedule: "today", Status: "open"},
	}

	lines := searchLines(t, tasks, ddmmyyyy)
	if len(lines) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(lines))
	}

	want := strings.Index(lines[0], "today")
	if want < 0 {
		t.Fatalf("row 0 missing schedule cell: %q", lines[0])
	}
	for i, line := range lines[1:] {
		if got := strings.Index(line, "today"); got != want {
			t.Errorf("row %d schedule column at %d, want %d (misaligned):\n%q\n%q", i+1, got, want, lines[0], line)
		}
	}

	// Padding tracks the widest title in the set, not a fixed width.
	if wantCol := searchRowIDCol + utf8.RuneCountInString(widest) + 1; want != wantCol {
		t.Errorf("schedule column at %d, want %d (pad should equal the widest title)", want, wantCol)
	}
}

// TestFormatSearchResults_StatusCell covers the 2-char status cell, including
// done-beats-active precedence (done auto-deactivates, so both should never
// hold at once — pin the precedence anyway).
func TestFormatSearchResults_StatusCell(t *testing.T) {
	tests := []struct {
		name   string
		task   model.Task
		prefix string
	}{
		{"open", model.Task{ID: "01AAAAAAAA", Title: "T", Schedule: "today", Status: "open"}, "  "},
		{"active", model.Task{ID: "01AAAAAAAA", Title: "T", Schedule: "today", Status: "open", Tags: []string{model.ActiveTag}}, "* "},
		{"done", model.Task{ID: "01AAAAAAAA", Title: "T", Schedule: "today", Status: "done"}, "x "},
		{"done beats active", model.Task{ID: "01AAAAAAAA", Title: "T", Schedule: "today", Status: "done", Tags: []string{model.ActiveTag}}, "x "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := searchLines(t, []model.Task{tt.task}, ddmmyyyy)
			if len(lines) != 1 {
				t.Fatalf("expected 1 row, got %d", len(lines))
			}
			if !strings.HasPrefix(lines[0], tt.prefix) {
				t.Errorf("row should start with %q, got %q", tt.prefix, lines[0])
			}
			// The status cell is exactly 2 chars wide, so the ID must start at
			// offset 2 — no marker glyph may follow.
			if rest := lines[0][2:]; strings.HasPrefix(rest, "x") || strings.HasPrefix(rest, "*") {
				t.Errorf("status cell must be exactly 2 chars wide, got %q", lines[0])
			}
		})
	}
}

// TestFormatSearchResults_TagsFilterActive verifies the reserved active tag is
// filtered out of the tag cell (it is rendered as the status marker instead).
func TestFormatSearchResults_TagsFilterActive(t *testing.T) {
	tasks := []model.Task{
		{
			ID:       "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:    "Tagged task",
			Schedule: "today",
			Status:   "open",
			Tags:     []string{model.ActiveTag, "work", "urgent"},
		},
	}

	lines := searchLines(t, tasks, ddmmyyyy)
	if len(lines) != 1 {
		t.Fatalf("expected 1 row, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "[work, urgent]") {
		t.Errorf("row should contain visible tags '[work, urgent]', got %q", lines[0])
	}
	if strings.Contains(lines[0], model.ActiveTag) {
		t.Errorf("row should not contain the reserved %q tag in the tag cell, got %q", model.ActiveTag, lines[0])
	}
	// Active still shows as the status marker.
	if !strings.HasPrefix(lines[0], "* ") {
		t.Errorf("active task row should start with '* ', got %q", lines[0])
	}
}

// TestFormatSearchResults_IDColumn verifies the ID cell holds a
// searchIDWidth-character prefix — wider than ShortID's 8, because search
// output is meant to be pasted into `monolog note <id>` and an 8-character
// ULID prefix collides for tasks created in the same ~256 ms window — but
// still not the full ULID.
func TestFormatSearchResults_IDColumn(t *testing.T) {
	const id = "01ABCDEFGHIJKLMNOPQRSTUVWX"
	tasks := []model.Task{
		{ID: id, Title: "Buy milk", Schedule: "today", Status: "open"},
	}

	lines := searchLines(t, tasks, ddmmyyyy)
	if len(lines) != 1 {
		t.Fatalf("expected 1 row, got %d", len(lines))
	}
	if searchIDWidth <= 10 {
		t.Fatalf("searchIDWidth = %d; a ULID needs 10 characters just for the millisecond timestamp, so anything at or below that collides for same-millisecond tasks", searchIDWidth)
	}
	want := id[:searchIDWidth]
	if !strings.Contains(lines[0], want) {
		t.Errorf("row should contain the %d-char ID prefix %q, got %q", searchIDWidth, want, lines[0])
	}
	if strings.Contains(lines[0], id) {
		t.Errorf("row should not contain the full ULID, got %q", lines[0])
	}
}

// TestFormatSearchResults_EmptyTitleAndSchedule pins the degenerate cells: a
// task with no title still renders a row (with its columns still aligned
// against a neighbour), and an empty schedule renders as blank padding rather
// than tripping schedule.FormatDisplay.
func TestFormatSearchResults_EmptyTitleAndSchedule(t *testing.T) {
	tasks := []model.Task{
		{ID: "01AAAAAAAAAAAAAAAAAAAAAAAA", Title: "", Schedule: "", Status: "open"},
		{ID: "01BBBBBBBBBBBBBBBBBBBBBBBB", Title: "Has a title", Schedule: "today", Status: "open", Tags: []string{"work"}},
	}

	lines := searchLines(t, tasks, ddmmyyyy)
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[0], "  01AAAAAAAAAA") {
		t.Errorf("empty-title row should still start with the status cell and ID, got %q", lines[0])
	}
	// Column alignment: the tag column of row 1 must start where row 0's would.
	if idx := strings.Index(lines[1], "[work]"); idx < 0 {
		t.Errorf("row 1 should carry its tag cell, got %q", lines[1])
	}
	if got := len(strings.TrimRight(lines[0], " ")); got == 0 {
		t.Errorf("empty-title row should not collapse to whitespace, got %q", lines[0])
	}

	// A single empty-title, empty-schedule task on its own must not panic or
	// emit "No matches.".
	solo := searchLines(t, tasks[:1], ddmmyyyy)
	if len(solo) != 1 {
		t.Fatalf("expected 1 row for the solo empty task, got %d: %q", len(solo), solo)
	}
	if strings.Contains(solo[0], "No matches.") {
		t.Errorf("a real task with an empty title must not render as %q", "No matches.")
	}
}

// TestFormatSearchResults_ISOScheduleRendersInConfiguredLayout mirrors
// TestFormatTasks_ISOScheduleRendersInConfiguredLayout: stored ISO schedules
// must render through schedule.FormatDisplay in the configured layout, and the
// layout parameter must be wired through rather than hardcoded.
func TestFormatSearchResults_ISOScheduleRendersInConfiguredLayout(t *testing.T) {
	tasks := []model.Task{
		{ID: "01ABCDEFGHIJKLMNOPQRSTUVWX", Title: "Dated task", Schedule: "2030-04-15", Status: "open"},
	}

	var buf bytes.Buffer
	FormatSearchResults(&buf, tasks, ddmmyyyy)
	output := buf.String()
	if !strings.Contains(output, "15-04-2030") {
		t.Errorf("output should contain schedule in DD-MM-YYYY (15-04-2030), got:\n%s", output)
	}
	if strings.Contains(output, "2030-04-15") {
		t.Errorf("output should NOT contain stored ISO schedule 2030-04-15, got:\n%s", output)
	}

	// Under an alternative layout the schedule must render in that layout,
	// proving the parameter is wired through (not hardcoded).
	buf.Reset()
	FormatSearchResults(&buf, tasks, "01/02/2006")
	output = buf.String()
	if !strings.Contains(output, "04/15/2030") {
		t.Errorf("output should contain schedule in MM/DD/YYYY (04/15/2030), got:\n%s", output)
	}

	// Bucket names are not ISO dates, so FormatDisplay passes them through.
	buf.Reset()
	FormatSearchResults(&buf, []model.Task{{ID: "01X", Title: "Bucket", Schedule: "tomorrow", Status: "open"}}, ddmmyyyy)
	if !strings.Contains(buf.String(), "tomorrow") {
		t.Errorf("bucket schedule should pass through unchanged, got:\n%s", buf.String())
	}
}

// TestFormatSearchResults_MultibyteTitlePadding verifies padding is computed in
// runes, not bytes, so a title full of multibyte characters does not overshoot
// its column.
func TestFormatSearchResults_MultibyteTitlePadding(t *testing.T) {
	tasks := []model.Task{
		{ID: "01AAAAAAAAAAAAAAAAAAAAAAAA", Title: "Café → naïve", Schedule: "today", Status: "open"},
		{ID: "01BBBBBBBBBBBBBBBBBBBBBBBB", Title: "ASCII title!", Schedule: "today", Status: "open"},
	}
	// Both titles have the same rune count but different byte lengths.
	if utf8.RuneCountInString(tasks[0].Title) != utf8.RuneCountInString(tasks[1].Title) {
		t.Fatalf("fixture titles must have equal rune counts")
	}
	if len(tasks[0].Title) == len(tasks[1].Title) {
		t.Fatalf("fixture titles must differ in byte length")
	}

	lines := searchLines(t, tasks, ddmmyyyy)
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(lines))
	}
	c0 := utf8.RuneCountInString(lines[0][:strings.Index(lines[0], "today")])
	c1 := utf8.RuneCountInString(lines[1][:strings.Index(lines[1], "today")])
	if c0 != c1 {
		t.Errorf("schedule column at rune %d vs %d — padding is byte-based, not rune-based:\n%q\n%q", c0, c1, lines[0], lines[1])
	}
}
