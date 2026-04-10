package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncCommand_WithChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add a task (creates a commit), then create an untracked file to sync
	_ = addTestTask(t, dir, "Task to sync")

	// Create an uncommitted file to simulate pending changes
	extraFile := filepath.Join(dir, ".monolog", "tasks", "extra.json")
	if err := os.WriteFile(extraFile, []byte(`{"id":"extra"}`), 0o644); err != nil {
		t.Fatalf("write extra file: %v", err)
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"sync"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("sync command error = %v\noutput: %s", err, buf.String())
	}

	// Verify that the extra file was committed
	gitCmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected clean working tree after sync, got: %s", string(out))
	}

	// Verify a "sync" commit was made
	gitCmd2 := exec.Command("git", "-C", dir, "log", "--oneline", "-1")
	out2, err := gitCmd2.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(string(out2), "sync") {
		t.Errorf("expected last commit message to contain 'sync', got: %s", string(out2))
	}
}

func TestSyncCommand_NothingToCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Count commits before sync
	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline")
	outBefore, _ := gitCmd.Output()
	commitsBefore := len(strings.Split(strings.TrimSpace(string(outBefore)), "\n"))

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"sync"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("sync command error = %v\noutput: %s", err, buf.String())
	}

	// Count commits after sync — should be the same (no new commit)
	gitCmd2 := exec.Command("git", "-C", dir, "log", "--oneline")
	outAfter, _ := gitCmd2.Output()
	commitsAfter := len(strings.Split(strings.TrimSpace(string(outAfter)), "\n"))

	if commitsAfter != commitsBefore {
		t.Errorf("expected no new commits when nothing to sync, before=%d after=%d", commitsBefore, commitsAfter)
	}
}

func TestSyncCommand_NoRemoteWarning(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// No remote is configured in the test repo

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"sync"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("sync command should not error with no remote, got: %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "no remote configured") {
		t.Errorf("expected 'no remote configured' warning, got: %s", output)
	}
}

func TestSyncCommand_NoRemoteStillCommitsLocally(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create an uncommitted file
	extraFile := filepath.Join(dir, ".monolog", "tasks", "local-only.json")
	if err := os.WriteFile(extraFile, []byte(`{"id":"local"}`), 0o644); err != nil {
		t.Fatalf("write extra file: %v", err)
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"sync"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("sync command error = %v\noutput: %s", err, buf.String())
	}

	// Verify the file was committed even without a remote
	gitCmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected clean working tree after sync, got: %s", string(out))
	}

	// Verify the warning was shown
	output := buf.String()
	if !strings.Contains(output, "no remote configured") {
		t.Errorf("expected 'no remote configured' warning, got: %s", output)
	}
}

func TestSyncCommand_WithRemote(t *testing.T) {
	// Set up a bare remote repo
	tmp := t.TempDir()
	remoteDir := filepath.Join(tmp, "remote.git")
	if err := exec.Command("git", "init", "--bare", remoteDir).Run(); err != nil {
		t.Fatalf("init bare repo: %v", err)
	}

	dir := filepath.Join(tmp, "monolog")
	initTestRepo(t, dir)

	// Add a remote
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", remoteDir).Run(); err != nil {
		t.Fatalf("add remote: %v", err)
	}

	// Push initial content so the remote has a branch
	branchCmd := exec.Command("git", "-C", dir, "branch", "--show-current")
	branchOut, err := branchCmd.Output()
	if err != nil {
		t.Fatalf("get branch: %v", err)
	}
	branch := strings.TrimSpace(string(branchOut))
	if err := exec.Command("git", "-C", dir, "push", "-u", "origin", branch).Run(); err != nil {
		t.Fatalf("initial push: %v", err)
	}

	// Create an uncommitted file
	extraFile := filepath.Join(dir, ".monolog", "tasks", "remote-sync.json")
	if err := os.WriteFile(extraFile, []byte(`{"id":"rsync"}`), 0o644); err != nil {
		t.Fatalf("write extra file: %v", err)
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"sync"})

	err = rootCmd.Execute()
	if err != nil {
		t.Fatalf("sync command error = %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	// Should NOT warn about no remote
	if strings.Contains(output, "no remote configured") {
		t.Errorf("should not warn about no remote when remote exists, got: %s", output)
	}

	// Verify the commit was pushed to the remote
	// Clone the remote to check
	cloneDir := filepath.Join(tmp, "clone")
	if err := exec.Command("git", "clone", remoteDir, cloneDir).Run(); err != nil {
		t.Fatalf("clone: %v", err)
	}

	clonedFile := filepath.Join(cloneDir, ".monolog", "tasks", "remote-sync.json")
	if _, err := os.Stat(clonedFile); os.IsNotExist(err) {
		t.Error("expected synced file to exist in remote clone")
	}
}
