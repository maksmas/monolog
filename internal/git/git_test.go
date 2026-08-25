package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maksmas/monolog/internal/model"
)

// TestMain isolates every git subprocess this package spawns from the machine's
// own git configuration.
//
// Without it the suite reads ~/.gitconfig, so a developer or CI runner with
// rebase.autoStash = true silently changes what these tests exercise —
// TestPullRebaseResolving_AutostashPreservesDirtyFile asserts that a rebase
// WITHOUT autostash fails on a dirty tracked file, which is exactly the
// assertion that setting flips. Nulling the global/system files means the
// identity has to come from the environment instead, since Init commits.
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_SYSTEM":   os.DevNull,
		"GIT_AUTHOR_NAME":     "monolog test",
		"GIT_AUTHOR_EMAIL":    "test@monolog.invalid",
		"GIT_COMMITTER_NAME":  "monolog test",
		"GIT_COMMITTER_EMAIL": "test@monolog.invalid",
		"GIT_TERMINAL_PROMPT": "0",
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintf(os.Stderr, "setenv %s: %v\n", k, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

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

	out, err := runOut(context.Background(), repoPath, nil, "git", "rev-parse", "HEAD")
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

	out, err := runOut(context.Background(), dir, nil, "git", "rev-parse", "HEAD")
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

	if _, err := runOut(ctx, repoPath, nil, "git", "rev-parse", "HEAD"); err == nil {
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

	res, err := pullRebaseResolving(context.Background(), a, false, nil, upstreamRef{})
	if err != nil {
		t.Fatalf("pullRebaseResolving() error = %v", err)
	}
	if res.Resolved != 0 {
		t.Errorf("resolved = %d, want 0 on a clean fast-forward", res.Resolved)
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

	res, err := pullRebaseResolving(context.Background(), a, false, nil, upstreamRef{})
	if err != nil {
		t.Fatalf("pullRebaseResolving() error = %v", err)
	}
	if res.Resolved != 1 {
		t.Errorf("resolved = %d, want 1", res.Resolved)
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

// TestPullRebaseResolving_RemoteSideWinsConflict covers the mirror image of
// TestPullRebaseResolving_AutoResolvesConflict: the INCOMING version has the
// later UpdatedAt, so ResolveConflicts keeps it and the local commit being
// replayed becomes empty. `git rebase --continue` refuses an empty commit
// ("No changes - did you forget to use 'git add'?"), so the replay has to be
// skipped instead of continued.
func TestPullRebaseResolving_RemoteSideWinsConflict(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// B pushes the task with a LATER UpdatedAt, so B wins the auto-resolution.
	taskPath := pushTask(t, b, model.Task{
		ID: "01RW", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	absA := filepath.Join(a, taskPath)
	writeTaskJSON(t, absA, model.Task{
		ID: "01RW", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, a, "add", taskPath)
	gitRun(t, a, "commit", "-m", "A edit")

	res, err := pullRebaseResolving(context.Background(), a, false, nil, upstreamRef{})
	if err != nil {
		t.Fatalf("pullRebaseResolving() error = %v", err)
	}
	if res.Resolved != 1 {
		t.Errorf("resolved = %d, want 1", res.Resolved)
	}
	rebasing, err := IsRebasing(a)
	if err != nil {
		t.Fatalf("IsRebasing: %v", err)
	}
	if rebasing {
		t.Error("repo is still mid-rebase after a resolved conflict")
	}
	if got := readTaskJSON(t, absA); got.Title != "from B" {
		t.Errorf("later UpdatedAt should win; got Title = %q", got.Title)
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
	if _, err := pullRebaseResolving(context.Background(), a, false, nil, upstreamRef{}); err == nil {
		t.Fatal("expected pullRebaseResolving(autostash=false) to fail with a dirty tracked file")
	}

	// With autostash it succeeds and the modification is restored afterwards.
	res, err := pullRebaseResolving(context.Background(), a, true, nil, upstreamRef{})
	if err != nil {
		t.Fatalf("pullRebaseResolving(autostash=true) error = %v", err)
	}
	if res.Resolved != 0 {
		t.Errorf("resolved = %d, want 0", res.Resolved)
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

	// Overlap is guaranteed rather than hoped for: the test takes repoMu
	// first, so both calls are demonstrably pending at the same time before
	// either can start. Without the rendezvous the Sync goroutine could
	// finish before the commit even began, and the test would assert nothing
	// about concurrency while still passing.
	firstPath := filepath.Join(".monolog", "tasks", "01CT0000.json")
	writeTaskJSON(t, filepath.Join(a, firstPath), model.Task{
		ID: "01CT0000", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	repoMu.Lock()
	syncErr := make(chan error, 1)
	go func() {
		_, err := Sync(a)
		syncErr <- err
	}()
	type commitResult struct {
		sha string
		err error
	}
	commitDone := make(chan commitResult, 1)
	go func() {
		sha, err := AutoCommitSHA(a, "add: 01CT0000", firstPath)
		commitDone <- commitResult{sha: sha, err: err}
	}()

	select {
	case <-syncErr:
		repoMu.Unlock()
		t.Fatal("Sync completed while repoMu was held")
	case <-commitDone:
		repoMu.Unlock()
		t.Fatal("AutoCommitSHA completed while repoMu was held")
	case <-time.After(200 * time.Millisecond):
		// Both are blocked on the mutex: the overlap the test needs.
	}
	repoMu.Unlock()

	first := <-commitDone
	if err := <-syncErr; err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	// A concurrent Sync commits everything it finds (`git add -A`), so it may
	// legitimately absorb the pending write and leave AutoCommitSHA with
	// nothing to commit. That is a pre-existing interaction between the two
	// entry points, not lock corruption, so retry with a fresh task until the
	// commit lands. What repoMu must guarantee is that neither call ever fails
	// on .git/index.lock or commits onto a detached rebase HEAD.
	sha, lastErr := first.sha, first.err
	for i := 1; lastErr != nil && i < 20; i++ {
		if strings.Contains(lastErr.Error(), "index.lock") {
			t.Fatalf("AutoCommitSHA raced Sync on the git index: %v", lastErr)
		}
		id := fmt.Sprintf("01CT%04d", i)
		relPath := filepath.Join(".monolog", "tasks", id+".json")
		writeTaskJSON(t, filepath.Join(a, relPath), model.Task{
			ID: id, Title: "from A", Status: "open", Schedule: "today",
			UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
		})
		sha, lastErr = AutoCommitSHA(a, "add: "+id, relPath)
	}
	if lastErr != nil {
		t.Fatalf("AutoCommitSHA() error = %v", lastErr)
	}
	if sha == "" {
		t.Fatal("AutoCommitSHA() returned an empty SHA")
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

// gitOut runs a read-only git command in the given repo and returns its output.
func gitOut(t *testing.T, repoPath string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repoPath}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, repoPath, err)
	}
	return strings.TrimSpace(string(out))
}

// dirtyConfig writes an uncommitted change to the tracked .monolog/config.json,
// mirroring what the TUI's settings modal does (config.Save writes the file and
// nothing commits it), and returns its repo-relative path.
func dirtyConfig(t *testing.T, repoPath, content string) string {
	t.Helper()
	rel := filepath.Join(".monolog", "config.json")
	if err := os.WriteFile(filepath.Join(repoPath, rel), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return rel
}

// TestPullRebaseResolving_AutostashConflictIsNotSilentSuccess pins the one git
// exit status that lies. `git rebase --autostash` returns ZERO when the rebase
// itself succeeded but reapplying the stash at the end conflicted: it prints
// "Applying autostash resulted in conflicts", leaves unmerged index entries and
// conflict markers in the worktree, and still says "Successfully rebased".
//
// Taken at face value that reports as a clean sync while every later commit
// fails with "Committing is not possible because you have unmerged files" — and
// `monolog sync`'s `git add -A` would stage the conflict markers and push a
// corrupted config.json to every device.
func TestPullRebaseResolving_AutostashConflictIsNotSilentSuccess(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// B changes the shared config file and pushes it.
	cfgRel := dirtyConfig(t, b, "{\n  \"theme\": \"dracula\"\n}\n")
	gitRun(t, b, "add", cfgRel)
	gitRun(t, b, "commit", "-m", "B theme")
	gitRun(t, b, "push")

	// A has an uncommitted change to the same file, so the autostash pop
	// conflicts — two devices changing theme is all it takes.
	dirtyConfig(t, a, "{\n  \"theme\": \"nord\"\n}\n")

	res, err := pullRebaseResolving(context.Background(), a, true, nil, upstreamRef{})
	if err == nil {
		t.Fatal("pullRebaseResolving() error = nil; a conflicting autostash pop must not report success")
	}
	if !errors.Is(err, ErrAutostashConflict) {
		t.Errorf("error = %v, want it to wrap ErrAutostashConflict so AutoPush can tell it "+
			"apart from a failed rebase and still retry its push", err)
	}
	if !strings.Contains(err.Error(), cfgRel) {
		t.Errorf("error should name the affected file; got %v", err)
	}
	if !res.Started {
		t.Error("Started = false, want true: the rebase itself ran")
	}

	// The repo must be left usable, not wedged with unmerged paths.
	paths, uErr := unmergedPaths(a)
	if uErr != nil {
		t.Fatalf("unmergedPaths: %v", uErr)
	}
	if len(paths) != 0 {
		t.Errorf("unmerged paths = %v, want none: the worktree must not be left mid-conflict", paths)
	}
	data, rErr := os.ReadFile(filepath.Join(a, cfgRel))
	if rErr != nil {
		t.Fatalf("read config.json: %v", rErr)
	}
	if strings.Contains(string(data), "<<<<<<<") {
		t.Errorf("config.json still holds conflict markers:\n%s", data)
	}

	// The user's own uncommitted version is what stays on disk. Restoring HEAD
	// over it silently reverted a setting the running TUI still showed as saved
	// and had already confirmed; the incoming version is not lost by keeping
	// the local one, it is committed in HEAD.
	if !strings.Contains(string(data), "nord") {
		t.Errorf("config.json = %s, want the user's uncommitted \"nord\" kept", data)
	}
	if head := gitOut(t, a, "show", "HEAD:"+cfgRel); !strings.Contains(head, "dracula") {
		t.Errorf("HEAD config.json = %s, want the incoming \"dracula\" version still recoverable", head)
	}
	// The file is left as an ordinary unstaged modification — exactly the state
	// it was in before the rebase, so the next commit is unaffected by it.
	if status := gitOut(t, a, "status", "--porcelain", "--", cfgRel); status != "M "+cfgRel &&
		status != " M "+cfgRel {
		t.Errorf("status = %q, want an unstaged modification of %s", status, cfgRel)
	}

	// The stash entry git created is left in place as a backup.
	if stash := gitOut(t, a, "stash", "list"); stash == "" {
		t.Error("git stash list is empty; the stashed local change was lost")
	}

	// And the next mutation can still commit.
	commitTask(t, a, model.Task{
		ID: "01AC", Title: "later mutation", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
}

// TestRunOut_WaitDelayOnASuccessfulCommandIsNotAFailure covers the second thing
// cmd.WaitDelay bounds, which is not a failure at all: a process that exits 0
// while a grandchild still holds the inherited output pipe. git spawns exactly
// those — git-credential-cache--daemon inherits stderr and lives for its 900s
// idle timeout — so without the ProcessState check a successful push would be
// reported to the user as "push failed: ... WaitDelay expired before I/O
// complete".
func TestRunOut_WaitDelayOnASuccessfulCommandIsNotAFailure(t *testing.T) {
	// Exits 0 immediately; the backgrounded child keeps the pipe open well past
	// waitDelay, exactly like the credential-cache daemon.
	sleep := strconv.Itoa(int(waitDelay/time.Second) + 2)
	out, err := runOut(context.Background(), t.TempDir(), nil,
		"sh", "-c", "sleep "+sleep+" & echo pushed")
	if err != nil {
		t.Fatalf("runOut() error = %v, want nil: the command exited 0", err)
	}
	if !strings.Contains(out, "pushed") {
		t.Errorf("out = %q, want the command's output", out)
	}
}

// TestSyncUnattended_PullsAndPushesLikeSync is the parity guard for the
// bounded, prompt-free Sync variant the TUI and the bot use: same commit,
// rebase and push behavior, only the network budget differs.
func TestSyncUnattended_PullsAndPushesLikeSync(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")
	bTask := pushTask(t, b, model.Task{
		ID: "01SU", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// An uncommitted local write, which Sync commits before pulling.
	writeTaskJSON(t, filepath.Join(a, ".monolog", "tasks", "01SA.json"), model.Task{
		ID: "01SA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	res, err := SyncUnattended(a)
	if err != nil {
		t.Fatalf("SyncUnattended() error = %v", err)
	}
	if !res.Committed {
		t.Error("Committed = false, want true")
	}
	if !res.HasRemote {
		t.Error("HasRemote = false, want true")
	}
	if _, err := os.Stat(filepath.Join(a, bTask)); err != nil {
		t.Errorf("B's task should have been pulled in: %v", err)
	}
	if !strings.Contains(gitOut(t, bare, "log", "--oneline"), "sync") {
		t.Error("A's sync commit never reached the remote")
	}
}

// TestAutoCommit_UnstagesAfterAFailedCommit covers the cross-process window
// that repoMu cannot close. While one monolog process sits on a conflicted
// rebase, another's `git add` still succeeds but its `git commit` fails with
// "Committing is not possible because you have unmerged files" — and the first
// process's `git rebase --abort` then resets the index and worktree, deleting
// the staged-but-uncommitted task file outright, with no warning anywhere.
//
// Unstaging on the failed commit leaves the write as an untracked file, which
// an abort does not touch; the next auto-push commits it (pendingTaskWrites).
func TestAutoCommit_UnstagesAfterAFailedCommit(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// Both clones add the same task file with different content, so A's pull
	// stops mid-rebase on an add/add conflict.
	taskPath := pushTask(t, b, model.Task{
		ID: "01CONF", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	writeTaskJSON(t, filepath.Join(a, taskPath), model.Task{
		ID: "01CONF", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, a, "add", taskPath)
	gitRun(t, a, "commit", "-m", "A edit")
	// Expected to fail: it leaves the repo mid-rebase, which is the fixture.
	_ = exec.Command("git", "-C", a, "pull", "--rebase").Run()
	if rebasing, err := IsRebasing(a); err != nil || !rebasing {
		t.Fatalf("IsRebasing() = %v, %v; fixture should have left the repo mid-rebase", rebasing, err)
	}

	// A concurrent capture writes a brand-new task and commits it.
	newRel := filepath.Join(".monolog", "tasks", "01DURING.json")
	writeTaskJSON(t, filepath.Join(a, newRel), model.Task{
		ID: "01DURING", Title: "concurrent capture", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-12T00:00:00Z",
	})
	if err := AutoCommit(a, "add: concurrent capture", newRel); err == nil {
		t.Fatal("AutoCommit() error = nil; a commit cannot succeed with unmerged index entries")
	}
	if status := gitOut(t, a, "status", "--porcelain", "--", newRel); status != "?? "+newRel {
		t.Errorf("status = %q, want %q: a failed commit must leave nothing staged, or the "+
			"other process's rebase --abort discards the write", status, "?? "+newRel)
	}

	// The other process gives up on its rebase.
	if err := RebaseAbort(a); err != nil {
		t.Fatalf("RebaseAbort: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a, newRel)); err != nil {
		t.Errorf("the concurrent write was destroyed by rebase --abort: %v", err)
	}
}

func TestShortError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"single line", errors.New("boom"), "boom"},
		{
			// gitError's shape: the diagnosis is on the SECOND line, which is
			// why this is not a "first line only" trim.
			name: "keeps git's fatal line",
			err: errors.New("git [push]: exit status 128\n" +
				"fatal: could not read Username for 'https://github.com'\n"),
			want: "git [push]: exit status 128 fatal: could not read Username for 'https://github.com'",
		},
		{
			name: "drops the hint block",
			err: errors.New("rebase continue: exit status 1\n" +
				"CONFLICT (content): Merge conflict in .monolog/tasks/01X.json\n" +
				"hint: Resolve all conflicts manually, mark them as resolved with\n" +
				"hint: \"git add/rm <conflicted_files>\", then run \"git rebase --continue\".\n"),
			want: "rebase continue: exit status 1 CONFLICT (content): Merge conflict in .monolog/tasks/01X.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShortError(tt.err); got != tt.want {
				t.Errorf("ShortError() = %q, want %q", got, tt.want)
			}
		})
	}

	long := ShortError(errors.New(strings.Repeat("x", shortErrorLimit*2)))
	if len([]rune(long)) != shortErrorLimit+1 { // +1 for the ellipsis
		t.Errorf("ShortError() length = %d, want it capped at %d", len([]rune(long)), shortErrorLimit)
	}
}

// TestRecoverAutostash_DeleteModifyReportsTheDiscardAndTheStash covers the
// autostash conflict shape that has no stage 3: the stash deleted the file
// (the user removed config.json locally without committing) while the incoming
// commit modified it. There is nothing stashed to write back, so recovery has
// to take HEAD — which throws the local change away. Reporting that as "kept
// your uncommitted <file>" sent the user looking for a change that is no longer
// on disk, and the message did not mention the stash that still holds it.
func TestRecoverAutostash_DeleteModifyReportsTheDiscardAndTheStash(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// B modifies the shared config file and pushes it.
	cfgRel := dirtyConfig(t, b, "{\n  \"theme\": \"dracula\"\n}\n")
	gitRun(t, b, "add", cfgRel)
	gitRun(t, b, "commit", "-m", "B theme")
	gitRun(t, b, "push")

	// A deleted the same file locally without committing, so the autostash pop
	// is a delete/modify conflict.
	if err := os.Remove(filepath.Join(a, cfgRel)); err != nil {
		t.Fatalf("remove config: %v", err)
	}

	_, err := pullRebaseResolving(context.Background(), a, true, nil, upstreamRef{})
	if err == nil {
		t.Fatal("pullRebaseResolving() error = nil; a conflicting autostash pop must not report success")
	}
	if !errors.Is(err, ErrAutostashConflict) {
		t.Fatalf("error = %v, want it to wrap ErrAutostashConflict", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "kept your uncommitted") {
		t.Errorf("error = %v; the local change was NOT kept on this path, so the message must not claim it was", err)
	}
	if !strings.Contains(msg, "stash") {
		t.Errorf("error = %v; the stash is the only remaining copy of the discarded change "+
			"and must be named", err)
	}
	if !strings.Contains(msg, cfgRel) {
		t.Errorf("error = %v, want it to name %s", err, cfgRel)
	}

	// The repo must be left usable, and the stash must still hold the change.
	paths, uErr := unmergedPaths(a)
	if uErr != nil {
		t.Fatalf("unmergedPaths: %v", uErr)
	}
	if len(paths) != 0 {
		t.Errorf("unmerged paths = %v, want none", paths)
	}
	if list := gitOut(t, a, "stash", "list"); !strings.Contains(list, "autostash") {
		t.Errorf("stash list = %q, want the autostash entry kept as the last copy of the change", list)
	}
}

// TestPullRebaseResolving_ResolvesConflictsAcrossTwoCommits reproduces the
// ordinary two-device conflict where each device made TWO commits touching the
// same two tasks. The rebase stops once per local commit, so a single
// resolve/--continue pair leaves the second stop unhandled and the whole rebase
// is aborted — which made every subsequent auto-push AND `monolog sync` fail
// forever with the identical error.
func TestPullRebaseResolving_ResolvesConflictsAcrossTwoCommits(t *testing.T) {
	bare, a := setupRemoteFixture(t)

	// Both tasks exist on the remote first, so each side MODIFIES them.
	one := pushTask(t, a, model.Task{
		ID: "01TWO1", Title: "base one", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-10T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	two := pushTask(t, a, model.Task{
		ID: "01TWO2", Title: "base two", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-10T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	edit := func(repo, path, id, title, updated string) {
		t.Helper()
		writeTaskJSON(t, filepath.Join(repo, path), model.Task{
			ID: id, Title: title, Status: "open", Schedule: "today",
			UpdatedAt: updated, CreatedAt: "2026-04-10T00:00:00Z",
		})
		gitRun(t, repo, "add", path)
		gitRun(t, repo, "commit", "-m", "edit "+id)
	}

	// B edits both tasks in two separate commits and pushes them.
	b := cloneOf(t, bare, "clone-b")
	edit(b, one, "01TWO1", "one from B", "2026-04-11T00:00:00Z")
	edit(b, two, "01TWO2", "two from B", "2026-04-11T00:00:00Z")
	gitRun(t, b, "push")

	// A edits the same two tasks in two commits of its own, with a LATER
	// UpdatedAt so A wins both auto-resolutions.
	edit(a, one, "01TWO1", "one from A", "2026-04-12T00:00:00Z")
	edit(a, two, "01TWO2", "two from A", "2026-04-12T00:00:00Z")

	res, err := pullRebaseResolving(context.Background(), a, false, nil, upstreamRef{})
	if err != nil {
		t.Fatalf("pullRebaseResolving() error = %v", err)
	}
	if res.Resolved != 2 {
		t.Errorf("resolved = %d, want 2 (one per conflicting commit)", res.Resolved)
	}
	rebasing, err := IsRebasing(a)
	if err != nil {
		t.Fatalf("IsRebasing: %v", err)
	}
	if rebasing {
		t.Error("repo is still mid-rebase after a resolvable two-commit conflict")
	}
	for _, e := range []struct{ path, want string }{{one, "one from A"}, {two, "two from A"}} {
		if got := readTaskJSON(t, filepath.Join(a, e.path)); got.Title != e.want {
			t.Errorf("%s: Title = %q, want %q (later UpdatedAt wins)", e.path, got.Title, e.want)
		}
	}
}

// TestPullRebaseResolving_AbortedRebaseReportsNoResolutions pins that a rebase
// which ends in an abort reports Resolved == 0, however many rounds it got
// through first.
//
// The abort rolls every resolution back, so the count is not merely stale, it
// describes work that was undone. Callers render it as "Synced (auto-resolved N
// conflicts)" — the one line that tells the user an edit was DISCARDED to keep
// the newer one — so publishing it after an abort fires that warning on the
// single path where nothing was merged and nothing was lost.
func TestPullRebaseResolving_AbortedRebaseReportsNoResolutions(t *testing.T) {
	bare, a := setupRemoteFixture(t)

	task := pushTask(t, a, model.Task{
		ID: "01ABRT", Title: "base", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-10T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	cfg := filepath.Join(".monolog", "config.json")

	editTask := func(repo, title, updated string) {
		t.Helper()
		writeTaskJSON(t, filepath.Join(repo, task), model.Task{
			ID: "01ABRT", Title: title, Status: "open", Schedule: "today",
			UpdatedAt: updated, CreatedAt: "2026-04-10T00:00:00Z",
		})
		gitRun(t, repo, "add", task)
		gitRun(t, repo, "commit", "-m", "edit task in "+repo)
	}
	editCfg := func(repo, theme string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, cfg), []byte("{\n  \"theme\": \""+theme+"\"\n}\n"), 0o644); err != nil {
			t.Fatalf("write config in %s: %v", repo, err)
		}
		gitRun(t, repo, "add", cfg)
		gitRun(t, repo, "commit", "-m", "theme in "+repo)
	}

	// B pushes a conflicting edit to BOTH the task and config.json.
	b := cloneOf(t, bare, "clone-b")
	editTask(b, "from B", "2026-04-11T00:00:00Z")
	editCfg(b, "dracula")
	gitRun(t, b, "push")

	// A makes the same two edits locally, task first. Replaying them stops
	// twice: the first stop is a task conflict the resolver settles, the second
	// is config.json, which it refuses to touch — so the rebase is aborted after
	// a productive round.
	editTask(a, "from A", "2026-04-12T00:00:00Z")
	editCfg(a, "solarized")

	res, err := pullRebaseResolving(context.Background(), a, false, nil, upstreamRef{})
	if err == nil {
		t.Fatal("pullRebaseResolving() error = nil, want the unresolvable config.json conflict")
	}
	if res.Resolved != 0 {
		t.Errorf("Resolved = %d after an aborted rebase, want 0: the abort rolled that "+
			"resolution back, so reporting it tells the user an edit was discarded when none was",
			res.Resolved)
	}
	if !res.Started {
		t.Error("Started = false, want true: the rebase did run and may have rewritten SHAs")
	}
	if rebasing, rbErr := IsRebasing(a); rbErr != nil || rebasing {
		t.Errorf("IsRebasing() = %v, %v; want false, nil after the abort", rebasing, rbErr)
	}
	if got := readTaskJSON(t, filepath.Join(a, task)); got.Title != "from A" {
		t.Errorf("%s: Title = %q, want %q: the abort restores A's own state", task, got.Title, "from A")
	}
}
