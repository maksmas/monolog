package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
)

func addTask(t *testing.T, args ...string) {
	t.Helper()
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(append([]string{"add"}, args...))
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add failed: %v", err)
	}
}

func TestLsCommand_DefaultShowsTodayOpen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add today tasks and a tomorrow task
	addTask(t, "Today task 1")
	addTask(t, "Today task 2")
	addTask(t, "Tomorrow task", "-s", "tomorrow")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Today task 1") {
		t.Errorf("output should contain 'Today task 1', got:\n%s", output)
	}
	if !strings.Contains(output, "Today task 2") {
		t.Errorf("output should contain 'Today task 2', got:\n%s", output)
	}
	if strings.Contains(output, "Tomorrow task") {
		t.Errorf("output should NOT contain 'Tomorrow task' (default shows today only), got:\n%s", output)
	}
}

func TestLsCommand_AllFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Today task")
	addTask(t, "Tomorrow task", "-s", "tomorrow")
	addTask(t, "Someday task", "-s", "someday")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--all"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --all error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Today task") {
		t.Errorf("--all should show 'Today task', got:\n%s", output)
	}
	if !strings.Contains(output, "Tomorrow task") {
		t.Errorf("--all should show 'Tomorrow task', got:\n%s", output)
	}
	if !strings.Contains(output, "Someday task") {
		t.Errorf("--all should show 'Someday task', got:\n%s", output)
	}
}

func TestLsCommand_ScheduleFilter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Today task")
	addTask(t, "Tomorrow task", "-s", "tomorrow")
	addTask(t, "Week task", "-s", "week")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--schedule", "tomorrow"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --schedule tomorrow error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Tomorrow task") {
		t.Errorf("--schedule tomorrow should show 'Tomorrow task', got:\n%s", output)
	}
	if strings.Contains(output, "Today task") {
		t.Errorf("--schedule tomorrow should NOT show 'Today task', got:\n%s", output)
	}
	if strings.Contains(output, "Week task") {
		t.Errorf("--schedule tomorrow should NOT show 'Week task', got:\n%s", output)
	}
}

func TestLsCommand_TagFilter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Work task", "-t", "work")
	addTask(t, "Personal task", "-t", "personal")
	addTask(t, "Work urgent", "-t", "work,urgent")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--all", "--tag", "work"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --tag work error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Work task") {
		t.Errorf("--tag work should show 'Work task', got:\n%s", output)
	}
	if !strings.Contains(output, "Work urgent") {
		t.Errorf("--tag work should show 'Work urgent', got:\n%s", output)
	}
	if strings.Contains(output, "Personal task") {
		t.Errorf("--tag work should NOT show 'Personal task', got:\n%s", output)
	}
}

func TestLsCommand_DoneFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Open task")
	addTask(t, "Another open task")

	// We can't easily mark a task as done via CLI yet, so we'll just check
	// that --done shows no tasks when all are open
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--done"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --done error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "No tasks.") {
		t.Errorf("--done should show 'No tasks.' when all tasks are open, got:\n%s", output)
	}
}

func TestLsCommand_EmptyList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "No tasks.") {
		t.Errorf("empty list should show 'No tasks.', got:\n%s", output)
	}
}

func TestLsCommand_SortedByPosition(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add tasks in order — they should display in order
	addTask(t, "First")
	addTask(t, "Second")
	addTask(t, "Third")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	firstIdx := strings.Index(output, "First")
	secondIdx := strings.Index(output, "Second")
	thirdIdx := strings.Index(output, "Third")

	if firstIdx == -1 || secondIdx == -1 || thirdIdx == -1 {
		t.Fatalf("output should contain all tasks, got:\n%s", output)
	}
	if firstIdx >= secondIdx {
		t.Errorf("First should appear before Second in output:\n%s", output)
	}
	if secondIdx >= thirdIdx {
		t.Errorf("Second should appear before Third in output:\n%s", output)
	}
}

func TestLsCommand_DoneShowsAllSchedules(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add tasks with different schedules
	id1 := addTestTask(t, dir, "Today done")
	id2 := addTestTaskWithSchedule(t, dir, "Tomorrow done", "tomorrow")

	// Mark both as done
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id1})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	rootCmd2 := NewRootCmd()
	rootCmd2.SetOut(new(bytes.Buffer))
	rootCmd2.SetErr(new(bytes.Buffer))
	rootCmd2.SetArgs([]string{"done", id2})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	// ls --done should show both (not filtered to today)
	rootCmd3 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd3.SetOut(buf)
	rootCmd3.SetErr(buf)
	rootCmd3.SetArgs([]string{"ls", "--done"})

	if err := rootCmd3.Execute(); err != nil {
		t.Fatalf("ls --done error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Today done") {
		t.Errorf("--done should show 'Today done', got:\n%s", output)
	}
	if !strings.Contains(output, "Tomorrow done") {
		t.Errorf("--done should show 'Tomorrow done', got:\n%s", output)
	}
}

// TestLsCommand_ScheduleFilter_AcceptsConfiguredFormat verifies that the
// --schedule flag accepts the user-facing configured layout (DD-MM-YYYY by
// default) as an exact-date filter, matching the advertised help text and
// error message. Regression guard: the prior implementation gated input via
// IsISODate, which rejected DD-MM-YYYY despite the error saying it was
// allowed.
func TestLsCommand_ScheduleFilter_AcceptsConfiguredFormat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add one task scheduled at a far-future concrete date so it can be
	// filtered exactly, and one for "today" that must be filtered out.
	addTask(t, "Today task")
	addTask(t, "Future task", "-s", "15-04-2030")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--schedule", "15-04-2030"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --schedule DD-MM-YYYY error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Future task") {
		t.Errorf("--schedule 15-04-2030 should show 'Future task', got:\n%s", output)
	}
	if strings.Contains(output, "Today task") {
		t.Errorf("--schedule 15-04-2030 should NOT show 'Today task', got:\n%s", output)
	}
}

// TestLsCommand_ScheduleFilter_AcceptsLegacyISO verifies that legacy ISO
// input to --schedule still works silently alongside the DD-MM-YYYY path.
func TestLsCommand_ScheduleFilter_AcceptsLegacyISO(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Today task")
	addTask(t, "Future task", "-s", "2030-04-15")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--schedule", "2030-04-15"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --schedule ISO error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Future task") {
		t.Errorf("--schedule 2030-04-15 (legacy ISO) should show 'Future task', got:\n%s", output)
	}
}

func TestLsCommand_InvalidSchedule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--schedule", "garbage"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid schedule in ls, got nil")
	}
	if !strings.Contains(err.Error(), "invalid schedule") {
		t.Errorf("error should mention 'invalid schedule', got: %v", err)
	}
}

func TestLsCommand_ScheduleAndTagCombined(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Today work", "-t", "work")
	addTask(t, "Today personal", "-t", "personal")
	addTask(t, "Tomorrow work", "-s", "tomorrow", "-t", "work")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--schedule", "today", "--tag", "work"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Today work") {
		t.Errorf("should show 'Today work', got:\n%s", output)
	}
	if strings.Contains(output, "Today personal") {
		t.Errorf("should NOT show 'Today personal', got:\n%s", output)
	}
	if strings.Contains(output, "Tomorrow work") {
		t.Errorf("should NOT show 'Tomorrow work', got:\n%s", output)
	}
}

// setTaskActive marks a task on disk as active by adding the ActiveTag to its tags.
func setTaskActive(t *testing.T, dir, id string) {
	t.Helper()
	path := filepath.Join(dir, ".monolog", "tasks", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task %s: %v", id, err)
	}
	var task model.Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	task.SetActive(true)
	data, err = json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}
}

func TestLs_ActiveFlag_LiftsScheduleFilter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create three tasks: two today (one active, one not), one week (active).
	addTestTask(t, dir, "Today not active")
	weekID := addTestTaskWithSchedule(t, dir, "Week active", "week")
	addTestTask(t, dir, "Today also not active")

	// Mark the week task as active on disk.
	setTaskActive(t, dir, weekID)

	// Verify schedule.Parse("week") gives us the right ISO date
	weekISO, err := schedule.Parse("week", time.Now(), config.DateFormat())
	if err != nil {
		t.Fatalf("schedule.Parse(week): %v", err)
	}
	task, ok := getTaskByID(t, dir, weekID)
	if !ok {
		t.Fatal("week task not found on disk")
	}
	if task.Schedule != weekISO {
		t.Fatalf("week task schedule = %q, want %q", task.Schedule, weekISO)
	}

	// Run: ls --active (no --schedule)
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--active"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --active error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	// The active week task must appear (schedule filter was lifted).
	if !strings.Contains(output, "Week active") {
		t.Errorf("--active should show 'Week active' (schedule filter lifted), got:\n%s", output)
	}
	// The non-active today tasks must NOT appear.
	if strings.Contains(output, "Today not active") {
		t.Errorf("--active should NOT show 'Today not active', got:\n%s", output)
	}
	if strings.Contains(output, "Today also not active") {
		t.Errorf("--active should NOT show 'Today also not active', got:\n%s", output)
	}
}

func TestLs_ActiveFlag_RespectsExplicitSchedule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create two active tasks in different schedules.
	todayID := addTestTask(t, dir, "Today active")
	weekID := addTestTaskWithSchedule(t, dir, "Week active", "week")

	setTaskActive(t, dir, todayID)
	setTaskActive(t, dir, weekID)

	// Run: ls --active --schedule today
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--active", "--schedule", "today"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --active --schedule today error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	// Only the today-active task should appear.
	if !strings.Contains(output, "Today active") {
		t.Errorf("should show 'Today active', got:\n%s", output)
	}
	if strings.Contains(output, "Week active") {
		t.Errorf("should NOT show 'Week active' when --schedule today is explicit, got:\n%s", output)
	}
}

func TestLs_ActiveWithDoneFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create two tasks, mark both as done, then re-activate one on disk.
	// (The done command auto-deactivates, so we re-add the active tag after.)
	id1 := addTestTask(t, dir, "Done active")
	id2 := addTestTask(t, dir, "Done not active")

	// Mark both as done using full IDs to avoid prefix collisions.
	for _, id := range []string{id1, id2} {
		rc := NewRootCmd()
		rc.SetOut(new(bytes.Buffer))
		rc.SetErr(new(bytes.Buffer))
		rc.SetArgs([]string{"done", id})
		if err := rc.Execute(); err != nil {
			t.Fatalf("done %s: %v", id, err)
		}
	}

	// Re-activate the first done task on disk to simulate a done+active state.
	setTaskActive(t, dir, id1)

	// Run: ls --active --done
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--active", "--done"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --active --done error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Done active") {
		t.Errorf("--active --done should show 'Done active', got:\n%s", output)
	}
	if strings.Contains(output, "Done not active") {
		t.Errorf("--active --done should NOT show 'Done not active', got:\n%s", output)
	}
}

// TestLsCommand_FullFlag_ShowsMetadata verifies that ls --full --all shows
// indented metadata keys (Status, Schedule, Tags) and separator lines.
func TestLsCommand_FullFlag_ShowsMetadata(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Work task", "-t", "work,backend", "-s", "today")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--full", "--all"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --full --all error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Work task") {
		t.Errorf("output should contain task title, got:\n%s", output)
	}
	if !strings.Contains(output, "Status:") {
		t.Errorf("output should contain 'Status:' metadata label, got:\n%s", output)
	}
	if !strings.Contains(output, "Schedule:") {
		t.Errorf("output should contain 'Schedule:' metadata label, got:\n%s", output)
	}
	if !strings.Contains(output, "Tags:") {
		t.Errorf("output should contain 'Tags:' metadata label, got:\n%s", output)
	}
	if !strings.Contains(output, "work") {
		t.Errorf("output should contain tag 'work', got:\n%s", output)
	}
	// Separator line contains these specific Unicode dashes
	if !strings.Contains(output, "────────────────────────────────────────────────────────────") {
		t.Errorf("output should contain separator line, got:\n%s", output)
	}
}

// TestLsCommand_FullFlag_Empty verifies that ls --full on empty store → "No tasks.\n"
func TestLsCommand_FullFlag_Empty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"ls", "--full"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls --full error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "No tasks.") {
		t.Errorf("ls --full on empty store should show 'No tasks.', got:\n%s", output)
	}
}

// TestLsCommand_FullFlag_WithBody verifies that a task with body text is shown
// indented in --full mode but body content does not appear in compact mode.
func TestLsCommand_FullFlag_WithBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add a task, then update it on disk to include a body.
	id := addTestTask(t, dir, "Task with body")
	path := filepath.Join(dir, ".monolog", "tasks", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task file: %v", err)
	}
	var task model.Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	task.Body = "This is the body content."
	data, err = json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}

	// Full mode: body should appear.
	rootCmdFull := NewRootCmd()
	bufFull := new(bytes.Buffer)
	rootCmdFull.SetOut(bufFull)
	rootCmdFull.SetErr(bufFull)
	rootCmdFull.SetArgs([]string{"ls", "--full"})

	if err := rootCmdFull.Execute(); err != nil {
		t.Fatalf("ls --full error = %v\noutput: %s", err, bufFull.String())
	}
	fullOutput := bufFull.String()
	if !strings.Contains(fullOutput, "This is the body content.") {
		t.Errorf("ls --full should show body content, got:\n%s", fullOutput)
	}

	// Compact mode: body should NOT appear.
	rootCmdCompact := NewRootCmd()
	bufCompact := new(bytes.Buffer)
	rootCmdCompact.SetOut(bufCompact)
	rootCmdCompact.SetErr(bufCompact)
	rootCmdCompact.SetArgs([]string{"ls"})

	if err := rootCmdCompact.Execute(); err != nil {
		t.Fatalf("ls (compact) error = %v\noutput: %s", err, bufCompact.String())
	}
	compactOutput := bufCompact.String()
	if strings.Contains(compactOutput, "This is the body content.") {
		t.Errorf("ls (compact) should NOT show body content, got:\n%s", compactOutput)
	}
}

func TestFilterActive(t *testing.T) {
	tests := []struct {
		name string
		in   []model.Task
		want int
	}{
		{"nil slice", nil, 0},
		{"empty slice", []model.Task{}, 0},
		{"no active tasks", []model.Task{
			{ID: "01A", Title: "a"},
			{ID: "01B", Title: "b"},
		}, 0},
		{"all active", []model.Task{
			{ID: "01A", Title: "a", Tags: []string{"active"}},
			{ID: "01B", Title: "b", Tags: []string{"active"}},
		}, 2},
		{"mixed", []model.Task{
			{ID: "01A", Title: "a", Tags: []string{"active"}},
			{ID: "01B", Title: "b"},
			{ID: "01C", Title: "c", Tags: []string{"active", "work"}},
		}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Make a copy to avoid mutation issues from the [:0] trick.
			input := make([]model.Task, len(tc.in))
			copy(input, tc.in)
			got := filterActive(input)
			if len(got) != tc.want {
				t.Errorf("filterActive() returned %d tasks, want %d", len(got), tc.want)
			}
			for _, task := range got {
				if !task.IsActive() {
					t.Errorf("filterActive() returned non-active task: %v", task)
				}
			}
		})
	}
}
