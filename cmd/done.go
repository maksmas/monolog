package cmd

import (
	"fmt"
	"time"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/recurrence"
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

			commitMsg, commitFiles, err := recurrence.CompleteAndSpawn(s, &task, time.Now(), cmd.ErrOrStderr(), config.DateFormat())
			if err != nil {
				return err
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
