package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Init initializes a new monolog repository at the given path.
// It creates the directory structure (.monolog/tasks/, .monolog/config.json, .gitignore),
// runs git init, and makes an initial commit.
// If remote is non-empty, it adds the remote as origin and pushes the initial commit.
func Init(path string, remote string) error {
	// Check if already initialized
	if _, err := os.Stat(filepath.Join(path, ".monolog")); err == nil {
		return fmt.Errorf("monolog repo already initialized at %s", path)
	}

	// Create directory structure
	tasksDir := filepath.Join(path, ".monolog", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return fmt.Errorf("create tasks directory: %w", err)
	}

	// Write config.json
	configPath := filepath.Join(path, ".monolog", "config.json")
	configData := []byte(`{
  "default_schedule": "today",
  "editor": "$EDITOR"
}
`)
	if err := os.WriteFile(configPath, configData, 0o644); err != nil {
		return fmt.Errorf("write config.json: %w", err)
	}

	// Write .gitignore
	gitignorePath := filepath.Join(path, ".gitignore")
	gitignoreData := []byte("# monolog gitignore\n")
	if err := os.WriteFile(gitignorePath, gitignoreData, 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}

	// Write .gitkeep in tasks/ so the empty directory is tracked
	gitkeepPath := filepath.Join(tasksDir, ".gitkeep")
	if err := os.WriteFile(gitkeepPath, []byte{}, 0o644); err != nil {
		return fmt.Errorf("write .gitkeep: %w", err)
	}

	// git init
	if err := run(path, "git", "init"); err != nil {
		return fmt.Errorf("git init: %w", err)
	}

	// Stage everything
	if err := run(path, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	// Initial commit
	if err := run(path, "git", "commit", "-m", "init: monolog repository"); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	// If remote provided, add origin and push
	if remote != "" {
		if err := run(path, "git", "remote", "add", "origin", remote); err != nil {
			return fmt.Errorf("git remote add: %w", err)
		}
		// Get current branch name
		branchCmd := exec.Command("git", "-C", path, "branch", "--show-current")
		branchOut, err := branchCmd.Output()
		if err != nil {
			return fmt.Errorf("get branch name: %w", err)
		}
		branch := string(branchOut[:len(branchOut)-1]) // trim newline
		if err := run(path, "git", "push", "-u", "origin", branch); err != nil {
			return fmt.Errorf("git push: %w", err)
		}
	}

	return nil
}

// AutoCommit stages the specified files (relative paths within the repo) and
// commits them with the given message. This is used by mutation commands
// (add, done, edit, rm, mv) for automatic git commits.
func AutoCommit(repoPath string, message string, files ...string) error {
	for _, f := range files {
		if err := run(repoPath, "git", "add", f); err != nil {
			return fmt.Errorf("git add %s: %w", f, err)
		}
	}
	if err := run(repoPath, "git", "commit", "-m", message); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

// run executes a command in the given directory, returning an error with
// combined output if the command fails.
func run(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, out)
	}
	return nil
}
