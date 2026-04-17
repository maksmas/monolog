package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/recurrence"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/spf13/cobra"
)

func newEditCmd() *cobra.Command {
	var (
		title       string
		body        string
		scheduleArg string
		tags        string
		active      bool
		recur       string
	)

	cmd := &cobra.Command{
		Use:   "edit <id-prefix>",
		Short: "Edit a task",
		Long:  "Resolves the task by ID prefix and updates fields via inline flags.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := args[0]

			// Require at least one edit flag
			if !cmd.Flags().Changed("title") && !cmd.Flags().Changed("body") && !cmd.Flags().Changed("schedule") && !cmd.Flags().Changed("tags") && !cmd.Flags().Changed("active") && !cmd.Flags().Changed("recur") {
				return fmt.Errorf("at least one of --title, --body, --schedule, --tags, --active, or --recur is required")
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

			// Validate recurrence rule if provided; normalize to canonical form.
			// An explicitly-set empty string clears the rule.
			var newRecur string
			if cmd.Flags().Changed("recur") {
				canonical, err := recurrence.Canonicalize(recur)
				if err != nil {
					return err
				}
				newRecur = canonical
			}

			s, repoPath, err := openStore()
			if err != nil {
				return err
			}

			task, err := s.Resolve(prefix)
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
				wasActive := task.IsActive()
				task.Tags = model.SanitizeTags(tags)
				task.SetActive(wasActive)
			}
			if cmd.Flags().Changed("active") {
				task.SetActive(active)
			}
			if cmd.Flags().Changed("recur") {
				task.Recurrence = newRecur
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
	cmd.Flags().StringVar(&scheduleArg, "schedule", "", "New schedule (today, tomorrow, week, month, someday, or ISO date)")
	cmd.Flags().StringVar(&tags, "tags", "", "New comma-separated tags")
	cmd.Flags().BoolVar(&active, "active", false, "Mark as active (use --active=false to deactivate)")
	cmd.Flags().StringVar(&recur, "recur", "", "New recurrence rule: monthly:N, weekly:<day>, workdays, days:N (pass \"\" to clear)")

	return cmd
}
