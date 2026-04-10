package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
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
