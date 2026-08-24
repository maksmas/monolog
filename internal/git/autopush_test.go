package git

import (
	"errors"
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

	if err := <-pushErr; err != nil {
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
