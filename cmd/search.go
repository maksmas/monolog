package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

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

// minUnionTokenLen is the shortest word rankQuery will rank on its own. A
// one-character token fuzzy-matches almost every task at a low score, so
// unioning it in buys no recall and only crowds the tail of the result set.
const minUnionTokenLen = 2

// rankQuery ranks query against ix, making multi-word queries
// order-independent.
//
// sahilm/fuzzy matches the query as an ordered subsequence over the whole
// candidate string, spaces included, so a phrase only matches when its words
// appear in that order. `search "week telegram"` finds "week command from
// telegram ..." while `search "telegram week"` does not — and, worse, does not
// report "No matches" either: it returns whichever unrelated task happens to
// contain t-e-l-e-g-r-a-m-space-w-e-e-k scattered through its text. That is a
// silently wrong answer, and the Claude skill leans on this command as its
// only duplicate guard before filing a task, so a missed hit means a duplicate
// task rather than a visible error.
//
// The fix is caller-side on purpose. internal/search.Rank keeps its exact
// subsequence semantics because the TUI overlay ranks per keystroke against
// live results, where a human refines the query incrementally and in-order
// matching is the desired behaviour. A one-shot CLI query has no such feedback
// loop, so it widens the net here instead:
//
//  1. the whole phrase, ranked first — an in-order match is the strongest
//     signal available and must never be displaced by token noise;
//  2. then each word on its own, merged, deduped by task ID, and re-sorted by
//     descending score so the best single-word evidence floats up regardless
//     of the order the words were typed in.
//
// Single-token queries take exactly the old path. Ordering therefore diverges
// from the TUI for multi-word queries only; see the parity tests
// (cmd.TestSearchCommand_DoneRankingMatchesSharedIndex and
// tui.TestSearch_RankingMatchesSharedIndexOverStoreList) for the pinned
// single-token agreement.
func rankQuery(ix *search.Index, query string, limit int) []search.Result {
	tokens := strings.Fields(query)
	switch len(tokens) {
	case 0:
		// All whitespace. The command rejects a blank query before it gets
		// here; pass the string through unchanged rather than quietly giving
		// it different semantics on a path nothing uses.
		return ix.Rank(query, limit)
	case 1:
		// Rank the bare token, not the raw string: padding is not part of the
		// query, and leaving it in would make a padded single word behave like
		// a phrase.
		return ix.Rank(tokens[0], limit)
	}

	// Rank with no truncation at each stage: capping per stage would let a
	// large whole-phrase result set hide every token hit, and the union is
	// trimmed to limit once at the end anyway.
	out := ix.Rank(query, 0)
	seen := make(map[string]bool, len(out))
	for _, r := range out {
		seen[r.Task.ID] = true
	}

	var extra []search.Result
	// pos maps a task ID to its slot in extra so a task matched by several
	// tokens is carried once, keeping the best score any single token gave it.
	pos := make(map[string]int)
	for _, tok := range tokens {
		if utf8.RuneCountInString(tok) < minUnionTokenLen {
			continue
		}
		for _, r := range ix.Rank(tok, 0) {
			if seen[r.Task.ID] {
				continue
			}
			if i, ok := pos[r.Task.ID]; ok {
				if r.Score > extra[i].Score {
					extra[i].Score = r.Score
				}
				continue
			}
			pos[r.Task.ID] = len(extra)
			extra = append(extra, r)
		}
	}

	sort.SliceStable(extra, func(i, j int) bool {
		if extra[i].Score != extra[j].Score {
			return extra[i].Score > extra[j].Score
		}
		return extra[i].Task.CreatedAt > extra[j].Task.CreatedAt
	})

	out = append(out, extra...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

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
			"Multiple arguments are joined with a space, so quoting is optional.\n" +
			"A multi-word query is order-independent: the whole phrase is ranked first,\n" +
			"then each word on its own, so \"telegram week\" and \"week telegram\" both find\n" +
			"the same task. A single distinctive keyword is still the sharpest query.",
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

			// Clamp before calling Rank: the ranker treats limit <= 0 as "no
			// truncation", so passing a non-positive value straight through
			// would dump the entire backlog instead of the top hits.
			if limit < 1 {
				limit = defaultSearchLimit
			}

			results := rankQuery(search.NewIndex(tasks), query, limit)

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
