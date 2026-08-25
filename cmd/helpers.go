package cmd

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/git"
	"github.com/maksmas/monolog/internal/store"
)

// loadConfig resolves the monolog data directory and applies
// <dir>/.monolog/config.json to the in-memory config package, returning the
// resolved path. Errors are non-fatal — a missing or malformed config
// silently leaves the package defaults in place.
//
// Split out of openStore so read-only commands (the `status` subcommands)
// can pick up persisted settings without paying for the store handle. The
// config package keeps its values in package-level state that only Load
// populates, so a command that skips this reports built-in defaults rather
// than what the user actually configured.
func loadConfig() string {
	repoPath := monologDir()
	_ = config.Load(repoPath)
	return repoPath
}

// openStore resolves the monolog data directory, loads user config, and opens
// the task store. Returns the store, the repo path (for git operations), and
// any error.
func openStore() (*store.Store, string, error) {
	repoPath := loadConfig()
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	s, err := store.New(tasksDir)
	if err != nil {
		return nil, "", fmt.Errorf("open store: %w", err)
	}
	return s, repoPath, nil
}

// autoPushFn is the seam tests replace to avoid real network I/O, mirroring
// archiveFn in done.go. Nothing outside internal/git may touch the network.
var autoPushFn = git.AutoPush

// pushAfter pushes the commit a mutation just made to the remote, warning
// failures to w and swallowing them: the commit is durable locally and the next
// push (or `monolog sync`) catches up, so a mutation must never fail — or change
// its exit code — because the network did.
//
// Call it AFTER the command's user-visible output. Replacing the git.AutoCommit
// call with a combined commit-and-push would insert up to CLIPushTimeout of
// network I/O between the store write and the success line, turning
// `monolog add` from Raycast or the Claude skill into a visible hang on a bad
// network — exactly where the tool must feel instant. This mirrors done.go's
// archive ordering, which prints "Done:" and only then talks to Gmail.
//
// A skipped push (no remote, or a detached HEAD) returns a nil error and is
// therefore silent: a local-only repo is a supported configuration, not
// something to nag about on every mutation.
//
// An auto-resolved conflict is NOT silent. A rejected push falls back to
// pull --rebase, and ResolveConflicts settles a task-file conflict by keeping
// the newer UpdatedAt and discarding the other side — dropping a phone-side
// edit with no output at all would be the one case where silence costs data.
// `monolog sync` and the TUI both report the same count.
func pushAfter(w io.Writer, repoPath string) {
	if !config.AutoPush() {
		return
	}
	res, err := autoPushFn(repoPath, git.CLIPushTimeout)
	if res.Resolved > 0 {
		fmt.Fprintf(w, "Synced (auto-resolved %d conflicts)\n", res.Resolved)
	}
	switch {
	case err != nil && res.Pushed:
		// The commit reached the remote and something alongside it needs saying
		// (an autostash conflict over config.json). "push failed" would send the
		// user chasing a task that is already synced.
		fmt.Fprintf(w, "%v\n", err)
	case err != nil:
		fmt.Fprintf(w, "push failed: %v\n", err)
	}
}
