package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/spf13/cobra"
)

func newEditCmd() *cobra.Command {
	var (
		title       string
		body        string
		scheduleArg string
		tags        string
	)

	cmd := &cobra.Command{
		Use:   "edit <id-prefix>",
		Short: "Edit a task",
		Long:  "Resolves the task by ID prefix and updates fields via inline flags.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := args[0]

			// Require at least one edit flag
			if !cmd.Flags().Changed("title") && !cmd.Flags().Changed("body") && !cmd.Flags().Changed("schedule") && !cmd.Flags().Changed("tags") {
				return fmt.Errorf("at least one of --title, --body, --schedule, or --tags is required")
			}

			now := time.Now()
			var newSchedule string
			if cmd.Flags().Changed("schedule") {
				ns, err := schedule.Parse(scheduleArg, now)
				if err != nil {
					return err
				}
				newSchedule = ns
			}

			s, repoPath, err := openStore()
			if err != nil {
				return err
			}

			task, err := s.GetByPrefix(prefix)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			if cmd.Flags().Changed("title") {
				task.Title = title
			}
			if cmd.Flags().Changed("body") {
				task.Body = body
			}
			if cmd.Flags().Changed("schedule") {
				task.Schedule = newSchedule
			} else {
				// Lazy-migrate any legacy bucket string to ISO so subsequent
				// reads see a normalized value.
				task.Schedule = schedule.Normalize(task.Schedule, now)
			}
			if cmd.Flags().Changed("tags") {
				task.Tags = sanitizeTags(tags)
			}

			task.UpdatedAt = now.UTC().Format(time.RFC3339)

			if err := s.Update(task); err != nil {
				return fmt.Errorf("update task: %w", err)
			}

			taskFile := filepath.Join(".monolog", "tasks", task.ID+".json")
			if err := git.AutoCommit(repoPath, fmt.Sprintf("edit: %s", task.Title), taskFile); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Edited: %s [%s]\n", task.Title, display.ShortID(task.ID))
			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVar(&body, "body", "", "New body text")
	cmd.Flags().StringVar(&scheduleArg, "schedule", "", "New schedule (today, tomorrow, week, someday, or ISO date)")
	cmd.Flags().StringVar(&tags, "tags", "", "New comma-separated tags")

	return cmd
}
