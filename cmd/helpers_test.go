package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/git"
)

// pushCall records a single invocation of the auto-push seam so tests can
// assert on the repo path and the timeout budget the CLI passes in.
type pushCall struct {
	repoPath string
	timeout  time.Duration
}

// stubAutoPushFn replaces the package-level autoPushFn with a recorder so no
// test touches the network. The returned slice is appended to on each call;
// res/retErr are what the seam hands back.
func stubAutoPushFn(t *testing.T, res git.PushResult, retErr error) *[]pushCall {
	t.Helper()
	var calls []pushCall
	prev := autoPushFn
	autoPushFn = func(repoPath string, timeout time.Duration) (git.PushResult, error) {
		calls = append(calls, pushCall{repoPath: repoPath, timeout: timeout})
		return res, retErr
	}
	t.Cleanup(func() { autoPushFn = prev })
	return &calls
}

// loadAutoPushConfig pins the config package's in-session auto_push value, so
// the pure pushAfter tests do not inherit whatever the previously-run test in
// this package happened to load. Command-level tests do not need it: every
// mutation command re-runs config.Load against its own repo via openStore
// before it reaches pushAfter.
func loadAutoPushConfig(t *testing.T, enabled bool) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".monolog"), 0o755); err != nil {
		t.Fatalf("mkdir .monolog: %v", err)
	}
	body := `{"auto_push": false}` + "\n"
	if enabled {
		body = `{"auto_push": true}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, ".monolog", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
}

// runCLI executes one root command with fresh stdout/stderr buffers.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	rootCmd := NewRootCmd()
	out := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs(args)
	err = rootCmd.Execute()
	return out.String(), errBuf.String(), err
}

// --- pushAfter unit tests ---

func TestPushAfter_CallsSeamWithRepoPathAndCLITimeout(t *testing.T) {
	loadAutoPushConfig(t, true)
	calls := stubAutoPushFn(t, git.PushResult{Pushed: true}, nil)

	w := new(bytes.Buffer)
	pushAfter(w, "/some/repo")

	if len(*calls) != 1 {
		t.Fatalf("expected 1 push call, got %d", len(*calls))
	}
	if (*calls)[0].repoPath != "/some/repo" {
		t.Errorf("repoPath: got %q, want %q", (*calls)[0].repoPath, "/some/repo")
	}
	if (*calls)[0].timeout != git.CLIPushTimeout {
		t.Errorf("timeout: got %v, want %v (CLI budget, not the TUI's)", (*calls)[0].timeout, git.CLIPushTimeout)
	}
	if w.String() != "" {
		t.Errorf("successful push must be silent, got: %q", w.String())
	}
}

func TestPushAfter_DisabledByEnvDoesNotCallSeam(t *testing.T) {
	// On-disk true, env kill switch on: the env var must win.
	loadAutoPushConfig(t, true)
	t.Setenv("MONOLOG_NO_AUTOPUSH", "1")
	calls := stubAutoPushFn(t, git.PushResult{Pushed: true}, nil)

	w := new(bytes.Buffer)
	pushAfter(w, "/some/repo")

	if len(*calls) != 0 {
		t.Errorf("expected no push calls when auto-push is disabled, got %d", len(*calls))
	}
	if w.String() != "" {
		t.Errorf("disabled auto-push must be silent, got: %q", w.String())
	}
}

func TestPushAfter_SkippedProducesNoWarning(t *testing.T) {
	loadAutoPushConfig(t, true)
	// No remote / no upstream is a supported local-only configuration, not a
	// misconfiguration to nag about on every mutation.
	calls := stubAutoPushFn(t, git.PushResult{Skipped: true}, nil)

	w := new(bytes.Buffer)
	pushAfter(w, "/some/repo")

	if len(*calls) != 1 {
		t.Fatalf("expected 1 push call, got %d", len(*calls))
	}
	if w.String() != "" {
		t.Errorf("skipped push must produce no output, got: %q", w.String())
	}
}

func TestPushAfter_FailureWarnsAndSwallows(t *testing.T) {
	loadAutoPushConfig(t, true)
	stubAutoPushFn(t, git.PushResult{}, errors.New("dial tcp: no route to host"))

	w := new(bytes.Buffer)
	pushAfter(w, "/some/repo") // must not panic, must not exit

	got := w.String()
	if !strings.Contains(got, "push failed") || !strings.Contains(got, "no route to host") {
		t.Errorf("expected a warning naming the failure, got: %q", got)
	}
}

// --- Command-level wiring ---

func TestAddCommand_PushFailureIsNonFatalAndCommitSurvives(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	calls := stubAutoPushFn(t, git.PushResult{}, errors.New("network down"))

	stdout, stderr, err := runCLI(t, "add", "Offline task")
	// Critical: the command MUST succeed (exit 0) even when the push fails.
	if err != nil {
		t.Fatalf("add must succeed despite push failure, got: %v\nstderr: %s", err, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 push call, got %d", len(*calls))
	}
	if !strings.Contains(stdout, "Added: Offline task") {
		t.Errorf("success line missing from stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "push failed") || !strings.Contains(stderr, "network down") {
		t.Errorf("expected push warning on stderr, got: %q", stderr)
	}

	// The commit is durable locally — a failed push rolls nothing back.
	out, gerr := exec.Command("git", "-C", dir, "log", "--oneline", "-1").Output()
	if gerr != nil {
		t.Fatalf("git log failed: %v", gerr)
	}
	if !strings.Contains(string(out), "add: Offline task") {
		t.Errorf("commit should survive a failed push, git log -1: %s", out)
	}
}

// TestAddCommand_PushRunsAfterSuccessLine is the ordering regression guard:
// inserting the push before the Fprintf would make `monolog add` hang on a bad
// network before printing anything, which is the whole reason pushAfter exists.
func TestAddCommand_PushRunsAfterSuccessLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	out := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"add", "Ordering guard"})

	// The stub inspects stdout at push time and blocks briefly, standing in for
	// the network I/O a real push performs.
	var stdoutAtPushTime string
	prev := autoPushFn
	autoPushFn = func(string, time.Duration) (git.PushResult, error) {
		stdoutAtPushTime = out.String()
		time.Sleep(10 * time.Millisecond)
		return git.PushResult{}, errors.New("slow network")
	}
	t.Cleanup(func() { autoPushFn = prev })

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add error = %v\nstderr: %s", err, errBuf.String())
	}

	if !strings.Contains(stdoutAtPushTime, "Added: Ordering guard") {
		t.Errorf("success line must be printed BEFORE the push runs; stdout at push time was %q", stdoutAtPushTime)
	}
}

func TestAddCommand_ConfigAutoPushFalseSkipsPush(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Exercise the real config path: on-disk "auto_push": false, picked up by
	// the config.Load inside openStore.
	cfgPath := filepath.Join(dir, ".monolog", "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"auto_push": false}`+"\n"), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	calls := stubAutoPushFn(t, git.PushResult{Pushed: true}, nil)

	stdout, stderr, err := runCLI(t, "add", "No push please")
	if err != nil {
		t.Fatalf("add error = %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Added: No push please") {
		t.Errorf("success line missing from stdout: %q", stdout)
	}
	if len(*calls) != 0 {
		t.Errorf(`expected no push with "auto_push": false on disk, got %d calls`, len(*calls))
	}
}

// TestAddCommand_EnvDisablesPushEndToEnd is the env-var twin of
// TestAddCommand_ConfigAutoPushFalseSkipsPush. The kill switch is only useful
// if it survives the whole command path — git.Init writes "auto_push": true
// into the repo, openStore loads it, and MONOLOG_NO_AUTOPUSH=1 still has to
// win by the time pushAfter runs.
func TestAddCommand_EnvDisablesPushEndToEnd(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	t.Setenv("MONOLOG_NO_AUTOPUSH", "1")

	calls := stubAutoPushFn(t, git.PushResult{Pushed: true}, nil)

	stdout, stderr, err := runCLI(t, "add", "Env kill switch")
	if err != nil {
		t.Fatalf("add error = %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Added: Env kill switch") {
		t.Errorf("success line missing from stdout: %q", stdout)
	}
	if len(*calls) != 0 {
		t.Errorf("expected no push with MONOLOG_NO_AUTOPUSH=1, got %d calls", len(*calls))
	}
	// The mutation itself is unaffected: the commit still lands locally.
	out, gerr := exec.Command("git", "-C", dir, "log", "--oneline", "-1").Output()
	if gerr != nil {
		t.Fatalf("git log failed: %v", gerr)
	}
	if !strings.Contains(string(out), "add: Env kill switch") {
		t.Errorf("disabling auto-push must not disable the commit, git log -1: %s", out)
	}
}

// TestCLIMutations_EachPushesExactlyOnce drives every mutation command end to
// end and asserts the seam fires exactly once. This is the failure mode of a
// mechanical six-file edit: a missed (or duplicated) pushAfter call in one
// command is invisible to any per-command test.
func TestCLIMutations_EachPushesExactlyOnce(t *testing.T) {
	cases := []struct {
		name    string
		args    func(id string) []string
		wantOut string
	}{
		{
			name:    "add",
			args:    func(string) []string { return []string{"add", "Fresh task"} },
			wantOut: "Added:",
		},
		{
			name:    "edit",
			args:    func(id string) []string { return []string{"edit", id, "--title", "Renamed"} },
			wantOut: "Edited:",
		},
		{
			name:    "done",
			args:    func(id string) []string { return []string{"done", id} },
			wantOut: "Done:",
		},
		{
			name:    "mv",
			args:    func(id string) []string { return []string{"mv", id, "--top"} },
			wantOut: "Moved:",
		},
		{
			name:    "rm",
			args:    func(id string) []string { return []string{"rm", id} },
			wantOut: "Removed:",
		},
		{
			name:    "note",
			args:    func(id string) []string { return []string{"note", id, "a note"} },
			wantOut: "Note added:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "monolog")
			initTestRepo(t, dir)

			// Two seed tasks so `mv --top` genuinely changes a position instead
			// of short-circuiting on "Already at top". Identifiers are the full
			// ULID: two tasks created in the same millisecond share an 8-char
			// prefix, which store.Resolve rejects as ambiguous.
			addTestTask(t, dir, "Seed one")
			id := addTestTask(t, dir, "Seed two")

			// Stub only after seeding, so the seeds' own pushes are not counted.
			calls := stubAutoPushFn(t, git.PushResult{Pushed: true}, nil)

			stdout, stderr, err := runCLI(t, tc.args(id)...)
			if err != nil {
				t.Fatalf("%s error = %v\nstderr: %s", tc.name, err, stderr)
			}
			if !strings.Contains(stdout, tc.wantOut) {
				t.Fatalf("%s did not mutate (stdout %q lacks %q)", tc.name, stdout, tc.wantOut)
			}
			if len(*calls) != 1 {
				t.Fatalf("%s: expected exactly 1 push, got %d", tc.name, len(*calls))
			}
			if (*calls)[0].repoPath != dir {
				t.Errorf("%s: push repoPath: got %q, want %q", tc.name, (*calls)[0].repoPath, dir)
			}
			if (*calls)[0].timeout != git.CLIPushTimeout {
				t.Errorf("%s: push timeout: got %v, want %v", tc.name, (*calls)[0].timeout, git.CLIPushTimeout)
			}
		})
	}
}

// TestCLIMutations_NoCommitNoPush pins the other half of the wiring: the
// early-return paths that make no commit must not push either.
func TestCLIMutations_NoCommitNoPush(t *testing.T) {
	t.Run("already_done", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "monolog")
		initTestRepo(t, dir)
		id := addTestTask(t, dir, "Done twice")
		if _, stderr, err := runCLI(t, "done", id); err != nil {
			t.Fatalf("first done error = %v\nstderr: %s", err, stderr)
		}

		calls := stubAutoPushFn(t, git.PushResult{Pushed: true}, nil)
		stdout, stderr, err := runCLI(t, "done", id)
		if err != nil {
			t.Fatalf("second done error = %v\nstderr: %s", err, stderr)
		}
		if !strings.Contains(stdout, "Already done") {
			t.Fatalf("expected 'Already done', got %q", stdout)
		}
		if len(*calls) != 0 {
			t.Errorf("no commit was made, so no push should happen; got %d calls", len(*calls))
		}
	})

	t.Run("mv_already_at_top", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "monolog")
		initTestRepo(t, dir)
		id := addTestTask(t, dir, "Only task")

		calls := stubAutoPushFn(t, git.PushResult{Pushed: true}, nil)
		if _, stderr, err := runCLI(t, "mv", id, "--top"); err != nil {
			t.Fatalf("mv error = %v\nstderr: %s", err, stderr)
		}
		// A lone task in its bucket is already at the top: the command returns
		// before rebalanceAndCommit, so there is nothing to push.
		if _, stderr, err := runCLI(t, "mv", id, "--top"); err != nil {
			t.Fatalf("second mv error = %v\nstderr: %s", err, stderr)
		}
		if len(*calls) != 0 {
			t.Errorf("no-op mv should not push, got %d calls", len(*calls))
		}
	})
}
