package display

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mmaksmas/monolog/internal/model"
)

func TestFormatTasks_Empty(t *testing.T) {
	var buf bytes.Buffer
	FormatTasks(&buf, nil)
	output := buf.String()
	if output != "No tasks.\n" {
		t.Errorf("expected 'No tasks.\\n', got %q", output)
	}
}

func TestFormatTasks_SingleTask(t *testing.T) {
	tasks := []model.Task{
		{
			ID:       "01ABCDEFGHIJKLMNOPQRSTUVWX",
			Title:    "Buy milk",
			Schedule: "today",
			Status:   "open",
			Position: 1000,
		},
	}

	var buf bytes.Buffer
	FormatTasks(&buf, tasks)
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
	// Should contain position indicator
	if !strings.Contains(output, "1") {
		t.Errorf("output should contain position indicator, got:\n%s", output)
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
	FormatTasks(&buf, tasks)
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
	FormatTasks(&buf, tasks)
	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Should have 3 task lines (no header)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d:\n%s", len(lines), output)
	}

	// Position indicators should be sequential
	if !strings.Contains(lines[0], "1") {
		t.Errorf("first line should have position 1, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "2") {
		t.Errorf("second line should have position 2, got: %s", lines[1])
	}
	if !strings.Contains(lines[2], "3") {
		t.Errorf("third line should have position 3, got: %s", lines[2])
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
	FormatTasks(&buf, tasks)
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
	FormatTasks(&buf, tasks)
	output := buf.String()

	// Done tasks should show a done indicator
	if !strings.Contains(output, "x") && !strings.Contains(output, "X") && !strings.Contains(output, "done") {
		t.Errorf("done task should have a done indicator, got:\n%s", output)
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
