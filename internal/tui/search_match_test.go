package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mmaksmas/monolog/internal/model"
)

// newDoc builds a searchDoc with lowercased title/body precomputed the same
// way openSearch will do it at runtime.
func newDoc(id, title, body, createdAt string) searchDoc {
	return searchDoc{
		task: model.Task{
			ID:        id,
			Title:     title,
			Body:      body,
			Status:    "open",
			CreatedAt: createdAt,
		},
		titleLC: strings.ToLower(title),
		bodyLC:  strings.ToLower(body),
	}
}

func newDoneDoc(id, title, body, createdAt string) searchDoc {
	d := newDoc(id, title, body, createdAt)
	d.task.Status = "done"
	return d
}

// docIDs extracts the ordered list of task IDs from a result slice, using
// docIdx to look up the original doc. Tests compare against this to assert
// stable ordering without caring about score values.
func docIDs(results []searchResult, docs []searchDoc) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = docs[r.docIdx].task.ID
	}
	return out
}

func TestRankSearch_EmptyQuery_ReturnsAllDocsByCreatedAtDesc(t *testing.T) {
	docs := []searchDoc{
		newDoc("A", "alpha", "", "2026-04-01T10:00:00Z"),
		newDoc("B", "beta", "", "2026-04-03T10:00:00Z"),
		newDoc("C", "gamma", "", "2026-04-02T10:00:00Z"),
	}
	got := rankSearch("", docs, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	want := []string{"B", "C", "A"}
	if ids := docIDs(got, docs); !reflect.DeepEqual(ids, want) {
		t.Errorf("order: got %v, want %v", ids, want)
	}
	for _, r := range got {
		if r.titleHit != nil || r.bodyHit != nil {
			t.Errorf("empty query should not produce highlights, got titleHit=%v bodyHit=%v", r.titleHit, r.bodyHit)
		}
	}
}

func TestRankSearch_TitleWeightBeatsBody(t *testing.T) {
	// Two docs with the "same" content in different fields. The one with
	// the match in the title must sort first because title score is doubled.
	docs := []searchDoc{
		newDoc("BODY", "unrelated words", "the word login appears here", "2026-04-01T10:00:00Z"),
		newDoc("TITLE", "login flow", "unrelated body", "2026-04-01T10:00:00Z"),
	}
	got := rankSearch("login", docs, 0)
	if len(got) < 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if docs[got[0].docIdx].task.ID != "TITLE" {
		t.Errorf("expected TITLE first, got %v", docIDs(got, docs))
	}
}

func TestRankSearch_CaseInsensitive(t *testing.T) {
	docs := []searchDoc{
		newDoc("A", "Fix login bug", "", "2026-04-01T10:00:00Z"),
		newDoc("B", "another task", "", "2026-04-01T10:00:00Z"),
	}
	got := rankSearch("FIX", docs, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(got), docIDs(got, docs))
	}
	if docs[got[0].docIdx].task.ID != "A" {
		t.Errorf("expected A, got %v", docIDs(got, docs))
	}
}

func TestRankSearch_NoMatches_ReturnsEmpty(t *testing.T) {
	docs := []searchDoc{
		newDoc("A", "alpha", "beta", "2026-04-01T10:00:00Z"),
	}
	got := rankSearch("zzzzz", docs, 0)
	if got == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 results, got %d", len(got))
	}
}

func TestRankSearch_TieBreakByCreatedAtDesc(t *testing.T) {
	// Identical titles guarantee equal scores so the tie-break kicks in.
	docs := []searchDoc{
		newDoc("OLD", "fix login", "", "2026-04-01T10:00:00Z"),
		newDoc("NEW", "fix login", "", "2026-04-05T10:00:00Z"),
		newDoc("MID", "fix login", "", "2026-04-03T10:00:00Z"),
	}
	got := rankSearch("fix", docs, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	want := []string{"NEW", "MID", "OLD"}
	if ids := docIDs(got, docs); !reflect.DeepEqual(ids, want) {
		t.Errorf("tie-break order: got %v, want %v", ids, want)
	}
}

func TestRankSearch_LimitTruncates(t *testing.T) {
	docs := []searchDoc{
		newDoc("A", "fix one", "", "2026-04-01T10:00:00Z"),
		newDoc("B", "fix two", "", "2026-04-02T10:00:00Z"),
		newDoc("C", "fix three", "", "2026-04-03T10:00:00Z"),
		newDoc("D", "fix four", "", "2026-04-04T10:00:00Z"),
	}
	got := rankSearch("fix", docs, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 (limit), got %d", len(got))
	}

	// empty query path also respects the limit
	got2 := rankSearch("", docs, 2)
	if len(got2) != 2 {
		t.Fatalf("empty query with limit: expected 2, got %d", len(got2))
	}

	// limit <= 0 means no truncation
	got3 := rankSearch("fix", docs, 0)
	if len(got3) != 4 {
		t.Fatalf("limit 0 should not truncate, got %d", len(got3))
	}
}

func TestRankSearch_DoneTasksIncluded(t *testing.T) {
	docs := []searchDoc{
		newDoc("OPEN", "fix open bug", "", "2026-04-01T10:00:00Z"),
		newDoneDoc("DONE", "fix done bug", "", "2026-04-02T10:00:00Z"),
	}
	got := rankSearch("fix", docs, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	var seenDone bool
	for _, r := range got {
		if docs[r.docIdx].task.ID == "DONE" {
			seenDone = true
		}
	}
	if !seenDone {
		t.Errorf("done task should appear in results, got %v", docIDs(got, docs))
	}
}

func TestRankSearch_MatchPositionsReturned(t *testing.T) {
	docs := []searchDoc{
		newDoc("A", "fix login", "body content", "2026-04-01T10:00:00Z"),
		newDoc("B", "unrelated", "login is here", "2026-04-01T10:00:00Z"),
	}
	got := rankSearch("login", docs, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	// Find result for A (title match) and B (body match).
	var titleResult, bodyResult *searchResult
	for i, r := range got {
		switch docs[r.docIdx].task.ID {
		case "A":
			titleResult = &got[i]
		case "B":
			bodyResult = &got[i]
		}
	}
	if titleResult == nil || len(titleResult.titleHit) == 0 {
		t.Errorf("A should have title match positions, got %+v", titleResult)
	}
	if bodyResult == nil || len(bodyResult.bodyHit) == 0 {
		t.Errorf("B should have body match positions, got %+v", bodyResult)
	}
	// "login" in "fix login" starts at index 4. First matched position should be 4.
	if titleResult != nil && len(titleResult.titleHit) > 0 && titleResult.titleHit[0] != 4 {
		t.Errorf("A first titleHit index: got %d, want 4", titleResult.titleHit[0])
	}
	// B must not have title hits (title doesn't contain 'login').
	if bodyResult != nil && len(bodyResult.titleHit) != 0 {
		t.Errorf("B should not have title hits, got %v", bodyResult.titleHit)
	}
}

func TestRankSearch_EmptyDocs(t *testing.T) {
	got := rankSearch("anything", nil, 10)
	if got == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty results, got %d", len(got))
	}
}
