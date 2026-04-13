package cmd

import (
	"fmt"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newLsCmd() *cobra.Command {
	var (
		all          bool
		scheduleFlag string
		tag          string
		done         bool
		active       bool
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
			if tag != "" {
				opts.Tag = tag
			}

			// Resolve the schedule filter. --schedule wins; otherwise default
			// to today (unless --all or --done lifts the filter).
			now := time.Now()
			var (
				bucketFilter string // bucket name, or "" for none
				exactDate    string // ISO date for exact match, or "" for none
			)
			scheduleChanged := cmd.Flags().Changed("schedule")
			switch {
			case scheduleFlag != "":
				if schedule.IsBucket(scheduleFlag) {
					bucketFilter = scheduleFlag
				} else if schedule.IsISODate(scheduleFlag) {
					exactDate = scheduleFlag
				} else {
					return fmt.Errorf("invalid schedule %q: must be today, tomorrow, week, month, someday, or ISO date (YYYY-MM-DD)", scheduleFlag)
				}
			case !all && !done && !(active && !scheduleChanged):
				bucketFilter = schedule.Today
			}

			tasks, err := s.List(opts)
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}

			tasks = filterBySchedule(tasks, bucketFilter, exactDate, now)

			if active {
				tasks = filterActive(tasks)
			}

			display.FormatTasks(cmd.OutOrStdout(), tasks, now)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show all open tasks across all schedules")
	cmd.Flags().StringVarP(&scheduleFlag, "schedule", "s", "", "Filter by schedule (today, tomorrow, week, month, someday, or ISO date)")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Filter by tag")
	cmd.Flags().BoolVarP(&done, "done", "d", false, "Show completed tasks")
	cmd.Flags().BoolVar(&active, "active", false, "Show only active tasks (lifts default today filter unless --schedule is explicit)")

	return cmd
}

// filterActive returns only tasks that are marked active.
func filterActive(tasks []model.Task) []model.Task {
	out := tasks[:0]
	for _, t := range tasks {
		if t.IsActive() {
			out = append(out, t)
		}
	}
	return out
}

// filterBySchedule applies a bucket or exact-date predicate to tasks. Either
// argument (or both, in which case nothing is filtered) may be empty.
func filterBySchedule(tasks []model.Task, bucket, exactDate string, now time.Time) []model.Task {
	if bucket == "" && exactDate == "" {
		return tasks
	}
	out := tasks[:0]
	for _, t := range tasks {
		switch {
		case bucket != "":
			if schedule.MatchesBucket(t.Schedule, bucket, now) {
				out = append(out, t)
			}
		case exactDate != "":
			// Normalize legacy strings so a hand-crafted "today" still
			// matches today's ISO date filter.
			if schedule.Normalize(t.Schedule, now) == exactDate {
				out = append(out, t)
			}
		}
	}
	return out
}
