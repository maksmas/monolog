package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmaksmas/monolog/internal/model"
)

// initTestRepo initializes a monolog repo at dir for testing.
func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("MONOLOG_DIR", dir)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"init"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}
}

// readTasks reads all task JSON files from the tasks directory.
func readTasks(t *testing.T, dir string) []model.Task {
	t.Helper()
	tasksDir := filepath.Join(dir, ".monolog", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		t.Fatalf("read tasks dir: %v", err)
	}

	var tasks []model.Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(tasksDir, e.Name()))
		if err != nil {
			t.Fatalf("read task file: %v", err)
		}
		var task model.Task
		if err := json.Unmarshal(data, &task); err != nil {
			t.Fatalf("unmarshal task: %v", err)
		}
		tasks = append(tasks, task)
	}
	return tasks
}

func TestAddCommand_DefaultSchedule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "Buy milk"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.Title != "Buy milk" {
		t.Errorf("Title: got %q, want %q", task.Title, "Buy milk")
	}
	if task.Schedule != "today" {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, "today")
	}
	if task.Status != "open" {
		t.Errorf("Status: got %q, want %q", task.Status, "open")
	}
	if task.Source != "manual" {
		t.Errorf("Source: got %q, want %q", task.Source, "manual")
	}
	if task.ID == "" {
		t.Error("ID should not be empty")
	}
	if task.Position != 1000 {
		t.Errorf("Position: got %f, want 1000", task.Position)
	}
	if task.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
	if task.UpdatedAt == "" {
		t.Error("UpdatedAt should not be empty")
	}
}

func TestAddCommand_CustomSchedule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "Plan next sprint", "-s", "tomorrow"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Schedule != "tomorrow" {
		t.Errorf("Schedule: got %q, want %q", tasks[0].Schedule, "tomorrow")
	}
}

func TestAddCommand_ScheduleValues(t *testing.T) {
	schedules := []string{"today", "tomorrow", "week", "someday", "2026-04-15"}
	for _, sched := range schedules {
		t.Run(sched, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "monolog")
			initTestRepo(t, dir)

			rootCmd := NewRootCmd()
			buf := new(bytes.Buffer)
			rootCmd.SetOut(buf)
			rootCmd.SetErr(buf)
			rootCmd.SetArgs([]string{"add", "Task", "-s", sched})

			err := rootCmd.Execute()
			if err != nil {
				t.Fatalf("add -s %s error = %v\noutput: %s", sched, err, buf.String())
			}

			tasks := readTasks(t, dir)
			if len(tasks) != 1 {
				t.Fatalf("expected 1 task, got %d", len(tasks))
			}
			if tasks[0].Schedule != sched {
				t.Errorf("Schedule: got %q, want %q", tasks[0].Schedule, sched)
			}
		})
	}
}

func TestAddCommand_WithTags(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "Review PR", "-t", "work,urgent"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if len(tasks[0].Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d: %v", len(tasks[0].Tags), tasks[0].Tags)
	}
	if tasks[0].Tags[0] != "work" || tasks[0].Tags[1] != "urgent" {
		t.Errorf("Tags: got %v, want [work urgent]", tasks[0].Tags)
	}
}

func TestAddCommand_WithScheduleAndTags(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "Deploy v2", "-s", "week", "-t", "devops,prod"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Schedule != "week" {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, "week")
	}
	if len(task.Tags) != 2 || task.Tags[0] != "devops" || task.Tags[1] != "prod" {
		t.Errorf("Tags: got %v, want [devops prod]", task.Tags)
	}
}

func TestAddCommand_NoTitle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no title provided, got nil")
	}
}

func TestAddCommand_PositionIncrementsWithMultipleTasks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add first task
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"add", "First task"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first add error = %v", err)
	}

	// Add second task
	rootCmd2 := NewRootCmd()
	rootCmd2.SetOut(new(bytes.Buffer))
	rootCmd2.SetErr(new(bytes.Buffer))
	rootCmd2.SetArgs([]string{"add", "Second task"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("second add error = %v", err)
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	// Find which is which
	var first, second model.Task
	for _, task := range tasks {
		if task.Title == "First task" {
			first = task
		} else {
			second = task
		}
	}

	if second.Position <= first.Position {
		t.Errorf("second task position (%f) should be greater than first (%f)",
			second.Position, first.Position)
	}
}

func TestAddCommand_OutputMessage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "Test output"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add error = %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Error("expected output message, got empty")
	}
	if !strings.Contains(output, "Test output") {
		t.Errorf("output should contain task title, got: %s", output)
	}
}

// --- Auto-commit tests ---

func TestAddCommand_AutoCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "Committed task"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	// Verify a git commit was made with the expected message
	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline", "-1")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	commitMsg := string(out)
	if !strings.Contains(commitMsg, "add: Committed task") {
		t.Errorf("commit message should contain 'add: Committed task', got: %s", commitMsg)
	}
}

func TestAddCommand_AutoCommitIncludesTaskFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "File check"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	// Verify the task file is tracked in git
	gitCmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}

	// After auto-commit, working tree should be clean
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected clean working tree after auto-commit, got: %s", string(out))
	}
}

func TestAddCommand_InvalidSchedule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	invalid := []string{"garbage", "next-week", "2026-13-01", "yesterday", ""}
	for _, sched := range invalid {
		t.Run(sched, func(t *testing.T) {
			rootCmd := NewRootCmd()
			buf := new(bytes.Buffer)
			rootCmd.SetOut(buf)
			rootCmd.SetErr(buf)
			rootCmd.SetArgs([]string{"add", "Task", "-s", sched})

			err := rootCmd.Execute()
			if err == nil {
				t.Errorf("expected error for invalid schedule %q, got nil", sched)
			}
		})
	}
}

func TestAddCommand_TagSanitization(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "Tag test", "-t", ",,work,,urgent,,"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	if len(tasks[0].Tags) != 2 {
		t.Fatalf("expected 2 tags after sanitization, got %d: %v", len(tasks[0].Tags), tasks[0].Tags)
	}
	if tasks[0].Tags[0] != "work" || tasks[0].Tags[1] != "urgent" {
		t.Errorf("Tags: got %v, want [work urgent]", tasks[0].Tags)
	}
}

func TestAddCommand_EmptyTagsProducesNil(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "No tags", "-t", ",,"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	if len(tasks[0].Tags) != 0 {
		t.Errorf("expected no tags for all-empty input, got: %v", tasks[0].Tags)
	}
}

func TestAddCommand_MultipleAddsCreateSeparateCommits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add first task
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"add", "Task one"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first add error = %v", err)
	}

	// Add second task
	rootCmd2 := NewRootCmd()
	rootCmd2.SetOut(new(bytes.Buffer))
	rootCmd2.SetErr(new(bytes.Buffer))
	rootCmd2.SetArgs([]string{"add", "Task two"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("second add error = %v", err)
	}

	// Verify two separate "add:" commits exist (plus the init commit)
	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	addCount := 0
	for _, line := range lines {
		if strings.Contains(line, "add:") {
			addCount++
		}
	}
	if addCount != 2 {
		t.Errorf("expected 2 'add:' commits, got %d in:\n%s", addCount, string(out))
	}
}
