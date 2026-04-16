package tui

import (
	"sort"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/sahilm/fuzzy"
)

// searchDoc is a precomputed searchable document wrapping a Task with
// lowercased title and body strings so the ranker does not repeatedly
// allocate for case-insensitive matching.
type searchDoc struct {
	task    model.Task
	titleLC string
	bodyLC  string
}

// searchResult is a ranked match pointing back at the source haystack index.
// TitleHit and BodyHit carry byte-offset match positions from sahilm/fuzzy,
// suitable for highlight rendering. Either may be nil when that field did not
// match (or when the query was empty).
type searchResult struct {
	docIdx   int
	score    int
	titleHit []int
	bodyHit  []int
}

// titleWeight multiplies the title score so title hits outrank body-only hits.
const titleWeight = 2

// rankSearch is a pure ranker: given a query, a precomputed haystack, and a
// result limit, it returns matching documents ordered by descending score with
// a CreatedAt-descending tie-break.
//
// Empty query returns every doc sorted by CreatedAt desc (no highlights),
// truncated to limit. A non-empty query runs sahilm/fuzzy against the
// precomputed title and body slices, combines per-doc scores as
// max(titleScore*titleWeight, bodyScore), and drops docs that matched neither.
//
// limit <= 0 is treated as "no truncation".
func rankSearch(query string, docs []searchDoc, limit int) []searchResult {
	if len(docs) == 0 {
		return []searchResult{}
	}

	if query == "" {
		results := make([]searchResult, len(docs))
		for i := range docs {
			results[i] = searchResult{docIdx: i}
		}
		sort.SliceStable(results, func(i, j int) bool {
			return docs[results[i].docIdx].task.CreatedAt > docs[results[j].docIdx].task.CreatedAt
		})
		return truncate(results, limit)
	}

	titles := make([]string, len(docs))
	bodies := make([]string, len(docs))
	for i, d := range docs {
		titles[i] = d.titleLC
		bodies[i] = d.bodyLC
	}

	type agg struct {
		titleScore int
		bodyScore  int
		titleHit   []int
		bodyHit    []int
		matched    bool
	}
	aggs := make([]agg, len(docs))

	for _, m := range fuzzy.Find(query, titles) {
		a := &aggs[m.Index]
		a.matched = true
		a.titleScore = m.Score
		a.titleHit = append([]int(nil), m.MatchedIndexes...)
	}

	for _, m := range fuzzy.Find(query, bodies) {
		a := &aggs[m.Index]
		a.matched = true
		a.bodyScore = m.Score
		a.bodyHit = append([]int(nil), m.MatchedIndexes...)
	}

	results := make([]searchResult, 0, len(docs))
	for i, a := range aggs {
		if !a.matched {
			continue
		}
		score := a.titleScore * titleWeight
		if a.bodyScore > score {
			score = a.bodyScore
		}
		results = append(results, searchResult{
			docIdx:   i,
			score:    score,
			titleHit: a.titleHit,
			bodyHit:  a.bodyHit,
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return docs[results[i].docIdx].task.CreatedAt > docs[results[j].docIdx].task.CreatedAt
	})

	return truncate(results, limit)
}

func truncate(results []searchResult, limit int) []searchResult {
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
}
