package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <identifier>",
		Short: "Show full task detail",
		Long:  "Resolves the task by ID prefix or title initials and prints its full detail to stdout.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			identifier := args[0]

			s, _, err := openStore()
			if err != nil {
				return err
			}

			task, err := s.Resolve(identifier)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			w := cmd.OutOrStdout()
			now := time.Now()

			// Title
			fmt.Fprintf(w, "Title:     %s\n", task.Title)

			// ID
			fmt.Fprintf(w, "ID:        %s\n", display.ShortID(task.ID))

			// Status
			fmt.Fprintf(w, "Status:    %s\n", task.Status)

			// Schedule (show bucket name for readability)
			bucket := schedule.Bucket(task.Schedule, now)
			fmt.Fprintf(w, "Schedule:  %s (%s)\n", bucket, task.Schedule)

			// Recurrence (only when set)
			if task.Recurrence != "" {
				fmt.Fprintf(w, "Recurrence: %s\n", task.Recurrence)
			}

			// Tags
			if vt := display.VisibleTags(task.Tags); len(vt) > 0 {
				fmt.Fprintf(w, "Tags:      %s\n", strings.Join(vt, ", "))
			}

			// Created
			fmt.Fprintf(w, "Created:   %s\n", task.CreatedAt)

			// Updated
			if task.UpdatedAt != "" {
				fmt.Fprintf(w, "Updated:   %s\n", task.UpdatedAt)
			}

			// Completed
			if task.CompletedAt != "" {
				fmt.Fprintf(w, "Completed: %s\n", task.CompletedAt)
			}

			// Notes count
			if task.NoteCount > 0 {
				fmt.Fprintf(w, "Notes:     %d\n", task.NoteCount)
			}

			// Body
			if task.Body != "" {
				fmt.Fprintf(w, "\n%s\n", task.Body)
			}

			return nil
		},
	}

	return cmd
}
