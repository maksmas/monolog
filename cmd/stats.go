package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newStatsCmd() *cobra.Command {
	var tag string

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show backlog statistics",
		Long:  "Prints the same numbers as the TUI stats bar: total, open and done task counts, the average age of open tasks, and the average time to complete done tasks.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := openStore()
			if err != nil {
				return err
			}

			tasks, err := s.List(store.ListOptions{Tag: tag})
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}

			writeStats(cmd.OutOrStdout(), model.ComputeStats(tasks, time.Now()))
			return nil
		},
	}

	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Only count tasks with this tag")

	return cmd
}

// writeStats prints one labelled line per metric. Averages print "-" when
// there is nothing to average, so an empty backlog does not show "1h".
func writeStats(w io.Writer, s model.Stats) {
	avgOpen := "-"
	if s.Open > 0 && s.AvgDaysOpen > 0 {
		avgOpen = model.FormatDuration(s.AvgDaysOpen)
	}
	avgDone := "-"
	if s.AvgDaysToComplete > 0 {
		avgDone = model.FormatDuration(s.AvgDaysToComplete)
	}
	fmt.Fprintf(w, "Total:                   %d\n", s.Total)
	fmt.Fprintf(w, "Open:                    %d\n", s.Open)
	fmt.Fprintf(w, "Done:                    %d\n", s.Done)
	fmt.Fprintf(w, "Avg time open:           %s\n", avgOpen)
	fmt.Fprintf(w, "Avg time to complete:    %s\n", avgDone)
}
