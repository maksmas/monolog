package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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

func TestListFilterBySchedule(t *testing.T) {
	s := newTestStore(t)
	createTestTasks(t, s)

	tasks, err := s.List(ListOptions{Schedule: "today"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 today tasks, got %d", len(tasks))
	}
	for _, task := range tasks {
		if task.Schedule != "today" {
			t.Errorf("expected schedule=today, got %q", task.Schedule)
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

	tasks, err := s.List(ListOptions{Schedule: "today", Status: "open", Tag: "work"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task matching all filters, got %d", len(tasks))
	}
	if tasks[0].Title != "Today work" {
		t.Errorf("expected 'Today work', got %q", tasks[0].Title)
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
