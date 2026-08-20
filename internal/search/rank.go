// Package search provides the one ranker over tasks in this codebase, shared
// by the TUI search overlay and the `monolog search` CLI command so neither
// has to grow a private copy of the ranking logic.
//
// Both sides build an Index over a task set. There are two entry points, and
// which one a caller uses is the only place ranking can differ between them:
//
//   - Rank is fuzzy matching — the query is one ordered subsequence over the
//     whole candidate string. The TUI always uses it, because a human refining
//     a query per keystroke against live results wants exactly that.
//   - RankTerms counts how many query words a task actually contains. The CLI
//     uses it, since a one-shot query has no keystroke feedback loop; see its
//     doc comment for the full rationale.
//
// RankTerms delegates a single-word query straight to Rank, so the two agree
// exactly on every one-word query and diverge only on phrases.
//
// The package is status-agnostic — filtering open vs. done tasks is the
// caller's job.
package search

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/maksmas/monolog/internal/model"
	"github.com/sahilm/fuzzy"
)

// Result is a ranked match carrying the source task.
//
// Score and TitleHit describe a fuzzy match and are therefore only populated
// by Rank. TitleHit holds byte-offset match positions from sahilm/fuzzy,
// suitable for highlight rendering against the original-case title; it is nil
// when the title did not match (body-only hit) or the query was empty.
// RankTerms ranks on term counts rather than on a fuzzy score, so the results
// it builds itself carry a zero Score and a nil TitleHit — its CLI caller
// renders neither.
type Result struct {
	Task     model.Task
	Score    int
	TitleHit []int
}

// Index is a prepared task set ready to be ranked repeatedly. Building it once
// and calling Rank per keystroke keeps the TUI overlay from re-allocating the
// parallel title/body slices on every input event.
//
// The zero value is a usable empty index (Len returns 0, Rank returns no
// results), and so is a nil *Index — the nil case is what lets callers nil the
// field out to release the task set while render paths keep calling Len/Rank.
// Construct populated indexes with NewIndex.
type Index struct {
	tasks  []model.Task
	titles []string
	bodies []string
}

// NewIndex builds an Index over tasks, extracting the title and body slices
// fuzzy.Find operates on.
//
// The task slice is copied, so the index owns its own slice header and
// elements: callers may append to, re-sort, or reassign elements of the slice
// they passed in without the index — or the Result.Task values it hands back —
// changing underneath them. That is what the TUI relies on when it hands over
// the live Model.allTasks.
//
// The copy is shallow, and the snapshot guarantee stops there. Task's
// slice-typed fields (Tags) still share a backing array with the caller's
// tasks, so an in-place edit of one — model.SetActive(false) filters via
// `out := t.Tags[:0]`, rewriting the array in place — is visible through the
// index too. No caller does that to a task it has already indexed, and
// deep-copying every Tags slice on every index build would cost more than the
// hazard is worth; the boundary is documented rather than defended.
//
// The copy happens once per index build, not per Rank call.
//
// Titles and bodies are stored as-is. sahilm/fuzzy performs
// case-insensitive matching natively via Unicode case folding, so no
// pre-lowercased copies are needed; storing them would also misalign
// match-index positions for runes whose lowercase form has a different byte
// length (e.g. Turkish "İ" -> "i", German "ẞ" -> "ß").
func NewIndex(tasks []model.Task) *Index {
	ix := &Index{
		tasks:  append([]model.Task(nil), tasks...),
		titles: make([]string, len(tasks)),
		bodies: make([]string, len(tasks)),
	}
	for i, t := range tasks {
		ix.titles[i] = t.Title
		ix.bodies[i] = t.Body
	}
	return ix
}

// Len reports how many tasks the index holds. It is nil-receiver safe and
// returns 0 for a nil *Index.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return len(ix.tasks)
}

// titleWeight multiplies the title score so title hits outrank body-only hits.
const titleWeight = 2

// rankAgg is a per-task scratchpad used while combining title and body match
// scores inside Rank. It is a plain struct (not a slice of structs of slices)
// so the outer []rankAgg stays cache-friendly.
type rankAgg struct {
	titleScore int
	bodyScore  int
	titleHit   []int
	matched    bool
}

// Rank returns matching tasks ordered by descending score with a
// CreatedAt-descending tie-break.
//
// Empty query returns every task sorted by CreatedAt desc (no highlights),
// truncated to limit. A non-empty query runs sahilm/fuzzy against the indexed
// titles and bodies, combines per-task scores as
// max(titleScore*titleWeight, bodyScore), and drops tasks that matched neither.
//
// limit <= 0 is treated as "no truncation".
//
// Rank always returns a non-nil slice, and is nil-receiver safe.
func (ix *Index) Rank(query string, limit int) []Result {
	if ix == nil || len(ix.tasks) == 0 {
		return []Result{}
	}

	if query == "" {
		results := make([]Result, len(ix.tasks))
		for i, t := range ix.tasks {
			results[i] = Result{Task: t}
		}
		sort.SliceStable(results, func(i, j int) bool {
			return newerFirst(results[i].Task, results[j].Task)
		})
		return truncateResults(results, limit)
	}

	aggs := make([]rankAgg, len(ix.tasks))

	for _, m := range fuzzy.Find(query, ix.titles) {
		a := &aggs[m.Index]
		a.matched = true
		a.titleScore = m.Score
		// Defensive-copy MatchedIndexes so a Result never aliases a buffer
		// owned by sahilm/fuzzy.
		//
		// This is forward-insurance, not a fix for observable behaviour under
		// the pinned sahilm/fuzzy v0.1.1: there, FindFromNoSort recycles the
		// index buffer only after a *failed* candidate (`matchedIndexes =
		// match.MatchedIndexes[:0]`) and sets it to nil after a successful one,
		// so two returned Matches never share a backing array. No test can
		// exercise aliasing against v0.1.1 — dropping the copy keeps every
		// test green. Keep it anyway: the recycling scheme is an internal
		// optimization upstream is free to widen to successful matches, and
		// the copy is one allocation per title hit on a slice of a few ints.
		//
		// Body hits skip the copy because a body equivalent of TitleHit is
		// intentionally not carried on Result.
		a.titleHit = append([]int(nil), m.MatchedIndexes...)
	}

	for _, m := range fuzzy.Find(query, ix.bodies) {
		a := &aggs[m.Index]
		a.matched = true
		a.bodyScore = m.Score
	}

	results := make([]Result, 0, len(ix.tasks))
	for i, a := range aggs {
		if !a.matched {
			continue
		}
		score := a.titleScore * titleWeight
		if a.bodyScore > score {
			score = a.bodyScore
		}
		results = append(results, Result{
			Task:     ix.tasks[i],
			Score:    score,
			TitleHit: a.titleHit,
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return newerFirst(results[i].Task, results[j].Task)
	})

	return truncateResults(results, limit)
}

// newerFirst is the final tie-break both Rank and RankTerms fall back on.
// CreatedAt is RFC3339, so comparing the strings compares the instants.
func newerFirst(a, b model.Task) bool {
	return a.CreatedAt > b.CreatedAt
}

// minTermRunes is the shortest query word RankTerms counts as a term. A
// one-character term is a substring of nearly every sentence, so counting it
// would flatten the term-hit ranking back into noise.
const minTermRunes = 2

// distinctTerms lowercases the words of a query and drops the ones too short
// to discriminate, deduplicating so a word typed twice cannot inflate a task's
// term-hit count. It takes the query already split by strings.Fields.
//
// Lowercasing here is safe even though NewIndex deliberately does NOT
// pre-lowercase its title/body copies: that constraint exists because
// sahilm/fuzzy reports MatchedIndexes as byte offsets into the indexed string,
// and case folding can change a rune's byte length. Nothing downstream of this
// function is an offset — the terms feed a boolean strings.Contains test only.
func distinctTerms(fields []string) []string {
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

// termCandidate is a task that hit at least one query term, carried with the
// counts RankTerms sorts on.
type termCandidate struct {
	task      model.Task
	termHits  int // distinct query terms found in title or body
	titleHits int // of those, how many were found in the title
}

// RankTerms ranks a multi-word query by how many of its words a task actually
// contains. It is what `monolog search` calls, and it is the canonical
// explanation of why the CLI and the TUI rank phrases differently — other doc
// comments and tests point here rather than restating it.
//
// A single-word query is delegated to Rank unchanged, which is what keeps
// single-token CLI output in lockstep with the TUI overlay (pinned from both
// sides by cmd.TestSearchCommand_DoneRankingMatchesSharedIndex and
// tui.TestSearch_RankingMatchesSharedIndexOverStoreList).
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
// The TUI masks both — a human refines the query per keystroke and watches it
// narrow. A one-shot CLI query has no such feedback loop, and the Claude Code
// skill (docs/claude-skill/SKILL.md) runs this command as its only duplicate
// guard before filing a task, with no write cap behind it. A missed hit files a
// duplicate; a noisy result set makes the dedupe judgement unreliable in the
// other direction.
//
// So a multi-word query is ranked the way search engines rank one — by how many
// query terms the document actually contains:
//
//  1. tokenize on whitespace, keeping words of at least minTermRunes runes;
//  2. count the distinct terms appearing as a case-insensitive substring of the
//     title or body (the term-hit count);
//  3. drop tasks whose term-hit count is zero — this is the precision floor,
//     and it is what removes the subsequence noise above;
//  4. sort by term-hit count desc, then title hits desc, then CreatedAt desc.
//
// Every one of those keys is a function of the *set* of query words, so the
// returned rows and their order are identical however the words are ordered
// (and however much whitespace separates them). That order-independence is a
// documented promise in README.md and SKILL.md, which is why there is no
// whole-phrase fuzzy score in the tie-break chain: it would reintroduce word
// order through the back door and, once the limit clamps the list, change which
// rows are printed at all.
//
// It is a ranking rule with a zero-hit floor, not an AND filter: "telegram
// pagination bug" still surfaces a task matching two of the three words, just
// below anything matching all three.
//
// Two inputs are not multi-word and fall through to Rank rather than to a
// second set of semantics: a query that tokenizes to nothing (blank — the
// command rejects that before it gets here) and one whose every word is a
// single rune, where there is nothing left to count.
//
// Results built here carry no Score or TitleHit; see Result. Ranking is over
// the whole index, so limit only truncates the finished list, and limit <= 0
// means no truncation. RankTerms is nil-receiver safe and always returns a
// non-nil slice.
func (ix *Index) RankTerms(query string, limit int) []Result {
	if ix == nil || len(ix.tasks) == 0 {
		return []Result{}
	}

	fields := strings.Fields(query)
	switch len(fields) {
	case 0:
		return ix.Rank(query, limit)
	case 1:
		// Rank the bare token, not the raw string: padding is not part of the
		// query, and leaving it in would make a padded single word behave like
		// a phrase.
		return ix.Rank(fields[0], limit)
	}

	terms := distinctTerms(fields)
	if len(terms) == 0 {
		return ix.Rank(query, limit)
	}

	// One pass over the index, so a task hitting several terms is carried once
	// — no dedupe bookkeeping needed.
	cands := make([]termCandidate, 0, len(ix.tasks))
	for i, t := range ix.tasks {
		title := strings.ToLower(ix.titles[i])
		body := strings.ToLower(ix.bodies[i])

		c := termCandidate{task: t}
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
		return newerFirst(a.task, b.task)
	})

	results := make([]Result, len(cands))
	for i, c := range cands {
		results[i] = Result{Task: c.task}
	}
	return truncateResults(results, limit)
}

// truncateResults caps results at limit, treating limit <= 0 as no truncation.
func truncateResults(results []Result, limit int) []Result {
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
}
