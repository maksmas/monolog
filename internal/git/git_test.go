package git

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

