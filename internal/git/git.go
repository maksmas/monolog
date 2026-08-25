package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maksmas/monolog/internal/model"
)

// tasksPrefix is the repo-relative path prefix of task JSON files.
const tasksPrefix = ".monolog/tasks/"

// repoMu serializes the mutating git entry points in this package:
// AutoCommit, AutoCommitSHA, Revert, RevertSHA, Sync and AutoPush.
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
// Deliberately NOT locked: PullRebase and Push are lock-free primitives, since
// Sync calls Push while already holding this mutex (sync.Mutex is not
// reentrant) and PullRebase's only caller, internal/telegram, serializes all of
// its git work under its own handler mutex. A future in-process caller — the
// plan's "periodic background pull ticker in the TUI", say — must either take
// repoMu itself or route through Sync, or it will race AutoPush's rebase in
// exactly the way this mutex exists to prevent.
//
// Holding it costs latency: AutoPush keeps the lock across its push and any
// rebase fallback, so a mutation's commit can wait on a slow network. Every
// network step under the lock is therefore bounded (see pushWithTimeout and
// pullRebaseResolving's context-bounded fetch); the ceiling is two push
// timeouts plus one fetch timeout, not "until ssh gives up".
//
// The one exception is the interactive Sync, which deliberately runs unbounded
// with credential prompting left on — but it is only reachable from the
// one-shot `monolog sync` process, where nothing else contends for the lock.
// Every in-process caller that shares this mutex with a background push (the
// TUI, the Telegram bot) uses SyncUnattended, which is bounded.
//
// The lock is per-process only, and the cross-process failure mode is DATA
// LOSS, not just a warning. A second monolog process (a Raycast capture, a
// `monolog add` in another terminal, the Claude skill) can now run
// `pull --rebase --autostash` of its own, which moves HEAD and rewrites the
// worktree while this process is mid-commit — .git/index.lock guards the index,
// not the rebase sequence. Concretely: while one process sits on a conflicted
// rebase, another's `git add` still succeeds but its `git commit` fails with
// "Committing is not possible because you have unmerged files", and the first
// process's `git rebase --abort` then resets the index and worktree, deleting
// that staged file with no warning anywhere. autoCommit unstages on a failed
// commit for exactly this reason, which saves a NEW task file (it survives the
// abort as untracked) but not an uncommitted modification to a tracked one —
// abort hard-resets tracked paths. Cross-process serialization is out of scope
// for this design; that residual window is the known cost.
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
	// Deliberately pathspec-LESS. `git commit -m msg -- <files>` would narrow
	// the commit to what this call staged, which is otherwise attractive: with
	// two monolog processes running, the first to reach the commit currently
	// sweeps the other's staged file into its own, and the loser then reports
	// "nothing added to commit" for a task that is on disk, in the commit and on
	// the remote.
	//
	// It cannot be used, because a pathspec makes it a PARTIAL commit, and a
	// partial commit does not refuse the way a whole-index commit does. While
	// another process sits on a conflicted rebase, `git commit -m msg` fails
	// with "Committing is not possible because you have unmerged files" — the
	// refusal the unstaging below depends on — whereas the pathspec form
	// succeeds and lands the write on the detached rebase HEAD, which that
	// process's `git rebase --abort` then throws away, file and all. Verified:
	// the task file is gone from the worktree after the abort. Trading a wrong
	// error message for a silently destroyed task is not a trade worth making;
	// cross-process serialization is the real fix and is out of scope here.
	if err := run(repoPath, "git", "commit", "-m", message); err != nil {
		// Unstage what this call staged. A commit fails outright while another
		// monolog process holds the repo mid-rebase ("Committing is not
		// possible because you have unmerged files"), and that process then
		// runs `git rebase --abort`, which resets the index and the worktree:
		// a staged-but-uncommitted new task file is deleted outright, silently.
		// Unstaged, the same file survives the abort as an untracked write that
		// the next auto-push commits (see pendingTaskWrites). Best-effort — a
		// failure here leaves exactly the state that existed before.
		//
		// This only saves NEW files. An uncommitted modification to a tracked
		// task file is still in the worktree, and `rebase --abort` hard-resets
		// tracked paths; nothing short of cross-process locking covers that.
		for _, f := range files {
			_ = run(repoPath, "git", "reset", "-q", "--", f)
		}
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

// DefaultFetchTimeout bounds the network half of an automatic pull-rebase.
// Every caller of PullRebase is an unattended background path (the Telegram
// bot's ticker and its per-command freshness gate), and each one holds a lock
// while it runs, so a half-open connection must not be able to stall it
// indefinitely.
const DefaultFetchTimeout = 15 * time.Second

// PullRebase fetches (bounded by fetchTimeout, with interactive prompting
// disabled) and rebases the current branch onto its upstream.
//
// fetchTimeout <= 0 falls back to DefaultFetchTimeout. Callers on a latency
// budget — the Telegram bot must answer a callback query within seconds or the
// user's button spins — pass something shorter.
//
// Lock-free by design — see repoMu. Its only caller, internal/telegram,
// serializes all of its git work under its own handler mutex.
func PullRebase(repoPath string, fetchTimeout time.Duration) error {
	if fetchTimeout <= 0 {
		fetchTimeout = DefaultFetchTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	if _, err := runOut(ctx, repoPath, noPromptEnv, "git", "fetch"); err != nil {
		return err
	}
	// The upstream is named explicitly, exactly as `git pull --rebase` does it
	// — see rebaseTarget.
	return run(repoPath, "git", "rebase", upstreamRef{}.rebaseTarget())
}

// Push runs git push. Lock-free by design — see repoMu; Sync calls it while
// already holding the mutex.
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
// after the local commit.
//
// This is the INTERACTIVE variant, for `monolog sync`: its network steps are
// unbounded and credential prompting is left on, because the user ran a
// foreground command and can answer a prompt or Ctrl-C a slow fetch. Callers
// that cannot do either must use SyncUnattended.
func Sync(repoPath string) (SyncResult, error) {
	return syncRepo(repoPath, true)
}

// SyncUnattended is Sync for callers that own the terminal and cannot answer a
// credential prompt: the TUI (Bubble Tea holds the tty in raw mode on the
// alt-screen, so git's /dev/tty prompt is unanswerable and invisible) and the
// Telegram bot (no terminal at all).
//
// Both network steps are bounded and prompting is disabled, which matters
// beyond the sync itself: Sync holds repoMu, so a fetch or push blocked on an
// unanswerable prompt would also block every subsequent mutation's commit —
// the TUI would silently stop saving until the process was killed.
func SyncUnattended(repoPath string) (SyncResult, error) {
	return syncRepo(repoPath, false)
}

// syncRepo is the shared body of Sync/SyncUnattended. interactive selects the
// prompt-and-wait behavior described on each.
func syncRepo(repoPath string, interactive bool) (SyncResult, error) {
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
	ctx := context.Background()
	env := noPromptEnv
	if interactive {
		env = nil
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultFetchTimeout)
		defer cancel()
	}
	out, err := pullRebaseResolving(ctx, repoPath, false, env, upstreamRef{})
	if err != nil {
		return res, err
	}
	res.Resolved = out.Resolved

	if interactive {
		if err := Push(repoPath); err != nil {
			return res, fmt.Errorf("push: %w", err)
		}
		return res, nil
	}
	if _, err := pushWithTimeout(repoPath, DefaultPushTimeout, "push"); err != nil {
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

// gitEnv returns the process environment with git's own messages pinned to the
// C locale, plus any extra entries.
//
// Pinning the locale is load-bearing, not cosmetic: git has no distinguishing
// exit status for a push rejection, so isNonFastForward classifies what git
// printed. A git built with NLS under a non-English LANG prints translated
// prose, which would silently turn every rejection into an unrecoverable
// "push failed" warning on every mutation.
func gitEnv(extra ...string) []string {
	env := append(os.Environ(), "LC_ALL=C", "LANG=C")
	return append(env, extra...)
}

// noPromptEnv disables git's interactive credential prompting. Unattended
// pushes must never grab the terminal: git and ssh open /dev/tty directly
// rather than reading stdin, so an HTTPS remote with no credential helper would
// write a prompt over the Bubble Tea alt-screen and eat the user's keystrokes
// after every mutation. With prompting off git fails fast instead, which
// isNonFastForward classifies as a non-rejection error and the caller surfaces
// as a warning.
//
// Deliberately NOT applied to `monolog sync` or `monolog init`, where the user
// is at the keyboard and a credential prompt is the expected behavior.
var noPromptEnv = []string{"GIT_TERMINAL_PROMPT=0"}

// waitDelay bounds how long Wait blocks on the output pipes after a command's
// process is gone. Without it a killed `git push` still waits for every
// inherited writer to close its descriptor — and the ssh or credential-helper
// grandchild git spawned holds one, so a half-open connection would keep the
// call (and repoMu) alive long past the context deadline.
const waitDelay = 2 * time.Second

// credentialsRe matches the userinfo component of a remote URL. git echoes the
// full URL back in "fatal: unable to access '<url>'", so an HTTPS remote with
// an embedded token leaks it into whatever surfaces the error.
//
// The character class excludes "/" and whitespace but NOT "@", so the match
// runs to the LAST "@" of the authority: a password may legally contain an
// unencoded "@" (git accepts `https://user:p@ss@host/`), and stopping at the
// first one would redact `user:p` and leak the rest of the password.
var credentialsRe = regexp.MustCompile(`://[^/\s]+@`)

// redactCredentials strips embedded credentials out of git output before it is
// surfaced to a terminal, a log or the TUI status bar.
func redactCredentials(s string) string {
	return credentialsRe.ReplaceAllString(s, "://***@")
}

// shortErrorLimit caps ShortError's output. Long enough for a header line plus
// git's "fatal:"/"error:" line, short enough not to bury a CLI success line.
const shortErrorLimit = 240

// ShortError renders a git error for a one-line surface: the CLI's `push
// failed:` warning and the TUI status bar.
//
// Every error out of this package carries git's whole combined output (see
// gitError), and for a rejected push or a stopped rebase most of that is a
// four-line "hint:" block telling a human at a terminal to run
// `git rebase --continue` or `git pull` by hand — advice that is actively wrong
// here, since monolog is doing the rebase itself, and that buries the one line
// that says what went wrong. So the hint block is dropped, what remains is
// collapsed onto a single line, and the result is capped.
//
// It is deliberately not a "first line only" trim: gitError's first line is the
// command and its exit status, and the diagnosis ("fatal: could not read
// Username…") is on the second.
func ShortError(err error) string {
	if err == nil {
		return ""
	}
	var kept []string
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "hint:") {
			continue
		}
		kept = append(kept, line)
	}
	s := strings.Join(kept, " ")
	if r := []rune(s); len(r) > shortErrorLimit {
		s = string(r[:shortErrorLimit]) + "…"
	}
	return s
}

// gitError formats a failed command uniformly, with credentials redacted out of
// both the argument list and git's output.
func gitError(name string, args []string, err error, out []byte) error {
	return fmt.Errorf("%s: %w\n%s",
		redactCredentials(fmt.Sprintf("%s %v", name, args)),
		err,
		redactCredentials(string(out)))
}

// run executes a command in the given directory, returning an error with
// combined output if the command fails.
func run(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return gitError(name, args, err, out)
	}
	return nil
}

// runOut is run's output-returning, context-aware sibling. The combined output
// is returned on both the success and the failure path so callers can classify
// a failure by what git printed (git signals push rejections on stderr with a
// generic exit code, so there is no status to switch on).
//
// extraEnv is appended to the C-locale environment; pass noPromptEnv for the
// unattended paths.
func runOut(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(extraEnv...)
	// Bound the post-kill pipe wait so ctx actually is the ceiling — see waitDelay.
	cmd.WaitDelay = waitDelay
	out, err := cmd.CombinedOutput()
	if err != nil && !waitDelayAfterSuccess(cmd, err) {
		return string(out), gitError(name, args, err, out)
	}
	return string(out), nil
}

// waitDelayAfterSuccess reports whether err is nothing but the WaitDelay timer
// firing on a command that itself exited 0.
//
// WaitDelay bounds two different things, and only one of them is a failure:
// a process that outlives its killed context, and a process that exits cleanly
// while a grandchild still holds the inherited output pipe. git spawns exactly
// such grandchildren — git-credential-cache--daemon inherits stderr and lives
// for its 900s idle timeout — so on an HTTPS remote with credential.helper=cache
// a perfectly successful push returns exec.ErrWaitDelay. Without this check
// that push is reported to the user as "push failed: ... WaitDelay expired
// before I/O complete" despite having reached the remote.
//
// ProcessState.Success() is what discriminates: a context kill leaves a signal
// status, so a genuinely hung command still surfaces as an error.
func waitDelayAfterSuccess(cmd *exec.Cmd, err error) bool {
	return errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success()
}

// upstreamRef names the branch a rebase should replay onto. The zero value
// means "the branch's own configured upstream", which is the normal case;
// AutoPush's first push fills it in because a branch that has no upstream yet
// still has a remote branch to rebase onto.
type upstreamRef struct {
	remote string // e.g. "origin"
	branch string // e.g. "main"
}

// explicit reports whether the ref names a concrete remote branch.
func (u upstreamRef) explicit() bool { return u.remote != "" && u.branch != "" }

// fetchArgs returns the `git fetch` argument list that makes rebaseTarget
// resolvable. The explicit form matters on a branch with no upstream: a bare
// `git fetch` there falls back to "origin", which is wrong (or absent) when the
// sole remote is named something else.
func (u upstreamRef) fetchArgs() []string {
	if u.explicit() {
		return []string{"fetch", u.remote, u.branch}
	}
	return []string{"fetch"}
}

// rebaseTarget returns the ref to rebase onto.
//
// It is always passed explicitly, never left for `git rebase` to infer, for two
// reasons. A branch with no upstream cannot infer one at all — bare `git rebase`
// dies with "There is no tracking information for the current branch", which is
// exactly the dead end AutoPush's --set-upstream first push used to fall into.
// And git turns --fork-point ON when no upstream argument is given and OFF when
// one is; `git pull --rebase` passes the ref, so naming it here keeps the
// semantics this code replaced (fork-point can silently drop local commits if
// the upstream is ever rewound).
func (u upstreamRef) rebaseTarget() string {
	if u.explicit() {
		return u.remote + "/" + u.branch
	}
	return "@{upstream}"
}

// maxRebaseRounds bounds pullRebaseResolving's resolve/--continue loop. One
// round per conflicting local commit is the normal shape, so the ceiling is not
// a budget anyone is expected to spend: it exists so a pathological rebase still
// terminates into RebaseAbort rather than spinning while holding repoMu.
const maxRebaseRounds = 50

// rebaseOutcome reports what pullRebaseResolving did, alongside its error.
type rebaseOutcome struct {
	// Resolved is the number of task-file conflicts auto-resolved.
	Resolved int
	// Started is true once `git rebase` has been invoked, i.e. from the point
	// local history may have been rewritten. It stays false when the call fails
	// in the fetch, which touches nothing local — the distinction is what keeps
	// AutoPush from telling the TUI to throw away its undo/redo stacks on every
	// mutation while the network is down.
	Started bool
}

// pullRebaseResolving pulls with rebase (autostashing local modifications when
// autostash is true), auto-resolving task-file conflicts via ResolveConflicts.
// Extracted from Sync so the manual sync path and the automatic push path share
// identical conflict semantics.
//
// It is `git pull --rebase` split in two on purpose. The fetch is the only
// network step and is bounded by ctx: killing a fetch is safe, it leaves no
// .git/rebase-merge behind. The rebase that follows is purely local and
// deliberately runs unbounded — killing git mid-rebase would strand the repo in
// a state nothing in this package recovers from. Callers that must not block
// indefinitely (AutoPush holds repoMu across this call) get their ceiling from
// ctx; extraEnv reaches the fetch only, since the rebase talks to nobody.
//
// Errors are returned already wrapped ("pull: ", "resolve conflicts: ",
// "rebase continue: ", "autostash: ") so callers can surface them verbatim. On
// a resolution or continue failure the rebase is aborted, leaving the worktree
// clean.
func pullRebaseResolving(ctx context.Context, repoPath string, autostash bool, extraEnv []string, up upstreamRef) (rebaseOutcome, error) {
	var res rebaseOutcome
	if _, err := runOut(ctx, repoPath, extraEnv, "git", up.fetchArgs()...); err != nil {
		return res, fmt.Errorf("pull: %w", err)
	}
	args := []string{"rebase"}
	if autostash {
		args = append(args, "--autostash")
	}
	args = append(args, up.rebaseTarget())
	res.Started = true
	if err := run(repoPath, "git", args...); err != nil {
		rebasing, rbErr := IsRebasing(repoPath)
		if rbErr != nil || !rebasing {
			return res, fmt.Errorf("pull: %w", err)
		}
		// A rebase stops once per conflicting local commit, so resolving has to
		// loop. Two devices that each made two commits touching the same tasks
		// is the ordinary case this whole feature targets, and handling only the
		// first stop aborted the rebase and returned an error that every later
		// auto-push — and `monolog sync`, the documented escape hatch — then
		// reproduced identically, forever.
		//
		// The running count stays local and is published to res only once the
		// rebase completes: every bail below aborts, which rolls the
		// resolutions back. Reporting them anyway told the user "auto-resolved
		// N conflicts" — which callers render as "N edits were discarded to
		// keep the newer one" — on the one path where nothing was merged and
		// nothing was discarded.
		resolved := 0
		for round := 0; ; round++ {
			n, resErr := ResolveConflicts(repoPath)
			if resErr != nil {
				_ = RebaseAbort(repoPath)
				return res, fmt.Errorf("resolve conflicts: %w", resErr)
			}
			resolved += n
			contErr := RebaseContinue(repoPath)
			if contErr == nil {
				res.Resolved = resolved
				break
			}
			// --continue exits non-zero both when it stopped at the NEXT
			// conflicting commit (retryable) and when the rebase cannot be
			// advanced at all (not). Retry only while all three hold: the repo
			// is still mid-rebase, this round actually resolved something (n==0
			// means the next round would do the same nothing and fail
			// identically), and the round ceiling is not reached.
			rebasing, rbErr := IsRebasing(repoPath)
			if rbErr != nil || !rebasing || n == 0 || round+1 >= maxRebaseRounds {
				_ = RebaseAbort(repoPath)
				return res, fmt.Errorf("rebase continue: %w", contErr)
			}
		}
	}
	if autostash {
		if err := recoverAutostash(repoPath); err != nil {
			return res, err
		}
	}
	return res, nil
}

// ErrAutostashConflict reports that the rebase itself completed but reapplying
// the autostash conflicted. It is a warning about files OUTSIDE the task set —
// the worktree is clean and usable afterwards — so callers must keep going
// rather than treating it as a failed rebase (AutoPush still retries its push).
var ErrAutostashConflict = errors.New("autostash conflict")

// recoverAutostash cleans up after a conflicting autostash pop.
//
// `git rebase --autostash` exits ZERO when the rebase itself succeeded but
// reapplying the stash at the end conflicted; it prints "Applying autostash
// resulted in conflicts. Your changes are safe in the stash." and leaves the
// worktree with conflict markers and unmerged index entries. Nothing downstream
// notices: the caller reports success, the TUI flashes "Synced", and then every
// later `git commit` fails with "Committing is not possible because you have
// unmerged files" while `monolog sync`'s `git add -A` happily stages the
// conflict-marker text and pushes it to every device.
//
// So the unmerged paths are checked directly rather than trusting the exit
// status. Recovery puts each path back exactly where it stood before the
// rebase: stage 3 (the stashed copy — the user's own uncommitted change) is
// written to the worktree and `git reset -- <path>` returns the index entry to
// HEAD, leaving an ordinary unstaged modification.
//
// It deliberately does NOT restore HEAD over the worktree. In practice the only
// file that reaches here is .monolog/config.json — the settings modal writes it
// without committing (see applySettings) — and discarding it silently reverted
// a setting the running TUI still believed was in effect and had already
// confirmed as saved. The incoming version is not lost by keeping the local
// one: it is the committed HEAD version, one `git checkout HEAD -- <path>`
// away, which the returned error names. Task files cannot reach here at all
// (AutoPush commits or defers a pending task write before rebasing, see
// pendingTaskWrites).
//
// The autostash stash entry git left behind is deliberately not dropped. For a
// path restored from stage 3 it is merely a backup of what is already back in
// the worktree; for a path with no stage 3 (a delete/modify conflict, where the
// only way to leave the repo committable is to take HEAD and lose the local
// change) it is the ONLY remaining copy. The returned error names it either
// way, and a stale stash entry costs nothing.
func recoverAutostash(repoPath string) error {
	paths, err := unmergedPaths(repoPath)
	if err != nil {
		return fmt.Errorf("autostash: %w", err)
	}
	if len(paths) == 0 {
		return nil
	}
	// Split by what actually happened to each path, because the two outcomes
	// need opposite advice: a kept path is on disk and the INCOMING version is
	// the one to fetch back, a dropped path is the reverse and its only
	// remaining copy is the stash entry.
	var kept, dropped []string
	for _, p := range paths {
		// Read the stashed side before touching the index: `git reset` drops
		// the conflict stages, and with them the only copy of it in this repo
		// outside the stash.
		local, showErr := gitShow(repoPath, ":3:"+p)
		if showErr != nil {
			// No stage 3 at all. A delete/modify conflict is the realistic
			// shape: the stash deleted the file, so there is no stashed content
			// to put back and `checkout HEAD` is the only way to leave the repo
			// committable. That DISCARDS the local change, which the message
			// below has to say out loud — the stash git left behind is then the
			// only copy of it.
			if cErr := run(repoPath, "git", "checkout", "HEAD", "--", p); cErr != nil {
				return fmt.Errorf("autostash: reapplying stashed changes to %s conflicted and could not be undone: %w", p, cErr)
			}
			dropped = append(dropped, p)
			continue
		}
		// `reset -- <path>` rewrites the index entry from HEAD, which is what
		// clears the unmerged stages; it leaves the worktree alone, so the
		// conflict markers are overwritten separately.
		if rErr := run(repoPath, "git", "reset", "-q", "--", p); rErr != nil {
			return fmt.Errorf("autostash: reapplying stashed changes to %s conflicted and could not be undone: %w", p, rErr)
		}
		if wErr := os.WriteFile(filepath.Join(repoPath, p), local, 0o644); wErr != nil {
			return fmt.Errorf("autostash: restoring your version of %s failed: %w", p, wErr)
		}
		kept = append(kept, p)
	}
	var parts []string
	if len(kept) > 0 {
		parts = append(parts, fmt.Sprintf("kept your uncommitted %s; the incoming version is in HEAD (git checkout HEAD -- %s to take it)",
			strings.Join(kept, ", "), kept[0]))
	}
	if len(dropped) > 0 {
		parts = append(parts, fmt.Sprintf("could NOT keep your local change to %s; the file is back at the incoming version",
			strings.Join(dropped, ", ")))
	}
	// The stash pointer goes on every path: it is the pre-rebase copy of
	// everything above, and for a dropped path it is the only one left.
	parts = append(parts, "your pre-rebase copy is in the autostash (git stash list, git stash show -p stash@{0})")
	return fmt.Errorf("%w: %s", ErrAutostashConflict, strings.Join(parts, "; "))
}
