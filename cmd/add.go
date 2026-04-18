package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/recurrence"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var scheduleArg string
	var tags string
	var recur string

	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new task to the backlog",
		Long:  "Creates a new task with the given title. Defaults to schedule=today, appended at the bottom of the list.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := args[0]

			now := time.Now()
			scheduleDate, err := schedule.Parse(scheduleArg, now, config.DateFormat())
			if err != nil {
				if errors.Is(err, schedule.ErrInvalid) {
					return fmt.Errorf("invalid schedule %q: must be today, tomorrow, week, month, someday, or %s", scheduleArg, config.DateFormatLabel())
				}
				return err
			}

			// Validate recurrence rule (if provided) and normalize to canonical
			// grammar form, so aliases like "weekly:Monday" or "weekly:1" store
			// as "weekly:mon".
			recurCanonical, err := recurrence.Canonicalize(recur)
			if err != nil {
				return err
			}

			s, repoPath, err := openStore()
			if err != nil {
				return err
			}

			// Get existing tasks to compute next position
			existing, err := s.List(store.ListOptions{})
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}

			id, err := model.NewID()
			if err != nil {
				return fmt.Errorf("generate ID: %w", err)
			}

			nowStr := now.UTC().Format(time.RFC3339)
			task := model.Task{
				ID:         id,
				Title:      title,
				Source:     "manual",
				Status:     "open",
				Position:   ordering.NextPosition(existing),
				Schedule:   scheduleDate,
				Recurrence: recurCanonical,
				CreatedAt:  nowStr,
				UpdatedAt:  nowStr,
			}

			// Parse and sanitize tags, then auto-tag from title prefix
			task.Tags = model.SanitizeTags(tags)
			task.Tags = model.AutoTag(title, model.CollectTags(existing), task.Tags)

			if err := s.Create(task); err != nil {
				return fmt.Errorf("create task: %w", err)
			}

			// Auto-commit
			taskFile := filepath.Join(".monolog", "tasks", task.ID+".json")
			if err := git.AutoCommit(repoPath, fmt.Sprintf("add: %s", title), taskFile); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added: %s [%s]\n", title, display.ShortID(task.ID))
			return nil
		},
	}

	cmd.Flags().StringVarP(&scheduleArg, "schedule", "s", "today", fmt.Sprintf("Schedule: today, tomorrow, week, month, someday, or %s", config.DateFormatLabel()))
	cmd.Flags().StringVarP(&tags, "tags", "t", "", "Comma-separated tags")
	cmd.Flags().StringVar(&recur, "recur", "", "Recurrence rule: "+recurrence.GrammarHint+" (e.g. monthly:1, weekly:mon, workdays, days:7)")

	return cmd
}
