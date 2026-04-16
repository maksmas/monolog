package cmd

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoteCommand_AppendsNoteToTask(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Note target")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"note", id[:8], "first note text"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("note command error = %v\noutput: %s", err, buf.String())
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after note")
	}

	if !strings.Contains(task.Body, "first note text") {
		t.Errorf("Body should contain note text, got: %q", task.Body)
	}
	if !strings.Contains(task.Body, "---") {
		t.Errorf("Body should contain separator, got: %q", task.Body)
	}
	if task.NoteCount != 1 {
		t.Errorf("NoteCount: got %d, want 1", task.NoteCount)
	}
}

func TestNoteCommand_IncrementsNoteCount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Multi note")

	// Add first note
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"note", id[:8], "note one"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first note error = %v", err)
	}

	// Add second note
	rootCmd2 := NewRootCmd()
	rootCmd2.SetOut(new(bytes.Buffer))
	rootCmd2.SetErr(new(bytes.Buffer))
	rootCmd2.SetArgs([]string{"note", id[:8], "note two"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("second note error = %v", err)
	}

	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found after notes")
	}
	if task.NoteCount != 2 {
		t.Errorf("NoteCount: got %d, want 2", task.NoteCount)
	}
	if !strings.Contains(task.Body, "note one") {
		t.Errorf("Body should contain first note, got: %q", task.Body)
	}
	if !strings.Contains(task.Body, "note two") {
		t.Errorf("Body should contain second note, got: %q", task.Body)
	}
}

func TestNoteCommand_UpdatesUpdatedAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Timestamp note")

	// Backdate the task
	task, _ := getTaskByID(t, dir, id)
	task.UpdatedAt = "2020-01-01T00:00:00Z"
	writeTestTask(t, dir, task)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"note", id[:8], "update check"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("note command error = %v", err)
	}

	taskAfter, _ := getTaskByID(t, dir, id)
	if taskAfter.UpdatedAt == "2020-01-01T00:00:00Z" {
		t.Error("updated_at should change after note")
	}
}

func TestNoteCommand_ErrorOnBadIdentifier(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"note", "NONEXISTENT", "some text"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-existent identifier, got nil")
	}
}

func TestNoteCommand_ErrorNoArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"note"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided, got nil")
	}
}

func TestNoteCommand_ErrorOneArg(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "One arg test")

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"note", id[:8]})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when only one arg provided, got nil")
	}
}

func TestNoteCommand_OutputMessage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Output check")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"note", id[:8], "a note"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("note command error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Note added") {
		t.Errorf("output should contain 'Note added', got: %s", output)
	}
	if !strings.Contains(output, "Output check") {
		t.Errorf("output should contain task title, got: %s", output)
	}
}

func TestNoteCommand_AutoCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Committed note")

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"note", id[:8], "some note"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("note command error = %v", err)
	}

	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline", "-1")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !strings.Contains(string(out), "note: Committed note") {
		t.Errorf("commit message should contain 'note: Committed note', got: %s", string(out))
	}
}
