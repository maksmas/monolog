package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/display"
	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/search"
	"github.com/maksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

// defaultSearchLimit caps how many ranked hits `monolog search` prints when
// --limit is not given (or is out of range).
const defaultSearchLimit = 10

func newSearchCmd() *cobra.Command {
	var (
		limit int
		done  bool
	)

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Fuzzy-search tasks by title and body",
		Long: "Fuzzy-searches task titles and bodies, printing the top matches with untruncated titles.\n" +
			"Title matches outrank body-only matches. Default: open tasks only, top 10.\n" +
			"Multiple arguments are joined with a space, so quoting is optional.",
		// MinimumNArgs rather than ExactArgs so `monolog search fix login bug`
		// works unquoted — friendlier for shell and agent callers alike.
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := openStore()
			if err != nil {
				return err
			}

			// An empty Status is "no filter", i.e. open + done. Note that -d
			// here means "include done", not "only done" as in `ls -d`.
			opts := store.ListOptions{Status: "open"}
			if done {
				opts.Status = ""
			}

			tasks, err := s.List(opts)
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}

			// Clamp before calling Rank: the ranker treats limit <= 0 as "no
			// truncation", so passing a non-positive value straight through
			// would dump the entire backlog instead of the top hits.
			if limit < 1 {
				limit = defaultSearchLimit
			}

			results := search.NewIndex(tasks).Rank(strings.Join(args, " "), limit)

			matches := make([]model.Task, len(results))
			for i, r := range results {
				matches[i] = r.Task
			}

			display.FormatSearchResults(cmd.OutOrStdout(), matches, time.Now(), config.DateFormat())
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", defaultSearchLimit, "Maximum number of results (values below 1 fall back to the default)")
	// -d/--done means "include completed", not "only completed" as in `ls -d`.
	// The letter is shared deliberately; -a is not, since `ls -a` means "all
	// schedules, still open" and reusing it here would invert its meaning.
	cmd.Flags().BoolVarP(&done, "done", "d", false, "Include completed tasks in the search")

	return cmd
}
