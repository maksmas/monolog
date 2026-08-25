package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maksmas/monolog/internal/model"
)

// bareHead returns the bare repo's HEAD SHA, or "" when it has no commits yet.
// --verify is required: a plain `rev-parse HEAD` in an empty repo exits 0 and
// echoes back "HEAD" instead of failing.
func bareHead(t *testing.T, bare string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "--quiet", "HEAD").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	// Exit 1 with no output is the legitimate "no commits yet" answer; anything
	// else is a broken fixture that must not masquerade as an empty repo and
	// silently satisfy a `bareHead(...) != ""` assertion.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return ""
	}
	t.Fatalf("rev-parse --verify HEAD in %s: %v", bare, err)
	return ""
}

// commitTask writes, stages and commits a task in the given clone without
// pushing it, returning the repo-relative task path.
func commitTask(t *testing.T, repoPath string, task model.Task) string {
	t.Helper()
	taskPath := filepath.Join(".monolog", "tasks", task.ID+".json")
	writeTaskJSON(t, filepath.Join(repoPath, taskPath), task)
	gitRun(t, repoPath, "add", taskPath)
	gitRun(t, repoPath, "commit", "-m", "add "+task.ID)
	return taskPath
}

func TestAutoPush_PushesLocalCommit(t *testing.T) {
	bare, a := setupRemoteFixture(t)

	commitTask(t, a, model.Task{
		ID: "01AP", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	localHead, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}

	res, err := AutoPush(a, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v", err)
	}
	if !res.Pushed {
		t.Error("Pushed = false, want true")
	}
	if res.Rebased {
		t.Error("Rebased = true, want false on a clean fast-forward push")
	}
	if res.Skipped {
		t.Error("Skipped = true, want false for a repo with an upstream")
	}
	if res.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0", res.Resolved)
	}
	if got := bareHead(t, bare); got != localHead {
		t.Errorf("remote HEAD = %q, want the pushed local commit %q", got, localHead)
	}
	// A plain push must not rewrite local history — the SHA the caller pushed
	// onto its undo stack has to still resolve.
	if after, err := headSHA(a); err != nil || after != localHead {
		t.Errorf("local HEAD = %q (err %v), want unchanged %q", after, err, localHead)
	}
}

func TestAutoPush_SkipsWithoutRemote(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "local-only")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	commitTask(t, repoPath, model.Task{
		ID: "01NR", Title: "local only", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	before, err := headSHA(repoPath)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}

	res, err := AutoPush(repoPath, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v, want nil for a local-only repo", err)
	}
	if !res.Skipped {
		t.Error("Skipped = false, want true with no remote configured")
	}
	if res.Pushed || res.Rebased || res.Resolved != 0 {
		t.Errorf("skip must be a pure no-op, got %+v", res)
	}
	after, err := headSHA(repoPath)
	if err != nil {
		t.Fatalf("headSHA after: %v", err)
	}
	if after != before {
		t.Errorf("HEAD changed from %q to %q; the skip path must not touch the repo", before, after)
	}
	has, err := HasChanges(repoPath)
	if err != nil {
		t.Fatalf("HasChanges: %v", err)
	}
	if has {
		t.Error("working tree should still be clean after a skipped push")
	}
}

func TestAutoPush_SetsUpstreamOnFirstPush(t *testing.T) {
	// A remote added by hand after `monolog init` has an origin but no tracking
	// branch. Skipping silently forever there reintroduces the exact "my tasks
	// never reach GitHub" symptom the feature exists to fix, so the first push
	// sets the upstream as it goes.
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	repoPath := filepath.Join(dir, "clone")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	gitRun(t, repoPath, "remote", "add", "origin", bare)
	if hasUpstream(repoPath) {
		t.Fatal("fixture should have no upstream before the first push")
	}

	commitTask(t, repoPath, model.Task{
		ID: "01UP", Title: "first push", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	localHead, err := headSHA(repoPath)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}

	res, err := AutoPush(repoPath, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v", err)
	}
	if !res.Pushed {
		t.Error("Pushed = false, want true: the first push should set the upstream and land")
	}
	if res.Skipped {
		t.Error("Skipped = true, want false: a remote with no upstream must not be a silent no-op")
	}
	if got := bareHead(t, bare); got != localHead {
		t.Errorf("remote HEAD = %q, want the pushed local commit %q", got, localHead)
	}
	if !hasUpstream(repoPath) {
		t.Error("hasUpstream() = false after the first push; --set-upstream should have stuck")
	}
	// The second push must be a plain one against the upstream just recorded.
	commitTask(t, repoPath, model.Task{
		ID: "01UQ", Title: "second push", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	if res, err := AutoPush(repoPath, DefaultPushTimeout); err != nil || !res.Pushed {
		t.Fatalf("second AutoPush() = %+v, %v; want a plain successful push", res, err)
	}
}

func TestAutoPush_SkipsOnDetachedHEAD(t *testing.T) {
	// A detached HEAD has no branch to track and nothing sane to push, so it
	// must stay a silent no-op rather than warning on every mutation.
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	repoPath := filepath.Join(dir, "clone")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	gitRun(t, repoPath, "remote", "add", "origin", bare)
	sha, err := headSHA(repoPath)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}
	gitRun(t, repoPath, "checkout", "--detach", sha)

	res, err := AutoPush(repoPath, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v, want nil on a detached HEAD", err)
	}
	if !res.Skipped {
		t.Error("Skipped = false, want true on a detached HEAD")
	}
	if res.Pushed || res.Rebased || res.Resolved != 0 {
		t.Errorf("skip must be a pure no-op, got %+v", res)
	}
	if got := bareHead(t, bare); got != "" {
		t.Errorf("remote HEAD = %q, want empty: nothing may have been pushed", got)
	}
}

func TestAutoPush_SkipsWithSeveralRemotesAndNoOrigin(t *testing.T) {
	// Guessing between two hand-added remotes would push a user's tasks
	// somewhere they never asked for; skipping silently is the safe default.
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "clone")
	if err := Init(repoPath, ""); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	gitRun(t, repoPath, "remote", "add", "work", filepath.Join(dir, "work.git"))
	gitRun(t, repoPath, "remote", "add", "backup", filepath.Join(dir, "backup.git"))

	res, err := AutoPush(repoPath, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v, want nil with no default remote", err)
	}
	if !res.Skipped {
		t.Error("Skipped = false, want true when no remote is an obvious default")
	}
	if res.Pushed || res.Rebased {
		t.Errorf("skip must be a pure no-op, got %+v", res)
	}
}

func TestAutoPush_MidRebaseReturnsSentinel(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// Both clones add the same task file with different content, so A's pull
	// stops mid-rebase on an add/add conflict.
	taskPath := pushTask(t, b, model.Task{
		ID: "01MR", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	writeTaskJSON(t, filepath.Join(a, taskPath), model.Task{
		ID: "01MR", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, a, "add", taskPath)
	gitRun(t, a, "commit", "-m", "A edit")

	// Expected to fail: it leaves the repo mid-rebase, which is the fixture.
	_ = exec.Command("git", "-C", a, "pull", "--rebase").Run()
	rebasing, err := IsRebasing(a)
	if err != nil {
		t.Fatalf("IsRebasing: %v", err)
	}
	if !rebasing {
		t.Fatal("fixture should have left the repo mid-rebase")
	}
	remoteBefore := bareHead(t, bare)

	res, err := AutoPush(a, DefaultPushTimeout)
	if !errors.Is(err, ErrRebaseInProgress) {
		t.Errorf("error = %v, want ErrRebaseInProgress", err)
	}
	if res.Pushed || res.Rebased || res.Skipped || res.Resolved != 0 {
		t.Errorf("mid-rebase must be a pure no-op, got %+v", res)
	}
	if got := bareHead(t, bare); got != remoteBefore {
		t.Errorf("remote HEAD = %q, want unchanged %q: no push may be attempted mid-rebase", got, remoteBefore)
	}
	// The rebase is left exactly as found, for the user to resolve.
	if rebasing, err := IsRebasing(a); err != nil || !rebasing {
		t.Errorf("IsRebasing() = %v, %v; want true, nil (AutoPush must not touch the rebase)", rebasing, err)
	}
}

func TestAutoPush_BogusRemoteURLReturnsError(t *testing.T) {
	// The upstream is configured (Init pushed with -u) but the URL now points
	// nowhere, so the push fails for a reason no rebase can fix.
	_, a := setupRemoteFixture(t)
	commitTask(t, a, model.Task{
		ID: "01BR", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, a, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "does-not-exist.git"))
	before, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}

	res, err := AutoPush(a, DefaultPushTimeout)
	if err == nil {
		t.Fatal("expected an error pushing to a nonexistent remote")
	}
	if res.Pushed {
		t.Error("Pushed = true, want false when the push failed")
	}
	if res.Rebased {
		t.Error("Rebased = true, want false: a non-rejection failure must not trigger a rebase")
	}
	if res.Skipped {
		t.Error("Skipped = true, want false: the repo has both a remote and an upstream")
	}
	after, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA after: %v", err)
	}
	if after != before {
		t.Errorf("HEAD changed from %q to %q; a failed push must not rewrite local history", before, after)
	}
	if rebasing, err := IsRebasing(a); err != nil || rebasing {
		t.Errorf("IsRebasing() = %v, %v; want false, nil", rebasing, err)
	}
}

// rejectPushes installs a pre-receive hook in the bare repo that declines every
// push. It does not affect a non-fast-forward rejection, which git decides on
// the client from the ref advertisement without ever running receive-pack hooks
// — so a repo with this hook rejects the first push as non-fast-forward and the
// retry push after the rebase as a remote decline.
func rejectPushes(t *testing.T, bare string) {
	t.Helper()
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write pre-receive hook: %v", err)
	}
}

func TestAutoPush_RebasesOnRejectionThenPushes(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// B pushes a DIFFERENT task, so A's push is rejected as non-fast-forward and
	// the rebase is conflict-free.
	bTask := pushTask(t, b, model.Task{
		ID: "01RJB", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	aTask := commitTask(t, a, model.Task{
		ID: "01RJA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	beforeSHA, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}

	res, err := AutoPush(a, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v", err)
	}
	if !res.Rebased {
		t.Error("Rebased = false, want true after a non-fast-forward rejection")
	}
	if !res.Pushed {
		t.Error("Pushed = false, want true: the retry push should succeed")
	}
	if res.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0 for a conflict-free rebase", res.Resolved)
	}
	if res.Skipped {
		t.Error("Skipped = true, want false")
	}

	// The rebase rewrote local history, and A's commit reached the remote.
	afterSHA, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA after: %v", err)
	}
	if afterSHA == beforeSHA {
		t.Error("local HEAD unchanged; the rebase should have rewritten A's commit")
	}
	if got := bareHead(t, bare); got != afterSHA {
		t.Errorf("remote HEAD = %q, want A's rebased HEAD %q", got, afterSHA)
	}
	// Both sides' tasks survive in A.
	if _, err := os.Stat(filepath.Join(a, bTask)); err != nil {
		t.Errorf("B's task should be present in A after the rebase: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a, aTask)); err != nil {
		t.Errorf("A's own task should survive the rebase: %v", err)
	}
	if rebasing, err := IsRebasing(a); err != nil || rebasing {
		t.Errorf("IsRebasing() = %v, %v; want false, nil", rebasing, err)
	}
}

func TestAutoPush_RejectionWithConflictAutoResolves(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// Both clones add the SAME task file: the rejection's rebase hits an add/add
	// conflict that ResolveConflicts settles on the later UpdatedAt.
	taskPath := pushTask(t, b, model.Task{
		ID: "01RC", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	abs := filepath.Join(a, taskPath)
	writeTaskJSON(t, abs, model.Task{
		ID: "01RC", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, a, "add", taskPath)
	gitRun(t, a, "commit", "-m", "A edit")

	res, err := AutoPush(a, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v", err)
	}
	if !res.Rebased {
		t.Error("Rebased = false, want true")
	}
	if !res.Pushed {
		t.Error("Pushed = false, want true after the conflict was resolved")
	}
	if res.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", res.Resolved)
	}
	if got := readTaskJSON(t, abs).Title; got != "from A" {
		t.Errorf("Title = %q, want %q: the later UpdatedAt wins", got, "from A")
	}
	head, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}
	if got := bareHead(t, bare); got != head {
		t.Errorf("remote HEAD = %q, want the resolved local HEAD %q", got, head)
	}
	if rebasing, err := IsRebasing(a); err != nil || rebasing {
		t.Errorf("IsRebasing() = %v, %v; want false, nil", rebasing, err)
	}
}

func TestAutoPush_RebasedSurvivesRetryPushFailure(t *testing.T) {
	// The load-bearing case: the rebase rewrote local SHAs and only then did the
	// retry push fail. Reporting Rebased false here would leave the TUI's undo
	// stack holding commits that no longer exist.
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	bTask := pushTask(t, b, model.Task{
		ID: "01RFB", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	// Installed after B's push so only the retry push meets the hook.
	rejectPushes(t, bare)
	commitTask(t, a, model.Task{
		ID: "01RFA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	beforeSHA, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}
	remoteBefore := bareHead(t, bare)

	res, err := AutoPush(a, DefaultPushTimeout)
	if err == nil {
		t.Fatal("expected an error when the retry push is declined by the remote")
	}
	if !res.Rebased {
		t.Error("Rebased = false, want true: local history was rewritten before the retry failed")
	}
	if res.Pushed {
		t.Error("Pushed = true, want false: the retry push failed")
	}
	if res.Skipped {
		t.Error("Skipped = true, want false")
	}

	afterSHA, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA after: %v", err)
	}
	if afterSHA == beforeSHA {
		t.Fatal("local HEAD unchanged; the fixture did not exercise the rebase")
	}
	if _, err := os.Stat(filepath.Join(a, bTask)); err != nil {
		t.Errorf("B's task should be present in A after the rebase: %v", err)
	}
	if got := bareHead(t, bare); got != remoteBefore {
		t.Errorf("remote HEAD = %q, want unchanged %q: the retry push was declined", got, remoteBefore)
	}
	if rebasing, err := IsRebasing(a); err != nil || rebasing {
		t.Errorf("IsRebasing() = %v, %v; want false, nil", rebasing, err)
	}
}

func TestAutoPush_RejectionAutostashesDirtyFile(t *testing.T) {
	// pull --rebase refuses to run over a modified tracked file, and AutoPush
	// deliberately does not commit unrelated files — the TUI's applySettings
	// writes .monolog/config.json without committing it, so without --autostash
	// every rejected push after a settings change would fail.
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	pushTask(t, b, model.Task{
		ID: "01DS", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	commitTask(t, a, model.Task{
		ID: "01DA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	dirty := filepath.Join(a, ".monolog", "config.json")
	const dirtyContent = "{\n  \"theme\": \"dracula\"\n}\n"
	if err := os.WriteFile(dirty, []byte(dirtyContent), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	res, err := AutoPush(a, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v", err)
	}
	if !res.Rebased || !res.Pushed {
		t.Errorf("got %+v, want Rebased and Pushed true", res)
	}
	data, err := os.ReadFile(dirty)
	if err != nil {
		t.Fatalf("read dirty file after autostash: %v", err)
	}
	if string(data) != dirtyContent {
		t.Errorf("autostash should restore the modification; got %q, want %q", string(data), dirtyContent)
	}
	// Still uncommitted: AutoPush must not sweep unrelated files into a commit.
	has, err := HasChanges(a)
	if err != nil {
		t.Fatalf("HasChanges: %v", err)
	}
	if !has {
		t.Error("dirty file should still be uncommitted after the autostashed rebase")
	}
}

func TestIsNonFastForward(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want bool
	}{
		{
			name: "non-fast-forward rejection",
			out: "To /tmp/remote.git\n" +
				" ! [rejected]        main -> main (non-fast-forward)\n" +
				"error: failed to push some refs to '/tmp/remote.git'\n",
			want: true,
		},
		{
			name: "fetch first rejection",
			out: "To github.com:maksmas/monolog-tasks.git\n" +
				" ! [rejected]        main -> main (fetch first)\n" +
				"error: failed to push some refs to 'github.com:maksmas/monolog-tasks.git'\n",
			want: true,
		},
		{
			name: "updates were rejected hint",
			out: "hint: Updates were rejected because the remote contains work that you do\n" +
				"hint: not have locally.\n",
			want: true,
		},
		{
			name: "branch behind hint",
			out: "hint: Updates were rejected because the tip of your current branch is behind\n" +
				"hint: its remote counterpart.\n",
			want: true,
		},
		{
			name: "tag rejection reuses the same hint prefix but no rebase fixes it",
			out: " ! [rejected]        v1 -> v1 (already exists)\n" +
				"hint: Updates were rejected because the tag already exists in the remote.\n",
			want: false,
		},
		{
			name: "marker casing is ignored",
			out:  " ! [rejected]        main -> main (NON-FAST-FORWARD)\n",
			want: true,
		},
		{
			name: "authentication failure",
			out: "remote: Invalid username or password.\n" +
				"fatal: Authentication failed for 'https://github.com/maksmas/monolog-tasks.git/'\n",
			want: false,
		},
		{
			name: "dns failure",
			out: "fatal: unable to access 'https://github.com/maksmas/monolog-tasks.git/': " +
				"Could not resolve host: github.com\n",
			want: false,
		},
		{
			name: "timeout kill",
			out:  "signal: killed\ncontext deadline exceeded\n",
			want: false,
		},
		{
			name: "stale info rejection is not a non-fast-forward",
			out: "To /tmp/remote.git\n" +
				" ! [rejected]        main -> main (stale info)\n" +
				"error: failed to push some refs to '/tmp/remote.git'\n",
			want: false,
		},
		{
			name: "tag clobber rejection is not a non-fast-forward",
			out: "To /tmp/remote.git\n" +
				" ! [rejected]        v1 -> v1 (would clobber existing tag)\n",
			want: false,
		},
		{
			name: "protected branch rejection is not a non-fast-forward",
			out: "remote: error: GH006: Protected branch update failed for refs/heads/main.\n" +
				" ! [remote rejected] main -> main (protected branch hook declined)\n" +
				"error: failed to push some refs\n",
			want: false,
		},
		{
			name: "empty output",
			out:  "",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNonFastForward(tt.out); got != tt.want {
				t.Errorf("isNonFastForward(%q) = %v, want %v", tt.out, got, tt.want)
			}
		})
	}
}

func TestHasUpstream_WithTrackingBranch(t *testing.T) {
	_, a := setupRemoteFixture(t)

	if !hasUpstream(a) {
		t.Error("hasUpstream() = false, want true for a branch pushed with -u")
	}
}

func TestHasUpstream_DetachedHEAD(t *testing.T) {
	// Detached HEAD has no upstream and must be reported as absence, not as an
	// error, so a mutation in that state skips instead of warning.
	_, a := setupRemoteFixture(t)
	sha, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}
	gitRun(t, a, "checkout", "--detach", sha)

	if hasUpstream(a) {
		t.Error("hasUpstream() = true, want false for a detached HEAD")
	}
}

func TestHasUpstream_NotARepo(t *testing.T) {
	// Nothing to push to, reported as absence. AutoPush still surfaces a bogus
	// path as an error — via pushRemote, which is where the real repo check is.
	dir := t.TempDir()
	if hasUpstream(dir) {
		t.Error("hasUpstream() = true, want false outside a git repository")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("fixture dir should not be a repo: %v", err)
	}
	if _, err := pushRemote(dir); err == nil {
		t.Error("pushRemote() error = nil, want an error outside a git repository")
	}
	if _, err := AutoPush(dir, DefaultPushTimeout); err == nil {
		t.Error("AutoPush() error = nil, want an error outside a git repository")
	}
}

// TestAutoPush_HoldsRepoMuAcrossTheRebase pins the lock AutoPush takes.
//
// Deterministic by construction: the test itself holds repoMu, so an AutoPush
// that does not take it runs to completion immediately (a push against a local
// bare repo takes milliseconds). Deleting repoMu.Lock/Unlock from AutoPush
// therefore fails this test instead of merely making a race more likely.
func TestAutoPush_HoldsRepoMuAcrossTheRebase(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")
	pushTask(t, b, model.Task{
		ID: "01LKB", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	commitTask(t, a, model.Task{
		ID: "01LKA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	repoMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := AutoPush(a, DefaultPushTimeout)
		done <- err
	}()

	select {
	case err := <-done:
		repoMu.Unlock()
		t.Fatalf("AutoPush() returned (%v) while repoMu was held: it does not take the lock, "+
			"so a concurrent commit can land on a detached rebase HEAD", err)
	case <-time.After(500 * time.Millisecond):
	}

	repoMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AutoPush() error = %v after the lock was released", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("AutoPush() never completed after repoMu was released")
	}
}

// TestAutoPush_ConcurrentWithAutoCommitSHA is the live-fire twin of the lock
// test: a rebasing AutoPush and a mutation's commit in flight at the same time,
// which is exactly what the TUI produces (an autoPushCmd goroutine vs. a
// taskSavedMsg mutation's AutoCommitSHA goroutine).
func TestAutoPush_ConcurrentWithAutoCommitSHA(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")
	pushTask(t, b, model.Task{
		ID: "01CCB", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	commitTask(t, a, model.Task{
		ID: "01CCA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// Written before the goroutines start: writeTaskJSON calls t.Fatalf, which
	// is only valid on the test goroutine.
	relPath := filepath.Join(".monolog", "tasks", "01CCC.json")
	writeTaskJSON(t, filepath.Join(a, relPath), model.Task{
		ID: "01CCC", Title: "concurrent", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	pushErr := make(chan error, 1)
	go func() {
		_, err := AutoPush(a, DefaultPushTimeout)
		pushErr <- err
	}()
	commitErr := make(chan error, 1)
	go func() {
		_, err := AutoCommitSHA(a, "add: 01CCC", relPath)
		commitErr <- err
	}()

	// Both orderings are legitimate and the race between them is the point of
	// the test: if AutoCommitSHA takes repoMu first, the push carries its commit
	// to the remote; if AutoPush takes it first, it finds an uncommitted task
	// write and defers the rebase to the next push BY DESIGN, rather than
	// autostashing a file someone else is mid-write on. Only a third outcome is
	// a defect, and that is what the assertions below cover.
	if err := <-pushErr; err != nil && !errors.Is(err, ErrRebaseDeferred) {
		t.Errorf("AutoPush() error = %v", err)
	}
	// The commit may legitimately find nothing to commit: the rebase's autostash
	// can absorb the pending write. What must never happen is an index.lock
	// collision or a commit onto a detached rebase HEAD.
	if err := <-commitErr; err != nil && !strings.Contains(err.Error(), "nothing to commit") {
		t.Errorf("AutoCommitSHA() error = %v", err)
	}
	if rebasing, err := IsRebasing(a); err != nil || rebasing {
		t.Errorf("IsRebasing() = %v, %v; want false, nil", rebasing, err)
	}
	if _, err := headSHA(a); err != nil {
		t.Errorf("HEAD does not resolve after the concurrent run: %v", err)
	}
}

// TestAutoPush_RebasedTrueWhenTheRebaseItselfFails pins the "Rebased is set
// BEFORE the recovery" contract. Gating it on err == nil would leave the TUI
// holding undo/redo SHAs that a partially-applied rebase may already have
// invalidated.
func TestAutoPush_RebasedTrueWhenTheRebaseItselfFails(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// Both sides edit .monolog/config.json — a NON-task file, which
	// ResolveConflicts refuses to auto-resolve — so A's push is rejected as
	// non-fast-forward and the rebase that follows fails.
	cfg := filepath.Join(".monolog", "config.json")
	if err := os.WriteFile(filepath.Join(b, cfg), []byte("{\n  \"theme\": \"dracula\"\n}\n"), 0o644); err != nil {
		t.Fatalf("write config in B: %v", err)
	}
	gitRun(t, b, "add", cfg)
	gitRun(t, b, "commit", "-m", "B theme")
	gitRun(t, b, "push")

	if err := os.WriteFile(filepath.Join(a, cfg), []byte("{\n  \"theme\": \"solarized\"\n}\n"), 0o644); err != nil {
		t.Fatalf("write config in A: %v", err)
	}
	gitRun(t, a, "add", cfg)
	gitRun(t, a, "commit", "-m", "A theme")

	res, err := AutoPush(a, DefaultPushTimeout)
	if err == nil {
		t.Fatal("AutoPush() error = nil, want the rebase failure to surface")
	}
	if !res.Rebased {
		t.Error("Rebased = false; it must be reported even when the rebase itself failed, " +
			"or the caller keeps undo/redo SHAs a partial rebase may have invalidated")
	}
	if res.Pushed {
		t.Error("Pushed = true, want false")
	}
	if got := bareHead(t, bare); got == "" {
		t.Error("remote HEAD is empty; the fixture should have B's commit")
	}
	// The failed rebase is aborted, not left for the user to find.
	if rebasing, err := IsRebasing(a); err != nil || rebasing {
		t.Errorf("IsRebasing() = %v, %v; want false, nil after the abort", rebasing, err)
	}
}

// TestAutoPush_DefersRebaseWhileATaskWriteIsUncommitted covers the guard that
// keeps --autostash away from a half-written task file. repoMu serializes git
// calls but cannot make a caller's store.Update -> AutoCommitSHA pair atomic,
// and a conflicting autostash pop writes conflict markers into the JSON that
// the pending `git add` would then commit.
func TestAutoPush_DefersRebaseWhileATaskWriteIsUncommitted(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")
	pushTask(t, b, model.Task{
		ID: "01DFB", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	aTask := commitTask(t, a, model.Task{
		ID: "01DFA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	beforeSHA, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}

	// Another mutation is between its store write and its commit.
	const pending = "{\n  \"id\": \"01DFA\",\n  \"title\": \"edited, not yet committed\"\n}\n"
	if err := os.WriteFile(filepath.Join(a, aTask), []byte(pending), 0o644); err != nil {
		t.Fatalf("write pending task edit: %v", err)
	}

	res, err := AutoPush(a, DefaultPushTimeout)
	if !errors.Is(err, ErrRebaseDeferred) {
		t.Fatalf("AutoPush() error = %v, want ErrRebaseDeferred", err)
	}
	if res.Rebased {
		t.Error("Rebased = true, want false: the rebase must be deferred, not attempted")
	}
	if res.Pushed {
		t.Error("Pushed = true, want false")
	}
	if after, err := headSHA(a); err != nil || after != beforeSHA {
		t.Errorf("HEAD = %q (err %v), want unchanged %q", after, err, beforeSHA)
	}
	// The pending write is untouched — no stash, no conflict markers.
	data, err := os.ReadFile(filepath.Join(a, aTask))
	if err != nil {
		t.Fatalf("read pending task edit: %v", err)
	}
	if string(data) != pending {
		t.Errorf("pending task edit = %q, want it left exactly as written", string(data))
	}
}

// TestAutoPush_TimeoutBoundsAHungPush pins that the push timeout actually kills
// something. git's transport child (here a stand-in ssh that just sleeps)
// inherits the output pipe, so without cmd.WaitDelay the call blocks in Wait
// long after the context killed git itself.
func TestAutoPush_TimeoutBoundsAHungPush(t *testing.T) {
	_, a := setupRemoteFixture(t)
	commitTask(t, a, model.Task{
		ID: "01TO", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// A stand-in for ssh that connects and then never answers — a half-open
	// connection with no ServerAliveInterval, the case the timeout exists for.
	hang := filepath.Join(t.TempDir(), "hanging-ssh")
	if err := os.WriteFile(hang, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	gitRun(t, a, "config", "core.sshCommand", hang)
	gitRun(t, a, "remote", "set-url", "origin", "ssh://git@monolog.invalid/tasks.git")

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := AutoPush(a, 200*time.Millisecond)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("AutoPush() error = nil, want the timed-out push to fail")
		}
		// waitDelay adds its own grace period on top of the deadline.
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("AutoPush() took %v for a 200ms timeout", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("AutoPush() did not return: the push timeout bounds nothing")
	}
}

func TestRedactCredentials(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "token in userinfo",
			in:   "fatal: unable to access 'https://x-access-token:ghp_secret@github.com/u/t.git/'",
			want: "fatal: unable to access 'https://***@github.com/u/t.git/'",
		},
		{
			name: "bare username is still userinfo",
			in:   "remote: https://ghp_secret@github.com/u/t.git",
			want: "remote: https://***@github.com/u/t.git",
		},
		{
			// git accepts an unencoded "@" inside the password, so a regex that
			// stops at the first one redacts "user:p" and leaks the rest.
			name: "password containing an at-sign is fully redacted",
			in:   "fatal: unable to access 'https://user:p@sw0rd@github.com/u/t.git/'",
			want: "fatal: unable to access 'https://***@github.com/u/t.git/'",
		},
		{
			name: "two urls on one line are redacted independently",
			in:   "https://a:secret1@host/x https://b:secret2@host/y",
			want: "https://***@host/x https://***@host/y",
		},
		{
			name: "ssh scp-style address is untouched",
			in:   "fatal: unable to access 'git@github.com:maksmas/monolog-tasks.git'",
			want: "fatal: unable to access 'git@github.com:maksmas/monolog-tasks.git'",
		},
		{
			name: "credential-free url is untouched",
			in:   "To https://github.com/maksmas/monolog-tasks.git",
			want: "To https://github.com/maksmas/monolog-tasks.git",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactCredentials(tt.in); got != tt.want {
				t.Errorf("redactCredentials(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestAutoPush_ErrorRedactsRemoteCredentials is the end-to-end half: the error
// the CLI prints and the TUI flashes must not carry a token.
func TestAutoPush_ErrorRedactsRemoteCredentials(t *testing.T) {
	_, a := setupRemoteFixture(t)
	commitTask(t, a, model.Task{
		ID: "01RD", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	gitRun(t, a, "remote", "set-url", "origin", "https://x-access-token:ghp_supersecret@monolog.invalid/tasks.git")

	_, err := AutoPush(a, 3*time.Second)
	if err == nil {
		t.Fatal("AutoPush() error = nil, want a failure against an unreachable host")
	}
	if strings.Contains(err.Error(), "ghp_supersecret") {
		t.Errorf("error leaks the remote's token: %v", err)
	}
}

// TestAutoPush_FirstPushRebasesOntoANonEmptyRemote is the other half of
// TestAutoPush_SetsUpstreamOnFirstPush, and the case that actually happens: the
// user creates the GitHub repo with a README (or a second device pushed first),
// so the very first `push --set-upstream` is REJECTED.
//
// A rejected push does not record the upstream it asked for, so the rebase
// fallback has nothing to infer one from: a bare `git rebase` there dies with
// "There is no tracking information for the current branch" and the repo can
// never push again — every mutation, forever. The remote branch is therefore
// named explicitly all the way through the fallback.
func TestAutoPush_FirstPushRebasesOntoANonEmptyRemote(t *testing.T) {
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	// Seed the remote the way "create the repo with a README" does.
	seed := filepath.Join(dir, "seed")
	if err := Init(seed, bare); err != nil {
		t.Fatalf("Init(seed) error = %v", err)
	}
	branch := baseBranch(t, seed)
	gitRun(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	pushTask(t, seed, model.Task{
		ID: "01NE", Title: "already on the remote", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	remoteHeadBefore := bareHead(t, bare)

	// A separate clone with the remote added by hand and no upstream, holding a
	// commit the remote does not have.
	a := cloneOf(t, bare, "clone-a")
	gitRun(t, a, "branch", "--unset-upstream")
	if hasUpstream(a) {
		t.Fatal("fixture should have no upstream")
	}
	gitRun(t, a, "reset", "--hard", remoteHeadBefore+"~1")
	commitTask(t, a, model.Task{
		ID: "01NF", Title: "local only", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	res, err := AutoPush(a, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v; the first push must recover from a rejection", err)
	}
	if !res.Rebased {
		t.Error("Rebased = false, want true: the rejection was recoverable")
	}
	if !res.Pushed {
		t.Error("Pushed = false, want true")
	}
	if !hasUpstream(a) {
		t.Error("hasUpstream() = false; the retry push should have recorded it")
	}
	localHead, err := headSHA(a)
	if err != nil {
		t.Fatalf("headSHA: %v", err)
	}
	if got := bareHead(t, bare); got != localHead {
		t.Errorf("remote HEAD = %q, want the rebased local commit %q", got, localHead)
	}
	// The remote's own task survived the rebase.
	if _, err := os.Stat(filepath.Join(a, ".monolog", "tasks", "01NE.json")); err != nil {
		t.Errorf("the remote's task should be present locally after the rebase: %v", err)
	}
}

// TestAutoPush_FetchFailureIsNotReportedAsARebase pins where Rebased flips.
//
// The TUI throws away its whole undo and redo history whenever Rebased is true,
// because a rebase rewrites local SHAs. A fetch rewrites nothing — so a push
// rejected against a remote whose fetch then fails (here: a reachable pushurl
// and a broken fetch url) must NOT cost the user their undo stack, on this
// mutation or on any of the ones that follow while the network is down.
func TestAutoPush_FetchFailureIsNotReportedAsARebase(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")
	pushTask(t, b, model.Task{
		ID: "01FF1", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	commitTask(t, a, model.Task{
		ID: "01FF2", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// Push reaches the real bare repo (and is rejected as non-fast-forward);
	// fetch goes to a path that does not exist and fails.
	gitRun(t, a, "config", "remote.origin.pushurl", bare)
	gitRun(t, a, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	res, err := AutoPush(a, DefaultPushTimeout)
	if err == nil {
		t.Fatal("AutoPush() error = nil, want the failed fetch surfaced")
	}
	if res.Rebased {
		t.Error("Rebased = true, want false: the call died in the fetch, which rewrites nothing")
	}
	if res.Pushed {
		t.Error("Pushed = true, want false")
	}
}

func TestPendingTaskWrites(t *testing.T) {
	tests := []struct {
		name  string
		file  string // path relative to .monolog/tasks/
		body  string
		track bool // commit it first, then modify
		want  bool
	}{
		{name: "clean repo", want: false},
		{
			name: "stray untracked .DS_Store is not a task write",
			file: ".DS_Store", body: "\x00\x01", want: false,
		},
		{
			name: "editor swap file is not a task write",
			file: ".01ABC.json.swp", body: "swap", want: false,
		},
		{
			name: "untracked task json is a pending create",
			file: "01ABC.json", body: `{"id":"01ABC"}`, want: true,
		},
		{
			name: "modified tracked task json is a pending edit",
			file: "01ABD.json", body: `{"id":"01ABD"}`, track: true, want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoPath := filepath.Join(t.TempDir(), "repo")
			if err := Init(repoPath, ""); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if tt.file != "" {
				abs := filepath.Join(repoPath, ".monolog", "tasks", tt.file)
				if err := os.WriteFile(abs, []byte(tt.body), 0o644); err != nil {
					t.Fatalf("write %s: %v", tt.file, err)
				}
				if tt.track {
					gitRun(t, repoPath, "add", "-A")
					gitRun(t, repoPath, "commit", "-m", "track "+tt.file)
					if err := os.WriteFile(abs, []byte(tt.body+" edited"), 0o644); err != nil {
						t.Fatalf("modify %s: %v", tt.file, err)
					}
				}
			}
			got, err := pendingTaskWrites(repoPath)
			if err != nil {
				t.Fatalf("pendingTaskWrites: %v", err)
			}
			if (len(got) > 0) != tt.want {
				t.Errorf("pendingTaskWrites() = %v, want pending = %v", got, tt.want)
			}
			if tt.want {
				want := tasksPrefix + tt.file
				if len(got) != 1 || got[0] != want {
					t.Errorf("pendingTaskWrites() = %v, want [%s]; the paths are handed "+
						"straight to `git add`, so they must be usable pathspecs", got, want)
				}
			}
		})
	}
}

// TestAutoPush_StrayFileDoesNotDeferTheRebaseForever is the user-visible half of
// TestTasksDirty. `git status --porcelain` lists untracked files and monolog's
// .gitignore excludes nothing, so a single .DS_Store dropped into
// .monolog/tasks/ by a Finder visit would make the deferral guard permanent:
// every rejection answered with ErrRebaseDeferred, nothing ever reaching the
// remote again, with no recovery short of the manual `monolog sync` this
// feature exists to remove.
func TestAutoPush_StrayFileDoesNotDeferTheRebaseForever(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")
	pushTask(t, b, model.Task{
		ID: "01SF1", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	commitTask(t, a, model.Task{
		ID: "01SF2", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	stray := filepath.Join(a, ".monolog", "tasks", ".DS_Store")
	if err := os.WriteFile(stray, []byte("\x00finder"), 0o644); err != nil {
		t.Fatalf("write .DS_Store: %v", err)
	}

	res, err := AutoPush(a, DefaultPushTimeout)
	if errors.Is(err, ErrRebaseDeferred) {
		t.Fatal("AutoPush() deferred the rebase over a stray .DS_Store; auto-push would never recover")
	}
	if err != nil {
		t.Fatalf("AutoPush() error = %v", err)
	}
	if !res.Rebased || !res.Pushed {
		t.Errorf("res = %+v, want a rebase followed by a successful push", res)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("the stray file should be left alone: %v", err)
	}
}

// TestAutoPush_DeferralIsBoundedAndCommitsTheOrphan is the bound on
// ErrRebaseDeferred. A task file that is uncommitted but NOT being written
// right now is an orphan — a real task whose commit failed — and deferring on
// it made auto-push stop working permanently: every later rejection answered
// with ErrRebaseDeferred, nothing ever reaching the remote again, with nothing
// telling the user that `monolog sync` clears it.
func TestAutoPush_DeferralIsBoundedAndCommitsTheOrphan(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")
	pushTask(t, b, model.Task{
		ID: "01ORB", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	commitTask(t, a, model.Task{
		ID: "01ORA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// A task written by store.Create whose AutoCommit then failed.
	orphanRel := filepath.Join(".monolog", "tasks", "01ORPHAN.json")
	orphan := filepath.Join(a, orphanRel)
	writeTaskJSON(t, orphan, model.Task{
		ID: "01ORPHAN", Title: "orphaned write", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-12T00:00:00Z",
	})

	// Freshly written, so it is indistinguishable from a store write whose
	// commit is still in flight: defer, but say which file and how to recover.
	res, err := AutoPush(a, DefaultPushTimeout)
	if !errors.Is(err, ErrRebaseDeferred) {
		t.Fatalf("AutoPush() error = %v, want ErrRebaseDeferred", err)
	}
	if !strings.Contains(err.Error(), orphanRel) {
		t.Errorf("error = %v, want it to name the offending path %s", err, orphanRel)
	}
	if !strings.Contains(err.Error(), "monolog sync") {
		t.Errorf("error = %v, want it to name the remedy; the user has no other way to "+
			"discover that a manual sync clears this", err)
	}
	if res.Rebased || res.Pushed {
		t.Errorf("res = %+v, want a pure deferral", res)
	}

	// Past the settle window nobody is mid-write, so the next push must commit
	// the orphan rather than defer again — losing a task the user created is
	// not an option, and deferring forever loses it just as thoroughly.
	old := time.Now().Add(-writeSettleWindow - time.Minute)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("backdate orphan: %v", err)
	}

	res, err = AutoPush(a, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v, want the settled orphan committed and pushed", err)
	}
	if !res.Rebased || !res.Pushed {
		t.Errorf("res = %+v, want a rebase followed by a successful push", res)
	}
	if status := gitOut(t, a, "status", "--porcelain"); status != "" {
		t.Errorf("status = %q, want a clean tree: the orphan must be committed", status)
	}
	if body := gitOut(t, a, "show", "HEAD:"+orphanRel); !strings.Contains(body, "orphaned write") {
		t.Errorf("HEAD:%s = %q, want the orphan's content committed", orphanRel, body)
	}
	if got, want := bareHead(t, bare), gitOut(t, a, "rev-parse", "HEAD"); got != want {
		t.Errorf("remote HEAD = %q, want the pushed local HEAD %q", got, want)
	}
}

// TestAutoPush_AutostashConflictStillPushes covers the case where the rebase
// itself succeeded and only reapplying the autostash conflicted. The worktree
// is clean and the commit is sitting on HEAD at that point, so bailing out
// before the retry push left the mutation unpushed — the exact staleness this
// feature exists to remove — over a conflict in a file that has nothing to do
// with it (config.json, which the settings modal writes without committing).
func TestAutoPush_AutostashConflictStillPushes(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")

	// B changes the shared config and pushes, putting the remote ahead.
	cfgRel := dirtyConfig(t, b, "{\n  \"theme\": \"dracula\"\n}\n")
	gitRun(t, b, "add", cfgRel)
	gitRun(t, b, "commit", "-m", "settings: dracula")
	gitRun(t, b, "push")

	// A commits a task (this is what must reach the remote) and has its own
	// uncommitted settings change, so the autostash pop will conflict.
	taskRel := commitTask(t, a, model.Task{
		ID: "01ASC", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	dirtyConfig(t, a, "{\n  \"theme\": \"solarized\"\n}\n")

	res, err := AutoPush(a, DefaultPushTimeout)
	if err == nil {
		t.Fatal("AutoPush() error = nil; the autostash conflict must still be reported")
	}
	if !errors.Is(err, ErrAutostashConflict) {
		t.Fatalf("AutoPush() error = %v, want it to wrap ErrAutostashConflict", err)
	}
	if !res.Pushed {
		t.Error("Pushed = false; the rebase completed and the tree was clean, so the retry " +
			"push must still run — otherwise the task sits local until some later mutation")
	}
	if !res.Rebased {
		t.Error("Rebased = false, want true")
	}
	// The task really is on the remote.
	if got, want := bareHead(t, bare), gitOut(t, a, "rev-parse", "HEAD"); got != want {
		t.Errorf("remote HEAD = %q, want the pushed local HEAD %q", got, want)
	}
	remoteTask := cloneOf(t, bare, "clone-c")
	if _, sErr := os.Stat(filepath.Join(remoteTask, taskRel)); sErr != nil {
		t.Errorf("task missing from a fresh clone of the remote: %v", sErr)
	}
	// And the user's settings change is still on disk, not silently reverted
	// under a TUI that already flashed "Settings saved".
	data, rErr := os.ReadFile(filepath.Join(a, cfgRel))
	if rErr != nil {
		t.Fatalf("read config.json: %v", rErr)
	}
	if !strings.Contains(string(data), "solarized") {
		t.Errorf("config.json = %s, want the user's uncommitted setting kept", data)
	}
}

// TestWriteSettleWindowOutlastsAPush pins the sizing invariant: AutoPush holds
// repoMu across BOTH of its pushes and, on the rejection path, the rebase
// fallback's fetch, and every one of those seconds is time a concurrent
// AutoCommitSHA spends blocked on that same mutex without touching its file. A
// window shorter than that sum would let AutoPush classify a write it is itself
// delaying as an orphan. (The rebase between the two pushes is deliberately
// unbounded and cannot be covered by any constant — see writeSettleWindow —
// which is exactly why the bounded part must not be a near miss.)
func TestWriteSettleWindowOutlastsAPush(t *testing.T) {
	bounded := 2*DefaultPushTimeout + DefaultFetchTimeout
	if writeSettleWindow <= bounded {
		t.Errorf("writeSettleWindow = %v, want > 2*DefaultPushTimeout+DefaultFetchTimeout (%v)",
			writeSettleWindow, bounded)
	}
}

// TestWritesInFlight_WindowIsClosedAtBothEnds pins the two anchors of the
// in-flight window: the start of AutoPush at the early end (so a write AutoPush
// is itself blocking is never aged by AutoPush's own network I/O) and the
// moment of the check at the late end (so a write that landed during the lock
// hold counts, while a badly skewed mtime still expires instead of deferring
// the rebase forever).
func TestWritesInFlight_WindowIsClosedAtBothEnds(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join(".monolog", "tasks", "01SKEW.json")
	writeTaskJSON(t, filepath.Join(dir, rel), model.Task{
		ID: "01SKEW", Title: "skewed", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-12T00:00:00Z",
	})
	now := time.Now()

	tests := []struct {
		name         string
		mtime        time.Time
		since, until time.Time
		want         bool
	}{
		{"just written", now, now, now, true},
		{
			// A write that landed while AutoPush was already running: newer
			// than `since`, so its age against that anchor is negative.
			name:  "written after AutoPush started",
			mtime: now.Add(writeSettleWindow / 2), since: now, until: now.Add(writeSettleWindow / 2),
			want: true,
		},
		{
			// The lock was held far longer than the window (a slow push, or a
			// sync ahead of it in the queue). Everything written up to the
			// check is still someone's in-flight write.
			name:  "written during a lock hold longer than the window",
			mtime: now.Add(-time.Second), since: now.Add(-5 * writeSettleWindow), until: now,
			want: true,
		},
		{"settled", now.Add(-writeSettleWindow - time.Minute), now, now, false},
		{
			// A badly skewed clock (an rsync/Dropbox restore, a device running
			// fast) must NOT read as in flight forever: past the window the
			// file is an orphan and gets committed, which is the whole point of
			// bounding the deferral.
			name:  "far future, beyond the window",
			mtime: now.Add(2*writeSettleWindow + time.Minute), since: now, until: now,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.Chtimes(filepath.Join(dir, rel), tt.mtime, tt.mtime); err != nil {
				t.Fatalf("chtimes: %v", err)
			}
			if got := writesInFlight(dir, []string{rel}, tt.since, tt.until); got != tt.want {
				t.Errorf("writesInFlight() = %v, want %v", got, tt.want)
			}
		})
	}
}

// slowReceivePack makes pushes to the fixture's remote take at least d by
// wrapping the remote end's git-receive-pack in a sleep. It is the only way to
// exercise the window AutoPush itself opens between a caller's store write and
// that caller's blocked AutoCommitSHA.
func slowReceivePack(t *testing.T, repoPath string, d time.Duration) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "slow-receive-pack")
	body := fmt.Sprintf("#!/bin/sh\nsleep %.1f\nexec git-receive-pack \"$@\"\n", d.Seconds())
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write receive-pack wrapper: %v", err)
	}
	gitRun(t, repoPath, "config", "remote.origin.receivepack", script)
}

// setSettleWindow shrinks writeSettleWindow for the duration of a test so a
// slow push can be simulated in seconds rather than in the half-minute the
// production value budgets for.
func setSettleWindow(t *testing.T, d time.Duration) {
	t.Helper()
	prev := writeSettleWindow
	writeSettleWindow = d
	t.Cleanup(func() { writeSettleWindow = prev })
}

// TestAutoPush_DoesNotAgeAWriteItIsItselfBlocking is the regression test for
// the settle window being measured at the wrong moment.
//
// A caller writes its task file and then blocks on repoMu inside
// AutoCommitSHA, which AutoPush holds across the whole push. Measuring the
// file's age when the rejection comes back — rather than when AutoPush started
// — ages that file by the entire push, so the write AutoPush is itself
// delaying is the first one to look "settled". AutoPush then sweeps it into a
// `recover:` commit, the caller's own commit fails with "nothing to commit",
// and a mutation that succeeded and reached the remote is reported to the user
// as a failure (and, in the TUI, silently desyncs the undo stack).
func TestAutoPush_DoesNotAgeAWriteItIsItselfBlocking(t *testing.T) {
	setSettleWindow(t, 300*time.Millisecond)

	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")
	pushTask(t, b, model.Task{
		ID: "01SLOWB", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	commitTask(t, a, model.Task{
		ID: "01SLOWA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// The concurrent write: on disk, its commit still queued behind repoMu.
	pendingRel := filepath.Join(".monolog", "tasks", "01SLOWP.json")
	writeTaskJSON(t, filepath.Join(a, pendingRel), model.Task{
		ID: "01SLOWP", Title: "still committing", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-12T00:00:00Z",
	})

	// The push burns far more than the settle window before it is rejected.
	slowReceivePack(t, a, 2*time.Second)

	res, err := AutoPush(a, DefaultPushTimeout)
	if !errors.Is(err, ErrRebaseDeferred) {
		t.Fatalf("AutoPush() error = %v, want ErrRebaseDeferred: a write from just before "+
			"AutoPush started is in flight no matter how long AutoPush blocked it", err)
	}
	if res.Rebased || res.Pushed {
		t.Errorf("res = %+v, want a pure deferral", res)
	}
	if status := gitOut(t, a, "status", "--porcelain", "--", pendingRel); status != "?? "+pendingRel {
		t.Errorf("status = %q, want %q: the in-flight write must not be swept into a "+
			"recover: commit under the caller that is about to commit it",
			status, "?? "+pendingRel)
	}
}

// TestAutoPush_DoesNotPushAnUnreadableTaskFile covers the orphan sweep meeting a
// file that is not a task. store.List fails on the FIRST unreadable file rather
// than skipping it, so committing one would break `monolog ls`, the TUI and the
// bot on every device instead of only on the one that produced it — the likely
// source being a crash mid-store.writeTask, which is a plain os.WriteFile.
func TestAutoPush_DoesNotPushAnUnreadableTaskFile(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	b := cloneOf(t, bare, "clone-b")
	pushTask(t, b, model.Task{
		ID: "01BADB", Title: "from B", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-11T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})
	commitTask(t, a, model.Task{
		ID: "01BADA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// A truncated write, settled long ago: the orphan branch will pick it up.
	badRel := filepath.Join(".monolog", "tasks", "01TRUNC.json")
	bad := filepath.Join(a, badRel)
	if err := os.WriteFile(bad, []byte(`{"id":"01TRUNC","title":"trunc`), 0o644); err != nil {
		t.Fatalf("write truncated task: %v", err)
	}
	// A good orphan alongside it, to pin that one bad file does not block the
	// recovery of the others.
	goodRel := filepath.Join(".monolog", "tasks", "01GOODO.json")
	writeTaskJSON(t, filepath.Join(a, goodRel), model.Task{
		ID: "01GOODO", Title: "recoverable", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-12T00:00:00Z",
	})
	old := time.Now().Add(-writeSettleWindow - time.Minute)
	for _, p := range []string{bad, filepath.Join(a, goodRel)} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("backdate %s: %v", p, err)
		}
	}

	res, err := AutoPush(a, DefaultPushTimeout)
	if !res.Pushed {
		t.Fatalf("res = %+v, err = %v; the push must still go through", res, err)
	}
	if err == nil || !strings.Contains(err.Error(), badRel) {
		t.Errorf("AutoPush() error = %v, want it to name the unreadable file %s", err, badRel)
	}
	if status := gitOut(t, a, "status", "--porcelain", "--", badRel); status != "?? "+badRel {
		t.Errorf("status = %q, want %q: the unreadable file must stay uncommitted", status, "?? "+badRel)
	}
	if body := gitOut(t, a, "show", "HEAD:"+goodRel); !strings.Contains(body, "recoverable") {
		t.Errorf("HEAD:%s = %q, want the readable orphan committed anyway", goodRel, body)
	}
	fresh := cloneOf(t, bare, "clone-c")
	if _, sErr := os.Stat(filepath.Join(fresh, badRel)); !os.IsNotExist(sErr) {
		t.Errorf("the unreadable file reached the remote (stat err = %v); every device's "+
			"store.List would fail on it", sErr)
	}
}

// TestAutoPush_SweepsAnOrphanOnTheFastForwardPath is the regression test for
// the orphan sweep running only after a rejected push.
//
// The single-device case never sees a rejection: every push fast-forwards. So
// a task whose commit failed — a store write that already landed, which is the
// exact file this recovery exists for — was committed by nothing and reached
// the remote never, while the user saw only the one `commit:` error.
func TestAutoPush_SweepsAnOrphanOnTheFastForwardPath(t *testing.T) {
	bare, a := setupRemoteFixture(t)
	commitTask(t, a, model.Task{
		ID: "01FFA", Title: "from A", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-10T00:00:00Z",
	})

	// A task written by store.Create whose AutoCommit then failed, settled long
	// enough ago that nobody can still be mid-write on it.
	orphanRel := filepath.Join(".monolog", "tasks", "01FFORPH.json")
	orphan := filepath.Join(a, orphanRel)
	writeTaskJSON(t, orphan, model.Task{
		ID: "01FFORPH", Title: "orphaned write", Status: "open", Schedule: "today",
		UpdatedAt: "2026-04-12T00:00:00Z", CreatedAt: "2026-04-12T00:00:00Z",
	})
	old := time.Now().Add(-writeSettleWindow - time.Minute)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("backdate orphan: %v", err)
	}

	res, err := AutoPush(a, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v", err)
	}
	if !res.Pushed {
		t.Fatalf("res = %+v, want Pushed", res)
	}
	if res.Rebased {
		t.Errorf("res = %+v, want no rebase: the remote is not ahead", res)
	}
	if status := gitOut(t, a, "status", "--porcelain"); status != "" {
		t.Errorf("status = %q, want a clean tree: the orphan must be committed", status)
	}
	if got, want := bareHead(t, bare), gitOut(t, a, "rev-parse", "HEAD"); got != want {
		t.Errorf("remote HEAD = %q, want the pushed local HEAD %q", got, want)
	}
	// Committed-but-unpushed is the same bug in a new shape, so the file is
	// checked through a fresh clone rather than through the local HEAD.
	fresh2 := cloneOf(t, bare, "clone-fresh")
	body, err := os.ReadFile(filepath.Join(fresh2, orphanRel))
	if err != nil {
		t.Fatalf("the orphan never reached the remote: %v", err)
	}
	if !strings.Contains(string(body), "orphaned write") {
		t.Errorf("remote %s = %q, want the orphan's content", orphanRel, body)
	}
}
