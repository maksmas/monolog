package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var scheduleArg string
	var tags string

	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new task to the backlog",
		Long:  "Creates a new task with the given title. Defaults to schedule=today, appended at the bottom of the list.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := args[0]

			now := time.Now()
			scheduleDate, err := schedule.Parse(scheduleArg, now)
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
				ID:        id,
				Title:     title,
				Source:    "manual",
				Status:    "open",
				Position:  ordering.NextPosition(existing),
				Schedule:  scheduleDate,
				CreatedAt: nowStr,
				UpdatedAt: nowStr,
			}

			// Parse and sanitize tags
			task.Tags = sanitizeTags(tags)

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

	cmd.Flags().StringVarP(&scheduleArg, "schedule", "s", "today", "Schedule: today, tomorrow, week, month, someday, or ISO date")
	cmd.Flags().StringVarP(&tags, "tags", "t", "", "Comma-separated tags")

	return cmd
}
