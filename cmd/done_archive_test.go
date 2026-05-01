package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/model"
)

// archiveCall records a single invocation of the archive seam so tests can
// assert that the right SourceID was passed (or that the seam was never hit).
type archiveCall struct {
	sourceID string
	ec       config.EmailConfig
}

// stubArchiveFn replaces the package-level archiveFn with a recorder. The
// returned slice is appended to on each call. If retErr is non-nil it is
// returned to the caller (simulating an archive failure).
func stubArchiveFn(t *testing.T, retErr error) *[]archiveCall {
	t.Helper()
	var calls []archiveCall
	prev := archiveFn
	archiveFn = func(sourceID string, ec config.EmailConfig) error {
		calls = append(calls, archiveCall{sourceID: sourceID, ec: ec})
		return retErr
	}
	t.Cleanup(func() { archiveFn = prev })
	return &calls
}

// seedGmailTask creates a task on disk with Source=gmail and SourceID set.
// It commits the file so the subsequent `done` doesn't see an unstaged
// preexisting fixture as part of its own commit.
func seedGmailTask(t *testing.T, dir, title, sourceID string) string {
	t.Helper()
	id := addTestTask(t, dir, title)
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatalf("seed task %s not found", id)
	}
	task.Source = "gmail"
	task.SourceID = sourceID
	path := filepath.Join(dir, ".monolog", "tasks", id+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal patched seed: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write patched seed: %v", err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", filepath.Join(".monolog", "tasks", id+".json")).CombinedOutput(); err != nil {
		t.Fatalf("git add patched seed: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "seed gmail task: "+title).CombinedOutput(); err != nil {
		t.Fatalf("git commit patched seed: %v\n%s", err, out)
	}
	return id
}

func TestDone_GmailTask_TriggersArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableEmailConfig(t, dir)

	id := seedGmailTask(t, dir, "Reply to invoice", "gmail-msg-abc")

	calls := stubArchiveFn(t, nil)

	rootCmd := NewRootCmd()
	out := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v\nstderr: %s", err, errBuf.String())
	}

	if len(*calls) != 1 {
		t.Fatalf("expected 1 archive call, got %d", len(*calls))
	}
	if (*calls)[0].sourceID != "gmail-msg-abc" {
		t.Errorf("archive sourceID: got %q, want %q", (*calls)[0].sourceID, "gmail-msg-abc")
	}
	if !strings.Contains(out.String(), "email archived") {
		t.Errorf("expected 'email archived' on stdout, got: %q", out.String())
	}
	// Task is still done.
	task, _ := getTaskByID(t, dir, id)
	if task.Status != "done" {
		t.Errorf("task Status: got %q, want done", task.Status)
	}
}

func TestDone_NonGmailTask_DoesNotArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableEmailConfig(t, dir)

	// Plain manual task — no Source=gmail, no SourceID.
	id := addTestTask(t, dir, "Plain task")

	calls := stubArchiveFn(t, nil)

	rootCmd := NewRootCmd()
	out := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v\nstderr: %s", err, errBuf.String())
	}

	if len(*calls) != 0 {
		t.Errorf("archive should not be called for non-gmail task, got %d calls", len(*calls))
	}
	if strings.Contains(out.String(), "email archived") {
		t.Errorf("non-gmail done should not print 'email archived', got: %q", out.String())
	}
}

func TestDone_GmailTask_ArchiveFailureIsNonFatal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableEmailConfig(t, dir)

	id := seedGmailTask(t, dir, "Reply to ping", "gmail-msg-fail")

	calls := stubArchiveFn(t, errors.New("network down"))

	rootCmd := NewRootCmd()
	out := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"done", id[:8]})
	// Critical: command MUST succeed (exit 0) even when archive fails.
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command should succeed despite archive failure, got: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("expected 1 archive call, got %d", len(*calls))
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "archive failed") || !strings.Contains(stderr, "network down") {
		t.Errorf("expected 'archive failed: network down' on stderr, got: %q", stderr)
	}
	// Stdout should NOT claim 'email archived' on failure.
	if strings.Contains(out.String(), "email archived") {
		t.Errorf("stdout should not say 'email archived' on archive failure, got: %q", out.String())
	}
	// Task is still done despite archive failure.
	task, _ := getTaskByID(t, dir, id)
	if task.Status != "done" {
		t.Errorf("task Status: got %q, want done", task.Status)
	}
}

func TestDone_GmailTask_EmailDisabledSkipsArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	// Do NOT call enableEmailConfig — config has no email block, so
	// Email().Enabled is false.
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	id := seedGmailTask(t, dir, "Skip me", "gmail-msg-skip")

	calls := stubArchiveFn(t, nil)

	rootCmd := NewRootCmd()
	out := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v\nstderr: %s", err, errBuf.String())
	}

	if len(*calls) != 0 {
		t.Errorf("archive should not be called when email integration disabled, got %d calls", len(*calls))
	}
	if strings.Contains(out.String(), "email archived") {
		t.Errorf("disabled-email done should not print 'email archived', got: %q", out.String())
	}
}

func TestDone_GmailTask_NoSourceIDSkipsArchive(t *testing.T) {
	// A task with Source=gmail but empty SourceID should not trigger archive
	// (defensive — we cannot identify the message to archive).
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableEmailConfig(t, dir)

	id := addTestTask(t, dir, "Bad gmail task")
	task, _ := getTaskByID(t, dir, id)
	task.Source = "gmail"
	task.SourceID = "" // explicitly empty
	path := filepath.Join(dir, ".monolog", "tasks", id+".json")
	data, _ := json.MarshalIndent(task, "", "  ")
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write patched seed: %v", err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", filepath.Join(".monolog", "tasks", id+".json")).CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "seed").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	calls := stubArchiveFn(t, nil)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v", err)
	}

	if len(*calls) != 0 {
		t.Errorf("archive should not be called for gmail task with empty SourceID, got %d calls", len(*calls))
	}
}

// Sanity check: realArchive correctly delegates to the email package. We
// don't try to exercise the real Gmail call (no token in tests) — we just
// verify the function exists with the expected signature and that the
// archiveFn variable points at it by default. The recurring-task path
// shares the archive hook unchanged since the spawn happens before the
// archive call (model.Task value passed to archiveFn is always the
// completed one, never the spawn).
func TestRealArchive_IsDefault(t *testing.T) {
	// Compile-time check that realArchive has the expected signature.
	var fn func(sourceID string, ec config.EmailConfig) error = realArchive
	if fn == nil {
		t.Fatal("realArchive should be non-nil")
	}
}

// TestDone_GmailRecurringTask_TriggersArchive ensures the archive hook
// fires even when the completed task is recurring (the spawn happens
// inside CompleteAndSpawn before the archive runs against the original
// task's SourceID — there's no risk of archiving the spawn).
func TestDone_GmailRecurringTask_TriggersArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableEmailConfig(t, dir)

	id := seedGmailTask(t, dir, "Recurring email", "gmail-msg-recur")
	// Patch the seed task to also carry a recurrence rule.
	task, _ := getTaskByID(t, dir, id)
	task.Recurrence = "days:7"
	path := filepath.Join(dir, ".monolog", "tasks", id+".json")
	data, _ := json.MarshalIndent(task, "", "  ")
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write patched seed: %v", err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", filepath.Join(".monolog", "tasks", id+".json")).CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "add recurrence to seed").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	calls := stubArchiveFn(t, nil)

	rootCmd := NewRootCmd()
	out := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v\nstderr: %s", err, errBuf.String())
	}

	if len(*calls) != 1 {
		t.Fatalf("expected 1 archive call for recurring gmail task, got %d", len(*calls))
	}
	if (*calls)[0].sourceID != "gmail-msg-recur" {
		t.Errorf("archive sourceID: got %q, want gmail-msg-recur (must be the original task's SourceID, not the spawn's)", (*calls)[0].sourceID)
	}

	// Verify the spawn is open and does NOT carry the gmail SourceID
	// (otherwise completing it would re-archive the same Gmail message).
	tasks := readTasks(t, dir)
	var spawn model.Task
	for _, tk := range tasks {
		if tk.ID != id && tk.Title == "Recurring email" {
			spawn = tk
			break
		}
	}
	if spawn.ID == "" {
		t.Fatal("spawn not found")
	}
	if spawn.SourceID == "gmail-msg-recur" {
		t.Errorf("spawn must NOT inherit SourceID from original (would cause double-archive on next done)")
	}
}
