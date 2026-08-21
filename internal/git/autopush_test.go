package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maksmas/monolog/internal/model"
)

// bareHead returns the bare repo's HEAD SHA, or "" when it has no commits yet.
// --verify is required: a plain `rev-parse HEAD` in an empty repo exits 0 and
// echoes back "HEAD" instead of failing.
func bareHead(t *testing.T, bare string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

func TestAutoPush_SkipsWithoutUpstream(t *testing.T) {
	// A remote added by hand after `monolog init` has an origin but no tracking
	// branch. Without the upstream check this would warn on every mutation.
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

	hasRemote, err := HasRemote(repoPath)
	if err != nil {
		t.Fatalf("HasRemote: %v", err)
	}
	if !hasRemote {
		t.Fatal("fixture should have a remote configured")
	}
	if up, err := hasUpstream(repoPath); err != nil || up {
		t.Fatalf("hasUpstream() = %v, %v; want false, nil for a branch with no tracking ref", up, err)
	}

	res, err := AutoPush(repoPath, DefaultPushTimeout)
	if err != nil {
		t.Fatalf("AutoPush() error = %v, want nil when no upstream is configured", err)
	}
	if !res.Skipped {
		t.Error("Skipped = false, want true with a remote but no upstream")
	}
	if res.Pushed || res.Rebased || res.Resolved != 0 {
		t.Errorf("skip must be a pure no-op, got %+v", res)
	}
	if got := bareHead(t, bare); got != "" {
		t.Errorf("remote HEAD = %q, want empty: nothing may have been pushed", got)
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

	up, err := hasUpstream(a)
	if err != nil {
		t.Fatalf("hasUpstream() error = %v", err)
	}
	if !up {
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

	up, err := hasUpstream(a)
	if err != nil {
		t.Fatalf("hasUpstream() error = %v, want nil for a detached HEAD", err)
	}
	if up {
		t.Error("hasUpstream() = true, want false for a detached HEAD")
	}
}

func TestHasUpstream_NotARepo(t *testing.T) {
	// An unrecognized failure is surfaced rather than silently read as absence.
	dir := t.TempDir()
	if _, err := hasUpstream(dir); err == nil {
		t.Error("expected an error outside a git repository")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("fixture dir should not be a repo: %v", err)
	}
}
