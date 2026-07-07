package telegram

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maksmas/monolog/internal/git"
	"github.com/maksmas/monolog/internal/model"
)

// writeTaskFile writes a task JSON directly under <repoPath>/.monolog/tasks
// bypassing store.Update. Used by concurrency tests to simulate a
// pull-rebase landing a foreign edit without driving real git operations.
// The directory layout mirrors store.taskPath verbatim.
func writeTaskFile(t *testing.T, repoPath string, task model.Task) {
	t.Helper()
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data = append(data, '\n')
	path := filepath.Join(repoPath, ".monolog", "tasks", task.ID+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestPullOnceSerializesAgainstWriteHandler verifies that the pull-ticker's
// pullOnce holds h.mu for the duration of its git.PullRebase call. A
// goroutine holding the lock should block pullOnce until released — which
// is the property that prevents concurrent git subprocesses from racing on
// .git/index.lock.
func TestPullOnceSerializesAgainstWriteHandler(t *testing.T) {
	h, _, _, _ := newTestHandler(t, []int64{100})

	pullStarted := make(chan struct{})
	pullDone := make(chan struct{})
	withPullFunc(t, func(string) error {
		close(pullStarted)
		return nil
	})

	// Acquire the lock from a worker so pullOnce must wait. We hold the
	// lock long enough to make the contention observable; 100ms is a
	// gentle margin even on slow CI.
	lockHeld := make(chan struct{})
	lockReleased := make(chan struct{})
	go func() {
		h.mu.Lock()
		close(lockHeld)
		time.Sleep(100 * time.Millisecond)
		h.mu.Unlock()
		close(lockReleased)
	}()
	<-lockHeld

	go func() {
		if err := h.pullOnce(); err != nil {
			t.Errorf("pullOnce: %v", err)
		}
		close(pullDone)
	}()

	// pullOnce must NOT have entered pullFunc before the lock-holder
	// releases. Wait briefly to confirm the pull is blocked behind mu.
	select {
	case <-pullStarted:
		t.Fatal("pullOnce ran pullFunc while another goroutine held h.mu — mutex is not enforced")
	case <-time.After(50 * time.Millisecond):
		// Expected — the pull is blocked behind the mutex.
	}

	<-lockReleased

	// After the lock release, the pull should proceed.
	select {
	case <-pullStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pullOnce never ran pullFunc after the lock was released")
	}
	select {
	case <-pullDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pullOnce never returned")
	}
}

// TestHandleCallbackDoneRereadsTaskInsideLock verifies that the Done
// callback re-reads the task under the lock so a concurrent file rewrite
// (e.g. a pull-ticker landing a laptop edit) is observed instead of
// clobbered. We simulate the race by feeding handleCallbackDone the stale
// pre-Resolve value while the on-disk file holds the newer "laptop edit"
// state — the Done flow must pick up the new title because of the re-read.
func TestHandleCallbackDoneRereadsTaskInsideLock(t *testing.T) {
	withPullFunc(t, func(string) error { return nil })
	withSyncFunc(t, noopSync)

	h, bot, s, repoPath := newTestHandler(t, []int64{100})

	original := model.Task{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title:     "original title",
		Source:    "telegram",
		Status:    "open",
		Position:  1000,
		Schedule:  handlerTestNow.Format("2006-01-02"),
		CreatedAt: handlerTestNow.Format(time.RFC3339),
		UpdatedAt: handlerTestNow.Format(time.RFC3339),
		Tags:      []string{"work"},
	}
	if err := s.Create(original); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if err := git.AutoCommit(repoPath, "add: original title", taskRelPath(original.ID)); err != nil {
		t.Fatalf("seed AutoCommit: %v", err)
	}

	// Simulate the "stale in-memory" view that handleCallback would have
	// passed in: take a copy of original, then rewrite the file on disk
	// to mimic a pull-ticker landing a laptop edit (new title + new tag).
	stale := original
	updated := original
	updated.Title = "laptop edit title"
	updated.Tags = []string{"work", "important"}
	updated.UpdatedAt = handlerTestNow.Add(time.Hour).Format(time.RFC3339)
	writeTaskFile(t, repoPath, updated)

	cq := &CallbackQuery{ID: "cb-done", UserID: 100, ChatID: 5, MessageID: 99, Data: "done:" + original.ID}
	if err := h.handleCallbackDone(context.Background(), cq, stale); err != nil {
		t.Fatalf("handleCallbackDone: %v", err)
	}

	got, err := s.Get(original.ID)
	if err != nil {
		t.Fatalf("post-Get: %v", err)
	}
	if got.Status != "done" {
		t.Fatalf("expected status done, got %q", got.Status)
	}
	if got.Title != "laptop edit title" {
		t.Fatalf("re-read did not pick up laptop title; got %q want %q", got.Title, "laptop edit title")
	}
	// The new "important" tag must survive — the bot would have wiped it
	// if it wrote back the stale value.
	if !containsAll(got.Tags, []string{"work", "important"}) {
		t.Fatalf("re-read did not preserve laptop-side tags; got %v", got.Tags)
	}
	if len(bot.answers) == 0 {
		t.Fatalf("expected at least one AnswerCallback")
	}
}

// TestHandleCallbackDoneSkipsWhenAlreadyDoneAfterReread covers the case
// where a concurrent CLI/TUI marked the task done between the bot's
// Resolve and the bot's lock acquisition — the re-read must catch this
// and the bot must not double-complete.
func TestHandleCallbackDoneSkipsWhenAlreadyDoneAfterReread(t *testing.T) {
	withPullFunc(t, func(string) error { return nil })
	withSyncFunc(t, noopSync)

	h, bot, s, repoPath := newTestHandler(t, []int64{100})

	original := model.Task{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title:     "race me",
		Source:    "telegram",
		Status:    "open",
		Position:  1000,
		Schedule:  handlerTestNow.Format("2006-01-02"),
		CreatedAt: handlerTestNow.Format(time.RFC3339),
		UpdatedAt: handlerTestNow.Format(time.RFC3339),
	}
	if err := s.Create(original); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if err := git.AutoCommit(repoPath, "add: race me", taskRelPath(original.ID)); err != nil {
		t.Fatalf("seed AutoCommit: %v", err)
	}

	stale := original
	// A concurrent path lands a done before the bot grabs the lock.
	done := original
	done.Status = "done"
	done.CompletedAt = handlerTestNow.Format(time.RFC3339)
	writeTaskFile(t, repoPath, done)

	cq := &CallbackQuery{ID: "cb-done", UserID: 100, ChatID: 5, MessageID: 99, Data: "done:" + original.ID}
	if err := h.handleCallbackDone(context.Background(), cq, stale); err != nil {
		t.Fatalf("handleCallbackDone: %v", err)
	}

	// The user-visible signal is the toast — "already done" — and no
	// edit to the message.
	if len(bot.answers) == 0 {
		t.Fatalf("expected at least one AnswerCallback")
	}
	last := bot.answers[len(bot.answers)-1]
	if last.Toast != "already done" {
		t.Fatalf("expected 'already done' toast, got %q", last.Toast)
	}
	if len(bot.edits) != 0 {
		t.Fatalf("expected no message edit on stale done, got %d edits", len(bot.edits))
	}
}

// TestHandleCallbackActiveRereadsTaskInsideLock mirrors the Done test for
// the Active toggle path — the lost-update fix must apply uniformly.
func TestHandleCallbackActiveRereadsTaskInsideLock(t *testing.T) {
	withPullFunc(t, func(string) error { return nil })
	withSyncFunc(t, noopSync)

	h, _, s, repoPath := newTestHandler(t, []int64{100})

	original := model.Task{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title:     "original title",
		Source:    "telegram",
		Status:    "open",
		Position:  1000,
		Schedule:  handlerTestNow.Format("2006-01-02"),
		CreatedAt: handlerTestNow.Format(time.RFC3339),
		UpdatedAt: handlerTestNow.Format(time.RFC3339),
	}
	if err := s.Create(original); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if err := git.AutoCommit(repoPath, "add: original title", taskRelPath(original.ID)); err != nil {
		t.Fatalf("seed AutoCommit: %v", err)
	}

	stale := original
	// Simulate a laptop edit landing between Resolve and the Active toggle.
	updated := original
	updated.Title = "laptop renamed it"
	updated.Tags = []string{"keep-me"}
	writeTaskFile(t, repoPath, updated)

	cq := &CallbackQuery{ID: "cb-active", UserID: 100, ChatID: 5, MessageID: 99, Data: "active:" + original.ID}
	if err := h.handleCallbackActive(context.Background(), cq, stale); err != nil {
		t.Fatalf("handleCallbackActive: %v", err)
	}

	got, err := s.Get(original.ID)
	if err != nil {
		t.Fatalf("post-Get: %v", err)
	}
	if got.Title != "laptop renamed it" {
		t.Fatalf("title overwritten; got %q want %q", got.Title, "laptop renamed it")
	}
	// keep-me tag from the laptop edit must survive; the active toggle
	// should have added "active" alongside it.
	if !containsAll(got.Tags, []string{"keep-me", model.ActiveTag}) {
		t.Fatalf("expected both keep-me and active tags, got %v", got.Tags)
	}
}

// TestServePullTickerAndBrowseConcurrent exercises the lock from end-to-
// end: it spins the pull-ticker against a stream of /all browse requests.
// Run with `-race` to catch any unsynchronized access. The substantive
// assertion is "no panic, no race detector fail" — the updates are valid
// and the store is empty, so successful completion within the deadline is
// sufficient. Without the listLocked fix, a concurrent List + file rewrite
// would trip the race detector and produce sporadic "list failed" replies.
func TestServePullTickerAndBrowseConcurrent(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)

	var pulls int32
	withPullFunc(t, func(string) error {
		atomic.AddInt32(&pulls, 1)
		// Simulate a tiny amount of work so the ticker overlaps with the
		// browse path's listLocked call.
		time.Sleep(time.Millisecond)
		return nil
	})
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   2 * time.Millisecond,
		BrowseLimit:    20,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
		})
	}()

	// Pump browse requests through the long-poll channel for a short
	// window so the ticker and the readers overlap.
	stop := time.After(120 * time.Millisecond)
	var updateID int64 = 1
loop:
	for {
		select {
		case <-stop:
			break loop
		default:
			bot.queue([]Update{{
				UpdateID: updateID,
				Message:  &Message{ChatID: 5, UserID: 100, Text: "/all"},
			}})
			updateID++
		}
	}

	cancel()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s")
	}

	// Sanity: the ticker did fire at least once during the test window.
	if atomic.LoadInt32(&pulls) < 1 {
		t.Fatalf("expected ≥1 pull during concurrent run, got %d", atomic.LoadInt32(&pulls))
	}
}

// TestHandleCaptureDoesNotHoldLockAcrossBotSend verifies the iter-3 fix:
// a slow Telegram SendMessage in handleCapture must NOT block another
// goroutine that needs h.mu (e.g. the pull-ticker). Without the fix the
// write handlers held h.mu across the entire bot HTTP round-trip; a slow
// Telegram call would starve the pull-ticker for the full duration.
//
// The test fires handleCapture with a fakeBot whose SendMessage sleeps,
// then concurrently attempts to acquire h.mu directly. The acquisition
// must complete well before SendMessage returns. With the bug, the lock
// is held until SendMessage finishes and the acquisition observably
// waits the full delay; with the fix the acquisition succeeds almost
// immediately after the in-lock work completes.
func TestHandleCaptureDoesNotHoldLockAcrossBotSend(t *testing.T) {
	withPullFunc(t, func(string) error { return nil })
	withSyncFunc(t, noopSync)

	h, bot, _, _ := newTestHandler(t, []int64{100})
	bot.sendDelay = 500 * time.Millisecond

	captureDone := make(chan error, 1)
	go func() {
		captureDone <- h.handleCapture(context.Background(), &Message{
			ChatID: 5, UserID: 100, Text: "slow capture",
		})
	}()

	// Give handleCapture a moment to enter its critical section and
	// progress to the post-lock SendMessage. 50ms is generous; the
	// in-lock work is a no-op store.Create + no-op AutoCommit + no-op
	// sync — sub-millisecond on every machine. We then try to acquire
	// h.mu; it MUST be available because the bot call is now happening
	// outside the lock.
	time.Sleep(50 * time.Millisecond)

	lockAcquired := make(chan struct{})
	go func() {
		h.mu.Lock()
		close(lockAcquired)
		h.mu.Unlock()
	}()

	select {
	case <-lockAcquired:
		// Good — the lock was acquirable while SendMessage was still
		// in flight, meaning the handler released h.mu before issuing
		// the bot HTTP call.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("h.mu was not acquirable while SendMessage was in flight — handler is holding the lock across bot HTTP I/O")
	}

	// Let the capture finish so the test cleans up cleanly.
	select {
	case err := <-captureDone:
		if err != nil {
			t.Fatalf("handleCapture: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handleCapture never returned")
	}
}

// TestPullTickerNotBlockedBySlowBotSend exercises the end-to-end
// starvation scenario via Serve: a slow Telegram SendMessage from a
// capture must not delay the pull-ticker's next tick. We run a Serve
// loop with a tight PullInterval, fire a single capture with a long
// sendDelay, and assert that the ticker fires multiple times during the
// delay window. Without the fix the ticker would stall behind h.mu for
// the entire delay; with the fix it runs unaffected.
func TestPullTickerNotBlockedBySlowBotSend(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)

	var pulls int32
	pullStarted := make(chan struct{})
	var startOnce sync.Once
	withPullFunc(t, func(string) error {
		startOnce.Do(func() { close(pullStarted) })
		atomic.AddInt32(&pulls, 1)
		return nil
	})
	withSyncFunc(t, noopSync)

	bot := newBlockingBot()
	bot.sendDelay = 400 * time.Millisecond
	cfg := TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   10 * time.Millisecond,
		BrowseLimit:    20,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, ServeOptions{
			RepoPath:   repoPath,
			Bot:        bot,
			Store:      s,
			Cfg:        cfg,
			DateFormat: "02-01-2006",
			Now:        func() time.Time { return handlerTestNow },
		})
	}()

	// Wait for the startup pull to complete so we have a clean baseline.
	select {
	case <-pullStarted:
	case <-time.After(time.Second):
		t.Fatal("startup pull never ran")
	}

	// Queue one capture; its SendMessage will sleep 400ms with sendDelay.
	bot.queue([]Update{{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "slow capture"},
	}})

	// Record the pull count just BEFORE the capture starts its slow
	// SendMessage, then again 300ms later (well within the sendDelay
	// window). With a 10ms PullInterval we expect multiple ticks even
	// after subtracting test scheduling jitter — pick a conservative
	// floor of 3 extra pulls. Without the fix only ~0–1 pulls would
	// land in this window.
	baseline := atomic.LoadInt32(&pulls)
	time.Sleep(300 * time.Millisecond)
	after := atomic.LoadInt32(&pulls)

	if got := after - baseline; got < 3 {
		t.Fatalf("pull-ticker starved during slow SendMessage: only %d ticks in 300ms with 10ms interval (baseline=%d after=%d)", got, baseline, after)
	}

	cancel()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s")
	}
}
