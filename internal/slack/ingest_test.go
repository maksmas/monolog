package slack

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/store"
)

// fixedNow returns a clock function pinned to a single instant for
// deterministic CreatedAt/UpdatedAt/displayTime values.
func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// newGitStore mirrors the store package's test helper: a fully initialized
// mlog repo (required for store.CreateBatch which calls git.AutoCommit).
func newGitStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := git.Init(repoPath, ""); err != nil {
		t.Fatalf("git.Init: %v", err)
	}
	s, err := store.New(filepath.Join(repoPath, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	return s, repoPath
}

func headSubject(t *testing.T, repoPath string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log -1: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func sampleItem() SavedItem {
	return SavedItem{
		Channel:     "C0123",
		ChannelName: "general",
		TS:          "1712345678.000100",
		Text:        "remember to review the PR",
		AuthorID:    "U0456",
		AuthorName:  "alice",
		Permalink:   "https://myteam.slack.com/archives/C0123/p1712345678000100",
	}
}

// --- BuildTask tests ---

func TestBuildTask_HappyPath(t *testing.T) {
	now := time.Date(2026, 4, 23, 9, 30, 0, 0, time.UTC)
	opts := Options{
		ChannelAsTag: true,
		DateFormat:   "02-01-2006",
		Now:          fixedNow(now),
	}
	task := BuildTask(sampleItem(), opts)

	if task.Title != "remember to review the PR" {
		t.Errorf("Title: got %q", task.Title)
	}
	if task.Source != "slack" {
		t.Errorf("Source: got %q want slack", task.Source)
	}
	if task.SourceID != "C0123/1712345678.000100" {
		t.Errorf("SourceID: got %q", task.SourceID)
	}
	if task.Status != "open" {
		t.Errorf("Status: got %q want open", task.Status)
	}
	if task.Schedule != "2026-04-23" {
		t.Errorf("Schedule: got %q want 2026-04-23", task.Schedule)
	}
	// Attribution date now derives from item.TS ("1712345678.000100" = Apr 5
	// 2024 UTC), not from ingest time — a bookmark saved months ago reads
	// with its original message date.
	if !strings.Contains(task.Body, "— @alice in #general, 05-04-2024") {
		t.Errorf("Body missing attribution line; got:\n%s", task.Body)
	}
	if !strings.Contains(task.Body, sampleItem().Permalink) {
		t.Errorf("Body missing permalink; got:\n%s", task.Body)
	}
	if !strings.Contains(task.Body, "remember to review the PR") {
		t.Errorf("Body missing original text; got:\n%s", task.Body)
	}
	// Tags: ["slack", "general"] when ChannelAsTag=true
	if len(task.Tags) != 2 || task.Tags[0] != "slack" || task.Tags[1] != "general" {
		t.Errorf("Tags: got %v want [slack general]", task.Tags)
	}
}

func TestBuildTask_ChannelAsTagFalse(t *testing.T) {
	opts := Options{
		ChannelAsTag: false,
		DateFormat:   "02-01-2006",
		Now:          fixedNow(time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)),
	}
	task := BuildTask(sampleItem(), opts)
	if len(task.Tags) != 1 || task.Tags[0] != "slack" {
		t.Errorf("Tags: got %v want [slack]", task.Tags)
	}
}

func TestBuildTask_TitleExactly80Runes(t *testing.T) {
	text := strings.Repeat("a", 80)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	item := sampleItem()
	item.Text = text
	task := BuildTask(item, opts)
	if got := []rune(task.Title); len(got) != 80 {
		t.Errorf("Title rune count: got %d want 80", len(got))
	}
	if strings.Contains(task.Title, "…") {
		t.Errorf("80-rune title should not have ellipsis; got %q", task.Title)
	}
}

func TestBuildTask_TitleLongerThan80Truncates(t *testing.T) {
	text := strings.Repeat("b", 200)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	item := sampleItem()
	item.Text = text
	task := BuildTask(item, opts)
	runes := []rune(task.Title)
	if len(runes) != 80 {
		t.Errorf("Title rune count: got %d want 80", len(runes))
	}
	if runes[len(runes)-1] != '…' {
		t.Errorf("Title should end with ellipsis; got %q", task.Title)
	}
	// The 79 preceding runes are the original content.
	if string(runes[:79]) != strings.Repeat("b", 79) {
		t.Errorf("Pre-ellipsis content corrupted; got %q", string(runes[:79]))
	}
}

func TestBuildTask_TitleMultibyteBoundary(t *testing.T) {
	// 100 emoji (each >1 byte) — must cut cleanly at rune 79, not byte 79.
	text := strings.Repeat("😀", 100)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	item := sampleItem()
	item.Text = text
	task := BuildTask(item, opts)
	runes := []rune(task.Title)
	if len(runes) != 80 {
		t.Errorf("Title rune count: got %d want 80", len(runes))
	}
	// Every rune except the last should be the emoji.
	for i := 0; i < 79; i++ {
		if runes[i] != '😀' {
			t.Errorf("Rune %d: got %q want 😀", i, runes[i])
		}
	}
	if runes[79] != '…' {
		t.Errorf("Last rune: got %q want …", runes[79])
	}
}

func TestBuildTask_TitleLeadingBlankLines(t *testing.T) {
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	item := sampleItem()
	item.Text = "\n\n   \nactual title\nmore body"
	task := BuildTask(item, opts)
	if task.Title != "actual title" {
		t.Errorf("Title: got %q want 'actual title'", task.Title)
	}
}

func TestBuildTask_TitleWhitespaceOnlyFallback(t *testing.T) {
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	item := sampleItem()
	item.Text = "   \n\t\n  "
	task := BuildTask(item, opts)
	if task.Title != "(empty message)" {
		t.Errorf("Title: got %q want '(empty message)'", task.Title)
	}
}

func TestBuildTask_TitleEmptyFallback(t *testing.T) {
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	item := sampleItem()
	item.Text = ""
	task := BuildTask(item, opts)
	if task.Title != "(empty message)" {
		t.Errorf("Title: got %q want '(empty message)'", task.Title)
	}
}

func TestBuildTask_BodyVerbatimPassthrough(t *testing.T) {
	// Slack markup tokens should render literally (documented v1 limitation).
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	item := sampleItem()
	item.Text = "ping <@U0456> see <#C0999|eng> and <https://example.com|docs> &amp;"
	task := BuildTask(item, opts)
	if !strings.Contains(task.Body, "<@U0456>") {
		t.Errorf("Body should preserve <@U0456> verbatim; got:\n%s", task.Body)
	}
	if !strings.Contains(task.Body, "<#C0999|eng>") {
		t.Errorf("Body should preserve <#C0999|eng> verbatim; got:\n%s", task.Body)
	}
	if !strings.Contains(task.Body, "<https://example.com|docs>") {
		t.Errorf("Body should preserve link markup verbatim; got:\n%s", task.Body)
	}
	if !strings.Contains(task.Body, "&amp;") {
		t.Errorf("Body should preserve HTML entity verbatim; got:\n%s", task.Body)
	}
}

func TestBuildTask_ThreadedReplyBodyEqualsNonThreaded(t *testing.T) {
	// ThreadTS is metadata only — BuildTask consumes Text, not ThreadTS, so a
	// threaded-reply body is identical to the same item without ThreadTS.
	// Asserts exact body equality (both cases) rather than a weak "contains"
	// check that would pass even if ThreadTS were accidentally wired in.
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}

	plain := sampleItem()
	plain.Text = "follow-up reply"
	threaded := plain
	threaded.ThreadTS = "1712345670.000001"

	if got, want := BuildTask(threaded, opts).Body, BuildTask(plain, opts).Body; got != want {
		t.Errorf("threaded body differed from plain body:\nthreaded=%q\nplain=%q", got, want)
	}
}

func TestBuildTask_AttachmentsDroppedFromBody(t *testing.T) {
	// HasFiles=true must not alter the body — attachments are metadata, not
	// rendered in v1. Strong assertion: body is exactly Text + attribution +
	// permalink, with no extra lines introduced by the HasFiles flag.
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	item := sampleItem()

	plain := BuildTask(item, opts).Body
	item.HasFiles = true
	withFiles := BuildTask(item, opts).Body

	if plain != withFiles {
		t.Errorf("HasFiles changed body rendering:\nplain=%q\nwithFiles=%q", plain, withFiles)
	}

	// Also verify exact structure: Text + "\n\n— @author in #channel, date\n\n" + permalink.
	// sampleItem() TS "1712345678.000100" = 2024-04-05 UTC.
	want := "remember to review the PR\n\n— @alice in #general, 05-04-2024\n\nhttps://myteam.slack.com/archives/C0123/p1712345678000100"
	if plain != want {
		t.Errorf("body structure mismatch:\ngot:  %q\nwant: %q", plain, want)
	}
}

func TestBuildTask_EmptyAuthorDropsAtSign(t *testing.T) {
	// Bot or deleted-user messages arrive with AuthorName="". The attribution
	// line must read "— in #channel, date" rather than a dangling "— @ in …".
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	item := sampleItem()
	item.AuthorName = ""
	body := BuildTask(item, opts).Body

	if strings.Contains(body, "@") {
		t.Errorf("body should not contain '@' when author empty; got:\n%s", body)
	}
	if !strings.Contains(body, "— in #general, 05-04-2024") {
		t.Errorf("body missing expected attribution without author; got:\n%s", body)
	}
}

func TestBuildTask_AttributionUsesMessageTS(t *testing.T) {
	// item.TS ("1712345678.000100" = 2024-04-05 UTC) must drive the
	// attribution date rather than the ingest wall clock. A bookmark saved
	// months ago should read with its original message date.
	ingestNow := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(ingestNow)}
	body := BuildTask(sampleItem(), opts).Body

	if !strings.Contains(body, "05-04-2024") {
		t.Errorf("attribution missing message-date 05-04-2024; got:\n%s", body)
	}
	if strings.Contains(body, "23-04-2026") {
		t.Errorf("attribution must not echo ingest date 23-04-2026; got:\n%s", body)
	}
}

func TestBuildTask_AttributionFallsBackToNowOnBadTS(t *testing.T) {
	// Malformed or zero ts must fall back to opts.Now so the attribution line
	// still renders a date.
	ingestNow := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(ingestNow)}
	item := sampleItem()
	item.TS = "not-a-number"
	body := BuildTask(item, opts).Body

	if !strings.Contains(body, "23-04-2026") {
		t.Errorf("fallback to now missing; got:\n%s", body)
	}
}

func TestBuildTask_NowDefaultsToTimeNow(t *testing.T) {
	opts := Options{DateFormat: "02-01-2006"} // Now nil
	task := BuildTask(sampleItem(), opts)
	// Parse CreatedAt; any RFC3339 value in a reasonable window is fine.
	created, err := time.Parse(time.RFC3339, task.CreatedAt)
	if err != nil {
		t.Fatalf("CreatedAt parse: %v", err)
	}
	if time.Since(created) > 5*time.Second || time.Since(created) < -5*time.Second {
		t.Errorf("CreatedAt outside expected window: %v", created)
	}
}

// --- Ingest tests ---

func TestIngest_EmptyReturnsZeroNoCommit(t *testing.T) {
	s, repoPath := newGitStore(t)
	before := headSubject(t, repoPath)

	synced := map[string]bool{}
	n, err := Ingest(s, nil, synced, Options{})
	if err != nil {
		t.Fatalf("Ingest(nil): %v", err)
	}
	if n != 0 {
		t.Errorf("newCount: got %d want 0", n)
	}
	if after := headSubject(t, repoPath); after != before {
		t.Errorf("HEAD changed on empty ingest: before=%q after=%q", before, after)
	}
}

func TestIngest_NewItemsAreCommittedAndSyncedMapUpdated(t *testing.T) {
	s, repoPath := newGitStore(t)
	now := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)
	opts := Options{ChannelAsTag: true, DateFormat: "02-01-2006", Now: fixedNow(now)}

	items := []SavedItem{
		{Channel: "C0123", ChannelName: "general", TS: "1.000100", Text: "one", AuthorName: "alice", Permalink: "p1"},
		{Channel: "C0123", ChannelName: "general", TS: "2.000200", Text: "two", AuthorName: "bob", Permalink: "p2"},
	}
	synced := map[string]bool{}

	n, err := Ingest(s, items, synced, opts)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if n != 2 {
		t.Errorf("newCount: got %d want 2", n)
	}
	if !synced["C0123/1.000100"] || !synced["C0123/2.000200"] {
		t.Errorf("synced map not updated: %v", synced)
	}
	if got := headSubject(t, repoPath); got != "slack: ingest 2 items" {
		t.Errorf("commit subject: got %q want %q", got, "slack: ingest 2 items")
	}

	all, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("task count: got %d want 2", len(all))
	}
	// Verify both tasks carry the SourceID we expect.
	seen := map[string]bool{}
	for _, task := range all {
		seen[task.SourceID] = true
		if task.Source != "slack" {
			t.Errorf("task %s Source: got %q want slack", task.ID, task.Source)
		}
	}
	if !seen["C0123/1.000100"] || !seen["C0123/2.000200"] {
		t.Errorf("task SourceIDs not as expected: %v", seen)
	}
}

func TestIngest_DedupSkipsSyncedKeys(t *testing.T) {
	s, repoPath := newGitStore(t)
	now := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(now)}

	items := []SavedItem{
		{Channel: "C0123", ChannelName: "g", TS: "1.000", Text: "old", AuthorName: "a", Permalink: "p1"},
		{Channel: "C0123", ChannelName: "g", TS: "2.000", Text: "new", AuthorName: "a", Permalink: "p2"},
	}
	synced := map[string]bool{"C0123/1.000": true}

	n, err := Ingest(s, items, synced, opts)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if n != 1 {
		t.Errorf("newCount: got %d want 1", n)
	}
	if got := headSubject(t, repoPath); got != "slack: ingest 1 items" {
		t.Errorf("commit subject: got %q want %q", got, "slack: ingest 1 items")
	}
	all, _ := s.List(store.ListOptions{})
	if len(all) != 1 {
		t.Fatalf("task count: got %d want 1", len(all))
	}
	if all[0].SourceID != "C0123/2.000" {
		t.Errorf("SourceID: got %q want C0123/2.000", all[0].SourceID)
	}
}

func TestIngest_AllItemsSkippedNoCommit(t *testing.T) {
	s, repoPath := newGitStore(t)
	before := headSubject(t, repoPath)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}

	items := []SavedItem{
		{Channel: "C0123", ChannelName: "g", TS: "1.000", Text: "one", AuthorName: "a", Permalink: "p"},
	}
	synced := map[string]bool{"C0123/1.000": true}

	n, err := Ingest(s, items, synced, opts)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if n != 0 {
		t.Errorf("newCount: got %d want 0", n)
	}
	if after := headSubject(t, repoPath); after != before {
		t.Errorf("HEAD changed when all items deduped: before=%q after=%q", before, after)
	}
}

func TestIngest_DMChannelsSkippedSilentlyToStderr(t *testing.T) {
	s, _ := newGitStore(t)
	stderr := &bytes.Buffer{}
	opts := Options{
		DateFormat: "02-01-2006",
		Now:        fixedNow(time.Now()),
		Stderr:     stderr,
	}

	// Note: "G" prefix is NOT a DM marker — Slack uses it for private
	// channels (the common case) AND legacy group DMs (rare). Ingesting
	// the occasional legacy group DM with an ugly channel name is a
	// cheaper mistake than silently dropping private-channel bookmarks.
	items := []SavedItem{
		{Channel: "D123", ChannelName: "", TS: "1.000", Text: "dm", AuthorName: "a", Permalink: "p"},
		{Channel: "G123", ChannelName: "priv", TS: "2.000", Text: "private channel", AuthorName: "a", Permalink: "p"},
		{Channel: "mpdm-alice--bob--carol-1", ChannelName: "", TS: "3.000", Text: "mpdm", AuthorName: "a", Permalink: "p"},
		{Channel: "C0123", ChannelName: "g", TS: "4.000", Text: "public", AuthorName: "a", Permalink: "p"},
	}
	synced := map[string]bool{}

	n, err := Ingest(s, items, synced, opts)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	// D + mpdm- skipped; G (private channel) + C (public) ingested.
	if n != 2 {
		t.Errorf("newCount: got %d want 2", n)
	}

	msg := stderr.String()
	for _, ts := range []string{"1.000", "3.000"} {
		if !strings.Contains(msg, ts) {
			t.Errorf("stderr missing DM-skip for %s; got:\n%s", ts, msg)
		}
	}
	// Private channel (G-prefix) should NOT appear as a DM skip.
	if strings.Contains(msg, "2.000") {
		t.Errorf("G-prefixed private channel should not be skipped as DM; got:\n%s", msg)
	}
	// Public channel should not appear in stderr.
	if strings.Contains(msg, "4.000") {
		t.Errorf("stderr should not mention public channel TS; got:\n%s", msg)
	}
}

// failingStore is a drop-in store whose CreateBatch always errors. We can't
// easily mock *store.Store, so we simulate commit failure by pointing the
// store at a non-git directory — git.AutoCommit will fail.
func TestIngest_CommitFailurePreservesSyncedMap(t *testing.T) {
	// Store rooted at a temp dir that is NOT a git repo.
	tmp := t.TempDir()
	s, err := store.New(filepath.Join(tmp, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	items := []SavedItem{
		{Channel: "C0123", ChannelName: "g", TS: "1.000", Text: "one", AuthorName: "a", Permalink: "p"},
	}
	synced := map[string]bool{}

	n, err := Ingest(s, items, synced, opts)
	if err == nil {
		t.Fatalf("Ingest should have errored on non-git repo")
	}
	if n != 0 {
		t.Errorf("newCount on error: got %d want 0", n)
	}
	if len(synced) != 0 {
		t.Errorf("synced map should be untouched on commit failure: %v", synced)
	}
}

// TestIngest_PrivateChannelGPrefixIngested asserts that a G-prefixed channel
// (Slack's namespace for private channels, shared with legacy group DMs)
// flows through to task creation. Regression guard: the previous version of
// isDMChannel silently dropped these, which broke private-channel bookmark
// ingest entirely.
func TestIngest_PrivateChannelGPrefixIngested(t *testing.T) {
	s, _ := newGitStore(t)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}

	items := []SavedItem{
		{Channel: "GPRIV123", ChannelName: "private-room", TS: "1.000", Text: "from a private channel", AuthorName: "alice", Permalink: "https://example.slack.com/archives/GPRIV123/p1"},
	}
	synced := map[string]bool{}

	n, err := Ingest(s, items, synced, opts)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if n != 1 {
		t.Fatalf("newCount: got %d want 1", n)
	}

	all, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("task count: got %d want 1", len(all))
	}
	if all[0].SourceID != "GPRIV123/1.000" {
		t.Errorf("SourceID: got %q want %q", all[0].SourceID, "GPRIV123/1.000")
	}
}

func TestIngest_PositionsIncrementBy1000(t *testing.T) {
	s, _ := newGitStore(t)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}

	items := []SavedItem{
		{Channel: "C0123", ChannelName: "g", TS: "1", Text: "a", AuthorName: "x", Permalink: "p"},
		{Channel: "C0123", ChannelName: "g", TS: "2", Text: "b", AuthorName: "x", Permalink: "p"},
		{Channel: "C0123", ChannelName: "g", TS: "3", Text: "c", AuthorName: "x", Permalink: "p"},
	}
	synced := map[string]bool{}

	if _, err := Ingest(s, items, synced, opts); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	all, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("task count: got %d want 3", len(all))
	}
	// Tasks sorted by position; first task at basePos (1000 on empty store),
	// subsequent increments by DefaultSpacing.
	basePos := ordering.DefaultSpacing
	for i, task := range all {
		want := basePos + float64(i)*ordering.DefaultSpacing
		if task.Position != want {
			t.Errorf("task %d Position: got %v want %v", i, task.Position, want)
		}
	}
}

func TestIngest_PositionBaseRespectsExistingTasks(t *testing.T) {
	s, repoPath := newGitStore(t)

	// Seed the store with one existing task via Store.Create (commits).
	existing := model.Task{
		ID: "01PREEXIST0000000000000000", Title: "old", Source: "manual", Status: "open",
		Position: 5000, Schedule: "2026-04-23",
		CreatedAt: "2026-04-23T00:00:00Z", UpdatedAt: "2026-04-23T00:00:00Z",
	}
	if err := s.Create(existing); err != nil {
		t.Fatalf("Create seed: %v", err)
	}
	// Commit it so headSubject advances (not strictly required, but keeps
	// state consistent with how real workflows flow).
	if err := git.AutoCommit(repoPath, "seed", ".monolog/tasks/"+existing.ID+".json"); err != nil {
		t.Fatalf("AutoCommit seed: %v", err)
	}

	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}
	items := []SavedItem{
		{Channel: "C0123", ChannelName: "g", TS: "1", Text: "a", AuthorName: "x", Permalink: "p"},
		{Channel: "C0123", ChannelName: "g", TS: "2", Text: "b", AuthorName: "x", Permalink: "p"},
	}

	if _, err := Ingest(s, items, map[string]bool{}, opts); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	all, _ := s.List(store.ListOptions{})
	if len(all) != 3 {
		t.Fatalf("task count: got %d want 3", len(all))
	}
	// Expect the new tasks at 6000 and 7000 (existing at 5000 → NextPosition=6000).
	// Sort order: existing 5000 first, then slack 6000, 7000.
	if all[0].ID != existing.ID {
		t.Errorf("first task: got %q want seeded ID", all[0].ID)
	}
	if all[1].Position != 6000 {
		t.Errorf("second task Position: got %v want 6000", all[1].Position)
	}
	if all[2].Position != 7000 {
		t.Errorf("third task Position: got %v want 7000", all[2].Position)
	}
}

func TestParseSourceID(t *testing.T) {
	cases := []struct {
		in        string
		channel   string
		ts        string
		ok        bool
		rationale string
	}{
		{"C0123/1712345678.000100", "C0123", "1712345678.000100", true, "happy path"},
		{"", "", "", false, "empty input"},
		{"C0123", "", "", false, "no slash"},
		{"/1712345678.000100", "", "", false, "empty channel"},
		{"C0123/", "", "", false, "empty ts"},
		{"C0123/ts/extra", "C0123", "ts/extra", true, "slashes past the first stay in ts"},
	}
	for _, tc := range cases {
		ch, ts, ok := ParseSourceID(tc.in)
		if ch != tc.channel || ts != tc.ts || ok != tc.ok {
			t.Errorf("ParseSourceID(%q) [%s]: got (%q, %q, %v) want (%q, %q, %v)",
				tc.in, tc.rationale, ch, ts, ok, tc.channel, tc.ts, tc.ok)
		}
	}
}

func TestIsDMChannel(t *testing.T) {
	cases := map[string]bool{
		"":                false,
		"C0123":           false,
		"D0123":           true,
		// G-prefix is NOT treated as a DM — Slack uses it for private
		// channels (primary use) AND legacy group DMs (rare). Private
		// channel bookmarks must ingest; the occasional legacy group DM
		// passes through as well and is accepted as a cost of that choice.
		"G0123":                  false,
		"mpdm-alice--bob-1":      true,
		"c_lowercase_shouldpass": false,
	}
	for ch, want := range cases {
		if got := isDMChannel(ch); got != want {
			t.Errorf("isDMChannel(%q): got %v want %v", ch, got, want)
		}
	}
}

// TestBuildTask_CommitMessagePluralization sanity-checks our message format
// when the plan-specified template is "slack: ingest N items" irrespective
// of plurality. The plan explicitly specifies that exact format (no singular
// fork).
func TestIngest_CommitMessageExactFormat(t *testing.T) {
	s, repoPath := newGitStore(t)
	opts := Options{DateFormat: "02-01-2006", Now: fixedNow(time.Now())}

	cases := []struct {
		count    int
		expected string
	}{
		{1, "slack: ingest 1 items"},
		{2, "slack: ingest 2 items"},
		{5, "slack: ingest 5 items"},
	}

	for _, tc := range cases {
		items := make([]SavedItem, tc.count)
		for i := range items {
			items[i] = SavedItem{
				Channel: "C0123", ChannelName: "g",
				TS:   fmt.Sprintf("%d.000", i+1000*tc.count), // unique across cases
				Text: "x", AuthorName: "x", Permalink: "p",
			}
		}
		if _, err := Ingest(s, items, map[string]bool{}, opts); err != nil {
			t.Fatalf("Ingest(%d): %v", tc.count, err)
		}
		if got := headSubject(t, repoPath); got != tc.expected {
			t.Errorf("count=%d: commit subject got %q want %q", tc.count, got, tc.expected)
		}
	}
}
