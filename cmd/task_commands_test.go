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

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/schedule"
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

func TestDoneCommand_DoubleDone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Double done test")

	// First done
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first done command error = %v", err)
	}

	// Count commits after first done
	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline")
	outBefore, _ := gitCmd.Output()
	commitsBefore := len(strings.Split(strings.TrimSpace(string(outBefore)), "\n"))

	// Second done — should be a no-op (no extra commit)
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"done", id[:8]})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("second done command error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Already done") {
		t.Errorf("expected 'Already done' message, got: %s", output)
	}

	// Verify no new commit was made
	gitCmd2 := exec.Command("git", "-C", dir, "log", "--oneline")
	outAfter, _ := gitCmd2.Output()
	commitsAfter := len(strings.Split(strings.TrimSpace(string(outAfter)), "\n"))

	if commitsAfter != commitsBefore {
		t.Errorf("expected no new commits on double-done, before=%d after=%d", commitsBefore, commitsAfter)
	}
}

func TestDone_DeactivatesTask(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Active then done")

	// Activate the task first via edit --active=true.
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--active=true"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit --active=true error = %v", err)
	}

	// Verify it's active.
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after activate")
	}
	if !task.IsActive() {
		t.Fatal("task should be active before done")
	}

	// Mark as done.
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"done", id[:8]})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("done command error = %v\noutput: %s", err, buf.String())
	}

	// Verify task is done AND no longer active.
	task, ok = getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after done")
	}
	if task.Status != "done" {
		t.Errorf("Status: got %q, want %q", task.Status, "done")
	}
	if task.IsActive() {
		t.Error("task should not be active after done — done must auto-deactivate")
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
	if want := expectSchedule(t, "tomorrow"); task.Schedule != want {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, want)
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
	if want := expectSchedule(t, "someday"); task.Schedule != want {
		t.Errorf("Schedule: got %q, want %q", task.Schedule, want)
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

func TestEditCommand_ChangeBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Body test")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--body", "Some detailed notes"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("edit command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if task.Body != "Some detailed notes" {
		t.Errorf("Body: got %q, want %q", task.Body, "Some detailed notes")
	}
}

func TestEditCommand_ClearBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Clear body test")

	// First set a body
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--body", "Has body"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit body set error = %v", err)
	}

	// Now clear it
	rootCmd2 := NewRootCmd()
	rootCmd2.SetOut(new(bytes.Buffer))
	rootCmd2.SetErr(new(bytes.Buffer))
	rootCmd2.SetArgs([]string{"edit", id[:8], "--body", ""})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("edit body clear error = %v", err)
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if task.Body != "" {
		t.Errorf("Body should be empty after clearing, got %q", task.Body)
	}
}

func TestEditCommand_InvalidSchedule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Bad schedule edit")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--schedule", "garbage"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid schedule in edit, got nil")
	}
}

func TestEditCommand_EditDoneTask(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Done task edit")

	// Mark as done
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	// Edit the done task's title
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"edit", id[:8], "--title", "Updated done task"})

	err := rootCmd2.Execute()
	if err != nil {
		t.Fatalf("edit done task error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if task.Title != "Updated done task" {
		t.Errorf("Title: got %q, want %q", task.Title, "Updated done task")
	}
	// Status should remain done
	if task.Status != "done" {
		t.Errorf("Status should remain 'done', got %q", task.Status)
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

// TestEditCommand_RepeatedIdenticalEditDoesNotError is the regression guard
// for a real crash: re-issuing an edit that doesn't actually change the
// stored task (most commonly --active=true on an already-active task, or any
// edit flag reissued fast enough to land within the same UpdatedAt second)
// used to fail with "nothing to commit, working tree clean" — git's honest
// answer to being asked to commit content byte-identical to HEAD, surfaced
// as a command failure even though the store write had already succeeded and
// the task was already in the requested state. Neither call here should
// error; whether the second one produces a real commit depends on whether it
// landed in the same wall-clock second as the first, which this test does
// not control and doesn't need to.
func TestEditCommand_RepeatedIdenticalEditDoesNotError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Repeat me")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--active=true"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first edit --active=true error = %v\noutput: %s", err, buf.String())
	}

	rootCmd2 := NewRootCmd()
	buf2 := new(bytes.Buffer)
	rootCmd2.SetOut(buf2)
	rootCmd2.SetErr(buf2)
	rootCmd2.SetArgs([]string{"edit", id[:8], "--active=true"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("repeated edit --active=true error = %v\noutput: %s", err, buf2.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found")
	}
	if !task.IsActive() {
		t.Error("task should still be active")
	}
}

// --- Edit --active tests ---

func TestEdit_ActivateAndDeactivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Active toggle")

	// Initially task should not be active
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found")
	}
	if task.IsActive() {
		t.Fatal("new task should not be active")
	}

	// Activate with --active=true
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--active=true"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit --active=true error = %v\noutput: %s", err, buf.String())
	}

	task, ok = getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after activate")
	}
	if !task.IsActive() {
		t.Error("task should be active after --active=true")
	}

	// Deactivate with --active=false
	rootCmd2 := NewRootCmd()
	buf2 := new(bytes.Buffer)
	rootCmd2.SetOut(buf2)
	rootCmd2.SetErr(buf2)
	rootCmd2.SetArgs([]string{"edit", id[:8], "--active=false"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("edit --active=false error = %v\noutput: %s", err, buf2.String())
	}

	task, ok = getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after deactivate")
	}
	if task.IsActive() {
		t.Error("task should not be active after --active=false")
	}

	// Omitting --active leaves tags unchanged (task stays inactive)
	rootCmd3 := NewRootCmd()
	rootCmd3.SetOut(new(bytes.Buffer))
	rootCmd3.SetErr(new(bytes.Buffer))
	rootCmd3.SetArgs([]string{"edit", id[:8], "--title", "Renamed"})
	if err := rootCmd3.Execute(); err != nil {
		t.Fatalf("edit --title error = %v", err)
	}

	task, ok = getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after title edit")
	}
	if task.IsActive() {
		t.Error("task should remain inactive when --active is not passed")
	}
}

func TestEdit_ActivateMovesToTodayTop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	idToday := addTestTaskWithSchedule(t, dir, "already today", "today")
	idWeek := addTestTaskWithSchedule(t, dir, "week task", "week")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", idWeek, "--active=true"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit --active=true error = %v\noutput: %s", err, buf.String())
	}

	moved, ok := getTaskByID(t, dir, idWeek)
	if !ok {
		t.Fatal("task not found after activate")
	}
	if !moved.IsActive() {
		t.Error("task should be active")
	}
	today, ok := getTaskByID(t, dir, idToday)
	if !ok {
		t.Fatal("today task not found")
	}
	if moved.Schedule != today.Schedule {
		t.Errorf("Schedule = %q, want %q (today, matching existing today task)", moved.Schedule, today.Schedule)
	}
	if moved.Position >= today.Position {
		t.Errorf("activated task Position = %v, want less than existing today task's %v", moved.Position, today.Position)
	}
}

func TestEdit_ActivateWithExplicitScheduleWins(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTaskWithSchedule(t, dir, "week task", "week")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--active=true", "--schedule", "month"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found")
	}
	if !task.IsActive() {
		t.Error("task should be active")
	}
	wantSchedule, err := schedule.Parse("month", time.Now(), config.DateFormat())
	if err != nil {
		t.Fatalf("schedule.Parse: %v", err)
	}
	if task.Schedule != wantSchedule {
		t.Errorf("Schedule = %q, want explicit --schedule value %q (not auto-moved to today)", task.Schedule, wantSchedule)
	}
}

func TestEdit_DeactivateDoesNotMoveTask(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTaskWithSchedule(t, dir, "week task", "week")

	// Activate first (this is the step that moves the task to today's top),
	// then deactivate and assert the post-activate schedule/position are
	// left exactly as they were — deactivating must not move anything.
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id, "--active=true"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit --active=true error = %v\noutput: %s", err, buf.String())
	}
	afterActivate, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after activate")
	}

	rootCmd2 := NewRootCmd()
	buf2 := new(bytes.Buffer)
	rootCmd2.SetOut(buf2)
	rootCmd2.SetErr(buf2)
	rootCmd2.SetArgs([]string{"edit", id, "--active=false"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("edit --active=false error = %v\noutput: %s", err, buf2.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found")
	}
	if task.IsActive() {
		t.Error("task should be inactive")
	}
	if task.Schedule != afterActivate.Schedule {
		t.Errorf("Schedule = %q, want unchanged from post-activate value %q", task.Schedule, afterActivate.Schedule)
	}
	if task.Position != afterActivate.Position {
		t.Errorf("Position = %v, want unchanged from post-activate value %v", task.Position, afterActivate.Position)
	}
}

func TestEdit_ActivateOverdueTaskKeepsSchedule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	overdue := time.Now().AddDate(0, 0, -3).Format(schedule.IsoLayout)
	id := addTestTaskWithSchedule(t, dir, "overdue task", overdue)
	idToday := addTestTaskWithSchedule(t, dir, "today task", "today")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id, "--active=true"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit --active=true error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found")
	}
	if !task.IsActive() {
		t.Error("task should be active")
	}
	if task.Schedule != overdue {
		t.Errorf("Schedule = %q, want unchanged overdue date %q (must not clobber a custom date)", task.Schedule, overdue)
	}
	today, ok := getTaskByID(t, dir, idToday)
	if !ok {
		t.Fatal("today task not found")
	}
	if task.Position >= today.Position {
		t.Errorf("activated task Position = %v, want less than existing today task's %v", task.Position, today.Position)
	}
}

// TestEdit_ActivateRebalancesTodayBucketCleanly mirrors the TUI/Telegram
// activate-rebalance tests: PositionTop halves the lowest sibling's
// position, so a sibling seeded close enough to zero forces a whole-bucket
// rebalance. Every rebalanced file must land in the same commit as the
// activation, or .monolog/tasks/ stays dirty and blocks auto-push (see
// CLAUDE.md's Auto-push-on-mutation section).
func TestEdit_ActivateRebalancesTodayBucketCleanly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	idTight := addTestTaskWithSchedule(t, dir, "tight today", "today")
	tight, ok := getTaskByID(t, dir, idTight)
	if !ok {
		t.Fatal("tight task not found")
	}
	tight.Position = 1
	writeTestTask(t, dir, tight)

	idWeek := addTestTaskWithSchedule(t, dir, "week task", "week")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", idWeek, "--active=true"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit --active=true error = %v\noutput: %s", err, buf.String())
	}

	moved, ok := getTaskByID(t, dir, idWeek)
	if !ok {
		t.Fatal("moved task not found")
	}
	rebalancedTight, ok := getTaskByID(t, dir, idTight)
	if !ok {
		t.Fatal("tight task not found")
	}
	if moved.Position >= rebalancedTight.Position {
		t.Errorf("activated task Position = %v, want less than sibling's %v", moved.Position, rebalancedTight.Position)
	}
	if rebalancedTight.Position-moved.Position < 100 {
		t.Fatalf("fixture did not trigger a rebalance; positions were not spread back out (moved=%v tight=%v)", moved.Position, rebalancedTight.Position)
	}

	gitCmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("rebalanced siblings left uncommitted, which permanently defers auto-push's rebase:\n%s", string(out))
	}
}

// --- Edit --body recalculates NoteCount (regression) ---

func TestEditCommand_ClearBodyResetsNoteCount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Notes then clear")

	// Add two notes via the note command.
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"note", id[:8], "first note"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first note error = %v", err)
	}

	rootCmd2 := NewRootCmd()
	rootCmd2.SetOut(new(bytes.Buffer))
	rootCmd2.SetErr(new(bytes.Buffer))
	rootCmd2.SetArgs([]string{"note", id[:8], "second note"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("second note error = %v", err)
	}

	// Sanity check: NoteCount should be 2 now.
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after notes")
	}
	if task.NoteCount != 2 {
		t.Fatalf("NoteCount before clear: got %d, want 2", task.NoteCount)
	}

	// Clear the body with --body "".
	rootCmd3 := NewRootCmd()
	rootCmd3.SetOut(new(bytes.Buffer))
	rootCmd3.SetErr(new(bytes.Buffer))
	rootCmd3.SetArgs([]string{"edit", id[:8], "--body", ""})
	if err := rootCmd3.Execute(); err != nil {
		t.Fatalf("edit --body clear error = %v", err)
	}

	task, ok = getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after body clear")
	}
	if task.Body != "" {
		t.Errorf("Body: got %q, want empty", task.Body)
	}
	if task.NoteCount != 0 {
		t.Errorf("NoteCount after body clear: got %d, want 0", task.NoteCount)
	}
}

func TestEditCommand_BodyWithSeparatorSetsNoteCount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Manual body separator")

	// Set a body containing a well-formed separator line via --body.
	body := "some intro\n--- 17-04-2026 10:30:00 ---\nthe note content"

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--body", body})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit --body error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if task.Body != body {
		t.Errorf("Body: got %q, want %q", task.Body, body)
	}
	if task.NoteCount != 1 {
		t.Errorf("NoteCount: got %d, want 1 (recalculated from body separator)", task.NoteCount)
	}
}

// --- Edit --recur tests ---

func TestEditCommand_SetRecurrence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Recurrence target")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--recur", "weekly:mon"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit --recur error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if task.Recurrence != "weekly:mon" {
		t.Errorf("Recurrence: got %q, want %q", task.Recurrence, "weekly:mon")
	}
}

func TestEditCommand_SetRecurrenceAliasCanonicalizes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Alias target")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"edit", id[:8], "--recur", "weekly:Monday"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit --recur error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after edit")
	}
	if task.Recurrence != "weekly:mon" {
		t.Errorf("Recurrence: got %q, want %q (canonical form)", task.Recurrence, "weekly:mon")
	}
}

func TestEditCommand_ClearRecurrence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Clear recurrence target")

	// Set a recurrence first
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--recur", "monthly:1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("set recurrence error = %v", err)
	}

	task, _ := getTaskByID(t, dir, id)
	if task.Recurrence != "monthly:1" {
		t.Fatalf("recurrence not set: got %q, want %q", task.Recurrence, "monthly:1")
	}

	// Now clear it
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"edit", id[:8], "--recur", ""})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("clear recurrence error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after clear")
	}
	if task.Recurrence != "" {
		t.Errorf("Recurrence: got %q, want empty (cleared)", task.Recurrence)
	}
}

func TestEditCommand_InvalidRecurrenceDoesNotMutate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Keep existing")

	// Set a known-good rule first.
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--recur", "days:7"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("set baseline recurrence error = %v", err)
	}

	// Capture updated_at before the failed edit.
	taskBefore, _ := getTaskByID(t, dir, id)

	// Attempt an invalid edit — should return an error and leave the task alone.
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"edit", id[:8], "--recur", "bogus"})
	if err := rootCmd2.Execute(); err == nil {
		t.Fatal("expected error for invalid recurrence, got nil")
	}

	taskAfter, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after failed edit")
	}
	if taskAfter.Recurrence != "days:7" {
		t.Errorf("Recurrence mutated on failed edit: got %q, want %q", taskAfter.Recurrence, "days:7")
	}
	if taskAfter.UpdatedAt != taskBefore.UpdatedAt {
		t.Errorf("UpdatedAt changed on failed edit: before %q, after %q", taskBefore.UpdatedAt, taskAfter.UpdatedAt)
	}
}

func TestEditCommand_OmittedRecurrencePreserved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Preserve recur")

	// Set a recurrence first
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--recur", "workdays"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("set recurrence error = %v", err)
	}

	// Edit something else without passing --recur
	rootCmd2 := NewRootCmd()
	rootCmd2.SetOut(new(bytes.Buffer))
	rootCmd2.SetErr(new(bytes.Buffer))
	rootCmd2.SetArgs([]string{"edit", id[:8], "--title", "Renamed"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("edit title error = %v", err)
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after title edit")
	}
	if task.Title != "Renamed" {
		t.Errorf("Title: got %q, want %q", task.Title, "Renamed")
	}
	if task.Recurrence != "workdays" {
		t.Errorf("Recurrence should be preserved when --recur omitted: got %q, want %q", task.Recurrence, "workdays")
	}
}

func TestEditCommand_InvalidRecurrenceGrammar(t *testing.T) {
	cases := []string{
		"bogus",
		"monthly:0",
		"monthly:32",
		"weekly:xyz",
		"weekly:0",
		"weekly:8",
		"days:0",
		"days:-1",
		"unknown:rule",
	}

	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "monolog")
			initTestRepo(t, dir)

			id := addTestTask(t, dir, "invalid recur test")

			rootCmd := NewRootCmd()
			buf := new(bytes.Buffer)
			rootCmd.SetOut(buf)
			rootCmd.SetErr(buf)
			rootCmd.SetArgs([]string{"edit", id[:8], "--recur", input})

			if err := rootCmd.Execute(); err == nil {
				t.Fatalf("expected error for invalid recurrence %q, got nil", input)
			}

			task, _ := getTaskByID(t, dir, id)
			if task.Recurrence != "" {
				t.Errorf("Recurrence should remain empty after failed edit, got %q", task.Recurrence)
			}
		})
	}
}

func TestEdit_TagsPreservesActive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Tag preserve")

	// Activate the task first
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--active=true"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("activate error = %v", err)
	}

	// Now rewrite tags without mentioning active
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"edit", id[:8], "--tags", "work,urgent"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("edit --tags error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after tag rewrite")
	}
	if !task.IsActive() {
		t.Error("active state should be preserved after --tags rewrite")
	}
	// Check that the user's tags are present
	hasWork, hasUrgent := false, false
	for _, tag := range task.Tags {
		if tag == "work" {
			hasWork = true
		}
		if tag == "urgent" {
			hasUrgent = true
		}
	}
	if !hasWork || !hasUrgent {
		t.Errorf("expected tags to contain 'work' and 'urgent', got %v", task.Tags)
	}

	// Test: when both --tags AND --active=false are given, explicit --active wins
	rootCmd3 := NewRootCmd()
	rootCmd3.SetOut(new(bytes.Buffer))
	rootCmd3.SetErr(new(bytes.Buffer))
	rootCmd3.SetArgs([]string{"edit", id[:8], "--tags", "personal", "--active=false"})
	if err := rootCmd3.Execute(); err != nil {
		t.Fatalf("edit --tags --active=false error = %v", err)
	}

	task, ok = getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after combined edit")
	}
	if task.IsActive() {
		t.Error("task should not be active when --active=false is explicitly given with --tags")
	}
	hasPersonal := false
	for _, tag := range task.Tags {
		if tag == "personal" {
			hasPersonal = true
		}
	}
	if !hasPersonal {
		t.Errorf("expected tags to contain 'personal', got %v", task.Tags)
	}
}
