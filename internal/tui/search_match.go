package tui

import (
	"sort"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/sahilm/fuzzy"
)

// searchDoc is a searchable document wrapping a Task. sahilm/fuzzy performs
// case-insensitive matching natively via Unicode case folding, so no
// pre-lowercased copies are needed; storing them would also misalign
// match-index positions for runes whose lowercase form has a different byte
// length (e.g. Turkish "İ" -> "i", German "ẞ" -> "ß").
type searchDoc struct {
	task model.Task
}

// searchResult is a ranked match pointing back at the source haystack index.
// TitleHit carries byte-offset match positions from sahilm/fuzzy, suitable
// for highlight rendering against the original-case title. It is nil when
// the title did not match (body-only hit) or the query was empty.
type searchResult struct {
	docIdx   int
	score    int
	titleHit []int
}

// titleWeight multiplies the title score so title hits outrank body-only hits.
const titleWeight = 2

// rankAgg is a per-doc scratchpad used while combining title and body match
// scores inside rankSearch. It is a plain struct (not a slice of structs of
// slices) so the outer []rankAgg stays cache-friendly.
type rankAgg struct {
	titleScore int
	bodyScore  int
	titleHit   []int
	matched    bool
}

// rankSearch is a pure ranker: given a query, precomputed title/body slices
// indexed the same as docs, and a result limit, it returns matching documents
// ordered by descending score with a CreatedAt-descending tie-break.
//
// Empty query returns every doc sorted by CreatedAt desc (no highlights),
// truncated to limit. A non-empty query runs sahilm/fuzzy against the
// provided title and body slices, combines per-doc scores as
// max(titleScore*titleWeight, bodyScore), and drops docs that matched neither.
//
// limit <= 0 is treated as "no truncation".
//
// titles and bodies must be the same length as docs. Passing them in
// separately lets openSearch cache the []string slices once so the ranker
// does not re-allocate on every keystroke.
func rankSearch(query string, docs []searchDoc, titles, bodies []string, limit int) []searchResult {
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

	aggs := make([]rankAgg, len(docs))

	for _, m := range fuzzy.Find(query, titles) {
		a := &aggs[m.Index]
		a.matched = true
		a.titleScore = m.Score
		// Defensive-copy MatchedIndexes: sahilm/fuzzy reuses this buffer
		// across Match entries inside a single Find call, so retaining the
		// slice without copying would later show the last match's indexes
		// for every earlier hit. Body hits skip the copy because bodyHit
		// is intentionally not carried on searchResult.
		a.titleHit = append([]int(nil), m.MatchedIndexes...)
	}

	for _, m := range fuzzy.Find(query, bodies) {
		a := &aggs[m.Index]
		a.matched = true
		a.bodyScore = m.Score
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
