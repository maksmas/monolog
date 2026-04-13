package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setTaskSchedule modifies a task's schedule on disk directly.
func setTaskSchedule(t *testing.T, dir, id, schedule string) {
	t.Helper()
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatalf("task %s not found", id)
	}
	task.Schedule = schedule
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	data = append(data, '\n')
	path := filepath.Join(dir, ".monolog", "tasks", id+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}
}

// --- Bump command tests ---

func TestBumpCommand_TomorrowBecomesToday(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTaskWithSchedule(t, dir, "Tomorrow task", "tomorrow")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after bump")
	}
	if task.Schedule != "today" {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, "today")
	}
}

func TestBumpCommand_PastISODateBecomesToday(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTaskWithSchedule(t, dir, "Past date task", "2020-01-15")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after bump")
	}
	if task.Schedule != "today" {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, "today")
	}
}

func TestBumpCommand_TodayStaysToday(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTaskWithSchedule(t, dir, "Today task", "today")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after bump")
	}
	if task.Schedule != "today" {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, "today")
	}
}

func TestBumpCommand_WeekStaysWeek(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTaskWithSchedule(t, dir, "Week task", "week")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after bump")
	}
	if task.Schedule != "week" {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, "week")
	}
}

func TestBumpCommand_SomedayStaysSomeday(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTaskWithSchedule(t, dir, "Someday task", "someday")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after bump")
	}
	if task.Schedule != "someday" {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, "someday")
	}
}

func TestBumpCommand_TodayISODateStays(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	todayDate := time.Now().UTC().Format("2006-01-02")
	id := addTestTaskWithSchedule(t, dir, "Today ISO task", todayDate)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after bump")
	}
	// Today's ISO date should NOT be promoted (it's not "past")
	if task.Schedule != todayDate {
		t.Errorf("Schedule: got %q, want %q (today's date should not be bumped)", task.Schedule, todayDate)
	}

	output := buf.String()
	if !strings.Contains(output, "nothing to bump") {
		t.Errorf("expected 'nothing to bump' for today's ISO date, got: %s", output)
	}
}

func TestBumpCommand_FutureISODateStays(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	futureDate := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	id := addTestTaskWithSchedule(t, dir, "Future task", futureDate)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after bump")
	}
	if task.Schedule != futureDate {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, futureDate)
	}
}

func TestBumpCommand_AutoCommitMultipleTasks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	_ = addTestTaskWithSchedule(t, dir, "Bump-A", "tomorrow")
	_ = addTestTaskWithSchedule(t, dir, "Bump-B", "tomorrow")
	_ = addTestTaskWithSchedule(t, dir, "Bump-C", "2020-01-01")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline", "-1")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !strings.Contains(string(out), "bump: promote 3 tasks to today") {
		t.Errorf("commit message should contain 'bump: promote 3 tasks to today', got: %s", string(out))
	}
}

func TestBumpCommand_NothingToBump(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add only tasks that don't need bumping
	_ = addTestTaskWithSchedule(t, dir, "Today task", "today")
	_ = addTestTaskWithSchedule(t, dir, "Someday task", "someday")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "nothing to bump") {
		t.Errorf("expected 'nothing to bump' output, got: %s", output)
	}
}

func TestBumpCommand_NothingToBump_NoExtraCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	_ = addTestTaskWithSchedule(t, dir, "Today task", "today")

	// Count commits before bump
	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline")
	outBefore, _ := gitCmd.Output()
	commitsBefore := len(strings.Split(strings.TrimSpace(string(outBefore)), "\n"))

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"bump"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("bump command error = %v", err)
	}

	// Count commits after bump
	gitCmd2 := exec.Command("git", "-C", dir, "log", "--oneline")
	outAfter, _ := gitCmd2.Output()
	commitsAfter := len(strings.Split(strings.TrimSpace(string(outAfter)), "\n"))

	if commitsAfter != commitsBefore {
		t.Errorf("expected no new commits when nothing to bump, before=%d after=%d", commitsBefore, commitsAfter)
	}
}

func TestBumpCommand_SkipsDoneTasks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add a task with schedule "tomorrow" and mark it done
	id := addTestTaskWithSchedule(t, dir, "Done tomorrow", "tomorrow")

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	// Now bump - should report nothing to bump
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"bump"})

	err := rootCmd2.Execute()
	if err != nil {
		t.Fatalf("bump command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "nothing to bump") {
		t.Errorf("expected 'nothing to bump' for done tasks, got: %s", output)
	}
}

// --- Log command tests ---

func TestLogCommand_ShowsRecentDoneTasks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Completed recently")

	// Mark it done
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	// Run log
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"log"})

	err := rootCmd2.Execute()
	if err != nil {
		t.Fatalf("log command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Completed recently") {
		t.Errorf("expected log to show completed task, got: %s", output)
	}
}

func TestLogCommand_ExcludesOldDoneTasks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Old completed")

	// Mark it done
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	// Backdate the updated_at to more than 7 days ago
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found")
	}
	task.UpdatedAt = time.Now().AddDate(0, 0, -10).UTC().Format(time.RFC3339)
	data, _ := json.MarshalIndent(task, "", "  ")
	data = append(data, '\n')
	os.WriteFile(filepath.Join(dir, ".monolog", "tasks", id+".json"), data, 0o644)

	// Run log
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"log"})

	err := rootCmd2.Execute()
	if err != nil {
		t.Fatalf("log command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if strings.Contains(output, "Old completed") {
		t.Errorf("log should not show tasks older than 7 days, got: %s", output)
	}
}

func TestLogCommand_EmptyLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add only open tasks (no done tasks)
	_ = addTestTask(t, dir, "Still open")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"log"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("log command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "No tasks.") {
		t.Errorf("expected empty log message, got: %s", output)
	}
}

func TestLogCommand_MultipleScheduleGroups(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create tasks in different schedule groups
	id1 := addTestTaskWithSchedule(t, dir, "Today done log", "today")
	id2 := addTestTaskWithSchedule(t, dir, "Tomorrow done log", "tomorrow")
	id3 := addTestTaskWithSchedule(t, dir, "Week done log", "week")

	// Mark all as done
	for _, id := range []string{id1, id2, id3} {
		rootCmd := NewRootCmd()
		rootCmd.SetOut(new(bytes.Buffer))
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs([]string{"done", id})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("done command error = %v", err)
		}
	}

	// Run log
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"log"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("log command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Today done log") {
		t.Errorf("log should show 'Today done log', got:\n%s", output)
	}
	if !strings.Contains(output, "Tomorrow done log") {
		t.Errorf("log should show 'Tomorrow done log', got:\n%s", output)
	}
	if !strings.Contains(output, "Week done log") {
		t.Errorf("log should show 'Week done log', got:\n%s", output)
	}
}

func TestLogCommand_SortsByUpdatedAtDescending(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create and complete two tasks
	id1 := addTestTask(t, dir, "First done")
	id2 := addTestTask(t, dir, "Second done")

	// Mark first done (use full ID to avoid ambiguity with rapid ULID generation)
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id1})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	// Mark second done
	rootCmd2 := NewRootCmd()
	rootCmd2.SetOut(new(bytes.Buffer))
	rootCmd2.SetErr(new(bytes.Buffer))
	rootCmd2.SetArgs([]string{"done", id2})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	// Backdate first task so second is more recent
	task1, _ := getTaskByID(t, dir, id1)
	task1.UpdatedAt = time.Now().AddDate(0, 0, -2).UTC().Format(time.RFC3339)
	data, _ := json.MarshalIndent(task1, "", "  ")
	data = append(data, '\n')
	os.WriteFile(filepath.Join(dir, ".monolog", "tasks", id1+".json"), data, 0o644)

	// Run log
	rootCmd3 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd3.SetOut(buf)
	rootCmd3.SetErr(buf)
	rootCmd3.SetArgs([]string{"log"})

	err := rootCmd3.Execute()
	if err != nil {
		t.Fatalf("log command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	// "Second done" should appear before "First done" since it was completed more recently
	pos1 := strings.Index(output, "Second done")
	pos2 := strings.Index(output, "First done")
	if pos1 == -1 || pos2 == -1 {
		t.Fatalf("expected both tasks in output, got: %s", output)
	}
	if pos1 > pos2 {
		t.Errorf("Second done should appear before First done (most recent first), got:\n%s", output)
	}
}
