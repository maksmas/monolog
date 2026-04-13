package cmd

import (
	"fmt"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newLsCmd() *cobra.Command {
	var (
		all      bool
		schedule string
		tag      string
		done     bool
	)

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List tasks",
		Long:  "Lists tasks from the backlog. Default: shows today's open tasks sorted by position.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := openStore()
			if err != nil {
				return err
			}

			opts := store.ListOptions{}

			if done {
				opts.Status = "done"
			} else {
				opts.Status = "open"
			}

			// --schedule flag takes precedence; if not set and not --all and not --done, default to today.
			// When --done is used, show all done tasks across all schedules unless --schedule is explicit.
			if schedule != "" {
				if err := validateSchedule(schedule); err != nil {
					return err
				}
				opts.Schedule = schedule
			} else if !all && !done {
				opts.Schedule = "today"
			}

			if tag != "" {
				opts.Tag = tag
			}

			tasks, err := s.List(opts)
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}

			display.FormatTasks(cmd.OutOrStdout(), tasks, time.Now())
			return nil
		},
	}

	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show all open tasks across all schedules")
	cmd.Flags().StringVarP(&schedule, "schedule", "s", "", "Filter by schedule (today, tomorrow, week, someday, or ISO date)")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Filter by tag")
	cmd.Flags().BoolVarP(&done, "done", "d", false, "Show completed tasks")

	return cmd
}
