package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maksmas/monolog/internal/git"
	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/store"
)

// handlerTestNow is the fixed clock value the handler tests inject so the
// generated timestamps and "today" schedule resolution are deterministic
// regardless of when the test runs. We can't reuse the convert_test.go
// helper (it returns a time.Time via a function) because we need a value
// reference for .Format and the injectable now func returns a copy.
var handlerTestNow = time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)

// initTelegramTestRepo creates a fresh monolog git repo via git.Init and
// returns the repo path + an opened Store rooted at the tasks directory.
// Mirrors the email package's initSyncRepo helper.
func initTelegramTestRepo(t *testing.T) (string, *store.Store) {
	t.Helper()
	root := t.TempDir()
	repoPath := filepath.Join(root, "monolog")
	if err := git.Init(repoPath, ""); err != nil {
		t.Fatalf("git.Init: %v", err)
	}
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	s, err := store.New(tasksDir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	return repoPath, s
}

// newTestHandler wires up a Handler against a fakeBot + temp-repo store
// with the test clock and a wide-open allow-list so the access-control
// path is opt-in per test.
func newTestHandler(t *testing.T, allowed []int64) (*Handler, *fakeBot, *store.Store, string) {
	t.Helper()
	return newTestHandlerWithClock(t, allowed, func() time.Time { return handlerTestNow })
}

// newTestHandlerWithClock is newTestHandler with an injectable clock, for the
// freshness-gate tests that need to move `now` past commandPullMaxAge.
func newTestHandlerWithClock(t *testing.T, allowed []int64, now func() time.Time) (*Handler, *fakeBot, *store.Store, string) {
	t.Helper()
	repoPath, s := initTelegramTestRepo(t)
	bot := &fakeBot{}
	h := NewHandler(bot, s, repoPath, TelegramConfig{
		AllowedUserIDs: allowed,
		PullInterval:   30 * time.Second,
		BrowseLimit:    20,
	}, "02-01-2006", now)
	return h, bot, s, repoPath
}

// gitLogSubjects returns commit subjects newest→oldest. Used to assert
// that the capture path lands one commit.
func gitLogSubjects(t *testing.T, repoPath string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", repoPath, "log", "--pretty=%s")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func TestHandleCaptureCreatesTaskAndCommitsAndReplies(t *testing.T) {
	h, bot, s, repoPath := newTestHandler(t, []int64{100})

	update := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID: 1001,
			UserID: 100,
			Text:   "buy milk #shopping #urgent",
		},
	}
	if err := h.Handle(context.Background(), update); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Exactly one outbound message: the summary card.
	if len(bot.sent) != 1 {
		t.Fatalf("sent count=%d want 1; sent=%+v", len(bot.sent), bot.sent)
	}
	sent := bot.sent[0]
	if sent.ChatID != 1001 {
		t.Fatalf("ChatID=%d want 1001", sent.ChatID)
	}
	if !strings.Contains(sent.HTML, "buy milk") {
		t.Fatalf("HTML missing title; got %q", sent.HTML)
	}
	if !strings.Contains(sent.HTML, "shopping") || !strings.Contains(sent.HTML, "urgent") {
		t.Fatalf("HTML missing extracted tags; got %q", sent.HTML)
	}
	if len(sent.Keyboard) != 1 || len(sent.Keyboard[0]) != 3 {
		t.Fatalf("expected 3-button summary keyboard, got %+v", sent.Keyboard)
	}

	// Store now holds one task with the hashtags as tags and no
	// hashtags in the title.
	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("store count=%d want 1", len(tasks))
	}
	got := tasks[0]
	if got.Title != "buy milk" {
		t.Fatalf("title=%q want %q", got.Title, "buy milk")
	}
	if got.Source != "telegram" {
		t.Fatalf("Source=%q want telegram", got.Source)
	}
	if got.Status != "open" {
		t.Fatalf("Status=%q want open", got.Status)
	}
	wantSchedule := handlerTestNow.Format("2006-01-02")
	if got.Schedule != wantSchedule {
		t.Fatalf("Schedule=%q want %q", got.Schedule, wantSchedule)
	}
	if !containsAll(got.Tags, []string{"shopping", "urgent"}) {
		t.Fatalf("tags=%v want both shopping & urgent", got.Tags)
	}

	// A single commit landed for the capture (plus the init commit).
	subjects := gitLogSubjects(t, repoPath)
	if len(subjects) < 2 {
		t.Fatalf("expected ≥2 commits (init + capture), got %d: %v", len(subjects), subjects)
	}
	if subjects[0] != "add: buy milk" {
		t.Fatalf("newest commit=%q want %q", subjects[0], "add: buy milk")
	}
}

func TestHandleCaptureDropsNonAllowedUserSilently(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{42})

	update := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID: 1001,
			UserID: 999, // not in allow-list
			Text:   "should be ignored",
		},
	}
	if err := h.Handle(context.Background(), update); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 0 {
		t.Fatalf("expected silent drop, got %d sent messages", len(bot.sent))
	}
	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no task creation, got %d", len(tasks))
	}
}

func TestHandleCaptureMultilineSplitsTitleAndBody(t *testing.T) {
	h, _, s, _ := newTestHandler(t, []int64{100})

	body := "line two of the body\nline three #notATag" // hashtag in body must survive
	update := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID: 1001,
			UserID: 100,
			Text:   "the title #real\n" + body,
		},
	}
	if err := h.Handle(context.Background(), update); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("count=%d want 1", len(tasks))
	}
	got := tasks[0]
	if got.Title != "the title" {
		t.Fatalf("title=%q want %q", got.Title, "the title")
	}
	if got.Body != body {
		t.Fatalf("body=%q want %q", got.Body, body)
	}
	if !containsAll(got.Tags, []string{"real"}) {
		t.Fatalf("tags=%v should include real and only real", got.Tags)
	}
	for _, tag := range got.Tags {
		if tag == "notatag" || tag == "notATag" {
			t.Fatalf("body hashtag #notATag must not be extracted; got tags=%v", got.Tags)
		}
	}
}

func TestHandleCapturePreservesHTMLMetacharactersInStore(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})

	rawTitle := "<broken & sad>"
	update := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 1, UserID: 100, Text: rawTitle},
	}
	if err := h.Handle(context.Background(), update); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Store has raw, un-escaped title.
	tasks, _ := s.List(store.ListOptions{})
	if len(tasks) != 1 || tasks[0].Title != rawTitle {
		t.Fatalf("stored title=%q want %q", tasks[0].Title, rawTitle)
	}

	// Sent HTML has the title escaped (look for &lt; entity).
	if len(bot.sent) != 1 {
		t.Fatalf("sent count=%d want 1", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "&lt;") || !strings.Contains(bot.sent[0].HTML, "&amp;") {
		t.Fatalf("expected escaped HTML in sent message, got %q", bot.sent[0].HTML)
	}
}

func TestHandleCaptureRejectedWhenReadOnly(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	h.SetReadOnly(true)

	update := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "should not save"},
	}
	if err := h.Handle(context.Background(), update); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected one canned reply, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "sync conflict") {
		t.Fatalf("expected conflict message, got %q", bot.sent[0].HTML)
	}
	tasks, _ := s.List(store.ListOptions{})
	if len(tasks) != 0 {
		t.Fatalf("expected store untouched, got %d tasks", len(tasks))
	}
}

func TestHandleCaptureSetsReadOnlyOnSyncFailure(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})

	// Swap syncFunc for one that always errors. Use t.Cleanup to restore.
	orig := syncFunc
	syncFunc = func(repoPath string) (git.SyncResult, error) {
		return git.SyncResult{}, fmt.Errorf("simulated sync failure")
	}
	t.Cleanup(func() { syncFunc = orig })

	update := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "save me"},
	}
	if err := h.Handle(context.Background(), update); err == nil {
		t.Fatalf("expected sync error to propagate")
	}
	if !h.IsReadOnly() {
		t.Fatalf("readOnly should be set after sync failure")
	}
	// Store has the task (local commit landed) but the bot replied
	// with the conflict message rather than the summary card.
	tasks, _ := s.List(store.ListOptions{})
	if len(tasks) != 1 {
		t.Fatalf("expected local task to persist on disk, got %d", len(tasks))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected exactly one outbound message, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "sync conflict") {
		t.Fatalf("expected conflict message, got %q", bot.sent[0].HTML)
	}
}

func TestCommitAndSyncClearsReadOnlyOnSuccess(t *testing.T) {
	// commitAndSync is the helper called from inside handleCapture once
	// the readOnly guard has already passed. We exercise it directly so
	// the success path can observe the flag being cleared even when
	// previously set (the next clean operation must heal the state).
	repoPath, s := initTelegramTestRepo(t)
	h := NewHandler(&fakeBot{}, s, repoPath, TelegramConfig{
		AllowedUserIDs: []int64{100},
	}, "02-01-2006", func() time.Time { return handlerTestNow })
	h.SetReadOnly(true)

	// Create a task on disk so commitAndSync has something to commit.
	task := model.Task{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title:     "heal me",
		Source:    "telegram",
		Status:    "open",
		Position:  1000,
		Schedule:  handlerTestNow.Format("2006-01-02"),
		CreatedAt: handlerTestNow.Format(time.RFC3339),
		UpdatedAt: handlerTestNow.Format(time.RFC3339),
	}
	if err := s.Create(task); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if err := h.commitAndSync("add: heal me", taskRelPath(task.ID)); err != nil {
		t.Fatalf("commitAndSync: %v", err)
	}
	if h.IsReadOnly() {
		t.Fatalf("expected readOnly cleared after successful commitAndSync")
	}
}

func TestHandleCaptureEmptyTitleAfterStripReplies(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})

	update := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "#a #b"},
	}
	if err := h.Handle(context.Background(), update); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected one hint reply, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "empty title") {
		t.Fatalf("expected empty-title hint, got %q", bot.sent[0].HTML)
	}
	tasks, _ := s.List(store.ListOptions{})
	if len(tasks) != 0 {
		t.Fatalf("expected no task, got %d", len(tasks))
	}
}

func TestHandleCaptureAppliesAutoTagFromTitlePrefix(t *testing.T) {
	h, _, s, _ := newTestHandler(t, []int64{100})

	// Pre-create a task with the `work` tag so the auto-tag rule fires.
	pre := model.Task{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title:     "seed",
		Source:    "manual",
		Status:    "open",
		Position:  1000,
		Schedule:  handlerTestNow.Format("2006-01-02"),
		CreatedAt: handlerTestNow.Format(time.RFC3339),
		UpdatedAt: handlerTestNow.Format(time.RFC3339),
		Tags:      []string{"work"},
	}
	if err := s.Create(pre); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	update := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "work: review PR"},
	}
	if err := h.Handle(context.Background(), update); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	tasks, _ := s.List(store.ListOptions{})
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks (seed + new), got %d", len(tasks))
	}
	var fresh model.Task
	for _, t := range tasks {
		if t.Title == "work: review PR" {
			fresh = t
			break
		}
	}
	if fresh.ID == "" {
		t.Fatalf("did not find the new task by title")
	}
	if !containsAll(fresh.Tags, []string{"work"}) {
		t.Fatalf("expected auto-tag 'work' applied, got %v", fresh.Tags)
	}
}

func TestHandleIgnoresBareUpdates(t *testing.T) {
	h, bot, _, _ := newTestHandler(t, []int64{100})

	// An update with neither Message nor Callback (edited messages,
	// channel posts, etc.) is silently dropped.
	if err := h.Handle(context.Background(), Update{UpdateID: 1}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 0 || len(bot.edits) != 0 || len(bot.answers) != 0 {
		t.Fatalf("expected no outbound activity, got sent=%d edits=%d answers=%d",
			len(bot.sent), len(bot.edits), len(bot.answers))
	}
}

func TestHandleCallbackDroppedForNonAllowedUser(t *testing.T) {
	// Callbacks from users outside the allow-list are dropped without
	// sending any visible message or edit — same policy as the message
	// path. We DO answer the callback with an empty toast so Telegram
	// stops the loading spinner on the user's button (otherwise it
	// spins forever until their client times out). The empty toast is
	// indistinguishable from a "did nothing" tap, so it doesn't leak
	// the bot's existence to drive-by queries.
	h, bot, _, _ := newTestHandler(t, []int64{100})

	cbDenied := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb2", UserID: 999, ChatID: 5, MessageID: 99, Data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), cbDenied); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Exactly one (silent) answer to stop the spinner; no other outbound
	// activity.
	if len(bot.answers) != 1 || bot.answers[0].Toast != "" {
		t.Fatalf("expected one silent AnswerCallback, got %+v", bot.answers)
	}
	if len(bot.sent) != 0 || len(bot.edits) != 0 {
		t.Fatalf("expected no message/edit activity, got sent=%d edits=%d",
			len(bot.sent), len(bot.edits))
	}
}

func TestHandleReplyToMessageDoesNotCreateNewTask(t *testing.T) {
	// A reply with no resolvable task token must NOT fall through to the
	// capture path — that would create spurious tasks every time the user
	// replies to anything that doesn't begin with a ULID prefix. The reply
	// path either appends a note or surfaces a resolve error; it never
	// captures.
	h, bot, s, _ := newTestHandler(t, []int64{100})

	upd := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID:  5,
			UserID:  100,
			Text:    "this is a note",
			ReplyTo: &Message{ChatID: 5, UserID: 100, MessageID: 7, Text: "no-such-prefix original"},
		},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// One reply — the resolve error message — but zero captured tasks.
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 outbound message (resolve error), got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "could not resolve task") {
		t.Fatalf("expected resolve error reply, got %q", bot.sent[0].HTML)
	}
	tasks, _ := s.List(store.ListOptions{})
	if len(tasks) != 0 {
		t.Fatalf("expected no task created via reply path, got %d", len(tasks))
	}
}

func TestHandleSlashDropsNonAllowedUserSilently(t *testing.T) {
	// Slash commands from users outside the allow-list are dropped with
	// no outbound activity at all — same policy as plain capture. The
	// previous review caught this gap: a future routing-refactor must
	// not be allowed to surface a help message (or any other reply) to
	// unknown users.
	h, bot, _, _ := newTestHandler(t, []int64{100})

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 999, Text: "/help"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 0 || len(bot.edits) != 0 || len(bot.answers) != 0 {
		t.Fatalf("expected silent drop for non-allowed slash, got sent=%d edits=%d answers=%d",
			len(bot.sent), len(bot.edits), len(bot.answers))
	}
}

func TestHandleSlashSplitsOnNewlineToken(t *testing.T) {
	// "/today\n<args>" must route to today (not "today\n<args>" → unknown).
	// strings.IndexAny(" \t") missed \n and \r which Telegram clients
	// readily insert when the user pastes a multi-line message starting
	// with a slash command.
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedBrowseTasks(t, s)

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today\nleftover"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Two today-bucket rows; if the newline had not been treated as a
	// separator we would have gotten the unknown-command reply.
	if len(bot.sent) != 2 {
		t.Fatalf("expected 2 today rows after newline-split, got %d: %+v", len(bot.sent), bot.sent)
	}
	for _, sm := range bot.sent {
		if strings.Contains(sm.HTML, "unknown command") {
			t.Fatalf("newline-separated args misrouted to unknown: %q", sm.HTML)
		}
	}
}

func TestHandleSlashRoutesToDispatcherAndDoesNotCreateTask(t *testing.T) {
	// Slash-prefixed text must be reserved for the slash dispatcher and not
	// fall through to capture. With task 6 wired, `/today` now produces a
	// browse reply (an empty-bucket message in this empty store), but it
	// must still NOT result in a captured task.
	h, bot, s, _ := newTestHandler(t, []int64{100})

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected one browse-reply message, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "nothing") {
		t.Fatalf("expected empty-bucket reply, got %q", bot.sent[0].HTML)
	}
	tasks, _ := s.List(store.ListOptions{})
	if len(tasks) != 0 {
		t.Fatalf("expected no task created for slash command, got %d", len(tasks))
	}
}

func TestIsAllowed(t *testing.T) {
	h := &Handler{cfg: TelegramConfig{AllowedUserIDs: []int64{1, 2, 3}}}
	if !h.isAllowed(2) {
		t.Fatalf("2 should be allowed")
	}
	if h.isAllowed(99) {
		t.Fatalf("99 should not be allowed")
	}
	// Empty allow-list rejects everyone.
	h2 := &Handler{cfg: TelegramConfig{}}
	if h2.isAllowed(1) {
		t.Fatalf("empty allow-list must reject everyone")
	}
}

func TestTaskRelPath(t *testing.T) {
	got := taskRelPath("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	want := filepath.Join(".monolog", "tasks", "01ARZ3NDEKTSV4RRFFQ69G5FAV.json")
	if got != want {
		t.Fatalf("taskRelPath: got %q want %q", got, want)
	}
}

func TestNewHandlerDefaultsNowToTimeNow(t *testing.T) {
	repoPath, s := initTelegramTestRepo(t)
	h := NewHandler(&fakeBot{}, s, repoPath, TelegramConfig{}, "02-01-2006", nil)
	if h.now == nil {
		t.Fatalf("now should default to time.Now")
	}
	// Just exercise it — we don't compare values, only confirm callable.
	if h.now().IsZero() {
		t.Fatalf("default now returned zero value")
	}
}

// seedBrowseTasks populates the given store with a deterministic set of
// open tasks the browse tests share. Returns the ULIDs in insertion order
// so individual tests can reference specific tasks. The schedule values are
// expressed relative to handlerTestNow so the test runs deterministically.
//
// Layout:
//   - tk1 (work, urgent) → today
//   - tk2 (active, work) → tomorrow (falls into the "week" bucket)
//   - tk3 (active)       → today
//   - tk4                → someday
//   - tk5                → week (5 days out, also "week" bucket)
func seedBrowseTasks(t *testing.T, s *store.Store) [5]string {
	t.Helper()
	today := handlerTestNow.Format("2006-01-02")
	tomorrow := handlerTestNow.AddDate(0, 0, 1).Format("2006-01-02")
	weekISO := handlerTestNow.AddDate(0, 0, 5).Format("2006-01-02")
	somedayISO := handlerTestNow.AddDate(0, 0, 200).Format("2006-01-02")
	rfc := handlerTestNow.Format(time.RFC3339)

	tasks := []model.Task{
		{
			ID:        "01ARZ3NDEKTSV4RRFFQ69G5FA1",
			Title:     "ship login bug fix",
			Status:    "open",
			Position:  1000,
			Schedule:  today,
			CreatedAt: rfc,
			UpdatedAt: rfc,
			Tags:      []string{"work", "urgent"},
		},
		{
			ID:        "01ARZ3NDEKTSV4RRFFQ69G5FA2",
			Title:     "review PR",
			Status:    "open",
			Position:  2000,
			Schedule:  tomorrow,
			CreatedAt: rfc,
			UpdatedAt: rfc,
			Tags:      []string{"active", "work"},
		},
		{
			ID:        "01ARZ3NDEKTSV4RRFFQ69G5FA3",
			Title:     "call mom",
			Status:    "open",
			Position:  3000,
			Schedule:  today,
			CreatedAt: rfc,
			UpdatedAt: rfc,
			Tags:      []string{"active"},
		},
		{
			ID:        "01ARZ3NDEKTSV4RRFFQ69G5FA4",
			Title:     "plan vacation",
			Status:    "open",
			Position:  4000,
			Schedule:  somedayISO,
			CreatedAt: rfc,
			UpdatedAt: rfc,
		},
		{
			ID:        "01ARZ3NDEKTSV4RRFFQ69G5FA5",
			Title:     "dentist appointment",
			Status:    "open",
			Position:  5000,
			Schedule:  weekISO,
			CreatedAt: rfc,
			UpdatedAt: rfc,
		},
	}
	for _, tk := range tasks {
		if err := s.Create(tk); err != nil {
			t.Fatalf("seed Create: %v", err)
		}
	}
	return [5]string{
		"01ARZ3NDEKTSV4RRFFQ69G5FA1",
		"01ARZ3NDEKTSV4RRFFQ69G5FA2",
		"01ARZ3NDEKTSV4RRFFQ69G5FA3",
		"01ARZ3NDEKTSV4RRFFQ69G5FA4",
		"01ARZ3NDEKTSV4RRFFQ69G5FA5",
	}
}

func TestHandleSlashTodayListsOnlyTodayBucket(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	_ = seedBrowseTasks(t, s)

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Two tasks fall into the "today" bucket: ids[0] (ship login bug) and
	// ids[2] (call mom). The other three are tomorrow / week / someday.
	// We assert via titles (unique) since all seed ULIDs share the same
	// 5-char display prefix `01ARZ`.
	if len(bot.sent) != 2 {
		t.Fatalf("expected 2 task rows, got %d: %+v", len(bot.sent), bot.sent)
	}
	combined := bot.sent[0].HTML + "\n" + bot.sent[1].HTML
	if !strings.Contains(combined, "ship login bug fix") || !strings.Contains(combined, "call mom") {
		t.Fatalf("expected both today-bucket titles in output, got %q", combined)
	}
	for _, leaked := range []string{"review PR", "plan vacation", "dentist appointment"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("non-today task %q leaked into /today output: %q", leaked, combined)
		}
	}
	// Each row carries the 3-button summary keyboard.
	for i, sm := range bot.sent {
		if len(sm.Keyboard) != 1 || len(sm.Keyboard[0]) != 3 {
			t.Fatalf("sent[%d] keyboard shape=%+v want 1x3", i, sm.Keyboard)
		}
	}
}

func TestHandleSlashWeekListsWeekBucketTasks(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	_ = seedBrowseTasks(t, s)

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/week"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// `week` covers strictly-after-tomorrow through +7 days. The seed has
	// one entry 5 days out (dentist). The tomorrow entry (review PR) is the
	// Tomorrow bucket so it is NOT week.
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 task row for /week, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "dentist appointment") {
		t.Fatalf("expected dentist task in /week, got %q", bot.sent[0].HTML)
	}
}

func TestHandleSlashActiveListsOnlyActiveTagged(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	_ = seedBrowseTasks(t, s)

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/active"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Two tasks have the `active` tag: review PR and call mom.
	if len(bot.sent) != 2 {
		t.Fatalf("expected 2 task rows for /active, got %d", len(bot.sent))
	}
	combined := bot.sent[0].HTML + "\n" + bot.sent[1].HTML
	if !strings.Contains(combined, "review PR") || !strings.Contains(combined, "call mom") {
		t.Fatalf("expected both active-tagged tasks in output: %q", combined)
	}
	for _, leaked := range []string{"ship login bug fix", "plan vacation", "dentist appointment"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("non-active task %q leaked into /active: %q", leaked, combined)
		}
	}
	// The `active` tag itself must not leak into the row's `<i>` line —
	// VisibleTags filters it out.
	for _, sm := range bot.sent {
		if strings.Contains(sm.HTML, ">active<") {
			t.Fatalf("active tag should be hidden from visible tag list, got %q", sm.HTML)
		}
	}
}

func TestHandleSlashAllRespectsBrowseLimitAndAddsFooter(t *testing.T) {
	// Force a small cap so the overflow footer fires deterministically.
	repoPath, s := initTelegramTestRepo(t)
	bot := &fakeBot{}
	h := NewHandler(bot, s, repoPath, TelegramConfig{
		AllowedUserIDs: []int64{100},
		BrowseLimit:    2,
	}, "02-01-2006", func() time.Time { return handlerTestNow })
	seedBrowseTasks(t, s)

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/all"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// 2 task rows + 1 footer (5 tasks total, cap=2, overflow=3).
	if len(bot.sent) != 3 {
		t.Fatalf("expected 2 rows + 1 footer = 3 messages, got %d: %+v", len(bot.sent), bot.sent)
	}
	footer := bot.sent[2]
	if !strings.Contains(footer.HTML, "+3 more") {
		t.Fatalf("expected overflow footer with +3 more, got %q", footer.HTML)
	}
	if len(footer.Keyboard) != 0 {
		t.Fatalf("footer should have no keyboard, got %+v", footer.Keyboard)
	}
}

func TestHandleSlashAllReturnsAllWhenUnderLimit(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100}) // default BrowseLimit=20
	seedBrowseTasks(t, s)

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/all"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// 5 rows, no footer.
	if len(bot.sent) != 5 {
		t.Fatalf("expected 5 task rows, got %d", len(bot.sent))
	}
	for _, sm := range bot.sent {
		if strings.Contains(sm.HTML, "more — open laptop") {
			t.Fatalf("footer should not appear under the cap: %q", sm.HTML)
		}
	}
}

func TestHandleSlashTodayEmptyBucketReturnsNothingMessage(t *testing.T) {
	h, bot, _, _ := newTestHandler(t, []int64{100})
	// No seed — empty store.
	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected one empty-bucket message, got %d", len(bot.sent))
	}
	got := bot.sent[0].HTML
	if !strings.Contains(got, "Today") || !strings.Contains(got, "nothing") {
		t.Fatalf("expected `Today — nothing` message, got %q", got)
	}
	if len(bot.sent[0].Keyboard) != 0 {
		t.Fatalf("empty-bucket message should have no keyboard, got %+v", bot.sent[0].Keyboard)
	}
}

func TestHandleSlashReadOnlyPrependsBanner(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedBrowseTasks(t, s)
	h.SetReadOnly(true)

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Banner + 2 today-bucket rows.
	if len(bot.sent) != 3 {
		t.Fatalf("expected 1 banner + 2 rows = 3, got %d", len(bot.sent))
	}
	if bot.sent[0].HTML != readOnlyBanner {
		t.Fatalf("expected first message to be the banner, got %q", bot.sent[0].HTML)
	}
}

func TestHandleSlashReadOnlyBannerOnEmptyBucket(t *testing.T) {
	h, bot, _, _ := newTestHandler(t, []int64{100})
	h.SetReadOnly(true)

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Banner first, then empty-bucket message.
	if len(bot.sent) != 2 {
		t.Fatalf("expected banner + empty-bucket = 2 messages, got %d", len(bot.sent))
	}
	if bot.sent[0].HTML != readOnlyBanner {
		t.Fatalf("expected banner first, got %q", bot.sent[0].HTML)
	}
	if !strings.Contains(bot.sent[1].HTML, "nothing") {
		t.Fatalf("expected empty-bucket message, got %q", bot.sent[1].HTML)
	}
}

func TestHandleSlashUnknownCommandReplies(t *testing.T) {
	h, bot, _, _ := newTestHandler(t, []int64{100})

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/foo"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "unknown command") {
		t.Fatalf("expected unknown-command reply, got %q", bot.sent[0].HTML)
	}
}

func TestHandleSlashCaseInsensitiveAndIgnoresArgs(t *testing.T) {
	// `/Today extra args` should normalize to `today` and route to the
	// today browse handler.
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedBrowseTasks(t, s)

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/Today some args"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 2 {
		t.Fatalf("expected 2 today-bucket rows, got %d", len(bot.sent))
	}
}

func TestHandleSlashHelpListsCommands(t *testing.T) {
	// /help must surface the cheatsheet covering capture, browse commands,
	// inline buttons, and the reply-to-note flow. We assert on a handful
	// of stable substrings rather than the full message so cosmetic edits
	// don't make the test brittle.
	h, bot, _, _ := newTestHandler(t, []int64{100})

	upd := Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/help"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(bot.sent))
	}
	help := bot.sent[0].HTML
	for _, needle := range []string{
		"capture",
		"#hashtag",
		"tagname:",
		"/today",
		"/week",
		"/active",
		"/all",
		"Done",
		"Active",
		"Details",
		"Reply",
	} {
		if !strings.Contains(help, needle) {
			t.Fatalf("help message missing %q; got %q", needle, help)
		}
	}
	if len(bot.sent[0].Keyboard) != 0 {
		t.Fatalf("help should have no keyboard, got %+v", bot.sent[0].Keyboard)
	}
}

func TestHandleSlashStartReturnsSameAsHelp(t *testing.T) {
	// /start is an alias for /help — they MUST emit identical bodies so
	// the first interaction in a chat mirrors the on-demand cheatsheet.
	h, bot, _, _ := newTestHandler(t, []int64{100})

	for _, cmd := range []string{"/help", "/start"} {
		bot.sent = nil
		upd := Update{
			UpdateID: 1,
			Message:  &Message{ChatID: 5, UserID: 100, Text: cmd},
		}
		if err := h.Handle(context.Background(), upd); err != nil {
			t.Fatalf("Handle %s: %v", cmd, err)
		}
		if len(bot.sent) != 1 {
			t.Fatalf("%s: expected 1 reply, got %d", cmd, len(bot.sent))
		}
	}
	// /start's body must equal /help's body — re-run /help here so the
	// assertion is anchored to the helpText constant by behavior, not by
	// reading the constant in the test.
	bot.sent = nil
	if err := h.Handle(context.Background(), Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/help"},
	}); err != nil {
		t.Fatalf("Handle /help (anchor): %v", err)
	}
	helpHTML := bot.sent[0].HTML
	bot.sent = nil
	if err := h.Handle(context.Background(), Update{
		UpdateID: 2,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/start"},
	}); err != nil {
		t.Fatalf("Handle /start: %v", err)
	}
	if bot.sent[0].HTML != helpHTML {
		t.Fatalf("/start body must equal /help body\n/help:  %q\n/start: %q", helpHTML, bot.sent[0].HTML)
	}
}

// seedSingleTask is a small helper for the callback tests that need a
// specific task on disk before issuing a callback against it. The task is
// open by default; callers tweak fields (Recurrence, Tags, Status) before
// passing in.
func seedSingleTask(t *testing.T, s *store.Store, task model.Task) {
	t.Helper()
	rfc := handlerTestNow.Format(time.RFC3339)
	if task.CreatedAt == "" {
		task.CreatedAt = rfc
	}
	if task.UpdatedAt == "" {
		task.UpdatedAt = rfc
	}
	if task.Schedule == "" {
		task.Schedule = handlerTestNow.Format("2006-01-02")
	}
	if task.Status == "" {
		task.Status = "open"
	}
	if task.Position == 0 {
		task.Position = 1000
	}
	if err := s.Create(task); err != nil {
		t.Fatalf("seedSingleTask Create: %v", err)
	}
}

func TestHandleCallbackInvalidDataAnswersInvalid(t *testing.T) {
	h, bot, _, _ := newTestHandler(t, []int64{100})

	upd := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb", UserID: 100, ChatID: 5, MessageID: 9, Data: "nope"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.answers) != 1 || bot.answers[0].Toast != "invalid" {
		t.Fatalf("expected single 'invalid' answer, got %+v", bot.answers)
	}
	if len(bot.sent) != 0 || len(bot.edits) != 0 {
		t.Fatalf("expected no message activity, got sent=%d edits=%d", len(bot.sent), len(bot.edits))
	}
}

func TestHandleCallbackUnknownULIDAnswersNotFound(t *testing.T) {
	h, bot, _, _ := newTestHandler(t, []int64{100})

	upd := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb", UserID: 100, ChatID: 5, MessageID: 9, Data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.answers) != 1 || bot.answers[0].Toast != "task not found" {
		t.Fatalf("expected 'task not found' toast, got %+v", bot.answers)
	}
	if len(bot.edits) != 0 {
		t.Fatalf("expected no edits for unknown ULID, got %d", len(bot.edits))
	}
}

func TestHandleCallbackDoneCompletesNonRecurringTask(t *testing.T) {
	h, bot, s, repoPath := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "ship it",
	})

	upd := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb", UserID: 100, ChatID: 5, MessageID: 9, Data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got, err := s.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "done" {
		t.Fatalf("Status=%q want done", got.Status)
	}
	if got.CompletedAt == "" {
		t.Fatalf("CompletedAt should be set")
	}
	if len(bot.edits) != 1 {
		t.Fatalf("expected 1 edit (strike-through), got %d", len(bot.edits))
	}
	edit := bot.edits[0]
	if edit.MsgID != 9 || edit.ChatID != 5 {
		t.Fatalf("edit target wrong: %+v", edit)
	}
	if !strings.Contains(edit.HTML, "<s>") || !strings.Contains(edit.HTML, "ship it") {
		t.Fatalf("expected strike-through with title, got %q", edit.HTML)
	}
	if strings.Contains(edit.HTML, "↻ next") {
		t.Fatalf("non-recurring task should not include next-date line: %q", edit.HTML)
	}
	if len(edit.Keyboard) != 0 {
		t.Fatalf("done message should have no buttons, got %+v", edit.Keyboard)
	}
	if len(bot.answers) != 1 || bot.answers[0].Toast != "" {
		t.Fatalf("expected silent answer, got %+v", bot.answers)
	}

	// Single commit with the canonical message.
	subjects := gitLogSubjects(t, repoPath)
	if len(subjects) < 2 {
		t.Fatalf("expected >=2 commits, got %d: %v", len(subjects), subjects)
	}
	if subjects[0] != "done: ship it" {
		t.Fatalf("commit subject=%q want %q", subjects[0], "done: ship it")
	}
}

func TestHandleCallbackDoneSpawnsRecurringFollowUp(t *testing.T) {
	h, bot, s, repoPath := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:         "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title:      "water plants",
		Recurrence: "days:1",
	})

	upd := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb", UserID: 100, ChatID: 5, MessageID: 9, Data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks (done + spawn), got %d", len(tasks))
	}

	// One is the original (done), one is the spawn (open, fresh ULID).
	var orig, spawn model.Task
	for _, tk := range tasks {
		if tk.ID == "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
			orig = tk
		} else {
			spawn = tk
		}
	}
	if orig.Status != "done" {
		t.Fatalf("original Status=%q want done", orig.Status)
	}
	if spawn.Status != "open" || spawn.Recurrence != "days:1" {
		t.Fatalf("spawn unexpected: status=%q recur=%q", spawn.Status, spawn.Recurrence)
	}
	if spawn.Title != orig.Title {
		t.Fatalf("spawn title=%q want %q", spawn.Title, orig.Title)
	}

	// Edit message includes next-date line (days:1 → tomorrow).
	if len(bot.edits) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(bot.edits))
	}
	if !strings.Contains(bot.edits[0].HTML, "↻ next:") {
		t.Fatalf("expected next-date line in done message, got %q", bot.edits[0].HTML)
	}
	// Next date is handlerTestNow + 1 day, rendered in default layout
	// DD-MM-YYYY. Computing it keeps this test resilient if handlerTestNow
	// is ever bumped.
	wantNext := handlerTestNow.AddDate(0, 0, 1).Format("02-01-2006")
	if !strings.Contains(bot.edits[0].HTML, wantNext) {
		t.Fatalf("expected formatted next date %s in message, got %q", wantNext, bot.edits[0].HTML)
	}

	// Both files are in a single commit (one subject line).
	subjects := gitLogSubjects(t, repoPath)
	if len(subjects) < 2 {
		t.Fatalf("expected >=2 commits, got %d: %v", len(subjects), subjects)
	}
	if !strings.HasPrefix(subjects[0], "done: water plants (recurring, next ") {
		t.Fatalf("expected recurring done commit subject, got %q", subjects[0])
	}
}

func TestHandleCallbackDoneAlreadyDoneSurfacesToast(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:     "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title:  "stale",
		Status: "done",
	})

	upd := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb", UserID: 100, ChatID: 5, MessageID: 9, Data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.answers) != 1 || bot.answers[0].Toast != "already done" {
		t.Fatalf("expected 'already done' toast, got %+v", bot.answers)
	}
	if len(bot.edits) != 0 {
		t.Fatalf("expected no edits for already-done, got %d", len(bot.edits))
	}
}

func TestHandleCallbackActiveTogglesTagAndEditsRow(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "draft post",
	})

	upd := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb", UserID: 100, ChatID: 5, MessageID: 9, Data: "active:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got, err := s.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.IsActive() {
		t.Fatalf("expected task to be active after first toggle, tags=%v", got.Tags)
	}

	if len(bot.edits) != 1 {
		t.Fatalf("expected 1 edit after first toggle, got %d", len(bot.edits))
	}
	if len(bot.edits[0].Keyboard) != 1 || len(bot.edits[0].Keyboard[0]) != 3 {
		t.Fatalf("expected 3-button summary keyboard after toggle, got %+v", bot.edits[0].Keyboard)
	}

	// Second toggle should clear it.
	upd2 := Update{
		UpdateID: 2,
		Callback: &CallbackQuery{ID: "cb2", UserID: 100, ChatID: 5, MessageID: 9, Data: "active:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), upd2); err != nil {
		t.Fatalf("Handle 2: %v", err)
	}
	got2, _ := s.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if got2.IsActive() {
		t.Fatalf("expected task to be inactive after second toggle, tags=%v", got2.Tags)
	}
	if len(bot.edits) != 2 {
		t.Fatalf("expected 2 edits total, got %d", len(bot.edits))
	}
	// Toast feedback: first toggle says "active on", second says
	// "active off". Pinned text — the toast is the only confirmation
	// users get before the EditMessage round-trip lands.
	if len(bot.answers) != 2 {
		t.Fatalf("expected 2 answers, got %d: %+v", len(bot.answers), bot.answers)
	}
	if bot.answers[0].Toast != "active on" {
		t.Fatalf("first toggle toast: got %q want %q", bot.answers[0].Toast, "active on")
	}
	if bot.answers[1].Toast != "active off" {
		t.Fatalf("second toggle toast: got %q want %q", bot.answers[1].Toast, "active off")
	}
}

// TestHandleCallbackActivePlainTaskRowChangesBetweenStates is the regression
// guard for the silent EditMessage failure on plainest-case tasks. Before
// the ⭐ marker fix, a task with no other tags / no recur / no notes
// rendered identically before vs. after toggling active — Telegram then
// rejects the editMessageText call with `Bad Request: message is not
// modified`. We simulate the API failure by configuring fakeBot.editErr
// to the exact error string and assert two things:
//   - the FormatTaskRow output for the active state is non-empty and
//     differs from the inactive rendering (caught by FormatTaskRow tests),
//   - the handler still answers the callback even when EditMessage fails,
//     so the user is not left with a spinning loading indicator.
func TestHandleCallbackActivePlainTaskRowChangesBetweenStates(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "buy milk", // no tags, no recur, no body → no NoteCount
	})

	// Simulate Telegram returning the exact "message is not modified"
	// error — fakeBot.editErr is returned verbatim by EditMessage.
	bot.editErr = fmt.Errorf("Bad Request: message is not modified")

	upd := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb", UserID: 100, ChatID: 5, MessageID: 9, Data: "active:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	// Handle returns the EditMessage error wrapped — that's expected
	// (Serve logs it). We only care that the callback got answered.
	_ = h.Handle(context.Background(), upd)

	if len(bot.answers) != 1 {
		t.Fatalf("expected exactly 1 AnswerCallback even on edit failure, got %d: %+v",
			len(bot.answers), bot.answers)
	}
	if bot.answers[0].Toast != "active on" {
		t.Fatalf("expected toast %q, got %q", "active on", bot.answers[0].Toast)
	}

	// And — most importantly — the row rendering differs between
	// active-on and active-off. This is what prevents Telegram from
	// rejecting the edit with "message is not modified" in production.
	got, _ := s.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if !got.IsActive() {
		t.Fatalf("toggle should have set active, tags=%v", got.Tags)
	}
	activeRow := FormatTaskRow(got)
	got.SetActive(false)
	inactiveRow := FormatTaskRow(got)
	if activeRow == inactiveRow {
		t.Fatalf("active and inactive rows are identical — Telegram would reject the edit:\n active:   %q\n inactive: %q",
			activeRow, inactiveRow)
	}
}

func TestHandleCallbackViewExpandsToDetail(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "the title",
		Body:  "body content",
		Tags:  []string{"work"},
	})

	upd := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb", UserID: 100, ChatID: 5, MessageID: 9, Data: "view:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.edits) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(bot.edits))
	}
	html := bot.edits[0].HTML
	if !strings.Contains(html, "Schedule:") || !strings.Contains(html, "body content") {
		t.Fatalf("detail view missing expected fields: %q", html)
	}
	kb := bot.edits[0].Keyboard
	if len(kb) != 1 || len(kb[0]) != 3 {
		t.Fatalf("expected 3-button detail keyboard, got %+v", kb)
	}
	// First button is Collapse on the detail keyboard.
	if !strings.Contains(kb[0][0].Text, "Collapse") {
		t.Fatalf("expected first button to be Collapse, got %q", kb[0][0].Text)
	}
	if len(bot.answers) != 1 || bot.answers[0].Toast != "" {
		t.Fatalf("expected silent answer, got %+v", bot.answers)
	}
}

func TestHandleCallbackAnswersEvenWhenEditMessageFails(t *testing.T) {
	// Telegram requires every callback query to be answered within ~15s
	// or the loading spinner persists on the button forever. The four
	// callback branches that call EditMessage (done / active / view /
	// collapse) MUST answer the callback regardless of whether the edit
	// succeeded.
	cases := []struct {
		name   string
		data   string
		status string // "done" forces handleCallbackDone path; otherwise open
	}{
		{name: "done", data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		{name: "active", data: "active:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		{name: "view", data: "view:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		{name: "collapse", data: "collapse:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, bot, s, _ := newTestHandler(t, []int64{100})
			seedSingleTask(t, s, model.Task{
				ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
				Title: "ship it",
			})
			// Force every EditMessage call to fail.
			bot.editErr = fmt.Errorf("telegram: simulated edit failure")

			upd := Update{
				UpdateID: 1,
				Callback: &CallbackQuery{ID: "cb-" + tc.name, UserID: 100, ChatID: 5, MessageID: 9, Data: tc.data},
			}
			// Done/Active wrap edit failure in an error return (write-side
			// paths propagate so the Serve loop can log); view/collapse do
			// the same. Either way the callback MUST be answered.
			_ = h.Handle(context.Background(), upd)

			if len(bot.answers) != 1 {
				t.Fatalf("%s: expected exactly 1 AnswerCallback even on edit failure, got %d: %+v",
					tc.name, len(bot.answers), bot.answers)
			}
			if bot.answers[0].CallbackID != "cb-"+tc.name {
				t.Fatalf("%s: AnswerCallback targeted wrong ID: got %q want %q",
					tc.name, bot.answers[0].CallbackID, "cb-"+tc.name)
			}
		})
	}
}

func TestHandleCallbackCollapseRevertsToSummary(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "the title",
		Body:  "body content",
	})

	upd := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb", UserID: 100, ChatID: 5, MessageID: 9, Data: "collapse:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.edits) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(bot.edits))
	}
	html := bot.edits[0].HTML
	if strings.Contains(html, "Schedule:") || strings.Contains(html, "body content") {
		t.Fatalf("collapse should drop detail fields, got %q", html)
	}
	if !strings.Contains(html, "the title") {
		t.Fatalf("expected title in summary, got %q", html)
	}
	kb := bot.edits[0].Keyboard
	if len(kb) != 1 || len(kb[0]) != 3 {
		t.Fatalf("expected 3-button summary keyboard, got %+v", kb)
	}
	// First button on summary keyboard is Done.
	if !strings.Contains(kb[0][0].Text, "Done") {
		t.Fatalf("expected first summary button to be Done, got %q", kb[0][0].Text)
	}
}

func TestHandleCallbackReadOnlyBlocksDoneAndActiveAllowsView(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "frozen",
	})
	h.SetReadOnly(true)

	// Done is blocked.
	if err := h.Handle(context.Background(), Update{
		Callback: &CallbackQuery{ID: "cb1", UserID: 100, ChatID: 5, MessageID: 9, Data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}); err != nil {
		t.Fatalf("Done Handle: %v", err)
	}
	if got, _ := s.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV"); got.Status != "open" {
		t.Fatalf("readOnly should leave task open, got status=%q", got.Status)
	}

	// Active is blocked.
	if err := h.Handle(context.Background(), Update{
		Callback: &CallbackQuery{ID: "cb2", UserID: 100, ChatID: 5, MessageID: 9, Data: "active:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}); err != nil {
		t.Fatalf("Active Handle: %v", err)
	}
	if got, _ := s.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV"); got.IsActive() {
		t.Fatalf("readOnly should leave task inactive, tags=%v", got.Tags)
	}

	// Both write-side answers are present and use the exact wording the
	// plan pinned. The pinned text appears verbatim in the user's toast,
	// so we assert on the full string rather than a loose substring.
	if len(bot.answers) < 2 {
		t.Fatalf("expected at least 2 answers, got %+v", bot.answers)
	}
	const wantToast = "sync conflict — resolve on laptop"
	for _, a := range bot.answers[:2] {
		if a.Toast != wantToast {
			t.Fatalf("expected toast %q, got %+v", wantToast, a)
		}
	}

	// View is allowed even in read-only mode.
	if err := h.Handle(context.Background(), Update{
		Callback: &CallbackQuery{ID: "cb3", UserID: 100, ChatID: 5, MessageID: 9, Data: "view:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}); err != nil {
		t.Fatalf("View Handle: %v", err)
	}
	if len(bot.edits) != 1 {
		t.Fatalf("expected View to land 1 edit, got %d", len(bot.edits))
	}
}

func TestHandleNoteReplyAppendsNoteAndCommits(t *testing.T) {
	// Replying with text to a task summary message must append a
	// timestamped note to the task's body and land a single git commit
	// with the canonical "note: <title>" subject.
	h, bot, s, repoPath := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "ship it",
		Body:  "original body",
	})

	upd := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID:  5,
			UserID:  100,
			Text:    "tested locally, looks good",
			ReplyTo: &Message{ChatID: 5, UserID: 100, MessageID: 7, Text: "01ARZ  ship it"},
		},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got, err := s.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(got.Body, "tested locally, looks good") {
		t.Fatalf("body should include new note, got %q", got.Body)
	}
	if !strings.Contains(got.Body, "original body") {
		t.Fatalf("body should retain original content, got %q", got.Body)
	}
	if got.NoteCount != 1 {
		t.Fatalf("NoteCount=%d want 1; body=%q", got.NoteCount, got.Body)
	}

	if len(bot.sent) != 1 {
		t.Fatalf("expected one ack message, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "note added") {
		t.Fatalf("expected note-added ack, got %q", bot.sent[0].HTML)
	}
	if len(bot.sent[0].Keyboard) != 0 {
		t.Fatalf("note ack should carry no keyboard, got %+v", bot.sent[0].Keyboard)
	}

	subjects := gitLogSubjects(t, repoPath)
	if len(subjects) < 2 {
		t.Fatalf("expected >=2 commits, got %d: %v", len(subjects), subjects)
	}
	if subjects[0] != "note: ship it" {
		t.Fatalf("commit subject=%q want %q", subjects[0], "note: ship it")
	}
}

// TestHandleNoteReplyOnActiveTaskAppendsNote is the regression guard for
// the ⭐-shadows-the-ULID bug: FormatTaskRow prepends "⭐ " for active
// tasks, so the quoted reply text a Telegram client hands back starts
// with the marker, not the prefix. Peeling the first token blindly fed
// "⭐" to store.Resolve — below the 2-char minimum — and the note was
// silently dropped. The row is rendered through FormatTaskRow (rather
// than hand-written) so a future marker change is caught here too.
func TestHandleNoteReplyOnActiveTaskAppendsNote(t *testing.T) {
	h, bot, s, repoPath := newTestHandler(t, []int64{100})
	task := model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "ship it",
		Body:  "original body",
		Tags:  []string{model.ActiveTag},
	}
	seedSingleTask(t, s, task)

	upd := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID: 5,
			UserID: 100,
			Text:   "tested locally, looks good",
			ReplyTo: &Message{
				ChatID: 5, UserID: 100, MessageID: 7,
				Text: stripHTMLTags(FormatTaskRow(task)),
			},
		},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got, err := s.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(got.Body, "tested locally, looks good") {
		t.Fatalf("body should include new note, got %q", got.Body)
	}
	if got.NoteCount != 1 {
		t.Fatalf("NoteCount=%d want 1; body=%q", got.NoteCount, got.Body)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].HTML, "note added") {
		t.Fatalf("expected note-added ack, got %+v", bot.sent)
	}
	if subjects := gitLogSubjects(t, repoPath); subjects[0] != "note: ship it" {
		t.Fatalf("commit subject=%q want %q", subjects[0], "note: ship it")
	}
}

// TestHandleNoteReplyOnDoneRowAppendsNote covers the sibling marker:
// after a ✅ tap the summary message is edited into FormatDoneRow, and a
// reply to that edited message must still resolve the task.
func TestHandleNoteReplyOnDoneRowAppendsNote(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{100})
	task := model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "ship it",
		Body:  "original body",
	}
	seedSingleTask(t, s, task)

	upd := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID: 5,
			UserID: 100,
			Text:   "shipped at 4pm",
			ReplyTo: &Message{
				ChatID: 5, UserID: 100, MessageID: 7,
				Text: stripHTMLTags(FormatDoneRow(task, "")),
			},
		},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got, err := s.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(got.Body, "shipped at 4pm") {
		t.Fatalf("body should include new note, got %q", got.Body)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].HTML, "note added") {
		t.Fatalf("expected note-added ack, got %+v", bot.sent)
	}
}

func TestHandleNoteReplyEmptyFirstTokenReplies(t *testing.T) {
	// A reply whose replied-to text is all whitespace yields no token at
	// all — the handler must point the user at the prefix shape rather
	// than reach the resolver with an empty input.
	h, bot, s, _ := newTestHandler(t, []int64{100})

	upd := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID:  5,
			UserID:  100,
			Text:    "hi",
			ReplyTo: &Message{ChatID: 5, UserID: 100, MessageID: 7, Text: "   "},
		},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 hint reply, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "could not find a task ID") {
		t.Fatalf("expected missing-prefix hint, got %q", bot.sent[0].HTML)
	}
	tasks, _ := s.List(store.ListOptions{})
	if len(tasks) != 0 {
		t.Fatalf("expected no store mutation, got %d tasks", len(tasks))
	}
}

func TestHandleNoteReplyUnknownPrefixSurfacesResolveError(t *testing.T) {
	// A reply whose first token does not resolve to any task surfaces the
	// store's error verbatim — the user sees "task not found" wording so
	// the hint matches what the laptop CLI would say.
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "ship it",
	})

	upd := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID:  5,
			UserID:  100,
			Text:    "note text",
			ReplyTo: &Message{ChatID: 5, UserID: 100, MessageID: 7, Text: "ZZZZZ  some other task"},
		},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "could not resolve task") {
		t.Fatalf("expected resolve-error reply, got %q", bot.sent[0].HTML)
	}
	// Store is unchanged — body of the seed task still empty.
	got, _ := s.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if got.NoteCount != 0 {
		t.Fatalf("expected no notes added, got %d", got.NoteCount)
	}
}

func TestHandleNoteReplyAmbiguousPrefixIsEscaped(t *testing.T) {
	// When the prefix matches multiple tasks by initials, store.Resolve
	// returns an "ambiguous prefix" error containing user-controlled
	// task titles. Those titles MUST be HTML-escaped before the reply
	// goes back to Telegram so the bot doesn't get a parser rejection.
	//
	// We use leading-`<` titles so Initials("<x ...") starts with `<`,
	// making `<x` a valid two-character prefix that matches both tasks
	// (Initials is the first char of each whitespace-bounded word).
	h, bot, s, _ := newTestHandler(t, []int64{100})
	rfc := handlerTestNow.Format(time.RFC3339)
	if err := s.Create(model.Task{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5FA1",
		Title:     "<broken> fix",
		Status:    "open",
		Position:  1000,
		Schedule:  handlerTestNow.Format("2006-01-02"),
		CreatedAt: rfc,
		UpdatedAt: rfc,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.Create(model.Task{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5FA2",
		Title:     "<broken> feature",
		Status:    "open",
		Position:  2000,
		Schedule:  handlerTestNow.Format("2006-01-02"),
		CreatedAt: rfc,
		UpdatedAt: rfc,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Initials("<broken> fix") = "<f"; Initials("<broken> feature") = "<f"
	// — so `<f` is an ambiguous prefix matching both.
	upd := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID:  5,
			UserID:  100,
			Text:    "note text",
			ReplyTo: &Message{ChatID: 5, UserID: 100, MessageID: 7, Text: "<f something"},
		},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(bot.sent))
	}
	got := bot.sent[0].HTML
	// HTML metacharacters from the user titles must be escaped.
	if strings.Contains(got, "<broken>") {
		t.Fatalf("ambiguous-task reply must HTML-escape titles, got %q", got)
	}
	if !strings.Contains(got, "&lt;broken&gt;") {
		t.Fatalf("expected escaped title in reply, got %q", got)
	}
}

func TestHandleNoteReplyBlockedWhenReadOnly(t *testing.T) {
	// Note-append is a write — read-only mode rejects it the same way
	// capture does, with the canned sync-conflict message and no store
	// mutation.
	h, bot, s, _ := newTestHandler(t, []int64{100})
	seedSingleTask(t, s, model.Task{
		ID:    "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Title: "frozen",
	})
	h.SetReadOnly(true)

	upd := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID:  5,
			UserID:  100,
			Text:    "note attempt",
			ReplyTo: &Message{ChatID: 5, UserID: 100, MessageID: 7, Text: "01ARZ  frozen"},
		},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].HTML, "sync conflict") {
		t.Fatalf("expected sync-conflict reply, got %q", bot.sent[0].HTML)
	}
	got, _ := s.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if got.NoteCount != 0 || got.Body != "" {
		t.Fatalf("expected store unchanged, got body=%q notes=%d", got.Body, got.NoteCount)
	}
}

// TestPrefixIDRoundTripsThroughStoreResolve enforces the contract that
// couples the rendering path (FormatTaskRow, which surfaces prefixID(t.ID)
// as the first whitespace-bounded token) and the reply-resolver path
// (handleNoteReply, which peels the prefix off the user's quoted reply and
// passes the result to store.Resolve). If prefixLength were ever bumped or
// FormatTaskRow's layout reshuffled to hide the ID in the middle of the
// line, this test would fail and force a re-validation of the coupling.
//
// We emulate the Telegram client's behavior of stripping HTML tags from a
// quoted message so the captured token mirrors what handleNoteReply sees
// on `m.ReplyTo.Text` after the round-trip through the user's phone.
func TestPrefixIDRoundTripsThroughStoreResolve(t *testing.T) {
	_, s := initTelegramTestRepo(t)
	const id = "01J5K7VC9RXMQ8NPZF2W3Y4ABC"
	task := model.Task{ID: id, Title: "buy milk", Tags: []string{"shopping"}}
	seedSingleTask(t, s, task)

	// Every row variant a user can reply to must round-trip: the plain
	// row, the active row (⭐ marker), and the done row (✅ marker).
	active := task
	active.Tags = []string{"shopping", model.ActiveTag}
	rows := map[string]string{
		"plain":  FormatTaskRow(task),
		"active": FormatTaskRow(active),
		"done":   FormatDoneRow(task, ""),
	}

	for name, rendered := range rows {
		t.Run(name, func(t *testing.T) {
			// Strip HTML tags the way a Telegram client would when
			// quoting the message in a reply. Naive but sufficient: no
			// nested tags, no attributes besides what the formatters
			// emit today.
			stripped := stripHTMLTags(rendered)

			prefix := taskPrefixFromRow(stripped)
			if prefix == "" {
				t.Fatalf("taskPrefixFromRow of rendered row %q returned empty", stripped)
			}
			if len(prefix) != prefixLength {
				t.Fatalf("taskPrefixFromRow returned %d chars, want exactly prefixLength=%d (rendered=%q)", len(prefix), prefixLength, stripped)
			}

			resolved, err := s.Resolve(prefix)
			if err != nil {
				t.Fatalf("store.Resolve(%q) error = %v (rendered=%q)", prefix, err, stripped)
			}
			if resolved.ID != id {
				t.Fatalf("store.Resolve(%q) = %q, want %q", prefix, resolved.ID, id)
			}
		})
	}
}

// stripHTMLTags removes balanced `<tag>...</tag>` markup and self-closing
// tags from s, returning the inner text. Used by the prefixID round-trip
// test to simulate Telegram's "quoted reply" rendering, which surfaces
// only the visible text to the bot.
func stripHTMLTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestTaskPrefixFromRow(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   \t\n", ""},
		{"abc", "abc"},
		{"abc def", "abc"},
		{"  abc def", "abc"},
		{"01ARZ original-text", "01ARZ"},
		{"\t\nABC123", "ABC123"},
		// Row markers are stepped over, not treated as the prefix.
		{"⭐ 01ARZ buy milk", "01ARZ"},
		{"✅ 01ARZ buy milk", "01ARZ"},
		{"  ⭐   01ARZ  buy milk", "01ARZ"},
		{"⭐", ""},
		{"⭐ ✅ 01ARZ", "01ARZ"},
		// A marker mid-row is not leading, so it never shadows a prefix.
		{"01ARZ ⭐ starred", "01ARZ"},
	}
	for _, c := range cases {
		got := taskPrefixFromRow(c.in)
		if got != c.want {
			t.Errorf("taskPrefixFromRow(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

// --- Freshness gate: per-command wiring --------------------------------
//
// sync_test.go pins the gate's clock arithmetic; these pin which commands are
// wired to it. The split matters: the gate is only useful if the paths that
// read pre-existing state actually call it, and only cheap if capture does
// not.

func TestCommandPullsOnceWhenStaleThenSkipsInsideTheWindow(t *testing.T) {
	fn, count := countingPull()
	withPullFunc(t, fn)
	clk := newMutableClock(handlerTestNow)
	h, bot, s, _ := newTestHandlerWithClock(t, []int64{100}, clk.now)
	seedBrowseTasks(t, s)

	browse := func(cmd string) {
		t.Helper()
		if err := h.Handle(context.Background(), Update{
			UpdateID: 1,
			Message:  &Message{ChatID: 5, UserID: 100, Text: cmd},
		}); err != nil {
			t.Fatalf("Handle %s: %v", cmd, err)
		}
	}

	browse("/today")
	if got := count(); got != 1 {
		t.Fatalf("first command pulls=%d want 1", got)
	}

	// A burst of follow-up commands inside the window must cost nothing.
	browse("/week")
	browse("/active")
	browse("/all")
	if got := count(); got != 1 {
		t.Fatalf("burst inside the window pulls=%d want 1", got)
	}
	if len(bot.sent) == 0 {
		t.Fatal("expected the browse commands to reply")
	}

	// Past the window, the next command re-fetches.
	clk.advance(commandPullMaxAge + time.Second)
	browse("/today")
	if got := count(); got != 2 {
		t.Fatalf("command after the window expired pulls=%d want 2", got)
	}
}

func TestCaptureDoesNotPrePullButDoneCallbackDoes(t *testing.T) {
	fn, count := countingPull()
	withPullFunc(t, fn)
	withSyncFunc(t, noopSync)
	h, bot, s, _ := newTestHandler(t, []int64{100})

	// Capture: a fresh ULID cannot conflict and commitAndSync pulls after
	// the write, so the most latency-sensitive path skips the pre-pull.
	if err := h.Handle(context.Background(), Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "buy milk"},
	}); err != nil {
		t.Fatalf("capture Handle: %v", err)
	}
	if got := count(); got != 0 {
		t.Fatalf("capture pre-pulled: pulls=%d want 0", got)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected the capture summary card, got %d messages", len(bot.sent))
	}

	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("store count=%d want 1", len(tasks))
	}

	// A done: callback acts on EXISTING state the laptop may have changed,
	// so it does pre-pull. Capture left the clock unstamped, so this is the
	// gate's first fetch.
	if err := h.Handle(context.Background(), Update{
		UpdateID: 2,
		Callback: &CallbackQuery{ID: "cb1", UserID: 100, ChatID: 5, MessageID: 9, Data: "done:" + tasks[0].ID},
	}); err != nil {
		t.Fatalf("done Handle: %v", err)
	}
	if got := count(); got != 1 {
		t.Fatalf("done callback pulls=%d want 1", got)
	}
	if got, _ := s.Get(tasks[0].ID); got.Status != "done" {
		t.Fatalf("expected the done callback to still complete the task, status=%q", got.Status)
	}
}

func TestNoteReplyPrePulls(t *testing.T) {
	fn, count := countingPull()
	withPullFunc(t, fn)
	withSyncFunc(t, noopSync)
	h, _, s, _ := newTestHandler(t, []int64{100})
	const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	seedSingleTask(t, s, model.Task{ID: id, Title: "frozen"})

	if err := h.Handle(context.Background(), Update{
		UpdateID: 1,
		Message: &Message{
			ChatID:  5,
			UserID:  100,
			Text:    "a note",
			ReplyTo: &Message{ChatID: 5, UserID: 100, MessageID: 7, Text: "01ARZ  frozen"},
		},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := count(); got != 1 {
		t.Fatalf("note reply pulls=%d want 1", got)
	}
	got, _ := s.Get(id)
	if got.NoteCount != 1 {
		t.Fatalf("expected the note to still land, NoteCount=%d", got.NoteCount)
	}
}

func TestPrePullFailureStillRepliesAndDoesNotSetReadOnly(t *testing.T) {
	withPullFunc(t, func(string) error { return errors.New("dial tcp: network is unreachable") })
	h, bot, s, _ := newTestHandler(t, []int64{100})
	var log bytes.Buffer
	h.SetWriter(&log)
	seedBrowseTasks(t, s)

	if err := h.Handle(context.Background(), Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}); err != nil {
		t.Fatalf("a failed pre-pull must not fail the command: %v", err)
	}
	// Served from local state: the two today-bucket rows, no read-only
	// banner in front of them.
	if len(bot.sent) != 2 {
		t.Fatalf("expected 2 today rows served from local state, got %d: %+v", len(bot.sent), bot.sent)
	}
	if strings.Contains(bot.sent[0].HTML, "read-only") {
		t.Fatalf("a failed pre-pull must not set readOnly; got banner %q", bot.sent[0].HTML)
	}
	if h.IsReadOnly() {
		t.Fatal("a failed pre-pull must leave readOnly clear — only a stuck rebase sets it")
	}
	if !strings.Contains(log.String(), "command pull") || !strings.Contains(log.String(), "network is unreachable") {
		t.Fatalf("expected the failure logged to the Serve writer, got %q", log.String())
	}
}

func TestRemoteTaskAppearsOnTheCommandAfterTheGateExpires(t *testing.T) {
	clk := newMutableClock(handlerTestNow)
	h, bot, s, _ := newTestHandlerWithClock(t, []int64{100}, clk.now)

	// The second fetch is the one that lands the laptop's commit — the
	// first runs against a remote that does not have it yet.
	const remoteID = "01ARZ3NDEKTSV4RRFFQ69G5FA9"
	var pulls int
	withPullFunc(t, func(string) error {
		pulls++
		if pulls == 2 {
			seedSingleTask(t, s, model.Task{ID: remoteID, Title: "pushed from the laptop"})
		}
		return nil
	})

	browse := func() {
		t.Helper()
		if err := h.Handle(context.Background(), Update{
			UpdateID: 1,
			Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
		}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}

	browse()
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].HTML, "nothing") {
		t.Fatalf("expected an empty-bucket reply before the remote task lands, got %+v", bot.sent)
	}

	clk.advance(commandPullMaxAge + time.Second)
	bot.sent = nil
	browse()
	if pulls != 2 {
		t.Fatalf("expected the expired gate to fetch, pulls=%d want 2", pulls)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].HTML, "pushed from the laptop") {
		t.Fatalf("expected the newly pulled task in the reply, got %+v", bot.sent)
	}
}

// containsAll returns true when haystack contains every element of needles.
// Used by tag-membership assertions where we don't care about ordering.
func containsAll(haystack, needles []string) bool {
	for _, n := range needles {
		found := false
		for _, h := range haystack {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestEveryBrowseCommandPrePullsFromCold is the per-handler twin of
// TestCommandPullsOnceWhenStaleThenSkipsInsideTheWindow, which only exercises
// /week, /active and /all from INSIDE the freshness window — where the expected
// count is 1 whether or not those handlers call the gate at all. Each row here
// starts from a cold handler, so deleting h.pullBeforeCommand() from any single
// browse handler fails exactly that row.
func TestEveryBrowseCommandPrePullsFromCold(t *testing.T) {
	for _, cmd := range []string{"/today", "/week", "/active", "/all"} {
		t.Run(cmd, func(t *testing.T) {
			fn, count := countingPull()
			withPullFunc(t, fn)
			h, bot, s, _ := newTestHandler(t, []int64{100})
			seedBrowseTasks(t, s)

			if err := h.Handle(context.Background(), Update{
				UpdateID: 1,
				Message:  &Message{ChatID: 5, UserID: 100, Text: cmd},
			}); err != nil {
				t.Fatalf("Handle %s: %v", cmd, err)
			}
			if got := count(); got != 1 {
				t.Fatalf("%s from a cold handler pulls=%d want 1: the handler is not wired to the freshness gate", cmd, got)
			}
			if len(bot.sent) == 0 {
				t.Fatalf("%s sent no reply", cmd)
			}
		})
	}
}

// TestDoneCallbackPrePullHealsReadOnly covers the deliberate ordering in
// handleCallback: the gate runs BEFORE the readOnly guard, so a button tap on a
// healthy network clears a stale conflict flag and lets the write through.
func TestDoneCallbackPrePullHealsReadOnly(t *testing.T) {
	fn, count := countingPull()
	withPullFunc(t, fn)
	withSyncFunc(t, noopSync)
	h, bot, s, _ := newTestHandler(t, []int64{100})
	ids := seedBrowseTasks(t, s)
	h.SetReadOnly(true)

	if err := h.Handle(context.Background(), Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb1", UserID: 100, ChatID: 5, MessageID: 9, Data: "done:" + ids[0]},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := count(); got != 1 {
		t.Fatalf("pulls=%d want 1", got)
	}
	if h.IsReadOnly() {
		t.Error("IsReadOnly() = true; a clean pre-pull must clear the flag")
	}
	if got, _ := s.Get(ids[0]); got.Status != "done" {
		t.Errorf("task status = %q, want done: the healed handler must let the write through", got.Status)
	}
	if len(bot.sent) == 0 && len(bot.edits) == 0 {
		t.Error("expected the callback to reply")
	}
}

// TestNoteReplyRefusesWhileReadOnlyEvenOnAHealthyNetwork is the mirror: the
// note path puts its gate AFTER the readOnly guard on purpose, so a conflicted
// bot refuses the write without paying for a network round-trip first.
func TestNoteReplyRefusesWhileReadOnlyEvenOnAHealthyNetwork(t *testing.T) {
	fn, count := countingPull()
	withPullFunc(t, fn)
	withSyncFunc(t, noopSync)
	h, bot, s, _ := newTestHandler(t, []int64{100})
	ids := seedBrowseTasks(t, s)
	before, err := s.Get(ids[0])
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	h.SetReadOnly(true)

	if err := h.Handle(context.Background(), Update{
		UpdateID: 1,
		Message: &Message{
			ChatID: 5, UserID: 100, Text: "a note",
			ReplyTo: &Message{ChatID: 5, Text: ids[0][:8] + " ship login bug fix"},
		},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := count(); got != 0 {
		t.Fatalf("pulls=%d want 0: the read-only guard runs first on the note path", got)
	}
	if !h.IsReadOnly() {
		t.Error("IsReadOnly() = false; the note path must not heal the flag")
	}
	after, err := s.Get(ids[0])
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.Body != before.Body {
		t.Errorf("body changed while read-only: %q -> %q", before.Body, after.Body)
	}
	if len(bot.sent) != 1 || bot.sent[0].HTML != readOnlyMessage {
		t.Errorf("expected the read-only refusal, got %+v", bot.sent)
	}
}
