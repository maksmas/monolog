package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maksmas/monolog/internal/git"
)

// errTestPullDisabled is what the package-wide default pullFunc returns
// while the test binary runs. See TestMain.
var errTestPullDisabled = errors.New("pull disabled in tests")

// TestMain defaults the pullFunc seam to a failing stub so no test ever
// shells out to a real `git pull`. This became load-bearing once
// pullBeforeCommand started running ahead of every state-dependent command:
// without it, every browse / callback / note-reply test in the package would
// spawn a git subprocess against a remote-less fixture repo.
//
// The stub returns an ERROR rather than nil on purpose. A remote-less fixture
// is exactly what a real `git pull --rebase` fails on, so the error is honest
// — and, more importantly, pullOnce clears readOnly on a *successful* pull.
// A silently-succeeding default would heal the flag underneath the tests that
// call SetReadOnly(true) and then assert on read-only behavior. Tests that
// want a pull to happen install their own stub via withPullFunc.
func TestMain(m *testing.M) {
	pullFunc = func(string) error { return errTestPullDisabled }
	os.Exit(m.Run())
}

// mutableClock is a race-safe settable clock for the freshness-gate tests.
// The gate compares wall-clock instants, so driving it needs a `now` the test
// can move forward by hand rather than the fixed handlerTestNow value.
type mutableClock struct {
	mu sync.Mutex
	t  time.Time
}

func newMutableClock(start time.Time) *mutableClock { return &mutableClock{t: start} }

func (c *mutableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// countingPull returns a succeeding pullFunc stub that increments a counter,
// plus a snapshot accessor. Used by every gate test to assert on the exact
// number of fetches — the whole point of the gate is that the count is 1, not 3.
func countingPull() (func(string) error, func() int) {
	var n int32
	fn := func(string) error {
		atomic.AddInt32(&n, 1)
		return nil
	}
	return fn, func() int { return int(atomic.LoadInt32(&n)) }
}

// withPullFunc swaps the package-level pullFunc seam for the duration of a
// test. Cleanup restores whatever was installed before — which for most tests
// is TestMain's failing stub, not the production git.PullRebase. Mirrors the
// pattern used elsewhere in this repo for the `emailAuthorize` swappable seam.
func withPullFunc(t *testing.T, fn func(string) error) {
	t.Helper()
	prev := pullFunc
	pullFunc = fn
	t.Cleanup(func() { pullFunc = prev })
}

// withSyncFunc swaps the package-level syncFunc seam for the duration of a
// test. syncFunc is called by commitAndSync after the local AutoCommit; the
// no-op default returned here lets the capture path land its commit without
// the test having to provision a remote.
func withSyncFunc(t *testing.T, fn func(string) (git.SyncResult, error)) {
	t.Helper()
	prev := syncFunc
	syncFunc = fn
	t.Cleanup(func() { syncFunc = prev })
}

// withPullRecovery swaps the four recovery seams pullOnce uses on a
// conflicted PullRebase. Each fn is optional — nil means "use the
// production value" — so individual tests can drive just the path they
// care about (e.g. "ResolveConflicts succeeds, RebaseContinue fails").
func withPullRecovery(t *testing.T,
	isRebasing func(string) (bool, error),
	resolve func(string) (int, error),
	cont func(string) error,
	abort func(string) error,
) {
	t.Helper()
	prevIs, prevRes, prevCont, prevAbort := isRebasingFunc, resolveConflictsFn, rebaseContinueFunc, rebaseAbortFunc
	if isRebasing != nil {
		isRebasingFunc = isRebasing
	}
	if resolve != nil {
		resolveConflictsFn = resolve
	}
	if cont != nil {
		rebaseContinueFunc = cont
	}
	if abort != nil {
		rebaseAbortFunc = abort
	}
	t.Cleanup(func() {
		isRebasingFunc, resolveConflictsFn, rebaseContinueFunc, rebaseAbortFunc = prevIs, prevRes, prevCont, prevAbort
	})
}

// blockingBot is a Bot fake purpose-built for Serve tests. The default
// fakeBot returns (nil, nil) immediately on an empty update queue, which
// would cause Serve's update loop to spin tightly until ctx cancellation —
// that races shutdown timing in tests. blockingBot drains a channel for
// updates and blocks (respecting ctx) when the channel is empty, mirroring
// real Telegram long-polling semantics.
type blockingBot struct {
	updates chan []Update

	mu        sync.Mutex
	pollCount int
	sent      []sentMessage
	edits     []editedMessage
	answers   []answeredCallback
	nextMsgID int

	// pollErrCh injects a single GetUpdates error when readable; tests use
	// it to verify the backoff path. After one read the channel is left
	// alone and subsequent polls drain `updates` normally.
	pollErrCh chan error

	// sendDelay / editDelay / answerDelay sleep for the given duration
	// inside the corresponding method BEFORE recording the call. Used by
	// tests that verify the handler does not hold h.mu across bot HTTP
	// I/O — the delay simulates a slow Telegram round-trip.
	sendDelay   time.Duration
	editDelay   time.Duration
	answerDelay time.Duration
}

func newBlockingBot() *blockingBot {
	return &blockingBot{
		updates:   make(chan []Update, 16),
		pollErrCh: make(chan error, 1),
	}
}

// queue enqueues a batch of updates for the next poll to return.
func (b *blockingBot) queue(batch []Update) {
	b.updates <- batch
}

func (b *blockingBot) pollCountSnapshot() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pollCount
}

func (b *blockingBot) sentSnapshot() []sentMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]sentMessage, len(b.sent))
	copy(out, b.sent)
	return out
}

func (b *blockingBot) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	b.mu.Lock()
	b.pollCount++
	b.mu.Unlock()

	// Drain a pre-staged error before consulting the update channel — that
	// way tests can interleave one transient failure between two healthy
	// polls without racing on goroutine schedule.
	select {
	case err := <-b.pollErrCh:
		if err != nil {
			return nil, err
		}
	default:
	}

	select {
	case batch := <-b.updates:
		return batch, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *blockingBot) SendMessage(ctx context.Context, chatID int64, html string, kb InlineKeyboard) (int, error) {
	// Snapshot the delay under the lock, then sleep WITHOUT holding it
	// so concurrent calls aren't unnecessarily serialized in tests that
	// drive parallel traffic.
	b.mu.Lock()
	delay := b.sendDelay
	b.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextMsgID++
	b.sent = append(b.sent, sentMessage{ChatID: chatID, HTML: html, Keyboard: kb, MsgID: b.nextMsgID})
	return b.nextMsgID, nil
}

func (b *blockingBot) EditMessage(ctx context.Context, chatID int64, msgID int, html string, kb InlineKeyboard) error {
	b.mu.Lock()
	delay := b.editDelay
	b.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.edits = append(b.edits, editedMessage{ChatID: chatID, MsgID: msgID, HTML: html, Keyboard: kb})
	return nil
}

func (b *blockingBot) AnswerCallback(ctx context.Context, callbackID, toast string) error {
	b.mu.Lock()
	delay := b.answerDelay
	b.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.answers = append(b.answers, answeredCallback{CallbackID: callbackID, Toast: toast})
	return nil
}

var _ Bot = (*blockingBot)(nil)

// noopSync returns a clean SyncResult so commitAndSync drives the capture
// path to completion without needing a real remote. Used by every Serve
// test that exercises the write flow.
func noopSync(string) (git.SyncResult, error) { return git.SyncResult{}, nil }

func TestServeValidateOptionsRejectsMissingFields(t *testing.T) {
	t.Parallel()
	_, s := initTelegramTestRepo(t)
	bot := newBlockingBot()
	cfg := TelegramConfig{PullInterval: 30 * time.Second, BrowseLimit: 20}
	good := ServeOptions{RepoPath: "/tmp/x", Bot: bot, Store: s, Cfg: cfg}

	cases := []struct {
		name string
		mut  func(o *ServeOptions)
		want string
	}{
		{"nil bot", func(o *ServeOptions) { o.Bot = nil }, "nil bot"},
		{"nil store", func(o *ServeOptions) { o.Store = nil }, "nil store"},
		{"empty repo path", func(o *ServeOptions) { o.RepoPath = "" }, "empty repo path"},
		{"non-positive interval", func(o *ServeOptions) { o.Cfg.PullInterval = 0 }, "non-positive pull interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := good
			tc.mut(&opts)
			err := Serve(context.Background(), opts)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestServeProcessesQueuedUpdateAndShutsDownOnContextCancel(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)
	withPullFunc(t, func(string) error { return nil })
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   time.Hour, // long enough not to interfere
		BrowseLimit:    20,
	}
	bot.queue([]Update{{
		UpdateID: 7,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "hello world"},
	}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
			Writer:     &bytes.Buffer{},
		})
	}()

	// Spin briefly until the handler has emitted the summary reply, then
	// cancel ctx. A tight loop with a deadline keeps the test responsive
	// without coupling timing to a fixed sleep.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(bot.sentSnapshot()) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("handler never sent a reply within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of cancel")
	}

	sent := bot.sentSnapshot()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d: %+v", len(sent), sent)
	}
	if sent[0].ChatID != 5 || !strings.Contains(sent[0].HTML, "hello world") {
		t.Fatalf("unexpected reply: %+v", sent[0])
	}
}

func TestServeRunsStartupPullAndPeriodicPullTicker(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)

	var pulls int32
	withPullFunc(t, func(string) error {
		atomic.AddInt32(&pulls, 1)
		return nil
	})
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   25 * time.Millisecond,
		BrowseLimit:    20,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
			Writer:     &bytes.Buffer{},
		})
	}()

	// Wait until at least 3 pull calls have landed (1 startup + ≥2 ticker
	// ticks at 25ms each). A 500ms ceiling gives plenty of headroom on a
	// slow CI box while staying snappy locally.
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if atomic.LoadInt32(&pulls) >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected ≥3 pull calls within 500ms, got %d", atomic.LoadInt32(&pulls))
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of cancel")
	}
}

func TestServeStartupPullFailureIsLoggedNotFatal(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)

	var calls int32
	withPullFunc(t, func(string) error {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return errors.New("startup pull boom")
		}
		return nil
	})
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   time.Hour,
		BrowseLimit:    20,
	}

	var logBuf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
			Writer:     &logBuf,
		})
	}()

	// Give the startup pull a moment to land, then cancel and assert the
	// error was surfaced to the writer instead of returned by Serve.
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if atomic.LoadInt32(&calls) >= 1 && bot.pollCountSnapshot() >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup pull never ran or update loop never polled within 500ms (pulls=%d polls=%d)",
				atomic.LoadInt32(&calls), bot.pollCountSnapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned err on startup-pull failure (should be non-fatal): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of cancel")
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "startup pull") || !strings.Contains(logged, "startup pull boom") {
		t.Fatalf("expected startup-pull warning in writer, got %q", logged)
	}
}

func TestServePullTickerErrorIsLoggedAndLoopContinues(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)

	var calls int32
	withPullFunc(t, func(string) error {
		n := atomic.AddInt32(&calls, 1)
		// Startup pull (1) is fine; first ticker pull (2) returns an
		// error; subsequent ones (3+) succeed so we can prove the
		// ticker did not get stuck after the failure.
		if n == 2 {
			return errors.New("ticker pull boom")
		}
		return nil
	})
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   20 * time.Millisecond,
		BrowseLimit:    20,
	}

	var logBuf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
			Writer:     &logBuf,
		})
	}()

	// Wait until at least one ticker tick after the failing one (so we know
	// the loop didn't deadlock after the error).
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if atomic.LoadInt32(&calls) >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ticker did not recover after failure: calls=%d", atomic.LoadInt32(&calls))
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of cancel")
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "pull") || !strings.Contains(logged, "ticker pull boom") {
		t.Fatalf("expected pull-ticker warning in writer, got %q", logged)
	}
}

func TestServeClearsReadOnlyOnPullSuccess(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)
	withPullFunc(t, func(string) error { return nil })
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   15 * time.Millisecond,
		BrowseLimit:    20,
	}

	// We need a handler reference to flip readOnly before Serve starts the
	// ticker. Because Serve constructs its own handler internally, we
	// install a synthetic handler via a captureHandlerHook seam below…
	// but we don't have one. Instead exercise this indirectly: queue a
	// write that fails via syncFunc-injected error so commitAndSync flips
	// readOnly, then watch a tick clear it.

	// Toggle syncFunc to inject a failure on the first write, succeed on
	// subsequent writes (none expected). We also need a way to observe
	// the readOnly flag. Take advantage of the fact that the browse path
	// renders a banner when readOnly is set: queue a /today after the
	// write, and assert the banner appears immediately but is gone after
	// a tick.
	var syncCalls int32
	withSyncFunc(t, func(string) (git.SyncResult, error) {
		if atomic.AddInt32(&syncCalls, 1) == 1 {
			return git.SyncResult{}, errors.New("first sync boom")
		}
		return git.SyncResult{}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
			Writer:     &bytes.Buffer{},
		})
	}()

	// Step 1: trigger a write that fails → readOnly becomes true.
	bot.queue([]Update{{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "capture one"},
	}})
	// Wait until the first reply lands (a readOnly conflict reply).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(bot.sentSnapshot()) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first reply never arrived")
		}
		time.Sleep(5 * time.Millisecond)
	}

	first := bot.sentSnapshot()[0]
	if !strings.Contains(first.HTML, "sync conflict") {
		t.Fatalf("expected conflict reply, got %q", first.HTML)
	}

	// Step 2: wait for a pull tick to clear readOnly, then issue /today
	// and assert no banner is prepended.
	time.Sleep(60 * time.Millisecond) // ≥1 pull tick at 15ms interval

	bot.queue([]Update{{
		UpdateID: 2,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}})

	// Wait for the browse reply.
	deadline = time.Now().Add(2 * time.Second)
	for {
		sent := bot.sentSnapshot()
		if len(sent) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/today reply never arrived; sent=%+v", bot.sentSnapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of cancel")
	}

	// The very first message after the failing capture should NOT contain
	// the read-only banner — the pull-ticker cleared the flag.
	for _, sm := range bot.sentSnapshot()[1:] {
		if strings.Contains(sm.HTML, "read-only — sync conflict pending") {
			t.Fatalf("read-only banner should have cleared after pull-tick success, still present in %q", sm.HTML)
		}
	}
}

func TestServeBackoffsOnGetUpdatesErrorAndExitsOnCancel(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)
	withPullFunc(t, func(string) error { return nil })
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	bot.pollErrCh <- errors.New("flaky network")

	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   time.Hour,
		BrowseLimit:    20,
	}

	var logBuf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
			Writer:     &logBuf,
		})
	}()

	// Wait until the bot's GetUpdates has been called at least once (the
	// failing call). Then immediately cancel — the loop's ctx-aware
	// backoff sleep must exit promptly rather than wait the full 2s.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if bot.pollCountSnapshot() >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bot never polled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancelStart := time.Now()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned err: %v", err)
		}
		// The ctx-aware backoff means we must NOT block for the full
		// pollErrorBackoff (2s) before returning. Pad generously to
		// avoid flaking on slow CI.
		if elapsed := time.Since(cancelStart); elapsed > 500*time.Millisecond {
			t.Fatalf("Serve waited %v after cancel during backoff sleep — ctx not respected", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return within 3s of cancel")
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "get updates") || !strings.Contains(logged, "flaky network") {
		t.Fatalf("expected get-updates warning, got %q", logged)
	}
}

func TestServeContextCancelDuringGetUpdatesReturnsCleanly(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)
	withPullFunc(t, func(string) error { return nil })
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   time.Hour,
		BrowseLimit:    20,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
			Writer:     &bytes.Buffer{},
		})
	}()

	// Bot is parked on an empty updates channel; cancel from the test
	// body to verify the ctx-aware blocking GetUpdates returns and Serve
	// shuts down within a small window.
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if bot.pollCountSnapshot() >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bot never polled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned err: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
			t.Fatalf("Serve took %v to shut down — ctx cancel was not honored promptly", elapsed)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Serve did not return within 1s of cancel")
	}
}

func TestHandlerIsReadOnlyAndClearReadOnlyAndSetReadOnly(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestHandler(t, nil)
	if h.IsReadOnly() {
		t.Fatal("expected fresh handler to be writable")
	}
	h.SetReadOnly(true)
	if !h.IsReadOnly() {
		t.Fatal("expected SetReadOnly(true) to flip the flag")
	}
	h.ClearReadOnly()
	if h.IsReadOnly() {
		t.Fatal("expected ClearReadOnly to clear the flag")
	}
}

func TestPullOnceClearsReadOnlyOnSuccess(t *testing.T) {
	withPullFunc(t, func(string) error { return nil })
	h, _, _, _ := newTestHandler(t, nil)
	h.SetReadOnly(true)
	if err := h.pullOnce(); err != nil {
		t.Fatalf("pullOnce: %v", err)
	}
	if h.IsReadOnly() {
		t.Fatal("expected pullOnce success to clear readOnly")
	}
}

func TestPullOncePreservesReadOnlyOnFailure(t *testing.T) {
	withPullFunc(t, func(string) error { return errors.New("nope") })
	// IsRebasing returns false → no recovery attempted, just propagate
	// the error. readOnly remains as the caller set it.
	withPullRecovery(t,
		func(string) (bool, error) { return false, nil },
		nil, nil, nil,
	)
	h, _, _, _ := newTestHandler(t, nil)
	h.SetReadOnly(true)
	if err := h.pullOnce(); err == nil {
		t.Fatal("expected error")
	}
	if !h.IsReadOnly() {
		t.Fatal("readOnly should remain set when pull fails")
	}
}

func TestPullOnceRecoversFromConflict(t *testing.T) {
	// Simulate the production conflict path: PullRebase errors,
	// IsRebasing reports mid-rebase, ResolveConflicts wins,
	// RebaseContinue succeeds. pullOnce should return nil and clear
	// readOnly so writes resume.
	withPullFunc(t, func(string) error { return errors.New("rebase conflict") })
	resolveCalled, continueCalled, abortCalled := false, false, false
	withPullRecovery(t,
		func(string) (bool, error) { return true, nil },
		func(string) (int, error) { resolveCalled = true; return 1, nil },
		func(string) error { continueCalled = true; return nil },
		func(string) error { abortCalled = true; return nil },
	)
	h, _, _, _ := newTestHandler(t, nil)
	h.SetReadOnly(true)
	if err := h.pullOnce(); err != nil {
		t.Fatalf("pullOnce should succeed via recovery: %v", err)
	}
	if !resolveCalled || !continueCalled {
		t.Fatalf("expected Resolve+Continue to be called: resolve=%v continue=%v",
			resolveCalled, continueCalled)
	}
	if abortCalled {
		t.Fatal("RebaseAbort should not be called on the happy recovery path")
	}
	if h.IsReadOnly() {
		t.Fatal("successful recovery should clear readOnly")
	}
}

func TestPullOnceAbortsAndSetsReadOnlyOnResolveFailure(t *testing.T) {
	// ResolveConflicts itself errors (e.g. non-task-file conflict).
	// pullOnce must call RebaseAbort, set readOnly, and return the
	// wrapped error.
	withPullFunc(t, func(string) error { return errors.New("rebase conflict") })
	abortCalled := false
	withPullRecovery(t,
		func(string) (bool, error) { return true, nil },
		func(string) (int, error) { return 0, errors.New("non-task conflict") },
		nil, // RebaseContinue should not be called
		func(string) error { abortCalled = true; return nil },
	)
	h, _, _, _ := newTestHandler(t, nil)
	err := h.pullOnce()
	if err == nil {
		t.Fatal("expected error when Resolve fails")
	}
	if !strings.Contains(err.Error(), "non-task conflict") {
		t.Fatalf("expected resolve error wrapped, got %v", err)
	}
	if !abortCalled {
		t.Fatal("expected RebaseAbort on Resolve failure")
	}
	if !h.IsReadOnly() {
		t.Fatal("expected readOnly to be set after unrecoverable conflict")
	}
}

func TestCommitAndSyncSetsReadOnlyOnAutoCommitFailure(t *testing.T) {
	// AutoCommit failure (e.g. stuck rebase from a prior conflicted
	// pull) used to leave readOnly untouched — users saw a per-write
	// "sync conflict" toast but no persistent banner, so multiple
	// failures in a row looked like flaky behavior. Now AutoCommit
	// failure must flip readOnly so the conflict state is reflected
	// consistently with syncFunc failures.
	withSyncFunc(t, noopSync) // sync never reached in this test
	h, _, _, _ := newTestHandler(t, nil)

	// Trigger AutoCommit failure by pointing it at a nonexistent file:
	// `git add` errors when the path doesn't exist.
	err := h.commitAndSync("test message", "missing-file-that-does-not-exist.txt")
	if err == nil {
		t.Fatal("expected commitAndSync to fail on missing file")
	}
	if !strings.Contains(err.Error(), "auto-commit") {
		t.Fatalf("expected auto-commit error wrap, got %v", err)
	}
	if !h.IsReadOnly() {
		t.Fatal("expected readOnly=true after AutoCommit failure (matches syncFunc failure behavior)")
	}
}

func TestPullOnceAbortsAndSetsReadOnlyOnRebaseContinueFailure(t *testing.T) {
	withPullFunc(t, func(string) error { return errors.New("rebase conflict") })
	abortCalled := false
	withPullRecovery(t,
		func(string) (bool, error) { return true, nil },
		func(string) (int, error) { return 1, nil },
		func(string) error { return errors.New("continue boom") },
		func(string) error { abortCalled = true; return nil },
	)
	h, _, _, _ := newTestHandler(t, nil)
	err := h.pullOnce()
	if err == nil {
		t.Fatal("expected error when RebaseContinue fails")
	}
	if !abortCalled {
		t.Fatal("expected RebaseAbort on RebaseContinue failure")
	}
	if !h.IsReadOnly() {
		t.Fatal("expected readOnly to be set after rebase continue failure")
	}
}

func TestCtxSleepReturnsFalseOnCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ctxSleep(ctx, 5*time.Second) {
		t.Fatal("ctxSleep should return false on cancelled ctx")
	}
}

func TestCtxSleepReturnsTrueWhenDurationElapses(t *testing.T) {
	t.Parallel()
	if !ctxSleep(context.Background(), 5*time.Millisecond) {
		t.Fatal("ctxSleep should return true after the timer fires")
	}
}

func TestCtxSleepNonPositiveDurationReturnsImmediately(t *testing.T) {
	t.Parallel()
	if !ctxSleep(context.Background(), 0) {
		t.Fatal("zero duration on a live ctx should return true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ctxSleep(ctx, 0) {
		t.Fatal("zero duration on a cancelled ctx should return false")
	}
}

// --- Freshness gate ----------------------------------------------------
//
// The gate closes the staleness window between a laptop-side push and the bot
// serving a command. The invariants pinned here: pull when the shared clock
// says we may be behind, do NOT pull when it says we are fresh, and share that
// one clock with the retained background ticker and the startup pull so the
// two mechanisms never double-fetch.

func TestPullBeforeCommandGatesOnCommandPullMaxAge(t *testing.T) {
	fn, count := countingPull()
	withPullFunc(t, fn)
	clk := newMutableClock(handlerTestNow)
	h, _, _, _ := newTestHandlerWithClock(t, []int64{100}, clk.now)

	// Never pulled: an unstamped clock counts as stale.
	h.pullBeforeCommand()
	if got := count(); got != 1 {
		t.Fatalf("first call pulls=%d want 1", got)
	}

	// Same instant: inside the window, no network at all.
	h.pullBeforeCommand()
	if got := count(); got != 1 {
		t.Fatalf("call inside the window pulled again: pulls=%d want 1", got)
	}

	// Exactly at the boundary is still fresh — the gate is `> maxAge`.
	clk.advance(commandPullMaxAge)
	h.pullBeforeCommand()
	if got := count(); got != 1 {
		t.Fatalf("call exactly at commandPullMaxAge pulled: pulls=%d want 1", got)
	}

	// One tick past the boundary: stale again.
	clk.advance(time.Nanosecond)
	h.pullBeforeCommand()
	if got := count(); got != 2 {
		t.Fatalf("call past commandPullMaxAge pulls=%d want 2", got)
	}
}

func TestPullBeforeCommandLogsPullErrorAndStillStampsClock(t *testing.T) {
	wantErr := errors.New("remote unreachable")
	withPullFunc(t, func(string) error { return wantErr })
	h, _, _, _ := newTestHandler(t, []int64{100})
	var log bytes.Buffer
	h.SetWriter(&log)

	h.pullBeforeCommand()

	if !strings.Contains(log.String(), "remote unreachable") {
		t.Errorf("a failed pre-pull must be logged, got: %q", log.String())
	}
	// A failed attempt still stamps the clock: it consumed a round-trip, and
	// re-paying that timeout on the very next command would make an offline
	// bot feel hung. The ticker retries on its own schedule.
	if h.lastPull.Load() == 0 {
		t.Fatal("a failed pull must still stamp the shared clock")
	}
}

func TestPullTickerRefreshesSharedClockSoNextCommandSkipsItsPull(t *testing.T) {
	fn, count := countingPull()
	withPullFunc(t, fn)
	withSyncFunc(t, noopSync)

	clk := newMutableClock(handlerTestNow)
	h, bot, s, _ := newTestHandlerWithClock(t, []int64{100}, clk.now)
	seedBrowseTasks(t, s)

	// Run the retained background ticker until it has pulled at least once,
	// then shut it down so no further tick can race the assertion below.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go runPullTicker(ctx, h, time.Millisecond, io.Discard, done)
	deadline := time.Now().Add(2 * time.Second)
	for count() == 0 {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("pull ticker never ran within 2s")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	afterTicker := count()

	// The command arrives at the same instant on the shared clock. If the
	// ticker's pull had not stamped lastPull, the gate would consider the
	// clone stale and fetch again — a double-fetch on every tick+command pair.
	if err := h.Handle(context.Background(), Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := count(); got != afterTicker {
		t.Fatalf("command after a ticker pull re-fetched: pulls=%d want %d (shared clock broken)", got, afterTicker)
	}
	if len(bot.sent) == 0 {
		t.Fatal("expected the browse reply to be served from local state")
	}
}

func TestServeStartupPullStampsSharedClock(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)
	fn, count := countingPull()
	withPullFunc(t, fn)
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   time.Hour, // long enough that the ticker never fires
		BrowseLimit:    20,
	}
	bot.queue([]Update{{
		UpdateID: 3,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
			Writer:     &bytes.Buffer{},
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for len(bot.sentSnapshot()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler never replied within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of cancel")
	}

	// Startup pull only. A count of 2 would mean the startup pull bypassed
	// recordPull and the first command immediately re-fetched what Serve had
	// just pulled.
	if got := count(); got != 1 {
		t.Fatalf("pulls=%d want 1 (startup pull must stamp the shared clock)", got)
	}
}
