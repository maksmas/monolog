package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
)

var (
	ErrNotFound  = errors.New("task not found")
	ErrAmbiguous = errors.New("ambiguous prefix")
)

// ListOptions controls filtering when listing tasks. Schedule/bucket
// filtering lives in the cmd and TUI layers (it depends on time.Now and on
// virtual-bucket semantics) — the store stays IO-only.
type ListOptions struct {
	Status string // filter by status ("open", "done")
	Tag    string // filter by tag
}

// Store manages task JSON files in a directory.
type Store struct {
	dir string // path to the tasks directory
}

// New creates a Store rooted at the given tasks directory.
// It ensures the directory exists.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create tasks dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// taskPath returns the file path for a task ID.
func (s *Store) taskPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// Create writes a new task to disk. The task's ID must already be set.
// NoteCount is recalculated from Body so callers never need to set it.
func (s *Store) Create(task model.Task) error {
	path := s.taskPath(task.ID)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("task %s already exists", task.ID)
	}
	task.NoteCount = model.CountNotes(task.Body, config.DateRegex())
	return s.writeTask(path, task)
}

// CreateBatch writes N task files and commits all of them in a single git
// commit with the given message. On empty input it is a no-op and returns nil.
//
// Atomicity: if any individual write fails, or if git.AutoCommit fails after
// all writes succeeded, any files that were written during this batch are
// removed (best-effort rollback, rollback errors are swallowed) before the
// original error is returned. The goal is that a failed batch leaves no
// .json files in the working tree that `git status` would pick up.
//
// NoteCount is recalculated from each task's Body so callers never need to
// set it — identical behavior to Create.
//
// The repo path for the commit is derived from the tasks dir
// (repoPath = parent of parent of s.dir), matching the convention that
// Store is rooted at <repoPath>/.monolog/tasks.
func (s *Store) CreateBatch(tasks []model.Task, commitMessage string) error {
	if len(tasks) == 0 {
		return nil
	}

	// Pre-check duplicate IDs before writing anything.
	for _, t := range tasks {
		if _, err := os.Stat(s.taskPath(t.ID)); err == nil {
			return fmt.Errorf("task %s already exists", t.ID)
		}
	}

	written := make([]string, 0, len(tasks))
	relFiles := make([]string, 0, len(tasks))
	dateRegex := config.DateRegex()
	for _, t := range tasks {
		t.NoteCount = model.CountNotes(t.Body, dateRegex)
		path := s.taskPath(t.ID)
		if err := s.writeTask(path, t); err != nil {
			cleanupFiles(written)
			return err
		}
		written = append(written, path)
		relFiles = append(relFiles, filepath.Join(".monolog", "tasks", t.ID+".json"))
	}

	repoPath := filepath.Dir(filepath.Dir(s.dir))
	if err := git.AutoCommit(repoPath, commitMessage, relFiles...); err != nil {
		cleanupFiles(written)
		return err
	}
	return nil
}

// cleanupFiles removes a slice of file paths, swallowing errors. Used by
// CreateBatch for best-effort rollback on mid-batch failure.
func cleanupFiles(paths []string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// Get reads a task by its full ID.
func (s *Store) Get(id string) (model.Task, error) {
	return s.readTask(s.taskPath(id))
}

// Update overwrites an existing task file. It recalculates NoteCount from
// the task's Body so that callers cannot accidentally leave the field stale
// after mutating Body through any code path.
func (s *Store) Update(task model.Task) error {
	path := s.taskPath(task.ID)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	task.NoteCount = model.CountNotes(task.Body, config.DateRegex())
	return s.writeTask(path, task)
}

// Delete removes a task file by its full ID.
func (s *Store) Delete(id string) error {
	path := s.taskPath(id)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return os.Remove(path)
}

// GetByPrefix resolves a partial ID prefix to a single task.
// Returns ErrNotFound if no match, ErrAmbiguous if multiple matches.
func (s *Store) GetByPrefix(prefix string) (model.Task, error) {
	if prefix == "" {
		return model.Task{}, fmt.Errorf("empty prefix: %w", ErrNotFound)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return model.Task{}, fmt.Errorf("read tasks dir: %w", err)
	}

	var matches []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}

	switch len(matches) {
	case 0:
		return model.Task{}, ErrNotFound
	case 1:
		return s.Get(matches[0])
	default:
		return model.Task{}, fmt.Errorf("%w: prefix %q matches %d tasks (%s)",
			ErrAmbiguous, prefix, len(matches), strings.Join(matches, ", "))
	}
}

// Resolve finds a task by ULID prefix first, then falls back to title-initials
// matching when the input is at least 2 characters and the prefix search found
// nothing. Initials matching is case-insensitive and supports prefix matching
// (e.g. "fl" matches a task whose full initials are "flb").
func (s *Store) Resolve(input string) (model.Task, error) {
	task, err := s.GetByPrefix(input)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return task, err
	}

	if len(input) < 2 {
		return model.Task{}, ErrNotFound
	}

	tasks, err := s.List(ListOptions{Status: "open"})
	if err != nil {
		return model.Task{}, fmt.Errorf("list tasks for initials lookup: %w", err)
	}

	lower := strings.ToLower(input)
	var matches []model.Task
	for _, t := range tasks {
		initials := model.Initials(t.Title)
		if strings.HasPrefix(initials, lower) {
			matches = append(matches, t)
		}
	}

	switch len(matches) {
	case 0:
		return model.Task{}, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		titles := make([]string, len(matches))
		for i, m := range matches {
			titles[i] = fmt.Sprintf("%q", m.Title)
		}
		return model.Task{}, fmt.Errorf("%w: initials %q matches %d tasks (%s)",
			ErrAmbiguous, input, len(matches), strings.Join(titles, ", "))
	}
}

// List reads all tasks and applies optional filters, returning results sorted by position.
func (s *Store) List(opts ListOptions) ([]model.Task, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}

	var tasks []model.Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		task, err := s.readTask(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if !matchesFilters(task, opts) {
			continue
		}
		tasks = append(tasks, task)
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Position < tasks[j].Position
	})

	return tasks, nil
}

// Tags returns a sorted, deduplicated list of all tags across all stored tasks.
func (s *Store) Tags() ([]string, error) {
	tasks, err := s.List(ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list tasks for tags: %w", err)
	}
	return model.CollectTags(tasks), nil
}

// matchesFilters returns true if the task matches all specified filter criteria.
func matchesFilters(task model.Task, opts ListOptions) bool {
	if opts.Status != "" && task.Status != opts.Status {
		return false
	}
	if opts.Tag != "" {
		found := false
		for _, t := range task.Tags {
			if t == opts.Tag {
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

// writeTask marshals and writes a task to the given path.
func (s *Store) writeTask(path string, task model.Task) error {
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write task file: %w", err)
	}
	return nil
}

// readTask reads and unmarshals a task from the given path.
func (s *Store) readTask(path string) (model.Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.Task{}, ErrNotFound
		}
		return model.Task{}, fmt.Errorf("read task file: %w", err)
	}
	var task model.Task
	if err := json.Unmarshal(data, &task); err != nil {
		return model.Task{}, fmt.Errorf("unmarshal task: %w", err)
	}
	return task, nil
}
