package telegram

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/maksmas/monolog/internal/git"
	"github.com/maksmas/monolog/internal/store"
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
// On error from either git.AutoCommit or syncFunc the readOnly flag is
// set so subsequent write requests reject early until the next clean
// background pull clears it. AutoCommit failures often indicate a stuck
// rebase from a prior conflicted pull (the index lock is held, or
// commit refuses because of unmerged paths); the persistent banner
// matches what the user sees on syncFunc failures and prevents a flurry
// of confusing per-write toasts. The wrapper returns the original error
// so the caller can decide what to surface to Telegram (typically the
// canned readOnlyMessage).
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
		h.readOnly.Store(true)
		return fmt.Errorf("auto-commit: %w", err)
	}
	if _, err := syncFunc(h.repoPath); err != nil {
		h.readOnly.Store(true)
		return fmt.Errorf("git sync: %w", err)
	}
	h.readOnly.Store(false)
	return nil
}

// pullRecoveryFuncs are package-level seams for the conflict-recovery
// path in pullOnce. They mirror the same pattern as pullFunc/syncFunc:
// production calls into the git package; tests swap them via withRecovery
// in sync_test.go. We keep the seams narrow so tests can drive
// arbitrary failure modes without standing up a real conflicted repo.
var (
	isRebasingFunc      = git.IsRebasing
	resolveConflictsFn  = git.ResolveConflicts
	rebaseContinueFunc  = git.RebaseContinue
	rebaseAbortFunc     = git.RebaseAbort
)

// pullOnce is the helper the pull-ticker goroutine in Serve invokes on
// each tick. It runs git.PullRebase via the injectable seam and, when
// PullRebase reports an error, performs the same conflict-recovery
// dance that git.Sync uses for the write path:
//
//  1. Check IsRebasing; if false, the error is a real failure (network,
//     auth, etc.) — return it without touching readOnly so the next
//     tick can retry.
//  2. If we ARE mid-rebase, call ResolveConflicts. For task-file
//     conflicts the UpdatedAt-ordering rule picks a deterministic
//     winner; for any other unmerged path (theme, config) it returns
//     an error.
//  3. On ResolveConflicts failure, RebaseAbort to leave the working
//     tree clean and set readOnly so writes are blocked with the
//     conflict banner. Operator must SSH in to recover.
//  4. On RebaseContinue failure, same recovery: abort + readOnly.
//
// Without this recovery, a background pull-ticker conflict would leave
// the repo stuck mid-rebase: every subsequent AutoCommit/Sync would
// fail, but readOnly would never trip (only syncFunc failures set it
// historically). The bot would silently break until manual recovery.
//
// On a clean pull (no conflict) we clear readOnly so writes can resume.
//
// Mutex: pullOnce acquires h.mu so the pull subprocess does not race
// concurrent write handlers (which run their own git add/commit/push
// chain). Git serializes on .git/index.lock at the OS level — two
// processes hitting the index simultaneously fail with "Unable to
// create '.git/index.lock'". Holding h.mu around the pull also blocks
// browse readers for the duration of the rebase, preventing them from
// observing intermediate fast-rename states on partially-replayed
// commits.
func (h *Handler) pullOnce() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Stamp the shared freshness clock on the way out — whether the pull
	// succeeded, failed, or recovered from a conflict. lastPull is a
	// rate-limit for pullIfStale, not a success ledger: an errored pull
	// still consumed a network round-trip, and re-paying that timeout on
	// the very next command would make an offline bot feel hung. The
	// ticker retries on its own schedule either way.
	defer func() { h.recordPull(h.now()) }()
	if err := pullFunc(h.repoPath); err != nil {
		rebasing, rbErr := isRebasingFunc(h.repoPath)
		if rbErr != nil || !rebasing {
			return err
		}
		// Mid-rebase from a conflicted pull. Mirror git.Sync's recovery
		// flow: resolve task-file conflicts via UpdatedAt ordering,
		// then continue. Any failure leaves the repo blocked.
		if _, resErr := resolveConflictsFn(h.repoPath); resErr != nil {
			_ = rebaseAbortFunc(h.repoPath)
			h.readOnly.Store(true)
			return fmt.Errorf("pull conflict recovery: %w", resErr)
		}
		if contErr := rebaseContinueFunc(h.repoPath); contErr != nil {
			_ = rebaseAbortFunc(h.repoPath)
			h.readOnly.Store(true)
			return fmt.Errorf("pull rebase continue: %w", contErr)
		}
	}
	h.readOnly.Store(false)
	return nil
}

// commandPullMaxAge is how stale the local clone is allowed to be when the
// bot serves a command that reads or acts on EXISTING task state. Past this
// age the command pulls first; inside it the command serves what is already
// on disk.
//
// The background ticker alone leaves up to cfg.PullInterval (default 30s) of
// staleness, so a /today issued seconds after a laptop-side `monolog add`
// could miss the task entirely. Pulling unconditionally on every update would
// instead put a network fetch behind every button tap. A 5s window resolves
// both: a deliberate command (typed, then read) is always fresh, while a burst
// — tapping ✅ on three tasks in a row — costs one fetch rather than three.
const commandPullMaxAge = 5 * time.Second

// recordPull stamps the shared freshness clock. EVERY completed pull calls it
// — the ticker's (via pullOnce), the on-demand one (also via pullOnce), and
// Serve's startup pull, which bypasses pullOnce and therefore stamps the clock
// itself. Skipping any of them would let the ticker and the on-demand gate
// double-fetch the same commits.
func (h *Handler) recordPull(t time.Time) { h.lastPull.Store(t.UnixNano()) }

// lastPullAt reports the time of the last completed pull attempt, or the zero
// time when nothing has pulled yet (which pullIfStale treats as "stale").
func (h *Handler) lastPullAt() time.Time {
	ns := h.lastPull.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// pullIfStale runs pullOnce when the shared clock says the local clone may
// have fallen behind the remote, and does nothing (no network, no lock) when
// it is still fresh.
//
// Mutex: pullOnce acquires h.mu itself and sync.Mutex is NOT reentrant, so
// pullIfStale must only ever be called from a point that does not already
// hold h.mu — see pullBeforeCommand.
func (h *Handler) pullIfStale(now time.Time) error {
	if last := h.lastPullAt(); !last.IsZero() && now.Sub(last) <= commandPullMaxAge {
		return nil
	}
	return h.pullOnce()
}

// pullBeforeCommand is the handler-side entry point for the freshness gate.
// It is deliberately error-free at the call site: a failed pull is NON-FATAL,
// logged to the Serve writer and followed by serving whatever is on disk. A
// command must never fail because the network blipped — the readOnly banner
// already communicates a pending conflict, and the ticker retries.
//
// Call it from OUTSIDE h.mu and BEFORE the reply is built. Both constraints
// are load-bearing: pullOnce takes h.mu (so calling this under the lock
// self-deadlocks), and a pull that lands after the store read is worthless.
// h.mu is never held across a bot SendMessage, so the pull always completes
// before any HTTP call.
func (h *Handler) pullBeforeCommand() {
	if err := h.pullIfStale(h.now()); err != nil {
		fmt.Fprintf(h.writer, "telegram: command pull: %v\n", err)
	}
}

// defaultLongPollTimeout is the timeout passed to bot.GetUpdates on each
// poll. Telegram supports up to 50s; 30s is a common middle-ground that
// keeps a TCP connection idle long enough to amortize the round-trip cost
// without crossing the 60s mark where some intermediate proxies start
// killing idle connections.
const defaultLongPollTimeout = 30 * time.Second

// pollErrorBackoff is the sleep applied between successive GetUpdates
// failures. We back off a small amount so a flaky network or rate-limit
// response doesn't spin the loop; the value is intentionally short so a
// transient network blip doesn't translate to a long observable outage.
const pollErrorBackoff = 2 * time.Second

// ServeOptions configures the long-running Serve loop. All required fields
// must be non-nil / non-empty — Serve validates them up front and returns
// an error rather than panicking on a misconfigured caller. The optional
// fields (Now, Writer) fall back to time.Now and io.Discard respectively
// when zero.
type ServeOptions struct {
	// RepoPath is the absolute path to the monolog git repo. Used for both
	// the background pull ticker and per-write commit/sync calls.
	RepoPath string
	// Bot is the Telegram client; tests pass a fakeBot, production passes
	// the *realBot returned by NewClient.
	Bot Bot
	// Store is the monolog task store rooted at <RepoPath>/.monolog/tasks.
	Store *store.Store
	// Cfg is the value-type Telegram config (allow-list, intervals, limits).
	Cfg TelegramConfig
	// DateFormat is the user-facing layout (e.g. "02-01-2006") passed
	// through to schedule rendering and AppendNote.
	DateFormat string
	// Now is the wall-clock anchor passed into the Handler. nil → time.Now.
	Now func() time.Time
	// Writer receives non-fatal warnings (pull failures, transient poll
	// errors). nil → io.Discard.
	Writer io.Writer
}

// Serve runs the long-polling update loop together with a background pull
// ticker. The function blocks until ctx is cancelled (typically by the
// signal handler in cmd/telegram.go) or a fatal startup error is returned.
//
// Flow:
//  1. Validate options. Missing required fields return an error before any
//     side effects.
//  2. Run one PullRebase at startup so the bot serves the latest tasks. A
//     failure here is logged via opts.Writer and ignored — the bot can
//     still serve potentially-stale data, and the next ticker tick will
//     try again.
//  3. Start the pull ticker goroutine. It invokes the injectable pullFunc
//     every Cfg.PullInterval; on success it clears the handler's readOnly
//     flag (via Handler.ClearReadOnly). Errors are logged but never abort
//     the goroutine — the ticker keeps trying until ctx ends.
//  4. Run the update loop on the calling goroutine: for each tick poll
//     bot.GetUpdates with the current offset, dispatch each update inline
//     (serialised — see note below), and advance offset. ctx cancellation
//     either via GetUpdates returning ctx.Err() or via the post-poll
//     select ends the loop.
//  5. On return, the deferred ticker.Stop() and the ticker goroutine's
//     ctx.Done() select branch ensure both pieces unwind cleanly.
//
// Concurrency choice: updates are dispatched inline rather than in a per-
// update goroutine. The mutex inside Handler would serialise the write
// paths anyway; spawning goroutines just for the dispatch would add
// shutdown complexity (wait-group, drain order) without any throughput
// gain — a single phone user generates updates well below the rate the
// store can absorb.
func Serve(ctx context.Context, opts ServeOptions) error {
	if err := validateServeOptions(opts); err != nil {
		return err
	}
	writer := opts.Writer
	if writer == nil {
		writer = io.Discard
	}

	handler := NewHandler(opts.Bot, opts.Store, opts.RepoPath, opts.Cfg, opts.DateFormat, opts.Now)
	handler.SetWriter(writer)

	// Best-effort startup pull. Failure here is informational — the bot can
	// still serve whatever's on disk, and the ticker will retry. We
	// deliberately call pullFunc directly (not handler.pullOnce) because
	// the latter would clear readOnly, but at startup the flag is already
	// false and pullOnce just adds an extra store update for no benefit.
	if err := pullFunc(opts.RepoPath); err != nil {
		fmt.Fprintf(writer, "telegram: startup pull: %v\n", err)
	}
	// Stamp the shared freshness clock. Because this path bypasses
	// pullOnce it has to do so explicitly — otherwise the very first
	// command after startup would immediately re-fetch what we just
	// pulled.
	handler.recordPull(handler.now())

	// Spawn the pull ticker. It uses a sub-context derived from ctx so a
	// ctx cancellation cleanly stops the ticker goroutine alongside the
	// main loop. We use a separate done channel to wait for the ticker to
	// exit before Serve returns; otherwise a fast shutdown could race the
	// goroutine and leave it spinning briefly on a stopped Stop()'d
	// ticker.
	tickerDone := make(chan struct{})
	go runPullTicker(ctx, handler, opts.Cfg.PullInterval, writer, tickerDone)
	defer func() {
		<-tickerDone
	}()

	return runUpdateLoop(ctx, opts.Bot, handler, writer)
}

// validateServeOptions checks the required fields up front. Missing fields
// fail loudly with a descriptive message — the caller (cmd/telegram.go)
// surfaces the error verbatim, so the user sees what they need to fix.
func validateServeOptions(opts ServeOptions) error {
	if opts.Bot == nil {
		return fmt.Errorf("telegram: nil bot")
	}
	if opts.Store == nil {
		return fmt.Errorf("telegram: nil store")
	}
	if opts.RepoPath == "" {
		return fmt.Errorf("telegram: empty repo path")
	}
	if opts.Cfg.PullInterval <= 0 {
		return fmt.Errorf("telegram: non-positive pull interval %v", opts.Cfg.PullInterval)
	}
	return nil
}

// runPullTicker is the background goroutine that periodically runs
// PullRebase. On success it clears the handler's readOnly flag (so a
// previously-broken write recovers automatically once the remote heals).
// On error it logs to writer but never exits — the goroutine only stops
// when ctx is cancelled.
//
// The ticker is RETAINED alongside the per-command pullIfStale gate rather
// than replaced by it: it is the only mechanism that heals readOnly and
// retries pullOnce's stuck-rebase recovery while the bot sits idle. Without
// it, a readOnly bot that only ever receives writes would reject them forever,
// since the write paths bail on the flag before they reach a pull. Its
// pullOnce call stamps the same lastPull clock pullIfStale reads, so a tick
// and a command arriving back to back cost one fetch, not two. interval is
// cfg.PullInterval with its original meaning — nothing here changed.
//
// The function closes done when it exits so Serve can wait for shutdown.
func runPullTicker(ctx context.Context, h *Handler, interval time.Duration, writer io.Writer, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.pullOnce(); err != nil {
				fmt.Fprintf(writer, "telegram: pull: %v\n", err)
			}
		}
	}
}

// runUpdateLoop is the body of Serve's main goroutine. It owns the
// Telegram long-poll offset and feeds each update to the handler.
//
// Error handling:
//   - ctx-cancellation errors from GetUpdates (or any subsequent ctx.Err)
//     return nil so Serve's caller sees a clean shutdown.
//   - Other GetUpdates errors are logged and the loop sleeps for
//     pollErrorBackoff (ctx-aware) before retrying. This prevents a hot
//     loop on, e.g., a sustained 401 from a revoked token while still
//     allowing the loop to recover from transient network blips.
//   - Per-update Handle errors are logged but never abort the loop — one
//     bad message must not take the bot offline.
func runUpdateLoop(ctx context.Context, bot Bot, h *Handler, writer io.Writer) error {
	var offset int64
	for {
		if ctx.Err() != nil {
			return nil
		}
		updates, err := bot.GetUpdates(ctx, offset, defaultLongPollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(writer, "telegram: get updates: %v\n", err)
			if !ctxSleep(ctx, pollErrorBackoff) {
				return nil
			}
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if handleErr := h.Handle(ctx, u); handleErr != nil {
				fmt.Fprintf(writer, "telegram: handle update %d: %v\n", u.UpdateID, handleErr)
			}
		}
	}
}

// ctxSleep blocks for d or until ctx is cancelled. Returns true on a
// completed sleep, false on cancellation — letting the caller short-
// circuit cleanly without needing to re-check ctx.Err() afterwards.
func ctxSleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
