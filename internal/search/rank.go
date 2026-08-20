// Package search provides a pure fuzzy ranker over tasks. It is shared by the
// TUI search overlay and the `monolog search` CLI command so ranking can never
// drift between the two: both build an Index over a task set and call Rank.
//
// The package is status-agnostic — filtering open vs. done tasks is the
// caller's job.
package search

import (
	"sort"

	"github.com/maksmas/monolog/internal/model"
	"github.com/sahilm/fuzzy"
)

// Result is a ranked match carrying the source task. TitleHit holds
// byte-offset match positions from sahilm/fuzzy, suitable for highlight
// rendering against the original-case title. It is nil when the title did not
// match (body-only hit) or the query was empty; CLI callers ignore it.
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
// The task slice is copied, so the index is a true snapshot: callers may keep
// mutating (or reassigning elements of) the slice they passed in without the
// index — or the Result.Task values it hands back — changing underneath them.
// The copy is shallow and happens once per index build, not per Rank call.
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
			return results[i].Task.CreatedAt > results[j].Task.CreatedAt
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
		return results[i].Task.CreatedAt > results[j].Task.CreatedAt
	})

	return truncateResults(results, limit)
}

// truncateResults caps results at limit, treating limit <= 0 as no truncation.
func truncateResults(results []Result, limit int) []Result {
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
}
