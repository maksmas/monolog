package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/recurrence"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newDoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done <id-prefix>",
		Short: "Mark a task as done",
		Long:  "Resolves the task by ID prefix and sets its status to done.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := args[0]

			s, repoPath, err := openStore()
			if err != nil {
				return err
			}

			task, err := s.Resolve(prefix)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			if task.Status == "done" {
				fmt.Fprintf(cmd.OutOrStdout(), "Already done: %s [%s]\n", task.Title, display.ShortID(task.ID))
				return nil
			}

			now := time.Now()
			nowStr := now.UTC().Format(time.RFC3339)

			task.Status = "done"
			task.SetActive(false)
			task.UpdatedAt = nowStr
			if task.CompletedAt == "" {
				task.CompletedAt = nowStr
			}

			if err := s.Update(task); err != nil {
				return fmt.Errorf("update task: %w", err)
			}

			taskFile := filepath.Join(".monolog", "tasks", task.ID+".json")
			commitMsg := fmt.Sprintf("done: %s", task.Title)
			commitFiles := []string{taskFile}

			// Spawn the next occurrence if the task has a recurrence rule.
			if task.Recurrence != "" {
				rule, parseErr := recurrence.Parse(task.Recurrence)
				if parseErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: recurrence %q invalid: %v; skipping spawn\n", task.Recurrence, parseErr)
				} else if rule != nil {
					newID, spawnErr := spawnRecurring(s, task, rule, now)
					if spawnErr != nil {
						return fmt.Errorf("spawn recurring task: %w", spawnErr)
					}
					nextDate := rule.Next(now).Format(schedule.IsoLayout)
					commitMsg = fmt.Sprintf("done: %s (recurring, next %s)", task.Title, nextDate)
					newFile := filepath.Join(".monolog", "tasks", newID+".json")
					commitFiles = append(commitFiles, newFile)
				}
			}

			if err := git.AutoCommit(repoPath, commitMsg, commitFiles...); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Done: %s [%s]\n", task.Title, display.ShortID(task.ID))
			return nil
		},
	}

	return cmd
}

// spawnRecurring creates a new task as the next occurrence of a recurring
// completed task and appends cross-reference notes on both tasks. The old
// task argument must already be persisted to disk with its done state. The
// function returns the new task's ULID.
func spawnRecurring(s *store.Store, old model.Task, rule recurrence.Rule, now time.Time) (string, error) {
	newID, err := model.NewID()
	if err != nil {
		return "", fmt.Errorf("generate ID: %w", err)
	}

	existing, err := s.List(store.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list tasks: %w", err)
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
	// Store.Create doesn't recalculate NoteCount (only Update does), so set it
	// explicitly here so the badge renders correctly on the fresh task.
	newTask.NoteCount = model.CountNotes(newTask.Body)

	if err := s.Create(newTask); err != nil {
		return "", fmt.Errorf("create spawned task: %w", err)
	}

	// Append back-reference note to the old task.
	old.Body = model.AppendNote(old.Body, fmt.Sprintf("Spawned follow-up: %s (scheduled %s)", newID, nextDate), now)
	old.UpdatedAt = nowStr
	if err := s.Update(old); err != nil {
		return "", fmt.Errorf("update old task with back-reference: %w", err)
	}

	return newID, nil
}

// tagsWithoutActive returns a copy of tags with the reserved ActiveTag removed.
// Returns nil when the input is nil or empty or contains only ActiveTag.
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
