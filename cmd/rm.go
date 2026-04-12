package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/spf13/cobra"
)

func newRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <id-prefix>",
		Short: "Delete a task",
		Long:  "Resolves the task by ID prefix and deletes its file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := args[0]

			s, repoPath, err := openStore()
			if err != nil {
				return err
			}

			task, err := s.GetByPrefix(prefix)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			title := task.Title

			if err := s.Delete(task.ID); err != nil {
				return fmt.Errorf("delete task: %w", err)
			}

			taskFile := filepath.Join(".monolog", "tasks", task.ID+".json")
			if err := git.AutoCommit(repoPath, fmt.Sprintf("rm: %s", title), taskFile); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Removed: %s [%s]\n", title, display.ShortID(task.ID))
			return nil
		},
	}

	return cmd
}
