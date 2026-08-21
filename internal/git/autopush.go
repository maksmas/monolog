package git

import (
	"context"
	"errors"
	"fmt"
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
// which a rebase resolves. Lowercase, matched against lowercased output.
var nonFastForwardMarkers = []string{
	"non-fast-forward",
	"fetch first",
	"updates were rejected",
}

// AutoPush pushes the current branch to its upstream, treating every failure as
// non-fatal information for the caller: the mutation that produced the commit
// has already succeeded locally and the next push (or `monolog sync`) catches up.
//
// It is a no-op returning Skipped for repos with no remote or no upstream
// (local-only use, or a remote added by hand after `monolog init` — without the
// upstream check that repo would warn on every mutation).
//
// A push failure that is not a non-fast-forward rejection is returned unchanged
// with Pushed false and no rebase attempted: the remote state is unknown (DNS,
// auth, timeout, protected branch), so rebasing onto it would be a guess.
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

	hasRemote, err := HasRemote(repoPath)
	if err != nil {
		return res, fmt.Errorf("check remote: %w", err)
	}
	if !hasRemote {
		res.Skipped = true
		return res, nil
	}

	upstream, err := hasUpstream(repoPath)
	if err != nil {
		return res, fmt.Errorf("check upstream: %w", err)
	}
	if !upstream {
		res.Skipped = true
		return res, nil
	}

	out, err := pushWithTimeout(repoPath, timeout)
	if err == nil {
		res.Pushed = true
		return res, nil
	}
	if isNonFastForward(out) {
		// Rejected because the remote holds commits we do not have — the one
		// rejection a rebase can fix, and where that recovery hangs off. With no
		// recovery wired up the rejection surfaces like any other failure.
		return res, err
	}
	// The remote state is unknown (DNS, auth, timeout, protected branch), so
	// rebasing onto it would be a guess. Surface the failure unchanged.
	return res, err
}

// pushWithTimeout runs a single `git push`, bounded by timeout, returning git's
// combined output alongside any error so the caller can classify the failure.
func pushWithTimeout(repoPath string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runOut(ctx, repoPath, "git", "push")
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

// hasUpstream reports whether the current branch has a configured upstream.
//
// git rev-parse exits non-zero for every flavor of "there is no upstream to
// push to" — no tracking branch, detached HEAD, a deleted remote branch — and
// says so on stderr rather than through a distinguishing status, so those are
// classified from the output and reported as absence. Anything unrecognized is
// returned as an error for the caller to surface.
func hasUpstream(repoPath string) (bool, error) {
	out, err := runOut(context.Background(), repoPath, "git", "rev-parse", "--abbrev-ref", "@{upstream}")
	if err == nil {
		return strings.TrimSpace(out) != "", nil
	}
	lower := strings.ToLower(out)
	for _, m := range []string{
		"no upstream configured",
		"does not point to a branch",
		"no such branch",
		"unknown revision",
	} {
		if strings.Contains(lower, m) {
			return false, nil
		}
	}
	return false, err
}
