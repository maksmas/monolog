package telegram

import (
	"fmt"
	"path/filepath"

	"github.com/mmaksmas/monolog/internal/git"
)

// taskRelPath returns the repo-relative path of a task JSON file for the
// given ULID. The path is used both for the explicit `git.AutoCommit`
// argument list and for any future logging that needs to mention the
// affected file.
func taskRelPath(taskID string) string {
	return filepath.Join(".monolog", "tasks", taskID+".json")
}

// pullFunc and syncFunc are package-level seams that tests override to
// avoid driving real git operations. Production code uses git.PullRebase
// and git.Sync; tests in sync_test.go (task 9) swap them for in-memory
// fakes. Mirror of internal/email's emailAuthorize var pattern.
//
// We do NOT inject these per-Handler because they would multiply the
// constructor surface; package-level vars keep the Handler struct lean
// and tests reset them via t.Cleanup.
var (
	pullFunc = git.PullRebase
	syncFunc = git.Sync
)

// commitAndSync stages the given file, commits with the given message,
// and runs the full sync workflow (commit-pull-rebase-push). On success
// it clears the readOnly flag — capture and Done can both observe the
// "previously failed, now healed" transition without a separate path.
//
// On error from git.Sync, the readOnly flag is set so subsequent write
// requests reject early until the next clean background pull clears it.
// The wrapper returns the original error so the caller can decide what
// to surface to Telegram (typically the canned readOnlyMessage).
//
// Mutex: callers MUST hold h.mu before calling commitAndSync. This is
// already true for every current caller (handleCapture, the Done /
// Active / note paths), and keeping the lock outside the helper avoids
// the recursive-lock trap.
//
// The variadic file list lets recurring-done callers commit both the
// completed task and the freshly spawned follow-up in a single commit
// — recurrence.CompleteAndSpawn returns the file pair for exactly this
// reason. Single-file writes (capture, note, active toggle) pass one
// element.
func (h *Handler) commitAndSync(message string, files ...string) error {
	if err := git.AutoCommit(h.repoPath, message, files...); err != nil {
		return fmt.Errorf("auto-commit: %w", err)
	}
	if _, err := syncFunc(h.repoPath); err != nil {
		h.readOnly.Store(true)
		return fmt.Errorf("git sync: %w", err)
	}
	h.readOnly.Store(false)
	return nil
}

// pullOnce is the helper the pull-ticker goroutine in Serve invokes on
// each tick. It runs git.PullRebase via the injectable seam, and on
// success clears the read-only flag so writes can resume. Errors are
// returned to the caller (which logs to opts.Writer); we deliberately
// do not flip readOnly on pull errors — only the local-write path can
// observe a "sync conflict" condition.
func (h *Handler) pullOnce() error {
	if err := pullFunc(h.repoPath); err != nil {
		return err
	}
	h.readOnly.Store(false)
	return nil
}
