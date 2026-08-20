package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/search"
	"github.com/maksmas/monolog/internal/store"
)

// End-to-end tests for `monolog search`. The ranking rules themselves are unit
// tested in internal/search (rank_test.go for fuzzy matching, rankterms_test.go
// for the multi-word term count); everything here goes through the cobra
// command and a real store, so a refactor that bypasses the ranker in RunE is
// caught too.

// --- helpers ----------------------------------------------------------------

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

// writeRawTask drops a task JSON file straight into the store, bypassing
// `monolog add` so a test can choose the ULID instead of taking whatever the
// clock hands out. Search only reads, so an uncommitted file is enough.
func writeRawTask(t *testing.T, dir string, task model.Task) {
	t.Helper()
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal task %s: %v", task.ID, err)
	}
	p := filepath.Join(dir, ".monolog", "tasks", task.ID+".json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write task file %s: %v", p, err)
	}
}

// subsequenceNoise is a title containing neither "telegram" nor "week" as a
// word, yet carrying both as an ordered character subsequence — which is all
// sahilm/fuzzy asks for. See search.Index.RankTerms for why that matters.
const subsequenceNoise = "t e l e g r a m w e e k"

// --- matching ---------------------------------------------------------------

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

// --- multi-word term ranking ------------------------------------------------

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

// TestSearchCommand_MultiWordIsOrderIndependent pins the promise README.md and
// SKILL.md both make: reversing the words of a query changes neither the rows
// nor their order. It is asserted on the raw output, and again under a limit
// small enough that a reordering would change which row survives the cut.
func TestSearchCommand_MultiWordIsOrderIndependent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Both titles carry both query words, so every sort key above CreatedAt
	// ties and only a word-order-sensitive tie-break could separate them.
	const (
		bucketTitle = "Telegram bot needs week bucket support"
		brokenTitle = "Week command from telegram is broken"
	)
	addTask(t, bucketTitle)
	addTask(t, brokenTitle)

	forward := runSearch(t, "telegram week")
	if !strings.Contains(forward, bucketTitle) || !strings.Contains(forward, brokenTitle) {
		t.Fatalf("fixture should return both tasks, got:\n%s", forward)
	}
	if got := runSearch(t, "week telegram"); got != forward {
		t.Errorf("reversing the query changed the output.\nforward:\n%s\nreversed:\n%s", forward, got)
	}

	// The row set, not just the order: the limit is applied after sorting, so a
	// flipped order prints a different single row.
	one := runSearch(t, "telegram week", "-n", "1")
	if len(resultLines(one)) != 1 {
		t.Fatalf("-n 1 should print exactly one row, got:\n%s", one)
	}
	if got := runSearch(t, "week telegram", "-n", "1"); got != one {
		t.Errorf("-n 1 returned a different row for the reversed query.\nforward:\n%s\nreversed:\n%s", one, got)
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

// --- flags ------------------------------------------------------------------

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

// --- output shape -----------------------------------------------------------

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

// TestSearchCommand_PrintedIDResolves is the end-to-end contract the shipped
// skill depends on: an ID copied out of `monolog search` output must resolve
// through store.Resolve.
//
// A ULID's first 10 Crockford characters carry the 48-bit millisecond
// timestamp, so 8 characters cover only 40 of those bits and every task created
// inside the same ~256 ms window shares an 8-character prefix — exactly the
// batch-filing case the skill encourages. The two ULIDs below are written by
// hand rather than taken from the clock so that collision is a property of the
// fixture instead of a race the test has to skip around: they agree on their
// first 11 characters and diverge on the 12th, which is precisely the width
// display.searchIDWidth prints.
func TestSearchCommand_PrintedIDResolves(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	const (
		idA = "01M0FRKNJAPA00000000000000"
		idB = "01M0FRKNJAPB00000000000000"
	)
	// Guard the hand-picked fixture in both directions: the 8-character prefix
	// must collide (or the widening proves nothing) and the 12-character one
	// must not (or the command is printing an unusable ID).
	if idA[:8] != idB[:8] {
		t.Fatalf("fixture ULIDs must share an 8-character prefix, got %s / %s", idA[:8], idB[:8])
	}
	if idA[:12] == idB[:12] {
		t.Fatalf("fixture ULIDs must diverge within 12 characters, got %s", idA[:12])
	}

	writeRawTask(t, dir, model.Task{
		ID: idA, Title: "Handle nil store in openStore", Source: "cli", Status: "open",
		Position: 1000, Schedule: "2030-04-15",
		CreatedAt: "2030-04-15T09:00:00Z", UpdatedAt: "2030-04-15T09:00:00Z",
	})
	writeRawTask(t, dir, model.Task{
		ID: idB, Title: "Handle nil config in openStore", Source: "cli", Status: "open",
		Position: 2000, Schedule: "2030-04-15",
		CreatedAt: "2030-04-15T09:00:01Z", UpdatedAt: "2030-04-15T09:00:01Z",
	})

	s, err := store.New(filepath.Join(dir, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
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

// --- error paths ------------------------------------------------------------

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

// --- CLI/TUI parity ---------------------------------------------------------

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
// Scope: SINGLE-TOKEN queries only, and deliberately so — that is the one path
// where the CLI and the overlay call the same method. Multi-word CLI queries go
// through search.Index.RankTerms instead, whose doc comment is the canonical
// explanation of why the two rank phrases differently.
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
	// RankTerms's output against Rank's and fail for the wrong reason.
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
