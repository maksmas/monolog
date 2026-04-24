package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/slack"
)

// gitLog returns the concatenated commit subjects in the repo at dir, newest
// first. Used to assert that slack-sync produced (or skipped) a batched
// ingest commit.
func gitLog(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--oneline").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	return string(out)
}

// slackSyncMock is a minimal httptest handler for the Slack Web API methods
// used by slack-sync (stars.list + conversations.info + users.info). We keep
// per-method response bodies and an item-list queue for stars.list so tests
// can model "second run" behavior (no items, or different items) without
// restarting the server.
type slackSyncMock struct {
	mu       sync.Mutex
	hits     map[string]int
	starsSeq [][]byte // ordered list of stars.list bodies; each call consumes one
}

func newSlackSyncMock() *slackSyncMock {
	return &slackSyncMock{hits: map[string]int{}}
}

// pushStars queues a stars.list response body. Tests supply one body per
// expected invocation of the command.
func (m *slackSyncMock) pushStars(body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.starsSeq = append(m.starsSeq, []byte(body))
}

func (m *slackSyncMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/")
	m.mu.Lock()
	m.hits[method]++
	var resp []byte
	switch method {
	case "stars.list":
		if len(m.starsSeq) > 0 {
			resp = m.starsSeq[0]
			m.starsSeq = m.starsSeq[1:]
		} else {
			resp = []byte(`{"ok":true,"items":[]}`)
		}
	case "conversations.info":
		resp = []byte(`{"ok":true,"channel":{"id":"C0123ABC","name":"general"}}`)
	case "users.info":
		resp = []byte(`{"ok":true,"user":{"id":"U0456","name":"alice","real_name":"Alice","profile":{"display_name":"alice","real_name":"Alice"}}}`)
	default:
		resp = []byte(`{"ok":false,"error":"unknown_method"}`)
	}
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp)
}

// installSlackSyncClient wires a test Slack client (pointing at server) into
// the slack-sync command's factory. Returns a restore closure that tests
// defer to undo the override.
func installSlackSyncClient(t *testing.T, server *httptest.Server) func() {
	t.Helper()
	orig := newSlackSyncClientFn
	newSlackSyncClientFn = func(token, workspace string) *slack.Client {
		return &slack.Client{Token: token, Workspace: workspace, BaseURL: server.URL}
	}
	return func() { newSlackSyncClientFn = orig }
}

// stars.list body with N message items starting at ts offset. Channel is
// C0123ABC and user is U0456 so the mock's resolvers fire once each.
func starsListBody(tsStart, count int) string {
	var items []string
	for i := 0; i < count; i++ {
		ts := fmt.Sprintf("%d.00010%d", tsStart+i, i)
		items = append(items, fmt.Sprintf(`{
			"type":"message",
			"channel":"C0123ABC",
			"message":{
				"type":"message",
				"ts":%q,
				"text":"saved message %d",
				"user":"U0456",
				"permalink":"https://myteam.slack.com/archives/C0123ABC/p%s"
			}
		}`, ts, i, strings.ReplaceAll(ts, ".", "")))
	}
	return fmt.Sprintf(`{"ok":true,"items":[%s]}`, strings.Join(items, ","))
}

func TestSlackSync_IngestsNewItems(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	t.Setenv("MONOLOG_SLACK_TOKEN", "xoxp-test")

	mock := newSlackSyncMock()
	mock.pushStars(starsListBody(1712345678, 3))
	server := httptest.NewServer(mock)
	defer server.Close()

	restore := installSlackSyncClient(t, server)
	defer restore()

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-sync"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("slack-sync error: %v\nout=%s\nerr=%s", err, outBuf.String(), errBuf.String())
	}

	if !strings.Contains(outBuf.String(), "Ingested 3 new task(s).") {
		t.Errorf("expected 'Ingested 3 new task(s).', got:\n%s", outBuf.String())
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 3 {
		t.Fatalf("got %d tasks on disk, want 3", len(tasks))
	}
	for _, task := range tasks {
		if task.Source != "slack" {
			t.Errorf("task %s: Source=%q, want slack", task.ID, task.Source)
		}
		if task.SourceID == "" {
			t.Errorf("task %s: empty SourceID", task.ID)
		}
	}

	// Verify a single commit landed for this batch. Inspect the git log.
	log := gitLog(t, dir)
	if !strings.Contains(log, "slack: ingest 3 items") {
		t.Errorf("expected batched commit in log, got:\n%s", log)
	}
}

func TestSlackSync_NoNewItems(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	t.Setenv("MONOLOG_SLACK_TOKEN", "xoxp-test")

	mock := newSlackSyncMock()
	mock.pushStars(`{"ok":true,"items":[]}`)
	server := httptest.NewServer(mock)
	defer server.Close()

	restore := installSlackSyncClient(t, server)
	defer restore()

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-sync"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("slack-sync error: %v", err)
	}

	if !strings.Contains(outBuf.String(), "No new items.") {
		t.Errorf("expected 'No new items.', got:\n%s", outBuf.String())
	}

	// No tasks created.
	if tasks := readTasks(t, dir); len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}

	// No batched commit should appear in the log.
	if log := gitLog(t, dir); strings.Contains(log, "slack: ingest") {
		t.Errorf("no commit expected for empty poll; got log:\n%s", log)
	}
}

func TestSlackSync_DedupAndOnlyNewIngested(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	t.Setenv("MONOLOG_SLACK_TOKEN", "xoxp-test")

	mock := newSlackSyncMock()
	// First run: 3 items.
	mock.pushStars(starsListBody(1712345678, 3))
	// Second run: 1 of the 3 has been removed server-side (Slack stopped
	// returning it), plus 1 brand new item. We simulate "removed" by dropping
	// the item with ts 1712345679.000101 (index 1) and appending a new item
	// at ts 1712345700.
	body := `{"ok":true,"items":[
		{"type":"message","channel":"C0123ABC","message":{"type":"message","ts":"1712345678.000100","text":"saved message 0","user":"U0456","permalink":"https://myteam.slack.com/archives/C0123ABC/p17123456780001000"}},
		{"type":"message","channel":"C0123ABC","message":{"type":"message","ts":"1712345680.000102","text":"saved message 2","user":"U0456","permalink":"https://myteam.slack.com/archives/C0123ABC/p17123456800001002"}},
		{"type":"message","channel":"C0123ABC","message":{"type":"message","ts":"1712345700.000003","text":"brand new","user":"U0456","permalink":"https://myteam.slack.com/archives/C0123ABC/p17123457000000003"}}
	]}`
	mock.pushStars(body)
	server := httptest.NewServer(mock)
	defer server.Close()

	restore := installSlackSyncClient(t, server)
	defer restore()

	// First run — ingests 3.
	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"slack-sync"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first slack-sync error: %v", err)
	}
	if tasks := readTasks(t, dir); len(tasks) != 3 {
		t.Fatalf("after first run: %d tasks, want 3", len(tasks))
	}

	// Second run — dedup skips the 2 overlapping items, ingests only the new one.
	rootCmd = NewRootCmd()
	outBuf = new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"slack-sync"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("second slack-sync error: %v", err)
	}

	if !strings.Contains(outBuf.String(), "Ingested 1 new task(s).") {
		t.Errorf("expected 'Ingested 1 new task(s).', got:\n%s", outBuf.String())
	}
	tasks := readTasks(t, dir)
	if len(tasks) != 4 {
		t.Fatalf("after second run: %d tasks, want 4", len(tasks))
	}
	// Verify the new task is present by SourceID.
	var foundNew bool
	for _, task := range tasks {
		if task.SourceID == "C0123ABC/1712345700.000003" {
			foundNew = true
			break
		}
	}
	if !foundNew {
		t.Errorf("new task not ingested; SourceIDs: %v", taskSourceIDs(tasks))
	}
}

func TestSlackSync_NoTokenConfigured(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	// Explicit empty env (initTestRepo sets MONOLOG_DIR; token env is
	// independent). No token file written either.
	t.Setenv("MONOLOG_SLACK_TOKEN", "")

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-sync"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error when no token configured, got nil")
	}
	// Error text should steer the user to slack-login.
	if !strings.Contains(err.Error(), "slack-login") {
		t.Errorf("error should mention slack-login, got: %v", err)
	}
}

func TestSlackSync_NotAGitRepo(t *testing.T) {
	// Point MONOLOG_DIR at a real directory that is NOT a git repo. The
	// tasks dir is created by store.New but no commits are possible, so
	// the Ingest step fails via store.CreateBatch -> git.AutoCommit.
	dir := filepath.Join(t.TempDir(), "monolog")
	if err := os.MkdirAll(filepath.Join(dir, ".monolog"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("MONOLOG_DIR", dir)
	t.Setenv("MONOLOG_SLACK_TOKEN", "xoxp-test")

	mock := newSlackSyncMock()
	mock.pushStars(starsListBody(1712345678, 1))
	server := httptest.NewServer(mock)
	defer server.Close()

	restore := installSlackSyncClient(t, server)
	defer restore()

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-sync"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error on non-git dir, got nil (out=%s)", outBuf.String())
	}
}

// taskSourceIDs returns the SourceID field of each task. Useful for test
// assertions that need to list what's present on disk.
func taskSourceIDs(tasks []model.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.SourceID)
	}
	return ids
}
