package tui

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
)

// expectSchedule resolves a bucket name to its ISO date for current now.
func expectSchedule(t *testing.T, bucket string) string {
	t.Helper()
	got, err := schedule.Parse(bucket, time.Now())
	if err != nil {
		t.Fatalf("schedule.Parse(%q): %v", bucket, err)
	}
	return got
}

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
	if got := len(m.lists[5].Items()); got != 1 {
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
	m, _ = key(t, m, "6")
	if m.activeTab != 5 {
		t.Errorf("after '6': tab = %d, want 5", m.activeTab)
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
	items := m.lists[5].Items()
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
	if got := len(m.lists[5].Items()); got != 1 {
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
	m, _ = key(t, m, "6")
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
	m, _ = key(t, m, "6")
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

func TestTUI_CKeyOpensAddModal(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	if m.mode != modeAdd {
		t.Errorf("after pressing 'c': mode = %v, want modeAdd", m.mode)
	}
}

func TestTUI_AKeyDoesNotOpenAddModal(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "a")
	if m.mode == modeAdd {
		t.Errorf("after pressing 'a': mode = modeAdd, want modeNormal (a should no longer open add modal)")
	}
	if m.mode != modeNormal {
		t.Errorf("after pressing 'a': mode = %v, want modeNormal", m.mode)
	}
}

func TestAdd_CreatesTaskInActiveTab(t *testing.T) {
	m := newTestModel(t)
	// On the Today tab (default); press 'c', type title, enter.
	m, _ = key(t, m, "c")
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

func TestAdd_FocusesNewTaskAfterCreate(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "existing one", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "existing two", Status: "open", Schedule: "today",
			Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Cursor starts at index 0; add a task and expect cursor to move to it.
	m, _ = key(t, m, "c")
	m = typeString(t, m, "fresh task")
	m, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)

	selected := m.selectedTask()
	if selected == nil {
		t.Fatal("no task selected after add")
	}
	if selected.Title != "fresh task" {
		t.Errorf("selected task = %q, want %q", selected.Title, "fresh task")
	}
}

func TestAdd_FocusesNewTaskAcrossTab(t *testing.T) {
	// Adding from the Done tab falls back to the Today bucket. The new task
	// should be focused in whatever tab it actually lands in.
	m := newTestModel(t)
	m, _ = key(t, m, "6") // Done tab
	if m.activeTab != 5 {
		t.Fatalf("precondition: activeTab = %d, want 5", m.activeTab)
	}
	m, _ = key(t, m, "c")
	m = typeString(t, m, "stray task")
	m, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)

	if m.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 (Today) where task landed", m.activeTab)
	}
	selected := m.selectedTask()
	if selected == nil || selected.Title != "stray task" {
		t.Errorf("selectedTask = %+v, want title %q", selected, "stray task")
	}
}

func TestAdd_UsesActiveTabSchedule(t *testing.T) {
	m := newTestModel(t)
	// Switch to Week tab first.
	m, _ = key(t, m, "3")
	m, _ = key(t, m, "c")
	m = typeString(t, m, "weekly thing")
	m, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)

	if got := len(m.lists[2].Items()); got != 1 {
		t.Errorf("Week tab items = %d, want 1", got)
	}
	task := m.lists[2].Items()[0].(item).task
	if want := expectSchedule(t, "week"); task.Schedule != want {
		t.Errorf("Schedule = %q, want %q", task.Schedule, want)
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
	if want := expectSchedule(t, "tomorrow"); task.Schedule != want {
		t.Errorf("Schedule = %q, want %q", task.Schedule, want)
	}
}

func TestGrab_RightIntoDoneSetsStatusDone(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "complete via grab", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	// Today -> Tomorrow -> Week -> Month -> Someday -> Done = 5 right presses.
	for i := 0; i < 5; i++ {
		m, _ = key(t, m, "right")
	}
	if m.activeTab != 5 {
		t.Fatalf("activeTab = %d, want 5 (Done)", m.activeTab)
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
	m, _ = key(t, m, "6")
	m, _ = key(t, m, "m")
	m, _ = key(t, m, "left")
	if m.activeTab != 4 {
		t.Fatalf("activeTab = %d, want 4 (Someday)", m.activeTab)
	}
	_, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)

	task, _ := m.store.GetByPrefix("01A")
	if task.Status != "open" {
		t.Errorf("Status = %q, want open", task.Status)
	}
	if want := expectSchedule(t, "someday"); task.Schedule != want {
		t.Errorf("Schedule = %q, want %q", task.Schedule, want)
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
	m, _ = key(t, m, "6") // Done tab
	m.lists[5].Select(1)
	m, _ = key(t, m, "m")
	before := m.grabIndex
	m, _ = key(t, m, "up")
	if m.grabIndex != before {
		t.Errorf("grabIndex should not change in Done tab, before=%d after=%d",
			before, m.grabIndex)
	}
}

func TestGrab_DelegateHighlightsGrabbedItemOnly(t *testing.T) {
	// Force a color profile so the rendered output actually contains ANSI
	// escape codes; otherwise lipgloss strips them when stdout isn't a TTY
	// (as under `go test`) and styled vs. unstyled text becomes identical.
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := newTestModel(t,
		model.Task{ID: "01A", Title: "first", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "second", Status: "open", Schedule: "today",
			Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Delegate needs a non-zero list width to render.
	m.lists[0].SetSize(80, 20)

	d := newItemDelegate(m)
	items := m.lists[0].Items()

	// Index 0 is the selected/cursor row in normal mode.
	var normalSel, normalOther bytes.Buffer
	d.Render(&normalSel, m.lists[0], 0, items[0])
	d.Render(&normalOther, m.lists[0], 1, items[1])

	// Grab the first item.
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}

	var grabSel, grabOther bytes.Buffer
	d.Render(&grabSel, m.lists[0], 0, items[0])
	d.Render(&grabOther, m.lists[0], 1, items[1])

	if normalSel.String() == grabSel.String() {
		t.Errorf("grabbed row should render with different styling than normal selected row;\n normal=%q\n grab  =%q",
			normalSel.String(), grabSel.String())
	}
	if normalOther.String() != grabOther.String() {
		t.Errorf("non-grabbed row should render identically in grab mode;\n normal=%q\n grab  =%q",
			normalOther.String(), grabOther.String())
	}
}

// --- edit (YAML) tests -----------------------------------------------------

func TestMarshalTaskForEdit_RoundTrip(t *testing.T) {
	// Schedule is stored as ISO date so the round-trip leaves it untouched.
	orig := model.Task{
		ID: "01A", Title: "buy milk", Body: "2% from the corner store",
		Status: "open", Schedule: "2026-04-13", Position: 1000,
		Tags:      []string{"home", "urgent"},
		CreatedAt: "2026-04-13T00:00:00Z", UpdatedAt: "2026-04-13T00:00:00Z",
	}
	data, err := marshalTaskForEdit(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := applyEditedYAML(orig, data, time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Title != orig.Title || got.Body != orig.Body ||
		got.Schedule != orig.Schedule || !sliceEq(got.Tags, orig.Tags) {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// ID and Status are not modifiable — they must be preserved.
	if got.ID != orig.ID {
		t.Errorf("ID changed: %q -> %q", orig.ID, got.ID)
	}
	if got.Status != orig.Status {
		t.Errorf("Status changed: %q -> %q", orig.Status, got.Status)
	}
}

func TestMarshalTaskForEdit_HidesInternalFields(t *testing.T) {
	// The YAML shown to the user should not leak id/position/status.
	orig := model.Task{
		ID: "01A", Title: "t", Status: "open", Schedule: "today",
		Position: 1234.5,
	}
	data, err := marshalTaskForEdit(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	for _, forbidden := range []string{"01A", "1234", "open"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("YAML should not contain %q, got:\n%s", forbidden, s)
		}
	}
}

func TestApplyEditedYAML_RejectsEmptyTitle(t *testing.T) {
	orig := model.Task{ID: "01A", Title: "old", Schedule: "today"}
	_, err := applyEditedYAML(orig, []byte("title: \"\"\nschedule: today\n"), time.Now())
	if err == nil {
		t.Error("expected error for empty title")
	}
}

func TestApplyEditedYAML_RejectsInvalidSchedule(t *testing.T) {
	orig := model.Task{ID: "01A", Title: "old", Schedule: "today"}
	_, err := applyEditedYAML(orig, []byte("title: new\nschedule: nextmonth\n"), time.Now())
	if err == nil {
		t.Error("expected error for invalid schedule")
	}
}

func TestApplyEditedYAML_AcceptsISODate(t *testing.T) {
	orig := model.Task{ID: "01A", Title: "old", Schedule: "today"}
	got, err := applyEditedYAML(orig, []byte("title: new\nschedule: 2026-05-20\n"), time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Schedule != "2026-05-20" {
		t.Errorf("Schedule = %q, want 2026-05-20", got.Schedule)
	}
}

func TestApplyEditedYAML_UpdatesUpdatedAt(t *testing.T) {
	orig := model.Task{ID: "01A", Title: "old", Schedule: "today",
		UpdatedAt: "2026-04-10T00:00:00Z"}
	got, err := applyEditedYAML(orig, []byte("title: new\nschedule: today\n"), time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.UpdatedAt == orig.UpdatedAt {
		t.Error("UpdatedAt should be refreshed after edit")
	}
}

func TestResolveEditor(t *testing.T) {
	t.Setenv("VISUAL", "emacs")
	t.Setenv("EDITOR", "nano")
	if got := resolveEditor(); len(got) != 1 || got[0] != "emacs" {
		t.Errorf("VISUAL preferred: got %q, want [emacs]", got)
	}
	t.Setenv("VISUAL", "")
	if got := resolveEditor(); len(got) != 1 || got[0] != "nano" {
		t.Errorf("EDITOR fallback: got %q, want [nano]", got)
	}
	t.Setenv("EDITOR", "")
	if got := resolveEditor(); len(got) != 1 || got[0] != "vi" {
		t.Errorf("vi fallback: got %q, want [vi]", got)
	}
	// Editors with flags (e.g. "idea --wait", "code -w") must be split
	// so exec.Command can find the binary and pass the flags through.
	t.Setenv("EDITOR", "idea --wait")
	got := resolveEditor()
	if len(got) != 2 || got[0] != "idea" || got[1] != "--wait" {
		t.Errorf("EDITOR with flags: got %q, want [idea --wait]", got)
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

// --- item.Description compact date tests ------------------------------------

func TestItemDescription_OpenTaskShowsCreatedDate(t *testing.T) {
	fixedNow := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	it := item{
		task: model.Task{
			ID:        "01ABCDEF",
			Title:     "buy milk",
			Status:    "open",
			Schedule:  "today",
			CreatedAt: "2026-04-11T12:00:00Z", // 2 days ago
			Tags:      []string{"shopping"},
		},
		now: fixedNow,
	}
	desc := it.Description()
	// Should end with the compact date "2d"
	if !strings.Contains(desc, "2d") {
		t.Errorf("Description() = %q, want to contain %q", desc, "2d")
	}
	// Ordering: shortID, tags, then date at the end
	idx2d := strings.Index(desc, "2d")
	idxTags := strings.Index(desc, "[shopping]")
	if idxTags < 0 || idx2d < idxTags {
		t.Errorf("date should appear after tags in Description() = %q", desc)
	}
}

func TestItemDescription_DoneTaskShowsCreatedAndUpdated(t *testing.T) {
	fixedNow := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	it := item{
		task: model.Task{
			ID:        "01ABCDEF",
			Title:     "write report",
			Status:    "done",
			Schedule:  "today",
			CreatedAt: "2026-04-08T12:00:00Z", // 5 days ago
			UpdatedAt: "2026-04-13T11:00:00Z", // 1 hour ago
			Tags:      []string{"work"},
		},
		now: fixedNow,
	}
	desc := it.Description()
	// Should contain "5d→1h"
	if !strings.Contains(desc, "5d→1h") {
		t.Errorf("Description() = %q, want to contain %q", desc, "5d→1h")
	}
}

func TestItemDescription_NoTimestampsOmitsDate(t *testing.T) {
	fixedNow := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	it := item{
		task: model.Task{
			ID:       "01ABCDEF",
			Title:    "no dates",
			Status:   "open",
			Schedule: "tomorrow",
		},
		now: fixedNow,
	}
	desc := it.Description()
	want := "01ABCDEF  tomorrow"
	if desc != want {
		t.Errorf("Description() = %q, want %q", desc, want)
	}
}

// --- active toggle tests -----------------------------------------------------

func TestTUI_AKeyTogglesActive(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "toggle me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Press 'a' to activate.
	m, cmd := key(t, m, "a")
	if cmd == nil {
		t.Fatal("a should return a save cmd")
	}
	m = runCmd(t, m, cmd)

	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !task.IsActive() {
		t.Error("task should be active after first 'a' press")
	}

	// Press 'a' again to deactivate.
	m, cmd = key(t, m, "a")
	if cmd == nil {
		t.Fatal("second 'a' should return a save cmd")
	}
	m = runCmd(t, m, cmd)

	task, err = m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.IsActive() {
		t.Error("task should not be active after second 'a' press")
	}
}

func TestTUI_AKeyNoOpWhenListEmpty(t *testing.T) {
	m := newTestModel(t) // no tasks
	m, cmd := key(t, m, "a")
	if cmd != nil {
		t.Error("a on empty list should return nil cmd, got non-nil")
	}
}

func TestTUI_RetagPreservesActive(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "active tag me", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active", "old"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Open retag modal.
	m, _ = key(t, m, "t")
	if m.mode != modeRetag {
		t.Fatalf("mode = %v, want modeRetag", m.mode)
	}
	// Clear input and type new tags (without "active").
	// The input is pre-filled with "active, old"; we need to clear it.
	m.input.SetValue("work, personal")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should save")
	}
	m = runCmd(t, m, cmd)

	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !task.IsActive() {
		t.Errorf("task should still be active after retag, tags = %v", task.Tags)
	}
	// Also verify new tags are present.
	hasWork := false
	hasPersonal := false
	for _, tag := range task.Tags {
		if tag == "work" {
			hasWork = true
		}
		if tag == "personal" {
			hasPersonal = true
		}
	}
	if !hasWork || !hasPersonal {
		t.Errorf("expected work and personal tags, got %v", task.Tags)
	}
}

// --- done deactivates active tests -------------------------------------------

func TestTUI_DKeyOnActiveTaskDeactivates(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "active task", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	// Verify the task starts active.
	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !task.IsActive() {
		t.Fatal("task should be active before done")
	}

	// Verify it appears in the active panel before done.
	if len(m.activeTasks) != 1 {
		t.Fatalf("activeTasks = %d, want 1", len(m.activeTasks))
	}

	// Press 'd' to mark done.
	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("d should return a save cmd")
	}
	m = runCmd(t, m, cmd)

	// Verify on disk: task is done AND no longer active.
	task, err = m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Status != "done" {
		t.Errorf("Status: got %q, want %q", task.Status, "done")
	}
	if task.IsActive() {
		t.Error("task should not be active after done — done must auto-deactivate")
	}

	// Active panel should no longer contain the task.
	if len(m.activeTasks) != 0 {
		t.Errorf("activeTasks = %d, want 0 after done", len(m.activeTasks))
	}
}

// --- active delegate styling tests -------------------------------------------

func TestActive_DelegateRendersGreenForActiveItem(t *testing.T) {
	// Force color profile so ANSI codes survive in test output.
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := newTestModel(t,
		model.Task{ID: "01A", Title: "first", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active"}, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "second", Status: "open", Schedule: "today",
			Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.lists[0].SetSize(80, 20)
	d := newItemDelegate(m)
	items := m.lists[0].Items()

	// Render the active item (index 0 = selected row) and the normal item
	// (index 1 = unselected row).
	var activeBuf, normalBuf bytes.Buffer
	d.Render(&activeBuf, m.lists[0], 0, items[0])
	d.Render(&normalBuf, m.lists[0], 1, items[1])

	activeOut := activeBuf.String()
	normalOut := normalBuf.String()

	// The active (green) delegate uses AdaptiveColor Dark="#22C55E" = RGB(34,197,94).
	// In TrueColor mode this produces the ANSI sequence "38;2;34;197;94".
	greenSeq := "38;2;34;197;94"
	if !strings.Contains(activeOut, greenSeq) {
		t.Errorf("active item should contain green ANSI sequence %q;\n rendered=%q", greenSeq, activeOut)
	}
	if strings.Contains(normalOut, greenSeq) {
		t.Errorf("normal item should NOT contain green ANSI sequence %q;\n rendered=%q", greenSeq, normalOut)
	}
}

func TestActive_GrabStyleWinsOverActiveStyle(t *testing.T) {
	// Force color profile so ANSI codes survive in test output.
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := newTestModel(t,
		model.Task{ID: "01A", Title: "the task", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active"}, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "other", Status: "open", Schedule: "today",
			Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.lists[0].SetSize(80, 20)
	d := newItemDelegate(m)

	// Grab the active task (index 0 is selected by default).
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}

	// Render the grabbed+active task at cursor index using the same item.
	theItem := m.lists[0].Items()[0]
	var grabbedBuf bytes.Buffer
	d.Render(&grabbedBuf, m.lists[0], 0, theItem)

	grabbedOut := grabbedBuf.String()

	// Grab style uses orange/yellow. Active style uses green. When both
	// apply, grab must win — so green should NOT appear in the output, and
	// the grab color (orange) SHOULD appear.
	greenSeq := "38;2;34;197;94"
	grabSeq := "38;2;255;179;84" // AdaptiveColor Dark="#FFB454" rendered by lipgloss
	if strings.Contains(grabbedOut, greenSeq) {
		t.Errorf("grabbed+active row should NOT contain green ANSI sequence (grab wins);\n rendered=%q", grabbedOut)
	}
	if !strings.Contains(grabbedOut, grabSeq) {
		t.Errorf("grabbed+active row should contain grab (orange) ANSI sequence %q;\n rendered=%q", grabSeq, grabbedOut)
	}
}

func TestItemDescription_ZeroNowShowsFarDate(t *testing.T) {
	// When now is zero (e.g. item constructed without setting now),
	// a valid CreatedAt is far in the future relative to time.Time{},
	// so FormatRelDate returns a YY-MM-DD date string — not empty.
	it := item{
		task: model.Task{
			ID:        "01ABCDEF",
			Title:     "zero now",
			Status:    "open",
			Schedule:  "today",
			CreatedAt: "2026-04-11T12:00:00Z",
		},
		// now is zero value — dates will be far-future relative to zero time
	}
	desc := it.Description()
	if !strings.Contains(desc, "01AB") {
		t.Errorf("Description() = %q, should contain short ID", desc)
	}
	// With zero now, the date should render as YY-MM-DD (different year from year 1)
	if !strings.Contains(desc, "26-04-11") {
		t.Errorf("Description() = %q, should contain far-future date '26-04-11'", desc)
	}
}

// --- active panel tests ------------------------------------------------------

func TestActivePanel_HiddenWhenNoActiveTasks(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "normal task", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Give the model a window size so View() renders properly.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	panel := m.activePanelView()
	if panel != "" {
		t.Errorf("activePanelView() should return empty string when no active tasks, got %q", panel)
	}
	if h := m.activePanelHeight(); h != 0 {
		t.Errorf("activePanelHeight() = %d, want 0 when no active tasks", h)
	}
}

func TestActivePanel_ShownWithActiveTasks(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "my active task", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	panel := m.activePanelView()
	if panel == "" {
		t.Fatal("activePanelView() should not be empty when active tasks exist")
	}
	if !strings.Contains(panel, "my active task") {
		t.Errorf("active panel should contain the task title; got %q", panel)
	}
	if !strings.Contains(panel, "01A") {
		t.Errorf("active panel should contain short ID; got %q", panel)
	}

	view := m.View()
	if !strings.Contains(view, "my active task") {
		t.Errorf("View() should contain the active task title somewhere; got %q", view)
	}
}

func TestActivePanel_RefreshedAfterToggle(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "toggle panel task", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	// Activate.
	m, cmd := key(t, m, "a")
	if cmd == nil {
		t.Fatal("a should return a save cmd")
	}
	m = runCmd(t, m, cmd)

	panel := m.activePanelView()
	if !strings.Contains(panel, "toggle panel task") {
		t.Errorf("panel should contain task after activation; got %q", panel)
	}

	// Deactivate.
	m, cmd = key(t, m, "a")
	if cmd == nil {
		t.Fatal("second 'a' should return a save cmd")
	}
	m = runCmd(t, m, cmd)

	panel = m.activePanelView()
	if strings.Contains(panel, "toggle panel task") {
		t.Errorf("panel should NOT contain task after deactivation; got %q", panel)
	}
	if panel != "" {
		t.Errorf("panel should be empty after deactivation; got %q", panel)
	}
}

func TestActivePanel_ShrinksListHeight(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "shrink test", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	// No active tasks: record height.
	heightBefore := m.lists[0].Height()

	// Activate a task.
	m, cmd := key(t, m, "a")
	if cmd == nil {
		t.Fatal("a should return a save cmd")
	}
	m = runCmd(t, m, cmd)

	heightAfter := m.lists[0].Height()
	panelH := m.activePanelHeight()

	if panelH == 0 {
		t.Fatal("activePanelHeight() should be > 0 with an active task")
	}
	if heightAfter >= heightBefore {
		t.Errorf("list height should shrink when panel is shown: before=%d after=%d", heightBefore, heightAfter)
	}
	if heightBefore-heightAfter != panelH {
		t.Errorf("list height difference should equal panel height: before=%d after=%d panelH=%d diff=%d",
			heightBefore, heightAfter, panelH, heightBefore-heightAfter)
	}
}

func TestActivePanel_HeightMatchesRendered(t *testing.T) {
	// Verify the fast activePanelHeight() matches lipgloss.Height(activePanelView())
	// so the two never drift apart.
	lipgloss.SetColorProfile(termenv.Ascii)
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "first active", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active"}, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "second active", Status: "open", Schedule: "today",
			Position: 2000, Tags: []string{"active"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	fast := m.activePanelHeight()
	rendered := lipgloss.Height(m.activePanelView())
	if fast != rendered {
		t.Errorf("activePanelHeight() = %d, lipgloss.Height(activePanelView()) = %d; they must match", fast, rendered)
	}
}

func TestActivePanel_TruncatesLongTitles(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	longTitle := strings.Repeat("x", 200)
	m := newTestModel(t,
		model.Task{ID: "01AAAAAA", Title: longTitle, Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	width := 60
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	m = next.(*Model)

	panel := m.activePanelView()
	panelLines := strings.Split(panel, "\n")
	for _, line := range panelLines {
		lineLen := len([]rune(line))
		if lineLen > width {
			t.Errorf("panel line exceeds terminal width (%d): got %d runes: %q", width, lineLen, line)
		}
	}
}

// --- grab-to-Done auto-deactivation test ------------------------------------

func TestGrab_RightIntoDoneOnActiveTaskDeactivates(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "active grab done", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active", "work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	// Verify task starts active.
	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !task.IsActive() {
		t.Fatal("task should be active before grab")
	}

	// Grab (m), then navigate right to Done tab (5 presses: Today->Tomorrow->Week->Month->Someday->Done).
	m, _ = key(t, m, "m")
	for i := 0; i < 5; i++ {
		m, _ = key(t, m, "right")
	}
	if m.activeTab != 5 {
		t.Fatalf("activeTab = %d, want 5 (Done)", m.activeTab)
	}

	// Drop with enter.
	_, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)

	// Verify on disk: task is done AND no longer active.
	task, err = m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Status != "done" {
		t.Errorf("Status = %q, want done", task.Status)
	}
	if task.IsActive() {
		t.Error("task should not be active after grab-to-Done — commitGrab must auto-deactivate")
	}

	// Active panel should be empty.
	if len(m.activeTasks) != 0 {
		t.Errorf("activeTasks = %d, want 0 after grab-to-Done", len(m.activeTasks))
	}
}

// --- YAML edit active-tag tests ---------------------------------------------

func TestTUI_YAMLEditOmitsActiveFromYAML(t *testing.T) {
	task := model.Task{
		ID: "01A", Title: "yaml test", Status: "open", Schedule: "today",
		Tags: []string{"active", "work"}, UpdatedAt: "2026-04-13T00:00:00Z",
	}
	data, err := marshalTaskForEdit(task)
	if err != nil {
		t.Fatalf("marshalTaskForEdit: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "active") {
		t.Errorf("marshalTaskForEdit should omit 'active' tag from YAML, got:\n%s", content)
	}
	if !strings.Contains(content, "work") {
		t.Errorf("marshalTaskForEdit should include 'work' tag, got:\n%s", content)
	}
}

func TestTUI_YAMLEditPreservesActive(t *testing.T) {
	orig := model.Task{
		ID: "01A", Title: "yaml active", Status: "open", Schedule: "today",
		Tags: []string{"active", "work"}, UpdatedAt: "2026-04-13T00:00:00Z",
	}
	// Simulate editing: YAML with a new tag but no "active" (since it's hidden).
	edited := []byte("title: yaml active\nschedule: today\ntags:\n- work\n- personal\n")
	got, err := applyEditedYAML(orig, edited, time.Now())
	if err != nil {
		t.Fatalf("applyEditedYAML: %v", err)
	}
	if !got.IsActive() {
		t.Errorf("task should still be active after YAML edit, tags = %v", got.Tags)
	}
	// Verify the new tag is present.
	hasPersonal := false
	for _, tag := range got.Tags {
		if tag == "personal" {
			hasPersonal = true
		}
	}
	if !hasPersonal {
		t.Errorf("expected 'personal' tag after edit, got %v", got.Tags)
	}
}

// --- retag prefill test ------------------------------------------------------

func TestTUI_RetagPrefillOmitsActive(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "prefill test", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active", "work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Open retag modal.
	m, _ = key(t, m, "t")
	if m.mode != modeRetag {
		t.Fatalf("mode = %v, want modeRetag", m.mode)
	}
	// The input should only show "work", not "active, work".
	val := m.input.Value()
	if strings.Contains(val, "active") {
		t.Errorf("retag input should not contain 'active', got %q", val)
	}
	if !strings.Contains(val, "work") {
		t.Errorf("retag input should contain 'work', got %q", val)
	}
}

