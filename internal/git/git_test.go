package git

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maksmas/monolog/internal/model"
)

func TestInit_CreatesDirectoryStructure(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	err := Init(repoPath, "")
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Check .git directory exists
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); os.IsNotExist(err) {
		t.Error(".git directory should exist after init")
	}

	// Check .monolog/tasks/ directory exists
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	if _, err := os.Stat(tasksDir); os.IsNotExist(err) {
		t.Error(".monolog/tasks/ directory should exist after init")
	}

	// Check .monolog/config.json exists with expected content
	configPath := filepath.Join(repoPath, ".monolog", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config.json: %v", err)
	}

	// map[string]any rather than map[string]string: "auto_push" is a bool.
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("config.json is not valid JSON: %v", err)
	}
	if config["default_schedule"] != "today" {
		t.Errorf("default_schedule = %q, want %q", config["default_schedule"], "today")
	}
	if config["editor"] != "$EDITOR" {
		t.Errorf("editor = %q, want %q", config["editor"], "$EDITOR")
	}
	if config["theme"] != "default" {
		t.Errorf("theme = %q, want %q", config["theme"], "default")
	}
	if config["date_format"] != "02-01-2006" {
		t.Errorf("date_format = %q, want %q", config["date_format"], "02-01-2006")
	}
	if config["auto_push"] != true {
		t.Errorf("auto_push = %v, want true", config["auto_push"])
	}

	// Check .gitignore exists
	gitignorePath := filepath.Join(repoPath, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		t.Error(".gitignore should exist after init")
	}
}

func TestInit_CreatesInitialCommit(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	err := Init(repoPath, "")
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Check there is at least one commit
	cmd := exec.Command("git", "-C", repoPath, "log", "--oneline")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if len(out) == 0 {
		t.Error("expected at least one commit after init")
	}
}

func TestInit_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	// First init should succeed
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("first Init() error = %v", err)
	}

	// Second init should fail
	err := Init(repoPath, "")
	if err == nil {
		t.Error("expected error when init on already-initialized repo")
	}
}

func TestInit_MonologDirExistsButNoGit(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	// Pre-create .monolog directory without git
	if err := os.MkdirAll(filepath.Join(repoPath, ".monolog"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Init should succeed since there's no .git
	err := Init(repoPath, "")
	if err != nil {
		t.Fatalf("Init() should succeed when .monolog exists but .git does not: %v", err)
	}

	// Verify git was initialized
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); os.IsNotExist(err) {
		t.Error(".git directory should exist after init")
	}
}

func TestInit_WithRemote(t *testing.T) {
	// Create a bare repo to act as the remote
	remoteDir := t.TempDir()
	bareRepo := filepath.Join(remoteDir, "remote.git")
	cmd := exec.Command("git", "init", "--bare", bareRepo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create bare repo: %v\n%s", err, out)
	}

	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	err := Init(repoPath, bareRepo)
	if err != nil {
		t.Fatalf("Init() with remote error = %v", err)
	}

	// Check remote is configured
	cmd = exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git remote get-url failed: %v", err)
	}
	if got := string(out[:len(out)-1]); got != bareRepo {
		t.Errorf("remote url = %q, want %q", got, bareRepo)
	}

	// Check that push succeeded by checking remote has a branch
	cmd = exec.Command("git", "-C", bareRepo, "branch")
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("git branch on bare repo failed: %v", err)
	}
	if len(out) == 0 {
		t.Error("remote should have at least one branch after init with push")
	}
}

func TestAutoCommit(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	// Initialize repo first
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Create a file to commit
	testFile := filepath.Join(repoPath, ".monolog", "tasks", "test.json")
	if err := os.WriteFile(testFile, []byte(`{"id":"test"}`), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// Auto-commit it
	relPath := filepath.Join(".monolog", "tasks", "test.json")
	err := AutoCommit(repoPath, "add: test task", relPath)
	if err != nil {
		t.Fatalf("AutoCommit() error = %v", err)
	}

	// Verify the commit exists
	cmd := exec.Command("git", "-C", repoPath, "log", "--oneline")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if got := string(out); !strings.Contains(got, "add: test task") {
		t.Errorf("commit log should contain 'add: test task', got: %s", got)
	}
}

func TestAutoCommit_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Create two files
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	if err := os.WriteFile(filepath.Join(tasksDir, "a.json"), []byte(`{"id":"a"}`), 0o644); err != nil {
		t.Fatalf("write file a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "b.json"), []byte(`{"id":"b"}`), 0o644); err != nil {
		t.Fatalf("write file b: %v", err)
	}

	// Auto-commit both
	err := AutoCommit(repoPath, "add: two tasks",
		filepath.Join(".monolog", "tasks", "a.json"),
		filepath.Join(".monolog", "tasks", "b.json"),
	)
	if err != nil {
		t.Fatalf("AutoCommit() error = %v", err)
	}

	// Verify commit
	cmd := exec.Command("git", "-C", repoPath, "log", "--oneline")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if got := string(out); !strings.Contains(got, "add: two tasks") {
		t.Errorf("commit log should contain 'add: two tasks', got: %s", got)
	}
}

func TestHasChanges_NoChanges(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	has, err := HasChanges(repoPath)
	if err != nil {
		t.Fatalf("HasChanges() error = %v", err)
	}
	if has {
		t.Error("expected no changes in clean repo")
	}
}

func TestHasChanges_WithChanges(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Create an untracked file
	if err := os.WriteFile(filepath.Join(repoPath, "new.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	has, err := HasChanges(repoPath)
	if err != nil {
		t.Fatalf("HasChanges() error = %v", err)
	}
	if !has {
		t.Error("expected changes after adding untracked file")
	}
}

func TestHasRemote_NoRemote(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	has, err := HasRemote(repoPath)
	if err != nil {
		t.Fatalf("HasRemote() error = %v", err)
	}
	if has {
		t.Error("expected no remote in repo without remote")
	}
}

func TestHasRemote_WithRemote(t *testing.T) {
	remoteDir := t.TempDir()
	bareRepo := filepath.Join(remoteDir, "remote.git")
	cmd := exec.Command("git", "init", "--bare", bareRepo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create bare repo: %v\n%s", err, out)
	}

	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	if err := Init(repoPath, bareRepo); err != nil {
		t.Fatalf("Init() with remote error = %v", err)
	}

	has, err := HasRemote(repoPath)
	if err != nil {
		t.Fatalf("HasRemote() error = %v", err)
	}
	if !has {
		t.Error("expected remote to exist in repo initialized with remote")
	}
}

func TestSyncCommit(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Create a file
	if err := os.WriteFile(filepath.Join(repoPath, ".monolog", "tasks", "sync.json"), []byte(`{"id":"sync"}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := SyncCommit(repoPath); err != nil {
		t.Fatalf("SyncCommit() error = %v", err)
	}

	// Working tree should be clean after sync commit
	has, err := HasChanges(repoPath)
	if err != nil {
		t.Fatalf("HasChanges() error = %v", err)
	}
	if has {
		t.Error("expected clean working tree after SyncCommit")
	}

	// Verify commit message
	out, err := exec.Command("git", "-C", repoPath, "log", "--oneline", "-1").Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(string(out), "sync") {
		t.Errorf("expected commit message to contain 'sync', got: %s", string(out))
	}
}

// writeTaskJSON writes a task as JSON to the given absolute path.
func writeTaskJSON(t *testing.T, path string, task model.Task) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// readTaskJSON reads a task from an absolute path.
func readTaskJSON(t *testing.T, path string) model.Task {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var task model.Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return task
}

// gitRun runs a git command in the repo and fails the test on error.
func gitRun(t *testing.T, repoPath string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// baseBranch returns the current branch name.
func baseBranch(t *testing.T, repoPath string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repoPath, "branch", "--show-current").Output()
	if err != nil {
		t.Fatalf("get branch: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// setupConflictRepo creates a monolog repo with an initial task on the base
// branch, then returns the repo path, the base branch name, and the
// repo-relative path to the task file. Callers create conflicts by diverging
// from this state.
func setupConflictRepo(t *testing.T, taskID string) (repoPath, branch, taskPath string) {
	t.Helper()
	dir := t.TempDir()
	repoPath = filepath.Join(dir, "repo")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	branch = baseBranch(t, repoPath)

	taskPath = filepath.Join(".monolog", "tasks", taskID+".json")
	abs := filepath.Join(repoPath, taskPath)
	writeTaskJSON(t, abs, model.Task{
		ID: taskID, Title: "base", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-10T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, repoPath, "add", taskPath)
	gitRun(t, repoPath, "commit", "-m", "base")
	return
}

func TestResolveConflicts_PicksLaterUpdatedAt(t *testing.T) {
	repoPath, branch, taskPath := setupConflictRepo(t, "01A")
	abs := filepath.Join(repoPath, taskPath)

	// Feature branch with LATER UpdatedAt.
	gitRun(t, repoPath, "checkout", "-b", "feature")
	writeTaskJSON(t, abs, model.Task{
		ID: "01A", Title: "from feature", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, repoPath, "add", taskPath)
	gitRun(t, repoPath, "commit", "-m", "feature change")

	// Base branch with EARLIER UpdatedAt.
	gitRun(t, repoPath, "checkout", branch)
	writeTaskJSON(t, abs, model.Task{
		ID: "01A", Title: "from main", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, repoPath, "add", taskPath)
	gitRun(t, repoPath, "commit", "-m", "main change")

	// Merge -> expected to fail with conflict.
	mergeCmd := exec.Command("git", "-C", repoPath, "merge", "--no-edit", "feature")
	if err := mergeCmd.Run(); err == nil {
		t.Fatalf("expected merge to conflict")
	}

	n, err := ResolveConflicts(repoPath)
	if err != nil {
		t.Fatalf("ResolveConflicts: %v", err)
	}
	if n != 1 {
		t.Errorf("resolved count = %d, want 1", n)
	}

	got := readTaskJSON(t, abs)
	if got.UpdatedAt != "2026-04-12T00:00:00Z" {
		t.Errorf("winner UpdatedAt = %q, want feature's %q", got.UpdatedAt, "2026-04-12T00:00:00Z")
	}
	if got.Title != "from feature" {
		t.Errorf("winner Title = %q, want %q", got.Title, "from feature")
	}
}

func TestResolveConflicts_TieBreaksToOurs(t *testing.T) {
	repoPath, branch, taskPath := setupConflictRepo(t, "01T")
	abs := filepath.Join(repoPath, taskPath)

	// Both sides share the SAME UpdatedAt but different Title.
	gitRun(t, repoPath, "checkout", "-b", "feature")
	writeTaskJSON(t, abs, model.Task{
		ID: "01T", Title: "from feature", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, repoPath, "add", taskPath)
	gitRun(t, repoPath, "commit", "-m", "feature change")

	gitRun(t, repoPath, "checkout", branch)
	writeTaskJSON(t, abs, model.Task{
		ID: "01T", Title: "from main", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, repoPath, "add", taskPath)
	gitRun(t, repoPath, "commit", "-m", "main change")

	mergeCmd := exec.Command("git", "-C", repoPath, "merge", "--no-edit", "feature")
	if err := mergeCmd.Run(); err == nil {
		t.Fatalf("expected merge to conflict")
	}

	n, err := ResolveConflicts(repoPath)
	if err != nil {
		t.Fatalf("ResolveConflicts: %v", err)
	}
	if n != 1 {
		t.Errorf("resolved count = %d, want 1", n)
	}
	got := readTaskJSON(t, abs)
	if got.Title != "from main" {
		t.Errorf("tie should pick ours; got Title = %q, want %q", got.Title, "from main")
	}
}

func TestResolveConflicts_DeleteVsModify_ModifyWins(t *testing.T) {
	repoPath, branch, taskPath := setupConflictRepo(t, "01D")
	abs := filepath.Join(repoPath, taskPath)

	// Feature branch: modify.
	gitRun(t, repoPath, "checkout", "-b", "feature")
	writeTaskJSON(t, abs, model.Task{
		ID: "01D", Title: "modified on feature", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, repoPath, "add", taskPath)
	gitRun(t, repoPath, "commit", "-m", "modify on feature")

	// Base: delete.
	gitRun(t, repoPath, "checkout", branch)
	if err := os.Remove(abs); err != nil {
		t.Fatalf("remove: %v", err)
	}
	gitRun(t, repoPath, "add", taskPath)
	gitRun(t, repoPath, "commit", "-m", "delete on main")

	mergeCmd := exec.Command("git", "-C", repoPath, "merge", "--no-edit", "feature")
	if err := mergeCmd.Run(); err == nil {
		t.Fatalf("expected merge to conflict (modify/delete)")
	}

	n, err := ResolveConflicts(repoPath)
	if err != nil {
		t.Fatalf("ResolveConflicts: %v", err)
	}
	if n != 1 {
		t.Errorf("resolved count = %d, want 1", n)
	}
	got := readTaskJSON(t, abs)
	if got.Title != "modified on feature" {
		t.Errorf("modify should win over delete; got Title = %q", got.Title)
	}
}

func TestResolveConflicts_NonTaskPathErrors(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	branch := baseBranch(t, repoPath)

	// Create a non-task file at repo root, committed on both sides differently.
	nonTask := "notes.txt"
	abs := filepath.Join(repoPath, nonTask)
	if err := os.WriteFile(abs, []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, repoPath, "add", nonTask)
	gitRun(t, repoPath, "commit", "-m", "base")

	gitRun(t, repoPath, "checkout", "-b", "feature")
	if err := os.WriteFile(abs, []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, repoPath, "add", nonTask)
	gitRun(t, repoPath, "commit", "-m", "feature")

	gitRun(t, repoPath, "checkout", branch)
	if err := os.WriteFile(abs, []byte("main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, repoPath, "add", nonTask)
	gitRun(t, repoPath, "commit", "-m", "main")

	mergeCmd := exec.Command("git", "-C", repoPath, "merge", "--no-edit", "feature")
	if err := mergeCmd.Run(); err == nil {
		t.Fatalf("expected merge to conflict")
	}

	if _, err := ResolveConflicts(repoPath); err == nil {
		t.Error("expected error for unmerged non-task file")
	}
}

func TestResolveConflicts_NoConflicts(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	n, err := ResolveConflicts(repoPath)
	if err != nil {
		t.Fatalf("ResolveConflicts on clean repo: %v", err)
	}
	if n != 0 {
		t.Errorf("resolved = %d, want 0 on clean repo", n)
	}
}

func TestIsRebasing_False(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	rebasing, err := IsRebasing(repoPath)
	if err != nil {
		t.Fatalf("IsRebasing: %v", err)
	}
	if rebasing {
		t.Error("clean repo should not be rebasing")
	}
}

func TestHeadSHA_Success(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	sha, err := headSHA(repoPath)
	if err != nil {
		t.Fatalf("headSHA() error = %v", err)
	}
	if sha == "" {
		t.Error("headSHA() returned empty string")
	}
	// SHA must be a valid hex string (40 chars for full SHA)
	if len(sha) < 7 {
		t.Errorf("headSHA() = %q, expected at least 7 chars", sha)
	}
	// Verify it matches the actual HEAD
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	want := strings.TrimSpace(string(out))
	if sha != want {
		t.Errorf("headSHA() = %q, want %q", sha, want)
	}
}

func TestHeadSHA_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	_, err := headSHA(dir)
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

func TestAutoCommitSHA_ReturnsCorrectSHA(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Create a file to commit
	testFile := filepath.Join(repoPath, ".monolog", "tasks", "sha-test.json")
	if err := os.WriteFile(testFile, []byte(`{"id":"sha-test"}`), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	relPath := filepath.Join(".monolog", "tasks", "sha-test.json")
	sha, err := AutoCommitSHA(repoPath, "add: sha test task", relPath)
	if err != nil {
		t.Fatalf("AutoCommitSHA() error = %v", err)
	}
	if sha == "" {
		t.Error("AutoCommitSHA() returned empty SHA")
	}

	// Verify returned SHA matches git rev-parse HEAD
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	want := strings.TrimSpace(string(out))
	if sha != want {
		t.Errorf("AutoCommitSHA() = %q, want HEAD %q", sha, want)
	}
}

func TestCommitSubject_ReturnsCorrectSubject(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Create a file and commit it with a known message
	testFile := filepath.Join(repoPath, ".monolog", "tasks", "subj-test.json")
	if err := os.WriteFile(testFile, []byte(`{"id":"subj-test"}`), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	relPath := filepath.Join(".monolog", "tasks", "subj-test.json")
	sha, err := AutoCommitSHA(repoPath, "add: subject test task", relPath)
	if err != nil {
		t.Fatalf("AutoCommitSHA() error = %v", err)
	}

	subject, err := CommitSubject(repoPath, sha)
	if err != nil {
		t.Fatalf("CommitSubject() error = %v", err)
	}
	if subject != "add: subject test task" {
		t.Errorf("CommitSubject() = %q, want %q", subject, "add: subject test task")
	}
}

func TestCommitSubject_BadSHA(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	_, err := CommitSubject(repoPath, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err == nil {
		t.Error("expected error for bad SHA")
	}
}

func TestRevert_RevertsFileChange(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Create a file and commit it
	testFile := filepath.Join(repoPath, ".monolog", "tasks", "revert-test.json")
	if err := os.WriteFile(testFile, []byte(`{"id":"revert-test","title":"original"}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	relPath := filepath.Join(".monolog", "tasks", "revert-test.json")
	if _, err := AutoCommitSHA(repoPath, "add: revert test task", relPath); err != nil {
		t.Fatalf("AutoCommitSHA() error = %v", err)
	}

	// Modify the file and commit — this is the SHA we will revert
	if err := os.WriteFile(testFile, []byte(`{"id":"revert-test","title":"modified"}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	sha, err := AutoCommitSHA(repoPath, "edit: revert test task", relPath)
	if err != nil {
		t.Fatalf("AutoCommitSHA() for modify: %v", err)
	}

	// Revert the modify commit
	if err := Revert(repoPath, sha); err != nil {
		t.Fatalf("Revert() error = %v", err)
	}

	// File should now contain the original content
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read file after revert: %v", err)
	}
	if string(data) != `{"id":"revert-test","title":"original"}` {
		t.Errorf("file content after revert = %q, want original content", string(data))
	}

	// Latest commit message should start with "Revert"
	subj, err := CommitSubject(repoPath, "HEAD")
	if err != nil {
		t.Fatalf("CommitSubject after revert: %v", err)
	}
	if !strings.HasPrefix(subj, "Revert") {
		t.Errorf("revert commit subject = %q, expected to start with 'Revert'", subj)
	}
}

func TestRevert_BadSHA(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	err := Revert(repoPath, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err == nil {
		t.Error("expected error when reverting a bad SHA")
	}
}

func TestRevertSHA_ReturnsRevertCommitSHA(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Create a file and commit it — this is the baseline
	testFile := filepath.Join(repoPath, ".monolog", "tasks", "revertsha-test.json")
	if err := os.WriteFile(testFile, []byte(`{"id":"revertsha-test","title":"original"}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	relPath := filepath.Join(".monolog", "tasks", "revertsha-test.json")
	if _, err := AutoCommitSHA(repoPath, "add: revertsha test task", relPath); err != nil {
		t.Fatalf("AutoCommitSHA() error = %v", err)
	}

	// Modify and commit — this is the SHA we will revert
	if err := os.WriteFile(testFile, []byte(`{"id":"revertsha-test","title":"modified"}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	sha, err := AutoCommitSHA(repoPath, "edit: revertsha test task", relPath)
	if err != nil {
		t.Fatalf("AutoCommitSHA() for modify: %v", err)
	}

	// RevertSHA should return the SHA of the new revert commit
	revertSHA, err := RevertSHA(repoPath, sha)
	if err != nil {
		t.Fatalf("RevertSHA() error = %v", err)
	}
	if revertSHA == "" {
		t.Fatal("RevertSHA() returned empty SHA")
	}
	if revertSHA == sha {
		t.Errorf("RevertSHA() returned same SHA as the reverted one: %q", revertSHA)
	}

	// Returned SHA must match current HEAD
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	want := strings.TrimSpace(string(out))
	if revertSHA != want {
		t.Errorf("RevertSHA() = %q, want HEAD %q", revertSHA, want)
	}

	// The returned SHA's commit subject should start with "Revert"
	subj, err := CommitSubject(repoPath, revertSHA)
	if err != nil {
		t.Fatalf("CommitSubject after RevertSHA: %v", err)
	}
	if !strings.HasPrefix(subj, "Revert") {
		t.Errorf("revert commit subject = %q, expected to start with 'Revert'", subj)
	}

	// File should be back to original content
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read file after revert: %v", err)
	}
	if string(data) != `{"id":"revertsha-test","title":"original"}` {
		t.Errorf("file content after revert = %q, want original content", string(data))
	}
}

func TestRevertSHA_BadSHA(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	sha, err := RevertSHA(repoPath, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err == nil {
		t.Error("expected error when reverting a bad SHA")
	}
	if sha != "" {
		t.Errorf("RevertSHA() returned %q for bad SHA, want empty", sha)
	}
}

func TestAutoCommit_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")

	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Create and commit a file first
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	testFile := filepath.Join(tasksDir, "del.json")
	if err := os.WriteFile(testFile, []byte(`{"id":"del"}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	relPath := filepath.Join(".monolog", "tasks", "del.json")
	if err := AutoCommit(repoPath, "add: del task", relPath); err != nil {
		t.Fatalf("AutoCommit() error = %v", err)
	}

	// Delete the file
	os.Remove(testFile)

	// Auto-commit the deletion
	err := AutoCommit(repoPath, "rm: del task", relPath)
	if err != nil {
		t.Fatalf("AutoCommit() error = %v", err)
	}

	// Verify the delete commit exists
	cmd := exec.Command("git", "-C", repoPath, "log", "--oneline")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if got := string(out); !strings.Contains(got, "rm: del task") {
		t.Errorf("commit log should contain 'rm: del task', got: %s", got)
	}
}

func TestRunOut_Success(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	out, err := runOut(context.Background(), repoPath, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("runOut() error = %v (out=%q)", err, out)
	}
	want, err := headSHA(repoPath)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}
	if strings.TrimSpace(out) != want {
		t.Errorf("runOut() output = %q, want %q", strings.TrimSpace(out), want)
	}
}

func TestRunOut_FailureReturnsOutputAndError(t *testing.T) {
	dir := t.TempDir()

	out, err := runOut(context.Background(), dir, "git", "rev-parse", "HEAD")
	if err == nil {
		t.Fatal("expected error running git rev-parse outside a repo")
	}
	// The output must come back alongside the error so callers can classify.
	if strings.TrimSpace(out) == "" {
		t.Error("expected git's stderr in the returned output, got empty string")
	}
	if !strings.Contains(err.Error(), "git [rev-parse HEAD]") {
		t.Errorf("error should name the command, got: %v", err)
	}
}

func TestRunOut_ExpiredContext(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already expired before the command starts

	if _, err := runOut(ctx, repoPath, "git", "rev-parse", "HEAD"); err == nil {
		t.Error("expected error for an expired context")
	}
}

// setupRemoteFixture creates a bare remote plus a monolog clone whose current
// branch tracks it, returning the bare repo path and the clone path. The bare
// repo's HEAD is pointed at the pushed branch so further clones check it out.
func setupRemoteFixture(t *testing.T) (bare, clone string) {
	t.Helper()
	dir := t.TempDir()
	bare = filepath.Join(dir, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	clone = filepath.Join(dir, "clone-a")
	if err := Init(clone, bare); err != nil {
		t.Fatalf("Init with remote: %v", err)
	}
	gitRun(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+baseBranch(t, clone))
	return bare, clone
}

// cloneOf clones the bare repo into a sibling directory of it named name.
func cloneOf(t *testing.T, bare, name string) string {
	t.Helper()
	dst := filepath.Join(filepath.Dir(bare), name)
	if out, err := exec.Command("git", "clone", bare, dst).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	return dst
}

// pushTask writes a task file in the given clone, commits and pushes it,
// returning the repo-relative task path.
func pushTask(t *testing.T, repoPath string, task model.Task) string {
	t.Helper()
	taskPath := filepath.Join(".monolog", "tasks", task.ID+".json")
	writeTaskJSON(t, filepath.Join(repoPath, taskPath), task)
	gitRun(t, repoPath, "add", taskPath)
	gitRun(t, repoPath, "commit", "-m", "add "+task.ID)
	gitRun(t, repoPath, "push")
	return taskPath
}

func TestPullRebaseResolving_CleanFastForward(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	taskPath := pushTask(t, b, model.Task{
		ID: "01FF", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	n, err := pullRebaseResolving(a, false)
	if err != nil {
		t.Fatalf("pullRebaseResolving() error = %v", err)
	}
	if n != 0 {
		t.Errorf("resolved = %d, want 0 on a clean fast-forward", n)
	}
	if _, err := os.Stat(filepath.Join(a, taskPath)); err != nil {
		t.Errorf("B's task should be present in A after the pull: %v", err)
	}
}

func TestPullRebaseResolving_AutoResolvesConflict(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// B pushes the task with an EARLIER UpdatedAt.
	taskPath := pushTask(t, b, model.Task{
		ID: "01CF", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// A commits the same task locally with a LATER UpdatedAt (add/add conflict).
	absA := filepath.Join(a, taskPath)
	writeTaskJSON(t, absA, model.Task{
		ID: "01CF", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, a, "add", taskPath)
	gitRun(t, a, "commit", "-m", "A edit")

	n, err := pullRebaseResolving(a, false)
	if err != nil {
		t.Fatalf("pullRebaseResolving() error = %v", err)
	}
	if n != 1 {
		t.Errorf("resolved = %d, want 1", n)
	}
	if got := readTaskJSON(t, absA); got.Title != "from A" {
		t.Errorf("later UpdatedAt should win; got Title = %q", got.Title)
	}
	rebasing, err := IsRebasing(a)
	if err != nil {
		t.Fatalf("IsRebasing: %v", err)
	}
	if rebasing {
		t.Error("repo should not be mid-rebase after a resolved pull")
	}
}

func TestPullRebaseResolving_AutostashPreservesDirtyFile(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// Something to rebase onto, so the pull actually runs a rebase.
	pushTask(t, b, model.Task{
		ID: "01AS", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// A has an uncommitted modification to a TRACKED file (mirrors the TUI
	// writing .monolog/config.json via config.Save without committing it).
	dirty := filepath.Join(a, ".monolog", "config.json")
	const dirtyContent = "{\n  \"theme\": \"dracula\"\n}\n"
	if err := os.WriteFile(dirty, []byte(dirtyContent), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	// Without autostash, git refuses to rebase over unstaged changes.
	if _, err := pullRebaseResolving(a, false); err == nil {
		t.Fatal("expected pullRebaseResolving(autostash=false) to fail with a dirty tracked file")
	}

	// With autostash it succeeds and the modification is restored afterwards.
	n, err := pullRebaseResolving(a, true)
	if err != nil {
		t.Fatalf("pullRebaseResolving(autostash=true) error = %v", err)
	}
	if n != 0 {
		t.Errorf("resolved = %d, want 0", n)
	}
	data, err := os.ReadFile(dirty)
	if err != nil {
		t.Fatalf("read dirty file after autostash: %v", err)
	}
	if string(data) != dirtyContent {
		t.Errorf("autostash should restore the modification; got %q, want %q", string(data), dirtyContent)
	}
	has, err := HasChanges(a)
	if err != nil {
		t.Fatalf("HasChanges: %v", err)
	}
	if !has {
		t.Error("dirty file should still be uncommitted after autostash")
	}
}

func TestSync_PullsAndPushesThroughSharedRebasePath(t *testing.T) {
	// Guards the Sync refactor onto pullRebaseResolving: a diverged remote is
	// rebased in and the local commit still reaches the remote.
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	bTask := pushTask(t, b, model.Task{
		ID: "01SY", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// A creates a DIFFERENT task, uncommitted, then syncs.
	aTask := filepath.Join(".monolog", "tasks", "01SZ.json")
	writeTaskJSON(t, filepath.Join(a, aTask), model.Task{
		ID: "01SZ", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	res, err := Sync(a)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !res.Committed {
		t.Error("Sync should have committed the pending change")
	}
	if !res.HasRemote {
		t.Error("Sync should report HasRemote for a repo with an origin")
	}
	if res.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0 (no conflicting file)", res.Resolved)
	}
	if _, err := os.Stat(filepath.Join(a, bTask)); err != nil {
		t.Errorf("B's task should be present in A after Sync: %v", err)
	}

	// A's commit must be on the remote now.
	out, err := exec.Command("git", "-C", bare, "log", "--oneline").Output()
	if err != nil {
		t.Fatalf("git log on bare: %v", err)
	}
	if !strings.Contains(string(out), "sync") {
		t.Errorf("remote should contain A's sync commit, got: %s", out)
	}
}

func TestAutoCommitSHA_ConcurrentCallsSerialize(t *testing.T) {
	// Without repoMu these goroutines race on .git/index.lock, which surfaces
	// as "Unable to create '.../index.lock': File exists".
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	const n = 8
	type result struct {
		sha string
		err error
	}
	results := make(chan result, n)
	var start sync.WaitGroup
	start.Add(1)
	// Files are written from the test goroutine (writeTaskJSON calls t.Fatalf,
	// which is only valid there). Untracked files are invisible to another
	// caller's `git add <path>` + `git commit`, so each commit still carries
	// exactly its own file.
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("01CO%04d", i)
		relPath := filepath.Join(".monolog", "tasks", id+".json")
		writeTaskJSON(t, filepath.Join(repoPath, relPath), model.Task{
			ID: id, Title: "concurrent " + id, Status: "open", Schedule: "today",
			UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
		})
		go func() {
			start.Wait()
			sha, err := AutoCommitSHA(repoPath, "add: "+id, relPath)
			results <- result{sha: sha, err: err}
		}()
	}
	start.Done()

	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("AutoCommitSHA() error = %v", r.err)
		}
		if r.sha == "" {
			t.Fatal("AutoCommitSHA() returned an empty SHA")
		}
		if seen[r.sha] {
			t.Errorf("duplicate SHA %q: a concurrent commit was attributed to the wrong caller", r.sha)
		}
		seen[r.sha] = true
	}

	// One commit per call, on top of Init's initial commit.
	out, err := exec.Command("git", "-C", repoPath, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-list: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != fmt.Sprint(n+1) {
		t.Errorf("commit count = %s, want %d", got, n+1)
	}
	// Nothing may be left uncommitted: each call must have staged and committed
	// exactly its own file.
	has, err := HasChanges(repoPath)
	if err != nil {
		t.Fatalf("HasChanges: %v", err)
	}
	if has {
		t.Error("working tree should be clean after all concurrent commits")
	}
}

func TestAutoCommitSHA_ConcurrentWithSync(t *testing.T) {
	// The mutex must keep a mutation's commit from interleaving with Sync's
	// commit/rebase/push. Both calls have to complete without a git-level
	// failure (notably no .git/index.lock contention).
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// Give Sync real remote work: a diverged commit to rebase onto.
	bTask := pushTask(t, b, model.Task{
		ID: "01CS", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	syncErr := make(chan error, 1)
	go func() {
		_, err := Sync(a)
		syncErr <- err
	}()

	// A concurrent Sync commits everything it finds (`git add -A`), so it may
	// legitimately absorb the pending write and leave AutoCommitSHA with
	// nothing to commit. That is a pre-existing interaction between the two
	// entry points, not lock corruption, so retry with a fresh task until the
	// commit lands. What repoMu must guarantee is that neither call ever fails
	// on .git/index.lock or commits onto a detached rebase HEAD.
	var (
		sha     string
		lastErr error
	)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("01CT%04d", i)
		relPath := filepath.Join(".monolog", "tasks", id+".json")
		writeTaskJSON(t, filepath.Join(a, relPath), model.Task{
			ID: id, Title: "from A", Status: "open", Schedule: "today",
			UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
		})
		sha, lastErr = AutoCommitSHA(a, "add: "+id, relPath)
		if lastErr == nil {
			break
		}
		if strings.Contains(lastErr.Error(), "index.lock") {
			t.Fatalf("AutoCommitSHA raced Sync on the git index: %v", lastErr)
		}
	}
	if lastErr != nil {
		t.Fatalf("AutoCommitSHA() error = %v", lastErr)
	}
	if sha == "" {
		t.Fatal("AutoCommitSHA() returned an empty SHA")
	}

	if err := <-syncErr; err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	// The repo must be left in a sane, non-rebasing state with B's task pulled in.
	rebasing, err := IsRebasing(a)
	if err != nil {
		t.Fatalf("IsRebasing: %v", err)
	}
	if rebasing {
		t.Error("repo should not be mid-rebase after concurrent commit + sync")
	}
	if _, err := os.Stat(filepath.Join(a, bTask)); err != nil {
		t.Errorf("B's task should be present in A after Sync: %v", err)
	}
	if _, err := CommitSubject(a, sha); err != nil {
		t.Errorf("the committed SHA should still resolve: %v", err)
	}
}
