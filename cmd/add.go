package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var schedule string
	var tags string

	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new task to the backlog",
		Long:  "Creates a new task with the given title. Defaults to schedule=today, appended at the bottom of the list.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := args[0]
			repoPath := monologDir()
			tasksDir := filepath.Join(repoPath, ".monolog", "tasks")

			s, err := store.New(tasksDir)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}

			// Get existing tasks to compute next position
			existing, err := s.List(store.ListOptions{})
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}

			now := time.Now().UTC().Format(time.RFC3339)
			task := model.Task{
				ID:        model.NewID(),
				Title:     title,
				Source:    "manual",
				Status:    "open",
				Position:  ordering.NextPosition(existing),
				Schedule:  schedule,
				CreatedAt: now,
				UpdatedAt: now,
			}

			// Parse tags
			if tags != "" {
				task.Tags = strings.Split(tags, ",")
			}

			if err := s.Create(task); err != nil {
				return fmt.Errorf("create task: %w", err)
			}

			// Auto-commit
			taskFile := filepath.Join(".monolog", "tasks", task.ID+".json")
			if err := git.AutoCommit(repoPath, fmt.Sprintf("add: %s", title), taskFile); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added: %s [%s]\n", title, task.ID[:8])
			return nil
		},
	}

	cmd.Flags().StringVarP(&schedule, "schedule", "s", "today", "Schedule: today, tomorrow, week, someday, or ISO date")
	cmd.Flags().StringVarP(&tags, "tags", "t", "", "Comma-separated tags")

	return cmd
}
