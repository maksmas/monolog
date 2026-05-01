package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/store"
)

// initSyncRepo creates a fresh monolog git repo via git.Init and returns the
// repo path plus an opened Store rooted at the tasks directory. Used by every
// Sync test that needs to commit.
func initSyncRepo(t *testing.T) (string, *store.Store) {
	t.Helper()
	root := t.TempDir()
	repoPath := filepath.Join(root, "monolog")
	if err := git.Init(repoPath, ""); err != nil {
		t.Fatalf("git.Init: %v", err)
	}
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	s, err := store.New(tasksDir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	return repoPath, s
}

// gitLogSubjects returns commit subjects from newest to oldest.
func gitLogSubjects(t *testing.T, repoPath string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", repoPath, "log", "--pretty=%s")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// fakeWithMessages builds a fakeGmail backed by a sequential ID list and a
// matching message map. The IDs end up in API order (newest-first).
func fakeWithMessages(ids []string) *fakeGmail {
	msgs := make(map[string]*Message, len(ids))
	for _, id := range ids {
		msgs[id] = &Message{
			ID:      id,
			Subject: "subject " + id,
			From:    fmt.Sprintf("Sender %s <%s@example.com>", id, id),
			Snippet: "snippet for " + id,
		}
	}
	return &fakeGmail{listIDs: ids, messages: msgs}
}

func TestSync_FirstRunImportsAndCommits(t *testing.T) {
	repoPath, s := initSyncRepo(t)
	gmail := fakeWithMessages([]string{"a", "b", "c", "d", "e"})

	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	res := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label:      "monolog",
		MaxPerSync: 100,
		Now:        now,
	})

	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Created != 5 {
		t.Fatalf("Created=%d want 5", res.Created)
	}

	// Confirm 5 task files exist.
	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("len(tasks)=%d want 5", len(tasks))
	}
	// Every task has Source=gmail and a populated SourceID.
	seenSourceIDs := make(map[string]struct{})
	for _, tk := range tasks {
		if tk.Source != "gmail" {
			t.Fatalf("Source=%q want gmail", tk.Source)
		}
		if tk.SourceID == "" {
			t.Fatalf("empty SourceID for task %s", tk.ID)
		}
		seenSourceIDs[tk.SourceID] = struct{}{}
	}
	for _, want := range []string{"a", "b", "c", "d", "e"} {
		if _, ok := seenSourceIDs[want]; !ok {
			t.Fatalf("missing SourceID %q in stored tasks", want)
		}
	}

	// Exactly one new commit beyond the init commit, with the right
	// message format.
	subs := gitLogSubjects(t, repoPath)
	if len(subs) != 2 {
		t.Fatalf("git log subjects=%v want 2 entries", subs)
	}
	wantMsg := "email: imported 5 task(s) (label=monolog)"
	if subs[0] != wantMsg {
		t.Fatalf("commit subject=%q want %q", subs[0], wantMsg)
	}
}

func TestSync_SecondRunIsNoOp(t *testing.T) {
	repoPath, s := initSyncRepo(t)
	gmail := fakeWithMessages([]string{"a", "b", "c"})

	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	first := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 100, Now: now,
	})
	if first.Err != nil || first.Created != 3 {
		t.Fatalf("first run: created=%d err=%v", first.Created, first.Err)
	}

	commitsBefore := len(gitLogSubjects(t, repoPath))

	// Second run sees the same messages but skips them all via dedup.
	second := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 100, Now: now.Add(time.Hour),
	})
	if second.Err != nil {
		t.Fatalf("second run err: %v", second.Err)
	}
	if second.Created != 0 {
		t.Fatalf("second run Created=%d want 0", second.Created)
	}

	commitsAfter := len(gitLogSubjects(t, repoPath))
	if commitsAfter != commitsBefore {
		t.Fatalf("second run added commits: before=%d after=%d", commitsBefore, commitsAfter)
	}
}

func TestSync_PartialDedup(t *testing.T) {
	repoPath, s := initSyncRepo(t)
	// First three IDs already imported; next two are new.
	gmail := fakeWithMessages([]string{"old1", "old2", "old3", "new1", "new2"})
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)

	// Pre-seed by syncing with a fake that only knows about the first 3.
	preFake := fakeWithMessages([]string{"old1", "old2", "old3"})
	pre := Sync(context.Background(), preFake, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 100, Now: now,
	})
	if pre.Err != nil || pre.Created != 3 {
		t.Fatalf("pre-seed: %+v", pre)
	}

	res := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 100, Now: now.Add(time.Hour),
	})
	if res.Err != nil {
		t.Fatalf("err: %v", res.Err)
	}
	if res.Created != 2 {
		t.Fatalf("Created=%d want 2", res.Created)
	}

	subs := gitLogSubjects(t, repoPath)
	wantMsg := "email: imported 2 task(s) (label=monolog)"
	if subs[0] != wantMsg {
		t.Fatalf("commit subject=%q want %q", subs[0], wantMsg)
	}

	// All five SourceIDs should now be on disk.
	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("total tasks=%d want 5", len(tasks))
	}
}

func TestSync_DedupSpansDoneTasks(t *testing.T) {
	// A "done" gmail-sourced task must still suppress re-import even
	// though its status differs from "open" — the dedup pass uses
	// store.List with no Status filter.
	repoPath, s := initSyncRepo(t)
	gmail := fakeWithMessages([]string{"a", "b"})
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)

	res := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 100, Now: now,
	})
	if res.Err != nil || res.Created != 2 {
		t.Fatalf("first sync: %+v", res)
	}

	// Mark one of the imported tasks as done.
	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var doneOne bool
	for _, tk := range tasks {
		if tk.SourceID == "a" {
			tk.Status = "done"
			tk.CompletedAt = now.Format(time.RFC3339)
			if err := s.Update(tk); err != nil {
				t.Fatalf("update: %v", err)
			}
			doneOne = true
			break
		}
	}
	if !doneOne {
		t.Fatal("did not find the seeded task to mark done")
	}

	// Second sync — the done task must still suppress re-import.
	res2 := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 100, Now: now.Add(time.Hour),
	})
	if res2.Err != nil {
		t.Fatalf("second sync err: %v", res2.Err)
	}
	if res2.Created != 0 {
		t.Fatalf("second sync Created=%d want 0", res2.Created)
	}
}

func TestSync_SoftCapHonored(t *testing.T) {
	repoPath, s := initSyncRepo(t)
	gmail := fakeWithMessages([]string{"a", "b", "c", "d", "e"})
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)

	first := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 3, Now: now,
	})
	if first.Err != nil {
		t.Fatalf("first err: %v", first.Err)
	}
	if first.Created != 3 {
		t.Fatalf("first Created=%d want 3", first.Created)
	}

	// Confirm the first three IDs (newest-first) were created.
	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	gotSrc := make(map[string]struct{})
	for _, tk := range tasks {
		gotSrc[tk.SourceID] = struct{}{}
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, ok := gotSrc[id]; !ok {
			t.Fatalf("missing SourceID %q after capped run", id)
		}
	}
	for _, id := range []string{"d", "e"} {
		if _, ok := gotSrc[id]; ok {
			t.Fatalf("unexpected SourceID %q after capped run", id)
		}
	}

	// Second run drains the remaining two.
	second := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 3, Now: now.Add(time.Hour),
	})
	if second.Err != nil {
		t.Fatalf("second err: %v", second.Err)
	}
	if second.Created != 2 {
		t.Fatalf("second Created=%d want 2", second.Created)
	}
}

func TestSync_NoMessagesNoCommit(t *testing.T) {
	repoPath, s := initSyncRepo(t)
	gmail := &fakeGmail{} // empty ListLabeled
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)

	commitsBefore := len(gitLogSubjects(t, repoPath))

	res := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 100, Now: now,
	})
	if res.Err != nil {
		t.Fatalf("err: %v", res.Err)
	}
	if res.Created != 0 {
		t.Fatalf("Created=%d want 0", res.Created)
	}

	commitsAfter := len(gitLogSubjects(t, repoPath))
	if commitsAfter != commitsBefore {
		t.Fatalf("commits added on no-op: before=%d after=%d", commitsBefore, commitsAfter)
	}
}

func TestSync_PartialCreateFailureCommitsRest(t *testing.T) {
	// Pre-seed the store with a task whose ID collides with the ULID we
	// can't predict, so we use a different mechanism: configure the fake
	// Get to fail on one specific id. The id with the failing Get is
	// skipped (warned to writer), and the rest are committed in a single
	// batch.
	repoPath, s := initSyncRepo(t)
	gmail := fakeWithMessages([]string{"a", "b", "c", "d", "e"})

	// Wrap the fake to make Get fail on id "c".
	wrapped := &failingGetGmail{inner: gmail, failID: "c"}
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)

	var buf bytes.Buffer
	res := Sync(context.Background(), wrapped, s, repoPath, SyncOptions{
		Label:      "monolog",
		MaxPerSync: 100,
		Now:        now,
		Writer:     &buf,
	})
	if res.Err != nil {
		t.Fatalf("err: %v", res.Err)
	}
	if res.Created != 4 {
		t.Fatalf("Created=%d want 4", res.Created)
	}
	if !strings.Contains(buf.String(), "email: get c") {
		t.Fatalf("warning not written for failing id, got %q", buf.String())
	}

	subs := gitLogSubjects(t, repoPath)
	if !strings.HasPrefix(subs[0], "email: imported 4 task(s)") {
		t.Fatalf("commit subject=%q expected '4 task(s)' prefix", subs[0])
	}
}

func TestSync_ListLabeledErrorAborts(t *testing.T) {
	repoPath, s := initSyncRepo(t)
	gmail := &fakeGmail{listErr: errors.New("api down")}
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)

	commitsBefore := len(gitLogSubjects(t, repoPath))

	res := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 100, Now: now,
	})
	if res.Err == nil {
		t.Fatal("expected non-nil Err on ListLabeled failure")
	}
	if res.Created != 0 {
		t.Fatalf("Created=%d want 0", res.Created)
	}

	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks created on list error: %d", len(tasks))
	}

	commitsAfter := len(gitLogSubjects(t, repoPath))
	if commitsAfter != commitsBefore {
		t.Fatalf("commits added on list error: before=%d after=%d", commitsBefore, commitsAfter)
	}
}

func TestSync_EmptyLabelRejected(t *testing.T) {
	repoPath, s := initSyncRepo(t)
	res := Sync(context.Background(), &fakeGmail{}, s, repoPath, SyncOptions{
		Label: "", MaxPerSync: 100, Now: time.Now(),
	})
	if res.Err == nil {
		t.Fatal("expected err for empty label")
	}
}

func TestSync_NilArgsRejected(t *testing.T) {
	repoPath, s := initSyncRepo(t)
	now := time.Now()

	if res := Sync(context.Background(), nil, s, repoPath, SyncOptions{Label: "monolog", Now: now}); res.Err == nil {
		t.Fatal("expected err for nil gmail client")
	}
	if res := Sync(context.Background(), &fakeGmail{}, nil, repoPath, SyncOptions{Label: "monolog", Now: now}); res.Err == nil {
		t.Fatal("expected err for nil store")
	}
}

func TestSync_PreservesNewestFirstOrder(t *testing.T) {
	// With MaxPerSync=2, only the first two IDs from ListLabeled are
	// imported. The fake returns them in newest-first order, so we must
	// see "a" and "b" (not "d" and "e").
	repoPath, s := initSyncRepo(t)
	gmail := fakeWithMessages([]string{"a", "b", "c", "d", "e"})
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)

	res := Sync(context.Background(), gmail, s, repoPath, SyncOptions{
		Label: "monolog", MaxPerSync: 2, Now: now,
	})
	if res.Err != nil || res.Created != 2 {
		t.Fatalf("res=%+v", res)
	}

	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make(map[string]struct{})
	for _, tk := range tasks {
		got[tk.SourceID] = struct{}{}
	}
	for _, want := range []string{"a", "b"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("missing newest-first SourceID %q", want)
		}
	}
}

// failingGetGmail wraps a fakeGmail and forces Get to fail for a specific id.
// Used by the partial-failure test.
type failingGetGmail struct {
	inner  *fakeGmail
	failID string
}

func (f *failingGetGmail) ListLabeled(ctx context.Context, label string) ([]string, error) {
	return f.inner.ListLabeled(ctx, label)
}

func (f *failingGetGmail) Get(ctx context.Context, id string) (*Message, error) {
	if id == f.failID {
		return nil, errors.New("simulated get failure")
	}
	return f.inner.Get(ctx, id)
}

func (f *failingGetGmail) ArchiveLabel(ctx context.Context, id string) error {
	return f.inner.ArchiveLabel(ctx, id)
}

var _ Gmail = (*failingGetGmail)(nil)
