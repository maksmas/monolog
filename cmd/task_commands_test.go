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

// addTestTask adds a task via the CLI and returns its ID.
func addTestTask(t *testing.T, dir, title string) string {
	t.Helper()
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"add", title})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add %q failed: %v", title, err)
	}
	tasks := readTasks(t, dir)
	for _, task := range tasks {
		if task.Title == title {
			return task.ID
		}
	}
	t.Fatalf("task %q not found after add", title)
	return ""
}

// getTaskByID reads a specific task from disk by its full ID.
func getTaskByID(t *testing.T, dir, id string) (model.Task, bool) {
	t.Helper()
	path := filepath.Join(dir, ".monolog", "tasks", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.Task{}, false
		}
		t.Fatalf("read task file: %v", err)
	}
	var task model.Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	return task, true
}

// --- Done command tests ---

func TestDoneCommand_SetsStatusDone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Finish report")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"done", id[:8]})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("done command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after done")
	}
	if task.Status != "done" {
		t.Errorf("Status: got %q, want %q", task.Status, "done")
	}
}

func TestDoneCommand_UpdatesUpdatedAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Check timestamps")

	// Backdate the task so we can detect the update
	task, _ := getTaskByID(t, dir, id)
	task.UpdatedAt = "2020-01-01T00:00:00Z"
	data, _ := json.MarshalIndent(task, "", "  ")
	data = append(data, '\n')
	os.WriteFile(filepath.Join(dir, ".monolog", "tasks", id+".json"), data, 0o644)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	taskAfter, _ := getTaskByID(t, dir, id)
	if taskAfter.UpdatedAt == "2020-01-01T00:00:00Z" {
		t.Error("updated_at should change after done")
	}
}

func TestDoneCommand_AutoCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Committed done")

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline", "-1")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !strings.Contains(string(out), "done: Committed done") {
		t.Errorf("commit message should contain 'done: Committed done', got: %s", string(out))
	}
}

func TestDoneCommand_ErrorOnBadPrefix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"done", "NONEXISTENT"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-existent prefix, got nil")
	}
}

func TestDoneCommand_ErrorNoArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided, got nil")
	}
}

// --- Rm command tests ---

func TestRmCommand_DeletesTaskFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Delete me")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"rm", id[:8]})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("rm command error = %v\noutput: %s", err, buf.String())
	}

	_, ok := getTaskByID(t, dir, id)
	if ok {
		t.Error("task file should be deleted after rm")
	}
}

func TestRmCommand_AutoCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Committed rm")

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"rm", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rm command error = %v", err)
	}

	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline", "-1")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !strings.Contains(string(out), "rm: Committed rm") {
		t.Errorf("commit message should contain 'rm: Committed rm', got: %s", string(out))
	}
}

func TestRmCommand_CleanWorkingTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Clean after rm")

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"rm", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rm command error = %v", err)
	}

	gitCmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}

	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected clean working tree after rm, got: %s", string(out))
	}
}

func TestRmCommand_ErrorOnBadPrefix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"rm", "NONEXISTENT"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-existent prefix, got nil")
	}
}

func TestRmCommand_ErrorNoArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"rm"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided, got nil")
	}
}

// --- Edit command tests ---

func TestEditCommand_ChangeTitle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Old title")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--title", "New title"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("edit command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if task.Title != "New title" {
		t.Errorf("Title: got %q, want %q", task.Title, "New title")
	}
}

func TestEditCommand_ChangeSchedule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Reschedule me")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--schedule", "tomorrow"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("edit command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if task.Schedule != "tomorrow" {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, "tomorrow")
	}
}

func TestEditCommand_ChangeTags(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Retag me")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--tags", "new,tags"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("edit command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if len(task.Tags) != 2 || task.Tags[0] != "new" || task.Tags[1] != "tags" {
		t.Errorf("Tags: got %v, want [new tags]", task.Tags)
	}
}

func TestEditCommand_MultipleFlags(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Edit everything")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--title", "Updated title", "--schedule", "someday", "--tags", "a,b"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("edit command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if task.Title != "Updated title" {
		t.Errorf("Title: got %q, want %q", task.Title, "Updated title")
	}
	if task.Schedule != "someday" {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, "someday")
	}
	if len(task.Tags) != 2 || task.Tags[0] != "a" || task.Tags[1] != "b" {
		t.Errorf("Tags: got %v, want [a b]", task.Tags)
	}
}

func TestEditCommand_UpdatesUpdatedAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Timestamp check")

	// Backdate the task so we can detect the update
	task, _ := getTaskByID(t, dir, id)
	task.UpdatedAt = "2020-01-01T00:00:00Z"
	data, _ := json.MarshalIndent(task, "", "  ")
	data = append(data, '\n')
	os.WriteFile(filepath.Join(dir, ".monolog", "tasks", id+".json"), data, 0o644)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--title", "Updated"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit command error = %v", err)
	}

	taskAfter, _ := getTaskByID(t, dir, id)
	if taskAfter.UpdatedAt == "2020-01-01T00:00:00Z" {
		t.Error("updated_at should change after edit")
	}
}

func TestEditCommand_AutoCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Committed edit")

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--title", "New name"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit command error = %v", err)
	}

	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline", "-1")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !strings.Contains(string(out), "edit: New name") {
		t.Errorf("commit message should contain 'edit: New name', got: %s", string(out))
	}
}

func TestEditCommand_ErrorOnBadPrefix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", "NONEXISTENT", "--title", "nope"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-existent prefix, got nil")
	}
}

func TestEditCommand_ErrorNoArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided, got nil")
	}
}

func TestEditCommand_NoFlagsNoChange(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "No change")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8]})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no edit flags provided, got nil")
	}
}
