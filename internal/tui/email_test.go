package tui

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/email"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/store"
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
