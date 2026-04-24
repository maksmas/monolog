package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
)

// helper to create a temp store
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, ".monolog", "tasks")
	s, err := New(tasksDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	return s
}

func sampleTask(id, title string) model.Task {
	return model.Task{
		ID:        id,
		Title:     title,
		Source:    "manual",
		Status:    "open",
		Position:  1000,
		Schedule:  "today",
		CreatedAt: "2026-04-11T10:00:00Z",
		UpdatedAt: "2026-04-11T10:00:00Z",
	}
}

// --- CRUD tests ---

func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01AAA", "Buy milk")
	task.Body = "Whole milk, 2%"
	task.Tags = []string{"errands"}

	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.Get("01AAA")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.ID != task.ID {
		t.Errorf("ID: got %q, want %q", got.ID, task.ID)
	}
	if got.Title != task.Title {
		t.Errorf("Title: got %q, want %q", got.Title, task.Title)
	}
	if got.Body != task.Body {
		t.Errorf("Body: got %q, want %q", got.Body, task.Body)
	}
	if got.Source != task.Source {
		t.Errorf("Source: got %q, want %q", got.Source, task.Source)
	}
	if got.Status != task.Status {
		t.Errorf("Status: got %q, want %q", got.Status, task.Status)
	}
	if got.Position != task.Position {
		t.Errorf("Position: got %f, want %f", got.Position, task.Position)
	}
	if got.Schedule != task.Schedule {
		t.Errorf("Schedule: got %q, want %q", got.Schedule, task.Schedule)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "errands" {
		t.Errorf("Tags: got %v, want [errands]", got.Tags)
	}
}

func TestCreateDuplicate(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01AAA", "Buy milk")

	if err := s.Create(task); err != nil {
		t.Fatalf("first Create failed: %v", err)
	}
	if err := s.Create(task); err == nil {
		t.Fatal("expected error on duplicate Create, got nil")
	}
}

func TestGetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get("NONEXISTENT")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestUpdate(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01AAA", "Buy milk")
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	task.Title = "Buy oat milk"
	task.UpdatedAt = "2026-04-11T11:00:00Z"
	if err := s.Update(task); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := s.Get("01AAA")
	if err != nil {
		t.Fatalf("Get after Update failed: %v", err)
	}
	if got.Title != "Buy oat milk" {
		t.Errorf("Title after Update: got %q, want %q", got.Title, "Buy oat milk")
	}
	if got.UpdatedAt != "2026-04-11T11:00:00Z" {
		t.Errorf("UpdatedAt after Update: got %q, want %q", got.UpdatedAt, "2026-04-11T11:00:00Z")
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("NONEXISTENT", "Ghost")
	err := s.Update(task)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// TestUpdateRecalculatesNoteCount verifies that Store.Update overrides a stale
// in-memory NoteCount using the count of note separators in Body.
func TestUpdateRecalculatesNoteCount(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01AAA", "Task with notes")
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	task.Body = "--- 17-04-2026 10:00:00 ---\nfirst note\n\n--- 17-04-2026 11:00:00 ---\nsecond note"
	task.NoteCount = 99 // deliberately stale
	if err := s.Update(task); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := s.Get("01AAA")
	if err != nil {
		t.Fatalf("Get after Update failed: %v", err)
	}
	if got.NoteCount != 2 {
		t.Errorf("NoteCount: got %d, want 2", got.NoteCount)
	}
}

// TestUpdateResetsNoteCountWhenBodyHasNone verifies that Store.Update resets
// NoteCount to 0 when the body contains no note separators, even if the
// caller-supplied value was non-zero.
func TestUpdateResetsNoteCountWhenBodyHasNone(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01AAA", "Task without notes")
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	task.Body = "some body text without separators"
	task.NoteCount = 5 // deliberately stale
	if err := s.Update(task); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := s.Get("01AAA")
	if err != nil {
		t.Fatalf("Get after Update failed: %v", err)
	}
	if got.NoteCount != 0 {
		t.Errorf("NoteCount: got %d, want 0", got.NoteCount)
	}
}

// TestCreateRecalculatesNoteCount is a regression guard: Store.Create must
// recalculate NoteCount from Body so callers (like the recurrence spawn
// path) cannot accidentally persist a stale count. A deliberately-wrong
// NoteCount on the input must be corrected on disk.
func TestCreateRecalculatesNoteCount(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01AAA", "New task")
	task.Body = "first line\n--- 17-04-2026 10:00:00 ---\nfirst note\n" +
		"--- 17-04-2026 11:00:00 ---\nsecond note"
	task.NoteCount = 99 // stale; Create must overwrite
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.Get("01AAA")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.NoteCount != 2 {
		t.Errorf("NoteCount after Create: got %d, want 2 (body has two note separators)", got.NoteCount)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01AAA", "Buy milk")
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := s.Delete("01AAA"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify file is gone
	_, err := s.Get("01AAA")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after Delete, got: %v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.Delete("NONEXISTENT")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// --- GetByPrefix tests ---

func TestGetByPrefixExact(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01ABCDEF", "Task A")
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.GetByPrefix("01ABC")
	if err != nil {
		t.Fatalf("GetByPrefix failed: %v", err)
	}
	if got.ID != "01ABCDEF" {
		t.Errorf("ID: got %q, want %q", got.ID, "01ABCDEF")
	}
}

func TestGetByPrefixFullID(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01ABCDEF", "Task A")
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.GetByPrefix("01ABCDEF")
	if err != nil {
		t.Fatalf("GetByPrefix with full ID failed: %v", err)
	}
	if got.ID != "01ABCDEF" {
		t.Errorf("ID: got %q, want %q", got.ID, "01ABCDEF")
	}
}

func TestGetByPrefixAmbiguous(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01ABC111", "Task A")); err != nil {
		t.Fatalf("Create A failed: %v", err)
	}
	if err := s.Create(sampleTask("01ABC222", "Task B")); err != nil {
		t.Fatalf("Create B failed: %v", err)
	}

	_, err := s.GetByPrefix("01ABC")
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("expected ErrAmbiguous, got: %v", err)
	}
}

func TestGetByPrefixNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01AAA", "Task A")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err := s.GetByPrefix("99ZZZ")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestGetByPrefixEmptyStore(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetByPrefix("01A")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound on empty store, got: %v", err)
	}
}

func TestGetByPrefixEmptyString(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01AAA", "Task A")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err := s.GetByPrefix("")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for empty prefix, got: %v", err)
	}
}

func TestGetByPrefixIgnoresNonJSON(t *testing.T) {
	s := newTestStore(t)
	// Write a .gitkeep file that would match prefix "." if not filtered
	gitkeep := filepath.Join(s.dir, ".gitkeep")
	if err := os.WriteFile(gitkeep, []byte{}, 0o644); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}

	_, err := s.GetByPrefix(".git")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for non-JSON prefix match, got: %v", err)
	}
}

// --- Resolve tests ---

func TestResolveByULIDPrefix(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01ABCDEF", "Fix login bug")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.Resolve("01ABC")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got.ID != "01ABCDEF" {
		t.Errorf("ID: got %q, want %q", got.ID, "01ABCDEF")
	}
}

func TestResolveByInitials(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01AAA", "Fix login bug")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.Resolve("flb")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got.ID != "01AAA" {
		t.Errorf("ID: got %q, want %q", got.ID, "01AAA")
	}
}

func TestResolveByInitialsPrefix(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01AAA", "Fix login bug")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.Resolve("fl")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got.ID != "01AAA" {
		t.Errorf("ID: got %q, want %q", got.ID, "01AAA")
	}
}

func TestResolveByInitialsCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01AAA", "Fix login bug")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.Resolve("FLB")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got.ID != "01AAA" {
		t.Errorf("ID: got %q, want %q", got.ID, "01AAA")
	}
}

func TestResolveInitialsAmbiguous(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01AAA", "Fix login bug")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := s.Create(sampleTask("01BBB", "Find lost backup")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err := s.Resolve("fl")
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("expected ErrAmbiguous, got: %v", err)
	}
}

func TestResolveInitialsNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01AAA", "Fix login bug")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err := s.Resolve("xyz")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestResolveTooShortForInitials(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(sampleTask("01AAA", "Fix login bug")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// single char "f" should not trigger initials lookup, and won't match ULID prefix
	_, err := s.Resolve("f")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for single char, got: %v", err)
	}
}

func TestResolveSkipsDoneTasks(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01AAA", "Fix login bug")
	task.Status = "done"
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err := s.Resolve("flb")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for done task, got: %v", err)
	}
}

func TestResolveULIDPrefixTakesPriority(t *testing.T) {
	s := newTestStore(t)
	// Create a task whose ID starts with "fl" — ULID prefix should win
	if err := s.Create(sampleTask("fl123456", "Something else")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := s.Create(sampleTask("01AAA", "Fix login bug")); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.Resolve("fl")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	// Should match ULID prefix "fl123456", not initials "flb"
	if got.ID != "fl123456" {
		t.Errorf("ID: got %q, want %q (ULID prefix should take priority)", got.ID, "fl123456")
	}
}

// --- List tests ---

func TestListEmpty(t *testing.T) {
	s := newTestStore(t)
	tasks, err := s.List(ListOptions{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestListAll(t *testing.T) {
	s := newTestStore(t)
	createTestTasks(t, s)

	tasks, err := s.List(ListOptions{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(tasks))
	}
}

func TestListSortedByPosition(t *testing.T) {
	s := newTestStore(t)
	createTestTasks(t, s)

	tasks, err := s.List(ListOptions{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	for i := 1; i < len(tasks); i++ {
		if tasks[i].Position < tasks[i-1].Position {
			t.Errorf("tasks not sorted by position: %f before %f",
				tasks[i-1].Position, tasks[i].Position)
		}
	}
}

func TestListFilterByStatus(t *testing.T) {
	s := newTestStore(t)
	createTestTasks(t, s)

	tasks, err := s.List(ListOptions{Status: "done"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 done task, got %d", len(tasks))
	}
	if tasks[0].Title != "Done task" {
		t.Errorf("expected 'Done task', got %q", tasks[0].Title)
	}
}

func TestListFilterByTag(t *testing.T) {
	s := newTestStore(t)
	createTestTasks(t, s)

	tasks, err := s.List(ListOptions{Tag: "work"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks with 'work' tag, got %d", len(tasks))
	}
}

func TestListCombinedFilters(t *testing.T) {
	s := newTestStore(t)
	createTestTasks(t, s)

	tasks, err := s.List(ListOptions{Status: "open", Tag: "work"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	// Both today/work and tomorrow/work tasks are open and tagged "work".
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks matching status=open + tag=work, got %d", len(tasks))
	}
}

func TestListNoMatches(t *testing.T) {
	s := newTestStore(t)
	createTestTasks(t, s)

	tasks, err := s.List(ListOptions{Tag: "nonexistent"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestListIgnoresNonJSON(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01AAA", "Real task")
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// write a non-JSON file in the tasks dir
	nonJSON := filepath.Join(s.dir, "README.txt")
	if err := os.WriteFile(nonJSON, []byte("not a task"), 0o644); err != nil {
		t.Fatalf("failed to write non-JSON file: %v", err)
	}

	tasks, err := s.List(ListOptions{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task (ignoring non-JSON), got %d", len(tasks))
	}
}

// --- ULID generation test ---

func TestNewID(t *testing.T) {
	id1, err := model.NewID()
	if err != nil {
		t.Fatalf("NewID returned error: %v", err)
	}
	id2, err := model.NewID()
	if err != nil {
		t.Fatalf("NewID returned error: %v", err)
	}

	if id1 == "" {
		t.Error("NewID returned empty string")
	}
	if len(id1) != 26 {
		t.Errorf("ULID should be 26 chars, got %d: %q", len(id1), id1)
	}
	if id1 == id2 {
		t.Error("two NewID calls returned the same value")
	}
}

// --- JSON round-trip test ---

func TestJSONRoundTrip(t *testing.T) {
	s := newTestStore(t)
	task := model.Task{
		ID:        "01ROUNDTRIP",
		Title:     "Round trip test",
		Body:      "Body text",
		Source:    "manual",
		Status:    "open",
		Position:  1500.5,
		Schedule:  "tomorrow",
		CreatedAt: "2026-04-11T10:00:00Z",
		UpdatedAt: "2026-04-11T10:00:00Z",
		Tags:      []string{"tag1", "tag2"},
	}

	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.Get("01ROUNDTRIP")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.Position != 1500.5 {
		t.Errorf("Position: got %f, want 1500.5", got.Position)
	}
	if got.Body != "Body text" {
		t.Errorf("Body: got %q, want %q", got.Body, "Body text")
	}
	if len(got.Tags) != 2 || got.Tags[0] != "tag1" || got.Tags[1] != "tag2" {
		t.Errorf("Tags: got %v, want [tag1 tag2]", got.Tags)
	}
}

// --- Tags tests ---

func TestTagsEmpty(t *testing.T) {
	s := newTestStore(t)
	tags, err := s.Tags()
	if err != nil {
		t.Fatalf("Tags() failed: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("expected 0 tags on empty store, got %d: %v", len(tags), tags)
	}
}

func TestTagsSortedUnique(t *testing.T) {
	s := newTestStore(t)
	createTestTasks(t, s) // has tags: work, personal, work, work

	tags, err := s.Tags()
	if err != nil {
		t.Fatalf("Tags() failed: %v", err)
	}
	want := []string{"personal", "work"}
	if len(tags) != len(want) {
		t.Fatalf("Tags() = %v, want %v", tags, want)
	}
	for i := range tags {
		if tags[i] != want[i] {
			t.Errorf("Tags()[%d] = %q, want %q", i, tags[i], want[i])
		}
	}
}

func TestTagsMultipleTagsPerTask(t *testing.T) {
	s := newTestStore(t)
	task := sampleTask("01MULTI", "Multi-tag task")
	task.Tags = []string{"zebra", "alpha", "middle"}
	if err := s.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	tags, err := s.Tags()
	if err != nil {
		t.Fatalf("Tags() failed: %v", err)
	}
	want := []string{"alpha", "middle", "zebra"}
	if len(tags) != len(want) {
		t.Fatalf("Tags() = %v, want %v", tags, want)
	}
	for i := range tags {
		if tags[i] != want[i] {
			t.Errorf("Tags()[%d] = %q, want %q", i, tags[i], want[i])
		}
	}
}

// --- CreateBatch tests ---

// newGitStore sets up a fully initialized monolog repo via git.Init and
// returns the Store plus the repo path. Required for CreateBatch success-path
// tests because CreateBatch calls git.AutoCommit internally.
func newGitStore(t *testing.T) (*Store, string) {
	t.Helper()
	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := git.Init(repoPath, ""); err != nil {
		t.Fatalf("git.Init: %v", err)
	}
	s, err := New(filepath.Join(repoPath, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, repoPath
}

// headSubject returns the subject line of the current HEAD commit. Used to
// assert that CreateBatch used the caller-provided commit message verbatim.
func headSubject(t *testing.T, repoPath string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log -1: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestCreateBatch_EmptyInputIsNoOp(t *testing.T) {
	s, repoPath := newGitStore(t)
	before := headSubject(t, repoPath)
	if err := s.CreateBatch(nil, "slack: ingest 0 items"); err != nil {
		t.Fatalf("CreateBatch(nil): %v", err)
	}
	if err := s.CreateBatch([]model.Task{}, "slack: ingest 0 items"); err != nil {
		t.Fatalf("CreateBatch([]): %v", err)
	}
	after := headSubject(t, repoPath)
	if before != after {
		t.Errorf("HEAD subject changed on empty batch: before=%q after=%q", before, after)
	}
}

func TestCreateBatch_SuccessWritesAllAndCommitsOnce(t *testing.T) {
	s, repoPath := newGitStore(t)

	tasks := []model.Task{
		sampleTask("01BATCH1", "First slack task"),
		sampleTask("01BATCH2", "Second slack task"),
		sampleTask("01BATCH3", "Third slack task"),
	}
	msg := "slack: ingest 3 items"
	if err := s.CreateBatch(tasks, msg); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	// All three files on disk.
	for _, task := range tasks {
		got, err := s.Get(task.ID)
		if err != nil {
			t.Errorf("Get(%s) after batch: %v", task.ID, err)
			continue
		}
		if got.Title != task.Title {
			t.Errorf("Title mismatch for %s: got %q, want %q", task.ID, got.Title, task.Title)
		}
	}

	// Exactly one commit with the caller's message.
	subj := headSubject(t, repoPath)
	if subj != msg {
		t.Errorf("HEAD commit subject: got %q, want %q", subj, msg)
	}

	// Verify only one commit was added for the batch (count commits since init).
	cmd := exec.Command("git", "log", "--format=%s", "--since=1970-01-01")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	subjects := strings.Split(strings.TrimSpace(string(out)), "\n")
	// init commit + batch commit = 2.
	if len(subjects) != 2 {
		t.Errorf("expected 2 commits (init + batch), got %d: %v", len(subjects), subjects)
	}
}

// TestCreateBatch_RecalculatesNoteCount mirrors TestCreateRecalculatesNoteCount
// for the batch path.
func TestCreateBatch_RecalculatesNoteCount(t *testing.T) {
	s, _ := newGitStore(t)
	task := sampleTask("01BATCHN", "Task with notes")
	task.Body = "intro\n--- 17-04-2026 10:00:00 ---\nfirst note\n" +
		"--- 17-04-2026 11:00:00 ---\nsecond note"
	task.NoteCount = 99 // stale on purpose
	if err := s.CreateBatch([]model.Task{task}, "slack: ingest 1 items"); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	got, err := s.Get("01BATCHN")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.NoteCount != 2 {
		t.Errorf("NoteCount: got %d, want 2", got.NoteCount)
	}
}

// TestCreateBatch_RollsBackOnDuplicateID verifies that a pre-check for
// duplicate IDs prevents any file from being written if one of the inputs
// collides with an existing task.
func TestCreateBatch_RollsBackOnDuplicateID(t *testing.T) {
	s, repoPath := newGitStore(t)

	// Seed an existing task (and commit so HEAD moves past init).
	existing := sampleTask("01DUP", "already exists")
	if err := s.Create(existing); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if err := git.AutoCommit(repoPath, "seed", filepath.Join(".monolog", "tasks", "01DUP.json")); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	headBefore := headSubject(t, repoPath)

	// Batch has a NEW task first and then a DUPLICATE. Expect the batch to
	// return an error and not create the first task either (pre-check).
	tasks := []model.Task{
		sampleTask("01NEW", "new task"),
		sampleTask("01DUP", "dup task"),
	}
	err := s.CreateBatch(tasks, "slack: ingest 2 items")
	if err == nil {
		t.Fatal("expected error on duplicate ID, got nil")
	}

	// The new task should NOT exist on disk.
	if _, err := os.Stat(s.taskPath("01NEW")); err == nil {
		t.Errorf("01NEW.json should not exist after failed batch")
	}

	// HEAD unchanged.
	if got := headSubject(t, repoPath); got != headBefore {
		t.Errorf("HEAD moved after failed batch: before=%q after=%q", headBefore, got)
	}
}

// TestCreateBatch_RollsBackOnCommitFailure verifies the post-write rollback
// path: when git.AutoCommit fails (here: the store is not inside a git repo
// at all), any files written in the batch are removed so the working tree
// is not left dirty.
func TestCreateBatch_RollsBackOnCommitFailure(t *testing.T) {
	dir := t.TempDir() // NOT a git repo
	tasksDir := filepath.Join(dir, ".monolog", "tasks")
	s, err := New(tasksDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tasks := []model.Task{
		sampleTask("01FAIL1", "first"),
		sampleTask("01FAIL2", "second"),
	}
	err = s.CreateBatch(tasks, "slack: ingest 2 items")
	if err == nil {
		t.Fatal("expected error on commit failure, got nil")
	}

	// Both files rolled back.
	for _, id := range []string{"01FAIL1", "01FAIL2"} {
		if _, statErr := os.Stat(s.taskPath(id)); statErr == nil {
			t.Errorf("%s.json should be rolled back after commit failure", id)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("unexpected stat error for %s: %v", id, statErr)
		}
	}
}

// TestCreateBatch_RollbackUnstagesOnCommitFailure verifies that when
// AutoCommit's `git add` succeeds but the subsequent `git commit` fails
// (simulated via a failing pre-commit hook), the rollback path unstages the
// files from the index before removing them from disk. Without the unstage
// step, a subsequent `monolog sync` would run `git add -A` + commit and
// sweep the stale staged entries into an unrelated sync commit.
func TestCreateBatch_RollbackUnstagesOnCommitFailure(t *testing.T) {
	s, repoPath := newGitStore(t)

	// Install a pre-commit hook that always fails. `git add` still succeeds,
	// so files end up staged, but `git commit` aborts.
	hooksDir := filepath.Join(repoPath, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	hookPath := filepath.Join(hooksDir, "pre-commit")
	hookBody := "#!/bin/sh\necho 'reject' >&2\nexit 1\n"
	if err := os.WriteFile(hookPath, []byte(hookBody), 0o755); err != nil {
		t.Fatalf("write pre-commit hook: %v", err)
	}

	tasks := []model.Task{
		sampleTask("01HOOK1", "first hooked"),
		sampleTask("01HOOK2", "second hooked"),
	}
	err := s.CreateBatch(tasks, "slack: ingest 2 items")
	if err == nil {
		t.Fatal("expected error when pre-commit hook rejects, got nil")
	}

	// Files must be removed from disk (existing rollback behavior).
	for _, id := range []string{"01HOOK1", "01HOOK2"} {
		if _, statErr := os.Stat(s.taskPath(id)); statErr == nil {
			t.Errorf("%s.json should be removed after failed commit", id)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("unexpected stat error for %s: %v", id, statErr)
		}
	}

	// The key assertion: the index must not have stale entries for the
	// rolled-back files. `git status --porcelain` should not mention them.
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status --porcelain: %v", err)
	}
	status := string(out)
	for _, id := range []string{"01HOOK1", "01HOOK2"} {
		if strings.Contains(status, id+".json") {
			t.Errorf("git status still references %s.json after rollback:\n%s", id, status)
		}
	}
}

// createTestTasks populates the store with a known set of tasks for filter tests.
// Tasks:
//   - "Today work"     schedule=today, status=open, tags=[work], position=1000
//   - "Today personal" schedule=today, status=open, tags=[personal], position=2000
//   - "Tomorrow work"  schedule=tomorrow, status=open, tags=[work], position=3000
//   - "Done task"       schedule=today, status=done, tags=[work], position=4000
func createTestTasks(t *testing.T, s *Store) {
	t.Helper()
	tasks := []model.Task{
		{ID: "01AAA", Title: "Today work", Source: "manual", Status: "open", Position: 1000, Schedule: "today", CreatedAt: "2026-04-11T10:00:00Z", UpdatedAt: "2026-04-11T10:00:00Z", Tags: []string{"work"}},
		{ID: "01BBB", Title: "Today personal", Source: "manual", Status: "open", Position: 2000, Schedule: "today", CreatedAt: "2026-04-11T10:00:00Z", UpdatedAt: "2026-04-11T10:00:00Z", Tags: []string{"personal"}},
		{ID: "01CCC", Title: "Tomorrow work", Source: "manual", Status: "open", Position: 3000, Schedule: "tomorrow", CreatedAt: "2026-04-11T10:00:00Z", UpdatedAt: "2026-04-11T10:00:00Z", Tags: []string{"work"}},
		{ID: "01DDD", Title: "Done task", Source: "manual", Status: "done", Position: 4000, Schedule: "today", CreatedAt: "2026-04-11T10:00:00Z", UpdatedAt: "2026-04-11T10:00:00Z", Tags: []string{"work"}},
	}
	for _, task := range tasks {
		if err := s.Create(task); err != nil {
			t.Fatalf("Create %q failed: %v", task.Title, err)
		}
	}
}
