package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/store"
)

// newTestModel returns a Model backed by a real (temp) git-initialized
// monolog repo, pre-populated with the given tasks in their declared buckets.
func newTestModel(t *testing.T, tasks ...model.Task) *Model {
	t.Helper()
	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := git.Init(repoPath, ""); err != nil {
		t.Fatalf("git.Init: %v", err)
	}
	s, err := store.New(filepath.Join(repoPath, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	for _, task := range tasks {
		if err := s.Create(task); err != nil {
			t.Fatalf("store.Create: %v", err)
		}
	}
	// Commit pre-populated tasks so the working tree is clean for later
	// AutoCommit calls (which commit only specific files).
	if len(tasks) > 0 {
		if err := git.AutoCommit(repoPath, "seed", "."); err != nil {
			t.Fatalf("seed commit: %v", err)
		}
	}
	m, err := newModel(s, repoPath)
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	return m
}

// key sends a key event to the model and returns the updated model plus any
// returned cmd.
func key(t *testing.T, m *Model, s string) (*Model, tea.Cmd) {
	t.Helper()
	msg := toKeyMsg(s)
	next, cmd := m.Update(msg)
	return next.(*Model), cmd
}

// typeString feeds each rune of s one at a time to the model (as would happen
// from keyboard input). Returns the final model.
func typeString(t *testing.T, m *Model, s string) *Model {
	t.Helper()
	for _, r := range s {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		next, _ := m.Update(msg)
		m = next.(*Model)
	}
	return m
}

// toKeyMsg converts a string spec to a tea.KeyMsg.
func toKeyMsg(s string) tea.KeyMsg {
	switch s {
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// runCmd executes cmd and feeds the resulting msg back through the model.
// Returns the model after the follow-up Update. Panics if cmd is nil.
func runCmd(t *testing.T, m *Model, cmd tea.Cmd) *Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("runCmd: nil cmd")
	}
	msg := cmd()
	next, _ := m.Update(msg)
	return next.(*Model)
}

func TestSkeleton_TabsPopulatedByBucket(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "today one", Status: "open", Schedule: "today", Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "today two", Status: "open", Schedule: "today", Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01C", Title: "tomorrow one", Status: "open", Schedule: "tomorrow", Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01D", Title: "done one", Status: "done", Schedule: "today", Position: 500, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	if got := len(m.lists[0].Items()); got != 2 {
		t.Errorf("Today tab items = %d, want 2", got)
	}
	if got := len(m.lists[1].Items()); got != 1 {
		t.Errorf("Tomorrow tab items = %d, want 1", got)
	}
	if got := len(m.lists[4].Items()); got != 1 {
		t.Errorf("Done tab items = %d, want 1", got)
	}
}

func TestSkeleton_RightArrowAdvancesTab(t *testing.T) {
	m := newTestModel(t)
	if m.activeTab != 0 {
		t.Fatalf("initial tab = %d, want 0", m.activeTab)
	}
	m, _ = key(t, m, "right")
	if m.activeTab != 1 {
		t.Errorf("after right: tab = %d, want 1", m.activeTab)
	}
}

func TestSkeleton_LeftArrowWrapsAround(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "left")
	if m.activeTab != len(defaultTabs)-1 {
		t.Errorf("after left from 0: tab = %d, want %d", m.activeTab, len(defaultTabs)-1)
	}
}

func TestSkeleton_NumberKeysJump(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "3")
	if m.activeTab != 2 {
		t.Errorf("after '3': tab = %d, want 2", m.activeTab)
	}
	m, _ = key(t, m, "5")
	if m.activeTab != 4 {
		t.Errorf("after '5': tab = %d, want 4", m.activeTab)
	}
}

func TestSkeleton_DoneSortedByUpdatedAtDesc(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "oldest done", Status: "done", Position: 1000,
			UpdatedAt: "2026-04-10T00:00:00Z"},
		model.Task{ID: "01B", Title: "newest done", Status: "done", Position: 2000,
			UpdatedAt: "2026-04-12T00:00:00Z"},
		model.Task{ID: "01C", Title: "middle done", Status: "done", Position: 3000,
			UpdatedAt: "2026-04-11T00:00:00Z"},
	)
	items := m.lists[4].Items()
	first := items[0].(item).task
	if first.Title != "newest done" {
		t.Errorf("Done[0] = %q, want %q", first.Title, "newest done")
	}
}

func TestSkeleton_QuitReturnsQuitCmd(t *testing.T) {
	m := newTestModel(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q should return tea.Quit cmd, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q cmd did not produce tea.QuitMsg")
	}
}

// --- mutation tests --------------------------------------------------------

func TestDone_MovesTaskToDoneTab(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "complete me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("d should return a save cmd")
	}
	m = runCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("save error: %v", m.err)
	}
	if got := len(m.lists[0].Items()); got != 0 {
		t.Errorf("Today tab should be empty after completion, got %d items", got)
	}
	if got := len(m.lists[4].Items()); got != 1 {
		t.Errorf("Done tab should have 1 item, got %d", got)
	}
}

func TestReschedule_PresetKeyMovesTaskToBucket(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "move me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Open reschedule modal.
	m, _ = key(t, m, "r")
	if m.mode != modeReschedule {
		t.Fatalf("mode = %v, want modeReschedule", m.mode)
	}
	// Press '2' = tomorrow.
	m, cmd := key(t, m, "2")
	if cmd == nil {
		t.Fatal("preset key should produce save cmd")
	}
	m = runCmd(t, m, cmd)
	if m.mode != modeNormal {
		t.Errorf("mode should reset to normal after save, got %v", m.mode)
	}
	if got := len(m.lists[0].Items()); got != 0 {
		t.Errorf("Today tab should be empty, got %d items", got)
	}
	if got := len(m.lists[1].Items()); got != 1 {
		t.Errorf("Tomorrow tab should have 1 item, got %d", got)
	}
}

func TestReschedule_CustomDate(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "custom date", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "r")
	m, _ = key(t, m, "5")
	if m.rescheduleSub != 1 {
		t.Fatalf("rescheduleSub = %d, want 1", m.rescheduleSub)
	}
	m = typeString(t, m, "2026-05-20")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter on valid date should save")
	}
	m = runCmd(t, m, cmd)

	// Fetch task from store to verify schedule.
	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Schedule != "2026-05-20" {
		t.Errorf("Schedule = %q, want %q", task.Schedule, "2026-05-20")
	}
}

func TestReschedule_InvalidCustomDateSurfacesError(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "bad date", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "r")
	m, _ = key(t, m, "5")
	m = typeString(t, m, "not-a-date")
	m, cmd := key(t, m, "enter")
	if cmd != nil {
		t.Error("invalid date should not produce save cmd")
	}
	if m.err == nil {
		t.Error("expected error on invalid date")
	}
}

func TestRetag_UpdatesTags(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "tag me", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"old"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "t")
	if m.mode != modeRetag {
		t.Fatalf("mode = %v, want modeRetag", m.mode)
	}
	// Clear the prefilled "old" tag via select-all-ish: just overwrite by
	// typing; existing chars remain so we select all via keys... simpler:
	// the textinput has "old" in it; we append ", new" to validate sanitize.
	m = typeString(t, m, ", new")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should save")
	}
	m = runCmd(t, m, cmd)

	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(task.Tags) != 2 || task.Tags[0] != "old" || task.Tags[1] != "new" {
		t.Errorf("Tags = %v, want [old new]", task.Tags)
	}
}

func TestAdd_CreatesTaskInActiveTab(t *testing.T) {
	m := newTestModel(t)
	// On the Today tab (default); press 'a', type title, enter.
	m, _ = key(t, m, "a")
	if m.mode != modeAdd {
		t.Fatalf("mode = %v, want modeAdd", m.mode)
	}
	m = typeString(t, m, "new task")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter with non-empty title should create")
	}
	m = runCmd(t, m, cmd)

	if got := len(m.lists[0].Items()); got != 1 {
		t.Errorf("Today tab items = %d, want 1", got)
	}
	title := m.lists[0].Items()[0].(item).task.Title
	if title != "new task" {
		t.Errorf("task title = %q, want %q", title, "new task")
	}
}

func TestAdd_UsesActiveTabSchedule(t *testing.T) {
	m := newTestModel(t)
	// Switch to Week tab first.
	m, _ = key(t, m, "3")
	m, _ = key(t, m, "a")
	m = typeString(t, m, "weekly thing")
	m, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)

	if got := len(m.lists[2].Items()); got != 1 {
		t.Errorf("Week tab items = %d, want 1", got)
	}
	task := m.lists[2].Items()[0].(item).task
	if task.Schedule != "week" {
		t.Errorf("Schedule = %q, want %q", task.Schedule, "week")
	}
}

func TestDeleteConfirm_YesRemovesTask(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "delete me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "x")
	if m.mode != modeConfirmDelete {
		t.Fatalf("mode = %v, want modeConfirmDelete", m.mode)
	}
	m, cmd := key(t, m, "y")
	if cmd == nil {
		t.Fatal("y should produce delete cmd")
	}
	m = runCmd(t, m, cmd)
	if got := len(m.lists[0].Items()); got != 0 {
		t.Errorf("Today tab should be empty after delete, got %d items", got)
	}
}

func TestDeleteConfirm_OtherKeyCancels(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "keep me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "x")
	m, cmd := key(t, m, "n")
	if cmd != nil {
		t.Error("n should not produce a cmd (cancel)")
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal after cancel", m.mode)
	}
	if got := len(m.lists[0].Items()); got != 1 {
		t.Errorf("task should remain, got %d items", got)
	}
}

func TestModal_EscCancels(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "cancel test", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "r")
	if m.mode != modeReschedule {
		t.Fatal("mode should be reschedule")
	}
	m, _ = key(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode after esc = %v, want normal", m.mode)
	}
}

// --- grab mode tests -------------------------------------------------------

func TestGrab_UpDownReordersWithinTab(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "first", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "second", Status: "open", Schedule: "today",
			Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01C", Title: "third", Status: "open", Schedule: "today",
			Position: 3000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Select the 2nd item (second), grab, move down.
	m.lists[0].Select(1)
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}
	m, _ = key(t, m, "down")
	if m.grabIndex != 2 {
		t.Errorf("grabIndex = %d, want 2", m.grabIndex)
	}

	// Drop.
	_, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should dispatch save cmd")
	}
	m = runCmd(t, m, cmd)
	if m.mode != modeNormal {
		t.Errorf("mode after drop = %v, want normal", m.mode)
	}

	// Verify "second" is now below "third" in today's bucket.
	task, err := m.store.GetByPrefix("01B")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	third, _ := m.store.GetByPrefix("01C")
	if task.Position <= third.Position {
		t.Errorf("second should have moved below third: second=%.1f third=%.1f",
			task.Position, third.Position)
	}
}

func TestGrab_RightMovesToNextTabAndSetsSchedule(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "move to tomorrow", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	m, _ = key(t, m, "right") // -> Tomorrow tab
	if m.activeTab != 1 {
		t.Errorf("activeTab = %d, want 1", m.activeTab)
	}
	_, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)

	task, _ := m.store.GetByPrefix("01A")
	if task.Schedule != "tomorrow" {
		t.Errorf("Schedule = %q, want tomorrow", task.Schedule)
	}
}

func TestGrab_RightIntoDoneSetsStatusDone(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "complete via grab", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	// Today -> Tomorrow -> Week -> Someday -> Done = 4 right presses.
	for i := 0; i < 4; i++ {
		m, _ = key(t, m, "right")
	}
	if m.activeTab != 4 {
		t.Fatalf("activeTab = %d, want 4 (Done)", m.activeTab)
	}
	_, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)

	task, _ := m.store.GetByPrefix("01A")
	if task.Status != "done" {
		t.Errorf("Status = %q, want done", task.Status)
	}
	// Schedule preserved (Done tab has no schedule filter).
	if task.Schedule != "today" {
		t.Errorf("Schedule = %q, want today (preserved)", task.Schedule)
	}
}

func TestGrab_LeftOutOfDoneSetsStatusOpen(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "uncomplete", Status: "done", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Jump to Done tab, grab, press left (-> Someday).
	m, _ = key(t, m, "5")
	m, _ = key(t, m, "m")
	m, _ = key(t, m, "left")
	if m.activeTab != 3 {
		t.Fatalf("activeTab = %d, want 3 (Someday)", m.activeTab)
	}
	_, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)

	task, _ := m.store.GetByPrefix("01A")
	if task.Status != "open" {
		t.Errorf("Status = %q, want open", task.Status)
	}
	if task.Schedule != "someday" {
		t.Errorf("Schedule = %q, want someday", task.Schedule)
	}
}

func TestGrab_EscCancels(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "stay put", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	m, _ = key(t, m, "right") // in-memory move
	m, _ = key(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode after esc = %v, want normal", m.mode)
	}
	// Task should still be on Today (esc reloads from disk).
	task, _ := m.store.GetByPrefix("01A")
	if task.Schedule != "today" {
		t.Errorf("Schedule = %q, want today (unchanged)", task.Schedule)
	}
}

func TestGrab_UpDownNoOpInDoneTab(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "done one", Status: "done", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-12T00:00:00Z"},
		model.Task{ID: "01B", Title: "done two", Status: "done", Schedule: "today",
			Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "5") // Done tab
	m.lists[4].Select(1)
	m, _ = key(t, m, "m")
	before := m.grabIndex
	m, _ = key(t, m, "up")
	if m.grabIndex != before {
		t.Errorf("grabIndex should not change in Done tab, before=%d after=%d",
			before, m.grabIndex)
	}
}

func TestSanitizeTags(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"one", []string{"one"}},
		{"one, two", []string{"one", "two"}},
		{" a , , b ", []string{"a", "b"}},
	}
	for _, tc := range tests {
		got := sanitizeTags(tc.in)
		if !sliceEq(got, tc.want) {
			t.Errorf("sanitizeTags(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestIsISODate(t *testing.T) {
	if !isISODate("2026-04-13") {
		t.Error("valid date rejected")
	}
	if isISODate("04-13-2026") {
		t.Error("wrong format accepted")
	}
	if isISODate("not-a-date") {
		t.Error("garbage accepted")
	}
	// Use strings to show the helper is used.
	_ = strings.Contains
}
