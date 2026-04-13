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

func TestSyncCommand_AutoResolvesConflict(t *testing.T) {
	// Two clones share a bare remote. Both edit the same task; the second
	// to sync must auto-resolve by picking the later UpdatedAt.
	tmp := t.TempDir()
	remoteDir := filepath.Join(tmp, "remote.git")
	if err := exec.Command("git", "init", "--bare", remoteDir).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}

	// Clone A: initial setup, creates a task, pushes.
	dirA := filepath.Join(tmp, "a")
	initTestRepo(t, dirA)
	if err := exec.Command("git", "-C", dirA, "remote", "add", "origin", remoteDir).Run(); err != nil {
		t.Fatalf("add remote A: %v", err)
	}
	branchOut, err := exec.Command("git", "-C", dirA, "branch", "--show-current").Output()
	if err != nil {
		t.Fatalf("get branch: %v", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	taskID := "01SHARED"
	taskPath := filepath.Join(".monolog", "tasks", taskID+".json")
	absA := filepath.Join(dirA, taskPath)
	if err := os.WriteFile(absA, []byte(`{"id":"01SHARED","title":"base","status":"open","position":1000,"source":"cli","schedule":"today","created_at":"2026-04-10T00:00:00Z","updated_at":"2026-04-10T00:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write task A: %v", err)
	}
	if err := exec.Command("git", "-C", dirA, "add", taskPath).Run(); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if err := exec.Command("git", "-C", dirA, "commit", "-m", "add task").Run(); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	if err := exec.Command("git", "-C", dirA, "push", "-u", "origin", branch).Run(); err != nil {
		t.Fatalf("initial push A: %v", err)
	}

	// Clone B: clones from remote and modifies the task with an OLDER
	// UpdatedAt, then pushes. (Doesn't need to be the monolog layout since
	// we only use it to produce a diverged commit on the remote.)
	dirB := filepath.Join(tmp, "b")
	if err := exec.Command("git", "clone", remoteDir, dirB).Run(); err != nil {
		t.Fatalf("clone B: %v", err)
	}
	absB := filepath.Join(dirB, taskPath)
	if err := os.WriteFile(absB, []byte(`{"id":"01SHARED","title":"from B","status":"open","position":1000,"source":"cli","schedule":"today","created_at":"2026-04-10T00:00:00Z","updated_at":"2026-04-11T00:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write task B: %v", err)
	}
	if err := exec.Command("git", "-C", dirB, "add", taskPath).Run(); err != nil {
		t.Fatalf("add B: %v", err)
	}
	if err := exec.Command("git", "-C", dirB, "commit", "-m", "B edit").Run(); err != nil {
		t.Fatalf("commit B: %v", err)
	}
	if err := exec.Command("git", "-C", dirB, "push").Run(); err != nil {
		t.Fatalf("push B: %v", err)
	}

	// Now A modifies the same task with a LATER UpdatedAt and runs sync —
	// pull will conflict on rebase; auto-resolver should pick A's version.
	if err := os.WriteFile(absA, []byte(`{"id":"01SHARED","title":"from A","status":"open","position":1000,"source":"cli","schedule":"today","created_at":"2026-04-10T00:00:00Z","updated_at":"2026-04-12T00:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("rewrite task A: %v", err)
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"sync"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("sync should auto-resolve: %v\noutput: %s", err, buf.String())
	}

	output := buf.String()
	if !strings.Contains(output, "auto-resolved") {
		t.Errorf("expected 'auto-resolved' in output, got: %s", output)
	}

	// Verify A's task now has its own UpdatedAt (the later one won).
	data, err := os.ReadFile(absA)
	if err != nil {
		t.Fatalf("read final task: %v", err)
	}
	if !strings.Contains(string(data), `"from A"`) {
		t.Errorf("A's version (later UpdatedAt) should have won, got: %s", string(data))
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
