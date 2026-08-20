package search

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/maksmas/monolog/internal/model"
)

// Term-count ranking (Index.RankTerms). A one-word query goes straight to
// Rank; two or more words are ranked by how many of them a task actually
// contains. Rank's own behaviour is pinned in rank_test.go — these tests cover
// only the term-count rule and the delegation boundary between the two.

// rankTerms indexes tasks and term-ranks them in one step, mirroring the rank
// helper in rank_test.go.
func rankTerms(query string, tasks []model.Task, limit int) []Result {
	return NewIndex(tasks).RankTerms(query, limit)
}

func containsID(results []Result, id string) bool {
	return find(results, id) != nil
}

// TestRankTerms_OrderIndependentMembership is the recall half of the contract:
// reversing the words of a query must not change which tasks come back.
// sahilm/fuzzy matches the query as one ordered subsequence including its
// spaces, so without term counting "telegram week" silently misses the task
// "week ... telegram" — and the skill's dedupe step files a duplicate on the
// strength of it.
func TestRankTerms_OrderIndependentMembership(t *testing.T) {
	const target = "01TARGET0000000000000000AA"
	tasks := []model.Task{
		newTask(target, "week command from telegram should display today's tasks", "", "2026-01-01T00:00:00Z"),
		newTask("01DECOY00000000000000000BB", "telegram bot: acknowledge weekly digest", "", "2026-01-02T00:00:00Z"),
	}

	for _, query := range []string{"week telegram", "telegram week"} {
		got := rankTerms(query, tasks, 10)
		if !containsID(got, target) {
			t.Errorf("RankTerms(%q) = %v, want it to include the target %q", query, ids(got), target)
		}
	}
}

// TestRankTerms_ReversedQueryProducesIdenticalOrder is the stronger half, and
// the reason the tie-break chain contains no whole-phrase fuzzy score.
//
// README.md and SKILL.md both promise that `search telegram week` and `search
// week telegram` return the same rows *in the same order*. Membership is
// order-independent for free; ordering is not. Both tasks below hit both terms
// in their titles, so every earlier sort key ties and only the last one
// decides. A phrase-score tie-break would rank whichever title happens to
// contain the query as an ordered subsequence, flipping the two — and because
// the limit is applied after sorting, that flip changes the printed ROW SET
// too, not just its order. Hence the assertion at --limit 1 as well.
func TestRankTerms_ReversedQueryProducesIdenticalOrder(t *testing.T) {
	tasks := []model.Task{
		newTask("01FIRSTORDER", "Telegram bot needs week bucket support", "", "2026-01-01T00:00:00Z"),
		newTask("01REVERSED00", "Week command from telegram is broken", "", "2026-01-02T00:00:00Z"),
	}

	// Guard the fixture: unless every key above CreatedAt ties, the assertion
	// would pass for a reason other than the one it is testing.
	forward := rankTerms("telegram week", tasks, 0)
	if len(forward) != 2 {
		t.Fatalf("both tasks should hit both terms, got %v", ids(forward))
	}

	for _, limit := range []int{0, 2, 1} {
		want := ids(rankTerms("telegram week", tasks, limit))
		got := ids(rankTerms("week telegram", tasks, limit))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("limit %d: reversing the query changed the result from %v to %v", limit, want, got)
		}
	}

	// And the order really is CreatedAt-descending, so the test would notice a
	// tie-break that merely happens to be symmetric.
	if want := []string{"01REVERSED00", "01FIRSTORDER"}; !reflect.DeepEqual(ids(forward), want) {
		t.Errorf("order = %v, want %v (newest first once term and title hits tie)", ids(forward), want)
	}
}

// TestRankTerms_ExtraWhitespaceDoesNotChangeRanking closes the same hole from
// the other side: the query is tokenized with strings.Fields, so runs of
// whitespace collapse and the separator between two words cannot matter.
//
// The fixture is built so that it would matter to a whole-phrase score. One
// title carries "alpha beta" as an in-order subsequence and the other does not,
// but neither matches "alpha  beta" (a fuzzy match needs both spaces, in
// order) — so a phrase tie-break would order the pair one way for the
// single-spaced query and the other way for the double-spaced one.
func TestRankTerms_ExtraWhitespaceDoesNotChangeRanking(t *testing.T) {
	tasks := []model.Task{
		newTask("01INORDER", "xx alpha beta xx", "", "2026-01-01T00:00:00Z"),
		newTask("01REORDER", "beta and alpha", "", "2026-01-02T00:00:00Z"),
	}

	// Guard the fixture: the two spellings must genuinely score differently as
	// phrases, or this test has nothing to catch.
	single, double := ids(rank("alpha beta", tasks, 0)), ids(rank("alpha  beta", tasks, 0))
	if reflect.DeepEqual(single, double) {
		t.Fatalf("fixture is not discriminating: phrase ranking is %v for both spellings", single)
	}

	want := ids(rankTerms("alpha beta", tasks, 0))
	if len(want) != 2 {
		t.Fatalf("both tasks should hit both terms, got %v", want)
	}
	for _, query := range []string{"alpha  beta", "  alpha beta  ", "alpha\tbeta", "alpha \t beta"} {
		if got := ids(rankTerms(query, tasks, 0)); !reflect.DeepEqual(got, want) {
			t.Errorf("RankTerms(%q) = %v, want %v", query, got, want)
		}
	}
}

// subsequenceNoise is a title containing neither "telegram" nor "week" as a
// word, yet carrying t-e-l-e-g-r-a-m-<space>-w-e-e-k as an ordered
// subsequence — which is all sahilm/fuzzy asks for. This is the exact shape of
// the spurious rows the real backlog returned for `search "telegram week"`:
// nine of the ten hits contained neither word, and one of them led the list.
const subsequenceNoise = "t e l e g r a m w e e k"

// TestRankTerms_ExcludesZeroTermHits is the precision floor, and the reason
// this ranking rule exists. A task containing none of the query words must not
// appear at all, however well it scores as a character subsequence.
func TestRankTerms_ExcludesZeroTermHits(t *testing.T) {
	const (
		hit   = "01HIT0000000000000000000AA"
		noise = "01NOISE00000000000000000BB"
	)
	tasks := []model.Task{
		newTask(hit, "week command from telegram should display today's tasks", "", "2026-01-01T00:00:00Z"),
		newTask(noise, subsequenceNoise, "", "2026-01-02T00:00:00Z"),
	}

	// Guard the fixture: unless the noise task really is a fuzzy match, the
	// exclusion below proves nothing.
	if !containsID(rank("telegram week", tasks, 0), noise) {
		t.Fatalf("fixture is not discriminating: %q must fuzzy-match the phrase", subsequenceNoise)
	}

	got := rankTerms("telegram week", tasks, 10)
	if containsID(got, noise) {
		t.Errorf("task containing neither query word was returned: %v", ids(got))
	}
	if !containsID(got, hit) {
		t.Errorf("the real match is missing from %v", ids(got))
	}
}

// TestRankTerms_MoreTermsRankHigher pins the primary sort key. This is a
// ranking rule with a floor, not an AND filter: a task matching two of three
// words still shows up, just below one matching all three.
//
// The 3-of-3 task deliberately hits two of its terms in the body only, so it
// has FEWER title hits and an older CreatedAt than the 2-of-3 task. Every
// lower tie-break therefore argues for the wrong order, and only the term-hit
// count produces the expected one — without that the fixture would pass on
// title weighting alone and prove nothing.
func TestRankTerms_MoreTermsRankHigher(t *testing.T) {
	const (
		all = "01ALL"
		two = "01TWO"
	)
	tasks := []model.Task{
		// 2 terms, both in the title; newer.
		newTask(two, "pagination bug in the exporter", "", "2026-01-02T00:00:00Z"),
		// 3 terms, only one of them in the title; older.
		newTask(all, "pagination in the bot", "telegram bug", "2026-01-01T00:00:00Z"),
	}

	got := rankTerms("telegram pagination bug", tasks, 10)
	if want := []string{all, two}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("order = %v, want %v (3-of-3 above 2-of-3, and 2-of-3 still present)", ids(got), want)
	}
}

// TestRankTerms_TitleHitsOutrankBodyOnly pins the second sort key. The
// body-only task is the NEWER of the two, so the CreatedAt tie-break below it
// argues for the opposite order: drop the title-hit comparison and this fails.
func TestRankTerms_TitleHitsOutrankBodyOnly(t *testing.T) {
	const (
		title = "01TITLE"
		body  = "01BODY"
	)
	tasks := []model.Task{
		newTask(title, "weekly telegram note", "", "2026-01-01T00:00:00Z"),
		newTask(body, "quarterly planning", "telegram week", "2026-01-02T00:00:00Z"),
	}

	got := rankTerms("telegram week", tasks, 10)
	if want := []string{title, body}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("order = %v, want %v (title hits before body-only hits)", ids(got), want)
	}
}

// TestRankTerms_NoPhraseTieBreak is the direct guard against reintroducing the
// whole-phrase score. Both tasks hit both terms in their titles; one contains
// the query verbatim and in order, the other reversed. The in-order one is
// OLDER, so a phrase tie-break would put it first and CreatedAt puts it last.
func TestRankTerms_NoPhraseTieBreak(t *testing.T) {
	const (
		inOrder  = "01PHRASE"
		anyOrder = "01REVERSED"
	)
	tasks := []model.Task{
		newTask(anyOrder, "beta alpha", "", "2026-01-02T00:00:00Z"),
		newTask(inOrder, "xx alpha xx beta xx", "", "2026-01-01T00:00:00Z"),
	}

	// Guard the fixture: the in-order title must genuinely out-score the
	// reversed one as a phrase, or there is no tie-break to have removed.
	scores := map[string]int{}
	for _, r := range rank("alpha beta", tasks, 0) {
		scores[r.Task.ID] = r.Score
	}
	if scores[inOrder] <= scores[anyOrder] {
		t.Fatalf("fixture is not discriminating: phrase scores %v must favour the in-order title", scores)
	}

	got := rankTerms("alpha beta", tasks, 10)
	if want := []string{anyOrder, inOrder}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("order = %v, want %v (newest first; the phrase score must not break the tie)", ids(got), want)
	}
}

// TestRankTerms_ResultsCarryNoScoreOrTitleHit pins the Result contract stated
// on the type: term ranking is not a fuzzy match, so it fabricates neither a
// score nor highlight offsets.
func TestRankTerms_ResultsCarryNoScoreOrTitleHit(t *testing.T) {
	tasks := []model.Task{
		newTask("01A", "alpha beta gamma", "", "2026-01-01T00:00:00Z"),
		newTask("01B", "beta only", "alpha", "2026-01-02T00:00:00Z"),
	}

	got := rankTerms("alpha beta", tasks, 10)
	if len(got) != 2 {
		t.Fatalf("fixture should produce 2 results, got %v", ids(got))
	}
	for _, r := range got {
		if r.Score != 0 {
			t.Errorf("result for %s has Score %d, want 0", r.Task.ID, r.Score)
		}
		if r.TitleHit != nil {
			t.Errorf("result for %s has TitleHit %v, want nil", r.Task.ID, r.TitleHit)
		}
	}
}

// TestRankTerms_SingleTokenMatchesRankExactly pins that the term-counting path
// is multi-word only. A one-word query must go straight through to Rank, byte
// for byte, since that is what keeps single-token CLI output in lockstep with
// the TUI overlay.
func TestRankTerms_SingleTokenMatchesRankExactly(t *testing.T) {
	tasks := []model.Task{
		newTask("01A", "Repair broken pagination", "", "2026-01-01T00:00:00Z"),
		newTask("01B", "Repave the parking lot", "", "2026-01-02T00:00:00Z"),
		newTask("01C", "Unrelated grocery run", "", "2026-01-03T00:00:00Z"),
	}

	want := rank("rep", tasks, 10)
	if len(want) < 2 {
		t.Fatalf("fixture must produce at least 2 hits, got %d", len(want))
	}
	if got := rankTerms("rep", tasks, 10); !reflect.DeepEqual(got, want) {
		t.Errorf("RankTerms single token = %+v, want Rank's exact result %+v", got, want)
	}

	// Surrounding whitespace is still one token, not two.
	if got := rankTerms("  rep  ", tasks, 10); !reflect.DeepEqual(got, want) {
		t.Errorf("padded single token = %+v, want %+v", got, want)
	}
}

// TestRankTerms_BlankQueryFallsBackToRank covers the branch where the query
// tokenizes to nothing. `monolog search` rejects a blank query before it gets
// here, but RankTerms is exported and must not invent a second meaning for it:
// it defers to Rank, whose empty query means "every task by CreatedAt desc".
func TestRankTerms_BlankQueryFallsBackToRank(t *testing.T) {
	tasks := []model.Task{
		newTask("01A", "alpha", "", "2026-01-01T00:00:00Z"),
		newTask("01B", "beta", "", "2026-01-02T00:00:00Z"),
	}

	for _, query := range []string{"", "   ", "\t\n"} {
		want := rank(query, tasks, 10)
		if got := rankTerms(query, tasks, 10); !reflect.DeepEqual(got, want) {
			t.Errorf("RankTerms(%q) = %v, want Rank's result %v", query, ids(got), ids(want))
		}
	}
}

// TestRankTerms_DeduplicatesByTaskID guards the one-pass-over-the-index shape:
// a task hitting several query terms must be returned once. Counting it per
// term would fill the top of a dedupe-critical result set with copies of the
// same task, hiding the other candidates behind the limit.
func TestRankTerms_DeduplicatesByTaskID(t *testing.T) {
	tasks := []model.Task{
		newTask("01A", "alpha beta gamma", "", "2026-01-01T00:00:00Z"),
		newTask("01B", "beta only", "", "2026-01-02T00:00:00Z"),
	}

	got := rankTerms("alpha beta", tasks, 10)

	counts := map[string]int{}
	for _, r := range got {
		counts[r.Task.ID]++
	}
	for id, n := range counts {
		if n != 1 {
			t.Errorf("task %s appears %d times in %v, want exactly 1", id, n, ids(got))
		}
	}
	if !containsID(got, "01A") {
		t.Errorf("two-term match 01A missing from %v", ids(got))
	}
	if !containsID(got, "01B") {
		t.Errorf("one-term match 01B missing from %v", ids(got))
	}
}

// TestRankTerms_RepeatedWordCountsOnce pins the "distinct terms" wording: both
// tasks below hit exactly one distinct term, so the CreatedAt tie-break
// decides and the newer one leads. Counting the repeat would give the alpha
// task two hits and wrongly put it on top.
func TestRankTerms_RepeatedWordCountsOnce(t *testing.T) {
	const (
		alpha = "01ALPHA"
		beta  = "01BETA"
	)
	tasks := []model.Task{
		newTask(alpha, "alpha only", "", "2026-01-01T00:00:00Z"),
		newTask(beta, "beta gamma", "", "2026-01-02T00:00:00Z"),
	}

	got := rankTerms("alpha alpha beta", tasks, 10)
	if want := []string{beta, alpha}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("order = %v, want %v (the repeated word must count once)", ids(got), want)
	}
}

// TestRankTerms_RespectsLimit pins that the limit is applied after ranking,
// and that limit <= 0 keeps Rank's "no truncation" meaning — `monolog search`
// clamps before calling, but the method must not invent a cap of its own.
func TestRankTerms_RespectsLimit(t *testing.T) {
	var tasks []model.Task
	for i := 0; i < 8; i++ {
		tasks = append(tasks, newTask(
			fmt.Sprintf("01%02d", i),
			fmt.Sprintf("alpha entry %02d", i),
			"",
			fmt.Sprintf("2026-01-%02dT00:00:00Z", i+1),
		))
	}
	tasks = append(tasks, newTask("01P", "alpha beta phrase", "", "2026-02-01T00:00:00Z"))

	if got := rankTerms("alpha beta", tasks, 3); len(got) != 3 {
		t.Errorf("RankTerms with limit 3 returned %d results (%v), want 3", len(got), ids(got))
	}
	if got := rankTerms("alpha beta", tasks, 0); len(got) != len(tasks) {
		t.Errorf("RankTerms with limit 0 returned %d results, want every match (%d)", len(got), len(tasks))
	}
}

// TestRankTerms_SkipsSingleCharacterTokens pins minTermRunes. A one-character
// term is a substring of nearly every sentence, so counting it would turn any
// query containing "a" or "I" into a backlog dump.
func TestRankTerms_SkipsSingleCharacterTokens(t *testing.T) {
	tasks := []model.Task{
		newTask("01A", "zeta report", "", "2026-01-01T00:00:00Z"),
		newTask("01B", "an unrelated task", "", "2026-01-02T00:00:00Z"),
	}

	got := rankTerms("a zeta", tasks, 10)
	if containsID(got, "01B") {
		t.Errorf("single-character token was counted as a term: %v", ids(got))
	}
	if !containsID(got, "01A") {
		t.Errorf("the real term hit is missing from %v", ids(got))
	}
}

// TestRankTerms_AllShortTokensFallsBackToRank covers the branch where every
// word is too short to count: there is nothing to rank on, so the whole-phrase
// ranking stands rather than the query reporting no matches.
func TestRankTerms_AllShortTokensFallsBackToRank(t *testing.T) {
	tasks := []model.Task{
		newTask("01A", "alpha beta", "", "2026-01-01T00:00:00Z"),
		newTask("01B", "an unrelated task", "", "2026-01-02T00:00:00Z"),
	}

	want := rank("a b", tasks, 10)
	if len(want) == 0 {
		t.Fatal("fixture must fuzzy-match the phrase for the fallback to be observable")
	}
	if got := rankTerms("a b", tasks, 10); !reflect.DeepEqual(got, want) {
		t.Errorf("all-short-token query = %v, want the phrase ranking %v", ids(got), ids(want))
	}
}

// TestRankTerms_NilReceiver extends the nil-receiver contract Len and Rank
// already honour: a nil *Index must rank as an empty, non-nil result set
// rather than panicking.
func TestRankTerms_NilReceiver(t *testing.T) {
	var ix *Index
	got := ix.RankTerms("alpha beta", 10)
	if got == nil {
		t.Error("RankTerms on a nil *Index returned nil, want an empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("RankTerms on a nil *Index returned %d results, want 0", len(got))
	}
}
