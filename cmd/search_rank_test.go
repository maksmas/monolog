package cmd

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/search"
)

// Multi-word ranking (cmd.rankQuery). A one-word query goes straight to the
// shared ranker; two or more words are ranked by how many of them a task
// actually contains. The behaviour of internal/search.Rank itself is pinned in
// internal/search/rank_test.go — these tests only cover the CLI-side rule.

func rankedIDs(results []search.Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Task.ID
	}
	return out
}

func containsID(results []search.Result, id string) bool {
	for _, r := range results {
		if r.Task.ID == id {
			return true
		}
	}
	return false
}

// TestRankQuery_MultiWordIsOrderIndependent is the recall half of the
// contract: reversing the words of a query must not change which tasks come
// back. sahilm/fuzzy matches the query as one ordered subsequence including
// its spaces, so without term counting "telegram week" silently misses the
// task "week ... telegram" — and the skill's dedupe step files a duplicate on
// the strength of it.
func TestRankQuery_MultiWordIsOrderIndependent(t *testing.T) {
	const target = "01TARGET0000000000000000AA"
	tasks := []model.Task{
		{ID: target, Title: "week command from telegram should display today's tasks",
			CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01DECOY00000000000000000BB", Title: "telegram bot: acknowledge weekly digest",
			CreatedAt: "2026-01-02T00:00:00Z"},
	}

	for _, query := range []string{"week telegram", "telegram week"} {
		got := rankQuery(tasks, query, 10)
		if !containsID(got, target) {
			t.Errorf("rankQuery(%q) = %v, want it to include the target %q",
				query, rankedIDs(got), target)
		}
	}
}

// TestSearchCommand_MultiWordIsOrderIndependent is the same guard end to end,
// through the cobra command and the real store, so a future refactor that
// bypasses rankQuery in RunE is caught too.
func TestSearchCommand_MultiWordIsOrderIndependent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	const targetTitle = "week command from telegram should display today's tasks"
	addTask(t, targetTitle)
	addTask(t, "telegram bot: acknowledge weekly digest")

	for _, query := range []string{"week telegram", "telegram week"} {
		output := runSearch(t, query)
		if !strings.Contains(output, targetTitle) {
			t.Errorf("search %q should find %q regardless of word order, got:\n%s",
				query, targetTitle, output)
		}
	}
}

// subsequenceNoise is a title containing neither "telegram" nor "week" as a
// word, yet carrying t-e-l-e-g-r-a-m-<space>-w-e-e-k as an ordered
// subsequence — which is all sahilm/fuzzy asks for. This is the exact shape of
// the spurious rows the real backlog returned for `search "telegram week"`:
// nine of the ten hits contained neither word, and one of them led the list.
const subsequenceNoise = "t e l e g r a m w e e k"

// TestRankQuery_ExcludesZeroTermHits is the precision floor, and the reason
// this ranking rule exists. A task containing none of the query words must not
// appear at all, however well it scores as a character subsequence.
func TestRankQuery_ExcludesZeroTermHits(t *testing.T) {
	const (
		hit   = "01HIT0000000000000000000AA"
		noise = "01NOISE00000000000000000BB"
	)
	tasks := []model.Task{
		{ID: hit, Title: "week command from telegram should display today's tasks",
			CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: noise, Title: subsequenceNoise, CreatedAt: "2026-01-02T00:00:00Z"},
	}

	// Guard the fixture: unless the noise task really is a fuzzy match, the
	// exclusion below proves nothing.
	if !containsID(search.NewIndex(tasks).Rank("telegram week", 0), noise) {
		t.Fatalf("fixture is not discriminating: %q must fuzzy-match the phrase", subsequenceNoise)
	}

	got := rankQuery(tasks, "telegram week", 10)
	if containsID(got, noise) {
		t.Errorf("task containing neither query word was returned: %v", rankedIDs(got))
	}
	if !containsID(got, hit) {
		t.Errorf("the real match is missing from %v", rankedIDs(got))
	}
}

// TestSearchCommand_MultiWordPrecision is the reproduction case end to end:
// the real backlog answered `search "telegram week"` with ten rows, only one
// of which contained either word, and led with one that contained neither.
func TestSearchCommand_MultiWordPrecision(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	const targetTitle = "week command from telegram should display tasks for today as well"
	addTask(t, subsequenceNoise)
	addTask(t, targetTitle)
	addTask(t, "home: pay valge rent")

	for _, query := range []string{"telegram week", "week telegram"} {
		rows := resultLines(runSearch(t, query))
		if len(rows) != 1 {
			t.Fatalf("search %q returned %d rows, want only the one real match:\n%s",
				query, len(rows), strings.Join(rows, "\n"))
		}
		if !strings.Contains(rows[0], targetTitle) {
			t.Errorf("search %q led with %q, want the row for %q", query, rows[0], targetTitle)
		}
	}
}

// TestSearchCommand_MultiWordNoTermHits pins that the zero-hit floor reports
// nothing rather than falling back to subsequence noise.
func TestSearchCommand_MultiWordNoTermHits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, subsequenceNoise)
	addTask(t, "home: pay valge rent")

	output := runSearch(t, "telegram week")
	if !strings.Contains(output, "No matches.") {
		t.Errorf("a query whose words appear in no task should print 'No matches.', got:\n%s", output)
	}
}

// TestRankQuery_MoreTermsRankHigher pins the primary sort key. This is a
// ranking rule with a floor, not an AND filter: a task matching two of three
// words still shows up, just below one matching all three.
//
// The 3-of-3 task deliberately hits two of its terms in the body only, so it
// has FEWER title hits and an older CreatedAt than the 2-of-3 task. Every
// lower tie-break therefore argues for the wrong order, and only the term-hit
// count produces the expected one — without that the fixture would pass on
// title weighting alone and prove nothing.
func TestRankQuery_MoreTermsRankHigher(t *testing.T) {
	const (
		all = "01ALL"
		two = "01TWO"
	)
	tasks := []model.Task{
		// 2 terms, both in the title; newer.
		{ID: two, Title: "pagination bug in the exporter", CreatedAt: "2026-01-02T00:00:00Z"},
		// 3 terms, only one of them in the title; older.
		{ID: all, Title: "pagination in the bot", Body: "telegram bug",
			CreatedAt: "2026-01-01T00:00:00Z"},
	}

	got := rankQuery(tasks, "telegram pagination bug", 10)
	if want := []string{all, two}; !reflect.DeepEqual(rankedIDs(got), want) {
		t.Errorf("order = %v, want %v (3-of-3 above 2-of-3, and 2-of-3 still present)",
			rankedIDs(got), want)
	}
}

// TestRankQuery_TitleHitsOutrankBodyOnly pins the second sort key. The fixture
// is built so the *body* match scores higher as a phrase, which is what makes
// the assertion about title hits rather than about fuzzy score: drop the
// title-hit comparison and the body-only task leads.
func TestRankQuery_TitleHitsOutrankBodyOnly(t *testing.T) {
	const (
		title = "01TITLE"
		body  = "01BODY"
	)
	tasks := []model.Task{
		// "telegram week" is not an ordered subsequence of this title (nothing
		// after "telegram" starts a "week"), so its phrase score is 0 — yet
		// both query words are present as substrings.
		{ID: title, Title: "weekly telegram note", CreatedAt: "2026-01-01T00:00:00Z"},
		// Phrase match in the body, so this one carries a positive score.
		{ID: body, Title: "quarterly planning", Body: "telegram week",
			CreatedAt: "2026-01-02T00:00:00Z"},
	}

	// Guard the fixture: the body-only task must out-score the title task on
	// the phrase, or the title-hit rule is not what the order is proving.
	scores := map[string]int{}
	for _, r := range search.NewIndex(tasks).Rank("telegram week", 0) {
		scores[r.Task.ID] = r.Score
	}
	if scores[body] <= scores[title] {
		t.Fatalf("fixture is not discriminating: phrase scores %v must favour the body-only task", scores)
	}

	got := rankQuery(tasks, "telegram week", 10)
	if want := []string{title, body}; !reflect.DeepEqual(rankedIDs(got), want) {
		t.Errorf("order = %v, want %v (title hits before body-only hits)", rankedIDs(got), want)
	}
}

// TestRankQuery_PhraseScoreBreaksTermCountTies pins the third sort key: with
// the same words hit in the same field, an in-order phrase match is the
// stronger signal and leads.
func TestRankQuery_PhraseScoreBreaksTermCountTies(t *testing.T) {
	const (
		inOrder  = "01PHRASE"
		anyOrder = "01REVERSED"
	)
	tasks := []model.Task{
		// Newer, and word-reversed, so the CreatedAt tie-break alone would put
		// it on top.
		{ID: anyOrder, Title: "beta alpha", CreatedAt: "2026-01-02T00:00:00Z"},
		{ID: inOrder, Title: "xx alpha xx beta xx", CreatedAt: "2026-01-01T00:00:00Z"},
	}

	got := rankQuery(tasks, "alpha beta", 10)
	if want := []string{inOrder, anyOrder}; !reflect.DeepEqual(rankedIDs(got), want) {
		t.Errorf("order = %v, want %v (phrase hit breaks the tie)", rankedIDs(got), want)
	}
}

// TestRankQuery_SingleTokenMatchesRankExactly pins that the term-counting path
// is multi-word only. A one-word query must go straight through to the shared
// ranker, byte for byte, since that is what keeps single-token CLI output in
// lockstep with the TUI overlay.
func TestRankQuery_SingleTokenMatchesRankExactly(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "Repair broken pagination", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01B", Title: "Repave the parking lot", CreatedAt: "2026-01-02T00:00:00Z"},
		{ID: "01C", Title: "Unrelated grocery run", CreatedAt: "2026-01-03T00:00:00Z"},
	}

	want := search.NewIndex(tasks).Rank("rep", 10)
	if len(want) < 2 {
		t.Fatalf("fixture must produce at least 2 hits, got %d", len(want))
	}
	got := rankQuery(tasks, "rep", 10)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rankQuery single token = %+v, want Rank's exact result %+v", got, want)
	}

	// Surrounding whitespace is still one token, not two.
	if got := rankQuery(tasks, "  rep  ", 10); !reflect.DeepEqual(got, want) {
		t.Errorf("padded single token = %+v, want %+v", got, want)
	}
}

// TestRankQuery_DeduplicatesByTaskID guards the one-pass-over-tasks shape: a
// task hitting several query terms must be printed once. Counting it per term
// would fill the top of a dedupe-critical result set with copies of the same
// task, hiding the other candidates behind the limit.
func TestRankQuery_DeduplicatesByTaskID(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "alpha beta gamma", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01B", Title: "beta only", CreatedAt: "2026-01-02T00:00:00Z"},
	}

	// 01A hits both terms and also matches the phrase; 01B hits one.
	got := rankQuery(tasks, "alpha beta", 10)

	counts := map[string]int{}
	for _, r := range got {
		counts[r.Task.ID]++
	}
	for id, n := range counts {
		if n != 1 {
			t.Errorf("task %s appears %d times in %v, want exactly 1", id, n, rankedIDs(got))
		}
	}
	if !containsID(got, "01A") {
		t.Errorf("two-term match 01A missing from %v", rankedIDs(got))
	}
	if !containsID(got, "01B") {
		t.Errorf("one-term match 01B missing from %v", rankedIDs(got))
	}
}

// TestRankQuery_RepeatedWordCountsOnce pins the "distinct terms" wording: both
// tasks below hit exactly one distinct term, so the CreatedAt tie-break
// decides and the newer one leads. Counting the repeat would give the alpha
// task two hits and wrongly put it on top.
func TestRankQuery_RepeatedWordCountsOnce(t *testing.T) {
	const (
		alpha = "01ALPHA"
		beta  = "01BETA"
	)
	tasks := []model.Task{
		{ID: alpha, Title: "alpha only", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: beta, Title: "beta gamma", CreatedAt: "2026-01-02T00:00:00Z"},
	}

	got := rankQuery(tasks, "alpha alpha beta", 10)
	if want := []string{beta, alpha}; !reflect.DeepEqual(rankedIDs(got), want) {
		t.Errorf("order = %v, want %v (the repeated word must count once)", rankedIDs(got), want)
	}
}

// TestRankQuery_RespectsLimit pins that the limit is applied after ranking,
// and that limit <= 0 keeps Rank's "no truncation" meaning — the command
// clamps before calling, but the helper must not invent a cap of its own.
func TestRankQuery_RespectsLimit(t *testing.T) {
	var tasks []model.Task
	for i := 0; i < 8; i++ {
		tasks = append(tasks, model.Task{
			ID:        fmt.Sprintf("01%02d", i),
			Title:     fmt.Sprintf("alpha entry %02d", i),
			CreatedAt: fmt.Sprintf("2026-01-%02dT00:00:00Z", i+1),
		})
	}
	tasks = append(tasks, model.Task{ID: "01P", Title: "alpha beta phrase", CreatedAt: "2026-02-01T00:00:00Z"})

	if got := rankQuery(tasks, "alpha beta", 3); len(got) != 3 {
		t.Errorf("rankQuery with limit 3 returned %d results (%v), want 3", len(got), rankedIDs(got))
	}
	if got := rankQuery(tasks, "alpha beta", 0); len(got) != len(tasks) {
		t.Errorf("rankQuery with limit 0 returned %d results, want every match (%d)", len(got), len(tasks))
	}
}

// TestRankQuery_SkipsSingleCharacterTokens pins minTermRunes. A one-character
// term is a substring of nearly every sentence, so counting it would turn any
// query containing "a" or "I" into a backlog dump.
func TestRankQuery_SkipsSingleCharacterTokens(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "zeta report", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01B", Title: "an unrelated task", CreatedAt: "2026-01-02T00:00:00Z"},
	}

	got := rankQuery(tasks, "a zeta", 10)
	if containsID(got, "01B") {
		t.Errorf("single-character token was counted as a term: %v", rankedIDs(got))
	}
	if !containsID(got, "01A") {
		t.Errorf("the real term hit is missing from %v", rankedIDs(got))
	}
}

// TestRankQuery_AllShortTokensFallsBackToPhrase covers the branch where every
// word is too short to count: there is nothing to rank on, so the phrase
// ranking stands rather than the query reporting no matches.
func TestRankQuery_AllShortTokensFallsBackToPhrase(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "alpha beta", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01B", Title: "an unrelated task", CreatedAt: "2026-01-02T00:00:00Z"},
	}

	want := search.NewIndex(tasks).Rank("a b", 10)
	if len(want) == 0 {
		t.Fatal("fixture must fuzzy-match the phrase for the fallback to be observable")
	}
	if got := rankQuery(tasks, "a b", 10); !reflect.DeepEqual(got, want) {
		t.Errorf("all-short-token query = %v, want the phrase ranking %v", rankedIDs(got), rankedIDs(want))
	}
}
