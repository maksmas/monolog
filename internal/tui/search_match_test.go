package tui

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mmaksmas/monolog/internal/model"
)

// newDoc builds a searchDoc for tests. The ranker is fed parallel titles/
// bodies slices separately (matching the production path where openSearch
// caches them once), so there is nothing extra to precompute here.
func newDoc(id, title, body, createdAt string) searchDoc {
	return searchDoc{
		task: model.Task{
			ID:        id,
			Title:     title,
			Body:      body,
			Status:    "open",
			CreatedAt: createdAt,
		},
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

// rank is a test helper that derives titles/bodies from docs and calls the
// production ranker. Keeps each test case compact.
func rank(query string, docs []searchDoc, limit int) []searchResult {
	titles := make([]string, len(docs))
	bodies := make([]string, len(docs))
	for i, d := range docs {
		titles[i] = d.task.Title
		bodies[i] = d.task.Body
	}
	return rankSearch(query, docs, titles, bodies, limit)
}

func TestRankSearch_EmptyQuery_ReturnsAllDocsByCreatedAtDesc(t *testing.T) {
	docs := []searchDoc{
		newDoc("A", "alpha", "", "2026-04-01T10:00:00Z"),
		newDoc("B", "beta", "", "2026-04-03T10:00:00Z"),
		newDoc("C", "gamma", "", "2026-04-02T10:00:00Z"),
	}
	got := rank("", docs, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	want := []string{"B", "C", "A"}
	if ids := docIDs(got, docs); !reflect.DeepEqual(ids, want) {
		t.Errorf("order: got %v, want %v", ids, want)
	}
	for _, r := range got {
		if r.titleHit != nil {
			t.Errorf("empty query should not produce highlights, got titleHit=%v", r.titleHit)
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
	got := rank("login", docs, 0)
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
	got := rank("FIX", docs, 0)
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
	got := rank("zzzzz", docs, 0)
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
	got := rank("fix", docs, 0)
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
	got := rank("fix", docs, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 (limit), got %d", len(got))
	}

	// empty query path also respects the limit
	got2 := rank("", docs, 2)
	if len(got2) != 2 {
		t.Fatalf("empty query with limit: expected 2, got %d", len(got2))
	}

	// limit <= 0 means no truncation
	got3 := rank("fix", docs, 0)
	if len(got3) != 4 {
		t.Fatalf("limit 0 should not truncate, got %d", len(got3))
	}
}

func TestRankSearch_DoneTasksIncluded(t *testing.T) {
	docs := []searchDoc{
		newDoc("OPEN", "fix open bug", "", "2026-04-01T10:00:00Z"),
		newDoneDoc("DONE", "fix done bug", "", "2026-04-02T10:00:00Z"),
	}
	got := rank("fix", docs, 0)
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
	got := rank("login", docs, 0)
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
	// "login" in "fix login" starts at byte index 4. First matched position should be 4.
	if titleResult != nil && len(titleResult.titleHit) > 0 && titleResult.titleHit[0] != 4 {
		t.Errorf("A first titleHit index: got %d, want 4", titleResult.titleHit[0])
	}
	// B must not have title hits (title doesn't contain 'login'); body match is
	// scored but not tracked for rendering.
	if bodyResult != nil && len(bodyResult.titleHit) != 0 {
		t.Errorf("B should not have title hits, got %v", bodyResult.titleHit)
	}
}

// TestRankSearch_MultibyteTitleMatch asserts the ranker and title hits work
// for titles containing multi-byte runes. "café" is 5 bytes (c=1, a=1, f=1,
// é=2) — matching query "é" must land on byte offset 3, which is where the
// "é" rune begins. An earlier implementation precomputed a lowercased copy
// and passed the original title to highlighting, which broke alignment for
// runes whose lowercase form changes byte length; this test guards against
// regressing to that class of bug.
func TestRankSearch_MultibyteTitleMatch(t *testing.T) {
	docs := []searchDoc{
		newDoc("CAFE", "café", "", "2026-04-01T10:00:00Z"),
	}
	got := rank("é", docs, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if len(got[0].titleHit) != 1 {
		t.Fatalf("expected 1 title hit, got %v", got[0].titleHit)
	}
	if got[0].titleHit[0] != 3 {
		t.Errorf("titleHit[0] = %d, want 3 (byte offset of 'é' in 'café')", got[0].titleHit[0])
	}
	// Feed the hit through highlightMatches against the original title and
	// verify the rendered output still strips to the original plain string.
	styled := highlightMatches("café", got[0].titleHit)
	// Strip any ANSI escapes and ensure we get "café" back (i.e. no runes
	// were dropped and no stray bytes injected).
	if plain := ansi.Strip(styled); plain != "café" {
		t.Errorf("highlightMatches round-trip: got %q, want %q", plain, "café")
	}
}

// TestRankSearch_CaseInsensitiveMultibyte confirms case-insensitive matching
// still works across common multi-byte ranges (here, "Café" vs "cafe" / "CAFE"),
// and that the returned byte offsets align with the original-case title so
// highlight rendering is stable.
func TestRankSearch_CaseInsensitiveMultibyte(t *testing.T) {
	docs := []searchDoc{
		newDoc("CAFE", "Café latte", "", "2026-04-01T10:00:00Z"),
	}
	// Lowercase query should match the mixed-case original title.
	got := rank("café", docs, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 result for lowercase query, got %d", len(got))
	}
	if len(got[0].titleHit) == 0 || got[0].titleHit[0] != 0 {
		t.Errorf("expected first hit at byte offset 0, got %v", got[0].titleHit)
	}
	// Highlighting the original title with these offsets must round-trip.
	styled := highlightMatches("Café latte", got[0].titleHit)
	if plain := ansi.Strip(styled); plain != "Café latte" {
		t.Errorf("highlightMatches round-trip: got %q, want %q", plain, "Café latte")
	}
}

func TestRankSearch_EmptyDocs(t *testing.T) {
	got := rankSearch("anything", nil, nil, nil, 10)
	if got == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty results, got %d", len(got))
	}
}
