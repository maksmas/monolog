package telegram

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/store"
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
	repoPath, s := initTelegramTestRepo(t)
	bot := &fakeBot{}
	h := NewHandler(bot, s, repoPath, TelegramConfig{
		AllowedUserIDs: allowed,
		PullInterval:   30 * time.Second,
		BrowseLimit:    20,
	}, "02-01-2006", func() time.Time { return handlerTestNow })
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

func TestHandleCallbackPlaceholderAnswersWithEmptyToast(t *testing.T) {
	// Task 5 only stubs the callback path: the real dispatcher lands in
	// task 7. For now we verify allowed users get an empty-toast answer
	// (spinner stops) and non-allowed users get a silent drop.
	h, bot, _, _ := newTestHandler(t, []int64{100})

	cbAllowed := Update{
		UpdateID: 1,
		Callback: &CallbackQuery{ID: "cb1", UserID: 100, ChatID: 5, MessageID: 99, Data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), cbAllowed); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.answers) != 1 || bot.answers[0].CallbackID != "cb1" || bot.answers[0].Toast != "" {
		t.Fatalf("expected one empty-toast answer for allowed user, got %+v", bot.answers)
	}

	cbDenied := Update{
		UpdateID: 2,
		Callback: &CallbackQuery{ID: "cb2", UserID: 999, ChatID: 5, MessageID: 99, Data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	if err := h.Handle(context.Background(), cbDenied); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.answers) != 1 {
		t.Fatalf("expected denied callback to be silently dropped (still 1 answer), got %d", len(bot.answers))
	}
}

func TestHandleReplyToMessageIsIgnoredUntilTask8(t *testing.T) {
	// Until task 8 wires up handleNoteReply, a message with ReplyTo set
	// must NOT fall through to the capture path. This test pins the
	// boundary so a future refactor that accidentally re-enables the
	// fall-through gets caught.
	h, bot, s, _ := newTestHandler(t, []int64{100})

	upd := Update{
		UpdateID: 1,
		Message: &Message{
			ChatID:  5,
			UserID:  100,
			Text:    "this is a note",
			ReplyTo: &Message{ChatID: 5, UserID: 100, MessageID: 7, Text: "01ARZ original"},
		},
	}
	if err := h.Handle(context.Background(), upd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(bot.sent) != 0 {
		t.Fatalf("expected no outbound message, got %d", len(bot.sent))
	}
	tasks, _ := s.List(store.ListOptions{})
	if len(tasks) != 0 {
		t.Fatalf("expected no task created via reply path, got %d", len(tasks))
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
	if !strings.Contains(bot.sent[0].HTML, "read-only") {
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
	if !strings.Contains(bot.sent[0].HTML, "read-only") {
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

func TestHandleSlashHelpAndStartStub(t *testing.T) {
	// Task 8 lands the real /help and /start replies; for now task 6's
	// stub should send a one-line placeholder so users hitting these
	// commands don't see silence.
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
