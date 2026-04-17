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

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
)

// expectSchedule returns the ISO date a bucket name resolves to right now.
// Used by tests that previously asserted on the bucket-name string.
func expectSchedule(t *testing.T, bucket string) string {
	t.Helper()
	got, err := schedule.Parse(bucket, time.Now())
	if err != nil {
		t.Fatalf("schedule.Parse(%q): %v", bucket, err)
	}
	return got
}

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
	if want := expectSchedule(t, "today"); task.Schedule != want {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, want)
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
	if want := expectSchedule(t, "tomorrow"); tasks[0].Schedule != want {
		t.Errorf("Schedule: got %q, want %q", tasks[0].Schedule, want)
	}
}

func TestAddCommand_ScheduleValues(t *testing.T) {
	cases := []struct {
		input string
		want  string // "" means resolve via expectSchedule(input)
	}{
		{input: "today"},
		{input: "tomorrow"},
		{input: "week"},
		{input: "month"},
		{input: "someday"},
		{input: "2026-04-15", want: "2026-04-15"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "monolog")
			initTestRepo(t, dir)

			rootCmd := NewRootCmd()
			buf := new(bytes.Buffer)
			rootCmd.SetOut(buf)
			rootCmd.SetErr(buf)
			rootCmd.SetArgs([]string{"add", "Task", "-s", tc.input})

			err := rootCmd.Execute()
			if err != nil {
				t.Fatalf("add -s %s error = %v\noutput: %s", tc.input, err, buf.String())
			}

			tasks := readTasks(t, dir)
			if len(tasks) != 1 {
				t.Fatalf("expected 1 task, got %d", len(tasks))
			}
			want := tc.want
			if want == "" {
				want = expectSchedule(t, tc.input)
			}
			if tasks[0].Schedule != want {
				t.Errorf("Schedule: got %q, want %q", tasks[0].Schedule, want)
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
	if want := expectSchedule(t, "week"); task.Schedule != want {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, want)
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

func TestAddCommand_AutoTagFromTitlePrefix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create an existing task with the tag "jean"
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"add", "some task", "-t", "jean"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("setup add error = %v", err)
	}

	// Now add a task with title prefix "jean: do thing" — should auto-tag "jean"
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"add", "jean: do thing"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	var newTask model.Task
	for _, task := range tasks {
		if task.Title == "jean: do thing" {
			newTask = task
			break
		}
	}
	if newTask.ID == "" {
		t.Fatal("could not find the new task")
	}
	if len(newTask.Tags) != 1 || newTask.Tags[0] != "jean" {
		t.Errorf("Tags: got %v, want [jean]", newTask.Tags)
	}
}

func TestAddCommand_AutoTagNotAppliedForUnknownTag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add a task with title prefix "jean: do thing" but no existing task has tag "jean"
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "jean: do thing"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if len(tasks[0].Tags) != 0 {
		t.Errorf("Tags: got %v, want nil/empty (no auto-tag for unknown prefix)", tasks[0].Tags)
	}
}

func TestAddCommand_AutoTagNoDuplicate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create an existing task with the tag "jean"
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"add", "some task", "-t", "jean"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("setup add error = %v", err)
	}

	// Add task with title prefix "jean: do thing" AND explicit --tags jean
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"add", "jean: do thing", "-t", "jean"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	var newTask model.Task
	for _, task := range tasks {
		if task.Title == "jean: do thing" {
			newTask = task
			break
		}
	}
	if newTask.ID == "" {
		t.Fatal("could not find the new task")
	}
	// Should have exactly one "jean" tag, not two
	if len(newTask.Tags) != 1 || newTask.Tags[0] != "jean" {
		t.Errorf("Tags: got %v, want [jean] (no duplicate)", newTask.Tags)
	}
}

func TestAddCommand_RecurrenceValidGrammar(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // canonical form stored on disk
	}{
		{name: "monthly_first", input: "monthly:1", want: "monthly:1"},
		{name: "monthly_mid", input: "monthly:15", want: "monthly:15"},
		{name: "monthly_last", input: "monthly:31", want: "monthly:31"},
		{name: "weekly_short", input: "weekly:mon", want: "weekly:mon"},
		{name: "weekly_full", input: "weekly:Monday", want: "weekly:mon"},
		{name: "weekly_numeric", input: "weekly:1", want: "weekly:mon"},
		{name: "weekly_numeric_sun", input: "weekly:7", want: "weekly:sun"},
		{name: "weekly_upper", input: "weekly:FRIDAY", want: "weekly:fri"},
		{name: "workdays_lower", input: "workdays", want: "workdays"},
		{name: "workdays_mixed", input: "WorkDays", want: "workdays"},
		{name: "days_one", input: "days:1", want: "days:1"},
		{name: "days_seven", input: "days:7", want: "days:7"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "monolog")
			initTestRepo(t, dir)

			rootCmd := NewRootCmd()
			buf := new(bytes.Buffer)
			rootCmd.SetOut(buf)
			rootCmd.SetErr(buf)
			rootCmd.SetArgs([]string{"add", "task", "--recur", tc.input})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("add --recur %s error = %v\noutput: %s", tc.input, err, buf.String())
			}

			tasks := readTasks(t, dir)
			if len(tasks) != 1 {
				t.Fatalf("expected 1 task, got %d", len(tasks))
			}
			if tasks[0].Recurrence != tc.want {
				t.Errorf("Recurrence: got %q, want %q", tasks[0].Recurrence, tc.want)
			}
		})
	}
}

func TestAddCommand_RecurrenceInvalidGrammar(t *testing.T) {
	cases := []string{
		"bogus",
		"monthly:0",
		"monthly:32",
		"monthly:-1",
		"monthly:abc",
		"weekly:xyz",
		"weekly:0",
		"weekly:8",
		"weekly:",
		"days:0",
		"days:-1",
		"days:abc",
		"unknown:rule",
		"monthly",
		"weekly",
	}

	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "monolog")
			initTestRepo(t, dir)

			rootCmd := NewRootCmd()
			buf := new(bytes.Buffer)
			rootCmd.SetOut(buf)
			rootCmd.SetErr(buf)
			rootCmd.SetArgs([]string{"add", "task", "--recur", input})

			err := rootCmd.Execute()
			if err == nil {
				t.Fatalf("expected error for invalid recurrence %q, got nil", input)
			}

			// No task file should have been created since validation runs
			// before the store is opened or the task is written.
			tasksDir := filepath.Join(dir, ".monolog", "tasks")
			entries, rerr := os.ReadDir(tasksDir)
			if rerr != nil {
				t.Fatalf("read tasks dir: %v", rerr)
			}
			jsonCount := 0
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
					jsonCount++
				}
			}
			if jsonCount != 0 {
				t.Errorf("expected 0 task files after invalid --recur, got %d", jsonCount)
			}
		})
	}
}

func TestAddCommand_EmptyRecurrenceOmitted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "plain task"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Recurrence != "" {
		t.Errorf("Recurrence: got %q, want empty", tasks[0].Recurrence)
	}

	// Verify omitempty is respected — raw JSON should not contain "recurrence" key.
	tasksDir := filepath.Join(dir, ".monolog", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		t.Fatalf("read tasks dir: %v", err)
	}
	var raw []byte
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			raw, err = os.ReadFile(filepath.Join(tasksDir, e.Name()))
			if err != nil {
				t.Fatalf("read task file: %v", err)
			}
			break
		}
	}
	if strings.Contains(string(raw), "recurrence") {
		t.Errorf("JSON should not contain 'recurrence' key for empty recurrence, got: %s", string(raw))
	}
}

func TestAddCommand_ExplicitEmptyRecurrence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"add", "task", "--recur", ""})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add command error = %v\noutput: %s", err, buf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Recurrence != "" {
		t.Errorf("Recurrence: got %q, want empty", tasks[0].Recurrence)
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
