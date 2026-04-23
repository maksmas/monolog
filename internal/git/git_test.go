package git

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmaksmas/monolog/internal/model"
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

	var config map[string]string
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

	// Check .gitignore exists and contains slack_token entry
	gitignorePath := filepath.Join(repoPath, ".gitignore")
	gitignoreData, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf(".gitignore should exist after init: %v", err)
	}
	if !strings.Contains(string(gitignoreData), "slack_token") {
		t.Errorf(".gitignore should contain 'slack_token' entry, got: %q", string(gitignoreData))
	}
	// Sanity check: the entry is on its own line (whole-line match).
	found := false
	for _, line := range strings.Split(string(gitignoreData), "\n") {
		if line == "slack_token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(".gitignore should contain 'slack_token' as a whole line, got: %q", string(gitignoreData))
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

func TestEnsureGitignoreEntry_AppendsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Fresh Init already writes slack_token; use a different entry for this test.
	if err := EnsureGitignoreEntry(repoPath, "secrets.env"); err != nil {
		t.Fatalf("EnsureGitignoreEntry() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoPath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	// Both entries should be present.
	if !strings.Contains(string(data), "slack_token") {
		t.Errorf(".gitignore should still contain 'slack_token', got: %q", string(data))
	}
	if !strings.Contains(string(data), "secrets.env") {
		t.Errorf(".gitignore should contain appended 'secrets.env', got: %q", string(data))
	}
	// Both entries should be on their own lines.
	lines := strings.Split(string(data), "\n")
	seenSlack, seenSecrets := false, false
	for _, l := range lines {
		if l == "slack_token" {
			seenSlack = true
		}
		if l == "secrets.env" {
			seenSecrets = true
		}
	}
	if !seenSlack || !seenSecrets {
		t.Errorf("expected both entries as whole lines, got: %q", string(data))
	}
}

func TestEnsureGitignoreEntry_Idempotent(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Call twice for the same entry; second call must be a no-op.
	if err := EnsureGitignoreEntry(repoPath, "dup.token"); err != nil {
		t.Fatalf("first call error = %v", err)
	}
	first, err := os.ReadFile(filepath.Join(repoPath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	if err := EnsureGitignoreEntry(repoPath, "dup.token"); err != nil {
		t.Fatalf("second call error = %v", err)
	}
	second, err := os.ReadFile(filepath.Join(repoPath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf(".gitignore changed between idempotent calls:\nfirst:  %q\nsecond: %q", string(first), string(second))
	}

	// Call with an existing built-in entry (slack_token) is also a no-op.
	if err := EnsureGitignoreEntry(repoPath, "slack_token"); err != nil {
		t.Fatalf("call for existing slack_token error = %v", err)
	}
	third, err := os.ReadFile(filepath.Join(repoPath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(third) != string(second) {
		t.Errorf(".gitignore changed after no-op for existing entry:\nbefore: %q\nafter:  %q", string(second), string(third))
	}
}

func TestEnsureGitignoreEntry_PreservesExistingEntries(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Write a customized .gitignore with user-added entries.
	custom := "# monolog gitignore\nslack_token\nuser-secret.env\n.DS_Store\n"
	if err := os.WriteFile(filepath.Join(repoPath, ".gitignore"), []byte(custom), 0o644); err != nil {
		t.Fatalf("write custom .gitignore: %v", err)
	}

	if err := EnsureGitignoreEntry(repoPath, "new-entry"); err != nil {
		t.Fatalf("EnsureGitignoreEntry() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoPath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, entry := range []string{"slack_token", "user-secret.env", ".DS_Store", "new-entry"} {
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			if line == entry {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected entry %q preserved/present, got: %q", entry, string(data))
		}
	}
}

func TestEnsureGitignoreEntry_CreatesFileIfMissing(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "bare")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// No Init — no .gitignore exists. EnsureGitignoreEntry should create one.
	if err := EnsureGitignoreEntry(repoPath, "slack_token"); err != nil {
		t.Fatalf("EnsureGitignoreEntry() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoPath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(data), "slack_token") {
		t.Errorf("expected created .gitignore to contain 'slack_token', got: %q", string(data))
	}
}

func TestEnsureGitignoreEntry_NoTrailingNewlineExistingFile(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "test-monolog")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write a .gitignore without a trailing newline — edge case.
	if err := os.WriteFile(filepath.Join(repoPath, ".gitignore"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := EnsureGitignoreEntry(repoPath, "slack_token"); err != nil {
		t.Fatalf("EnsureGitignoreEntry() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoPath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	seenExisting, seenSlack := false, false
	for _, l := range lines {
		if l == "existing" {
			seenExisting = true
		}
		if l == "slack_token" {
			seenSlack = true
		}
	}
	if !seenExisting || !seenSlack {
		t.Errorf("expected both 'existing' and 'slack_token' as whole lines, got: %q", string(data))
	}
}
