package cmd

import (
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

// isoDateRegexp matches ISO date format YYYY-MM-DD.
var isoDateRegexp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func newBumpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bump",
		Short: "Promote tasks scheduled for tomorrow or past dates to today",
		Long:  "Promotes tasks with schedule 'tomorrow' to 'today', and tasks with past ISO dates to 'today'. Tasks with 'today', 'week', 'someday', or future ISO dates are left unchanged.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := monologDir()
			tasksDir := filepath.Join(repoPath, ".monolog", "tasks")

			s, err := store.New(tasksDir)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}

			// List all open tasks
			tasks, err := s.List(store.ListOptions{Status: "open"})
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}

			today := time.Now().Format("2006-01-02")
			var promoted int
			var changedFiles []string

			for _, task := range tasks {
				shouldBump := false

				if task.Schedule == "tomorrow" {
					shouldBump = true
				} else if isoDateRegexp.MatchString(task.Schedule) && task.Schedule < today {
					shouldBump = true
				}

				if shouldBump {
					task.Schedule = "today"
					task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
					if err := s.Update(task); err != nil {
						return fmt.Errorf("update task %s: %w", task.ID, err)
					}
					changedFiles = append(changedFiles, filepath.Join(".monolog", "tasks", task.ID+".json"))
					promoted++
				}
			}

			if promoted == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to bump")
				return nil
			}

			// Auto-commit all changes in one commit
			msg := fmt.Sprintf("bump: promote %d tasks to today", promoted)
			if err := git.AutoCommit(repoPath, msg, changedFiles...); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Bumped %d tasks to today\n", promoted)
			return nil
		},
	}

	return cmd
}
