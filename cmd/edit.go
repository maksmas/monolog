package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newEditCmd() *cobra.Command {
	var (
		title    string
		schedule string
		tags     string
	)

	cmd := &cobra.Command{
		Use:   "edit <id-prefix>",
		Short: "Edit a task",
		Long:  "Resolves the task by ID prefix and updates fields via inline flags.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := args[0]
			repoPath := monologDir()
			tasksDir := filepath.Join(repoPath, ".monolog", "tasks")

			// Require at least one edit flag
			if !cmd.Flags().Changed("title") && !cmd.Flags().Changed("schedule") && !cmd.Flags().Changed("tags") {
				return fmt.Errorf("at least one of --title, --schedule, or --tags is required")
			}

			s, err := store.New(tasksDir)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}

			task, err := s.GetByPrefix(prefix)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			if cmd.Flags().Changed("title") {
				task.Title = title
			}
			if cmd.Flags().Changed("schedule") {
				task.Schedule = schedule
			}
			if cmd.Flags().Changed("tags") {
				if tags == "" {
					task.Tags = nil
				} else {
					task.Tags = strings.Split(tags, ",")
				}
			}

			task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

			if err := s.Update(task); err != nil {
				return fmt.Errorf("update task: %w", err)
			}

			taskFile := filepath.Join(".monolog", "tasks", task.ID+".json")
			if err := git.AutoCommit(repoPath, fmt.Sprintf("edit: %s", task.Title), taskFile); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Edited: %s [%s]\n", task.Title, task.ID[:8])
			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVar(&schedule, "schedule", "", "New schedule (today, tomorrow, week, someday, or ISO date)")
	cmd.Flags().StringVar(&tags, "tags", "", "New comma-separated tags")

	return cmd
}
