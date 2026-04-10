package cmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show recently completed tasks",
		Long:  "Lists tasks completed in the last 7 days, sorted by most recently completed first.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := monologDir()
			tasksDir := filepath.Join(repoPath, ".monolog", "tasks")

			s, err := store.New(tasksDir)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}

			// List all done tasks
			tasks, err := s.List(store.ListOptions{Status: "done"})
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}

			// Filter to last 7 days by updated_at
			cutoff := time.Now().AddDate(0, 0, -7)
			var recent []model.Task
			for _, task := range tasks {
				updatedAt, err := time.Parse(time.RFC3339, task.UpdatedAt)
				if err != nil {
					continue // skip tasks with unparseable timestamps
				}
				if updatedAt.After(cutoff) {
					recent = append(recent, task)
				}
			}

			// Sort by updated_at descending (most recently completed first)
			sort.Slice(recent, func(i, j int) bool {
				return recent[i].UpdatedAt > recent[j].UpdatedAt
			})

			display.FormatTasks(cmd.OutOrStdout(), recent)
			return nil
		},
	}

	return cmd
}
