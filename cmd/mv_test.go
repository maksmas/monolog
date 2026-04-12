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

// addTestTaskWithSchedule adds a task with a specific schedule via the CLI and returns its ID.
func addTestTaskWithSchedule(t *testing.T, dir, title, schedule string) string {
	t.Helper()
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"add", title, "--schedule", schedule})
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

// writeTestTask writes a task back to disk (for setting up test positions).
func writeTestTask(t *testing.T, dir string, task model.Task) {
	t.Helper()
	path := filepath.Join(dir, ".monolog", "tasks", task.ID+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}
}

// --- mv --top / --bottom tests ---

func TestMvCommand_MoveToTop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add three tasks — they get positions 1000, 2000, 3000
	_ = addTestTask(t, dir, "First")
	_ = addTestTask(t, dir, "Second")
	id3 := addTestTask(t, dir, "Third")

	// Move Third to top (use full ID to avoid prefix ambiguity)
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id3, "--top"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mv --top error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id3)
	if !ok {
		t.Fatal("task not found after mv")
	}

	// Third should now have a position less than First (1000)
	if task.Position >= 1000 {
		t.Errorf("Position after --top: got %v, want < 1000", task.Position)
	}
}

func TestMvCommand_MoveToBottom(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id1 := addTestTask(t, dir, "First")
	_ = addTestTask(t, dir, "Second")
	_ = addTestTask(t, dir, "Third")

	// Move First to bottom
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id1, "--bottom"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mv --bottom error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id1)
	if !ok {
		t.Fatal("task not found after mv")
	}

	// First should now have a position greater than Third (3000)
	if task.Position <= 3000 {
		t.Errorf("Position after --bottom: got %v, want > 3000", task.Position)
	}
}

func TestMvCommand_TopWithinScheduleGroup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add tasks in different schedule groups
	_ = addTestTaskWithSchedule(t, dir, "Today-1", "today")
	_ = addTestTaskWithSchedule(t, dir, "Today-2", "today")
	idTomorrow := addTestTaskWithSchedule(t, dir, "Tomorrow-1", "tomorrow")
	idTomorrow2 := addTestTaskWithSchedule(t, dir, "Tomorrow-2", "tomorrow")

	// Move Tomorrow-1 to top — should be top of "tomorrow" group
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", idTomorrow, "--top"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mv --top error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, idTomorrow)
	if !ok {
		t.Fatal("task not found after mv")
	}

	// Tomorrow-2 remains at its original position. Tomorrow-1 should be before it.
	task2, _ := getTaskByID(t, dir, idTomorrow2)
	if task.Position >= task2.Position {
		t.Errorf("Position after --top in tomorrow group: got %v, want < %v (Tomorrow-2)",
			task.Position, task2.Position)
	}
}

// --- mv --before / --after tests ---

func TestMvCommand_MoveAfter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id1 := addTestTask(t, dir, "First")
	id2 := addTestTask(t, dir, "Second")
	id3 := addTestTask(t, dir, "Third")

	// Move First after Second (between Second and Third)
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id1, "--after", id2})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mv --after error = %v\noutput: %s", err, buf.String())
	}

	task1, _ := getTaskByID(t, dir, id1)
	task2, _ := getTaskByID(t, dir, id2)
	task3, _ := getTaskByID(t, dir, id3)

	// First should now be between Second and Third
	if task1.Position <= task2.Position || task1.Position >= task3.Position {
		t.Errorf("After --after: task1.Position=%v should be between task2=%v and task3=%v",
			task1.Position, task2.Position, task3.Position)
	}
}

func TestMvCommand_MoveAfterLast(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id1 := addTestTask(t, dir, "First")
	_ = addTestTask(t, dir, "Second")
	id3 := addTestTask(t, dir, "Third")

	// Move First after Third (Third is last)
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id1, "--after", id3})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mv --after error = %v\noutput: %s", err, buf.String())
	}

	task1, _ := getTaskByID(t, dir, id1)
	task3, _ := getTaskByID(t, dir, id3)

	// First should now be after Third
	if task1.Position <= task3.Position {
		t.Errorf("After --after last: task1.Position=%v should be > task3=%v",
			task1.Position, task3.Position)
	}
}

func TestMvCommand_MoveBefore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id1 := addTestTask(t, dir, "First")
	id2 := addTestTask(t, dir, "Second")
	id3 := addTestTask(t, dir, "Third")

	// Move Third before Second (between First and Second)
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id3, "--before", id2})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mv --before error = %v\noutput: %s", err, buf.String())
	}

	task1, _ := getTaskByID(t, dir, id1)
	task2, _ := getTaskByID(t, dir, id2)
	task3, _ := getTaskByID(t, dir, id3)

	// Third should now be between First and Second
	if task3.Position <= task1.Position || task3.Position >= task2.Position {
		t.Errorf("After --before: task3.Position=%v should be between task1=%v and task2=%v",
			task3.Position, task1.Position, task2.Position)
	}
}

func TestMvCommand_MoveBeforeFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id1 := addTestTask(t, dir, "First")
	_ = addTestTask(t, dir, "Second")
	id3 := addTestTask(t, dir, "Third")

	// Move Third before First (First is first)
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id3, "--before", id1})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mv --before error = %v\noutput: %s", err, buf.String())
	}

	task1, _ := getTaskByID(t, dir, id1)
	task3, _ := getTaskByID(t, dir, id3)

	// Third should now be before First
	if task3.Position >= task1.Position {
		t.Errorf("After --before first: task3.Position=%v should be < task1=%v",
			task3.Position, task1.Position)
	}
}

// --- rebalance trigger and auto-commit tests ---

func TestMvCommand_AutoCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id1 := addTestTask(t, dir, "Move me")
	_ = addTestTask(t, dir, "Other")

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"mv", id1, "--bottom"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("mv command error = %v", err)
	}

	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline", "-1")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !strings.Contains(string(out), "mv: Move me") {
		t.Errorf("commit message should contain 'mv: Move me', got: %s", string(out))
	}
}

func TestMvCommand_RebalanceTrigger(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create tasks with very tight positions by manually editing their positions
	id1 := addTestTask(t, dir, "Task-A")
	id2 := addTestTask(t, dir, "Task-B")
	id3 := addTestTask(t, dir, "Task-C")

	// Set up tight positions: 1000, 1000.5, 1001
	task1, _ := getTaskByID(t, dir, id1)
	task1.Position = 1000
	writeTestTask(t, dir, task1)

	task2, _ := getTaskByID(t, dir, id2)
	task2.Position = 1000.5
	writeTestTask(t, dir, task2)

	task3, _ := getTaskByID(t, dir, id3)
	task3.Position = 1001
	writeTestTask(t, dir, task3)

	// Move Task-C before Task-B.
	// Without the moved task, others are [A=1000, B=1000.5].
	// --before B when B is at index 1: PositionBetween(1000, 1000.5) = 1000.25
	// After move: [A=1000, C=1000.25, B=1000.5] — gap 0.25 < 1.0, triggers rebalance.
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id3, "--before", id2})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("mv command error = %v\noutput: %s", err, buf.String())
	}

	// After rebalance, positions should be evenly spaced
	t1, _ := getTaskByID(t, dir, id1)
	t2, _ := getTaskByID(t, dir, id2)
	t3, _ := getTaskByID(t, dir, id3)

	// Verify ordering is preserved: Task-A, Task-C, Task-B
	if t1.Position >= t3.Position || t3.Position >= t2.Position {
		t.Errorf("Expected ordering A < C < B, got A=%v C=%v B=%v",
			t1.Position, t3.Position, t2.Position)
	}

	// Verify positions are well-spaced (rebalanced to 1000, 2000, 3000)
	gap1 := t3.Position - t1.Position
	gap2 := t2.Position - t3.Position
	if gap1 < 1.0 || gap2 < 1.0 {
		t.Errorf("Expected rebalanced gaps >= 1.0, got gap1=%v gap2=%v", gap1, gap2)
	}
}

// --- error cases ---

func TestMvCommand_ErrorNoFlags(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "No flags")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no position flag is specified")
	}
}

func TestMvCommand_ErrorMultipleFlags(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Multiple flags")
	id2 := addTestTask(t, dir, "Target")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id, "--top", "--after", id2})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when multiple position flags specified")
	}
}

func TestMvCommand_ErrorBadPrefix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", "NONEXISTENT", "--top"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-existent prefix")
	}
}

func TestMvCommand_ErrorBeforeDifferentScheduleGroup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	idToday := addTestTaskWithSchedule(t, dir, "Today task", "today")
	idTomorrow := addTestTaskWithSchedule(t, dir, "Tomorrow task", "tomorrow")

	// Try to move today task --before tomorrow task (different schedule group)
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", idToday, "--before", idTomorrow})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --before target is in a different schedule group")
	}
	if !strings.Contains(err.Error(), "schedule") {
		t.Errorf("error should mention schedule mismatch, got: %v", err)
	}
}

func TestMvCommand_ErrorAfterDifferentScheduleGroup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	idToday := addTestTaskWithSchedule(t, dir, "Today task", "today")
	idTomorrow := addTestTaskWithSchedule(t, dir, "Tomorrow task", "tomorrow")

	// Try to move today task --after tomorrow task (different schedule group)
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", idToday, "--after", idTomorrow})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --after target is in a different schedule group")
	}
	if !strings.Contains(err.Error(), "schedule") {
		t.Errorf("error should mention schedule mismatch, got: %v", err)
	}
}

func TestMvCommand_SoleMemberTop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add a single task in the tomorrow group
	id := addTestTaskWithSchedule(t, dir, "Lone task", "tomorrow")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id, "--top"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mv --top error = %v\noutput: %s", err, buf.String())
	}

	// Sole member at same position: should report "already at" and not crash
	output := buf.String()
	if !strings.Contains(output, "Already at") {
		t.Errorf("expected 'Already at' message for sole member, got: %s", output)
	}
}

func TestMvCommand_SoleMemberBottom(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTaskWithSchedule(t, dir, "Lone bottom", "tomorrow")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"mv", id, "--bottom"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mv --bottom error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Already at") {
		t.Errorf("expected 'Already at' message for sole member, got: %s", output)
	}
}

func TestMvCommand_ErrorBeforeDoneTarget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id1 := addTestTask(t, dir, "Open task")
	id2 := addTestTask(t, dir, "Done target")

	// Mark id2 as done
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id2})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	// Try to move id1 --before the done task
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"mv", id1, "--before", id2})

	err := rootCmd2.Execute()
	if err == nil {
		t.Fatal("expected error when --before target is done")
	}
	if !strings.Contains(err.Error(), "done") {
		t.Errorf("error should mention target is done, got: %v", err)
	}
}

func TestMvCommand_ErrorNoArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"mv"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided")
	}
}
