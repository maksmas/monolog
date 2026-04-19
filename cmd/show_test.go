package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestShowCommand_DisplaysTaskDetail(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Show me details")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", id[:8]})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	// Title
	if !strings.Contains(output, "Title:     Show me details") {
		t.Errorf("output should contain title, got:\n%s", output)
	}

	// Short ID
	if !strings.Contains(output, "ID:        "+id[:8]) {
		t.Errorf("output should contain short ID, got:\n%s", output)
	}

	// Status
	if !strings.Contains(output, "Status:    open") {
		t.Errorf("output should contain status, got:\n%s", output)
	}

	// Schedule (should show bucket name + date in the configured format,
	// e.g. "today (<DD-MM-YYYY>)").
	if !strings.Contains(output, "Schedule:") {
		t.Errorf("output should contain schedule, got:\n%s", output)
	}

	// Created
	if !strings.Contains(output, "Created:") {
		t.Errorf("output should contain created date, got:\n%s", output)
	}
}

func TestShowCommand_WithBodyAndNotes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Body task")

	// Set a body via edit
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--body", "Initial body text"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit body error = %v", err)
	}

	// Add a note
	rootCmd2 := NewRootCmd()
	rootCmd2.SetOut(new(bytes.Buffer))
	rootCmd2.SetErr(new(bytes.Buffer))
	rootCmd2.SetArgs([]string{"note", id[:8], "first note"})
	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("note command error = %v", err)
	}

	// Now show
	rootCmd3 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd3.SetOut(buf)
	rootCmd3.SetErr(buf)
	rootCmd3.SetArgs([]string{"show", id[:8]})

	if err := rootCmd3.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	// Body text (includes original body and note)
	if !strings.Contains(output, "Initial body text") {
		t.Errorf("output should contain body text, got:\n%s", output)
	}
	if !strings.Contains(output, "first note") {
		t.Errorf("output should contain note text, got:\n%s", output)
	}
	if !strings.Contains(output, "---") {
		t.Errorf("output should contain note separator, got:\n%s", output)
	}

	// Note count
	if !strings.Contains(output, "Notes:     1") {
		t.Errorf("output should contain note count, got:\n%s", output)
	}
}

func TestShowCommand_WithoutBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "No body task")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	// Should have metadata but no blank line + body section
	if !strings.Contains(output, "Title:     No body task") {
		t.Errorf("output should contain title, got:\n%s", output)
	}

	// The output should end after the metadata lines (no trailing body section)
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	lastLine := lines[len(lines)-1]
	// Last line should be a metadata line (Created: or Updated:), not empty
	if !strings.Contains(lastLine, ":") {
		t.Errorf("last line should be a metadata field when no body, got: %q", lastLine)
	}
}

func TestShowCommand_WithTags(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Tagged task")

	// Add tags
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--tags", "work,urgent"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit tags error = %v", err)
	}

	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"show", id[:8]})

	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	if !strings.Contains(output, "Tags:      work, urgent") {
		t.Errorf("output should contain tags, got:\n%s", output)
	}
}

func TestShowCommand_CompletedTask(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Done task")

	// Mark as done via CLI (CLI done does not set CompletedAt)
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v", err)
	}

	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"show", id[:8]})

	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	if !strings.Contains(output, "Status:    done") {
		t.Errorf("output should show done status, got:\n%s", output)
	}
}

func TestShowCommand_CompletedTaskWithCompletedAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Done with date")

	// Manually set CompletedAt (simulating TUI done which sets it)
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found")
	}
	task.Status = "done"
	task.CompletedAt = "2026-04-16T12:00:00Z"
	writeTestTask(t, dir, task)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	if !strings.Contains(output, "Completed: 2026-04-16T12:00:00Z") {
		t.Errorf("output should contain completed date, got:\n%s", output)
	}
}

func TestShowCommand_ErrorOnBadIdentifier(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", "NONEXISTENT"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-existent identifier, got nil")
	}
}

func TestShowCommand_ErrorNoArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"show"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided, got nil")
	}
}

func TestShowCommand_OmitsNotesWhenZero(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "No notes task")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	// NoteCount is 0, so "Notes:" should not appear in output.
	if strings.Contains(output, "Notes:") {
		t.Errorf("output should not contain Notes: when NoteCount is 0, got:\n%s", output)
	}
}

func TestShowCommand_DisplaysRecurrence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Recurring task")

	// Set recurrence via edit
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"edit", id[:8], "--recur", "monthly:1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("edit recur error = %v", err)
	}

	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"show", id[:8]})

	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	if !strings.Contains(output, "Recur:     monthly:1") {
		t.Errorf("output should contain recurrence line, got:\n%s", output)
	}
}

func TestShowCommand_OmitsRecurrenceWhenEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Non-recurring task")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	// Recurrence is empty, so "Recur:" should not appear in output.
	if strings.Contains(output, "Recur:") {
		t.Errorf("output should not contain Recur: when empty, got:\n%s", output)
	}
}

func TestShowCommand_ScheduleDisplay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Schedule display")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()

	// Default schedule is today, so output should contain "today" bucket name
	if !strings.Contains(output, "Schedule:  today") {
		t.Errorf("output should show 'today' bucket name in schedule, got:\n%s", output)
	}

	// Should also contain a date in parentheses
	if !strings.Contains(output, "(") || !strings.Contains(output, ")") {
		t.Errorf("output should show date in parentheses, got:\n%s", output)
	}
}

// TestShowCommand_ScheduleLegacyBucketNoRedundantDate verifies that when
// the stored schedule is a legacy bucket string (e.g. "today"), the Schedule
// line does not render a redundant "(today)" suffix — matching the TUI
// detail panel behavior.
func TestShowCommand_ScheduleLegacyBucketNoRedundantDate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Legacy bucket task")

	// Force a legacy bucket string as the stored schedule, which is what
	// older monolog versions wrote to disk.
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found")
	}
	task.Schedule = "today"
	writeTestTask(t, dir, task)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Schedule:  today\n") {
		t.Errorf("output should show 'Schedule:  today' without parenthetical, got:\n%s", output)
	}
	if strings.Contains(output, "today (today)") {
		t.Errorf("output should not duplicate bucket name, got:\n%s", output)
	}
}

// TestShowCommand_ScheduleRendersConfiguredFormat verifies the Schedule
// date inside the parentheses is rendered in the configured user-facing
// format (DD-MM-YYYY by default), not the stored ISO form.
func TestShowCommand_ScheduleRendersConfiguredFormat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "Fixed date schedule")

	// Force a concrete far-future schedule so the output is deterministic
	// regardless of when the test runs (buckets for "today" would shift).
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task not found")
	}
	task.Schedule = "2030-04-15"
	writeTestTask(t, dir, task)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	// DD-MM-YYYY rendering must appear.
	if !strings.Contains(output, "15-04-2030") {
		t.Errorf("output should show DD-MM-YYYY date '15-04-2030', got:\n%s", output)
	}
	// Raw ISO must not leak into the Schedule line.
	if strings.Contains(output, "(2030-04-15)") {
		t.Errorf("Schedule line should render in configured format, not raw ISO, got:\n%s", output)
	}
}
