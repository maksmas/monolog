package search

import (
	"reflect"
	"testing"

	"github.com/maksmas/monolog/internal/model"
)

// newTask builds an open task for tests. NewIndex derives the title/body
// slices itself, so there is nothing extra to precompute here.
func newTask(id, title, body, createdAt string) model.Task {
	return model.Task{
		ID:        id,
		Title:     title,
		Body:      body,
		Status:    "open",
		CreatedAt: createdAt,
	}
}

func newDoneTask(id, title, body, createdAt string) model.Task {
	t := newTask(id, title, body, createdAt)
	t.Status = "done"
	return t
}

// ids extracts the ordered list of task IDs from a result slice. Tests compare
// against this to assert stable ordering without caring about score values.
func ids(results []Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Task.ID
	}
	return out
}

// rank is a test helper that indexes tasks and ranks them in one step.
func rank(query string, tasks []model.Task, limit int) []Result {
	return NewIndex(tasks).Rank(query, limit)
}

// find returns the result carrying the given task ID, or nil.
func find(results []Result, id string) *Result {
	for i := range results {
		if results[i].Task.ID == id {
			return &results[i]
		}
	}
	return nil
}

func TestRank_EmptyQuery_ReturnsAllTasksByCreatedAtDesc(t *testing.T) {
	tasks := []model.Task{
		newTask("A", "alpha", "", "2026-04-01T10:00:00Z"),
		newTask("B", "beta", "", "2026-04-03T10:00:00Z"),
		newTask("C", "gamma", "", "2026-04-02T10:00:00Z"),
	}
	got := rank("", tasks, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	want := []string{"B", "C", "A"}
	if gotIDs := ids(got); !reflect.DeepEqual(gotIDs, want) {
		t.Errorf("order: got %v, want %v", gotIDs, want)
	}
	for _, r := range got {
		if r.TitleHit != nil {
			t.Errorf("empty query should not produce highlights, got TitleHit=%v", r.TitleHit)
		}
	}
}

func TestRank_TitleWeightBeatsBody(t *testing.T) {
	// Two tasks with the "same" content in different fields. The one with
	// the match in the title must sort first because title score is doubled.
	tasks := []model.Task{
		newTask("BODY", "unrelated words", "the word login appears here", "2026-04-01T10:00:00Z"),
		newTask("TITLE", "login flow", "unrelated body", "2026-04-01T10:00:00Z"),
	}
	got := rank("login", tasks, 0)
	if len(got) < 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].Task.ID != "TITLE" {
		t.Errorf("expected TITLE first, got %v", ids(got))
	}
}

func TestRank_CaseInsensitive(t *testing.T) {
	tasks := []model.Task{
		newTask("A", "Fix login bug", "", "2026-04-01T10:00:00Z"),
		newTask("B", "another task", "", "2026-04-01T10:00:00Z"),
	}
	got := rank("FIX", tasks, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(got), ids(got))
	}
	if got[0].Task.ID != "A" {
		t.Errorf("expected A, got %v", ids(got))
	}
}

func TestRank_NoMatches_ReturnsEmpty(t *testing.T) {
	tasks := []model.Task{
		newTask("A", "alpha", "beta", "2026-04-01T10:00:00Z"),
	}
	got := rank("zzzzz", tasks, 0)
	if got == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 results, got %d", len(got))
	}
}

func TestRank_TieBreakByCreatedAtDesc(t *testing.T) {
	// Identical titles guarantee equal scores so the tie-break kicks in.
	tasks := []model.Task{
		newTask("OLD", "fix login", "", "2026-04-01T10:00:00Z"),
		newTask("NEW", "fix login", "", "2026-04-05T10:00:00Z"),
		newTask("MID", "fix login", "", "2026-04-03T10:00:00Z"),
	}
	got := rank("fix", tasks, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	want := []string{"NEW", "MID", "OLD"}
	if gotIDs := ids(got); !reflect.DeepEqual(gotIDs, want) {
		t.Errorf("tie-break order: got %v, want %v", gotIDs, want)
	}
}

func TestRank_LimitTruncates(t *testing.T) {
	tasks := []model.Task{
		newTask("A", "fix one", "", "2026-04-01T10:00:00Z"),
		newTask("B", "fix two", "", "2026-04-02T10:00:00Z"),
		newTask("C", "fix three", "", "2026-04-03T10:00:00Z"),
		newTask("D", "fix four", "", "2026-04-04T10:00:00Z"),
	}
	got := rank("fix", tasks, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 (limit), got %d", len(got))
	}

	// empty query path also respects the limit
	got2 := rank("", tasks, 2)
	if len(got2) != 2 {
		t.Fatalf("empty query with limit: expected 2, got %d", len(got2))
	}

	// limit <= 0 means no truncation
	got3 := rank("fix", tasks, 0)
	if len(got3) != 4 {
		t.Fatalf("limit 0 should not truncate, got %d", len(got3))
	}
	got4 := rank("fix", tasks, -5)
	if len(got4) != 4 {
		t.Fatalf("negative limit should not truncate, got %d", len(got4))
	}
}

func TestRank_DoneTasksIncluded(t *testing.T) {
	// The ranker is status-agnostic: open/done filtering is the caller's job.
	tasks := []model.Task{
		newTask("OPEN", "fix open bug", "", "2026-04-01T10:00:00Z"),
		newDoneTask("DONE", "fix done bug", "", "2026-04-02T10:00:00Z"),
	}
	got := rank("fix", tasks, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if find(got, "DONE") == nil {
		t.Errorf("done task should appear in results, got %v", ids(got))
	}
}

func TestRank_MatchPositionsReturned(t *testing.T) {
	tasks := []model.Task{
		newTask("A", "fix login", "body content", "2026-04-01T10:00:00Z"),
		newTask("B", "unrelated", "login is here", "2026-04-01T10:00:00Z"),
	}
	got := rank("login", tasks, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	titleResult := find(got, "A")
	bodyResult := find(got, "B")
	if titleResult == nil || len(titleResult.TitleHit) == 0 {
		t.Fatalf("A should have title match positions, got %+v", titleResult)
	}
	// "login" in "fix login" starts at byte index 4. First matched position should be 4.
	if titleResult.TitleHit[0] != 4 {
		t.Errorf("A first TitleHit index: got %d, want 4", titleResult.TitleHit[0])
	}
	// B must not have title hits (title doesn't contain 'login'); body match is
	// scored but not tracked for rendering. A body-only hit carries nil TitleHit.
	if bodyResult == nil {
		t.Fatal("B should be in the results")
	}
	if bodyResult.TitleHit != nil {
		t.Errorf("body-only hit should carry nil TitleHit, got %v", bodyResult.TitleHit)
	}
}

// TestRank_TitleHitIsDefensivelyCopied guards the defensive copy of
// fuzzy.Match.MatchedIndexes. sahilm/fuzzy reuses that buffer across matches
// inside one Find call, so without the copy every earlier hit would end up
// showing the last match's offsets. Two titles that match at different byte
// offsets make the difference visible: the first must keep its own offsets.
func TestRank_TitleHitIsDefensivelyCopied(t *testing.T) {
	tasks := []model.Task{
		newTask("FIRST", "login page", "", "2026-04-02T10:00:00Z"),
		newTask("SECOND", "xxxxxlogin", "", "2026-04-01T10:00:00Z"),
	}
	got := rank("login", tasks, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	first := find(got, "FIRST")
	second := find(got, "SECOND")
	if first == nil || second == nil {
		t.Fatalf("both tasks should match, got %v", ids(got))
	}
	wantFirst := []int{0, 1, 2, 3, 4}
	if !reflect.DeepEqual(first.TitleHit, wantFirst) {
		t.Errorf("FIRST TitleHit: got %v, want %v (stale buffer from a later match?)", first.TitleHit, wantFirst)
	}
	wantSecond := []int{5, 6, 7, 8, 9}
	if !reflect.DeepEqual(second.TitleHit, wantSecond) {
		t.Errorf("SECOND TitleHit: got %v, want %v", second.TitleHit, wantSecond)
	}
}

// TestRank_MultibyteTitleMatch asserts the ranker and title hits work for
// titles containing multi-byte runes. "café" is 5 bytes (c=1, a=1, f=1, é=2) —
// matching query "é" must land on byte offset 3, which is where the "é" rune
// begins. An earlier implementation precomputed a lowercased copy and passed
// the original title to highlighting, which broke alignment for runes whose
// lowercase form changes byte length; this test guards against regressing to
// that class of bug.
func TestRank_MultibyteTitleMatch(t *testing.T) {
	tasks := []model.Task{
		newTask("CAFE", "café", "", "2026-04-01T10:00:00Z"),
	}
	got := rank("é", tasks, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0].TitleHit, []int{3}) {
		t.Errorf("TitleHit = %v, want [3] (byte offset of 'é' in 'café')", got[0].TitleHit)
	}
}

// TestRank_CaseInsensitiveMultibyte confirms case-insensitive matching still
// works across common multi-byte ranges (here, "Café" vs "café"), and that the
// returned byte offsets align with the original-case title so highlight
// rendering downstream stays stable.
func TestRank_CaseInsensitiveMultibyte(t *testing.T) {
	tasks := []model.Task{
		newTask("CAFE", "Café latte", "", "2026-04-01T10:00:00Z"),
	}
	// Lowercase query should match the mixed-case original title.
	got := rank("café", tasks, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 result for lowercase query, got %d", len(got))
	}
	// Offsets are byte offsets into the original title: C=0, a=1, f=2, é=3..4.
	if !reflect.DeepEqual(got[0].TitleHit, []int{0, 1, 2, 3}) {
		t.Errorf("TitleHit = %v, want [0 1 2 3]", got[0].TitleHit)
	}
}

func TestIndex_Len(t *testing.T) {
	tasks := []model.Task{
		newTask("A", "alpha", "", "2026-04-01T10:00:00Z"),
		newTask("B", "beta", "", "2026-04-02T10:00:00Z"),
	}
	if n := NewIndex(tasks).Len(); n != 2 {
		t.Errorf("Len() = %d, want 2", n)
	}
}

func TestNewIndex_Nil(t *testing.T) {
	ix := NewIndex(nil)
	if ix == nil {
		t.Fatal("NewIndex(nil) should return a usable index, got nil")
	}
	if n := ix.Len(); n != 0 {
		t.Errorf("Len() = %d, want 0", n)
	}
	got := ix.Rank("anything", 10)
	if got == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty results, got %d", len(got))
	}
}

// TestIndex_NilReceiver pins the nil contract: closeSearch in the TUI nils out
// the index, and callers still ask for Len/Rank afterwards.
func TestIndex_NilReceiver(t *testing.T) {
	var ix *Index
	if n := ix.Len(); n != 0 {
		t.Errorf("nil Len() = %d, want 0", n)
	}
	for _, q := range []string{"", "fix"} {
		got := ix.Rank(q, 10)
		if got == nil {
			t.Fatalf("nil Rank(%q) returned nil, want empty non-nil slice", q)
		}
		if len(got) != 0 {
			t.Errorf("nil Rank(%q) returned %d results, want 0", q, len(got))
		}
	}
}
