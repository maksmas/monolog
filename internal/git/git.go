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

	"github.com/maksmas/monolog/internal/model"
)

// tasksPrefix is the repo-relative path prefix of task JSON files.
const tasksPrefix = ".monolog/tasks/"

// repoMu serializes every mutating git entry point in this package.
//
// A push-only mutex is NOT enough. The auto-push design deliberately lets a
// mutation's AutoCommitSHA run while a push is in flight, each in its own
// tea.Cmd goroutine, and a rejected push falls back to pull --rebase. A commit
// racing that rebase either contends on .git/index.lock — leaving the user with
// a written task file and a "commit:" error — or commits onto a detached rebase
// HEAD. Pressing `s` during an auto-push rebase is likewise two concurrent
// rebases in one worktree. internal/telegram/handler.go already solves this the
// same way, with one mutex around all its git work.
//
// The lock is per-process only: a second monolog process (a Raycast capture, a
// `monolog add` in another terminal) still degrades to .git/index.lock
// contention, which callers surface as a warning.
//
// Exported entry points that take this lock must never call another locking
// exported function — Go's sync.Mutex is not reentrant. Shared work therefore
// lives in unexported, unlocked cores (autoCommit, revert).
var repoMu sync.Mutex

// Init initializes a new monolog repository at the given path.
// It creates the directory structure (.monolog/tasks/, .monolog/config.json, .gitignore),
// runs git init, and makes an initial commit.
// If remote is non-empty, it adds the remote as origin and pushes the initial commit.
func Init(path string, remote string) error {
	// Check if already initialized (a valid repo has a .git directory)
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
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
  "editor": "$EDITOR",
  "theme": "default",
  "date_format": "02-01-2006",
  "auto_push": true
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
		branch := strings.TrimSpace(string(branchOut))
		if branch == "" {
			return fmt.Errorf("could not determine current branch name")
		}
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
	repoMu.Lock()
	defer repoMu.Unlock()
	return autoCommit(repoPath, message, files...)
}

// autoCommit is AutoCommit's unlocked core, shared with AutoCommitSHA so the
// latter can hold repoMu across both the commit and the HEAD read.
func autoCommit(repoPath string, message string, files ...string) error {
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

// headSHA returns the SHA of the current HEAD commit.
func headSHA(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// AutoCommitSHA stages the specified files, commits with the given message,
// then returns the SHA of the resulting HEAD commit.
//
// The commit and the HEAD read happen under one repoMu hold, so a concurrent
// mutation cannot slip a commit in between and hand back the wrong SHA.
func AutoCommitSHA(repoPath string, message string, files ...string) (string, error) {
	repoMu.Lock()
	defer repoMu.Unlock()
	if err := autoCommit(repoPath, message, files...); err != nil {
		return "", err
	}
	sha, err := headSHA(repoPath)
	if err != nil {
		return "", fmt.Errorf("get HEAD SHA after commit: %w", err)
	}
	return sha, nil
}

// CommitSubject returns the one-line subject of the given commit.
func CommitSubject(repoPath, sha string) (string, error) {
	cmd := exec.Command("git", "log", "-1", "--format=%s", sha)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git log -1 --format=%%s %s: %w", sha, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Revert creates a new commit that reverses the named commit (git revert --no-edit).
// On conflict it runs git revert --abort before returning the error.
func Revert(repoPath, sha string) error {
	repoMu.Lock()
	defer repoMu.Unlock()
	return revert(repoPath, sha)
}

// revert is Revert's unlocked core, shared with RevertSHA so the latter can
// hold repoMu across both the revert and the HEAD read.
func revert(repoPath, sha string) error {
	cmd := exec.Command("git", "revert", sha, "--no-edit")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Attempt to abort the revert to leave the repo in a clean state.
		if abortErr := run(repoPath, "git", "revert", "--abort"); abortErr != nil {
			return fmt.Errorf("git revert %s: %w (abort also failed: %v)", sha, err, abortErr)
		}
		return fmt.Errorf("git revert %s: %w\n%s", sha, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RevertSHA reverts the named commit with --no-edit and returns the resulting
// HEAD SHA (the revert commit). On conflict, Revert runs git revert --abort
// before returning the error. Mirror of AutoCommitSHA for the revert path.
func RevertSHA(repoPath, sha string) (string, error) {
	repoMu.Lock()
	defer repoMu.Unlock()
	if err := revert(repoPath, sha); err != nil {
		return "", err
	}
	newSHA, err := headSHA(repoPath)
	if err != nil {
		return "", fmt.Errorf("get HEAD SHA after revert: %w", err)
	}
	return newSHA, nil
}

// HasChanges returns true if the working tree has uncommitted changes
// (untracked files, modified files, or staged changes).
func HasChanges(repoPath string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// HasRemote returns true if the repository has at least one remote configured.
func HasRemote(repoPath string) (bool, error) {
	cmd := exec.Command("git", "remote")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git remote: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// SyncCommit stages all changes and commits with "sync" message.
func SyncCommit(repoPath string) error {
	if err := run(repoPath, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if err := run(repoPath, "git", "commit", "-m", "sync"); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

// PullRebase runs git pull --rebase.
func PullRebase(repoPath string) error {
	return run(repoPath, "git", "pull", "--rebase")
}

// Push runs git push.
func Push(repoPath string) error {
	return run(repoPath, "git", "push")
}

// SyncResult summarizes what happened during a Sync call.
type SyncResult struct {
	// Committed is true if pending local changes were committed before the pull.
	Committed bool
	// HasRemote is false when no remote is configured (the sync becomes a
	// local-commit-only operation).
	HasRemote bool
	// Resolved is the number of task files where a conflict was auto-resolved.
	Resolved int
}

// Sync commits pending changes, pulls with rebase (auto-resolving conflicts
// via ResolveConflicts), and pushes. If no remote is configured, it stops
// after the local commit. Used by both the `monolog sync` CLI command and
// the TUI's sync key.
func Sync(repoPath string) (SyncResult, error) {
	// Held for the whole call: the commit, the rebase and the push must not
	// interleave with a concurrent mutation's commit. The helpers called below
	// (HasChanges, SyncCommit, HasRemote, pullRebaseResolving, Push) are all
	// lock-free, so this cannot self-deadlock.
	repoMu.Lock()
	defer repoMu.Unlock()

	var res SyncResult

	hasChanges, err := HasChanges(repoPath)
	if err != nil {
		return res, fmt.Errorf("check changes: %w", err)
	}
	if hasChanges {
		if err := SyncCommit(repoPath); err != nil {
			return res, fmt.Errorf("commit: %w", err)
		}
		res.Committed = true
	}

	hasRemote, err := HasRemote(repoPath)
	if err != nil {
		return res, fmt.Errorf("check remote: %w", err)
	}
	if !hasRemote {
		return res, nil
	}
	res.HasRemote = true

	// Sync commits pending changes above, so the working tree is already clean
	// and the rebase does not need an autostash.
	n, err := pullRebaseResolving(repoPath, false)
	if err != nil {
		return res, err
	}
	res.Resolved = n

	if err := Push(repoPath); err != nil {
		return res, fmt.Errorf("push: %w", err)
	}
	return res, nil
}

// IsRebasing returns true if the repository is currently in the middle of a
// rebase (either standard or merge-based). Used after a failed PullRebase to
// decide whether to attempt automatic conflict resolution.
func IsRebasing(repoPath string) (bool, error) {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		p := filepath.Join(repoPath, ".git", d)
		if _, err := os.Stat(p); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

// RebaseContinue runs `git rebase --continue`. Disables the commit-message
// editor so it doesn't hang in non-interactive contexts.
func RebaseContinue(repoPath string) error {
	cmd := exec.Command("git", "-c", "core.editor=true", "rebase", "--continue")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git rebase --continue: %w\n%s", err, out)
	}
	return nil
}

// RebaseAbort runs `git rebase --abort`.
func RebaseAbort(repoPath string) error {
	return run(repoPath, "git", "rebase", "--abort")
}

// ResolveConflicts picks a winner for each unmerged task file: the side with
// the later UpdatedAt wins; on a modify-vs-delete conflict the modified side
// wins (data preservation). Resolved files are written to disk and staged.
// Returns the number of files resolved.
//
// Returns an error if any unmerged path is not under .monolog/tasks/, if a
// conflicted task file can't be parsed as JSON, or if both sides are missing.
// Timestamps are RFC3339 strings, which sort correctly lexicographically;
// on a tie "ours" wins (deterministic).
func ResolveConflicts(repoPath string) (int, error) {
	paths, err := unmergedPaths(repoPath)
	if err != nil {
		return 0, err
	}
	resolved := 0
	for _, p := range paths {
		if !strings.HasPrefix(p, tasksPrefix) || !strings.HasSuffix(p, ".json") {
			return resolved, fmt.Errorf("unmerged non-task file: %s", p)
		}
		ours, oursErr := gitShow(repoPath, ":2:"+p)
		theirs, theirsErr := gitShow(repoPath, ":3:"+p)
		oursPresent := oursErr == nil && len(ours) > 0
		theirsPresent := theirsErr == nil && len(theirs) > 0

		var winner []byte
		switch {
		case !oursPresent && !theirsPresent:
			return resolved, fmt.Errorf("both sides missing for %s", p)
		case oursPresent && !theirsPresent:
			winner = ours // modify-vs-delete: modify wins
		case !oursPresent && theirsPresent:
			winner = theirs
		default:
			var ot, tt model.Task
			if err := json.Unmarshal(ours, &ot); err != nil {
				return resolved, fmt.Errorf("parse ours %s: %w", p, err)
			}
			if err := json.Unmarshal(theirs, &tt); err != nil {
				return resolved, fmt.Errorf("parse theirs %s: %w", p, err)
			}
			if tt.UpdatedAt > ot.UpdatedAt {
				winner = theirs
			} else {
				winner = ours
			}
		}

		absPath := filepath.Join(repoPath, p)
		if err := os.WriteFile(absPath, winner, 0o644); err != nil {
			return resolved, fmt.Errorf("write resolved %s: %w", p, err)
		}
		if err := run(repoPath, "git", "add", p); err != nil {
			return resolved, fmt.Errorf("git add %s: %w", p, err)
		}
		resolved++
	}
	return resolved, nil
}

// unmergedPaths returns the repo-relative paths of files in unmerged state.
func unmergedPaths(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only --diff-filter=U: %w", err)
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// gitShow returns the content of a git object at the given ref (e.g. ":2:path"
// for the "ours" side of a conflict). Returns (nil, error) if the ref doesn't
// exist, which the caller uses to detect deleted-on-that-side.
func gitShow(repoPath, ref string) ([]byte, error) {
	cmd := exec.Command("git", "show", ref)
	cmd.Dir = repoPath
	return cmd.Output()
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

// runOut is run's output-returning, context-aware sibling. The combined output
// is returned on both the success and the failure path so callers can classify
// a failure by what git printed (git signals push rejections on stderr with a
// generic exit code, so there is no status to switch on).
func runOut(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w\n%s", name, args, err, out)
	}
	return string(out), nil
}

// pullRebaseResolving pulls with rebase (autostashing local modifications when
// autostash is true), auto-resolving task-file conflicts via ResolveConflicts,
// and returns the number of resolved files. Extracted from Sync so the manual
// sync path and the automatic push path share identical conflict semantics.
//
// Errors are returned already wrapped ("pull: ", "resolve conflicts: ",
// "rebase continue: ") so callers can surface them verbatim. On a resolution or
// continue failure the rebase is aborted, leaving the worktree clean.
//
// Not context-aware: killing git mid-rebase would leave .git/rebase-merge
// behind, and nothing in this package recovers from that. Callers' timeouts
// therefore bound their pushes, not this call.
func pullRebaseResolving(repoPath string, autostash bool) (int, error) {
	args := []string{"pull", "--rebase"}
	if autostash {
		args = append(args, "--autostash")
	}
	if err := run(repoPath, "git", args...); err != nil {
		rebasing, rbErr := IsRebasing(repoPath)
		if rbErr != nil || !rebasing {
			return 0, fmt.Errorf("pull: %w", err)
		}
		n, resErr := ResolveConflicts(repoPath)
		if resErr != nil {
			_ = RebaseAbort(repoPath)
			return 0, fmt.Errorf("resolve conflicts: %w", resErr)
		}
		if err := RebaseContinue(repoPath); err != nil {
			_ = RebaseAbort(repoPath)
			return 0, fmt.Errorf("rebase continue: %w", err)
		}
		return n, nil
	}
	return 0, nil
}
