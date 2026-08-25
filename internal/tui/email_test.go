package tui

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/email"
	"github.com/maksmas/monolog/internal/git"
	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/store"
)

// fakeGmail is a recording stub used by the tui-level email tests.
type fakeGmail struct {
	listIDs    []string
	listErr    error
	messages   map[string]*email.Message
	getErr     error
	archiveErr error
	archived   []string
}

func (f *fakeGmail) ListLabeled(ctx context.Context, label string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]string, len(f.listIDs))
	copy(out, f.listIDs)
	return out, nil
}

func (f *fakeGmail) Get(ctx context.Context, id string) (*email.Message, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	m, ok := f.messages[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *m
	return &cp, nil
}

func (f *fakeGmail) ArchiveLabel(ctx context.Context, id string) error {
	if f.archiveErr != nil {
		return f.archiveErr
	}
	f.archived = append(f.archived, id)
	return nil
}

var _ email.Gmail = (*fakeGmail)(nil)

// stubEmailClientBuilder swaps the package-level emailClientBuilder so the
// goroutine launched by emailSyncCmd uses a fake instead of trying to load
// an OAuth token from disk.
func stubEmailClientBuilder(t *testing.T, g email.Gmail, retErr error) {
	t.Helper()
	prev := emailClientBuilder
	emailClientBuilder = func(ctx context.Context, ec config.EmailConfig) (email.Gmail, error) {
		if retErr != nil {
			return nil, retErr
		}
		return g, nil
	}
	t.Cleanup(func() { emailClientBuilder = prev })
}

// enableTUIEmailConfig writes the email block to config.json for the given
// monolog repo and reloads config. Returns the email config for further
// inspection. Mirrors enableEmailConfig in cmd/email_test.go but lives here
// so the tui package does not have to reach into the cmd package.
func enableTUIEmailConfig(t *testing.T, monologDir string) config.EmailConfig {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	ec := config.EmailConfig{
		Enabled:           true,
		Label:             "monolog",
		SyncInterval:      5 * time.Minute,
		MaxPerSync:        100,
		ClientSecretsPath: filepath.Join(xdg, "monolog", "gmail_credentials.json"),
	}
	if err := config.SaveEmail(monologDir, ec); err != nil {
		t.Fatalf("SaveEmail: %v", err)
	}
	if err := config.Load(monologDir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return ec
}

// newTestModelWithEmail wires up a TUI Model that sees emailEnabled=true.
// Mirrors newTestModelWithOpts but applies the email block to config.json
// before constructing the Model so its constructor reads the enabled state.
func newTestModelWithEmail(t *testing.T, tasks ...model.Task) *Model {
	t.Helper()
	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := git.Init(repoPath, ""); err != nil {
		t.Fatalf("git.Init: %v", err)
	}
	t.Setenv("MONOLOG_DIR", repoPath)
	enableTUIEmailConfig(t, repoPath)

	s, err := store.New(filepath.Join(repoPath, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	for _, task := range tasks {
		if err := s.Create(task); err != nil {
			t.Fatalf("store.Create: %v", err)
		}
	}
	if len(tasks) > 0 {
		if err := git.AutoCommit(repoPath, "seed", "."); err != nil {
			t.Fatalf("seed commit: %v", err)
		}
	}
	m, err := newModel(s, repoPath, Options{})
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	return m
}

func TestNewModel_PopulatesEmailFieldsFromConfig(t *testing.T) {
	m := newTestModelWithEmail(t)
	if !m.emailEnabled {
		t.Errorf("emailEnabled = false, want true")
	}
	if m.emailLabel != "monolog" {
		t.Errorf("emailLabel = %q, want %q", m.emailLabel, "monolog")
	}
	if m.emailMaxPerSync != 100 {
		t.Errorf("emailMaxPerSync = %d, want 100", m.emailMaxPerSync)
	}
	if m.emailInterval != 5*time.Minute {
		t.Errorf("emailInterval = %s, want 5m", m.emailInterval)
	}
}

func TestNewModel_EmailDisabledByDefault(t *testing.T) {
	// newTestModel uses newTestModelWithOpts which calls config.Load on the
	// repo's config.json (written by git.Init). Without an explicit
	// SaveEmail call the email block is absent and Enabled defaults to
	// false.
	m := newTestModel(t)
	if m.emailEnabled {
		t.Errorf("emailEnabled = true, want false (no email block in config)")
	}
}

func TestEmailSyncCmd_DisabledReturnsNoOp(t *testing.T) {
	m := newTestModel(t) // email disabled
	cmd := m.emailSyncCmd()
	if cmd == nil {
		t.Fatal("emailSyncCmd returned nil even when disabled — must return a no-op cmd so callers can batch unconditionally")
	}
	msg := cmd()
	if _, ok := msg.(emailNoOpMsg); !ok {
		t.Errorf("emailSyncCmd disabled produced %T, want emailNoOpMsg", msg)
	}
}

func TestEmailSyncCmd_EnabledRunsSyncWithFakeGmail(t *testing.T) {
	m := newTestModelWithEmail(t)

	fake := &fakeGmail{
		listIDs: []string{"msg1", "msg2", "msg3"},
		messages: map[string]*email.Message{
			"msg1": {ID: "msg1", Subject: "Hello", From: "Alice <a@x.com>", Snippet: "snip1"},
			"msg2": {ID: "msg2", Subject: "World", From: "Bob <b@x.com>", Snippet: "snip2"},
			"msg3": {ID: "msg3", Subject: "Foo", From: "Carol <c@x.com>", Snippet: "snip3"},
		},
	}
	stubEmailClientBuilder(t, fake, nil)

	cmd := m.emailSyncCmd()
	msg := cmd()
	res, ok := msg.(emailSyncResult)
	if !ok {
		t.Fatalf("emailSyncCmd produced %T, want emailSyncResult", msg)
	}
	if res.err != nil {
		t.Fatalf("res.err = %v, want nil", res.err)
	}
	if res.created != 3 {
		t.Errorf("res.created = %d, want 3", res.created)
	}
}

func TestEmailSyncCmd_BuilderErrorPropagates(t *testing.T) {
	m := newTestModelWithEmail(t)

	// Simulate a missing-token error like LoadToken would surface.
	stubEmailClientBuilder(t, nil, errors.New("no token: run monolog email auth"))

	cmd := m.emailSyncCmd()
	msg := cmd()
	res, ok := msg.(emailSyncResult)
	if !ok {
		t.Fatalf("got %T, want emailSyncResult", msg)
	}
	if res.err == nil {
		t.Fatal("res.err = nil, want non-nil from builder failure")
	}
	if res.created != 0 {
		t.Errorf("res.created = %d, want 0 on builder failure", res.created)
	}
}

func TestSKey_DispatchesGitOnly_WhenEmailDisabled(t *testing.T) {
	m := newTestModel(t)
	if m.emailEnabled {
		t.Fatal("precondition: emailEnabled should be false")
	}
	if m.emailSyncing {
		t.Fatal("precondition: emailSyncing should be false")
	}

	_, cmd := key(t, m, "s")
	if cmd == nil {
		t.Fatal("s should return a cmd")
	}
	// Disabled path returns the bare git syncCmd, not a Batch. Confirm by
	// asserting the produced msg is NOT a tea.BatchMsg (the wrapper Bubble
	// Tea uses for tea.Batch).
	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); ok {
		t.Errorf("disabled-email s key produced a BatchMsg; expected single git syncCmd")
	}
}

func TestSKey_DispatchesBatch_WhenEmailEnabled(t *testing.T) {
	m := newTestModelWithEmail(t)
	// Stub the builder so the real-network path is never taken even if the
	// email cmd somehow runs synchronously inside Batch.
	stubEmailClientBuilder(t, &fakeGmail{}, nil)

	if !m.emailEnabled {
		t.Fatal("precondition: emailEnabled should be true")
	}
	if m.emailSyncing {
		t.Fatal("precondition: emailSyncing should be false before s")
	}

	m, cmd := key(t, m, "s")
	if cmd == nil {
		t.Fatal("s should return a cmd")
	}
	if !m.emailSyncing {
		t.Errorf("emailSyncing not set true after s with email enabled")
	}
	msg := cmd()
	cmds, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("got %T, want tea.BatchMsg", msg)
	}
	if len(cmds) != 2 {
		t.Errorf("Batch len = %d, want 2 (git+email)", len(cmds))
	}
}

func TestEmailSyncResult_ClearsSpinnerAndFlashes(t *testing.T) {
	m := newTestModelWithEmail(t)
	m.emailSyncing = true

	next, _ := m.Update(emailSyncResult{created: 3, err: nil})
	m = next.(*Model)

	if m.emailSyncing {
		t.Errorf("emailSyncing not cleared after success result")
	}
	if m.statusMsg == "" {
		t.Errorf("statusMsg empty after success result, want flash")
	}
	if !contains(m.statusMsg, "3") || !contains(m.statusMsg, "imported") {
		t.Errorf("statusMsg = %q, expected to mention 3 imported", m.statusMsg)
	}
}

func TestEmailSyncResult_ErrorFlashesAndClearsSpinner(t *testing.T) {
	m := newTestModelWithEmail(t)
	m.emailSyncing = true

	next, _ := m.Update(emailSyncResult{err: errors.New("token expired")})
	m = next.(*Model)

	if m.emailSyncing {
		t.Errorf("emailSyncing not cleared after error result")
	}
	if !contains(m.statusMsg, "token expired") {
		t.Errorf("statusMsg = %q, expected to contain error text", m.statusMsg)
	}
}

func TestEmailSyncResult_ZeroCreatedSkipsReload(t *testing.T) {
	// Setup: seed a task, snapshot the current allTasks count, then dispatch
	// an emailSyncResult with created=0 — reloadAll should NOT fire so the
	// allTasks slice identity remains the same.
	m := newTestModelWithEmail(t,
		model.Task{ID: "01TEST", Title: "seed", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.emailSyncing = true
	beforeLen := len(m.allTasks)

	next, _ := m.Update(emailSyncResult{created: 0})
	m = next.(*Model)

	if m.emailSyncing {
		t.Errorf("emailSyncing not cleared")
	}
	if len(m.allTasks) != beforeLen {
		t.Errorf("allTasks length changed (%d→%d) — reload should be skipped on created=0",
			beforeLen, len(m.allTasks))
	}
	if !contains(m.statusMsg, "0 imported") {
		t.Errorf("statusMsg = %q, expected to mention '0 imported'", m.statusMsg)
	}
}

func TestEmailSyncResult_PositiveCreatedTriggersReload(t *testing.T) {
	// Seed two tasks and produce an emailSyncResult{created: 1}. Even
	// though the gmail import did not actually happen in this test (we
	// just dispatched the message), the handler should call reloadAll
	// which keeps the existing tasks visible. The smoke check is that
	// reloadAll did not error and statusMsg reflects the count.
	m := newTestModelWithEmail(t,
		model.Task{ID: "01ONE", Title: "alpha", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.emailSyncing = true

	next, _ := m.Update(emailSyncResult{created: 2})
	m = next.(*Model)

	if m.err != nil {
		t.Errorf("reloadAll error after positive created: %v", m.err)
	}
	if !contains(m.statusMsg, "2 imported") {
		t.Errorf("statusMsg = %q, expected '2 imported'", m.statusMsg)
	}
}

// TestEmailSyncResult_PositiveCreatedDispatchesPush pins that email.Sync's
// batch commit reaches the remote. The email path returns emailSyncResult, not
// taskSavedMsg, so the taskSavedMsg push trigger never fires for it — without
// this dispatch Gmail-imported tasks stay local forever.
func TestEmailSyncResult_PositiveCreatedDispatchesPush(t *testing.T) {
	m := newTestModelWithEmail(t,
		model.Task{ID: "01ONE", Title: "alpha", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	rec := stubAutoPush(t, git.PushResult{Pushed: true}, nil)
	m.emailSyncing = true

	next, cmd := m.Update(emailSyncResult{created: 2})
	m = next.(*Model)

	if !m.pushInFlight {
		t.Error("pushInFlight = false after an email import; the coalescing gate must be armed")
	}
	pushes := msgsOfType[autoPushResult](runCmds(cmd))
	if len(pushes) != 1 {
		t.Fatalf("got %d autoPushResult msgs, want 1", len(pushes))
	}
	if rec.count() != 1 {
		t.Fatalf("runAutoPush calls = %d, want 1", rec.count())
	}
	if rec.path(0) != m.repoPath {
		t.Errorf("push repoPath = %q, want %q", rec.path(0), m.repoPath)
	}
	if rec.timeout(0) != git.DefaultPushTimeout {
		t.Errorf("push timeout = %v, want git.DefaultPushTimeout (%v)", rec.timeout(0), git.DefaultPushTimeout)
	}
}

// TestEmailSyncResult_TagViewCursorSkipsSeparator pins that the email reload
// leaves the cursor on a real task in tag view. emailSyncResult used to reload
// and recompute the layout without the separator guard every other reload
// branch ran, so an import landing while the cursor sat on a bucket separator
// left it parked there — selectedTask() returns nil on a separator, so the
// next `d`/`e`/`Enter` would silently do nothing.
func TestEmailSyncResult_TagViewCursorSkipsSeparator(t *testing.T) {
	m := newTestModelWithEmail(t,
		model.Task{ID: "01TVS1", Title: "tagged task", Status: "open",
			Schedule: "today", Tags: []string{"work"}, Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.width = 80
	m.height = 40
	m.recomputeLayout()

	// Tag view: each tab starts with a bucket separator at index 0.
	m, _ = key(t, m, "v")
	m.activeTab = findTabByLabel(t, m, "work")
	m.lists[m.activeTab].Select(0)
	if !m.lists[m.activeTab].Items()[0].(item).isSeparator {
		t.Fatal("precondition: index 0 of a tag tab should be a bucket separator")
	}

	m.emailSyncing = true
	next, _ := m.Update(emailSyncResult{created: 1})
	m = next.(*Model)

	if m.selectedTask() == nil {
		t.Errorf("cursor rests on a separator after an email import (index %d); "+
			"the reload must skip past it in tag view", m.lists[m.activeTab].Index())
	}
}

// TestEmailSyncResult_NoPushWithoutCommit covers the emailSyncResult shapes
// that carry no commit: a zero-created run (email.Sync skips its batch commit
// entirely) and a failed run — including the created>0-but-commit-failed case,
// where the tasks are on disk but nothing was committed.
func TestEmailSyncResult_NoPushWithoutCommit(t *testing.T) {
	cases := []struct {
		name string
		msg  emailSyncResult
	}{
		{"zero created", emailSyncResult{created: 0}},
		{"list failed", emailSyncResult{err: errors.New("list labeled: api down")}},
		{"commit failed after writes", emailSyncResult{created: 2, err: errors.New("auto-commit: boom")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newTestModelWithEmail(t,
				model.Task{ID: "01ONE", Title: "alpha", Status: "open", Schedule: "today",
					Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
			)
			rec := stubAutoPush(t, git.PushResult{Pushed: true}, nil)
			m.emailSyncing = true

			next, cmd := m.Update(c.msg)
			m = next.(*Model)

			if m.pushInFlight {
				t.Error("pushInFlight = true with no commit to push")
			}
			if got := msgsOfType[autoPushResult](runCmds(cmd)); len(got) != 0 {
				t.Errorf("got %d autoPushResult msgs, want 0", len(got))
			}
			if rec.count() != 0 {
				t.Errorf("runAutoPush calls = %d, want 0", rec.count())
			}
		})
	}
}

// TestEmailSyncResult_NoPushWhenAutoPushDisabled pins the off switch on the
// email path too: an import still reloads and flashes, but never pushes.
func TestEmailSyncResult_NoPushWhenAutoPushDisabled(t *testing.T) {
	t.Setenv("MONOLOG_NO_AUTOPUSH", "1")
	m := newTestModelWithEmail(t,
		model.Task{ID: "01ONE", Title: "alpha", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	if m.autoPushEnabled {
		t.Fatal("precondition: auto-push should be disabled by MONOLOG_NO_AUTOPUSH=1")
	}
	rec := stubAutoPush(t, git.PushResult{Pushed: true}, nil)

	next, cmd := m.Update(emailSyncResult{created: 2})
	m = next.(*Model)

	if m.pushInFlight {
		t.Error("pushInFlight = true with auto-push disabled")
	}
	if got := msgsOfType[autoPushResult](runCmds(cmd)); len(got) != 0 {
		t.Errorf("got %d autoPushResult msgs with auto-push disabled, want 0", len(got))
	}
	if rec.count() != 0 {
		t.Errorf("runAutoPush calls = %d with auto-push disabled, want 0", rec.count())
	}
	if !contains(m.statusMsg, "2 imported") {
		t.Errorf("statusMsg = %q — the import flash must survive with pushing off", m.statusMsg)
	}
}

// TestEmailSyncResult_PushCoalescesWithInFlightPush pins that an email import
// landing while a mutation's push is still in flight does not start a second
// concurrent push — it sets pushPending and the in-flight push's handler fires
// the single follow-up.
func TestEmailSyncResult_PushCoalescesWithInFlightPush(t *testing.T) {
	m := newTestModelWithEmail(t,
		model.Task{ID: "01ONE", Title: "alpha", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	rec := stubAutoPush(t, git.PushResult{Pushed: true}, nil)
	m.pushInFlight = true

	next, cmd := m.Update(emailSyncResult{created: 1})
	m = next.(*Model)

	if !m.pushPending {
		t.Error("pushPending = false; an import during an in-flight push must queue a follow-up")
	}
	if got := msgsOfType[autoPushResult](runCmds(cmd)); len(got) != 0 {
		t.Errorf("got %d autoPushResult msgs, want 0 — no second concurrent push", len(got))
	}
	if rec.count() != 0 {
		t.Errorf("runAutoPush calls = %d, want 0 while a push is in flight", rec.count())
	}

	// The in-flight push returning drains the pending flag with exactly one push.
	next, cmd = m.Update(autoPushResult{})
	m = next.(*Model)
	if m.pushPending {
		t.Error("pushPending not cleared by the in-flight push's result")
	}
	if got := msgsOfType[autoPushResult](runCmds(cmd)); len(got) != 1 {
		t.Errorf("got %d autoPushResult msgs from the coalesced follow-up, want 1", len(got))
	}
	if rec.count() != 1 {
		t.Errorf("runAutoPush calls = %d after the follow-up, want 1", rec.count())
	}
}

// TestEmailSync_EndToEndDispatchesPush drives the whole email path — real
// email.Sync against a fake Gmail, real batch commit, real emailSyncResult —
// rather than a synthesized message, so the wiring cannot rot while the
// message-level tests keep passing.
func TestEmailSync_EndToEndDispatchesPush(t *testing.T) {
	m := newTestModelWithEmail(t)
	fake := &fakeGmail{
		listIDs: []string{"msg1"},
		messages: map[string]*email.Message{
			"msg1": {ID: "msg1", Subject: "Hello", From: "Alice <a@x.com>", Snippet: "snip1"},
		},
	}
	stubEmailClientBuilder(t, fake, nil)
	rec := stubAutoPush(t, git.PushResult{Pushed: true}, nil)

	res, ok := m.emailSyncCmd()().(emailSyncResult)
	if !ok {
		t.Fatalf("emailSyncCmd did not produce an emailSyncResult")
	}
	if res.err != nil || res.created != 1 {
		t.Fatalf("sync res = %+v, want created 1 and no error", res)
	}

	next, cmd := m.Update(res)
	m = next.(*Model)
	if got := msgsOfType[autoPushResult](runCmds(cmd)); len(got) != 1 {
		t.Fatalf("got %d autoPushResult msgs from the real email flow, want 1", len(got))
	}
	if rec.count() != 1 {
		t.Errorf("runAutoPush calls = %d, want 1", rec.count())
	}
	// The reload ran, so the imported task is visible.
	if len(m.allTasks) != 1 {
		t.Errorf("allTasks = %d after import, want 1", len(m.allTasks))
	}
}

func TestEmailNoOpMsg_IsAccepted(t *testing.T) {
	// Confirm the Update handler swallows the no-op message without any
	// state change.
	m := newTestModel(t)
	statusBefore := m.statusMsg
	syncingBefore := m.emailSyncing

	next, cmd := m.Update(emailNoOpMsg{})
	m = next.(*Model)

	if cmd != nil {
		t.Errorf("emailNoOpMsg returned cmd %v, want nil", cmd)
	}
	if m.statusMsg != statusBefore {
		t.Errorf("emailNoOpMsg mutated statusMsg %q→%q", statusBefore, m.statusMsg)
	}
	if m.emailSyncing != syncingBefore {
		t.Errorf("emailNoOpMsg mutated emailSyncing %v→%v", syncingBefore, m.emailSyncing)
	}
}

func TestInit_EmailDisabledReturnsNil(t *testing.T) {
	m := newTestModel(t)
	if m.emailEnabled {
		t.Fatal("precondition: email should be disabled")
	}
	if cmd := m.Init(); cmd != nil {
		t.Errorf("Init with email disabled returned %v, want nil", cmd)
	}
}

func TestInit_EmailEnabledReturnsBatchWithSyncAndTick(t *testing.T) {
	m := newTestModelWithEmail(t)
	stubEmailClientBuilder(t, &fakeGmail{}, nil)

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned nil with email enabled, want Batch")
	}
	if !m.emailSyncing {
		t.Errorf("emailSyncing not set true after Init with email enabled")
	}
	msg := cmd()
	cmds, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init result type %T, want tea.BatchMsg", msg)
	}
	// Batch should contain the immediate sync cmd and the ticker cmd. The
	// ticker is only included when interval > 0 (which it is, 5m by default).
	if len(cmds) != 2 {
		t.Errorf("Init batch len = %d, want 2 (sync + tick)", len(cmds))
	}
}

func TestInit_EmailEnabledZeroIntervalSkipsTicker(t *testing.T) {
	m := newTestModelWithEmail(t)
	stubEmailClientBuilder(t, &fakeGmail{}, nil)
	// Force interval to zero — emailTickCmd should return nil and tea.Batch
	// drops it. The remaining single cmd is the immediate sync.
	m.emailInterval = 0

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned nil, want sync cmd")
	}
	msg := cmd()
	cmds, ok := msg.(tea.BatchMsg)
	if !ok {
		// A single non-nil cmd batched with a nil cmd may collapse into a
		// non-batch message depending on bubbletea internals. Either way
		// there should be no tick scheduled — so this branch is acceptable
		// as long as the message isn't a tick.
		if _, isTick := msg.(emailTickMsg); isTick {
			t.Fatalf("Init produced an emailTickMsg with interval=0; tick should be disabled")
		}
		return
	}
	// Defensive — even when bubbletea returns a Batch with one nil entry, none
	// of the entries should be a tick cmd.
	for i, c := range cmds {
		if c == nil {
			continue
		}
		// Cannot inspect a tea.Cmd directly; just ensure none of them yield
		// an emailTickMsg synchronously. The sync cmd yields emailSyncResult.
		out := c()
		if _, isTick := out.(emailTickMsg); isTick {
			t.Errorf("Init batch[%d] produced emailTickMsg with interval=0", i)
		}
	}
}

func TestEmailTickCmd_ReturnsNilWhenDisabled(t *testing.T) {
	m := newTestModel(t) // email disabled
	if cmd := m.emailTickCmd(5 * time.Minute); cmd != nil {
		t.Errorf("emailTickCmd with email disabled = %v, want nil", cmd)
	}
}

func TestEmailTickCmd_ReturnsNilWhenIntervalZero(t *testing.T) {
	m := newTestModelWithEmail(t)
	if cmd := m.emailTickCmd(0); cmd != nil {
		t.Errorf("emailTickCmd with interval=0 = %v, want nil", cmd)
	}
}

func TestEmailTickCmd_ReturnsTickerWhenEnabled(t *testing.T) {
	m := newTestModelWithEmail(t)
	cmd := m.emailTickCmd(10 * time.Millisecond)
	if cmd == nil {
		t.Fatal("emailTickCmd returned nil with enabled+positive interval")
	}
	// Run the cmd — it should block briefly and yield emailTickMsg.
	msg := cmd()
	if _, ok := msg.(emailTickMsg); !ok {
		t.Errorf("emailTickCmd produced %T, want emailTickMsg", msg)
	}
}

func TestEmailTickMsg_RearmsAndDispatchesSync(t *testing.T) {
	m := newTestModelWithEmail(t)
	stubEmailClientBuilder(t, &fakeGmail{}, nil)
	if m.emailSyncing {
		t.Fatal("precondition: emailSyncing should be false")
	}

	next, cmd := m.Update(emailTickMsg{})
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("emailTickMsg produced no cmd; want Batch(sync, tick)")
	}
	if !m.emailSyncing {
		t.Errorf("emailSyncing not set true on tick handler")
	}
	msg := cmd()
	cmds, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("tick handler result type %T, want tea.BatchMsg", msg)
	}
	if len(cmds) != 2 {
		t.Errorf("tick handler batch len = %d, want 2 (sync + tick)", len(cmds))
	}
}

func TestEmailTickMsg_NoOpWhenDisabled(t *testing.T) {
	// Edge case: a stale tick fires after email is disabled (settings modal
	// will eventually let users toggle). The handler must short-circuit so
	// the loop unwinds.
	m := newTestModelWithEmail(t)
	m.emailEnabled = false

	next, cmd := m.Update(emailTickMsg{})
	m = next.(*Model)
	if cmd != nil {
		t.Errorf("disabled tick handler returned cmd %v, want nil", cmd)
	}
	if m.emailSyncing {
		t.Errorf("disabled tick handler set emailSyncing=true")
	}
}

// contains is a tiny stdlib-free substring helper.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestEmailTokenPathFor_UsesPlatformSeparators pins that email.TokenPathFor
// derives the token path with filepath.Dir+filepath.Join rather than a
// byte-level '/' scan, so the result is correct on every platform Go's
// filepath package supports (notably Windows, where the separator is
// '\\'). Two assertions:
//   - Empty ClientSecretsPath yields an empty result (caller short-circuits).
//   - Non-empty ClientSecretsPath yields filepath.Join(Dir, gmail_token.json).
func TestEmailTokenPathFor_UsesPlatformSeparators(t *testing.T) {
	if got := email.TokenPathFor(""); got != "" {
		t.Errorf("email.TokenPathFor with empty ClientSecretsPath = %q, want empty", got)
	}
	cred := filepath.Join("some", "dir", "gmail_credentials.json")
	want := filepath.Join("some", "dir", "gmail_token.json")
	got := email.TokenPathFor(cred)
	if got != want {
		t.Errorf("email.TokenPathFor(%q) = %q, want %q", cred, got, want)
	}
}

// --- Task 11: archive-on-done + status indicator + help-hint ---

func TestArchiveEmailCmd_DisabledReturnsNil(t *testing.T) {
	m := newTestModel(t) // email disabled
	if cmd := m.archiveEmailCmd("msg1"); cmd != nil {
		t.Errorf("archiveEmailCmd with email disabled returned non-nil cmd; want nil so callers can batch unconditionally")
	}
}

func TestArchiveEmailCmd_EmptySourceIDReturnsNil(t *testing.T) {
	m := newTestModelWithEmail(t)
	if cmd := m.archiveEmailCmd(""); cmd != nil {
		t.Errorf("archiveEmailCmd with empty sourceID returned non-nil cmd; want nil")
	}
}

func TestArchiveEmailCmd_SuccessYieldsArchiveResultNoErr(t *testing.T) {
	m := newTestModelWithEmail(t)
	fake := &fakeGmail{}
	stubEmailClientBuilder(t, fake, nil)

	cmd := m.archiveEmailCmd("msg1")
	if cmd == nil {
		t.Fatal("archiveEmailCmd returned nil with email enabled and non-empty sourceID")
	}
	res, ok := cmd().(archiveResult)
	if !ok {
		t.Fatalf("archiveEmailCmd produced %T, want archiveResult", cmd())
	}
	if res.err != nil {
		t.Errorf("res.err = %v, want nil", res.err)
	}
	if len(fake.archived) != 1 || fake.archived[0] != "msg1" {
		t.Errorf("fake.archived = %v, want [msg1]", fake.archived)
	}
}

func TestArchiveEmailCmd_BuilderErrorPropagates(t *testing.T) {
	m := newTestModelWithEmail(t)
	stubEmailClientBuilder(t, nil, errors.New("no token: run monolog email auth"))

	cmd := m.archiveEmailCmd("msg1")
	if cmd == nil {
		t.Fatal("archiveEmailCmd returned nil")
	}
	res, ok := cmd().(archiveResult)
	if !ok {
		t.Fatalf("got %T, want archiveResult", cmd())
	}
	if res.err == nil {
		t.Error("res.err = nil, want non-nil from builder failure")
	}
}

func TestArchiveEmailCmd_ArchiveErrorPropagates(t *testing.T) {
	m := newTestModelWithEmail(t)
	fake := &fakeGmail{archiveErr: errors.New("403 forbidden")}
	stubEmailClientBuilder(t, fake, nil)

	cmd := m.archiveEmailCmd("msg1")
	res, ok := cmd().(archiveResult)
	if !ok {
		t.Fatalf("got %T, want archiveResult", cmd())
	}
	if res.err == nil {
		t.Error("res.err = nil, want non-nil from archive failure")
	}
}

func TestArchiveResult_SuccessFlashesEmailArchived(t *testing.T) {
	m := newTestModel(t)
	m.statusMsg = "stale"

	next, cmd := m.Update(archiveResult{})
	m = next.(*Model)
	if cmd != nil {
		t.Errorf("archiveResult success returned cmd %v, want nil", cmd)
	}
	if !contains(m.statusMsg, "email archived") {
		t.Errorf("statusMsg = %q, want to contain 'email archived'", m.statusMsg)
	}
}

func TestArchiveResult_ErrorFlashesArchiveFailed(t *testing.T) {
	m := newTestModel(t)

	next, _ := m.Update(archiveResult{err: errors.New("network fail")})
	m = next.(*Model)
	if !contains(m.statusMsg, "archive failed") {
		t.Errorf("statusMsg = %q, want to contain 'archive failed'", m.statusMsg)
	}
	if !contains(m.statusMsg, "network fail") {
		t.Errorf("statusMsg = %q, want to contain underlying error 'network fail'", m.statusMsg)
	}
}

// TestDoneOnGmailTask_ArchivesViaSavedMsg pins the end-to-end TUI flow:
// pressing 'd' on a gmail-sourced task with email enabled produces a
// taskSavedMsg whose archiveSourceID is the task's SourceID. The Update
// handler kicks off the archive cmd which lands in the fake.
func TestDoneOnGmailTask_ArchivesViaSavedMsg(t *testing.T) {
	m := newTestModelWithEmail(t,
		model.Task{ID: "01GMAIL", Title: "from gmail", Status: "open",
			Schedule: "today", Position: 1000,
			Source:    "gmail",
			SourceID:  "msg-abc",
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	fake := &fakeGmail{}
	stubEmailClientBuilder(t, fake, nil)

	// Press 'd' — produces the doneSelected cmd. Run it to get taskSavedMsg.
	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("d returned nil cmd")
	}
	saved, ok := cmd().(taskSavedMsg)
	if !ok {
		t.Fatalf("d cmd produced %T, want taskSavedMsg", cmd())
	}
	if saved.archiveSourceID != "msg-abc" {
		t.Errorf("archiveSourceID = %q, want %q", saved.archiveSourceID, "msg-abc")
	}
	// Dispatch the saved msg into Update. It should return an archive cmd.
	// The test model has auto-push enabled (git.Init writes "auto_push": true
	// and the saved msg carries a commit SHA), so Update batches the archive
	// cmd with an auto-push cmd — hence runCmds/msgsOfType rather than a
	// direct type assertion on a lone cmd. The push seam is stubbed so this
	// test never shells out to the network.
	stubAutoPush(t, git.PushResult{Pushed: true}, nil)
	next, savedCmd := m.Update(saved)
	m = next.(*Model)
	if savedCmd == nil {
		t.Fatal("Update on taskSavedMsg with archiveSourceID returned nil cmd; want archiveEmailCmd")
	}
	// Run the batched cmds — fake records the archive call.
	msgs := runCmds(savedCmd)
	if got := msgsOfType[archiveResult](msgs); len(got) != 1 {
		t.Errorf("got %d archiveResult msgs from %v, want 1", len(got), msgs)
	}
	if len(fake.archived) != 1 || fake.archived[0] != "msg-abc" {
		t.Errorf("fake.archived = %v, want [msg-abc]", fake.archived)
	}
}

// TestDoneOnGmailTask_EmailDisabled_NoArchive pins that pressing 'd' on a
// gmail-sourced task with email disabled produces a taskSavedMsg whose
// archiveSourceID is empty — the Update handler returns no archive cmd.
func TestDoneOnGmailTask_EmailDisabled_NoArchive(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01GMAIL", Title: "from gmail", Status: "open",
			Schedule: "today", Position: 1000,
			Source:    "gmail",
			SourceID:  "msg-abc",
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	if m.emailEnabled {
		t.Fatal("precondition: email should be disabled")
	}
	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("d returned nil cmd")
	}
	saved, ok := cmd().(taskSavedMsg)
	if !ok {
		t.Fatalf("d cmd produced %T, want taskSavedMsg", cmd())
	}
	if saved.archiveSourceID != "" {
		t.Errorf("archiveSourceID = %q, want empty (email disabled)", saved.archiveSourceID)
	}
}

// TestDoneOnNonGmailTask_NoArchive pins that pressing 'd' on a non-gmail
// task (Source != "gmail") with email enabled does NOT trigger archive.
func TestDoneOnNonGmailTask_NoArchive(t *testing.T) {
	m := newTestModelWithEmail(t,
		model.Task{ID: "01MANUAL", Title: "typed manually", Status: "open",
			Schedule: "today", Position: 1000,
			Source:    "manual",
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	stubEmailClientBuilder(t, &fakeGmail{}, nil)

	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("d returned nil cmd")
	}
	saved, ok := cmd().(taskSavedMsg)
	if !ok {
		t.Fatalf("d cmd produced %T, want taskSavedMsg", cmd())
	}
	if saved.archiveSourceID != "" {
		t.Errorf("archiveSourceID = %q, want empty (Source != gmail)", saved.archiveSourceID)
	}
}

// TestDoneOnGmailTask_ArchiveFailureLeavesTaskDone pins the non-fatal
// contract: when the archive call returns an error, the task is still
// marked done in the store and the status flashes "archive failed".
func TestDoneOnGmailTask_ArchiveFailureLeavesTaskDone(t *testing.T) {
	m := newTestModelWithEmail(t,
		model.Task{ID: "01GMAIL", Title: "from gmail", Status: "open",
			Schedule: "today", Position: 1000,
			Source:    "gmail",
			SourceID:  "msg-abc",
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	fake := &fakeGmail{archiveErr: errors.New("network down")}
	stubEmailClientBuilder(t, fake, nil)

	// Auto-push is enabled in the test model and the done commit carries a
	// SHA, so Update batches archive + push; stub the push seam and unwrap
	// the batch below.
	stubAutoPush(t, git.PushResult{Pushed: true}, nil)
	m, cmd := key(t, m, "d")
	saved, _ := cmd().(taskSavedMsg)
	next, savedCmd := m.Update(saved)
	m = next.(*Model)

	// Verify the task moved to done in the store regardless of archive outcome.
	task, err := m.store.GetByPrefix("01GMAIL")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != "done" {
		t.Errorf("task.Status = %q, want done — archive failure must NOT roll back done", task.Status)
	}

	// Run archive cmd → archiveResult{err: ...} → flashes failure, leaves task done.
	if savedCmd == nil {
		t.Fatal("expected archive cmd to be returned from saved msg dispatch")
	}
	results := msgsOfType[archiveResult](runCmds(savedCmd))
	if len(results) != 1 {
		t.Fatalf("got %d archiveResult msgs, want 1", len(results))
	}
	res := results[0]
	if res.err == nil {
		t.Error("res.err = nil, want non-nil from fake archive error")
	}
	next, _ = m.Update(res)
	m = next.(*Model)
	if !contains(m.statusMsg, "archive failed") {
		t.Errorf("statusMsg = %q, want to flash archive failure", m.statusMsg)
	}
}

// TestStatsBar_SpinnerIndicator pins that the stats bar contains the ↻
// spinner character while emailSyncing is true and lacks it when false.
func TestStatsBar_SpinnerIndicator(t *testing.T) {
	m := newTestModelWithEmail(t)

	// Default state: no spinner.
	if contains(m.statsBarView(), "↻") {
		t.Errorf("statsBarView with emailSyncing=false contains ↻; want absent")
	}
	// Flip syncing on.
	m.emailSyncing = true
	if !contains(m.statsBarView(), "↻") {
		t.Errorf("statsBarView with emailSyncing=true missing ↻; want present")
	}
	// And back off.
	m.emailSyncing = false
	if contains(m.statsBarView(), "↻") {
		t.Errorf("statsBarView with emailSyncing=false (after toggle) contains ↻; want absent")
	}
}

// TestHelpLine_SyncCopyReflectsEmailEnabled pins that the bottom-bar help
// line reads "full sync (git+email)" when email is enabled and just
// "full sync" otherwise. Uses the rendered helpLine() string for a substring
// check. The "full" qualifier is load-bearing since auto-push landed: ordinary
// changes push without a keypress, so "s" is only needed for the round trip.
func TestHelpLine_SyncCopyReflectsEmailEnabled(t *testing.T) {
	// Disabled: should mention "full sync" but not "(git+email)".
	disabled := newTestModel(t)
	if disabled.emailEnabled {
		t.Fatal("precondition: disabled model should have email off")
	}
	dh := disabled.helpLine()
	if !contains(dh, "full sync") {
		t.Errorf("disabled helpLine missing 'full sync': %q", dh)
	}
	if contains(dh, "git+email") {
		t.Errorf("disabled helpLine should not mention 'git+email': %q", dh)
	}

	// Enabled: should advertise the wider scope.
	enabled := newTestModelWithEmail(t)
	if !enabled.emailEnabled {
		t.Fatal("precondition: enabled model should have email on")
	}
	eh := enabled.helpLine()
	if !contains(eh, "full sync (git+email)") {
		t.Errorf("enabled helpLine missing 'full sync (git+email)': %q", eh)
	}
}

// TestHelpLine_SyncCopyEnabled_TagView covers the tag-view branch of
// helpLine(), which has its own slice of help bindings. Both branches
// must reflect the email-enabled label.
func TestHelpLine_SyncCopyEnabled_TagView(t *testing.T) {
	m := newTestModelWithEmail(t)
	m.viewMode = viewTag
	// rebuild tabs for tag view via toggleViewMode would need data; we
	// just need the helpLine to render the tag branch, which it does
	// based on m.viewMode.
	h := m.helpLine()
	if !contains(h, "full sync (git+email)") {
		t.Errorf("tag-view helpLine missing 'full sync (git+email)': %q", h)
	}
}
