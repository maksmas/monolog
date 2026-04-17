package recurrence

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
)

// spawnResult describes the outcome of a recurring-task spawn.
type spawnResult struct {
	// newID is the ULID of the newly created spawn task.
	newID string
	// nextDate is the ISO-formatted next-occurrence date (e.g. "2026-05-01").
	nextDate string
}

// spawn creates the next-occurrence task for a completed recurring old task.
// It does NOT update the old task — the caller composes the cross-reference
// note from the returned newID and nextDate and persists the old task via a
// single Update that combines the done transition with the back-reference.
//
// On Create failure, the new task is not written and the returned error
// should be warned-and-skipped so the underlying completion still goes
// through.
func spawn(s *store.Store, old model.Task, rule Rule, now time.Time) (spawnResult, error) {
	newID, err := model.NewID()
	if err != nil {
		return spawnResult{}, fmt.Errorf("generate ID: %w", err)
	}

	existing, err := s.List(store.ListOptions{})
	if err != nil {
		return spawnResult{}, fmt.Errorf("list tasks: %w", err)
	}

	nowStr := now.UTC().Format(time.RFC3339)
	nextDate := rule.Next(now).Format(schedule.IsoLayout)

	newTask := model.Task{
		ID:         newID,
		Title:      old.Title,
		Body:       old.Body,
		Source:     old.Source,
		Status:     "open",
		Position:   ordering.NextPosition(existing),
		Schedule:   nextDate,
		Recurrence: old.Recurrence,
		Tags:       tagsWithoutActive(old.Tags),
		CreatedAt:  nowStr,
		UpdatedAt:  nowStr,
	}
	newTask.Body = model.AppendNote(newTask.Body, fmt.Sprintf("Spawned from %s", old.ID), now)

	if err := s.Create(newTask); err != nil {
		return spawnResult{}, fmt.Errorf("create spawned task: %w", err)
	}

	return spawnResult{newID: newID, nextDate: nextDate}, nil
}

// CompleteAndSpawn transitions a task to done and, when the task has a
// parseable recurrence rule, spawns the next occurrence with bidirectional
// cross-reference notes. It returns the git commit message and the list of
// relative task file paths (under .monolog/tasks/) to include in a single
// commit — old task alone for non-recurring, or old and new for recurring.
//
// Any recurrence-spawn failure (invalid rule, list error, create error) is
// downgraded to a warning on warn — the task's own completion is never
// blocked by spawn trouble, and the repo is never left in a partially-
// committed state. The function performs exactly one Store.Update on the
// old task: the done-state transition plus any back-reference note are
// carried in a single write.
//
// If the old-task Update fails after a successful spawn Create, the newly
// written spawn file is removed (best-effort) so the repo is not left with
// an orphaned uncommitted spawn.
//
// Callers pass the task by pointer because the function mutates it (status,
// timestamps, body) so the post-call view reflects persisted state.
func CompleteAndSpawn(s *store.Store, task *model.Task, now time.Time, warn io.Writer) (commitMsg string, commitFiles []string, err error) {
	nowStr := now.UTC().Format(time.RFC3339)
	task.Status = "done"
	task.SetActive(false)
	task.UpdatedAt = nowStr
	if task.CompletedAt == "" {
		task.CompletedAt = nowStr
	}

	oldFile := filepath.Join(".monolog", "tasks", task.ID+".json")
	commitMsg = fmt.Sprintf("done: %s", task.Title)
	commitFiles = []string{oldFile}

	// Try to spawn the next occurrence. Any failure here is warned-and-
	// skipped: we never want a broken spawn to eat the user's completion,
	// and we never want to leave the repo with the spawn partially written
	// but not committed.
	var spawnedID string
	if task.Recurrence != "" {
		rule, parseErr := Parse(task.Recurrence)
		if parseErr != nil {
			fmt.Fprintf(warn, "warning: recurrence %q invalid: %v; skipping spawn\n", task.Recurrence, parseErr)
		} else {
			// Parse with non-empty input returns (rule, nil) on success — rule
			// is never nil here; the empty-input path cannot reach this block.
			res, spawnErr := spawn(s, *task, rule, now)
			if spawnErr != nil {
				fmt.Fprintf(warn, "warning: recurrence %q spawn failed: %v; skipping spawn\n", task.Recurrence, spawnErr)
			} else {
				backRef := fmt.Sprintf("Spawned follow-up: %s (scheduled %s)", res.newID, res.nextDate)
				task.Body = model.AppendNote(task.Body, backRef, now)
				commitMsg = fmt.Sprintf("done: %s (recurring, next %s)", task.Title, res.nextDate)
				commitFiles = append(commitFiles, filepath.Join(".monolog", "tasks", res.newID+".json"))
				spawnedID = res.newID
			}
		}
	}

	if err := s.Update(*task); err != nil {
		// Roll back the spawn file so we don't leave an orphan that never
		// gets committed to git.
		if spawnedID != "" {
			_ = s.Delete(spawnedID)
		}
		return "", nil, fmt.Errorf("update task: %w", err)
	}

	return commitMsg, commitFiles, nil
}

// tagsWithoutActive returns a copy of tags with the reserved ActiveTag
// removed. Returns nil when the input is nil/empty or contains only the
// active tag. Always returns a fresh slice so the caller may mutate it
// without affecting the source.
func tagsWithoutActive(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t != model.ActiveTag {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
