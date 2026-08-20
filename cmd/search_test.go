package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/search"
	"github.com/maksmas/monolog/internal/store"
)

// runSearch executes `monolog search <args...>` against the current test repo
// and returns the combined output.
func runSearch(t *testing.T, args ...string) string {
	t.Helper()
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(append([]string{"search"}, args...))
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("search %v error = %v\noutput: %s", args, err, buf.String())
	}
	return buf.String()
}

// resultLines splits search output into non-empty lines, one per hit.
func resultLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestSearchCommand_MatchesTitle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Fix the login bug")
	addTask(t, "Water the plants")

	output := runSearch(t, "login")
	if !strings.Contains(output, "Fix the login bug") {
		t.Errorf("search login should match the title, got:\n%s", output)
	}
	if strings.Contains(output, "Water the plants") {
		t.Errorf("search login should not match unrelated task, got:\n%s", output)
	}
}

func TestSearchCommand_MatchesBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Unrelated heading", "--body", "the postgres connection pool leaks")
	addTask(t, "Water the plants")

	output := runSearch(t, "postgres")
	if !strings.Contains(output, "Unrelated heading") {
		t.Errorf("search should match on body text, got:\n%s", output)
	}
	if strings.Contains(output, "Water the plants") {
		t.Errorf("search should not match unrelated task, got:\n%s", output)
	}
}

// TestSearchCommand_TitleOutranksBody pins the titleWeight = 2 semantics end to
// end: a title hit must sort above a task that only matches in its body.
func TestSearchCommand_TitleOutranksBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Body only match", "--body", "deploy pipeline is flaky")
	addTask(t, "Deploy pipeline rewrite")

	output := runSearch(t, "deploy")
	titleIdx := strings.Index(output, "Deploy pipeline rewrite")
	bodyIdx := strings.Index(output, "Body only match")
	if titleIdx == -1 || bodyIdx == -1 {
		t.Fatalf("both tasks should match, got:\n%s", output)
	}
	if titleIdx > bodyIdx {
		t.Errorf("title hit should outrank body-only hit, got:\n%s", output)
	}
}

// TestSearchCommand_MultiWordUnquoted verifies MinimumNArgs(1) + strings.Join:
// `monolog search fix login` must behave like `monolog search "fix login"`.
func TestSearchCommand_MultiWordUnquoted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Fix the login bug")
	addTask(t, "Water the plants")

	output := runSearch(t, "fix", "login")
	if !strings.Contains(output, "Fix the login bug") {
		t.Errorf("unquoted multi-word query should match, got:\n%s", output)
	}
	if strings.Contains(output, "Water the plants") {
		t.Errorf("unquoted multi-word query should not match unrelated task, got:\n%s", output)
	}
}

func TestSearchCommand_LimitTruncates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	for i := 0; i < 6; i++ {
		addTask(t, fmt.Sprintf("Sample entry %02d", i))
	}

	output := runSearch(t, "sample", "--limit", "3")
	if got := len(resultLines(output)); got != 3 {
		t.Errorf("--limit 3 should print 3 result lines, got %d:\n%s", got, output)
	}
}

// TestSearchCommand_ZeroLimitFallsBackToDefault guards the clamp: the ranker
// treats limit <= 0 as "no truncation", so -n 0 must be rewritten to the
// default rather than dumping the whole backlog.
func TestSearchCommand_ZeroLimitFallsBackToDefault(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	const total = defaultSearchLimit + 5
	for i := 0; i < total; i++ {
		addTask(t, fmt.Sprintf("Sample entry %02d", i))
	}

	output := runSearch(t, "sample", "-n", "0")
	if got := len(resultLines(output)); got != defaultSearchLimit {
		t.Errorf("-n 0 should fall back to %d results, got %d:\n%s", defaultSearchLimit, got, output)
	}

	// Negative values clamp the same way.
	output = runSearch(t, "sample", "-n", "-4")
	if got := len(resultLines(output)); got != defaultSearchLimit {
		t.Errorf("-n -4 should fall back to %d results, got %d:\n%s", defaultSearchLimit, got, output)
	}
}

func TestSearchCommand_DefaultExcludesDone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTestTask(t, dir, "Archive the quarterly report")
	doneID := addTestTask(t, dir, "Archive the monthly report")

	rc := NewRootCmd()
	rc.SetOut(new(bytes.Buffer))
	rc.SetErr(new(bytes.Buffer))
	rc.SetArgs([]string{"done", doneID})
	if err := rc.Execute(); err != nil {
		t.Fatalf("done %s: %v", doneID, err)
	}

	output := runSearch(t, "archive")
	if !strings.Contains(output, "Archive the quarterly report") {
		t.Errorf("default search should show the open task, got:\n%s", output)
	}
	if strings.Contains(output, "Archive the monthly report") {
		t.Errorf("default search should exclude done tasks, got:\n%s", output)
	}

	output = runSearch(t, "archive", "--done")
	doneRow := rowContaining(t, output, "Archive the monthly report")
	openRow := rowContaining(t, output, "Archive the quarterly report")

	// Assert on the located rows, not on the buffer: "x " appears inside plenty
	// of titles, so a whole-output Contains would pass without the status cell
	// ever being rendered.
	if !strings.HasPrefix(doneRow, "x ") {
		t.Errorf("done row should start with the %q status cell, got %q", "x ", doneRow)
	}
	if !strings.HasPrefix(openRow, "  ") || strings.HasPrefix(openRow, "x ") {
		t.Errorf("open row should start with a blank status cell, got %q", openRow)
	}
}

// rowContaining returns the single output row containing want, failing the
// test when it is absent or ambiguous.
func rowContaining(t *testing.T, output, want string) string {
	t.Helper()
	var found []string
	for _, line := range resultLines(output) {
		if strings.Contains(line, want) {
			found = append(found, line)
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("no row contains %q, got:\n%s", want, output)
	default:
		t.Fatalf("%d rows contain %q, expected exactly 1:\n%s", len(found), want, output)
	}
	return ""
}

func TestSearchCommand_NoMatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Water the plants")

	output := runSearch(t, "zzzqqq")
	if !strings.Contains(output, "No matches.") {
		t.Errorf("no-match search should print 'No matches.', got:\n%s", output)
	}
}

func TestSearchCommand_EmptyStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	output := runSearch(t, "anything")
	if !strings.Contains(output, "No matches.") {
		t.Errorf("search over an empty store should print 'No matches.', got:\n%s", output)
	}
}

// TestSearchCommand_LongTitleNotTruncated is the whole point of the command:
// `ls` cuts titles at 40 runes, which makes it unfit for deduplication.
func TestSearchCommand_LongTitleNotTruncated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	longTitle := "Investigate the intermittent websocket disconnect during long-running exports"
	if len([]rune(longTitle)) <= 40 {
		t.Fatalf("test fixture must exceed the ls truncation width, got %d runes", len([]rune(longTitle)))
	}
	addTask(t, longTitle)

	output := runSearch(t, "websocket")
	if !strings.Contains(output, longTitle) {
		t.Errorf("search should print the full untruncated title, got:\n%s", output)
	}
	if strings.Contains(output, "…") {
		t.Errorf("search output should not contain a truncation ellipsis, got:\n%s", output)
	}
}

// TestSearchCommand_DoneRankingMatchesSharedIndex pins CLI/TUI ranking parity
// from the CLI side.
//
// The TUI ranks over its cached Model.allTasks, which reloadAllTasks fills
// from store.List(ListOptions{}) — no status filter, so open + done. `search
// --done` lifts the same filter, so both sides feed the shared search.Index
// an identically ordered task slice. This test reconstructs that haystack
// directly and asserts the command prints the ranker's results in the ranker's
// order, so any future divergence (a re-sort, a pre-filter, a second ranker)
// fails here instead of silently making the CLI disagree with `/`.
//
// The mirror assertion on the TUI side lives in internal/tui:
// TestSearch_RankingMatchesSharedIndexOverStoreList.
//
// Scope: SINGLE-TOKEN queries only, and deliberately so. Multi-word CLI
// queries no longer go straight to Index.Rank — cmd.rankQuery unions the whole
// phrase with each word ranked separately, because sahilm/fuzzy matches a
// query as one ordered subsequence and a one-shot CLI caller (the Claude
// skill's dedupe step) has no way to notice that "telegram week" silently
// missed the task "week ... telegram". The TUI keeps the strict in-order
// semantics: a human retypes against live per-keystroke results, so widening
// there would only add noise. That divergence is intended; the parity these
// two tests pin is the single-token path both sides share.
func TestSearchCommand_DoneRankingMatchesSharedIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Distinct, non-overlapping titles so an ordered Contains check cannot
	// match the wrong row.
	addTask(t, "Repair broken pagination")
	doneID := addTestTask(t, dir, "Repave the parking lot")
	addTask(t, "Reap rewards from caching")
	addTask(t, "Unrelated grocery run")

	rc := NewRootCmd()
	rc.SetOut(new(bytes.Buffer))
	rc.SetErr(new(bytes.Buffer))
	rc.SetArgs([]string{"done", doneID})
	if err := rc.Execute(); err != nil {
		t.Fatalf("done %s: %v", doneID, err)
	}

	// Rebuild the TUI's haystack: store.List with no status filter.
	s, err := store.New(filepath.Join(dir, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	all, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	// Guard the scope stated above: swapping in a phrase here would compare
	// rankQuery's union against Index.Rank and fail for the wrong reason.
	const query = "rep"
	if len(strings.Fields(query)) != 1 {
		t.Fatalf("parity fixture query %q must be a single token; multi-word CLI queries deliberately diverge from Index.Rank", query)
	}

	want := search.NewIndex(all).Rank(query, 25)
	if len(want) < 2 {
		t.Fatalf("fixture must produce at least 2 ranked hits, got %d", len(want))
	}

	got := resultLines(runSearch(t, query, "--done", "-n", "25"))
	if len(got) != len(want) {
		t.Fatalf("printed %d rows, ranker produced %d\noutput:\n%s",
			len(got), len(want), strings.Join(got, "\n"))
	}
	for i, r := range want {
		if !strings.Contains(got[i], r.Task.Title) {
			t.Errorf("row %d = %q, want the row for %q (score %d)",
				i, got[i], r.Task.Title, r.Score)
		}
	}
}

// TestSearchCommand_RequiresQuery pins cobra.MinimumNArgs(1).
func TestSearchCommand_RequiresQuery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"search"})

	if err := rootCmd.Execute(); err == nil {
		t.Fatalf("search with no query should error, got output:\n%s", buf.String())
	}
}

// TestSearchCommand_RejectsBlankQuery is the guard MinimumNArgs cannot give:
// `monolog search ""` satisfies "at least one argument", and Index.Rank("")
// deliberately means "every task by CreatedAt desc". Without an explicit
// check, a caller that interpolated an empty keyword string would get ten
// arbitrary rows back and read them as near-duplicates — which is exactly how
// the Claude skill's dedupe step decides to file a note against the wrong
// task. A blank query must be an error, not a dump.
func TestSearchCommand_RejectsBlankQuery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Water the plants")
	addTask(t, "Fix the login bug")

	for _, args := range [][]string{
		{""},
		{" "},
		{"\t"},
		{"", ""},
		{" ", " "},
	} {
		rootCmd := NewRootCmd()
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs(append([]string{"search"}, args...))

		err := rootCmd.Execute()
		if err == nil {
			t.Errorf("search %q should error, got output:\n%s", args, buf.String())
			continue
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("search %q error = %v, want it to mention an empty query", args, err)
		}
		if strings.Contains(buf.String(), "Water the plants") {
			t.Errorf("search %q must not print task rows, got:\n%s", args, buf.String())
		}
	}
}

// TestSearchCommand_DefaultLimitIsTen pins the documented "top 10". The clamp
// test reads defaultSearchLimit through the constant, so it stays green if the
// constant moves; both SKILL.md and the README promise the number.
func TestSearchCommand_DefaultLimitIsTen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	const (
		wantRows = 10
		total    = wantRows + 4
	)
	for i := 0; i < total; i++ {
		addTask(t, fmt.Sprintf("Sample entry %02d", i))
	}

	output := runSearch(t, "sample")
	if got := len(resultLines(output)); got != wantRows {
		t.Errorf("bare `search` printed %d rows, want the documented top %d:\n%s", got, wantRows, output)
	}
	// Sanity: the fixture really does have more matches than the cap, so the
	// assertion above is about the limit and not about how many tasks exist.
	if got := len(resultLines(runSearch(t, "sample", "-n", fmt.Sprint(total)))); got != total {
		t.Errorf("fixture should produce %d matches in total, got %d", total, got)
	}
}

// TestSearchCommand_SchedulesRenderInConfiguredDateFormat pins the
// config.DateFormat() wiring in RunE: hardcoding a layout there would still
// satisfy every other test in this file, since they all run under the default.
func TestSearchCommand_SchedulesRenderInConfiguredDateFormat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// config.Load never resets the date format back to the default, so restore
	// it rather than leaking MM-DD-YYYY into whatever test runs next.
	orig := config.DateFormat()
	t.Cleanup(func() {
		if err := config.SetDateFormat(orig); err != nil {
			t.Fatalf("restore date format: %v", err)
		}
	})

	// MM-DD-YYYY is neither the default (DD-MM-YYYY) nor ISO, so this fails
	// both for "layout ignored" and for "layout hardcoded to 2006-01-02".
	writeDateFormatConfig(t, dir, "01-02-2006")

	addTask(t, "Renew the domain", "-s", "2030-04-15")

	output := runSearch(t, "renew")
	if !strings.Contains(output, "04-15-2030") {
		t.Errorf("schedule should render in the configured MM-DD-YYYY layout (04-15-2030), got:\n%s", output)
	}
	if strings.Contains(output, "15-04-2030") || strings.Contains(output, "2030-04-15") {
		t.Errorf("schedule rendered in a layout other than the configured one, got:\n%s", output)
	}
}

// writeDateFormatConfig rewrites <dir>/.monolog/config.json with the given Go
// layout, preserving nothing else — the file `init` writes only carries
// "theme" and "date_format", and the theme is irrelevant to CLI output.
func writeDateFormatConfig(t *testing.T, dir, layout string) {
	t.Helper()
	p := filepath.Join(dir, ".monolog", "config.json")
	body := fmt.Sprintf("{\n  \"theme\": \"default\",\n  \"date_format\": %q\n}\n", layout)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
}

// TestSearchCommand_OpenStoreFailure covers the openStore() error path: a
// MONOLOG_DIR that cannot hold a .monolog/tasks directory.
func TestSearchCommand_OpenStoreFailure(t *testing.T) {
	// A regular file where the data directory should be, so MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	t.Setenv("MONOLOG_DIR", blocker)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"search", "anything"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("search against an unusable MONOLOG_DIR should error, got output:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "open store") {
		t.Errorf("error = %v, want it wrapped as %q", err, "open store")
	}
}

// TestSearchCommand_ListFailure covers the s.List() error path: an unparseable
// task file must surface as an error, not as a silently short result set.
func TestSearchCommand_ListFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Fix the login bug")

	corrupt := filepath.Join(dir, ".monolog", "tasks", "01CORRUPT.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt task: %v", err)
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"search", "login"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("search over a corrupt store should error, got output:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "list tasks") {
		t.Errorf("error = %v, want it wrapped as %q", err, "list tasks")
	}
}

// TestSearchCommand_PrintedIDResolves is the end-to-end contract the shipped
// skill depends on: an ID copied out of `monolog search` output must resolve
// through store.Resolve. Two tasks created back to back land in the same
// millisecond window, and a ULID's first 8 characters cover only 40 of the 48
// timestamp bits — so an 8-character prefix is ambiguous for exactly the batch
// -filing case the skill encourages.
func TestSearchCommand_PrintedIDResolves(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Back to back, no sleep: this is the collision case.
	addTask(t, "Handle nil store in openStore")
	addTask(t, "Handle nil config in openStore")

	s, err := store.New(filepath.Join(dir, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	all, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("fixture should have 2 tasks, got %d", len(all))
	}
	// Guard the fixture: if the two ULIDs happen not to share an 8-character
	// prefix the test proves nothing about the widening, so say so loudly.
	if all[0].ID[:8] != all[1].ID[:8] {
		t.Skipf("the two tasks landed in different 256ms windows (%s / %s); rerun", all[0].ID[:8], all[1].ID[:8])
	}

	rows := resultLines(runSearch(t, "openStore"))
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d:\n%s", len(rows), strings.Join(rows, "\n"))
	}

	for _, row := range rows {
		fields := strings.Fields(row)
		if len(fields) == 0 {
			t.Fatalf("empty row in search output:\n%s", strings.Join(rows, "\n"))
		}
		id := fields[0]
		task, err := s.Resolve(id)
		if err != nil {
			t.Errorf("ID %q printed by search does not resolve: %v", id, err)
			continue
		}
		if !strings.Contains(row, task.Title) {
			t.Errorf("ID %q resolved to %q, which is not the task on that row: %q", id, task.Title, row)
		}
	}
}

// --- multi-word order independence (rankQuery) ------------------------------

// orderFixture is the reproduction case for the word-order bug, distilled from
// a real backlog: `search "week telegram"` found the target while `search
// "telegram week"` returned the decoy and nothing else — a plausible-looking
// wrong answer rather than "No matches".
//
// target matches the phrase "week telegram" as an ordered subsequence but not
// "telegram week"; decoy is the mirror image. Neither is a red herring by
// accident, so if the union ever stops running, exactly one of the two
// direction assertions below fails.
func orderFixture() []model.Task {
	return []model.Task{
		{ID: "01TARGET0000000000000000AA", Title: "week command from telegram should display today's tasks",
			CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01DECOY00000000000000000BB", Title: "telegram bot: acknowledge weekly digest",
			CreatedAt: "2026-01-02T00:00:00Z"},
	}
}

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

// TestRankQuery_MultiWordIsOrderIndependent is the core guard: reversing the
// words of a query must not change which tasks come back. sahilm/fuzzy matches
// the query as one ordered subsequence including its spaces, so without the
// per-token union "telegram week" silently misses the task "week ... telegram"
// — and the skill's dedupe step files a duplicate on the strength of it.
func TestRankQuery_MultiWordIsOrderIndependent(t *testing.T) {
	const target = "01TARGET0000000000000000AA"
	ix := search.NewIndex(orderFixture())

	for _, query := range []string{"week telegram", "telegram week"} {
		got := rankQuery(ix, query, 10)
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

// TestRankQuery_SingleTokenMatchesRankExactly pins that the union path is
// multi-word only. A one-word query must go straight through to the shared
// ranker, byte for byte, since that is what keeps single-token CLI output in
// lockstep with the TUI overlay.
func TestRankQuery_SingleTokenMatchesRankExactly(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "Repair broken pagination", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01B", Title: "Repave the parking lot", CreatedAt: "2026-01-02T00:00:00Z"},
		{ID: "01C", Title: "Unrelated grocery run", CreatedAt: "2026-01-03T00:00:00Z"},
	}
	ix := search.NewIndex(tasks)

	want := ix.Rank("rep", 10)
	if len(want) < 2 {
		t.Fatalf("fixture must produce at least 2 hits, got %d", len(want))
	}
	got := rankQuery(ix, "rep", 10)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rankQuery single token = %v, want Rank's exact result %v",
			rankedIDs(got), rankedIDs(want))
	}

	// Surrounding whitespace is still one token, not two.
	if got := rankQuery(ix, "  rep  ", 10); !reflect.DeepEqual(got, want) {
		t.Errorf("padded single token = %v, want %v", rankedIDs(got), rankedIDs(want))
	}
}

// TestRankQuery_UnionDeduplicates guards the seen/pos bookkeeping: a task that
// matches the whole phrase AND one or more of its tokens must be printed once.
// Without the dedupe the top of a dedupe-critical result set fills with copies
// of the same task, hiding the other candidates behind the limit.
func TestRankQuery_UnionDeduplicates(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "alpha beta gamma", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01B", Title: "beta only", CreatedAt: "2026-01-02T00:00:00Z"},
	}
	ix := search.NewIndex(tasks)

	// "alpha beta" matches 01A as a phrase; the tokens "alpha" and "beta" both
	// match it again, so it is a three-way collision.
	got := rankQuery(ix, "alpha beta", 10)

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
		t.Errorf("phrase match 01A missing from %v", rankedIDs(got))
	}
	if !containsID(got, "01B") {
		t.Errorf("token-only match 01B missing from %v", rankedIDs(got))
	}
}

// TestRankQuery_UnionRespectsLimit pins that the limit is applied to the
// union, not per stage. The union is strictly wider than a phrase match, so an
// unclamped tail would dump most of the backlog on any multi-word query.
func TestRankQuery_UnionRespectsLimit(t *testing.T) {
	var tasks []model.Task
	for i := 0; i < 8; i++ {
		tasks = append(tasks, model.Task{
			ID:        fmt.Sprintf("01%02d", i),
			Title:     fmt.Sprintf("alpha entry %02d", i),
			CreatedAt: fmt.Sprintf("2026-01-%02dT00:00:00Z", i+1),
		})
	}
	// One task matching the phrase in order, so both stages contribute.
	tasks = append(tasks, model.Task{ID: "01P", Title: "alpha beta phrase", CreatedAt: "2026-02-01T00:00:00Z"})
	ix := search.NewIndex(tasks)

	if got := rankQuery(ix, "alpha beta", 3); len(got) != 3 {
		t.Errorf("rankQuery with limit 3 returned %d results (%v), want 3", len(got), rankedIDs(got))
	}
	// limit <= 0 keeps Rank's "no truncation" meaning; the command clamps
	// before calling, but the helper must not invent a cap of its own.
	if got := rankQuery(ix, "alpha beta", 0); len(got) < 9 {
		t.Errorf("rankQuery with limit 0 returned %d results, want every match (>=9)", len(got))
	}
}

// TestRankQuery_PhraseMatchesLeadTheUnion pins the ordering contract: an
// in-order phrase hit is the strongest signal there is, so it must never be
// pushed below a single-word hit that happens to score higher.
func TestRankQuery_PhraseMatchesLeadTheUnion(t *testing.T) {
	tasks := []model.Task{
		{ID: "01PHRASE", Title: "xx alpha xx beta xx", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01TOKEN", Title: "beta", CreatedAt: "2026-01-02T00:00:00Z"},
	}
	ix := search.NewIndex(tasks)

	got := rankQuery(ix, "alpha beta", 10)
	if len(got) != 2 {
		t.Fatalf("expected both tasks, got %v", rankedIDs(got))
	}
	if got[0].Task.ID != "01PHRASE" {
		t.Errorf("phrase hit should lead, got order %v", rankedIDs(got))
	}
}

// TestRankQuery_SkipsSingleCharacterTokens pins minUnionTokenLen. A
// one-character token fuzzy-matches nearly every task, so unioning it in would
// turn any query containing "a" or "I" into a backlog dump.
func TestRankQuery_SkipsSingleCharacterTokens(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "alpha beta", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "01B", Title: "an unrelated task", CreatedAt: "2026-01-02T00:00:00Z"},
	}
	ix := search.NewIndex(tasks)

	// "a" alone would match 01B ("an unrelated task"); "zeta" matches neither.
	got := rankQuery(ix, "a zeta", 10)
	if containsID(got, "01B") {
		t.Errorf("single-character token was unioned in: %v", rankedIDs(got))
	}
}
