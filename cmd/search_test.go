package cmd

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
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
	if !strings.Contains(output, "Archive the monthly report") {
		t.Errorf("--done should include the completed task, got:\n%s", output)
	}
	if !strings.Contains(output, "Archive the quarterly report") {
		t.Errorf("--done should still include open tasks, got:\n%s", output)
	}
	if !strings.Contains(output, "x ") {
		t.Errorf("--done output should carry the done status cell, got:\n%s", output)
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
