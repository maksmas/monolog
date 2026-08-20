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

// minTermRunes is the shortest query word rankQuery counts as a term. A
// one-character term is a substring of nearly every sentence, so counting it
// would flatten the term-hit ranking back into noise.
const minTermRunes = 2

// queryTerms lowercases the words of a multi-word query and drops the ones too
// short to discriminate, deduplicating so a word typed twice cannot inflate a
// task's term-hit count.
//
// Lowercasing here is safe even though internal/search deliberately does NOT
// pre-lowercase its title/body copies: that constraint exists because
// sahilm/fuzzy reports MatchedIndexes as byte offsets into the indexed string,
// and case folding can change a rune's byte length. Nothing downstream of this
// function is an offset — the terms feed a boolean strings.Contains test only.
func queryTerms(fields []string) []string {
	terms := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if utf8.RuneCountInString(f) < minTermRunes {
			continue
		}
		term := strings.ToLower(f)
		if seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

// candidate is a task that hit at least one query term, carried with the
// counts rankQuery sorts on.
type candidate struct {
	res       search.Result
	termHits  int // distinct query terms found in title or body
	titleHits int // of those, how many were found in the title
}

// rankQuery ranks query over tasks, ordering a multi-word query by how many of
// its words a task actually contains.
//
// A single-token query goes straight to the shared ranker, unchanged: that is
// the path pinned in lockstep with the TUI overlay by
// cmd.TestSearchCommand_DoneRankingMatchesSharedIndex and
// tui.TestSearch_RankingMatchesSharedIndexOverStoreList.
//
// Multi-word queries need their own rule. sahilm/fuzzy matches a query as one
// ordered subsequence over the whole candidate string, spaces included, which
// costs both recall and precision at once:
//
//   - recall: a phrase only matches when its words appear in that order, so
//     `search "telegram week"` misses the task "week command from telegram ...";
//   - precision: a short word is an ordered subsequence of almost any sentence,
//     so "week" alone matches "pay valge rent" (w-e-e-k scattered through it).
//     The result set fills with rows containing neither word, and the one real
//     hit sits behind a spurious leader.
//
// Both matter here because the Claude Code skill (docs/claude-skill/SKILL.md)
// runs this command as its only duplicate guard before filing a task, with no
// write cap behind it. A missed hit files a duplicate; a noisy result set makes
// the dedupe judgement unreliable in the other direction.
//
// So a multi-word query is ranked the way search engines rank one — by how many
// query terms the document actually contains:
//
//  1. tokenize on whitespace, keeping words of at least minTermRunes runes;
//  2. count the distinct terms appearing as a case-insensitive substring of the
//     title or body (the term-hit count);
//  3. drop tasks whose term-hit count is zero — this is the precision floor,
//     and it is what removes the subsequence noise above;
//  4. sort by term-hit count desc, then title hits desc, then the whole-phrase
//     fuzzy score desc, then CreatedAt desc (the ranker's own tie-break).
//
// It is a ranking rule with a zero-hit floor, not an AND filter: "telegram
// pagination bug" still surfaces a task matching two of the three words, just
// below anything matching all three.
//
// internal/search.Rank keeps its exact in-order semantics — the TUI ranks per
// keystroke against live results, where a human refines the query incrementally
// and in-order matching is what they expect. Only the one-shot CLI, which has
// no such feedback loop, widens the rule, and it does so caller-side.
//
// Result.Score and Result.TitleHit are carried over from the whole-phrase
// ranking and are therefore zero/nil for a task that hit terms but did not
// match the phrase as a subsequence. The CLI renders neither; they exist so the
// tie-break has something to sort on.
func rankQuery(tasks []model.Task, query string, limit int) []search.Result {
	ix := search.NewIndex(tasks)

	fields := strings.Fields(query)
	switch len(fields) {
	case 0:
		// All whitespace. The command rejects a blank query before it gets
		// here; pass the string through unchanged rather than quietly giving
		// it different semantics on a path nothing uses.
		return ix.Rank(query, limit)
	case 1:
		// Rank the bare token, not the raw string: padding is not part of the
		// query, and leaving it in would make a padded single word behave like
		// a phrase.
		return ix.Rank(fields[0], limit)
	}

	terms := queryTerms(fields)
	if len(terms) == 0 {
		// Every word was one rune long, so there is nothing to count. Fall
		// back to the phrase ranking rather than reporting no matches.
		return ix.Rank(query, limit)
	}

	// Whole-phrase scores, used only as a tie-break between tasks with equal
	// term counts. Ranked with no truncation: an arbitrary cut here would
	// silently zero the score of a task that hit every term.
	phrase := make(map[string]search.Result, len(tasks))
	for _, r := range ix.Rank(query, 0) {
		phrase[r.Task.ID] = r
	}

	// One pass over tasks, so a task hitting several terms is carried once —
	// no dedupe bookkeeping needed.
	cands := make([]candidate, 0, len(tasks))
	for _, t := range tasks {
		title := strings.ToLower(t.Title)
		body := strings.ToLower(t.Body)

		c := candidate{res: search.Result{Task: t}}
		for _, term := range terms {
			inTitle := strings.Contains(title, term)
			if inTitle {
				c.titleHits++
			}
			if inTitle || strings.Contains(body, term) {
				c.termHits++
			}
		}
		if c.termHits == 0 {
			continue
		}
		if p, ok := phrase[t.ID]; ok {
			c.res.Score = p.Score
			c.res.TitleHit = p.TitleHit
		}
		cands = append(cands, c)
	}

	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.termHits != b.termHits {
			return a.termHits > b.termHits
		}
		if a.titleHits != b.titleHits {
			return a.titleHits > b.titleHits
		}
		if a.res.Score != b.res.Score {
			return a.res.Score > b.res.Score
		}
		return a.res.Task.CreatedAt > b.res.Task.CreatedAt
	})

	if limit > 0 && len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]search.Result, len(cands))
	for i, c := range cands {
		out[i] = c.res
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
		Short: "Search tasks by title and body",
		Long: "Searches task titles and bodies, printing the top matches with untruncated titles.\n" +
			"Title matches outrank body-only matches. Default: open tasks only, top 10.\n" +
			"Multiple arguments are joined with a space, so quoting is optional.\n" +
			"A single word is fuzzy-matched. A multi-word query is ranked by how many of its\n" +
			"words a task actually contains: tasks containing none are dropped, and the rest\n" +
			"sort by matching-word count, then title over body, then fuzzy score. Word order\n" +
			"does not matter, and adding words narrows the result set rather than widening it.",
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

			results := rankQuery(tasks, query, limit)

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
