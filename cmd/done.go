package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/git"
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
			repoPath := monologDir()
			tasksDir := filepath.Join(repoPath, ".monolog", "tasks")

			s, err := store.New(tasksDir)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}

			task, err := s.GetByPrefix(prefix)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			task.Status = "done"
			task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

			if err := s.Update(task); err != nil {
				return fmt.Errorf("update task: %w", err)
			}

			taskFile := filepath.Join(".monolog", "tasks", task.ID+".json")
			if err := git.AutoCommit(repoPath, fmt.Sprintf("done: %s", task.Title), taskFile); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Done: %s [%s]\n", task.Title, task.ID[:8])
			return nil
		},
	}

	return cmd
}
