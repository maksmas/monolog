package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PushResult summarizes what AutoPush did. Rebased/Resolved are meaningful even
// when AutoPush returns a non-nil error: the rebase may have rewritten local
// history before the retry push failed, which invalidates any SHA the caller is
// holding (the TUI's undo/redo stacks).
type PushResult struct {
	Pushed   bool // a push succeeded (possibly after a rebase)
	Rebased  bool // pull --rebase ran → local SHAs rewritten, undo/redo invalid
	Resolved int  // task-file conflicts auto-resolved during that rebase
	Skipped  bool // no remote or no upstream; no-op, not an error
}

// DefaultPushTimeout bounds the git push invocations in an AutoPush call.
// It does NOT bound the rebase fallback — see pullRebaseResolving.
const DefaultPushTimeout = 10 * time.Second

// CLIPushTimeout is the shorter budget used by one-shot CLI commands, where a
// human is waiting on the process to exit.
const CLIPushTimeout = 5 * time.Second

// ErrRebaseInProgress is returned when the repo is mid-rebase on entry. A stuck
// rebase must be surfaced clearly rather than retried on every mutation, so
// AutoPush does nothing at all in that state.
var ErrRebaseInProgress = errors.New("repository is mid-rebase; resolve manually")

// nonFastForwardMarkers are the fragments git prints when a push was rejected
// because the remote holds commits we do not have — the one rejection a rebase
// can actually fix.
//
// Deliberately NOT a bare "! [rejected]": that line also covers "(stale info)",
// "(would clobber existing tag)" and protected-branch hook declines, none of
// which a rebase resolves. The "updates were rejected" hint is matched with its
// branch-specific tails for the same reason — git prints "Updates were rejected
// because the tag already exists in the remote" too, and no rebase fixes that.
// Lowercase, matched against lowercased output; git's messages are pinned to
// the C locale in gitEnv so a translated build cannot defeat this.
var nonFastForwardMarkers = []string{
	"non-fast-forward",
	"fetch first",
	"updates were rejected because the remote contains work",
	"updates were rejected because the tip of your current branch is behind",
	"updates were rejected because a pushed branch tip is behind",
}

// ErrRebaseDeferred is returned when a push was rejected as non-fast-forward
// while a task write is still in flight, so the rebase is postponed to the next
// push. The wrapping error names the paths and the remedy.
//
// The deferral is bounded by writeSettleWindow and can never become permanent:
// a task file still uncommitted after that window is an orphan, not an in-flight
// write, and AutoPush commits it instead of deferring again.
var ErrRebaseDeferred = errors.New("push rejected; rebase deferred while a task write is in flight")

// writeSettleWindow is how long an uncommitted task file is treated as a write
// still in flight — the gap between a caller's store.Create/Update and its
// AutoCommitSHA, which is milliseconds even across processes.
//
// Past it, the file is an orphan: a real task whose commit failed (every TUI
// mutation site returns `taskSavedMsg{err: "commit: …"}` after the store write
// has already landed) or a hand-dropped file. Deferring on those forever meant
// auto-push silently stopped working for good the first time one appeared —
// the exact bug this feature exists to fix, reinstated with no way for the user
// to discover it. Committing an orphan is the safe direction: it is a task the
// user created, and losing it is not acceptable.
const writeSettleWindow = 5 * time.Second

// AutoPush pushes the current branch to its upstream, treating every failure as
// non-fatal information for the caller: the mutation that produced the commit
// has already succeeded locally and the next push (or `monolog sync`) catches up.
//
// It is a no-op returning Skipped for a repo with no remote (local-only use is
// a supported configuration, not something to nag about on every mutation) and
// for a detached HEAD. A repo whose remote was added by hand after
// `monolog init` has no upstream yet; rather than skipping forever — the exact
// "my tasks never reach GitHub" symptom this feature exists to fix — the first
// push sets one with `push --set-upstream <remote> <branch>`.
//
// A push failure that is not a non-fast-forward rejection is returned unchanged
// with Pushed false and no rebase attempted: the remote state is unknown (DNS,
// auth, timeout, protected branch), so rebasing onto it would be a guess.
//
// Runs entirely under repoMu, so timeout (plus DefaultFetchTimeout for the
// rebase fallback's fetch) is also the ceiling on how long a concurrent
// mutation's commit can be made to wait — see repoMu.
func AutoPush(repoPath string, timeout time.Duration) (PushResult, error) {
	// Held for the whole call. A plain commit racing this call's push (or, once
	// the rejection path rebases, racing that rebase) either contends on
	// .git/index.lock or commits onto a detached rebase HEAD — see repoMu.
	// Every helper called below is lock-free, so this cannot self-deadlock.
	repoMu.Lock()
	defer repoMu.Unlock()

	var res PushResult

	rebasing, err := IsRebasing(repoPath)
	if err != nil {
		return res, fmt.Errorf("check rebase state: %w", err)
	}
	if rebasing {
		return res, ErrRebaseInProgress
	}

	// A configured upstream is the common case and needs no further probing;
	// only its absence costs the extra `git remote` / `git symbolic-ref` forks.
	//
	// up stays zero (meaning "@{upstream}") whenever one is configured. When it
	// is not, it is filled in and carried all the way to the rebase fallback:
	// a rejected `push --set-upstream` does NOT record the upstream it asked
	// for, so a rebase that tried to infer one afterwards would die with "There
	// is no tracking information for the current branch" — leaving the
	// remote-added-by-hand repo, the exact case --set-upstream exists for,
	// permanently unable to push whenever the remote already had commits.
	var up upstreamRef
	pushArgs := []string{"push"}
	if !hasUpstream(repoPath) {
		remote, err := pushRemote(repoPath)
		if err != nil {
			return res, fmt.Errorf("check remote: %w", err)
		}
		branch := currentBranch(repoPath)
		if remote == "" || branch == "" {
			// No remote at all, several remotes with no obvious default, or a
			// detached HEAD: nothing to push to.
			res.Skipped = true
			return res, nil
		}
		up = upstreamRef{remote: remote, branch: branch}
		pushArgs = append(pushArgs, "--set-upstream", remote, branch)
	}

	out, err := pushWithTimeout(repoPath, timeout, pushArgs...)
	if err == nil {
		res.Pushed = true
		return res, nil
	}
	if !isNonFastForward(out) {
		// The remote state is unknown (DNS, auth, timeout, protected branch), so
		// rebasing onto it would be a guess. Surface the failure unchanged.
		return res, err
	}

	// Rejected because the remote holds commits we do not have — the one
	// rejection a rebase can fix.

	// ...unless another mutation is between its store write and its commit.
	// repoMu serializes git calls but cannot make a caller's write+commit pair
	// atomic, and `--autostash` would stash that half-written task file: a
	// conflicting pop writes conflict markers into the JSON, which the pending
	// `git add` then commits. A rebase deferred to the next push costs a cycle
	// of staleness; a task file full of conflict markers costs user data.
	pending, err := pendingTaskWrites(repoPath)
	if err != nil {
		return res, fmt.Errorf("check pending task writes: %w", err)
	}
	if len(pending) > 0 {
		if writesInFlight(repoPath, pending, time.Now()) {
			return res, fmt.Errorf("%w (%s); the next push commits it, or run 'monolog sync' now",
				ErrRebaseDeferred, strings.Join(pending, ", "))
		}
		// Settled, so nobody is mid-write: this is an orphan, and deferring
		// again would defer forever. Commit it — it is a task the user created
		// — and carry it to the remote with everything else.
		msg := fmt.Sprintf("recover: %d uncommitted task write(s)", len(pending))
		if err := autoCommit(repoPath, msg, pending...); err != nil {
			return res, fmt.Errorf("commit orphaned task write (%s): %w",
				strings.Join(pending, ", "), err)
		}
	}

	// Autostash: AutoPush deliberately does not commit unrelated files, and the
	// rebase refuses to run over a modified tracked file (the TUI writes
	// .monolog/config.json without committing it).
	ctx, cancel := context.WithTimeout(context.Background(), DefaultFetchTimeout)
	defer cancel()
	reb, err := pullRebaseResolving(ctx, repoPath, true, noPromptEnv, up)
	// Rebased is reported on every return path below, error included: from the
	// moment `git rebase` is invoked the local SHAs may already have been
	// rewritten, so a caller told otherwise (the TUI's undo/redo stacks) would
	// keep holding SHAs that no longer resolve — and revertStackCmd silently
	// drops such an entry, corrupting the history in exactly the scenario this
	// feature exists for. It stays FALSE when the call died in the fetch, which
	// rewrites nothing: an unreachable remote must not cost the user their undo
	// history on every single mutation.
	res.Rebased = reb.Started
	res.Resolved = reb.Resolved
	// An autostash conflict is NOT a failed rebase: the rebase completed, the
	// worktree is clean and the commit this call exists to ship is sitting on
	// HEAD, ready to push. Returning there instead would leave the mutation
	// unpushed until some later mutation happened to retry — the very staleness
	// this feature removes — over a conflict in a file (config.json in
	// practice) that has nothing to do with it. So it is carried as a warning
	// and returned alongside the push result.
	var warn error
	switch {
	case errors.Is(err, ErrAutostashConflict):
		warn = err
	case err != nil:
		// The recovery decision is the caller's: the commit is durable locally
		// and the next push or `monolog sync` retries.
		return res, err
	}

	// Exactly one retry, no loop. Another client can always race in again, and a
	// push triggered by every mutation must not turn into an unbounded contest
	// with the remote; the next mutation's push picks up where this one stopped.
	if _, err := pushWithTimeout(repoPath, timeout, pushArgs...); err != nil {
		return res, errors.Join(warn, fmt.Errorf("push after rebase: %w", err))
	}
	res.Pushed = true
	return res, warn
}

// pushWithTimeout runs a single `git push`, bounded by timeout and with
// interactive prompting disabled, returning git's combined output alongside any
// error so the caller can classify the failure.
func pushWithTimeout(repoPath string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runOut(ctx, repoPath, noPromptEnv, "git", args...)
}

// pendingTaskWrites returns the repo-relative paths of task JSON files with
// uncommitted changes.
//
// Untracked entries that are not *.json are ignored on purpose. `git status
// --porcelain` lists them (nothing in monolog's .gitignore excludes them), so a
// single `.DS_Store` dropped in .monolog/tasks/ by a Finder visit — or an
// editor swap file — would otherwise make this non-empty forever, and AutoPush
// would answer every non-fast-forward rejection with ErrRebaseDeferred for
// good: the user's tasks would stop reaching the remote until they noticed and
// ran `monolog sync` by hand. An untracked *.json IS pending, because a
// concurrent store.Create between its write and its commit looks exactly like
// that, and that is the write the deferral exists to protect.
func pendingTaskWrites(repoPath string) ([]string, error) {
	out, err := runOut(context.Background(), repoPath, nil,
		"git", "status", "--porcelain", "--", tasksPrefix)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		// Porcelain v1 format: two status characters, a space, then the path.
		if len(line) < 4 {
			continue
		}
		path := porcelainPath(line[3:])
		if line[:2] == "??" && !strings.HasSuffix(path, ".json") {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// porcelainPath extracts the usable path from the path field of a
// `git status --porcelain` line: the destination half of a rename, unquoted if
// git C-quoted it. A ULID filename never needs quoting, but a stray file that
// did would otherwise be passed to `git add` with its quotes still attached.
func porcelainPath(field string) string {
	field = strings.TrimSpace(field)
	if i := strings.Index(field, " -> "); i >= 0 {
		field = field[i+len(" -> "):]
	}
	if strings.HasPrefix(field, `"`) {
		if unquoted, err := strconv.Unquote(field); err == nil {
			return unquoted
		}
	}
	return field
}

// writesInFlight reports whether any of the given uncommitted task files was
// written within writeSettleWindow of now, i.e. whether some caller is
// plausibly still between its store write and its commit.
//
// A path that no longer exists is a pending deletion. It counts as settled: a
// deletion carries no content that an autostash pop could fill with conflict
// markers, which is the whole hazard the deferral guards against.
func writesInFlight(repoPath string, paths []string, now time.Time) bool {
	for _, p := range paths {
		fi, err := os.Stat(filepath.Join(repoPath, p))
		if err != nil {
			continue
		}
		if now.Sub(fi.ModTime()) < writeSettleWindow {
			return true
		}
	}
	return false
}

// isNonFastForward reports whether combined git-push output indicates the push
// was rejected because the remote has commits we do not have.
//
// git signals this on stderr with a generic exit code, so there is no status to
// switch on and the classification has to be textual. It is kept narrow on
// purpose: matching every "! [rejected]" line would send AutoPush into a rebase
// for stale-info and protected-branch rejections that no rebase can fix.
func isNonFastForward(out string) bool {
	lower := strings.ToLower(out)
	for _, m := range nonFastForwardMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// hasUpstream reports whether the current branch has a usable upstream.
//
// A non-zero exit means there is nothing to push to — no tracking branch, a
// detached HEAD, a deleted remote branch, or no repository at all. All of them
// lead AutoPush to the same place, so this is deliberately a boolean and not a
// (bool, error): classifying git's prose to decide between "absent" and
// "broken" only added a way for a translated or reworded git message to turn a
// local-only repo into a warning on every single mutation. `--verify --quiet`
// keeps git silent on the absence case, so nothing has to be parsed.
func hasUpstream(repoPath string) bool {
	out, err := runOut(context.Background(), repoPath, nil,
		"git", "rev-parse", "--verify", "--quiet", "@{upstream}")
	return err == nil && strings.TrimSpace(out) != ""
}

// pushRemote picks the remote AutoPush should set as the upstream on a first
// push: the sole configured remote, or "origin" when several exist. It returns
// "" when the repo has no remote (a supported local-only configuration) or when
// several remotes exist with no obvious default — guessing there would push a
// user's tasks somewhere they never asked for.
func pushRemote(repoPath string) (string, error) {
	out, err := runOut(context.Background(), repoPath, nil, "git", "remote")
	if err != nil {
		return "", err
	}
	remotes := strings.Fields(out)
	if len(remotes) == 1 {
		return remotes[0], nil
	}
	for _, r := range remotes {
		if r == "origin" {
			return "origin", nil
		}
	}
	return "", nil
}

// currentBranch returns the checked-out branch name, or "" on a detached HEAD
// (or outside a repository).
func currentBranch(repoPath string) string {
	out, err := runOut(context.Background(), repoPath, nil,
		"git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
