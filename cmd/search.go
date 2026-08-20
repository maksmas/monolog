package cmd

import (
	"errors"
	"fmt"
	"strings"

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
		Short: "Search tasks by title and body",
		// Behaviour and defaults only. The ranking rule is documented once, on
		// search.Index.RankTerms; restating it in a sixth place is how a false
		// claim drifted into the docs the last time.
		Long: "Searches task titles and bodies, printing the top matches with untruncated titles. " +
			"One word is fuzzy-matched; several are ranked by how many of them a task contains, so word order does not matter. " +
			"Arguments are joined with a space, so quoting is optional. Default: open tasks only, top 10.",
		// MinimumNArgs rather than ExactArgs so `monolog search fix login bug`
		// works unquoted — friendlier for shell and agent callers alike.
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// MinimumNArgs(1) is satisfied by an empty string, and
			// Index.Rank("") deliberately means "every task by CreatedAt
			// desc" — correct for the TUI's initial seed, catastrophic here.
			// A caller that interpolates an empty keyword string would get ten
			// arbitrary rows and read them as near-duplicates. Reject instead.
			query := strings.TrimSpace(strings.Join(args, " "))
			if query == "" {
				return errors.New("search query is empty: pass at least one non-blank keyword")
			}

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

			// Clamp into a local rather than writing back to the flag variable:
			// the ranker treats limit <= 0 as "no truncation", so a non-positive
			// value has to become the default before the call, but the flag
			// itself should keep meaning "what the user typed".
			n := limit
			if n < 1 {
				n = defaultSearchLimit
			}

			results := search.NewIndex(tasks).RankTerms(query, n)

			matches := make([]model.Task, len(results))
			for i, r := range results {
				matches[i] = r.Task
			}

			display.FormatSearchResults(cmd.OutOrStdout(), matches, config.DateFormat())
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
