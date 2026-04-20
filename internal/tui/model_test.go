package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
)

// expectSchedule resolves a bucket name to its ISO date for current now.
func expectSchedule(t *testing.T, bucket string) string {
	t.Helper()
	got, err := schedule.Parse(bucket, time.Now(), config.DateFormat())
	if err != nil {
		t.Fatalf("schedule.Parse(%q): %v", bucket, err)
	}
	return got
}

// newTestModel returns a Model backed by a real (temp) git-initialized
// monolog repo, pre-populated with the given tasks in their declared buckets.
func newTestModel(t *testing.T, tasks ...model.Task) *Model {
	t.Helper()
	return newTestModelWithOpts(t, Options{}, tasks...)
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

// findTabByLabel returns the index of the tab with the given label,
// or calls t.Fatal if not found.
func findTabByLabel(t *testing.T, m *Model, label string) int {
	t.Helper()
	for i, tab := range m.tabs {
		if tab.label == label {
			return i
		}
	}
	t.Fatalf("tab with label %q not found", label)
	return -1
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
	// Legacy ISO input is still accepted silently so existing muscle
	// memory / scripts keep working after the format switch.
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

func TestReschedule_CustomDate_ConfiguredFormat(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "custom date cfg", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "r")
	m, _ = key(t, m, "6")
	// Enter the date in the configured DD-MM-YYYY layout. Stored value
	// must still be ISO so we can round-trip back through FormatDisplay.
	m = typeString(t, m, "20-05-2026")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter on valid DD-MM-YYYY date should save")
	}
	m = runCmd(t, m, cmd)

	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Schedule != "2026-05-20" {
		t.Errorf("Schedule = %q, want %q (DD-MM-YYYY input must store as ISO)",
			task.Schedule, "2026-05-20")
	}
}

func TestReschedule_Placeholder_UsesConfiguredLabel(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "placeholder check", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "r")
	m, _ = key(t, m, "6")
	if got, want := m.input.Placeholder, config.DateFormatLabel(); got != want {
		t.Errorf("reschedule placeholder = %q, want %q", got, want)
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
		t.Fatal("expected error on invalid date")
	}
	if got := m.err.Error(); !strings.Contains(got, config.DateFormatLabel()) {
		t.Errorf("error message should mention configured format %q, got %q",
			config.DateFormatLabel(), got)
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
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z",
			CompletedAt: "2026-04-13T00:00:00Z"},
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
	if task.CompletedAt != "" {
		t.Errorf("CompletedAt = %q, want empty after reopening", task.CompletedAt)
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

	items := m.lists[0].Items()

	// Index 0 is the selected/cursor row in normal mode.
	normalSel := m.renderListItem(0, items[0], true)
	normalOther := m.renderListItem(1, items[1], false)

	// Grab the first item.
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}

	grabSel := m.renderListItem(0, items[0], true)
	grabOther := m.renderListItem(1, items[1], false)

	if normalSel == grabSel {
		t.Errorf("grabbed row should render with different styling than normal selected row;\n normal=%q\n grab  =%q",
			normalSel, grabSel)
	}
	if normalOther != grabOther {
		t.Errorf("non-grabbed row should render identically in grab mode;\n normal=%q\n grab  =%q",
			normalOther, grabOther)
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

// TestMarshalTaskForEdit_ScheduleInConfiguredFormat ensures the YAML
// edit buffer renders the schedule in the user-facing (DD-MM-YYYY)
// layout even though the stored value is ISO. applyEditedYAML must be
// able to round-trip that back to ISO.
func TestMarshalTaskForEdit_ScheduleInConfiguredFormat(t *testing.T) {
	orig := model.Task{
		ID: "01A", Title: "pay bills", Status: "open", Schedule: "2026-04-13",
	}
	data, err := marshalTaskForEdit(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The rendered YAML must contain the user-facing date, not the raw ISO.
	if !strings.Contains(string(data), "schedule: 13-04-2026") {
		t.Errorf("YAML should contain DD-MM-YYYY schedule, got:\n%s", string(data))
	}
	if strings.Contains(string(data), "schedule: 2026-04-13") {
		t.Errorf("YAML should not contain raw ISO schedule, got:\n%s", string(data))
	}
	// Round-trip: applyEditedYAML must accept that rendering and
	// produce the original ISO value.
	got, err := applyEditedYAML(orig, data, time.Now())
	if err != nil {
		t.Fatalf("parse round-trip: %v", err)
	}
	if got.Schedule != "2026-04-13" {
		t.Errorf("round-trip Schedule = %q, want %q", got.Schedule, "2026-04-13")
	}
}

// TestMarshalTaskForEdit_IncludesRecurrence ensures the YAML edit buffer
// exposes the Recurrence field so users can set/clear it via $EDITOR.
func TestMarshalTaskForEdit_IncludesRecurrence(t *testing.T) {
	orig := model.Task{
		ID: "01A", Title: "pay bills", Status: "open", Schedule: "2026-04-13",
		Recurrence: "monthly:1",
	}
	data, err := marshalTaskForEdit(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "recurrence: monthly:1") {
		t.Errorf("YAML should contain recurrence line, got:\n%s", string(data))
	}
}

// TestMarshalTaskForEdit_OmitsEmptyRecurrence keeps the YAML clean for
// non-recurring tasks.
func TestMarshalTaskForEdit_OmitsEmptyRecurrence(t *testing.T) {
	orig := model.Task{
		ID: "01A", Title: "one-shot", Status: "open", Schedule: "2026-04-13",
	}
	data, err := marshalTaskForEdit(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "recurrence:") {
		t.Errorf("empty Recurrence should be omitted, got:\n%s", string(data))
	}
}

// TestApplyEditedYAML_SetsRecurrence verifies the YAML editor can add a
// recurrence rule, and canonicalizes aliases so weekly:Monday -> weekly:mon.
func TestApplyEditedYAML_SetsRecurrence(t *testing.T) {
	orig := model.Task{ID: "01A", Title: "old", Schedule: "today"}
	got, err := applyEditedYAML(orig, []byte("title: new\nschedule: today\nrecurrence: weekly:Monday\n"), time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Recurrence != "weekly:mon" {
		t.Errorf("Recurrence = %q, want weekly:mon (canonical)", got.Recurrence)
	}
}

// TestApplyEditedYAML_ClearsRecurrence verifies removing the recurrence
// line in the YAML clears the rule on disk — the stop semantics.
func TestApplyEditedYAML_ClearsRecurrence(t *testing.T) {
	orig := model.Task{ID: "01A", Title: "old", Schedule: "today", Recurrence: "monthly:1"}
	got, err := applyEditedYAML(orig, []byte("title: old\nschedule: today\n"), time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Recurrence != "" {
		t.Errorf("Recurrence = %q, want empty (cleared via YAML)", got.Recurrence)
	}
}

// TestApplyEditedYAML_RejectsInvalidRecurrence surfaces the parse error
// instead of silently storing garbage.
func TestApplyEditedYAML_RejectsInvalidRecurrence(t *testing.T) {
	orig := model.Task{ID: "01A", Title: "old", Schedule: "today"}
	_, err := applyEditedYAML(orig, []byte("title: new\nschedule: today\nrecurrence: bogus\n"), time.Now())
	if err == nil {
		t.Error("expected error for invalid recurrence rule")
	}
}

// TestMarshalApplyRoundTrip_RecurrencePreserved ensures a marshal → apply
// cycle with no user edits leaves Recurrence intact.
func TestMarshalApplyRoundTrip_RecurrencePreserved(t *testing.T) {
	orig := model.Task{
		ID: "01A", Title: "chore", Status: "open", Schedule: "2026-04-13",
		Recurrence: "days:3",
	}
	data, err := marshalTaskForEdit(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := applyEditedYAML(orig, data, time.Now())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.Recurrence != "days:3" {
		t.Errorf("Recurrence after round-trip: got %q, want %q", got.Recurrence, "days:3")
	}
}

// TestMarshalTaskForEdit_IncludesGrammarHeader verifies the generated YAML
// starts with a "# recurrence rules: ..." comment line so users discovering
// the recurrence field in $EDITOR see the full grammar without needing help.
func TestMarshalTaskForEdit_IncludesGrammarHeader(t *testing.T) {
	orig := model.Task{
		ID: "01A", Title: "chore", Status: "open", Schedule: "2026-04-13",
	}
	data, err := marshalTaskForEdit(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := "# recurrence rules: monthly:N | weekly:<day> | workdays | days:N"
	if !strings.Contains(string(data), want) {
		t.Errorf("YAML should contain grammar header %q, got:\n%s", want, string(data))
	}
	// The comment should be the first line so it is visible immediately.
	if !strings.HasPrefix(string(data), "# recurrence rules:") {
		t.Errorf("grammar header should be the first line, got:\n%s", string(data))
	}
}

// TestMarshalTaskForEdit_GrammarHeaderAlsoForRecurringTasks guards against
// conditional emission — the header is informational for BOTH setting and
// clearing a rule, so it must appear whether or not Recurrence is empty.
func TestMarshalTaskForEdit_GrammarHeaderAlsoForRecurringTasks(t *testing.T) {
	orig := model.Task{
		ID: "01A", Title: "pay bills", Status: "open", Schedule: "2026-04-13",
		Recurrence: "monthly:1",
	}
	data, err := marshalTaskForEdit(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "# recurrence rules:") {
		t.Errorf("grammar header should appear for recurring tasks too, got:\n%s", string(data))
	}
}

// TestApplyEditedYAML_IgnoresGrammarComment guards the parsing half of the
// round-trip: yaml.Unmarshal must treat the "#" header as a comment and not
// choke on it. This is an explicit regression test for the Task 4 change.
func TestApplyEditedYAML_IgnoresGrammarComment(t *testing.T) {
	orig := model.Task{ID: "01A", Title: "old", Schedule: "today"}
	body := "# recurrence rules: monthly:N | weekly:<day> | workdays | days:N\n" +
		"title: new\nschedule: today\nrecurrence: weekly:mon\n"
	got, err := applyEditedYAML(orig, []byte(body), time.Now())
	if err != nil {
		t.Fatalf("apply with header comment: %v", err)
	}
	if got.Title != "new" || got.Recurrence != "weekly:mon" {
		t.Errorf("comment-prefixed YAML parsed incorrectly: %+v", got)
	}
}

// TestMarshalApplyRoundTrip_WithGrammarHeader ensures the full round-trip
// — including the auto-prepended comment — leaves the task unchanged when
// the user makes no edits. This is the end-to-end guarantee.
func TestMarshalApplyRoundTrip_WithGrammarHeader(t *testing.T) {
	orig := model.Task{
		ID: "01A", Title: "chore", Status: "open", Schedule: "2026-04-13",
		Tags:       []string{"home"},
		Recurrence: "workdays",
	}
	data, err := marshalTaskForEdit(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := applyEditedYAML(orig, data, time.Now())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.Title != orig.Title || got.Schedule != orig.Schedule ||
		got.Recurrence != orig.Recurrence || !sliceEq(got.Tags, orig.Tags) {
		t.Errorf("round-trip with header mismatch: got %+v, want %+v", got, orig)
	}
}

// TestApplyEditedYAML_UserRemovesGrammarComment verifies that removing the
// header line in $EDITOR does not break parsing — the comment is purely
// informational and the buffer must remain valid YAML without it.
func TestApplyEditedYAML_UserRemovesGrammarComment(t *testing.T) {
	orig := model.Task{ID: "01A", Title: "old", Schedule: "today", Recurrence: "monthly:1"}
	// Simulate the user deleting the "# recurrence rules: ..." header line.
	body := "title: old\nschedule: today\nrecurrence: monthly:1\n"
	got, err := applyEditedYAML(orig, []byte(body), time.Now())
	if err != nil {
		t.Fatalf("apply without header: %v", err)
	}
	if got.Recurrence != "monthly:1" {
		t.Errorf("Recurrence = %q, want monthly:1", got.Recurrence)
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

func TestTruncateTitle(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"short string unchanged", "abc", 10, "abc"},
		{"exact width unchanged", "abcde", 5, "abcde"},
		{"truncated with ellipsis", "abcdefgh", 5, "abcd\u2026"},
		{"width zero returns unchanged", "abc", 0, "abc"},
		{"negative width returns unchanged", "abc", -1, "abc"},
		{"width 1 truncates to ellipsis", "abc", 1, "\u2026"},
		{"empty string unchanged", "", 5, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateTitle(tc.in, tc.width)
			if got != tc.want {
				t.Errorf("truncateTitle(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
			}
		})
	}
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
	// Clear input and type new tags.
	// The input is pre-filled with "old" (visibleTags filters out "active").
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
	items := m.lists[0].Items()

	// Render the active item (index 0 = selected row) and the normal item
	// (index 1 = unselected row).
	activeOut := m.renderListItem(0, items[0], true)
	normalOut := m.renderListItem(1, items[1], false)

	// When the active item is selected it uses the brighter AdaptiveColor Dark="#4ADE80" = RGB(73,222,128).
	// In TrueColor mode this produces the ANSI sequence "38;2;73;222;128".
	brightGreenSeq := "38;2;73;222;128"
	if !strings.Contains(activeOut, brightGreenSeq) {
		t.Errorf("selected active item should contain bright green ANSI sequence %q;\n rendered=%q", brightGreenSeq, activeOut)
	}
	// The normal (non-active, unselected) item should contain neither green shade.
	normalGreenSeq := "38;2;34;197;94"
	if strings.Contains(normalOut, brightGreenSeq) {
		t.Errorf("normal item should NOT contain bright green ANSI sequence %q;\n rendered=%q", brightGreenSeq, normalOut)
	}
	if strings.Contains(normalOut, normalGreenSeq) {
		t.Errorf("normal item should NOT contain green ANSI sequence %q;\n rendered=%q", normalGreenSeq, normalOut)
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

	// Grab the active task (index 0 is selected by default).
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}

	// Render the grabbed+active task at cursor index using the same item.
	theItem := m.lists[0].Items()[0]
	grabbedOut := m.renderListItem(0, theItem, true)

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
	// so FormatRelDate returns a DD-MM-YY date string — not empty.
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
	// With zero now, the date should render as DD-MM-YY (different year from year 1)
	if !strings.Contains(desc, "11-04-26") {
		t.Errorf("Description() = %q, should contain far-future date '11-04-26'", desc)
	}
}

// TestItemDescription_ISOScheduleRendersInConfiguredLayout verifies that a
// stored ISO schedule (e.g. "2026-05-15") is rendered in the configured
// user-facing layout (default DD-MM-YYYY) inside the list item description —
// not leaked raw as the stored ISO string.
func TestItemDescription_ISOScheduleRendersInConfiguredLayout(t *testing.T) {
	// Use a fixedNow such that 2026-05-15 is not in the "today" bucket so
	// the schedule fragment is actually emitted (the code path guards on
	// "not today" before appending the schedule).
	fixedNow := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	it := item{
		task: model.Task{
			ID:       "01ABCDEF",
			Title:    "future task",
			Status:   "open",
			Schedule: "2026-05-15",
		},
		now: fixedNow,
	}
	desc := it.Description()
	if !strings.Contains(desc, "15-05-2026") {
		t.Errorf("Description() = %q, should contain schedule in DD-MM-YYYY (15-05-2026)", desc)
	}
	if strings.Contains(desc, "2026-05-15") {
		t.Errorf("Description() = %q, should NOT contain raw stored ISO schedule", desc)
	}
}

// --- note count badge tests --------------------------------------------------

func TestItemDescription_NoteCountBadge(t *testing.T) {
	fixedNow := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	it := item{
		task: model.Task{
			ID:        "01ABCDEF",
			Title:     "task with notes",
			Status:    "open",
			Schedule:  "today",
			NoteCount: 3,
		},
		now: fixedNow,
	}
	desc := it.Description()
	if !strings.Contains(desc, "[3]") {
		t.Errorf("Description() = %q, want to contain note count badge [3]", desc)
	}
	// Badge should appear right after the short ID
	idxID := strings.Index(desc, "01AB")
	idxBadge := strings.Index(desc, "[3]")
	if idxBadge < idxID {
		t.Errorf("note count badge should appear after short ID in Description() = %q", desc)
	}
}

func TestItemDescription_NoteCountZeroNoBadge(t *testing.T) {
	fixedNow := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	it := item{
		task: model.Task{
			ID:        "01ABCDEF",
			Title:     "task without notes",
			Status:    "open",
			Schedule:  "today",
			NoteCount: 0,
		},
		now: fixedNow,
	}
	desc := it.Description()
	if strings.Contains(desc, "[0]") {
		t.Errorf("Description() = %q, should not contain [0] badge when NoteCount is 0", desc)
	}
}

func TestItemDescription_NoteCountWithOtherMetadata(t *testing.T) {
	fixedNow := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	it := item{
		task: model.Task{
			ID:        "01ABCDEF",
			Title:     "noted task",
			Status:    "open",
			Schedule:  "tomorrow",
			Tags:      []string{"work"},
			NoteCount: 5,
			CreatedAt: "2026-04-11T12:00:00Z",
		},
		now: fixedNow,
	}
	desc := it.Description()
	// Should contain all metadata pieces
	if !strings.Contains(desc, "[5]") {
		t.Errorf("Description() = %q, want note count badge [5]", desc)
	}
	if !strings.Contains(desc, "tomorrow") {
		t.Errorf("Description() = %q, want schedule 'tomorrow'", desc)
	}
	if !strings.Contains(desc, "[work]") {
		t.Errorf("Description() = %q, want tags [work]", desc)
	}
	// Verify ordering: ID, badge, schedule, tags
	idxID := strings.Index(desc, "01AB")
	idxBadge := strings.Index(desc, "[5]")
	idxSched := strings.Index(desc, "tomorrow")
	idxTags := strings.Index(desc, "[work]")
	if idxID > idxBadge || idxBadge > idxSched || idxSched > idxTags {
		t.Errorf("Description() = %q, parts should appear in order: ID, badge, schedule, tags", desc)
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
	heightBefore := m.lists[0].height

	// Activate a task.
	m, cmd := key(t, m, "a")
	if cmd == nil {
		t.Fatal("a should return a save cmd")
	}
	m = runCmd(t, m, cmd)

	heightAfter := m.lists[0].height
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
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
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

func TestActivePanel_ShowsFullLongTitle(t *testing.T) {
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
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
	// Count x's in the rendered panel — all 200 must be present (no truncation).
	xCount := strings.Count(panel, "x")
	if xCount != 200 {
		t.Errorf("panel should contain all 200 x's, got %d", xCount)
	}
	// Must NOT contain ellipsis.
	if strings.Contains(panel, "\u2026") {
		t.Error("panel should not truncate with ellipsis")
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

// --- add modal with tags tests -----------------------------------------------

func TestAdd_TabThenTypeSetsTagsOnCreatedTask(t *testing.T) {
	m := newTestModel(t)
	// Open add modal, type a title, Tab to switch to tags, type tags, Enter.
	m, _ = key(t, m, "c")
	if m.mode != modeAdd {
		t.Fatalf("mode = %v, want modeAdd", m.mode)
	}
	m = typeString(t, m, "tagged task")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "foo, bar")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)

	// The created task should have the tags.
	if got := len(m.lists[0].Items()); got != 1 {
		t.Fatalf("Today tab items = %d, want 1", got)
	}
	task := m.lists[0].Items()[0].(item).task
	if task.Title != "tagged task" {
		t.Errorf("Title = %q, want %q", task.Title, "tagged task")
	}
	if len(task.Tags) != 2 || task.Tags[0] != "foo" || task.Tags[1] != "bar" {
		t.Errorf("Tags = %v, want [foo bar]", task.Tags)
	}
}

func TestAdd_EnterWithTitleAndTagsCreatesBoth(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "buy milk")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "shopping, errands")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should save")
	}
	m = runCmd(t, m, cmd)

	// Verify in store.
	items := m.lists[0].Items()
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	task := items[0].(item).task
	stored, err := m.store.GetByPrefix(task.ID[:4])
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Title != "buy milk" {
		t.Errorf("Title = %q, want %q", stored.Title, "buy milk")
	}
	if len(stored.Tags) != 2 || stored.Tags[0] != "shopping" || stored.Tags[1] != "errands" {
		t.Errorf("Tags = %v, want [shopping errands]", stored.Tags)
	}
}

func TestAdd_EmptyTagsCreatesTaskWithNoTags(t *testing.T) {
	// Backward compat: enter with only a title and no tags should work.
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "plain task")
	// Do not press Tab or type tags — enter directly.
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)

	items := m.lists[0].Items()
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	task := items[0].(item).task
	if len(task.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", task.Tags)
	}
}

func TestAdd_TagsSanitized(t *testing.T) {
	// Extra whitespace and empty segments should be stripped.
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "sanitize test")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, " a , , b ")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)

	task := m.lists[0].Items()[0].(item).task
	if len(task.Tags) != 2 || task.Tags[0] != "a" || task.Tags[1] != "b" {
		t.Errorf("Tags = %v, want [a b]", task.Tags)
	}
}

func TestAdd_EscCancelsFromTagsField(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "will cancel")
	m, _ = key(t, m, "tab") // move to tags
	m = typeString(t, m, "some, tags")
	m, _ = key(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode after esc from tags = %v, want modeNormal", m.mode)
	}
	// No task should have been created.
	if got := len(m.lists[0].Items()); got != 0 {
		t.Errorf("Today tab should be empty after esc, got %d items", got)
	}
}

func TestAdd_FromDoneTabWithTags(t *testing.T) {
	// Adding from the Done tab falls back to Today bucket; tags should persist.
	m := newTestModel(t)
	m, _ = key(t, m, "6") // Done tab
	if m.activeTab != 5 {
		t.Fatalf("activeTab = %d, want 5", m.activeTab)
	}
	m, _ = key(t, m, "c")
	m = typeString(t, m, "done-tab task")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "urgent")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)

	// Task should land in Today tab.
	if m.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 (Today)", m.activeTab)
	}
	items := m.lists[0].Items()
	if len(items) != 1 {
		t.Fatalf("Today items = %d, want 1", len(items))
	}
	task := items[0].(item).task
	if task.Title != "done-tab task" {
		t.Errorf("Title = %q, want %q", task.Title, "done-tab task")
	}
	if len(task.Tags) != 1 || task.Tags[0] != "urgent" {
		t.Errorf("Tags = %v, want [urgent]", task.Tags)
	}
}

func TestAdd_ModalViewShowsBothLabels(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	if m.mode != modeAdd {
		t.Fatalf("mode = %v, want modeAdd", m.mode)
	}
	view := m.modalView()
	if !strings.Contains(view, "Title:") {
		t.Errorf("modalView should contain 'Title:', got %q", view)
	}
	if !strings.Contains(view, "Tags:") {
		t.Errorf("modalView should contain 'Tags:', got %q", view)
	}
	if !strings.Contains(view, "Recur:") {
		t.Errorf("modalView should contain 'Recur:', got %q", view)
	}
}

// --- recurrence add-modal tests --------------------------------------------

func TestAdd_TabTabSetsRecurrenceOnCreatedTask(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	if m.mode != modeAdd {
		t.Fatalf("mode = %v, want modeAdd", m.mode)
	}
	m = typeString(t, m, "pay bills")
	// Tab to tags, Tab to recur, type recurrence rule.
	m, _ = key(t, m, "tab")
	m, _ = key(t, m, "tab")
	if m.addFocus != addFocusRecur {
		t.Fatalf("addFocus = %v, want addFocusRecur after two Tabs", m.addFocus)
	}
	m = typeString(t, m, "monthly:1")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)

	items := m.lists[0].Items()
	if len(items) != 1 {
		t.Fatalf("Today items = %d, want 1", len(items))
	}
	task := items[0].(item).task
	if task.Title != "pay bills" {
		t.Errorf("Title = %q, want %q", task.Title, "pay bills")
	}
	if task.Recurrence != "monthly:1" {
		t.Errorf("Recurrence = %q, want %q", task.Recurrence, "monthly:1")
	}
}

func TestAdd_InvalidRecurrenceShowsErrorAndKeepsModal(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "title")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	m = typeString(t, m, "bogus")
	m, cmd := key(t, m, "enter")
	if cmd != nil {
		t.Fatal("invalid recurrence should not dispatch a create cmd")
	}
	if m.mode != modeAdd {
		t.Errorf("mode = %v, want modeAdd (modal stays open on error)", m.mode)
	}
	if m.err == nil {
		t.Fatal("expected m.err to be set on invalid recurrence")
	}
	if !strings.Contains(m.err.Error(), "recurrence") {
		t.Errorf("err = %v, want message containing 'recurrence'", m.err)
	}
	// No task was created.
	if got := len(m.lists[0].Items()); got != 0 {
		t.Errorf("Today tab should have no items after invalid recur submit, got %d", got)
	}
}

func TestAdd_FocusCyclingTitleTagsRecurTitle(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	if m.addFocus != addFocusTitle {
		t.Fatalf("initial addFocus = %v, want addFocusTitle", m.addFocus)
	}
	m, _ = key(t, m, "tab")
	if m.addFocus != addFocusTags {
		t.Errorf("after first Tab: addFocus = %v, want addFocusTags", m.addFocus)
	}
	m, _ = key(t, m, "tab")
	if m.addFocus != addFocusRecur {
		t.Errorf("after second Tab: addFocus = %v, want addFocusRecur", m.addFocus)
	}
	m, _ = key(t, m, "tab")
	if m.addFocus != addFocusTitle {
		t.Errorf("after third Tab: addFocus = %v, want addFocusTitle (wrap)", m.addFocus)
	}
}

func TestAdd_EmptyRecurCreatesTaskWithoutRecurrence(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "plain task")
	// Skip through to recur but leave it empty; then Enter.
	m, _ = key(t, m, "tab")
	m, _ = key(t, m, "tab")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)
	items := m.lists[0].Items()
	if len(items) != 1 {
		t.Fatalf("Today items = %d, want 1", len(items))
	}
	task := items[0].(item).task
	if task.Recurrence != "" {
		t.Errorf("Recurrence = %q, want empty", task.Recurrence)
	}
}

// TestAdd_RecurrenceAliasCanonicalizesOnSubmit verifies the TUI add modal
// stores the canonical form of a recurrence alias (e.g. weekly:Monday ->
// weekly:mon), matching the CLI's canonicalization so round-trips through
// show/edit see a normalized value.
func TestAdd_RecurrenceAliasCanonicalizesOnSubmit(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "weekly task")
	m, _ = key(t, m, "tab") // tags
	m, _ = key(t, m, "tab") // recur
	m = typeString(t, m, "weekly:Monday")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)
	items := m.lists[0].Items()
	if len(items) != 1 {
		t.Fatalf("Today items = %d, want 1", len(items))
	}
	task := items[0].(item).task
	if task.Recurrence != "weekly:mon" {
		t.Errorf("Recurrence: got %q, want %q (canonical form)", task.Recurrence, "weekly:mon")
	}
}

func TestAdd_RecurrenceClearedOnCloseModal(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "workdays")
	if m.recurInput.Value() != "workdays" {
		t.Fatalf("recurInput = %q, want 'workdays'", m.recurInput.Value())
	}
	// "workdays" populates recur suggestions, so the first Esc clears them
	// (mirroring the tag-field dismiss behavior) and the second Esc closes
	// the modal. Reopening must start fresh.
	if len(m.suggestions) > 0 {
		m, _ = key(t, m, "esc")
	}
	m, _ = key(t, m, "esc")
	if m.mode != modeNormal {
		t.Fatalf("mode after esc = %v, want modeNormal", m.mode)
	}
	if got := m.recurInput.Value(); got != "" {
		t.Errorf("recurInput value after close = %q, want empty", got)
	}
	m, _ = key(t, m, "c")
	if got := m.recurInput.Value(); got != "" {
		t.Errorf("recurInput value after reopen = %q, want empty", got)
	}
}

// --- wrapText tests ----------------------------------------------------------

func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{"short fits", "hello", 10, []string{"hello"}},
		{"exact width", "hello", 5, []string{"hello"}},
		{"wraps at space", "hello world", 7, []string{"hello", "world"}},
		{"wraps long sentence", "one two three four", 9, []string{"one two", "three", "four"}},
		{"hard break no spaces", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"zero width unchanged", "hello", 0, []string{"hello"}},
		{"negative width unchanged", "hello", -1, []string{"hello"}},
		{"empty string", "", 5, []string{""}},
		{"trailing space at break", "ab cd ef", 5, []string{"ab cd", "ef"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapText(tc.in, tc.width)
			if !sliceEq(got, tc.want) {
				t.Errorf("wrapText(%q, %d) = %v, want %v", tc.in, tc.width, got, tc.want)
			}
		})
	}
}

// --- vlist itemHeight tests --------------------------------------------------

func TestVlistItemHeight_ShortTitle(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "short", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)
	// Short title: 1 title line + 1 desc line + 1 blank = 3.
	if h := m.lists[0].itemHeight(0); h != 3 {
		t.Errorf("itemHeight = %d, want 3 for short title", h)
	}
}

func TestVlistItemHeight_LongTitle(t *testing.T) {
	longTitle := strings.Repeat("x", 100)
	m := newTestModel(t,
		model.Task{ID: "01A", Title: longTitle, Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = next.(*Model)
	// 60 - 2 (padding) = 58 text width; 100 chars wraps to 2 lines + 1 desc + 1 blank = 4.
	if h := m.lists[0].itemHeight(0); h != 4 {
		t.Errorf("itemHeight = %d, want 4 for long title", h)
	}
}

func TestVlistItemHeight_Separator(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = next.(*Model)
	sep := newSeparatorItem("Today")
	m.lists[0].SetItems([]list.Item{sep})
	if h := m.lists[0].itemHeight(0); h != 1 {
		t.Errorf("separator itemHeight = %d, want 1", h)
	}
}

func TestActivePanel_WrapsLongTitles(t *testing.T) {
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	longTitle := strings.Repeat("word ", 20) // 100 chars
	m := newTestModel(t,
		model.Task{ID: "01AAAAAA", Title: longTitle, Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	width := 60
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	m = next.(*Model)

	panel := m.activePanelView()

	// Panel should use at least 2 content lines for this task (1 wrapped + 1 continuation).
	fast := m.activePanelHeight()
	if fast < 4 { // 2 content lines + 2 border lines = 4
		t.Errorf("activePanelHeight() = %d, want >= 4 for wrapped title", fast)
	}

	// Rendered height must still match the fast calculation.
	rendered := lipgloss.Height(panel)
	if fast != rendered {
		t.Errorf("activePanelHeight() = %d, rendered = %d; they must match", fast, rendered)
	}

	// No line should exceed terminal width.
	for _, line := range strings.Split(panel, "\n") {
		if len([]rune(line)) > width {
			t.Errorf("panel line exceeds width %d: %d runes: %q", width, len([]rune(line)), line)
		}
	}
}

func TestAdd_AutoTagFromTitlePrefix(t *testing.T) {
	// Seed a task with tag "jean" so it's a known tag.
	m := newTestModel(t,
		model.Task{ID: "01SEED01", Title: "seed task", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"jean"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	// Open add modal, type a title with known tag prefix, then Enter.
	m, _ = key(t, m, "c")
	if m.mode != modeAdd {
		t.Fatalf("mode = %v, want modeAdd", m.mode)
	}
	m = typeString(t, m, "jean: create integration")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)

	// Find the newly created task (not the seed).
	var created *model.Task
	for _, li := range m.lists[0].Items() {
		task := li.(item).task
		if task.ID != "01SEED01" {
			created = &task
			break
		}
	}
	if created == nil {
		t.Fatal("new task not found in Today tab")
	}
	if created.Title != "jean: create integration" {
		t.Errorf("Title = %q, want %q", created.Title, "jean: create integration")
	}
	// Should have auto-tag "jean" applied.
	found := false
	for _, tag := range created.Tags {
		if tag == "jean" {
			found = true
		}
	}
	if !found {
		t.Errorf("Tags = %v, want to contain 'jean' via auto-tag", created.Tags)
	}
}

func TestAdd_NoAutoTagForUnknownPrefix(t *testing.T) {
	// No existing tasks with tag "unknown", so auto-tag should not fire.
	m := newTestModel(t)

	m, _ = key(t, m, "c")
	m = typeString(t, m, "unknown: some title")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)

	items := m.lists[0].Items()
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	task := items[0].(item).task
	if len(task.Tags) != 0 {
		t.Errorf("Tags = %v, want empty (unknown prefix should not auto-tag)", task.Tags)
	}
}

func TestAdd_AutoTagNoDuplicate(t *testing.T) {
	// Seed a task with tag "jean" so it's a known tag.
	m := newTestModel(t,
		model.Task{ID: "01SEED01", Title: "seed task", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"jean"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	// Open add modal, type title with known prefix, Tab to tags, type "jean", Enter.
	m, _ = key(t, m, "c")
	m = typeString(t, m, "jean: do thing")
	m, _ = key(t, m, "tab") // switch to tags field
	m = typeString(t, m, "jean")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should create task")
	}
	m = runCmd(t, m, cmd)

	// Find the newly created task (not the seed).
	var created *model.Task
	for _, li := range m.lists[0].Items() {
		task := li.(item).task
		if task.ID != "01SEED01" {
			created = &task
			break
		}
	}
	if created == nil {
		t.Fatal("new task not found in Today tab")
	}
	// Should have exactly one "jean" tag — no duplicate.
	count := 0
	for _, tag := range created.Tags {
		if tag == "jean" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Tags = %v, want exactly one 'jean' tag (no duplicate from auto-tag)", created.Tags)
	}
}

func TestAdd_ColonAutoPopulatesTagField(t *testing.T) {
	// Seed a task with tag "jean" so it's a known tag.
	m := newTestModel(t,
		model.Task{ID: "01SEED01", Title: "seed task", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"jean"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	// Open add modal, type "jean:" — the tags field should auto-populate.
	m, _ = key(t, m, "c")
	m = typeString(t, m, "jean: ")
	got := m.tagInput.Value()
	if got != "jean" {
		t.Errorf("tagInput = %q, want %q after typing known prefix + colon", got, "jean")
	}
}

func TestAdd_ColonNoAutoPopulateForUnknownTag(t *testing.T) {
	// No tasks with tag "unknown".
	m := newTestModel(t)

	m, _ = key(t, m, "c")
	m = typeString(t, m, "unknown: ")
	got := m.tagInput.Value()
	if got != "" {
		t.Errorf("tagInput = %q, want empty for unknown prefix", got)
	}
}

func TestAdd_ColonNoAutoPopulateDuplicate(t *testing.T) {
	// Seed a task with tag "jean" so it's a known tag.
	m := newTestModel(t,
		model.Task{ID: "01SEED01", Title: "seed task", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"jean"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	// Open add modal, manually type "jean" in tags field first, then type title.
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // focus tags
	m = typeString(t, m, "jean")
	// First Tab accepts the autocomplete suggestion ("jean" -> "jean, ").
	m, _ = key(t, m, "tab")
	// Second Tab switches focus back to the title field.
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "jean: ")
	got := m.tagInput.Value()
	// The tag field should contain "jean, " (from autocomplete acceptance) but
	// not a duplicate "jean, jean, " — the auto-populate should detect that
	// "jean" is already present.
	if got != "jean, " {
		t.Errorf("tagInput = %q, want %q (no duplicate)", got, "jean, ")
	}
}

// --- tag autocomplete tests ------------------------------------------------

func TestAdd_SuggestionsAppearWhenTypingPrefix(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work", "personal", "project"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")   // open add
	m, _ = key(t, m, "tab") // focus tags
	m = typeString(t, m, "wo")
	if len(m.suggestions) != 1 || m.suggestions[0] != "work" {
		t.Errorf("suggestions = %v, want [work]", m.suggestions)
	}
	if m.suggestionIdx != 0 {
		t.Errorf("suggestionIdx = %d, want 0", m.suggestionIdx)
	}
}

func TestAdd_SuggestionsEmptyOnNoMatch(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "xyz")
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions = %v, want empty", m.suggestions)
	}
	if m.suggestionIdx != -1 {
		t.Errorf("suggestionIdx = %d, want -1", m.suggestionIdx)
	}
}

func TestAdd_SuggestionsEmptyOnEmptyFragment(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab")
	// Empty tag field — no suggestions.
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions = %v, want empty on empty fragment", m.suggestions)
	}
}

func TestAdd_UpDownNavigatesSuggestions(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"personal", "project", "priority"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "p")
	// Should have all three p-tags.
	if len(m.suggestions) != 3 {
		t.Fatalf("suggestions = %v, want 3 items", m.suggestions)
	}
	if m.suggestionIdx != 0 {
		t.Errorf("initial suggestionIdx = %d, want 0", m.suggestionIdx)
	}
	// Down moves to 1.
	m, _ = key(t, m, "down")
	if m.suggestionIdx != 1 {
		t.Errorf("after down: suggestionIdx = %d, want 1", m.suggestionIdx)
	}
	// Down again to 2.
	m, _ = key(t, m, "down")
	if m.suggestionIdx != 2 {
		t.Errorf("after 2nd down: suggestionIdx = %d, want 2", m.suggestionIdx)
	}
	// Down at end stays at 2.
	m, _ = key(t, m, "down")
	if m.suggestionIdx != 2 {
		t.Errorf("down at end: suggestionIdx = %d, want 2", m.suggestionIdx)
	}
	// Up goes back to 1.
	m, _ = key(t, m, "up")
	if m.suggestionIdx != 1 {
		t.Errorf("after up: suggestionIdx = %d, want 1", m.suggestionIdx)
	}
	// Up again to 0.
	m, _ = key(t, m, "up")
	if m.suggestionIdx != 0 {
		t.Errorf("after 2nd up: suggestionIdx = %d, want 0", m.suggestionIdx)
	}
	// Up at start stays at 0.
	m, _ = key(t, m, "up")
	if m.suggestionIdx != 0 {
		t.Errorf("up at start: suggestionIdx = %d, want 0", m.suggestionIdx)
	}
}

func TestAdd_TabAcceptsSuggestion(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work", "personal"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // focus tags
	m = typeString(t, m, "wo")
	// suggestions = ["work"], idx = 0
	m, _ = key(t, m, "tab") // accept suggestion
	got := m.tagInput.Value()
	if got != "work, " {
		t.Errorf("tagInput = %q, want %q", got, "work, ")
	}
	// Suggestions should be cleared after acceptance (empty fragment).
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after accept = %v, want empty", m.suggestions)
	}
	// Focus should still be on tags (accept doesn't switch focus).
	if m.addFocus != addFocusTags {
		t.Errorf("addFocus = %v, want addFocusTags", m.addFocus)
	}
}

func TestAdd_EnterAcceptsSuggestionInsteadOfSubmitting(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "a title")
	m, _ = key(t, m, "tab") // focus tags
	m = typeString(t, m, "wo")
	// Enter should accept the suggestion, not submit the form.
	m, cmd := key(t, m, "enter")
	if cmd != nil {
		t.Error("enter with active suggestion should not submit (cmd should be nil)")
	}
	if m.mode != modeAdd {
		t.Errorf("mode = %v, want modeAdd (should still be in modal)", m.mode)
	}
	got := m.tagInput.Value()
	if got != "work, " {
		t.Errorf("tagInput = %q, want %q", got, "work, ")
	}
}

func TestAdd_EnterSubmitsWhenNoSuggestions(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "new task")
	// Do not type anything in tags — no suggestions visible.
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter with no suggestions should submit")
	}
	m = runCmd(t, m, cmd)
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal after submit", m.mode)
	}
}

func TestAdd_EscClearsSuggestionsInsteadOfClosing(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // focus tags
	m = typeString(t, m, "wo")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions before Esc")
	}
	// First Esc clears suggestions but stays in modal.
	m, _ = key(t, m, "esc")
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after esc = %v, want empty", m.suggestions)
	}
	if m.mode != modeAdd {
		t.Errorf("mode after first esc = %v, want modeAdd", m.mode)
	}
	// Second Esc closes the modal.
	m, _ = key(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode after second esc = %v, want modeNormal", m.mode)
	}
}

func TestAdd_SuggestionsClearedOnCloseModal(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "wo")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions before close")
	}
	// Esc once to clear suggestions, Esc again to close.
	m, _ = key(t, m, "esc")
	m, _ = key(t, m, "esc")
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after close = %v, want empty", m.suggestions)
	}
	if m.suggestionIdx != -1 {
		t.Errorf("suggestionIdx after close = %d, want -1", m.suggestionIdx)
	}
}

func TestAdd_SuggestionsExcludeAlreadyEnteredTags(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work", "writing"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab")
	// Type "work" and accept it.
	m = typeString(t, m, "wo")
	m, _ = key(t, m, "tab") // accept "work"
	// Now type "w" — "work" should be excluded, only "writing" suggested.
	m = typeString(t, m, "w")
	if len(m.suggestions) != 1 || m.suggestions[0] != "writing" {
		t.Errorf("suggestions = %v, want [writing] (work excluded)", m.suggestions)
	}
}

func TestAdd_DownNavigatesBeforeAccepting(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"personal", "project"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "p")
	// suggestions = ["personal", "project"], idx = 0
	m, _ = key(t, m, "down") // idx = 1 -> "project"
	m, _ = key(t, m, "tab")  // accept "project"
	got := m.tagInput.Value()
	if got != "project, " {
		t.Errorf("tagInput = %q, want %q", got, "project, ")
	}
}

func TestAdd_SuggestionsClearedInOpenAdd(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Set up some suggestions state, then open add modal.
	m.suggestions = []string{"stale"}
	m.suggestionIdx = 0
	m, _ = key(t, m, "c")
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after openAdd = %v, want empty", m.suggestions)
	}
	if m.suggestionIdx != -1 {
		t.Errorf("suggestionIdx after openAdd = %d, want -1", m.suggestionIdx)
	}
}

func TestAdd_UpDownIgnoredWhenTitleFocused(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	// Focus is on title (default). Even if we had stale suggestions, Up/Down
	// should not navigate them.
	m.suggestions = []string{"work"}
	m.suggestionIdx = 0
	m, _ = key(t, m, "down")
	// Should not change since title is focused — key falls through to textarea.
	if m.suggestionIdx != 0 {
		t.Errorf("suggestionIdx = %d, want 0 (should be unchanged when title focused)", m.suggestionIdx)
	}
}

func TestAdd_SuggestionsClearedOnTabAwayFromTags(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // focus tags
	// Simulate the stale-suggestions scenario: tag field has a partial value
	// and suggestions were populated, but suggestionIdx is -1 (no selection).
	// This happens after Esc clears the index but the field value remains.
	m.tagInput.SetValue("wo")
	m.suggestions = []string{"work"}
	m.suggestionIdx = -1
	// Tab should switch focus to recur (since idx < 0, handleSuggestionNav
	// does not intercept). Suggestions must be cleared so the dropdown
	// does not render while the recur field is focused.
	m, _ = key(t, m, "tab")
	if m.addFocus != addFocusRecur {
		t.Fatalf("addFocus = %v, want addFocusRecur", m.addFocus)
	}
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after Tab away from tags = %v, want empty", m.suggestions)
	}
	if m.suggestionIdx != -1 {
		t.Errorf("suggestionIdx after Tab away from tags = %d, want -1", m.suggestionIdx)
	}
}

func TestAdd_SuggestionIdxResetsOnKeystrokeAfterNavigation(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"personal", "project"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "p")
	// suggestions = ["personal", "project"], idx = 0
	if m.suggestionIdx != 0 {
		t.Fatalf("initial suggestionIdx = %d, want 0", m.suggestionIdx)
	}
	m, _ = key(t, m, "down") // idx = 1
	m, _ = key(t, m, "down") // idx stays 1 (clamped)
	if m.suggestionIdx < 1 {
		t.Fatalf("suggestionIdx after Down = %d, want >= 1", m.suggestionIdx)
	}
	// Type another character — should reset idx to 0 via refreshSuggestions.
	m = typeString(t, m, "e")
	if m.suggestionIdx != 0 {
		t.Errorf("suggestionIdx after keystroke = %d, want 0 (should reset on new input)", m.suggestionIdx)
	}
}

func TestAdd_ModalViewRendersSuggestions(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work", "writing"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	// Focus tag field and type a prefix to trigger suggestions.
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "w")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions after typing 'w'")
	}
	view := m.modalView()
	// The selected suggestion should appear with "> " prefix.
	if !strings.Contains(view, "> "+m.suggestions[0]) {
		t.Errorf("modalView should contain highlighted suggestion %q, got:\n%s", m.suggestions[0], view)
	}
	// All suggestions should be present in the output.
	for _, s := range m.suggestions {
		if !strings.Contains(view, s) {
			t.Errorf("modalView should contain suggestion %q", s)
		}
	}
}

func TestAdd_ModalViewNoSuggestionsWhenEmpty(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	// Don't type anything in the tag field.
	view := m.modalView()
	// Should not contain the indented suggestion marker format.
	if strings.Contains(view, "       > ") {
		t.Errorf("modalView should not contain suggestion markers when no suggestions, got:\n%s", view)
	}
}

func TestAdd_ModalViewSuggestionHighlightChangesOnNavigation(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"personal", "project"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab")
	m = typeString(t, m, "p")
	if len(m.suggestions) < 2 {
		t.Fatalf("expected at least 2 suggestions, got %d", len(m.suggestions))
	}
	// Initially the first suggestion is highlighted.
	view1 := m.modalView()
	if !strings.Contains(view1, "> "+m.suggestions[0]) {
		t.Errorf("first suggestion should be highlighted initially")
	}
	// Navigate down — second suggestion should now be highlighted.
	m, _ = key(t, m, "down")
	view2 := m.modalView()
	if !strings.Contains(view2, "> "+m.suggestions[1]) {
		t.Errorf("second suggestion should be highlighted after down, got:\n%s", view2)
	}
}

// --- recurrence autocomplete tests -----------------------------------------

// TestAdd_RecurSuggestionsAppearOnWeeklyPrefix verifies that typing "week"
// in the recur field populates m.suggestions with recurrence completions.
func TestAdd_RecurSuggestionsAppearOnWeeklyPrefix(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")   // open add
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	if m.addFocus != addFocusRecur {
		t.Fatalf("addFocus = %v, want addFocusRecur", m.addFocus)
	}
	m = typeString(t, m, "week")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions after typing 'week'")
	}
	// "week" should match "weekly:" from topLevelCandidates.
	found := false
	for _, s := range m.suggestions {
		if s == "weekly:" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("suggestions = %v, want to contain 'weekly:'", m.suggestions)
	}
}

// TestAdd_RecurTabAcceptsSuggestionReplacesInput verifies Tab accepts a recur
// suggestion and replaces the full input text (not appended, unlike tags).
func TestAdd_RecurTabAcceptsSuggestionReplacesInput(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	m = typeString(t, m, "week")
	// suggestions should be populated.
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions before accept")
	}
	m, _ = key(t, m, "tab") // accept highlighted suggestion
	got := m.recurInput.Value()
	if got != "weekly:" {
		t.Errorf("recurInput = %q, want %q (Tab should replace full input)", got, "weekly:")
	}
	// After accepting "weekly:", suggestions should immediately show weekdays.
	if len(m.suggestions) == 0 {
		t.Errorf("expected weekday suggestions after accepting 'weekly:', got empty")
	}
	// Focus should still be on recur (accept does not switch focus).
	if m.addFocus != addFocusRecur {
		t.Errorf("addFocus = %v, want addFocusRecur after accept", m.addFocus)
	}
}

// TestAdd_RecurWeeklyColonSuggestionsIncludeWeekdays verifies that the
// completion set after "weekly:" contains canonical weekday forms (capped
// at 5 by the pure Suggest function).
func TestAdd_RecurWeeklyColonSuggestionsIncludeWeekdays(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	m = typeString(t, m, "weekly:")
	// The suggester caps results at 5 to match the tag-autocomplete convention.
	// The exact cap is pinned in recurrence's TestSuggest_CapAndOrderingForWeekly;
	// here we only assert non-empty and within-cap to keep TUI tests decoupled.
	if n := len(m.suggestions); n == 0 || n > 5 {
		t.Fatalf("suggestions len = %d, want in [1,5], got %v", n, m.suggestions)
	}
	// First one should be weekly:mon, after accepting it input becomes "weekly:mon".
	m, _ = key(t, m, "tab") // accept first (weekly:mon)
	got := m.recurInput.Value()
	if got != "weekly:mon" {
		t.Errorf("recurInput = %q, want %q", got, "weekly:mon")
	}
}

// TestAdd_RecurEnterSubmitsEvenWithVisibleDropdown is the key divergence from
// tag autocomplete: Enter on the recur field always submits the modal, even
// when a suggestion is highlighted. The user's typed text is used as-is (after
// canonicalization), not replaced by the highlighted suggestion.
//
// This test uses "monthly:1" with the highlighted template "monthly:N" so
// that accept and submit would produce divergent outcomes — proving Enter
// chose submit (preserving the typed "monthly:1"), not accept (which would
// have replaced the input with the non-canonical template "monthly:N" and
// failed canonicalization, leaving the modal open with an error).
func TestAdd_RecurEnterSubmitsEvenWithVisibleDropdown(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "pay rent")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	// Type a concrete monthly rule; Suggest returns the template "monthly:N"
	// for any "monthly:<...>" prefix, so the highlighted suggestion diverges
	// from the typed value.
	m = typeString(t, m, "monthly:1")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions while typing 'monthly:1'")
	}
	if m.suggestions[m.suggestionIdx] != "monthly:N" {
		t.Fatalf("highlighted suggestion = %q, want %q (divergent accept/submit outcomes are the whole point of this test)",
			m.suggestions[m.suggestionIdx], "monthly:N")
	}
	// Enter must submit with the typed value "monthly:1" — NOT replace it
	// with the highlighted template "monthly:N". The real oracle for "did it
	// submit" is m.mode returning to modeNormal after the command runs;
	// runCmd itself fatals on nil cmd, so no separate nil check is needed.
	m, cmd := key(t, m, "enter")
	m = runCmd(t, m, cmd)
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal after submit (err=%v)", m.mode, m.err)
	}
	items := m.lists[0].Items()
	if len(items) != 1 {
		t.Fatalf("Today items = %d, want 1", len(items))
	}
	task := items[0].(item).task
	if task.Recurrence != "monthly:1" {
		t.Errorf("Recurrence = %q, want %q (accept would have produced %q)",
			task.Recurrence, "monthly:1", "monthly:N")
	}
}

// TestAdd_RecurTabAfterFullCanonicalFormCyclesFocus guards the self-match
// escape hatch in handleRecurSuggestionNav: when the highlighted suggestion
// equals the current input verbatim (e.g. user typed the full canonical form
// "weekly:mon" and Suggest returns ["weekly:mon"]), Tab must fall through to
// the focus-cycle handler instead of being consumed as a no-op accept.
// Without this, users cannot Tab-out without first pressing Esc.
func TestAdd_RecurTabAfterFullCanonicalFormCyclesFocus(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	if m.addFocus != addFocusRecur {
		t.Fatalf("addFocus = %v, want addFocusRecur", m.addFocus)
	}
	// Type the full canonical form; Suggest returns exactly ["weekly:mon"].
	m = typeString(t, m, "weekly:mon")
	if len(m.suggestions) != 1 || m.suggestions[0] != "weekly:mon" {
		t.Fatalf("suggestions = %v, want exactly [weekly:mon] for self-match precondition", m.suggestions)
	}
	if m.recurInput.Value() != "weekly:mon" {
		t.Fatalf("recurInput.Value() = %q, want %q", m.recurInput.Value(), "weekly:mon")
	}
	// Tab must cycle focus back to title (not be consumed as a no-op accept).
	m, _ = key(t, m, "tab")
	if m.addFocus != addFocusTitle {
		t.Errorf("addFocus = %v, want addFocusTitle (Tab must fall through on self-match)", m.addFocus)
	}
	// Suggestions should be cleared by the focus-cycle handler.
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions = %v, want empty after Tab-out", m.suggestions)
	}
	if m.suggestionIdx != -1 {
		t.Errorf("suggestionIdx = %d, want -1 after Tab-out", m.suggestionIdx)
	}
	// Recur value preserved (accept path was NOT invoked).
	if m.recurInput.Value() != "weekly:mon" {
		t.Errorf("recurInput.Value() = %q, want %q preserved after Tab-out",
			m.recurInput.Value(), "weekly:mon")
	}
}

// TestAdd_RecurTabAfterUppercaseCanonicalFormCyclesFocus pins the
// case-insensitive self-match behavior: a user typing "WEEKLY:MON" sees
// Suggest return the lowercase canonical ["weekly:mon"]. Tab must fall
// through to the focus-cycle handler (cycling to title) AND preserve the
// user's uppercase typed value rather than silently normalizing it. This
// matches user intent: they typed a valid rule, pressing Tab means "move
// on", not "rewrite my input".
func TestAdd_RecurTabAfterUppercaseCanonicalFormCyclesFocus(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	if m.addFocus != addFocusRecur {
		t.Fatalf("addFocus = %v, want addFocusRecur", m.addFocus)
	}
	// Type the full canonical form in uppercase; Suggest canonicalizes to
	// lowercase so the returned list is ["weekly:mon"].
	m = typeString(t, m, "WEEKLY:MON")
	if len(m.suggestions) != 1 || m.suggestions[0] != "weekly:mon" {
		t.Fatalf("suggestions = %v, want exactly [weekly:mon] for self-match precondition", m.suggestions)
	}
	if m.recurInput.Value() != "WEEKLY:MON" {
		t.Fatalf("recurInput.Value() = %q, want %q (uppercase preserved pre-Tab)",
			m.recurInput.Value(), "WEEKLY:MON")
	}
	// Tab must cycle focus back to title (not consume the key as an accept).
	m, _ = key(t, m, "tab")
	if m.addFocus != addFocusTitle {
		t.Errorf("addFocus = %v, want addFocusTitle (case-insensitive self-match must fall through)", m.addFocus)
	}
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions = %v, want empty after Tab-out", m.suggestions)
	}
	if m.suggestionIdx != -1 {
		t.Errorf("suggestionIdx = %d, want -1 after Tab-out", m.suggestionIdx)
	}
	// Crucially, the uppercase typed value is preserved — Tab must not
	// silently rewrite the user's input to the canonical lowercase form.
	// Canonicalization happens at submit via recurrence.Canonicalize.
	if m.recurInput.Value() != "WEEKLY:MON" {
		t.Errorf("recurInput.Value() = %q, want %q preserved (Tab must NOT rewrite case)",
			m.recurInput.Value(), "WEEKLY:MON")
	}
}

// TestAdd_RecurTabCyclesFocusWhenDropdownEmpty verifies that when the
// dropdown is empty (no input, no matches), Tab on the recur field cycles
// focus back to title rather than consuming the key for suggestion accept.
func TestAdd_RecurTabCyclesFocusWhenDropdownEmpty(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	if m.addFocus != addFocusRecur {
		t.Fatalf("addFocus = %v, want addFocusRecur", m.addFocus)
	}
	// Recur field is empty — no suggestions.
	if len(m.suggestions) != 0 {
		t.Fatalf("expected empty suggestions on empty recur field, got %v", m.suggestions)
	}
	m, _ = key(t, m, "tab") // should cycle focus back to title
	if m.addFocus != addFocusTitle {
		t.Errorf("addFocus = %v, want addFocusTitle (Tab should cycle when dropdown empty)", m.addFocus)
	}
}

// TestAdd_RecurTabCyclesFocusWithInvalidPrefix verifies that a recur-field
// input that has no matching suggestions (e.g. "xyz") still allows Tab to
// cycle focus, since no suggestion is available to accept.
func TestAdd_RecurTabCyclesFocusWithInvalidPrefix(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	m = typeString(t, m, "xyz")
	if len(m.suggestions) != 0 {
		t.Fatalf("expected no suggestions for 'xyz', got %v", m.suggestions)
	}
	m, _ = key(t, m, "tab") // should cycle focus back to title
	if m.addFocus != addFocusTitle {
		t.Errorf("addFocus = %v, want addFocusTitle when no matches", m.addFocus)
	}
}

// TestAdd_RecurUpDownNavigatesSuggestions verifies Up/Down navigation works
// on the recur field using the shared suggestionIdx plumbing.
func TestAdd_RecurUpDownNavigatesSuggestions(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	m = typeString(t, m, "weekly:")
	if len(m.suggestions) == 0 {
		t.Fatal("expected weekday suggestions")
	}
	if m.suggestionIdx != 0 {
		t.Fatalf("initial suggestionIdx = %d, want 0", m.suggestionIdx)
	}
	m, _ = key(t, m, "down")
	if m.suggestionIdx != 1 {
		t.Errorf("after down: suggestionIdx = %d, want 1", m.suggestionIdx)
	}
	m, _ = key(t, m, "up")
	if m.suggestionIdx != 0 {
		t.Errorf("after up: suggestionIdx = %d, want 0", m.suggestionIdx)
	}
	// Down past the last clamps.
	for i := 0; i < 10; i++ {
		m, _ = key(t, m, "down")
	}
	if m.suggestionIdx != len(m.suggestions)-1 {
		t.Errorf("suggestionIdx = %d, want %d (clamped at end)", m.suggestionIdx, len(m.suggestions)-1)
	}
}

// TestAdd_RecurEscClearsSuggestions verifies Esc dismisses the dropdown on
// the recur field, mirroring the existing tag-field Esc behavior.
func TestAdd_RecurEscClearsSuggestions(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	m = typeString(t, m, "week")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions before Esc")
	}
	// First Esc clears suggestions.
	m, _ = key(t, m, "esc")
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after Esc = %v, want empty", m.suggestions)
	}
	if m.mode != modeAdd {
		t.Errorf("mode after first Esc = %v, want modeAdd (should not close)", m.mode)
	}
	// Second Esc closes the modal.
	m, _ = key(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode after second Esc = %v, want modeNormal", m.mode)
	}
}

// TestAdd_RecurSuggestionsClearedOnTabAwayFromRecur verifies that after
// interacting with the recur dropdown (typing a valid prefix to populate
// suggestions, then erasing the input so the dropdown is empty), Tab-ing
// focus away from recur leaves the suggestion state fully clean
// (suggestions empty AND suggestionIdx reset). Driven through realistic
// keystrokes — no synthetic state.
func TestAdd_RecurSuggestionsClearedOnTabAwayFromRecur(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	// Populate suggestions by typing a valid prefix.
	m = typeString(t, m, "week")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions after typing 'week'")
	}
	if m.suggestionIdx != 0 {
		t.Fatalf("suggestionIdx after typing = %d, want 0", m.suggestionIdx)
	}
	// Erase input via backspace so the dropdown empties out — ensures Tab
	// below is not consumed as an accept and instead cycles focus. Loop is
	// value-driven so the test stays correct if the typed literal above
	// changes length.
	for m.recurInput.Value() != "" {
		m, _ = key(t, m, "backspace")
	}
	if m.recurInput.Value() != "" {
		t.Fatalf("recurInput after backspaces = %q, want empty", m.recurInput.Value())
	}
	if len(m.suggestions) != 0 {
		t.Fatalf("suggestions after erasing input = %v, want empty", m.suggestions)
	}
	// Tab away from recur — focus cycles to title and suggestion state stays clean.
	m, _ = key(t, m, "tab")
	if m.addFocus != addFocusTitle {
		t.Fatalf("addFocus = %v, want addFocusTitle", m.addFocus)
	}
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after Tab away from recur = %v, want empty", m.suggestions)
	}
	if m.suggestionIdx != -1 {
		t.Errorf("suggestionIdx after Tab away from recur = %d, want -1", m.suggestionIdx)
	}
}

// TestAdd_TagSuggestionsStillAcceptOnEnter is a regression guard confirming
// that the tag field retains its original behavior: both Tab and Enter accept
// a suggestion (append with ", "). Only the recur field diverges.
func TestAdd_TagSuggestionsStillAcceptOnEnter(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "a title")
	m, _ = key(t, m, "tab") // -> tags
	m = typeString(t, m, "wo")
	if len(m.suggestions) == 0 {
		t.Fatal("expected tag suggestions")
	}
	// Enter on tags accepts the suggestion (does NOT submit).
	m, cmd := key(t, m, "enter")
	if cmd != nil {
		t.Error("Enter on tag field with suggestion should accept, not submit")
	}
	if m.mode != modeAdd {
		t.Errorf("mode = %v, want modeAdd (should still be in modal)", m.mode)
	}
	got := m.tagInput.Value()
	if got != "work, " {
		t.Errorf("tagInput = %q, want %q (Enter should accept with append)", got, "work, ")
	}
}

// --- recurrence hint / view tests ------------------------------------------

// normalizeView collapses all whitespace (spaces, tabs, newlines) into single
// spaces so tests can assert on hint substrings without being affected by the
// modal's soft-wrapping at narrow terminal widths. Box-drawing characters
// that delimit the modal frame are also stripped so they don't break up the
// hint on wrapped lines.
func normalizeView(s string) string {
	// ansi.Strip removes styling so dim/bold markers don't hide substrings.
	plain := ansi.Strip(s)
	// Strip the rounded-border glyphs used by modalBox so a wrapped hint like
	// "(monthly:N | weekly:<day> |\n│ workdays | days:N)" collapses cleanly.
	replacer := strings.NewReplacer(
		"│", " ",
		"╭", " ",
		"╮", " ",
		"╰", " ",
		"╯", " ",
		"─", " ",
	)
	return strings.Join(strings.Fields(replacer.Replace(plain)), " ")
}

// TestAdd_ModalViewContainsRecurrenceHint verifies the GrammarHint string is
// always rendered in the add modal — regardless of which field is focused —
// as a discoverability affordance under the Recur input. Uses a normalized
// view to tolerate the box-wrap line break inserted by lipgloss when the
// modal is drawn at its minimum width.
func TestAdd_ModalViewContainsRecurrenceHint(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	view := normalizeView(m.modalView())
	// The literal grammar hint must appear in the rendered view (whitespace-
	// normalized to tolerate soft-wrap in narrow modal boxes).
	if !strings.Contains(view, "monthly:N | weekly:<day> | workdays | days:N") {
		t.Errorf("modalView should contain the grammar hint literal; got:\n%s", view)
	}
}

// TestAdd_ModalViewHintVisibleWithRecurFocus confirms that the hint remains
// visible even after Tab-ing into the recur field (not hidden or consumed by
// other decorations).
func TestAdd_ModalViewHintVisibleWithRecurFocus(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	if m.addFocus != addFocusRecur {
		t.Fatalf("addFocus = %v, want addFocusRecur", m.addFocus)
	}
	view := normalizeView(m.modalView())
	if !strings.Contains(view, "monthly:N | weekly:<day> | workdays | days:N") {
		t.Errorf("modalView with recur focus should still contain the grammar hint; got:\n%s", view)
	}
}

// TestAdd_ModalViewRecurSuggestionsRendered verifies that when the recur field
// is focused and a matching prefix is typed, suggestion lines appear in the
// rendered view (with the highlighted item prefixed by "> ").
func TestAdd_ModalViewRecurSuggestionsRendered(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	m = typeString(t, m, "weekly:")
	if len(m.suggestions) == 0 {
		t.Fatal("expected weekday suggestions")
	}
	view := m.modalView()
	// At least the first weekday completion (weekly:mon) should render.
	if !strings.Contains(view, "weekly:mon") {
		t.Errorf("modalView should contain 'weekly:mon' suggestion; got:\n%s", view)
	}
	// The highlighted suggestion is prefixed with "> ".
	if !strings.Contains(view, "> "+m.suggestions[0]) {
		t.Errorf("modalView should contain highlighted marker '> %s'; got:\n%s", m.suggestions[0], view)
	}
}

// TestAdd_ModalViewTagFocusRendersTagSuggestionsNotRecur verifies the
// field-owned rendering contract: real tag completions render under the Tags
// row when the tag field is focused, and no recur-style suggestion leaks
// into the view.
func TestAdd_ModalViewTagFocusRendersTagSuggestionsNotRecur(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work", "writing"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m = typeString(t, m, "w")
	if len(m.suggestions) == 0 {
		t.Fatal("expected tag suggestions after typing 'w'")
	}
	// Confirm the suggestions are tag-shaped (no colon like recur completions).
	for _, s := range m.suggestions {
		if strings.Contains(s, ":") {
			t.Fatalf("unexpected recur-looking suggestion in tag-focus: %q", s)
		}
	}
	view := m.modalView()
	// The highlighted tag should render.
	if !strings.Contains(view, "> "+m.suggestions[0]) {
		t.Errorf("expected highlighted tag suggestion in view, got:\n%s", view)
	}
	// No weekly: / workdays / days: / monthly: recur-style marker should appear.
	for _, leak := range []string{"> weekly:", "> workdays", "> days:N", "> monthly:N"} {
		if strings.Contains(view, leak) {
			t.Errorf("tag-focused view should not leak recur suggestion %q, got:\n%s", leak, view)
		}
	}
}

// TestAdd_ModalViewEmptyRecurHasNoSuggestionLines confirms an empty recur
// input with the recur field focused shows the hint but no suggestion list
// (since Suggest returns nil for empty input).
func TestAdd_ModalViewEmptyRecurHasNoSuggestionLines(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m, _ = key(t, m, "tab") // -> tags
	m, _ = key(t, m, "tab") // -> recur
	rawView := m.modalView()
	// No suggestion markers should be rendered for an empty recur field
	// (the seven-space indent followed by "> " is the suggestion marker).
	if strings.Contains(rawView, "       > ") {
		t.Errorf("modalView on empty recur should not contain suggestion markers; got:\n%s", rawView)
	}
	// But the hint MUST still be there.
	view := normalizeView(rawView)
	if !strings.Contains(view, "monthly:N | weekly:<day> | workdays | days:N") {
		t.Errorf("modalView on empty recur should contain hint; got:\n%s", view)
	}
}

// --- retag autocomplete tests -----------------------------------------------

func TestRetag_SuggestionsAppearWhenTypingPrefix(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work", "personal"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "t") // open retag
	if m.mode != modeRetag {
		t.Fatalf("mode = %v, want modeRetag", m.mode)
	}
	// knownTags should be populated.
	if len(m.knownTags) == 0 {
		t.Fatal("knownTags should be populated in openRetag")
	}
	// Clear the input and type a prefix.
	m.input.SetValue("")
	m = typeString(t, m, "wo")
	if len(m.suggestions) != 1 || m.suggestions[0] != "work" {
		t.Errorf("suggestions = %v, want [work]", m.suggestions)
	}
	if m.suggestionIdx != 0 {
		t.Errorf("suggestionIdx = %d, want 0", m.suggestionIdx)
	}
}

func TestRetag_UpDownNavigatesSuggestions(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"personal", "project", "priority"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "t")
	m.input.SetValue("")
	m = typeString(t, m, "p")
	if len(m.suggestions) != 3 {
		t.Fatalf("suggestions = %v, want 3 items", m.suggestions)
	}
	if m.suggestionIdx != 0 {
		t.Errorf("initial suggestionIdx = %d, want 0", m.suggestionIdx)
	}
	m, _ = key(t, m, "down")
	if m.suggestionIdx != 1 {
		t.Errorf("after down: suggestionIdx = %d, want 1", m.suggestionIdx)
	}
	m, _ = key(t, m, "up")
	if m.suggestionIdx != 0 {
		t.Errorf("after up: suggestionIdx = %d, want 0", m.suggestionIdx)
	}
	// Up at start stays at 0.
	m, _ = key(t, m, "up")
	if m.suggestionIdx != 0 {
		t.Errorf("up at start: suggestionIdx = %d, want 0", m.suggestionIdx)
	}
}

func TestRetag_TabAcceptsSuggestion(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work", "personal"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "t")
	m.input.SetValue("")
	m = typeString(t, m, "wo")
	// suggestions = ["work"], idx = 0
	m, _ = key(t, m, "tab")
	got := m.input.Value()
	if got != "work, " {
		t.Errorf("after tab accept: input = %q, want %q", got, "work, ")
	}
	// Suggestions should be cleared after acceptance (empty fragment).
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after accept = %v, want empty", m.suggestions)
	}
}

func TestRetag_EnterAcceptsSuggestionInsteadOfSubmitting(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "t")
	m.input.SetValue("")
	m = typeString(t, m, "wo")
	// Enter with active suggestion should accept, not submit.
	m, cmd := key(t, m, "enter")
	if cmd != nil {
		t.Error("enter with active suggestion should not submit (cmd should be nil)")
	}
	if m.mode != modeRetag {
		t.Errorf("mode = %v, want modeRetag (should stay in retag modal)", m.mode)
	}
	if !strings.Contains(m.input.Value(), "work, ") {
		t.Errorf("input value = %q, want it to contain accepted suggestion 'work, '", m.input.Value())
	}
}

func TestRetag_EnterSubmitsWhenNoSuggestions(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "t")
	// Don't type anything new — no suggestions visible.
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter with no suggestions should submit")
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal (should close modal after submit)", m.mode)
	}
}

func TestRetag_EscClearsSuggestionsInsteadOfClosing(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "t")
	m.input.SetValue("")
	m = typeString(t, m, "wo")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions before Esc")
	}
	// First Esc clears suggestions but stays in modal.
	m, _ = key(t, m, "esc")
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after esc = %v, want empty", m.suggestions)
	}
	if m.mode != modeRetag {
		t.Errorf("mode after esc = %v, want modeRetag (should stay in modal)", m.mode)
	}
	// Second Esc closes the modal.
	m, _ = key(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode after 2nd esc = %v, want modeNormal", m.mode)
	}
}

func TestRetag_SuggestionsClearedInOpenRetag(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Set up stale suggestions, then open retag modal.
	m.suggestions = []string{"stale"}
	m.suggestionIdx = 0
	m, _ = key(t, m, "t")
	if len(m.suggestions) != 0 {
		t.Errorf("suggestions after openRetag = %v, want empty", m.suggestions)
	}
	if m.suggestionIdx != -1 {
		t.Errorf("suggestionIdx after openRetag = %d, want -1", m.suggestionIdx)
	}
}

func TestRetag_ModalViewRendersSuggestions(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work", "writing"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "t")
	m.input.SetValue("")
	m = typeString(t, m, "w")
	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions after typing 'w'")
	}
	view := m.modalView()
	// The selected suggestion should appear with "> " prefix.
	if !strings.Contains(view, "> "+m.suggestions[0]) {
		t.Errorf("retag modalView should contain highlighted suggestion %q, got:\n%s", m.suggestions[0], view)
	}
	// All suggestions should be present in the output.
	for _, s := range m.suggestions {
		if !strings.Contains(view, s) {
			t.Errorf("retag modalView should contain suggestion %q", s)
		}
	}
}

func TestRetag_ModalViewNoSuggestionsWhenEmpty(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01S1", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"work"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "t")
	// Don't type anything new in the tag field.
	view := m.modalView()
	// Should not contain the indented suggestion marker format.
	if strings.Contains(view, "       > ") {
		t.Errorf("retag modalView should not contain suggestion markers when no suggestions, got:\n%s", view)
	}
}

// --- pendingAction tests ----------------------------------------------------

func TestPendingAction_NilByDefault(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "grab me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Enter grab mode.
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}
	if m.pendingAction != nil {
		t.Errorf("pendingAction should be nil by default in grab mode")
	}
}

func TestPendingAction_DispatchedOnSuccessfulSave(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "grab me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	// Set a pendingAction that records it was called and returns a sentinel cmd.
	type sentinelMsg struct{}
	dispatched := false
	m.pendingAction = func() tea.Cmd {
		dispatched = true
		return func() tea.Msg { return sentinelMsg{} }
	}

	// Simulate a successful taskSavedMsg.
	next, actionCmd := m.Update(taskSavedMsg{status: "Moved: grab me", focusID: "01A"})
	m = next.(*Model)

	if !dispatched {
		t.Error("pendingAction should have been dispatched on successful taskSavedMsg")
	}
	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
	// Verify the cmd returned by pendingAction is propagated through Update.
	if actionCmd == nil {
		t.Fatal("Update should return the cmd from pendingAction")
	}
	msg := actionCmd()
	if _, ok := msg.(sentinelMsg); !ok {
		t.Errorf("returned cmd should produce sentinelMsg, got %T", msg)
	}
}

func TestPendingAction_ClearedOnErrorNotDispatched(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "grab me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	// Set a pendingAction that records it was called.
	dispatched := false
	m.pendingAction = func() tea.Cmd {
		dispatched = true
		return nil
	}

	// Simulate an error taskSavedMsg.
	next, _ := m.Update(taskSavedMsg{err: fmt.Errorf("boom")})
	m = next.(*Model)

	if dispatched {
		t.Error("pendingAction should NOT be dispatched on error taskSavedMsg")
	}
	if m.pendingAction != nil {
		t.Error("pendingAction should be cleared even on error")
	}
}

// --- grab mode action key tests (Task 2) ---

func TestGrab_EditKey_SetsPendingAndCommits(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "grab me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Enter grab mode.
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}

	// Press 'e' — should set pendingAction and return commitGrab cmd.
	m, cmd := key(t, m, "e")
	if m.mode != modeNormal {
		t.Errorf("mode after 'e' = %v, want modeNormal (commitGrab resets mode)", m.mode)
	}
	if m.pendingAction == nil {
		t.Error("pendingAction should be set after pressing 'e' in grab mode")
	}
	if cmd == nil {
		t.Fatal("cmd should be non-nil (commitGrab)")
	}

	// Execute commitGrab — pendingAction should dispatch and return openEdit cmd.
	// openEdit uses tea.ExecProcess so we can't fully execute it, but we can
	// verify the dispatch chain works: pendingAction fires and returns non-nil.
	msg := cmd()
	next, actionCmd := m.Update(msg)
	m = next.(*Model)

	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
	// openEdit returns a tea.ExecProcess cmd — verify it's non-nil.
	if actionCmd == nil {
		t.Error("expected openEdit cmd to be returned from pendingAction dispatch")
	}
}

func TestGrab_RescheduleKey_OpensModalAfterCommit(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "grab me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Enter grab mode.
	m, _ = key(t, m, "m")

	// Press 'r' — should set pendingAction and return commitGrab cmd.
	m, cmd := key(t, m, "r")
	if m.mode != modeNormal {
		t.Errorf("mode after 'r' = %v, want modeNormal", m.mode)
	}
	if cmd == nil {
		t.Fatal("cmd should be non-nil (commitGrab)")
	}

	// Run the commitGrab cmd — simulates async save completing.
	m = runCmd(t, m, cmd)

	// After taskSavedMsg, pendingAction should have dispatched openReschedule.
	if m.mode != modeReschedule {
		t.Errorf("mode after commit+dispatch = %v, want modeReschedule", m.mode)
	}
	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
}

func TestGrab_RetagKey_OpensModalAfterCommit(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "grab me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	m, cmd := key(t, m, "t")
	if cmd == nil {
		t.Fatal("cmd should be non-nil (commitGrab)")
	}
	m = runCmd(t, m, cmd)

	if m.mode != modeRetag {
		t.Errorf("mode after commit+dispatch = %v, want modeRetag", m.mode)
	}
	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
}

func TestGrab_DoneKey_MarksTaskDoneAfterCommit(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "done me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("cmd should be non-nil (commitGrab)")
	}

	// Execute commitGrab cmd and capture the msg.
	msg := cmd()
	// Feed msg through Update — this triggers pendingAction dispatch.
	next, actionCmd := m.Update(msg)
	m = next.(*Model)

	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
	// actionCmd is the return from doneSelected(). Execute it.
	if actionCmd == nil {
		t.Fatal("expected doneSelected cmd to be returned from pendingAction dispatch")
	}
	m = runCmd(t, m, actionCmd)

	// The task should now be done.
	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Status != "done" {
		t.Errorf("task status = %q, want %q", task.Status, "done")
	}
}

func TestGrab_ActiveKey_TogglesActiveAfterCommit(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "activate me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	m, cmd := key(t, m, "a")
	if cmd == nil {
		t.Fatal("cmd should be non-nil (commitGrab)")
	}

	// Execute commitGrab and capture pending action cmd.
	msg := cmd()
	next, actionCmd := m.Update(msg)
	m = next.(*Model)

	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
	if actionCmd == nil {
		t.Fatal("expected toggleActive cmd to be returned from pendingAction dispatch")
	}
	m = runCmd(t, m, actionCmd)

	// The task should now be active.
	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !task.IsActive() {
		t.Error("task should be active after toggle")
	}
}

func TestHelpLine_GrabMode_ContainsActionKeys(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Enter grab mode.
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}

	help := m.helpLine()

	// Verify core grab controls are present.
	for _, want := range []string{"GRAB", "reorder", "bucket", "drop", "cancel"} {
		if !strings.Contains(help, want) {
			t.Errorf("helpLine() missing %q in grab mode: %s", want, help)
		}
	}

	// Verify action key hints are present.
	if !strings.Contains(help, "+d/e/r/t/a/c/x/s") {
		t.Errorf("helpLine() missing action key hints in grab mode: %s", help)
	}
}

func TestGrab_AddKey_OpensAddAfterCommit(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "grab me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	m, cmd := key(t, m, "c")
	if cmd == nil {
		t.Fatal("cmd should be non-nil (commitGrab)")
	}

	// Execute commitGrab and capture pending action cmd.
	msg := cmd()
	next, _ := m.Update(msg)
	m = next.(*Model)

	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
	if m.mode != modeAdd {
		t.Errorf("mode after commit+dispatch = %v, want modeAdd", m.mode)
	}
}

func TestGrab_DeleteKey_OpensConfirmDeleteAfterCommit(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "grab me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	m, cmd := key(t, m, "x")
	if cmd == nil {
		t.Fatal("cmd should be non-nil (commitGrab)")
	}

	// Execute commitGrab and capture pending action cmd.
	msg := cmd()
	next, actionCmd := m.Update(msg)
	m = next.(*Model)

	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
	// openConfirmDelete sets mode and returns nil.
	if actionCmd != nil {
		t.Error("openConfirmDelete returns nil cmd, expected nil from dispatch")
	}
	if m.mode != modeConfirmDelete {
		t.Errorf("mode after commit+dispatch = %v, want modeConfirmDelete", m.mode)
	}
}

func TestGrab_SyncKey_ReturnsSyncCmdAfterCommit(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "grab me", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "m")
	m, cmd := key(t, m, "s")
	if cmd == nil {
		t.Fatal("cmd should be non-nil (commitGrab)")
	}

	// Execute commitGrab and capture pending action cmd.
	msg := cmd()
	next, actionCmd := m.Update(msg)
	m = next.(*Model)

	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
	if m.statusMsg != "Syncing..." {
		t.Errorf("statusMsg = %q, want %q", m.statusMsg, "Syncing...")
	}
	// syncCmd returns a non-nil cmd (the async sync operation).
	if actionCmd == nil {
		t.Fatal("expected syncCmd to be returned from pendingAction dispatch")
	}
}

func TestGrab_CrossTabMoveAndDone(t *testing.T) {
	// Grab a task in Today, move it to Tomorrow with right-arrow, then press 'd'.
	// Verify the task ends up done in the Tomorrow schedule.
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "cross tab done", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Enter grab mode.
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}

	// Move to next tab (Tomorrow).
	m, _ = key(t, m, "right")

	// Press 'd' to mark done — should commit grab (saving to Tomorrow) then done.
	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("cmd should be non-nil (commitGrab)")
	}

	// Execute commitGrab — the task should be saved in the Tomorrow bucket.
	msg := cmd()
	next, actionCmd := m.Update(msg)
	m = next.(*Model)

	if m.pendingAction != nil {
		t.Error("pendingAction should be nil after dispatch")
	}
	if actionCmd == nil {
		t.Fatal("expected doneSelected cmd from pendingAction dispatch")
	}

	// Execute the doneSelected cmd.
	m = runCmd(t, m, actionCmd)

	// Verify the task is done and was scheduled for tomorrow.
	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Status != "done" {
		t.Errorf("task status = %q, want %q", task.Status, "done")
	}
	wantSched := expectSchedule(t, "tomorrow")
	if task.Schedule != wantSched {
		t.Errorf("task schedule = %q, want %q (tomorrow)", task.Schedule, wantSched)
	}
}

// --- viewMode and tagTab tests ----------------------------------------------

func TestViewMode_DefaultIsSchedule(t *testing.T) {
	m := newTestModel(t)
	if m.viewMode != viewSchedule {
		t.Errorf("default viewMode = %d, want viewSchedule (%d)", m.viewMode, viewSchedule)
	}
}

func TestViewMode_TagTabsNilByDefault(t *testing.T) {
	m := newTestModel(t)
	if m.tagTabs != nil {
		t.Errorf("tagTabs should be nil by default, got %v", m.tagTabs)
	}
}

// --- buildTagTabs tests ------------------------------------------------------

func TestBuildTagTabs_Ordering(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "task a", Tags: []string{"beta", "active"}},
		{ID: "01B", Title: "task b", Tags: []string{"alpha"}},
		{ID: "01C", Title: "task c", Tags: []string{"gamma"}},
	}
	tabs := buildTagTabs(tasks)

	// Expect: Active, alpha, beta, gamma, Untagged (5 total).
	if len(tabs) != 5 {
		t.Fatalf("len(tabs) = %d, want 5", len(tabs))
	}

	// First tab must be Active.
	if tabs[0].label != "Active" || !tabs[0].isActive {
		t.Errorf("tabs[0] = %+v, want Active tab", tabs[0])
	}
	if tabs[0].tag != model.ActiveTag {
		t.Errorf("Active tab tag = %q, want %q", tabs[0].tag, model.ActiveTag)
	}

	// Middle tabs in alphabetical order.
	if tabs[1].label != "alpha" || tabs[1].tag != "alpha" {
		t.Errorf("tabs[1] = %+v, want alpha tab", tabs[1])
	}
	if tabs[2].label != "beta" || tabs[2].tag != "beta" {
		t.Errorf("tabs[2] = %+v, want beta tab", tabs[2])
	}
	if tabs[3].label != "gamma" || tabs[3].tag != "gamma" {
		t.Errorf("tabs[3] = %+v, want gamma tab", tabs[3])
	}

	// Last tab must be Untagged.
	if tabs[len(tabs)-1].label != "Untagged" || !tabs[len(tabs)-1].isUntagged {
		t.Errorf("last tab = %+v, want Untagged tab", tabs[len(tabs)-1])
	}
	if tabs[len(tabs)-1].tag != "" {
		t.Errorf("Untagged tab tag = %q, want empty string", tabs[len(tabs)-1].tag)
	}
}

func TestBuildTagTabs_CaseInsensitiveSort(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "task a", Tags: []string{"mlog"}},
		{ID: "01B", Title: "task b", Tags: []string{"Warsaw"}},
		{ID: "01C", Title: "task c", Tags: []string{"jean"}},
	}
	tabs := buildTagTabs(tasks)

	// Expect: Active, jean, mlog, Warsaw, Untagged (case-insensitive alpha).
	if len(tabs) != 5 {
		t.Fatalf("len(tabs) = %d, want 5", len(tabs))
	}
	if tabs[1].label != "jean" {
		t.Errorf("tabs[1].label = %q, want %q", tabs[1].label, "jean")
	}
	if tabs[2].label != "mlog" {
		t.Errorf("tabs[2].label = %q, want %q", tabs[2].label, "mlog")
	}
	if tabs[3].label != "Warsaw" {
		t.Errorf("tabs[3].label = %q, want %q", tabs[3].label, "Warsaw")
	}
}

func TestBuildTagTabs_NoTags(t *testing.T) {
	// Tasks with no tags at all (or only active tag which is excluded by CollectTags).
	tasks := []model.Task{
		{ID: "01A", Title: "task a", Tags: nil},
		{ID: "01B", Title: "task b", Tags: []string{"active"}},
	}
	tabs := buildTagTabs(tasks)

	// Should still have Active + Untagged = 2 tabs.
	if len(tabs) != 2 {
		t.Fatalf("len(tabs) = %d, want 2", len(tabs))
	}
	if !tabs[0].isActive {
		t.Error("tabs[0] should be Active")
	}
	if !tabs[1].isUntagged {
		t.Error("tabs[1] should be Untagged")
	}
}

func TestBuildTagTabs_AllTasksUntagged(t *testing.T) {
	tasks := []model.Task{
		{ID: "01A", Title: "task a"},
		{ID: "01B", Title: "task b"},
	}
	tabs := buildTagTabs(tasks)

	// Active + Untagged only.
	if len(tabs) != 2 {
		t.Fatalf("len(tabs) = %d, want 2", len(tabs))
	}
	if tabs[0].label != "Active" {
		t.Errorf("tabs[0].label = %q, want Active", tabs[0].label)
	}
	if tabs[1].label != "Untagged" {
		t.Errorf("tabs[1].label = %q, want Untagged", tabs[1].label)
	}
}

func TestBuildTagTabs_EmptyTaskList(t *testing.T) {
	tabs := buildTagTabs(nil)

	// Even with zero tasks, Active + Untagged are present.
	if len(tabs) != 2 {
		t.Fatalf("len(tabs) = %d, want 2", len(tabs))
	}
	if !tabs[0].isActive {
		t.Error("tabs[0] should be Active")
	}
	if !tabs[1].isUntagged {
		t.Error("tabs[1] should be Untagged")
	}
}

func TestBuildTagTabs_TasksWithMultipleTags(t *testing.T) {
	// A task with multiple tags should cause both tags to appear as tabs.
	tasks := []model.Task{
		{ID: "01A", Title: "multi-tag task", Tags: []string{"work", "urgent", "active"}},
		{ID: "01B", Title: "single-tag task", Tags: []string{"work"}},
	}
	tabs := buildTagTabs(tasks)

	// Active + urgent + work + Untagged = 4
	if len(tabs) != 4 {
		t.Fatalf("len(tabs) = %d, want 4", len(tabs))
	}
	// Verify alpha order: urgent before work.
	if tabs[1].label != "urgent" {
		t.Errorf("tabs[1].label = %q, want urgent", tabs[1].label)
	}
	if tabs[2].label != "work" {
		t.Errorf("tabs[2].label = %q, want work", tabs[2].label)
	}
}

func TestBuildTagTabs_NoActiveTasksStillHasActiveTab(t *testing.T) {
	// No task has the active tag, but the Active tab should still be present.
	tasks := []model.Task{
		{ID: "01A", Title: "task a", Tags: []string{"docs"}},
	}
	tabs := buildTagTabs(tasks)

	// Active + docs + Untagged = 3
	if len(tabs) != 3 {
		t.Fatalf("len(tabs) = %d, want 3", len(tabs))
	}
	if !tabs[0].isActive {
		t.Error("tabs[0] should be Active even when no tasks have the active tag")
	}
}

func TestBuildTagTabs_MiddleTabsNotSpecial(t *testing.T) {
	// Verify middle tabs have isActive=false and isUntagged=false.
	tasks := []model.Task{
		{ID: "01A", Title: "task a", Tags: []string{"foo"}},
	}
	tabs := buildTagTabs(tasks)

	if len(tabs) < 3 {
		t.Fatalf("expected at least 3 tabs, got %d", len(tabs))
	}
	mid := tabs[1]
	if mid.isActive {
		t.Error("middle tab should not be isActive")
	}
	if mid.isUntagged {
		t.Error("middle tab should not be isUntagged")
	}
	if mid.tag != "foo" {
		t.Errorf("middle tab tag = %q, want foo", mid.tag)
	}
}

// --- separator item tests ----------------------------------------------------

func TestNewSeparatorItem_IsSeparator(t *testing.T) {
	sep := newSeparatorItem("Today")
	if !sep.isSeparator {
		t.Error("newSeparatorItem should set isSeparator = true")
	}
	if sep.task.Title != "Today" {
		t.Errorf("separator title = %q, want %q", sep.task.Title, "Today")
	}
	if sep.task.ID != "" {
		t.Errorf("separator task ID should be empty, got %q", sep.task.ID)
	}
}

func TestSeparatorItem_TitleAndFilterValue(t *testing.T) {
	sep := newSeparatorItem("Week")
	if sep.Title() != "Week" {
		t.Errorf("Title() = %q, want %q", sep.Title(), "Week")
	}
	if sep.FilterValue() != "Week" {
		t.Errorf("FilterValue() = %q, want %q", sep.FilterValue(), "Week")
	}
}

func TestSeparatorItem_DescriptionIsMinimal(t *testing.T) {
	sep := newSeparatorItem("Month")
	// Separator tasks have zero-value fields; Description returns an empty
	// string since there is no ID, schedule, tags, or dates to display.
	desc := sep.Description()
	if desc != "" {
		t.Errorf("separator Description() = %q, want empty string (no task data)", desc)
	}
}

func TestRegularItem_IsNotSeparator(t *testing.T) {
	regular := item{task: model.Task{ID: "01A", Title: "test"}}
	if regular.isSeparator {
		t.Error("regular item should not be a separator")
	}
}

func TestSelectedTask_ReturnsNilOnSeparator(t *testing.T) {
	m := newTestModel(t)
	// Inject a separator as the only item in the Today tab.
	sep := newSeparatorItem("Today")
	m.lists[0].SetItems([]list.Item{sep})
	m.lists[0].Select(0)
	m.activeTab = 0

	got := m.selectedTask()
	if got != nil {
		t.Errorf("selectedTask() should return nil for separator, got task %q", got.Title)
	}
}

func TestSelectedTask_ReturnsTaskForRegularItem(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "real task", Status: "open", Schedule: "today", Position: 1000},
	)
	m.activeTab = 0
	m.lists[0].Select(0)

	got := m.selectedTask()
	if got == nil {
		t.Fatal("selectedTask() should not be nil for a regular item")
	}
	if got.ID != "01A" {
		t.Errorf("selectedTask().ID = %q, want %q", got.ID, "01A")
	}
}

func TestSelectedTask_ReturnsNilOnEmptyList(t *testing.T) {
	m := newTestModel(t) // no tasks
	m.activeTab = 0

	got := m.selectedTask()
	if got != nil {
		t.Errorf("selectedTask() should return nil on empty list, got %+v", got)
	}
}

func TestSeparatorItem_MixedList_SkipsSeparators(t *testing.T) {
	// Verify selectedTask returns nil when cursor is on separator in a mixed list.
	m := newTestModel(t)
	sep := newSeparatorItem("Today")
	regular := item{
		task: model.Task{ID: "01X", Title: "real one"},
		now:  time.Now(),
	}
	m.lists[0].SetItems([]list.Item{sep, regular})

	// Select the separator (index 0).
	m.lists[0].Select(0)
	m.activeTab = 0
	if got := m.selectedTask(); got != nil {
		t.Errorf("selectedTask on separator should be nil, got %q", got.Title)
	}

	// Select the regular item (index 1).
	m.lists[0].Select(1)
	if got := m.selectedTask(); got == nil {
		t.Fatal("selectedTask on regular item should not be nil")
	} else if got.ID != "01X" {
		t.Errorf("selectedTask().ID = %q, want %q", got.ID, "01X")
	}
}

func TestSeparatorRender_ContainsLabel(t *testing.T) {
	// Verify that the separator renderer produces output containing the label.
	lipgloss.SetColorProfile(termenv.Ascii)
	m := newTestModel(t)

	sep := newSeparatorItem("Week")
	m.lists[0].SetItems([]list.Item{sep})
	m.lists[0].SetSize(60, 10)

	out := m.renderListItem(0, sep, false)
	if !strings.Contains(out, "Week") {
		t.Errorf("separator render should contain label %q, got %q", "Week", out)
	}
	if !strings.Contains(out, "──") {
		t.Errorf("separator render should contain dash decoration, got %q", out)
	}
}

// --- Task 4: tag view reload tests ------------------------------------------

// helper: create a task with specific schedule bucket and tags.
func makeTask(t *testing.T, id, title, bucket string, tags []string) model.Task {
	t.Helper()
	sched, err := schedule.Parse(bucket, time.Now(), config.DateFormat())
	if err != nil {
		t.Fatalf("schedule.Parse(%q): %v", bucket, err)
	}
	return model.Task{
		ID:       id,
		Title:    title,
		Status:   "open",
		Schedule: sched,
		Position: 1000,
		Tags:     tags,
	}
}

func TestRebuildForTagView_SwitchesViewMode(t *testing.T) {
	task1 := makeTask(t, "01TAG1", "task with work tag", schedule.Today, []string{"work"})
	task2 := makeTask(t, "01TAG2", "task with home tag", schedule.Today, []string{"home"})
	m := newTestModel(t, task1, task2)

	if m.viewMode != viewSchedule {
		t.Fatalf("expected viewSchedule, got %d", m.viewMode)
	}

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	if m.viewMode != viewTag {
		t.Errorf("expected viewTag after rebuild, got %d", m.viewMode)
	}
	if m.tagTabs == nil {
		t.Fatal("tagTabs should be populated after rebuild")
	}
}

func TestRebuildForTagView_CreatesCorrectTabs(t *testing.T) {
	task1 := makeTask(t, "01TAG1", "work task", schedule.Today, []string{"work"})
	task2 := makeTask(t, "01TAG2", "home task", schedule.Week, []string{"home"})
	task3 := makeTask(t, "01TAG3", "untagged task", schedule.Tomorrow, nil)
	m := newTestModel(t, task1, task2, task3)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	// Expect: Active, home, work, Untagged (alphabetical for middle tags)
	if len(m.tabs) != 4 {
		t.Fatalf("expected 4 tabs, got %d", len(m.tabs))
	}
	wantLabels := []string{"Active", "home", "work", "Untagged"}
	for i, want := range wantLabels {
		if m.tabs[i].label != want {
			t.Errorf("tab %d: want label %q, got %q", i, want, m.tabs[i].label)
		}
	}
	// Lists should match tab count.
	if len(m.lists) != len(m.tabs) {
		t.Errorf("lists count %d != tabs count %d", len(m.lists), len(m.tabs))
	}
}

func TestRebuildForScheduleView_RestoresDefaults(t *testing.T) {
	task1 := makeTask(t, "01TAG1", "work task", schedule.Today, []string{"work"})
	m := newTestModel(t, task1)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}
	if m.viewMode != viewTag {
		t.Fatalf("expected viewTag")
	}

	if err := m.rebuildForScheduleView(); err != nil {
		t.Fatalf("rebuildForScheduleView: %v", err)
	}

	if m.viewMode != viewSchedule {
		t.Errorf("expected viewSchedule after restore, got %d", m.viewMode)
	}
	if m.tagTabs != nil {
		t.Errorf("tagTabs should be nil after restore")
	}
	if len(m.tabs) != len(defaultTabs) {
		t.Errorf("tabs count %d != defaultTabs count %d", len(m.tabs), len(defaultTabs))
	}
}

func TestTagViewReload_TasksAppearUnderCorrectTag(t *testing.T) {
	task1 := makeTask(t, "01TAG1", "work task", schedule.Today, []string{"work"})
	task2 := makeTask(t, "01TAG2", "home task", schedule.Today, []string{"home"})
	m := newTestModel(t, task1, task2)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	// Find home tab (should be index 1: Active, home, work, Untagged).
	homeIdx := -1
	workIdx := -1
	for i, tt := range m.tagTabs {
		if tt.tag == "home" {
			homeIdx = i
		}
		if tt.tag == "work" {
			workIdx = i
		}
	}
	if homeIdx == -1 || workIdx == -1 {
		t.Fatalf("could not find home/work tabs; tagTabs: %v", m.tagTabs)
	}

	// Home tab should have home task (plus separator).
	homeItems := m.lists[homeIdx].Items()
	foundHome := false
	for _, it := range homeItems {
		if i, ok := it.(item); ok && !i.isSeparator && i.task.ID == "01TAG2" {
			foundHome = true
		}
	}
	if !foundHome {
		t.Errorf("home tab should contain task 01TAG2")
	}

	// Work tab should have work task.
	workItems := m.lists[workIdx].Items()
	foundWork := false
	for _, it := range workItems {
		if i, ok := it.(item); ok && !i.isSeparator && i.task.ID == "01TAG1" {
			foundWork = true
		}
	}
	if !foundWork {
		t.Errorf("work tab should contain task 01TAG1")
	}
}

func TestTagViewReload_BucketGroupingAndSeparators(t *testing.T) {
	// Two tasks in same tag, different buckets.
	task1 := makeTask(t, "01BKT1", "today task", schedule.Today, []string{"proj"})
	task1.Position = 1000
	task2 := makeTask(t, "01BKT2", "week task", schedule.Week, []string{"proj"})
	task2.Position = 2000
	m := newTestModel(t, task1, task2)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	// Find proj tab.
	projIdx := -1
	for i, tt := range m.tagTabs {
		if tt.tag == "proj" {
			projIdx = i
		}
	}
	if projIdx == -1 {
		t.Fatal("could not find proj tab")
	}

	items := m.lists[projIdx].Items()
	// Expect: separator("Today"), task1, separator("Week"), task2
	if len(items) != 4 {
		t.Fatalf("expected 4 items (2 separators + 2 tasks), got %d", len(items))
	}

	// First item: Today separator.
	sep0 := items[0].(item)
	if !sep0.isSeparator || sep0.task.Title != "Today" {
		t.Errorf("item 0: expected Today separator, got separator=%v title=%q", sep0.isSeparator, sep0.task.Title)
	}

	// Second item: today task.
	it1 := items[1].(item)
	if it1.isSeparator || it1.task.ID != "01BKT1" {
		t.Errorf("item 1: expected task 01BKT1, got separator=%v id=%q", it1.isSeparator, it1.task.ID)
	}

	// Third item: Week separator.
	sep2 := items[2].(item)
	if !sep2.isSeparator || sep2.task.Title != "Week" {
		t.Errorf("item 2: expected Week separator, got separator=%v title=%q", sep2.isSeparator, sep2.task.Title)
	}

	// Fourth item: week task.
	it3 := items[3].(item)
	if it3.isSeparator || it3.task.ID != "01BKT2" {
		t.Errorf("item 3: expected task 01BKT2, got separator=%v id=%q", it3.isSeparator, it3.task.ID)
	}
}

func TestTagViewReload_DoneTasksAppearUnderDoneSeparator(t *testing.T) {
	openTask := makeTask(t, "01OPEN", "open task", schedule.Today, []string{"proj"})
	openTask.Position = 1000

	doneTask := makeTask(t, "01DONE", "done task", schedule.Today, []string{"proj"})
	doneTask.Status = "done"
	doneTask.UpdatedAt = "2026-04-14T10:00:00Z"

	m := newTestModel(t, openTask, doneTask)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	projIdx := findTabByLabel(t, m, "proj")
	items := m.lists[projIdx].Items()

	// Expect: separator("Today"), openTask, separator("Done"), doneTask
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}

	sep0 := items[0].(item)
	if !sep0.isSeparator || sep0.task.Title != "Today" {
		t.Errorf("item 0: expected Today separator, got separator=%v title=%q", sep0.isSeparator, sep0.task.Title)
	}

	it1 := items[1].(item)
	if it1.task.ID != "01OPEN" {
		t.Errorf("item 1: expected 01OPEN, got %q", it1.task.ID)
	}

	sep2 := items[2].(item)
	if !sep2.isSeparator || sep2.task.Title != "Done" {
		t.Errorf("item 2: expected Done separator, got separator=%v title=%q", sep2.isSeparator, sep2.task.Title)
	}

	it3 := items[3].(item)
	if it3.task.ID != "01DONE" {
		t.Errorf("item 3: expected 01DONE, got %q", it3.task.ID)
	}
}

func TestTagViewReload_DoneOnlyTagHidden(t *testing.T) {
	// A tag with only done tasks should NOT create a tab.
	doneTask := makeTask(t, "01DTAG", "done tagged", schedule.Today, []string{"archived"})
	doneTask.Status = "done"

	m := newTestModel(t, doneTask)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	// The "archived" tab should NOT exist — only Active and Untagged.
	for _, tt := range m.tagTabs {
		if tt.label == "archived" {
			t.Error("done-only tag 'archived' should not create a tab")
		}
	}
}

func TestTagViewReload_DoneTasksVisibleWhenOpenTaskExists(t *testing.T) {
	// A tag with open + done tasks: tab exists and shows both.
	openTask := makeTask(t, "01OPEN", "open task", schedule.Today, []string{"proj"})
	openTask.Position = 1000
	doneTask := makeTask(t, "01DONE", "done task", schedule.Today, []string{"proj"})
	doneTask.Status = "done"

	m := newTestModel(t, openTask, doneTask)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	idx := findTabByLabel(t, m, "proj")
	items := m.lists[idx].Items()

	// Should have: separator("Today"), openTask, separator("Done"), doneTask
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
	if items[2].(item).task.Title != "Done" {
		t.Errorf("item 2: expected Done separator, got %q", items[2].(item).task.Title)
	}
	if items[3].(item).task.ID != "01DONE" {
		t.Errorf("item 3: expected 01DONE, got %q", items[3].(item).task.ID)
	}
}

func TestTagViewReload_SortsByPositionWithinBucket(t *testing.T) {
	task1 := makeTask(t, "01POS1", "second", schedule.Today, []string{"proj"})
	task1.Position = 2000
	task2 := makeTask(t, "01POS2", "first", schedule.Today, []string{"proj"})
	task2.Position = 1000
	m := newTestModel(t, task1, task2)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	projIdx := -1
	for i, tt := range m.tagTabs {
		if tt.tag == "proj" {
			projIdx = i
		}
	}
	if projIdx == -1 {
		t.Fatal("could not find proj tab")
	}

	items := m.lists[projIdx].Items()
	// Expect: separator, first (pos 1000), second (pos 2000)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	it1 := items[1].(item)
	it2 := items[2].(item)
	if it1.task.ID != "01POS2" || it2.task.ID != "01POS1" {
		t.Errorf("expected tasks sorted by position: got %q then %q", it1.task.ID, it2.task.ID)
	}
}

func TestTagViewReload_UntaggedTabFiltering(t *testing.T) {
	task1 := makeTask(t, "01UNT1", "untagged task", schedule.Today, nil)
	task2 := makeTask(t, "01UNT2", "tagged task", schedule.Today, []string{"work"})
	// A task with only the active tag should appear as "untagged" in terms of
	// VisibleTags, but it is active and appears in the Active tab.
	task3 := makeTask(t, "01UNT3", "active only", schedule.Today, []string{model.ActiveTag})
	m := newTestModel(t, task1, task2, task3)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	// Find untagged tab (last one).
	untaggedIdx := -1
	for i, tt := range m.tagTabs {
		if tt.isUntagged {
			untaggedIdx = i
		}
	}
	if untaggedIdx == -1 {
		t.Fatal("could not find untagged tab")
	}

	// Collect task IDs from the untagged tab.
	var taskIDs []string
	for _, it := range m.lists[untaggedIdx].Items() {
		i := it.(item)
		if !i.isSeparator {
			taskIDs = append(taskIDs, i.task.ID)
		}
	}

	// task1 (nil tags) should be there, task3 (only active tag) should also
	// be there since VisibleTags filters out ActiveTag.
	found1, found3 := false, false
	for _, id := range taskIDs {
		if id == "01UNT1" {
			found1 = true
		}
		if id == "01UNT3" {
			found3 = true
		}
	}
	if !found1 {
		t.Errorf("untagged tab should contain task 01UNT1 (nil tags)")
	}
	if !found3 {
		t.Errorf("untagged tab should contain task 01UNT3 (only active tag)")
	}

	// task2 (tagged with "work") should NOT be in untagged.
	for _, id := range taskIDs {
		if id == "01UNT2" {
			t.Errorf("untagged tab should NOT contain task 01UNT2 (has work tag)")
		}
	}
}

func TestTagViewReload_ActiveTabFiltering(t *testing.T) {
	task1 := makeTask(t, "01ACT1", "active work", schedule.Today, []string{"work", model.ActiveTag})
	task2 := makeTask(t, "01ACT2", "not active", schedule.Today, []string{"work"})
	m := newTestModel(t, task1, task2)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	// Active tab is first (index 0).
	activeIdx := 0
	if !m.tagTabs[activeIdx].isActive {
		t.Fatalf("expected first tab to be Active")
	}

	var taskIDs []string
	for _, it := range m.lists[activeIdx].Items() {
		i := it.(item)
		if !i.isSeparator {
			taskIDs = append(taskIDs, i.task.ID)
		}
	}

	if len(taskIDs) != 1 || taskIDs[0] != "01ACT1" {
		t.Errorf("active tab should only contain 01ACT1; got %v", taskIDs)
	}
}

func TestTagViewReload_MultipleTagsTaskAppearsInEachTab(t *testing.T) {
	task := makeTask(t, "01MTG1", "multi-tag task", schedule.Today, []string{"home", "work"})
	m := newTestModel(t, task)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	homeIdx, workIdx := -1, -1
	for i, tt := range m.tagTabs {
		if tt.tag == "home" {
			homeIdx = i
		}
		if tt.tag == "work" {
			workIdx = i
		}
	}
	if homeIdx == -1 || workIdx == -1 {
		t.Fatalf("could not find home/work tabs")
	}

	// Task should appear in both tabs.
	for _, idx := range []int{homeIdx, workIdx} {
		found := false
		for _, it := range m.lists[idx].Items() {
			if i, ok := it.(item); ok && !i.isSeparator && i.task.ID == "01MTG1" {
				found = true
			}
		}
		if !found {
			t.Errorf("task 01MTG1 should appear in tab %d (%s)", idx, m.tabs[idx].label)
		}
	}
}

func TestTagViewReload_EmptyBucketsOmitted(t *testing.T) {
	// A single task in today bucket should only produce one separator.
	task := makeTask(t, "01EMP1", "today only", schedule.Today, []string{"work"})
	m := newTestModel(t, task)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	workIdx := -1
	for i, tt := range m.tagTabs {
		if tt.tag == "work" {
			workIdx = i
		}
	}
	if workIdx == -1 {
		t.Fatal("could not find work tab")
	}

	items := m.lists[workIdx].Items()
	// Should have exactly: Today separator + 1 task = 2 items.
	if len(items) != 2 {
		t.Errorf("expected 2 items (1 separator + 1 task), got %d", len(items))
	}
	if sep := items[0].(item); !sep.isSeparator || sep.task.Title != "Today" {
		t.Errorf("first item should be Today separator, got separator=%v title=%q", sep.isSeparator, sep.task.Title)
	}
}

func TestActivePanel_HiddenInTagView(t *testing.T) {
	task := makeTask(t, "01APH1", "active task", schedule.Today, []string{model.ActiveTag})
	m := newTestModel(t, task)
	m.width = 80
	m.height = 40

	// In schedule view, active panel should be visible.
	if m.activePanelView() == "" {
		t.Error("active panel should be visible in schedule view with active tasks")
	}
	if m.activePanelHeight() == 0 {
		t.Error("activePanelHeight should be > 0 in schedule view with active tasks")
	}

	// Switch to tag view.
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	if m.activePanelView() != "" {
		t.Error("active panel should be hidden in tag view")
	}
	if m.activePanelHeight() != 0 {
		t.Errorf("activePanelHeight should be 0 in tag view, got %d", m.activePanelHeight())
	}
}

func TestActivePanel_VisibleAgainAfterScheduleView(t *testing.T) {
	task := makeTask(t, "01APR1", "active task", schedule.Today, []string{model.ActiveTag})
	m := newTestModel(t, task)
	m.width = 80
	m.height = 40

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}
	if m.activePanelView() != "" {
		t.Error("should be hidden in tag view")
	}

	if err := m.rebuildForScheduleView(); err != nil {
		t.Fatalf("rebuildForScheduleView: %v", err)
	}
	if m.activePanelView() == "" {
		t.Error("active panel should be visible again in schedule view")
	}
}

func TestBucketDisplayName(t *testing.T) {
	cases := []struct {
		bucket, want string
	}{
		{schedule.Today, "Today"},
		{schedule.Tomorrow, "Tomorrow"},
		{schedule.Week, "Week"},
		{schedule.Month, "Month"},
		{schedule.Someday, "Someday"},
		{"unknown", "unknown"},
	}
	for _, tc := range cases {
		got := bucketDisplayName(tc.bucket)
		if got != tc.want {
			t.Errorf("bucketDisplayName(%q) = %q, want %q", tc.bucket, got, tc.want)
		}
	}
}

func TestTaskHasTag(t *testing.T) {
	task := model.Task{Tags: []string{"work", "home"}}
	if !taskHasTag(task, "work") {
		t.Error("should have work tag")
	}
	if !taskHasTag(task, "home") {
		t.Error("should have home tag")
	}
	if taskHasTag(task, "other") {
		t.Error("should not have other tag")
	}
	if taskHasTag(model.Task{}, "work") {
		t.Error("empty task should not have work tag")
	}
}

func TestRebuildForTagView_ClampsActiveTab(t *testing.T) {
	// Start in schedule view with 6 tabs, set activeTab to 5 (Done tab).
	m := newTestModel(t)
	m.activeTab = 5

	// Tag view may have fewer tabs (Active + Untagged = 2 if no tags).
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}
	if m.activeTab >= len(m.tabs) {
		t.Errorf("activeTab %d out of range for %d tabs", m.activeTab, len(m.tabs))
	}
}

func TestTagViewReload_BucketOrderCorrect(t *testing.T) {
	// Create tasks in reverse bucket order to verify ordering is correct.
	task1 := makeTask(t, "01ORD1", "someday task", schedule.Someday, []string{"proj"})
	task1.Position = 1000
	task2 := makeTask(t, "01ORD2", "today task", schedule.Today, []string{"proj"})
	task2.Position = 1000
	task3 := makeTask(t, "01ORD3", "month task", schedule.Month, []string{"proj"})
	task3.Position = 1000
	task4 := makeTask(t, "01ORD4", "tomorrow task", schedule.Tomorrow, []string{"proj"})
	task4.Position = 1000
	task5 := makeTask(t, "01ORD5", "week task", schedule.Week, []string{"proj"})
	task5.Position = 1000
	m := newTestModel(t, task1, task2, task3, task4, task5)

	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	projIdx := -1
	for i, tt := range m.tagTabs {
		if tt.tag == "proj" {
			projIdx = i
		}
	}
	if projIdx == -1 {
		t.Fatal("could not find proj tab")
	}

	// Extract separator labels in order.
	var sepLabels []string
	for _, it := range m.lists[projIdx].Items() {
		i := it.(item)
		if i.isSeparator {
			sepLabels = append(sepLabels, i.task.Title)
		}
	}
	wantOrder := []string{"Today", "Tomorrow", "Week", "Month", "Someday"}
	if len(sepLabels) != len(wantOrder) {
		t.Fatalf("expected %d separators, got %d: %v", len(wantOrder), len(sepLabels), sepLabels)
	}
	for i, want := range wantOrder {
		if sepLabels[i] != want {
			t.Errorf("separator %d: want %q, got %q", i, want, sepLabels[i])
		}
	}
}

// --- Task 5: view mode toggle (v key) tests ---------------------------------

func TestToggleViewMode_ScheduleToTag(t *testing.T) {
	task1 := makeTask(t, "01TV01", "work task", schedule.Today, []string{"work"})
	task2 := makeTask(t, "01TV02", "home task", schedule.Tomorrow, []string{"home"})
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Start in schedule view.
	if m.viewMode != viewSchedule {
		t.Fatalf("initial viewMode = %d, want viewSchedule", m.viewMode)
	}

	// Press v to toggle to tag view.
	m, _ = key(t, m, "v")

	if m.viewMode != viewTag {
		t.Errorf("viewMode after v = %d, want viewTag", m.viewMode)
	}
	// Should have tag tabs now: Active, home, work, Untagged.
	if len(m.tabs) < 3 {
		t.Errorf("expected at least 3 tabs in tag view, got %d", len(m.tabs))
	}
	if m.tagTabs[0].label != "Active" {
		t.Errorf("first tab label = %q, want Active", m.tagTabs[0].label)
	}
}

func TestToggleViewMode_TagToSchedule(t *testing.T) {
	task1 := makeTask(t, "01TV03", "work task", schedule.Today, []string{"work"})
	m := newTestModel(t, task1)
	m.width = 80
	m.height = 40

	// Switch to tag view first.
	m, _ = key(t, m, "v")
	if m.viewMode != viewTag {
		t.Fatalf("viewMode after first v = %d, want viewTag", m.viewMode)
	}

	// Switch back to schedule view.
	m, _ = key(t, m, "v")
	if m.viewMode != viewSchedule {
		t.Errorf("viewMode after second v = %d, want viewSchedule", m.viewMode)
	}
	// Default tabs should be restored.
	if len(m.tabs) != len(defaultTabs) {
		t.Errorf("tab count = %d, want %d", len(m.tabs), len(defaultTabs))
	}
	if m.tagTabs != nil {
		t.Errorf("tagTabs should be nil in schedule view, got %v", m.tagTabs)
	}
}

func TestToggleViewMode_RoundTrip(t *testing.T) {
	task1 := makeTask(t, "01TV04", "alpha task", schedule.Today, []string{"proj"})
	task2 := makeTask(t, "01TV05", "beta task", schedule.Week, []string{"proj"})
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Schedule -> Tag -> Schedule.
	m, _ = key(t, m, "v")
	if m.viewMode != viewTag {
		t.Fatalf("after first toggle: viewMode = %d, want viewTag", m.viewMode)
	}
	m, _ = key(t, m, "v")
	if m.viewMode != viewSchedule {
		t.Fatalf("after second toggle: viewMode = %d, want viewSchedule", m.viewMode)
	}

	// Verify schedule view is functional: Today tab should have task1.
	todayItems := m.lists[0].Items()
	found := false
	for _, it := range todayItems {
		if it.(item).task.ID == "01TV04" {
			found = true
			break
		}
	}
	if !found {
		t.Error("task 01TV04 not found in Today tab after round-trip toggle")
	}
}

func TestToggleViewMode_CursorPreservation(t *testing.T) {
	// Create two tasks in Today. Select the second one, toggle, and verify
	// the cursor lands on the same task in the new view.
	task1 := makeTask(t, "01TV06", "first task", schedule.Today, []string{"work"})
	task1.Position = 1000
	task2 := makeTask(t, "01TV07", "second task", schedule.Today, []string{"work"})
	task2.Position = 2000
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Select the second task in the Today tab.
	m, _ = key(t, m, "down")
	sel := m.selectedTask()
	if sel == nil || sel.ID != "01TV07" {
		t.Fatalf("expected selected task 01TV07, got %v", sel)
	}

	// Toggle to tag view.
	m, _ = key(t, m, "v")

	// Find which tab we're on and verify the selected task ID.
	sel = m.selectedTask()
	if sel == nil {
		t.Fatal("no task selected after toggle to tag view")
	}
	if sel.ID != "01TV07" {
		t.Errorf("cursor after toggle: selected task = %q, want 01TV07", sel.ID)
	}
}

func TestToggleViewMode_CursorPreservation_TagToSchedule(t *testing.T) {
	task1 := makeTask(t, "01TV08", "first task", schedule.Today, []string{"proj"})
	task1.Position = 1000
	task2 := makeTask(t, "01TV09", "second task", schedule.Today, []string{"proj"})
	task2.Position = 2000
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Toggle to tag view.
	m, _ = key(t, m, "v")

	// Find the proj tab and navigate to second task.
	projIdx := -1
	for i, tt := range m.tagTabs {
		if tt.tag == "proj" {
			projIdx = i
			break
		}
	}
	if projIdx == -1 {
		t.Fatal("could not find proj tab")
	}
	m.activeTab = projIdx
	m.recomputeLayout()

	// The proj tab has: separator("Today"), task1, task2. Navigate down.
	// Items: [sep, task1, task2] — cursor starts at 0 (sep).
	// Move down to task1, then down to task2.
	m, _ = key(t, m, "down") // task1
	m, _ = key(t, m, "down") // task2
	sel := m.selectedTask()
	if sel == nil || sel.ID != "01TV09" {
		selID := ""
		if sel != nil {
			selID = sel.ID
		}
		t.Fatalf("expected 01TV09 selected, got %q", selID)
	}

	// Toggle back to schedule view.
	m, _ = key(t, m, "v")

	sel = m.selectedTask()
	if sel == nil {
		t.Fatal("no task selected after toggle back to schedule view")
	}
	if sel.ID != "01TV09" {
		t.Errorf("cursor after toggle back: selected task = %q, want 01TV09", sel.ID)
	}
}

func TestToggleViewMode_RecomputeLayout(t *testing.T) {
	// Create active tasks so the panel would be visible in schedule view.
	task := makeTask(t, "01TV10", "active task", schedule.Today, []string{model.ActiveTag})
	m := newTestModel(t, task)
	m.width = 80
	m.height = 40
	m.recomputeLayout()

	// In schedule view, active panel height is > 0.
	schedHeight := m.activePanelHeight()
	if schedHeight == 0 {
		t.Fatal("activePanelHeight should be > 0 in schedule view with active tasks")
	}

	// Get list height in schedule view.
	schedListH := m.lists[0].height

	// Toggle to tag view.
	m, _ = key(t, m, "v")

	// Active panel hidden in tag view.
	if m.activePanelHeight() != 0 {
		t.Error("activePanelHeight should be 0 in tag view")
	}

	// List should be taller in tag view (recovered the panel space).
	tagListH := m.lists[0].height
	if tagListH <= schedListH {
		t.Errorf("tag view list height (%d) should be > schedule view list height (%d)", tagListH, schedListH)
	}
}

func TestToggleViewMode_HelpLine_ScheduleView(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeNormal
	m.viewMode = viewSchedule

	help := m.helpLine()
	if !strings.Contains(help, "v tags") {
		t.Errorf("schedule view help line should contain 'v tags', got: %s", help)
	}
}

func TestToggleViewMode_HelpLine_TagView(t *testing.T) {
	task := makeTask(t, "01TV11", "task", schedule.Today, []string{"work"})
	m := newTestModel(t, task)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}
	m.mode = modeNormal

	help := m.helpLine()
	if !strings.Contains(help, "v schedule") {
		t.Errorf("tag view help line should contain 'v schedule', got: %s", help)
	}
	// Tag view normal help should not include "1-6 jump" since tabs are dynamic.
	if strings.Contains(help, "1-6 jump") {
		t.Errorf("tag view help line should not contain '1-6 jump', got: %s", help)
	}
}

func TestToggleViewMode_GrabHelpLine_TagView(t *testing.T) {
	task := makeTask(t, "01TV12", "task", schedule.Today, []string{"work"})
	m := newTestModel(t, task)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}
	m.mode = modeGrab

	help := m.helpLine()
	// Tag view grab help should not contain ←/→ bucket hint.
	if strings.Contains(help, "←/→ bucket") {
		t.Errorf("tag view grab help should not contain '←/→ bucket', got: %s", help)
	}
	if !strings.Contains(help, "GRAB") {
		t.Errorf("tag view grab help should contain 'GRAB', got: %s", help)
	}
}

func TestToggleViewMode_GrabHelpLine_ScheduleView(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeGrab

	help := m.helpLine()
	if !strings.Contains(help, "←/→ bucket") {
		t.Errorf("schedule view grab help should contain '←/→ bucket', got: %s", help)
	}
}

func TestToggleViewMode_TabBarWorksWithDynamicTabs(t *testing.T) {
	task1 := makeTask(t, "01TV13", "work task", schedule.Today, []string{"work"})
	task2 := makeTask(t, "01TV14", "home task", schedule.Today, []string{"home"})
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Toggle to tag view.
	m, _ = key(t, m, "v")

	// View should render without panicking.
	view := m.View()
	if view == "" {
		t.Error("View() returned empty string in tag view")
	}
	// Tag tabs should be visible in the rendered output.
	if !strings.Contains(view, "Active") {
		t.Error("tag view should show Active tab")
	}
	if !strings.Contains(view, "Untagged") {
		t.Error("tag view should show Untagged tab")
	}
}

// --- Task 6: Grab mode in tag view ------------------------------------------

func TestGrabTagView_LeftRightNoOp(t *testing.T) {
	// In tag view, left/right keys should be no-ops during grab mode.
	task1 := makeTask(t, "01GTL1", "task one", schedule.Today, []string{"work"})
	m := newTestModel(t, task1)
	m.width = 80
	m.height = 40

	// Switch to tag view. The cursor focuses on the "work" tab because
	// task1 was selected in schedule view and focusTaskByID finds it there.
	m, _ = key(t, m, "v")
	if m.tabs[m.activeTab].label != "work" {
		t.Fatalf("expected work tab after toggle, got %q", m.tabs[m.activeTab].label)
	}

	// Select the task (skip separator) and grab it.
	m.lists[m.activeTab].Select(1) // index 0 is separator, 1 is the task
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}

	origTab := m.activeTab
	origIndex := m.grabIndex

	// Press right — should be no-op.
	m, _ = key(t, m, "right")
	if m.activeTab != origTab {
		t.Errorf("right changed activeTab from %d to %d", origTab, m.activeTab)
	}
	if m.grabIndex != origIndex {
		t.Errorf("right changed grabIndex from %d to %d", origIndex, m.grabIndex)
	}

	// Press left — should be no-op.
	m, _ = key(t, m, "left")
	if m.activeTab != origTab {
		t.Errorf("left changed activeTab from %d to %d", origTab, m.activeTab)
	}
	if m.grabIndex != origIndex {
		t.Errorf("left changed grabIndex from %d to %d", origIndex, m.grabIndex)
	}

	// Cancel grab to leave clean state.
	m, _ = key(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode after esc = %v, want modeNormal", m.mode)
	}
}

func TestGrabTagView_SkipsSeparatorOnMoveDown(t *testing.T) {
	// Create two tasks in different buckets under the same tag so the tag
	// tab has: [sep:Today, task1, sep:Week, task2].
	task1 := model.Task{
		ID: "01GTS1", Title: "today task", Status: "open",
		Position: 1000, Tags: []string{"work"},
	}
	task1.Schedule = expectSchedule(t, schedule.Today)
	task2 := model.Task{
		ID: "01GTS2", Title: "week task", Status: "open",
		Position: 1000, Tags: []string{"work"},
	}
	task2.Schedule = expectSchedule(t, schedule.Week)
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Switch to tag view. Cursor auto-focuses on "work" tab since task1 was
	// selected and has the "work" tag.
	m, _ = key(t, m, "v")
	if m.tabs[m.activeTab].label != "work" {
		// Navigate manually if needed.
		m.activeTab = findTabByLabel(t, m, "work")
	}

	// Items should be: [sep:Today, task1, sep:Week, task2]
	items := m.lists[m.activeTab].Items()
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
	if !items[0].(item).isSeparator {
		t.Fatal("item 0 should be separator")
	}
	if items[1].(item).task.ID != "01GTS1" {
		t.Fatal("item 1 should be task1")
	}
	if !items[2].(item).isSeparator {
		t.Fatal("item 2 should be separator")
	}
	if items[3].(item).task.ID != "01GTS2" {
		t.Fatal("item 3 should be task2")
	}

	// Select task1 (index 1) and grab it.
	m.lists[m.activeTab].Select(1)
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("mode = %v, want modeGrab", m.mode)
	}
	if m.grabIndex != 1 {
		t.Fatalf("grabIndex = %d, want 1", m.grabIndex)
	}

	// Move down — should skip the separator at index 2 and land at index 3
	// (well, the task that was at index 3).
	m, _ = key(t, m, "down")

	// After the move, task1 should be in the Week group (below the Week separator).
	items = m.lists[m.activeTab].Items()
	// Find where task1 ended up and verify a Week separator is above it.
	found := false
	for i, it := range items {
		if it.(item).task.ID == "01GTS1" {
			found = true
			// There must be a separator above this task.
			foundSep := false
			for j := i - 1; j >= 0; j-- {
				if items[j].(item).isSeparator {
					label := items[j].(item).task.Title
					if label != "Week" {
						t.Errorf("nearest separator above task1 = %q, want Week", label)
					}
					foundSep = true
					break
				}
			}
			if !foundSep {
				t.Error("no separator found above task1")
			}
			break
		}
	}
	if !found {
		t.Error("task1 not found in items after move down")
	}

	// Cancel.
	m, _ = key(t, m, "esc")
}

func TestGrabTagView_SkipsSeparatorOnMoveUp(t *testing.T) {
	// [sep:Today, task1, sep:Week, task2] — grab task2, move up should
	// skip the Week separator and land at the task1 position area.
	task1 := model.Task{
		ID: "01GTU1", Title: "today task", Status: "open",
		Position: 1000, Tags: []string{"dev"},
	}
	task1.Schedule = expectSchedule(t, schedule.Today)
	task2 := model.Task{
		ID: "01GTU2", Title: "week task", Status: "open",
		Position: 1000, Tags: []string{"dev"},
	}
	task2.Schedule = expectSchedule(t, schedule.Week)
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Switch to tag view.
	m, _ = key(t, m, "v")

	// Navigate to the "dev" tab.
	m.activeTab = findTabByLabel(t, m, "dev")

	// Select task2 (index 3: [sep, t1, sep, t2]).
	m.lists[m.activeTab].Select(3)
	m, _ = key(t, m, "m")
	if m.grabIndex != 3 {
		t.Fatalf("grabIndex = %d, want 3", m.grabIndex)
	}

	// Move up — should skip the separator at index 2.
	m, _ = key(t, m, "up")

	// task2 should now be near task1 (above or below it, but not on a separator).
	items := m.lists[m.activeTab].Items()
	for i, it := range items {
		if it.(item).task.ID == "01GTU2" {
			if items[i].(item).isSeparator {
				t.Error("task2 should not be on a separator index")
			}
			// Should be before the Week separator (i.e., in the Today group).
			if i > 2 {
				t.Errorf("task2 at index %d, expected to be in Today group (<= 2)", i)
			}
			break
		}
	}

	m, _ = key(t, m, "esc")
}

func TestGrabTagView_CommitDerivesBucketFromSeparator(t *testing.T) {
	// Task starts in Today bucket. After moving it past the Week separator
	// and committing, its schedule should be updated to Week.
	task1 := model.Task{
		ID: "01GTC1", Title: "move me", Status: "open",
		Position: 1000, Tags: []string{"proj"},
	}
	task1.Schedule = expectSchedule(t, schedule.Today)
	task2 := model.Task{
		ID: "01GTC2", Title: "anchor", Status: "open",
		Position: 1000, Tags: []string{"proj"},
	}
	task2.Schedule = expectSchedule(t, schedule.Week)
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Switch to tag view.
	m, _ = key(t, m, "v")

	// Navigate to "proj" tab.
	m.activeTab = findTabByLabel(t, m, "proj")

	// Items: [sep:Today, task1, sep:Week, task2]
	items := m.lists[m.activeTab].Items()
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}

	m.lists[m.activeTab].Select(1) // task1
	m, _ = key(t, m, "m")
	if m.grabIndex != 1 {
		t.Fatalf("grabIndex = %d, want 1", m.grabIndex)
	}

	// Move down — skips separator, lands in Week group.
	m, _ = key(t, m, "down")

	// Drop (commit).
	_, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should dispatch save cmd")
	}
	m = runCmd(t, m, cmd)

	// Verify the task's schedule is now in the Week bucket.
	task, err := m.store.GetByPrefix("01GTC1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	bucket := schedule.Bucket(task.Schedule, time.Now())
	if bucket != schedule.Week {
		t.Errorf("schedule bucket = %q, want %q (schedule=%q)", bucket, schedule.Week, task.Schedule)
	}
}

func TestGrabTagView_CommitPreservesBucketWhenNoChange(t *testing.T) {
	// Two tasks in the same bucket under the same tag. Reordering within
	// the bucket should not change their schedule.
	task1 := model.Task{
		ID: "01GTP1", Title: "first", Status: "open",
		Position: 1000, Tags: []string{"misc"},
	}
	task1.Schedule = expectSchedule(t, schedule.Today)
	task2 := model.Task{
		ID: "01GTP2", Title: "second", Status: "open",
		Position: 2000, Tags: []string{"misc"},
	}
	task2.Schedule = expectSchedule(t, schedule.Today)
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Switch to tag view.
	m, _ = key(t, m, "v")

	// Navigate to "misc" tab.
	m.activeTab = findTabByLabel(t, m, "misc")

	// Items: [sep:Today, first, second]
	m.lists[m.activeTab].Select(1) // first
	m, _ = key(t, m, "m")

	// Move down within the same bucket (no separator skip needed).
	m, _ = key(t, m, "down")

	// Drop.
	_, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should dispatch save cmd")
	}
	m = runCmd(t, m, cmd)

	// Verify the schedule is still in the Today bucket.
	task, err := m.store.GetByPrefix("01GTP1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	bucket := schedule.Bucket(task.Schedule, time.Now())
	if bucket != schedule.Today {
		t.Errorf("schedule bucket = %q, want %q", bucket, schedule.Today)
	}
}

func TestFindBucketAbove(t *testing.T) {
	// Set up a model in tag view with known separator + task items.
	task1 := model.Task{
		ID: "01FBA1", Title: "today task", Status: "open",
		Position: 1000, Tags: []string{"test"},
	}
	task1.Schedule = expectSchedule(t, schedule.Today)
	task2 := model.Task{
		ID: "01FBA2", Title: "week task", Status: "open",
		Position: 1000, Tags: []string{"test"},
	}
	task2.Schedule = expectSchedule(t, schedule.Week)
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Switch to tag view.
	m, _ = key(t, m, "v")

	// Navigate to "test" tab.
	m.activeTab = findTabByLabel(t, m, "test")

	// Items: [sep:Today(0), task1(1), sep:Week(2), task2(3)]
	items := m.lists[m.activeTab].Items()
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}

	// Test: findBucketAbove with grabIndex at task1 (1) → Today
	m.grabIndex = 1
	bucket := m.findBucketAbove()
	if bucket != schedule.Today {
		t.Errorf("findBucketAbove at index 1 = %q, want %q", bucket, schedule.Today)
	}

	// Test: findBucketAbove with grabIndex at task2 (3) → Week
	m.grabIndex = 3
	bucket = m.findBucketAbove()
	if bucket != schedule.Week {
		t.Errorf("findBucketAbove at index 3 = %q, want %q", bucket, schedule.Week)
	}
}

func TestBucketNameFromLabel(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{"Today", schedule.Today},
		{"Tomorrow", schedule.Tomorrow},
		{"Week", schedule.Week},
		{"Month", schedule.Month},
		{"Someday", schedule.Someday},
		{"custom", "custom"}, // fallback
	}
	for _, tc := range tests {
		got := bucketNameFromLabel(tc.label)
		if got != tc.want {
			t.Errorf("bucketNameFromLabel(%q) = %q, want %q", tc.label, got, tc.want)
		}
	}
}

func TestGrabTagView_GKeySkipsSeparator(t *testing.T) {
	// In tag view, pressing 'g' (go to top) should skip the first separator.
	task1 := model.Task{
		ID: "01GTG1", Title: "task one", Status: "open",
		Position: 1000, Tags: []string{"alpha"},
	}
	task1.Schedule = expectSchedule(t, schedule.Today)
	task2 := model.Task{
		ID: "01GTG2", Title: "task two", Status: "open",
		Position: 2000, Tags: []string{"alpha"},
	}
	task2.Schedule = expectSchedule(t, schedule.Today)
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Switch to tag view.
	m, _ = key(t, m, "v")

	// Navigate to "alpha" tab.
	m.activeTab = findTabByLabel(t, m, "alpha")

	// Items: [sep:Today(0), task1(1), task2(2)]
	items := m.lists[m.activeTab].Items()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	m.lists[m.activeTab].Select(2) // task2
	m, _ = key(t, m, "m")
	if m.grabIndex != 2 {
		t.Fatalf("grabIndex = %d, want 2", m.grabIndex)
	}

	// Press 'g' — should go to index 1 (skipping separator at 0).
	m, _ = key(t, m, "g")
	if m.grabIndex == 0 {
		t.Error("g should not land on separator at index 0")
	}
	if m.grabIndex != 1 {
		t.Errorf("grabIndex after g = %d, want 1", m.grabIndex)
	}

	m, _ = key(t, m, "esc")
}

func TestGrabTagView_ComputeGrabPositionSkipsSeparators(t *testing.T) {
	// Verify that computeGrabPosition ignores separator items when
	// finding neighbors. After moving t1 past the Week separator, it lands
	// before t2 (between the separator and t2). The position should be
	// derived from t2 alone (no real task above, separator is skipped).
	task1 := model.Task{
		ID: "01GCP1", Title: "t1", Status: "open",
		Position: 1000, Tags: []string{"pos"},
	}
	task1.Schedule = expectSchedule(t, schedule.Today)
	task2 := model.Task{
		ID: "01GCP2", Title: "t2", Status: "open",
		Position: 5000, Tags: []string{"pos"},
	}
	task2.Schedule = expectSchedule(t, schedule.Week)
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Switch to tag view.
	m, _ = key(t, m, "v")

	// Navigate to "pos" tab.
	m.activeTab = findTabByLabel(t, m, "pos")

	// Items: [sep:Today(0), t1(1), sep:Week(2), t2(3)]
	items := m.lists[m.activeTab].Items()
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}

	// Move t1 down past Week separator.
	// After move: [sep:Today(0), sep:Week(1), t1(2), t2(3)]
	m.lists[m.activeTab].Select(1) // t1
	m, _ = key(t, m, "m")
	m, _ = key(t, m, "down") // skip separator

	// t1 is now between the Week separator and t2. computeGrabPosition
	// should skip both separators (prev=nil, next=t2) and return t2.Position/2.
	pos := m.computeGrabPosition("01GCP1")
	if pos <= 0 || pos >= 5000 {
		t.Errorf("position = %.1f, want between 0 and 5000 (before t2, separators skipped)", pos)
	}
	// Specifically, with prev=nil and next=5000, it should be 5000/2 = 2500.
	if pos != 2500 {
		t.Errorf("position = %.1f, want 2500.0", pos)
	}

	m, _ = key(t, m, "esc")
}

// --- newModel with Options tests ---

// newTestModelWithOpts is like newTestModel but accepts Options to configure
// the model at creation time.
func newTestModelWithOpts(t *testing.T, opts Options, tasks ...model.Task) *Model {
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
	if len(tasks) > 0 {
		if err := git.AutoCommit(repoPath, "seed", "."); err != nil {
			t.Fatalf("seed commit: %v", err)
		}
	}
	m, err := newModel(s, repoPath, opts)
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	return m
}

func TestNewModel_DefaultStartsInScheduleView(t *testing.T) {
	m := newTestModelWithOpts(t, Options{})
	if m.viewMode != viewSchedule {
		t.Errorf("default viewMode = %d, want viewSchedule (0)", m.viewMode)
	}
	if len(m.tabs) != len(defaultTabs) {
		t.Errorf("tab count = %d, want %d (default schedule tabs)", len(m.tabs), len(defaultTabs))
	}
}

func TestNewModel_StartInTagView(t *testing.T) {
	m := newTestModelWithOpts(t, Options{StartInTagView: true},
		model.Task{ID: "01TAG1", Title: "task with tag", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Tags: []string{"work"},
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01TAG2", Title: "untagged task", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Tags: nil,
			Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	if m.viewMode != viewTag {
		t.Errorf("viewMode = %d, want viewTag (1)", m.viewMode)
	}
	// Should have tag tabs: Active, work, Untagged
	if len(m.tabs) != 3 {
		t.Errorf("tab count = %d, want 3 (Active, work, Untagged)", len(m.tabs))
	}
	if m.tabs[0].label != "Active" {
		t.Errorf("tabs[0].label = %q, want %q", m.tabs[0].label, "Active")
	}
	if m.tabs[1].label != "work" {
		t.Errorf("tabs[1].label = %q, want %q", m.tabs[1].label, "work")
	}
	if m.tabs[2].label != "Untagged" {
		t.Errorf("tabs[2].label = %q, want %q", m.tabs[2].label, "Untagged")
	}
}

func TestNewModel_StartInTagViewNoTasks(t *testing.T) {
	m := newTestModelWithOpts(t, Options{StartInTagView: true})

	if m.viewMode != viewTag {
		t.Errorf("viewMode = %d, want viewTag (1)", m.viewMode)
	}
	// Even with no tasks, should have at least Active + Untagged tabs.
	if len(m.tabs) < 2 {
		t.Errorf("tab count = %d, want at least 2 (Active, Untagged)", len(m.tabs))
	}
}

// --- finding 1: moveGrabTo G key regression fix ---

func TestMoveGrabTo_GKeyPlacesLast(t *testing.T) {
	// Create 5 tasks in the same bucket so they appear in order.
	var tasks []model.Task
	for i, title := range []string{"A", "B", "C", "D", "E"} {
		tasks = append(tasks, model.Task{
			ID: fmt.Sprintf("01GK%d", i+1), Title: title, Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: float64((i + 1) * 1000),
			UpdatedAt: "2026-04-13T00:00:00Z",
		})
	}
	m := newTestModel(t, tasks...)
	m.width = 80
	m.height = 40

	// Enter grab mode on the second item (B, index 1).
	m.activeTab = 0
	m.lists[m.activeTab].Select(1)
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatal("expected grab mode")
	}
	if m.grabIndex != 1 {
		t.Fatalf("grabIndex = %d, want 1", m.grabIndex)
	}

	// Press G (go to bottom).
	m.moveGrabTo(len(m.lists[m.activeTab].Items()) - 1)

	items := m.lists[m.activeTab].Items()
	last := items[len(items)-1].(item)
	if last.task.Title != "B" {
		t.Errorf("last item title = %q, want %q (B should be at the bottom)", last.task.Title, "B")
	}
}

// --- finding 11a: findBucketAbove edge cases ---

func TestFindBucketAbove_GrabAtZero(t *testing.T) {
	task1 := model.Task{
		ID: "01FBZ1", Title: "task1", Status: "open",
		Position: 1000, Tags: []string{"test"},
		Schedule:  expectSchedule(t, schedule.Today),
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	m := newTestModel(t, task1)
	m.width = 80
	m.height = 40

	// Switch to tag view.
	m, _ = key(t, m, "v")

	// Navigate to "test" tab.
	m.activeTab = findTabByLabel(t, m, "test")

	// Items: [sep:Today(0), task1(1)]
	// Set grabIndex to 0 (separator position — edge case).
	m.grabIndex = 0
	bucket := m.findBucketAbove()
	// No separator above index 0, should return empty.
	if bucket != "" {
		t.Errorf("findBucketAbove at index 0 = %q, want empty string", bucket)
	}
}

func TestFindBucketAbove_NoSeparators(t *testing.T) {
	// Build a model in schedule view (no separators in schedule view lists).
	task1 := model.Task{
		ID: "01FBN1", Title: "task1", Status: "open",
		Position:  1000,
		Schedule:  expectSchedule(t, schedule.Today),
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	m := newTestModel(t, task1)
	m.width = 80
	m.height = 40

	// Stay in schedule view. grabIndex = 0.
	m.grabIndex = 0
	bucket := m.findBucketAbove()
	if bucket != "" {
		t.Errorf("findBucketAbove with no separators = %q, want empty", bucket)
	}
}

// --- finding 11b: reloadTagTab out-of-bounds ---

func TestReloadTagTab_OutOfBounds(t *testing.T) {
	task := model.Task{
		ID: "01RTB1", Title: "task", Status: "open",
		Position: 1000, Tags: []string{"test"},
		Schedule:  expectSchedule(t, schedule.Today),
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	m := newTestModel(t, task)
	m.width = 80
	m.height = 40
	m, _ = key(t, m, "v")

	// Attempt reload with an out-of-bounds index.
	err := m.reloadTagTab(999)
	if err == nil {
		t.Error("expected error for out-of-bounds tag tab index, got nil")
	}
	err = m.reloadTagTab(-1)
	if err == nil {
		t.Error("expected error for negative tag tab index, got nil")
	}
}

// --- finding 11c: commitGrab in tag view when no separator found ---

func TestGrabTagView_CommitNoSeparator(t *testing.T) {
	// A tag tab with a single task and no separators would be unusual,
	// but test that commitGrab does not crash and preserves the schedule.
	task := model.Task{
		ID: "01CNS1", Title: "task", Status: "open",
		Position: 1000, Tags: []string{"test"},
		Schedule:  expectSchedule(t, schedule.Today),
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	m := newTestModel(t, task)
	m.width = 80
	m.height = 40
	m, _ = key(t, m, "v")

	// Navigate to "test" tab.
	m.activeTab = findTabByLabel(t, m, "test")

	// Enter grab mode.
	items := m.lists[m.activeTab].Items()
	// Find the first non-separator item.
	for i, it := range items {
		if !it.(item).isSeparator {
			m.lists[m.activeTab].Select(i)
			break
		}
	}
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatal("expected grab mode")
	}

	// Commit grab — should produce a cmd without panicking.
	cmd := m.commitGrab()
	if cmd == nil {
		t.Error("commitGrab returned nil cmd")
	}
}

// --- finding 2: refreshTagTabs rebuilds tabs after mutation ---

func TestReloadAll_RebuildTagTabs(t *testing.T) {
	task1 := model.Task{
		ID: "01RTA1", Title: "task1", Status: "open",
		Position: 1000, Tags: []string{"alpha"},
		Schedule:  expectSchedule(t, schedule.Today),
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	m := newTestModel(t, task1)
	m.width = 80
	m.height = 40
	m, _ = key(t, m, "v")

	// Should have Active, alpha, Untagged = 3 tabs.
	if len(m.tabs) != 3 {
		t.Fatalf("initial tab count = %d, want 3", len(m.tabs))
	}

	// Create a task with a new tag directly in the store.
	task2 := model.Task{
		ID: "01RTA2", Title: "task2", Status: "open",
		Position: 2000, Tags: []string{"beta"},
		Schedule:  expectSchedule(t, schedule.Today),
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	if err := m.store.Create(task2); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	// reloadAll should detect the new tag and add a tab.
	if err := m.reloadAll(); err != nil {
		t.Fatalf("reloadAll: %v", err)
	}

	// Should now have Active, alpha, beta, Untagged = 4 tabs.
	if len(m.tabs) != 4 {
		t.Errorf("tab count after new tag = %d, want 4", len(m.tabs))
	}
	findTabByLabel(t, m, "beta") // verifies "beta" tab exists
}

// --- finding 13: skipSeparator ---

func TestSkipSeparator_CursorSkipsPastSeparator(t *testing.T) {
	task1 := model.Task{
		ID: "01SKP1", Title: "today task", Status: "open",
		Position: 1000, Tags: []string{"test"},
		Schedule:  expectSchedule(t, schedule.Today),
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	task2 := model.Task{
		ID: "01SKP2", Title: "week task", Status: "open",
		Position: 1000, Tags: []string{"test"},
		Schedule:  expectSchedule(t, schedule.Week),
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40
	m.recomputeLayout()

	// Switch to tag view.
	m, _ = key(t, m, "v")

	// Navigate to "test" tab.
	m.activeTab = findTabByLabel(t, m, "test")

	// Items: [sep:Today(0), task1(1), sep:Week(2), task2(3)]
	items := m.lists[m.activeTab].Items()
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}

	// Select the today task (index 1), then skip should be a no-op.
	m.lists[m.activeTab].Select(1)
	m.skipSeparator(1)
	if m.lists[m.activeTab].Index() != 1 {
		t.Errorf("cursor after skipSeparator on non-separator = %d, want 1", m.lists[m.activeTab].Index())
	}

	// Manually place cursor on separator at index 2 (simulating a down move from 1).
	m.lists[m.activeTab].Select(2)
	m.skipSeparator(1) // direction is down
	if m.lists[m.activeTab].Index() != 3 {
		t.Errorf("cursor after skipping separator downward = %d, want 3", m.lists[m.activeTab].Index())
	}

	// Place cursor on separator at index 2 (simulating an up move from 3).
	m.lists[m.activeTab].Select(2)
	m.skipSeparator(-1) // direction is up
	if m.lists[m.activeTab].Index() != 1 {
		t.Errorf("cursor after skipping separator upward = %d, want 1", m.lists[m.activeTab].Index())
	}
}

// --- finding 3: separator avoidance on tab switch ---

func TestTagView_TabSwitchSkipsSeparator(t *testing.T) {
	// Create tasks with two different tags so we get multiple tabs, each
	// starting with a separator at index 0.
	task1 := model.Task{
		ID: "01TSS1", Title: "work task", Status: "open",
		Schedule: expectSchedule(t, schedule.Today),
		Tags:     []string{"work"}, Position: 1000,
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	task2 := model.Task{
		ID: "01TSS2", Title: "hobby task", Status: "open",
		Schedule: expectSchedule(t, schedule.Today),
		Tags:     []string{"hobby"}, Position: 1000,
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	m := newTestModel(t, task1, task2)
	m.width = 80
	m.height = 40

	// Switch to tag view.
	m, _ = key(t, m, "v")
	if m.viewMode != viewTag {
		t.Fatal("expected tag view mode")
	}

	// Find the "hobby" and "work" tabs.
	hobbyIdx := findTabByLabel(t, m, "hobby")
	workIdx := findTabByLabel(t, m, "work")

	// Navigate to the hobby tab first.
	m.activeTab = hobbyIdx
	m.recomputeLayout()

	// Items in each tag tab start with a separator. Use left/right to switch.
	// Right key:
	m, _ = key(t, m, "right")
	items := m.lists[m.activeTab].Items()
	if len(items) > 0 {
		cur := m.lists[m.activeTab].Index()
		if it, ok := items[cur].(item); ok && it.isSeparator {
			t.Errorf("right-key tab switch: cursor at index %d is a separator", cur)
		}
	}

	// Left key:
	m, _ = key(t, m, "left")
	items = m.lists[m.activeTab].Items()
	if len(items) > 0 {
		cur := m.lists[m.activeTab].Index()
		if it, ok := items[cur].(item); ok && it.isSeparator {
			t.Errorf("left-key tab switch: cursor at index %d is a separator", cur)
		}
	}

	// Number key (jump to work tab):
	if workIdx < 6 { // number keys only work for 1-6
		m, _ = key(t, m, fmt.Sprintf("%d", workIdx+1))
		items = m.lists[m.activeTab].Items()
		if len(items) > 0 {
			cur := m.lists[m.activeTab].Index()
			if it, ok := items[cur].(item); ok && it.isSeparator {
				t.Errorf("number-key tab switch: cursor at index %d is a separator", cur)
			}
		}
	}
}

// --- finding 4: createCmd defaults to today in tag view ---

func TestCreateCmd_TagViewDefaultsToToday(t *testing.T) {
	task1 := model.Task{
		ID: "01CTV1", Title: "existing task", Status: "open",
		Schedule: expectSchedule(t, schedule.Today),
		Tags:     []string{"work"}, Position: 1000,
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	m := newTestModel(t, task1)
	m.width = 80
	m.height = 40

	// Switch to tag view.
	m, _ = key(t, m, "v")
	if m.viewMode != viewTag {
		t.Fatal("expected tag view mode")
	}

	// In tag view, all tabs have bucket="" which should default to "today".
	// Navigate to the "work" tab.
	m.activeTab = findTabByLabel(t, m, "work")

	// Verify the tab bucket is empty (tag view tabs have no bucket).
	if m.tabs[m.activeTab].bucket != "" {
		t.Fatalf("expected empty bucket in tag view tab, got %q", m.tabs[m.activeTab].bucket)
	}

	// Create a task from the tag view tab.
	cmd := m.createCmd("new task from tag view", nil, "")
	if cmd == nil {
		t.Fatal("createCmd returned nil")
	}

	// Execute the command to get the message.
	msg := cmd()
	saved, ok := msg.(taskSavedMsg)
	if !ok {
		t.Fatalf("expected taskSavedMsg, got %T", msg)
	}
	if saved.err != nil {
		t.Fatalf("createCmd error: %v", saved.err)
	}

	// Verify the created task has today's schedule.
	if saved.focusID == "" {
		t.Fatal("expected focusID to be set")
	}

	// Read back the created task from the store.
	task, err := m.store.Get(saved.focusID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	todayDate := expectSchedule(t, schedule.Today)
	if task.Schedule != todayDate {
		t.Errorf("created task schedule = %q, want %q (today)", task.Schedule, todayDate)
	}
}

func TestHelpLine_ScheduleView_UpdatedLabels(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeNormal
	m.viewMode = viewSchedule

	help := m.helpLine()
	for _, want := range []string{"r date", "c create", "h help"} {
		if !strings.Contains(help, want) {
			t.Errorf("schedule view help line missing %q, got: %s", want, help)
		}
	}
	for _, old := range []string{"r resched", "c add"} {
		if strings.Contains(help, old) {
			t.Errorf("schedule view help line should not contain old label %q, got: %s", old, help)
		}
	}
}

func TestHelpLine_TagView_UpdatedLabels(t *testing.T) {
	task := makeTask(t, "01HH01", "task", schedule.Today, []string{"work"})
	m := newTestModel(t, task)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}
	m.mode = modeNormal

	help := m.helpLine()
	for _, want := range []string{"r date", "c create", "h help"} {
		if !strings.Contains(help, want) {
			t.Errorf("tag view help line missing %q, got: %s", want, help)
		}
	}
}

func TestHelpMode_HKeyEntersModeHelp(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeNormal

	m, _ = key(t, m, "h")
	if m.mode != modeHelp {
		t.Errorf("after 'h', mode = %v, want modeHelp", m.mode)
	}
}

func TestHelpMode_AnyKeyClosesModal(t *testing.T) {
	for _, k := range []string{"esc", "q", "enter", "j"} {
		t.Run(k, func(t *testing.T) {
			m := newTestModel(t)
			m.mode = modeHelp
			m, _ = key(t, m, k)
			if m.mode != modeNormal {
				t.Errorf("after %q in modeHelp, mode = %v, want modeNormal", k, m.mode)
			}
		})
	}
}

func TestHelpMode_HelpLineText(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeHelp

	help := m.helpLine()
	if !strings.Contains(help, "press any key to close") {
		t.Errorf("modeHelp help line = %q, want to contain 'press any key to close'", help)
	}
}

func TestHelpMode_ModalViewNonEmpty(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeHelp

	view := m.modalView()
	if view == "" {
		t.Error("modeHelp modalView() returned empty string")
	}
	if !strings.Contains(view, "Keybindings") {
		t.Errorf("modeHelp modalView() should contain 'Keybindings', got: %s", view)
	}
}

func TestHelpLine_ScheduleView_EnterNotes(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeNormal
	m.viewMode = viewSchedule

	help := m.helpLine()
	if !strings.Contains(help, "enter") || !strings.Contains(help, "notes") {
		t.Errorf("schedule view help line should contain 'enter notes', got: %s", help)
	}
}

func TestHelpLine_TagView_EnterNotes(t *testing.T) {
	task := makeTask(t, "01HN01", "task", schedule.Today, []string{"work"})
	m := newTestModel(t, task)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}
	m.mode = modeNormal

	help := m.helpLine()
	if !strings.Contains(help, "enter") || !strings.Contains(help, "notes") {
		t.Errorf("tag view help line should contain 'enter notes', got: %s", help)
	}
}

func TestHelpLine_DetailOpen_ShowsPanelKeys(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01HN02", Title: "test task", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-16T00:00:00Z"},
	)
	m.mode = modeNormal
	m.detailOpen = true

	help := m.helpLine()
	for _, want := range []string{"esc", "close", "enter", "submit", "alt+enter", "newline"} {
		if !strings.Contains(help, want) {
			t.Errorf("detail open help line should contain %q, got: %s", want, help)
		}
	}
	// Should NOT contain normal mode keys like "done", "edit", "grab"
	for _, notWant := range []string{"done", "edit", "grab"} {
		if strings.Contains(help, notWant) {
			t.Errorf("detail open help line should not contain %q, got: %s", notWant, help)
		}
	}
}

// TestHelpLine_NormalMode_ContainsSearchHint verifies the `/` search key is
// advertised in the normal-mode help bar for both schedule and tag views.
func TestHelpLine_NormalMode_ContainsSearchHint_ScheduleView(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeNormal
	m.viewMode = viewSchedule

	help := m.helpLine()
	if !strings.Contains(help, "/") {
		t.Errorf("schedule view help line missing '/' hint, got: %s", help)
	}
	if !strings.Contains(strings.ToLower(help), "search") {
		t.Errorf("schedule view help line missing 'search' hint, got: %s", help)
	}
}

func TestHelpLine_NormalMode_ContainsSearchHint_TagView(t *testing.T) {
	task := makeTask(t, "01HS01", "task", schedule.Today, []string{"work"})
	m := newTestModel(t, task)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}
	m.mode = modeNormal

	help := m.helpLine()
	if !strings.Contains(help, "/") {
		t.Errorf("tag view help line missing '/' hint, got: %s", help)
	}
	if !strings.Contains(strings.ToLower(help), "search") {
		t.Errorf("tag view help line missing 'search' hint, got: %s", help)
	}
}

// TestHelpModal_ContainsSearchSection ensures the Help modal lists the search
// entry key and a brief description of search keybindings.
func TestHelpModal_ContainsSearchSection(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeHelp

	view := m.modalView()
	if !strings.Contains(view, "/") {
		t.Errorf("help modal missing '/' key: %s", view)
	}
	if !strings.Contains(strings.ToLower(view), "search") {
		t.Errorf("help modal missing 'search' label: %s", view)
	}
}

func TestDone_SetsCompletedAt(t *testing.T) {
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
	task, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.CompletedAt == "" {
		t.Error("CompletedAt should be set after marking done")
	}
	// CompletedAt should be a valid RFC3339 timestamp.
	if _, err := time.Parse(time.RFC3339, task.CompletedAt); err != nil {
		t.Errorf("CompletedAt %q is not valid RFC3339: %v", task.CompletedAt, err)
	}
}

// TestDone_RecurringSpawnsNewTaskInTUI pins the behavior that pressing 'd'
// on a task with a recurrence rule spawns a next-occurrence task, matching
// the CLI `monolog done` behavior. Without this, TUI users completing
// recurring chores would silently get no spawn.
func TestDone_RecurringSpawnsNewTaskInTUI(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "weekly chore", Status: "open", Schedule: "today",
			Recurrence: "days:1", Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("d should return a save cmd")
	}
	m = runCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("save error: %v", m.err)
	}

	// Original task is now done.
	orig, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	if orig.Status != "done" {
		t.Errorf("original Status: got %q, want done", orig.Status)
	}
	if !strings.Contains(orig.Body, "Spawned follow-up:") {
		t.Errorf("original Body missing 'Spawned follow-up:' back-reference:\n%s", orig.Body)
	}

	// A new task exists with the same recurrence rule.
	all, err := m.store.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 tasks after TUI done on recurring, got %d", len(all))
	}
	var spawn model.Task
	for _, tk := range all {
		if tk.ID != orig.ID {
			spawn = tk
			break
		}
	}
	if spawn.ID == "" {
		t.Fatal("spawn not found")
	}
	if spawn.Status != "open" {
		t.Errorf("spawn Status: got %q, want open", spawn.Status)
	}
	if spawn.Recurrence != "days:1" {
		t.Errorf("spawn Recurrence: got %q, want 'days:1'", spawn.Recurrence)
	}
	if spawn.Title != "weekly chore" {
		t.Errorf("spawn Title: got %q, want 'weekly chore'", spawn.Title)
	}
	if !strings.Contains(spawn.Body, "Spawned from "+orig.ID) {
		t.Errorf("spawn Body missing 'Spawned from %s':\n%s", orig.ID, spawn.Body)
	}
}

// TestDone_InvalidRecurrenceInTUI_SurfacesWarning verifies that when a
// task's stored Recurrence does not parse (hand-edited JSON, schema
// regression, etc.), the TUI still completes the task but surfaces a
// warning through the status bar. Completion must never be blocked.
func TestDone_InvalidRecurrenceInTUI_SurfacesWarning(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "bogus recur", Status: "open", Schedule: "today",
			Recurrence: "bogus", Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("d should return a save cmd")
	}
	m = runCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("save error (invalid recurrence must not block done): %v", m.err)
	}

	// Task is still completed.
	got, err := m.store.GetByPrefix("01A")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("Status: got %q, want done", got.Status)
	}
	// No spawn happened.
	all, _ := m.store.List(store.ListOptions{})
	if len(all) != 1 {
		t.Errorf("expected 1 task (no spawn for invalid recurrence), got %d", len(all))
	}
	// Warning surfaced in the status bar.
	if !strings.Contains(m.statusMsg, "warning") {
		t.Errorf("statusMsg should contain the spawn warning, got: %q", m.statusMsg)
	}
	if !strings.Contains(m.statusMsg, "bogus") {
		t.Errorf("statusMsg should mention the invalid rule value, got: %q", m.statusMsg)
	}
}

// TestDone_NonRecurringInTUI is the negative counterpart — a plain task
// completes without spawning. Guards against the spawn logic accidentally
// firing for non-recurring tasks.
func TestDone_NonRecurringInTUI(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "one-shot", Status: "open", Schedule: "today",
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
	all, err := m.store.List(store.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 task (no spawn for non-recurring), got %d", len(all))
	}
}

func TestStats_PopulatedAfterNewModel(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "open one", Status: "open", Schedule: "today",
			Position: 1000, CreatedAt: "2026-04-10T00:00:00Z", UpdatedAt: "2026-04-10T00:00:00Z"},
		model.Task{ID: "01B", Title: "open two", Status: "open", Schedule: "today",
			Position: 2000, CreatedAt: "2026-04-12T00:00:00Z", UpdatedAt: "2026-04-12T00:00:00Z"},
		model.Task{ID: "01C", Title: "done one", Status: "done", Schedule: "today",
			Position: 3000, CreatedAt: "2026-04-01T00:00:00Z", UpdatedAt: "2026-04-11T00:00:00Z",
			CompletedAt: "2026-04-11T00:00:00Z"},
	)
	if m.stats.Total != 3 {
		t.Errorf("stats.Total = %d, want 3", m.stats.Total)
	}
	if m.stats.Open != 2 {
		t.Errorf("stats.Open = %d, want 2", m.stats.Open)
	}
	if m.stats.Done != 1 {
		t.Errorf("stats.Done = %d, want 1", m.stats.Done)
	}
	if m.stats.AvgDaysToComplete == 0 {
		t.Error("stats.AvgDaysToComplete should be non-zero (one done task has CompletedAt)")
	}
	if len(m.allTasks) != 3 {
		t.Errorf("allTasks len = %d, want 3", len(m.allTasks))
	}
}

func TestStatsBarView_ScheduleView(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "open today", Status: "open", Schedule: "today",
			Position: 1000, CreatedAt: "2026-04-10T00:00:00Z", UpdatedAt: "2026-04-10T00:00:00Z"},
		model.Task{ID: "01B", Title: "open tomorrow", Status: "open", Schedule: "tomorrow",
			Position: 1000, CreatedAt: "2026-04-12T00:00:00Z", UpdatedAt: "2026-04-12T00:00:00Z"},
		model.Task{ID: "01C", Title: "done one", Status: "done", Schedule: "today",
			Position: 2000, CreatedAt: "2026-04-01T00:00:00Z", UpdatedAt: "2026-04-11T00:00:00Z",
			CompletedAt: "2026-04-11T00:00:00Z"},
	)

	bar := m.statsBarView()
	if !strings.Contains(bar, "3 tasks") {
		t.Errorf("statsBarView missing '3 tasks': %q", bar)
	}
	if !strings.Contains(bar, "2 open") {
		t.Errorf("statsBarView missing '2 open': %q", bar)
	}
	if !strings.Contains(bar, "1 done") {
		t.Errorf("statsBarView missing '1 done': %q", bar)
	}
	if !strings.Contains(bar, "in tab") {
		t.Errorf("statsBarView missing 'in tab': %q", bar)
	}
	// separator between overall and tab-specific sections
	if !strings.Contains(bar, "|") {
		t.Errorf("statsBarView missing '|' separator: %q", bar)
	}
	// avg fields present (have data; CreatedAt is set for both open tasks)
	if !strings.Contains(bar, "~") {
		t.Errorf("statsBarView should show avg (~) when CreatedAt and CompletedAt are set: %q", bar)
	}
	// tag-done should NOT appear in schedule view
	if strings.Contains(bar, "tag-done") {
		t.Errorf("statsBarView should not contain 'tag-done' in schedule view: %q", bar)
	}
}

func TestStatsBarView_TagView_WorkTab(t *testing.T) {
	m := newTestModelWithOpts(t, Options{StartInTagView: true},
		model.Task{ID: "01A", Title: "work open", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Tags: []string{"work"},
			Position: 1000, CreatedAt: "2026-04-10T00:00:00Z", UpdatedAt: "2026-04-10T00:00:00Z"},
		model.Task{ID: "01B", Title: "work done", Status: "done",
			Schedule: expectSchedule(t, schedule.Today), Tags: []string{"work"},
			Position: 2000, CreatedAt: "2026-04-01T00:00:00Z", UpdatedAt: "2026-04-08T00:00:00Z",
			CompletedAt: "2026-04-08T00:00:00Z"},
	)
	// Navigate to the "work" tab (index 1 after Active).
	workIdx := findTabByLabel(t, m, "work")
	m.activeTab = workIdx

	bar := m.statsBarView()
	if !strings.Contains(bar, "2 tasks") {
		t.Errorf("statsBarView missing '2 tasks': %q", bar)
	}
	if !strings.Contains(bar, "tag-done") {
		t.Errorf("statsBarView missing 'tag-done' on work tab: %q", bar)
	}
	if !strings.Contains(bar, "|") {
		t.Errorf("statsBarView missing '|' separator: %q", bar)
	}
}

func TestStatsBarView_TagView_ActiveTabNoTagDone(t *testing.T) {
	m := newTestModelWithOpts(t, Options{StartInTagView: true},
		model.Task{ID: "01A", Title: "work open", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Tags: []string{"work"},
			Position: 1000, CreatedAt: "2026-04-10T00:00:00Z", UpdatedAt: "2026-04-10T00:00:00Z"},
	)
	// Active tab is always index 0.
	m.activeTab = 0

	bar := m.statsBarView()
	if strings.Contains(bar, "tag-done") {
		t.Errorf("statsBarView should not show 'tag-done' on Active tab: %q", bar)
	}
	if !strings.Contains(bar, "in tab") {
		t.Errorf("statsBarView missing 'in tab' on Active tab: %q", bar)
	}
}

func TestStatsBarView_NoAvgWhenNoData(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "open", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-10T00:00:00Z"}, // no CreatedAt
	)
	bar := m.statsBarView()
	// AvgDaysOpen should be absent when CreatedAt is empty (can't compute).
	// AvgDaysToComplete should be absent when no done tasks with CompletedAt.
	if strings.Contains(bar, "~") {
		t.Errorf("statsBarView should not show avg (~) when no data: %q", bar)
	}
}

func TestStats_UpdateAfterDone(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task", Status: "open", Schedule: "today",
			Position: 1000, CreatedAt: "2026-04-10T00:00:00Z", UpdatedAt: "2026-04-10T00:00:00Z"},
	)
	if m.stats.Open != 1 || m.stats.Done != 0 {
		t.Fatalf("initial stats wrong: open=%d done=%d", m.stats.Open, m.stats.Done)
	}
	m, cmd := key(t, m, "d")
	if cmd == nil {
		t.Fatal("d should return a save cmd")
	}
	m = runCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("save error: %v", m.err)
	}
	if m.stats.Open != 0 {
		t.Errorf("after done: stats.Open = %d, want 0", m.stats.Open)
	}
	if m.stats.Done != 1 {
		t.Errorf("after done: stats.Done = %d, want 1", m.stats.Done)
	}
}

// --- multi-line title tests -------------------------------------------------

func TestAdd_AltEnterInsertsNewline(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	if m.mode != modeAdd {
		t.Fatalf("mode = %v, want modeAdd", m.mode)
	}
	m = typeString(t, m, "first")
	// Alt+Enter should insert a newline, not submit.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = next.(*Model)
	if cmd != nil {
		// Running the cmd would advance textinput.Blink; nothing destructive.
		_ = cmd
	}
	if m.mode != modeAdd {
		t.Fatalf("after alt+enter: mode = %v, want still modeAdd", m.mode)
	}
	m = typeString(t, m, "second")

	// Plain Enter now submits.
	m, cmd = key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should submit")
	}
	m = runCmd(t, m, cmd)

	items := m.lists[0].Items()
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	got := items[0].(item).task.Title
	want := "first\nsecond"
	if got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
}

func TestAdd_AltEnterKeepsFirstLineVisible(t *testing.T) {
	// Regression: after Alt+Enter the textarea viewport must not scroll such
	// that the first line disappears.
	//
	// Subtlety: viewport.ScrollDown no-ops when viewport.lines is empty
	// (bubbles/viewport.go). The real TUI calls View() on every render, which
	// populates viewport.lines via SetContent — so the scroll *does* happen
	// in production. We call titleArea.View() between operations here to
	// reproduce that behavior; without it the bug is silently hidden.
	m := newTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "c")
	m = typeString(t, m, "first")
	_ = m.titleArea.View() // simulate a render cycle so viewport.lines is populated
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = next.(*Model)
	_ = m.titleArea.View()
	m = typeString(t, m, "second")

	view := m.titleArea.View()
	t.Logf("view=%q", view)
	lines := strings.Split(view, "\n")
	if len(lines) < 2 {
		t.Fatalf("titleArea.View() should render 2 lines, got %d: %q", len(lines), view)
	}
	if !strings.Contains(lines[0], "first") {
		t.Errorf("line 0 missing 'first' (viewport scrolled off): %q", lines[0])
	}
	if !strings.Contains(lines[1], "second") {
		t.Errorf("line 1 missing 'second': %q", lines[1])
	}
}

func TestAdd_EnterWithoutAltSubmitsSingleLine(t *testing.T) {
	m := newTestModel(t)
	m, _ = key(t, m, "c")
	m = typeString(t, m, "single line")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should submit")
	}
	m = runCmd(t, m, cmd)
	got := m.lists[0].Items()[0].(item).task.Title
	if got != "single line" {
		t.Errorf("Title = %q, want %q", got, "single line")
	}
}

func TestWrapText_SplitsOnNewlines(t *testing.T) {
	got := wrapText("line one\nline two", 20)
	want := []string{"line one", "line two"}
	if !sliceEq(got, want) {
		t.Errorf("wrapText = %v, want %v", got, want)
	}
}

func TestWrapText_WrapsEachLineIndependently(t *testing.T) {
	// First line fits; second line wraps at the space.
	got := wrapText("short\none two three", 7)
	want := []string{"short", "one two", "three"}
	if !sliceEq(got, want) {
		t.Errorf("wrapText = %v, want %v", got, want)
	}
}

// TestWrapTextPreservingURLs_KeepsLongURLIntact confirms that a URL longer
// than width is NOT broken across lines — it occupies its own line in
// full, even though that line exceeds width.
func TestWrapTextPreservingURLs_KeepsLongURLIntact(t *testing.T) {
	url := "https://example.com/some/very/long/path/to/resource"
	got := wrapTextPreservingURLs(url, 20)
	if len(got) != 1 || got[0] != url {
		t.Errorf("wrapTextPreservingURLs kept URL split:\n got = %v\n want = [%q]", got, url)
	}
}

// TestWrapTextPreservingURLs_URLAmongText puts a URL in a sentence whose
// overall length exceeds width. The non-URL text wraps normally; the URL
// stays on a single line.
func TestWrapTextPreservingURLs_URLAmongText(t *testing.T) {
	// Very narrow width forces wrapping in the non-URL text.
	in := "see https://example.com/path for more info"
	got := wrapTextPreservingURLs(in, 12)
	// Every output line that contains a URL must contain the FULL URL.
	urlFragment := "https://example.com/path"
	for _, ln := range got {
		if !strings.Contains(ln, "https") {
			continue
		}
		if !strings.Contains(ln, urlFragment) {
			t.Errorf("line contains partial URL — URL was split:\n line = %q\n full URL = %q\n all = %v",
				ln, urlFragment, got)
		}
	}
}

// TestWrapTextPreservingURLs_NoURLMatchesWrapText confirms the URL-aware
// wrap is a strict superset of wrapText for URL-free input.
func TestWrapTextPreservingURLs_NoURLMatchesWrapText(t *testing.T) {
	in := "the quick brown fox jumps over the lazy dog"
	width := 12
	gotPreserving := wrapTextPreservingURLs(in, width)
	gotPlain := wrapText(in, width)
	if !sliceEq(gotPreserving, gotPlain) {
		t.Errorf("URL-free wrap should equal wrapText;\n got preserving = %v\n got plain = %v",
			gotPreserving, gotPlain)
	}
}

// TestWrapLinePreservingURLs_CollapsesMultipleSpaces pins the known
// behavioral divergence from wrapLine: once the URL-aware tokenizer runs
// (i.e., when input doesn't short-circuit via the early-return width
// check), non-URL spans are tokenized with strings.Fields, which collapses
// runs of whitespace to a single space. This test documents that
// behavior so it doesn't drift silently. (wrapLine preserves consecutive
// spaces inside words; wrapLinePreservingURLs collapses them.)
func TestWrapLinePreservingURLs_CollapsesMultipleSpaces(t *testing.T) {
	// Two spaces between "a" and "b"; URL in the middle forces the
	// preserving branch. Width smaller than total length so the early
	// "fits in one line" return does not fire.
	in := "a  b https://example.com/p c  d" // 31 runes
	got := wrapLinePreservingURLs(in, 28)
	// On the first line, "a  b" collapses to "a b"; " c  d" suffix collapses to " c d".
	// URL stays atomic; overflow to line 2 if needed.
	// Expected: ["a b https://example.com/p c", "d"] — multi-space collapsed.
	if len(got) == 0 {
		t.Fatalf("wrapLinePreservingURLs returned 0 lines")
	}
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "  ") {
		t.Errorf("expected runs of spaces to collapse in URL-aware wrap;\n joined = %q\n lines = %v", joined, got)
	}
	// Exact pin for regression detection:
	wantFirst := "a b https://example.com/p c"
	if got[0] != wantFirst {
		t.Errorf("wrapLinePreservingURLs first line:\n got  = %q\n want = %q", got[0], wantFirst)
	}
}

// TestWrapLinePreservingURLs_LeadingWhitespaceBeforeURLDropped pins the
// known behavioral divergence from wrapLine: when the tokenizer runs
// (no early-return), a leading-whitespace-only segment before the first
// URL is dropped entirely — strings.Fields on a whitespace-only string
// returns zero words, and the trailingSpace branch can't fire because
// curW is still 0 at that point. Documenting so changes surface as
// test failures.
func TestWrapLinePreservingURLs_LeadingWhitespaceBeforeURLDropped(t *testing.T) {
	// 3 leading spaces + URL (21 runes) = 24 total. Use width 20 to force
	// the tokenizer to run (bypass early "fits" return).
	in := "   https://example.com/p" // 24 runes
	got := wrapLinePreservingURLs(in, 20)
	if len(got) == 0 {
		t.Fatalf("wrapLinePreservingURLs returned 0 lines")
	}
	// The leading whitespace-only segment is dropped; URL stands alone
	// (overflow accepted since URL is wider than width).
	if got[0] != "https://example.com/p" {
		t.Errorf("leading-space-before-URL drop behavior changed:\n got = %v\n want first = %q",
			got, "https://example.com/p")
	}
}

// TestTruncateTitlePreservingURLs covers URL-aware title truncation. The
// critical invariant: a URL must never be cut mid-string, because Linkify
// runs over the output and would otherwise emit a broken URL as its OSC 8
// target (violating plan line 68's truncation-safety contract).
func TestTruncateTitlePreservingURLs(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{
			name:  "no url falls back to truncateTitle",
			in:    "the quick brown fox",
			width: 10,
			want:  "the quick\u2026", // width 10: 9 runes + ellipsis
		},
		{
			name:  "title shorter than width returns unchanged",
			in:    "Fix https://example.com/bug",
			width: 100,
			want:  "Fix https://example.com/bug",
		},
		{
			name:  "cut inside url keeps url atomic with ellipsized prefix",
			in:    "Fix long bug https://example.com/path",
			width: 25,
			// urlW = 24, prefix "Fix long bug " = 13 runes. prefixW+urlW = 37 > 25.
			// budget for prefix = 25 - 24 = 1, which is <= 1 → emit URL alone.
			want: "https://example.com/path",
		},
		{
			name:  "cut inside url with small prefix ellipsizes prefix",
			in:    "abcdefghijk https://example.com/xyz",
			width: 30,
			// urlW = 23, width 30, budget for prefix+suffix = 7. prefix = "abcdefghijk " = 12.
			// prefix doesn't fit; budget 7 > 1 → 6 prefix runes + "…" + URL.
			want: "abcdef\u2026https://example.com/xyz",
		},
		{
			name:  "url alone wider than width emits url atomically",
			in:    "Fix https://example.com/some/very/long/path/to/resource",
			width: 20,
			want:  "https://example.com/some/very/long/path/to/resource",
		},
		{
			name:  "cut outside url uses ordinary truncation",
			in:    "https://example.com/short then a lot more trailing text here",
			width: 30,
			// Cut point byte 29 is well past URL end, so normal truncateTitle.
			want: truncateTitle("https://example.com/short then a lot more trailing text here", 30),
		},
		{
			name:  "cut inside url with prefix exactly filling remaining width drops suffix",
			in:    "go https://example.com/pa and more",
			width: 25,
			// URL end rune index = 3 + 22 = 25. cutRune = 24 (inside URL).
			// prefixW=3 + urlW=22 = 25 == width → emit prefix+url, drop suffix.
			want: "go https://example.com/pa",
		},
		{
			name:  "width zero returns unchanged",
			in:    "anything",
			width: 0,
			want:  "anything",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateTitlePreservingURLs(tc.in, tc.width)
			if got != tc.want {
				t.Errorf("truncateTitlePreservingURLs(%q, %d)\n got  = %q\n want = %q",
					tc.in, tc.width, got, tc.want)
			}
		})
	}
}

// TestTruncateTitlePreservingURLs_LinkifiedOutputHasFullURLTarget is the
// end-to-end invariant check for plan line 68: even when the visible text
// is shorter than the full title, the OSC 8 target must be the full URL.
// Passes the output of truncateTitlePreservingURLs through display.Linkify
// and asserts the OSC 8 opener contains the unbroken URL, NOT the
// truncated fragment with an ellipsis.
func TestTruncateTitlePreservingURLs_LinkifiedOutputHasFullURLTarget(t *testing.T) {
	url := "https://example.com/some/reasonably/long/path"
	in := "Fix a big bug " + url + " right now"
	// Pick a width that forces the natural cut point to fall inside the URL.
	width := 30
	truncated := truncateTitlePreservingURLs(in, width)
	linkified := display.Linkify(truncated)
	// The OSC 8 opener must be "\x1b]8;;<FULL_URL>\x1b\\" — no truncation.
	wantOpener := "\x1b]8;;" + url + "\x1b\\"
	if !strings.Contains(linkified, wantOpener) {
		t.Errorf("OSC 8 target is not the full URL — plan line 68 contract violated.\n truncated = %q\n linkified = %q\n wantOpener = %q",
			truncated, linkified, wantOpener)
	}
	// And the truncated-with-ellipsis form must NOT appear as an OSC 8 target.
	// Anything like `\x1b]8;;https://...\u2026\x1b\\` would be broken.
	if strings.Contains(linkified, "\u2026\x1b\\") {
		// There is an ellipsis immediately inside an OSC 8 target block — bug.
		t.Errorf("OSC 8 target contains an ellipsis (broken URL):\n linkified = %q", linkified)
	}
}

func TestFlattenTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"one\ntwo", "one two"},
		{"one\r\ntwo", "one two"},
		{"a\n\nb", "a b"},
		{"  lead\ntrail  ", "lead trail"},
	}
	for _, tc := range cases {
		if got := flattenTitle(tc.in); got != tc.want {
			t.Errorf("flattenTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeTitle_DropsBlankLines(t *testing.T) {
	got := sanitizeTitle("  first  \n\n  second\n")
	want := "first\nsecond"
	if got != want {
		t.Errorf("sanitizeTitle = %q, want %q", got, want)
	}
}

func TestVlistItemHeight_MultilineTitle(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "line1\nline2", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)
	// 80 - 2 padding = 78 text width; title is short but has a newline → 2 title lines + 1 desc + 1 blank = 4.
	if h := m.lists[0].itemHeight(0); h != 4 {
		t.Errorf("itemHeight = %d, want 4 (newline in title)", h)
	}
}

// --- detail panel tests ---------------------------------------------------

func TestDetailPanel_EnterOpensPanel(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	if m.detailOpen {
		t.Fatal("detailOpen should be false initially")
	}

	m, _ = key(t, m, "enter")

	if !m.detailOpen {
		t.Error("detailOpen should be true after Enter")
	}
	if m.detailScroll != 0 {
		t.Errorf("detailScroll = %d, want 0", m.detailScroll)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal (%d)", m.mode, modeNormal)
	}
}

func TestDetailPanel_EnterDoesNothingWithNoTask(t *testing.T) {
	// Empty tab — no tasks to select.
	m := newTestModel(t)

	m, _ = key(t, m, "enter")

	if m.detailOpen {
		t.Error("detailOpen should remain false when no task is selected")
	}
}

func TestDetailPanel_EscClosesPanel(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)

	// Open panel.
	m, _ = key(t, m, "enter")
	if !m.detailOpen {
		t.Fatal("panel should be open after Enter")
	}

	// Close panel.
	m, _ = key(t, m, "esc")
	if m.detailOpen {
		t.Error("detailOpen should be false after Esc")
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal (%d)", m.mode, modeNormal)
	}
}

func TestDetailPanel_NavigationResetsScroll(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "task two", Status: "open", Schedule: "today",
			Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Give it a window so the list has dimensions.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	// Open panel.
	m, _ = key(t, m, "enter")
	if !m.detailOpen {
		t.Fatal("panel should be open")
	}

	// Simulate having scrolled down in the detail panel.
	m.detailScroll = 5

	// Navigate down — should reset scroll.
	m, _ = key(t, m, "down")
	if m.detailScroll != 0 {
		t.Errorf("detailScroll after down = %d, want 0", m.detailScroll)
	}

	// Simulate scroll again and navigate up.
	m.detailScroll = 3
	m, _ = key(t, m, "up")
	if m.detailScroll != 0 {
		t.Errorf("detailScroll after up = %d, want 0", m.detailScroll)
	}
}

func TestDetailPanel_StaysOpenDuringNavigation(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "task two", Status: "open", Schedule: "today",
			Position: 2000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	// Open panel.
	m, _ = key(t, m, "enter")

	// Navigate down — panel should stay open.
	m, _ = key(t, m, "down")
	if !m.detailOpen {
		t.Error("detailOpen should remain true after down navigation")
	}

	// Navigate up — panel should stay open.
	m, _ = key(t, m, "up")
	if !m.detailOpen {
		t.Error("detailOpen should remain true after up navigation")
	}
}

func TestDetailPanel_NoteAreaInitialized(t *testing.T) {
	m := newTestModel(t)

	// The noteArea should have the placeholder text.
	if m.noteArea.Placeholder != "add a note..." {
		t.Errorf("noteArea.Placeholder = %q, want %q", m.noteArea.Placeholder, "add a note...")
	}
}

func TestDetailPanel_TextInputRoutedToNoteArea(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	// Open panel — noteArea gets focus.
	m, _ = key(t, m, "enter")
	if !m.detailOpen {
		t.Fatal("panel should be open")
	}

	// Type text that doesn't start with an action key — when the textarea is
	// empty, single-char action keys (d/r/t/c/x/m/a/e/s/v/h/q) fall through
	// to their handlers. Once the first non-action character lands in the
	// textarea, subsequent characters (including action keys) are captured.
	m = typeString(t, m, "notes")
	if got := m.noteArea.Value(); got != "notes" {
		t.Errorf("noteArea.Value() = %q, want %q", got, "notes")
	}
}

func TestDetailPanel_EscClearsNoteArea(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	// Open, type (non-action-key starting text), close.
	m, _ = key(t, m, "enter")
	m = typeString(t, m, "note text")
	m, _ = key(t, m, "esc")

	if m.detailOpen {
		t.Error("panel should be closed")
	}
	if got := m.noteArea.Value(); got != "" {
		t.Errorf("noteArea should be empty after close, got %q", got)
	}
}

func TestDetailPanel_PrintableKeysAlwaysGoToTextarea(t *testing.T) {
	// When the detail panel is open, printable keys (including shortcut
	// letters like d/q/t) must go to the textarea so notes can begin with
	// any letter. Action shortcuts are intentionally unavailable while the
	// panel is focused — users press Esc first.
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	if !m.detailOpen {
		t.Fatal("panel should be open")
	}

	// Every action-shortcut letter must land in the textarea, not trigger
	// its shortcut. Type them one at a time starting from empty.
	for _, ch := range []string{"d", "q", "t", "c", "a", "e", "r", "h"} {
		m.noteArea.Reset()
		m, cmd := key(t, m, ch)
		if got := m.noteArea.Value(); got != ch {
			t.Errorf("key %q: noteArea.Value() = %q, want %q", ch, got, ch)
		}
		if m.mode != modeNormal {
			t.Errorf("key %q: mode changed to %d, expected modeNormal", ch, m.mode)
		}
		// Task should still be open (not marked done by 'd').
		if got := len(m.lists[0].Items()); got != 1 {
			t.Errorf("key %q: Today tab should still have 1 item, got %d", ch, got)
		}
		_ = cmd
	}
}

func TestDetailPanel_TabSwitchResetsScroll(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)

	// Open panel, set some scroll.
	m, _ = key(t, m, "enter")
	m.detailScroll = 5

	// Switch tab.
	m, _ = key(t, m, "right")
	if m.detailScroll != 0 {
		t.Errorf("detailScroll = %d after tab switch, want 0", m.detailScroll)
	}
}

// --- detail panel rendering tests ------------------------------------------

func TestDetailPanelView_EmptyWhenClosed(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	if got := m.detailPanelView(); got != "" {
		t.Errorf("detailPanelView() should be empty when panel is closed, got %q", got)
	}
}

func TestDetailPanelView_ShowsTaskMetadata(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01ABCDEF", Title: "my test task", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Tags:      []string{"work", "urgent"},
			CreatedAt: "2026-04-15T10:00:00Z",
			UpdatedAt: "2026-04-15T10:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open the panel.
	m, _ = key(t, m, "enter")
	if !m.detailOpen {
		t.Fatal("panel should be open")
	}

	panel := m.detailPanelView()
	if panel == "" {
		t.Fatal("detailPanelView() should not be empty when panel is open")
	}

	// Title should be visible.
	if !strings.Contains(panel, "my test task") {
		t.Errorf("panel should contain the task title; got %q", panel)
	}

	// Schedule should be visible.
	if !strings.Contains(panel, "Schedule:") {
		t.Errorf("panel should contain schedule label; got %q", panel)
	}

	// Tags should be visible.
	if !strings.Contains(panel, "Tags:") {
		t.Errorf("panel should contain tags label; got %q", panel)
	}
	if !strings.Contains(panel, "work") {
		t.Errorf("panel should contain tag 'work'; got %q", panel)
	}
	if !strings.Contains(panel, "urgent") {
		t.Errorf("panel should contain tag 'urgent'; got %q", panel)
	}

	// Created date should be visible.
	if !strings.Contains(panel, "Created:") {
		t.Errorf("panel should contain created label; got %q", panel)
	}
}

func TestDetailPanelView_ShowsCompletedDate(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01DONE", Title: "done task", Status: "done",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			CreatedAt:   "2026-04-10T10:00:00Z",
			UpdatedAt:   "2026-04-15T10:00:00Z",
			CompletedAt: "2026-04-15T10:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Navigate to the Done tab.
	doneIdx := findTabByLabel(t, m, "Done")
	for m.activeTab != doneIdx {
		m, _ = key(t, m, "right")
	}

	// Open the panel.
	m, _ = key(t, m, "enter")
	panel := m.detailPanelView()

	if !strings.Contains(panel, "Completed:") {
		t.Errorf("panel should show completed date for done tasks; got %q", panel)
	}
}

func TestDetailPanelView_ShowsBody(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01BODY", Title: "task with body", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Body:      "this is the body text\nwith multiple lines",
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	panel := m.detailPanelView()

	if !strings.Contains(panel, "this is the body text") {
		t.Errorf("panel should contain body text; got %q", panel)
	}
	if !strings.Contains(panel, "with multiple lines") {
		t.Errorf("panel should contain body continuation; got %q", panel)
	}
}

func TestDetailPanelView_EmptyBodyOK(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01NOBODY", Title: "no body task", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Body:      "",
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	panel := m.detailPanelView()
	if panel == "" {
		t.Error("panel should render even with empty body")
	}
	// Should still contain the separator line (─).
	if !strings.Contains(panel, "─") {
		t.Errorf("panel should contain separator line; got %q", panel)
	}
}

func TestDetailPanelView_HidesActiveTag(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01ACTIVE", Title: "active task", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Tags:      []string{"work", "active"},
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	panel := m.detailPanelView()

	// "work" should be visible but "active" tag should be filtered by VisibleTags.
	if !strings.Contains(panel, "work") {
		t.Errorf("panel should show visible tags; got %q", panel)
	}
	// The panel should not show "active" as a tag (it's filtered by VisibleTags).
	// Check that Tags: line doesn't contain the word "active".
	for _, line := range strings.Split(panel, "\n") {
		if strings.Contains(line, "Tags:") && strings.Contains(line, "active") {
			t.Errorf("panel Tags line should not contain the reserved 'active' tag; got %q", line)
		}
	}
}

func TestDetailPanelView_NoTagsLine(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01NOTAG", Title: "no tags", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Tags:      nil,
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	panel := m.detailPanelView()
	if strings.Contains(panel, "Tags:") {
		t.Errorf("panel should not show Tags line when there are no tags; got %q", panel)
	}
}

// TestDetailPanelView_LinkifiesBodyURL pins that URLs in the task body are
// wrapped in OSC 8 hyperlink escapes by the detail panel renderer.
func TestDetailPanelView_LinkifiesBodyURL(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01LINK1", Title: "url in body", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Body:      "see https://example.com for details",
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	panel := m.detailPanelView()

	if !strings.Contains(panel, "\x1b]8;;https://example.com") {
		t.Errorf("detail panel body should contain OSC 8 opener for URL; got %q", panel)
	}
	if !strings.Contains(panel, "\x1b]8;;\x1b\\") {
		t.Errorf("detail panel body should contain OSC 8 closer; got %q", panel)
	}
}

// TestDetailPanelView_LinkifiesTitleURL pins that URLs in the task title are
// wrapped in OSC 8 hyperlink escapes by the detail panel header renderer.
func TestDetailPanelView_LinkifiesTitleURL(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01LINK2", Title: "Fix https://example.com/bug", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	panel := m.detailPanelView()

	if !strings.Contains(panel, "\x1b]8;;https://example.com/bug") {
		t.Errorf("detail panel header should contain OSC 8 opener for title URL; got %q", panel)
	}
}

// TestDetailPanelView_LinkifiesTitleURL_TruncatedTitleKeepsFullURL pins
// plan line 68: even when the detail-panel title is truncated to fit the
// panel width, the OSC 8 target must be the FULL URL (not the truncated
// fragment ending in "…"). Symmetrical to the wrap-mid-URL fix from
// iteration 1, but for the truncation path.
func TestDetailPanelView_LinkifiesTitleURL_TruncatedTitleKeepsFullURL(t *testing.T) {
	// Long title that will force truncation when the detail panel opens in
	// a narrow window. URL is placed such that the natural cut lands inside
	// the URL — without the fix, Linkify would emit "https://…\u2026" as the
	// OSC 8 target and Cmd-click would navigate to a broken URL.
	url := "https://example.com/some/reasonably/long/path/to/page"
	title := "Fix the big bug " + url + " by tomorrow please"
	m := newTestModel(t,
		model.Task{ID: "01LINKTRUNC", Title: title, Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	// Narrow terminal → narrow detail panel inner width → forces truncation.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = next.(*Model)
	m, _ = key(t, m, "enter")
	panel := m.detailPanelView()

	wantOpener := "\x1b]8;;" + url + "\x1b\\"
	if !strings.Contains(panel, wantOpener) {
		t.Errorf("detail panel truncated title must wrap the FULL URL in OSC 8 (plan line 68);\n panel = %q\n wantOpener = %q",
			panel, wantOpener)
	}
	// And no OSC 8 target should end in an ellipsis (broken URL marker).
	if strings.Contains(panel, "\u2026\x1b\\") {
		t.Errorf("detail panel OSC 8 target contains an ellipsis (broken URL):\n panel = %q", panel)
	}
}

// TestDetailPanelView_MONOLOG_NO_LINKS_DisablesLinkify confirms the env
// escape hatch suppresses OSC 8 wrapping in both the title and body.
func TestDetailPanelView_MONOLOG_NO_LINKS_DisablesLinkify(t *testing.T) {
	t.Setenv("MONOLOG_NO_LINKS", "1")

	m := newTestModel(t,
		model.Task{ID: "01LINK3", Title: "Fix https://example.com/bug", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Body:      "see https://example.com for details",
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	panel := m.detailPanelView()

	if strings.Contains(panel, "\x1b]8;;") {
		t.Errorf("detail panel should not contain OSC 8 when MONOLOG_NO_LINKS=1; got %q", panel)
	}
	// The URL text itself should still be rendered as plain text.
	if !strings.Contains(panel, "https://example.com") {
		t.Errorf("detail panel should still display URL text as plain; got %q", panel)
	}
}

func TestDetailPanel_ListNarrowsWhenOpen(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	widthBefore := m.lists[0].Width()

	// Open the detail panel.
	m, _ = key(t, m, "enter")

	widthAfter := m.lists[0].Width()
	if widthAfter >= widthBefore {
		t.Errorf("list width should shrink when detail panel opens: before=%d after=%d", widthBefore, widthAfter)
	}

	// The detail panel width should be ~45% of terminal width.
	dpw := m.detailPanelWidth()
	if dpw == 0 {
		t.Fatal("detailPanelWidth() should be > 0 when panel is open")
	}
	if widthAfter+dpw != 100 {
		t.Errorf("list width + detail panel width should equal terminal width: %d + %d = %d, want 100",
			widthAfter, dpw, widthAfter+dpw)
	}
}

func TestDetailPanel_ListRestoresWidthWhenClosed(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	widthBefore := m.lists[0].Width()

	// Open and close the panel.
	m, _ = key(t, m, "enter")
	m, _ = key(t, m, "esc")

	widthAfter := m.lists[0].Width()
	if widthAfter != widthBefore {
		t.Errorf("list width should restore after panel close: before=%d after=%d", widthBefore, widthAfter)
	}
}

func TestDetailPanel_ResizeRecalculatesWidths(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task one", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open panel.
	m, _ = key(t, m, "enter")
	widthAt100 := m.lists[0].Width()

	// Resize terminal wider.
	next, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = next.(*Model)
	widthAt120 := m.lists[0].Width()

	if widthAt120 <= widthAt100 {
		t.Errorf("list width should grow when terminal widens: at100=%d at120=%d", widthAt100, widthAt120)
	}
	// Sum should equal new terminal width.
	dpw := m.detailPanelWidth()
	if widthAt120+dpw != 120 {
		t.Errorf("list + panel should equal terminal width: %d + %d = %d, want 120",
			widthAt120, dpw, widthAt120+dpw)
	}
}

func TestDetailPanelView_AppearsInView(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01ABCDEF", Title: "my viewable task", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Body:      "some body content",
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// View without panel open should not contain detail-panel-specific content.
	viewClosed := m.View()

	// Open panel.
	m, _ = key(t, m, "enter")
	viewOpen := m.View()

	// The open view should contain the task title in the panel.
	if !strings.Contains(viewOpen, "my viewable task") {
		t.Errorf("View() with panel open should contain task title")
	}
	if !strings.Contains(viewOpen, "some body content") {
		t.Errorf("View() with panel open should contain body content")
	}
	// The separator (─) should appear in the panel.
	if !strings.Contains(viewOpen, "─") {
		t.Errorf("View() with panel open should contain the separator line")
	}
	// Confirm that closed view does NOT contain body content in the list rendering.
	if strings.Contains(viewClosed, "some body content") {
		t.Errorf("View() without panel should not contain body content")
	}
}

func TestDetailPanelWidth_ZeroWhenClosed(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	if w := m.detailPanelWidth(); w != 0 {
		t.Errorf("detailPanelWidth() = %d when closed, want 0", w)
	}
}

func TestDetailPanelWidth_Proportional(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "task", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	pw := m.detailPanelWidth()

	// Should be ~45% of 100 = 45.
	if pw != 45 {
		t.Errorf("detailPanelWidth() = %d for width 100, want 45", pw)
	}
}

func TestDetailPanelView_BodyScrollOffset(t *testing.T) {
	// Create a task with a long body that requires scrolling.
	var bodyLines []string
	for i := 0; i < 50; i++ {
		bodyLines = append(bodyLines, fmt.Sprintf("line %d of the body", i))
	}
	longBody := strings.Join(bodyLines, "\n")

	m := newTestModel(t,
		model.Task{ID: "01SCROLL", Title: "scrollable", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Body:      longBody,
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")

	// With scroll at 0, "line 0" should be visible.
	panel0 := m.detailPanelView()
	if !strings.Contains(panel0, "line 0 of the body") {
		t.Error("panel at scroll=0 should show line 0")
	}

	// Set scroll offset to skip first 10 lines.
	m.detailScroll = 10
	panel10 := m.detailPanelView()

	// "line 0" should no longer be visible (scrolled past).
	if strings.Contains(panel10, "line 0 of the body") {
		t.Error("panel at scroll=10 should not show line 0")
	}
	// "line 10" should now be visible (first visible after scroll).
	if !strings.Contains(panel10, "line 10 of the body") {
		t.Error("panel at scroll=10 should show line 10")
	}
}

// --- Note Submission (Task 8) tests ----------------------------------------

func TestDetailPanel_NoteSubmission(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01NOTE", Title: "note target", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open detail panel.
	m, _ = key(t, m, "enter")
	if !m.detailOpen {
		t.Fatal("panel should be open")
	}

	// Type a note (starts with non-action-key character so it goes to textarea).
	m = typeString(t, m, "first note")
	if got := m.noteArea.Value(); got != "first note" {
		t.Fatalf("noteArea = %q, want %q", got, "first note")
	}

	// Submit with Enter — should return a tea.Cmd (the async save).
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("submit should return a non-nil cmd")
	}

	// Textarea should be cleared immediately after submission.
	if got := m.noteArea.Value(); got != "" {
		t.Errorf("noteArea should be empty after submit, got %q", got)
	}

	// Execute the async command to simulate the save completing.
	m = runCmd(t, m, cmd)

	// After save+reload, the task's body should contain the note text and
	// NoteCount should be 1.
	task := m.selectedTask()
	if task == nil {
		t.Fatal("no task selected after reload")
	}
	if !strings.Contains(task.Body, "first note") {
		t.Errorf("task body should contain the note, got %q", task.Body)
	}
	if task.NoteCount != 1 {
		t.Errorf("NoteCount = %d, want 1", task.NoteCount)
	}
	if m.statusMsg != "Note added: note target" {
		t.Errorf("statusMsg = %q, want %q", m.statusMsg, "Note added: note target")
	}
}

func TestDetailPanel_NoteSubmission_EmptyIgnored(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01EMPTY", Title: "empty note", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open panel.
	m, _ = key(t, m, "enter")

	// Press Enter with empty textarea — should be a no-op (nil cmd).
	_, cmd := key(t, m, "enter")
	if cmd != nil {
		t.Error("Enter with empty textarea should return nil cmd")
	}

	// Task should be unchanged.
	task := m.selectedTask()
	if task == nil {
		t.Fatal("no task selected")
	}
	if task.NoteCount != 0 {
		t.Errorf("NoteCount = %d, want 0 (empty input should be ignored)", task.NoteCount)
	}
}

func TestDetailPanel_NoteSubmission_WhitespaceOnlyIgnored(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01WS", Title: "whitespace note", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open panel.
	m, _ = key(t, m, "enter")

	// Type only spaces.
	m = typeString(t, m, "   ")

	// Press Enter — should be no-op.
	_, cmd := key(t, m, "enter")
	if cmd != nil {
		t.Error("Enter with whitespace-only textarea should return nil cmd")
	}
}

func TestDetailPanel_NoteSubmission_IncrementCount(t *testing.T) {
	// Task already has a note (NoteCount=1, body with separator).
	m := newTestModel(t,
		model.Task{ID: "01INC", Title: "has notes", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Body:      "--- 15-04-2026 10:00:00 ---\nexisting note",
			NoteCount: 1,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open panel, type (non-action-key starting text), submit.
	m, _ = key(t, m, "enter")
	m = typeString(t, m, "new note")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	m = runCmd(t, m, cmd)

	task := m.selectedTask()
	if task == nil {
		t.Fatal("no task selected after reload")
	}
	if task.NoteCount != 2 {
		t.Errorf("NoteCount = %d, want 2", task.NoteCount)
	}
	if !strings.Contains(task.Body, "existing note") {
		t.Error("original note should still be present")
	}
	if !strings.Contains(task.Body, "new note") {
		t.Error("new note should be present")
	}
}

func TestDetailPanel_AltEnterInsertsNewline(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01ALT", Title: "alt enter test", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open panel.
	m, _ = key(t, m, "enter")

	// Type some text.
	m = typeString(t, m, "line one")

	// Send Alt+Enter — should insert a newline, not submit.
	altEnter := tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	next2, cmd := m.Update(altEnter)
	m = next2.(*Model)
	// If there was a cmd from the textarea update, run it (textarea internal).
	if cmd != nil {
		// Textarea may return internal cmds; just feed them back.
		next3, _ := m.Update(cmd())
		m = next3.(*Model)
	}

	// Type more text after the newline.
	m = typeString(t, m, "line two")

	// The value should contain both lines separated by a newline.
	val := m.noteArea.Value()
	if !strings.Contains(val, "line one") || !strings.Contains(val, "line two") {
		t.Errorf("expected multi-line content, got %q", val)
	}
	if !strings.Contains(val, "\n") {
		t.Errorf("expected newline character in multi-line content, got %q", val)
	}
}

func TestDetailPanel_NoteSubmission_NoTaskSelected(t *testing.T) {
	// Empty model — no tasks, so no task selected.
	m := newTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Manually force panel open and type text (normally Enter wouldn't open
	// without a task, but we test the submitNote guard directly).
	m.detailOpen = true
	m.noteArea.Focus()
	m.noteArea.SetValue("orphan note")

	// submitNote should return nil because selectedTask() returns nil.
	cmd := m.submitNote()
	if cmd != nil {
		t.Error("submitNote with no task selected should return nil")
	}
}

func TestDetailPanel_NoteSubmission_UpdatesUpdatedAt(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01UPD", Title: "update at test", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2020-01-01T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open panel and type a note (non-action-key starting text).
	m, _ = key(t, m, "enter")
	m = typeString(t, m, "note for update")
	m, cmd := key(t, m, "enter")
	if cmd == nil {
		t.Fatal("submit should return a save cmd")
	}
	m = runCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("save error: %v", m.err)
	}

	task, err := m.store.GetByPrefix("01UPD")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.UpdatedAt == "2020-01-01T00:00:00Z" {
		t.Error("UpdatedAt should change after note submission via TUI")
	}
}

func TestDetailPanel_ViewWithNoTaskSelected(t *testing.T) {
	// Empty model — no tasks.
	m := newTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Force panel open — detailPanelView should return "" with no task.
	m.detailOpen = true
	got := m.detailPanelView()
	if got != "" {
		t.Errorf("detailPanelView with no task should return empty, got %q", got)
	}
}

func TestDetailPanel_ScrollBodyDown(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01SCR", Title: "scroll test", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Body:      "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10",
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open panel.
	m, _ = key(t, m, "enter")
	if !m.detailOpen {
		t.Fatal("panel should be open")
	}
	if m.detailScroll != 0 {
		t.Fatalf("initial scroll should be 0, got %d", m.detailScroll)
	}

	// Scroll down with ']'.
	m, _ = key(t, m, "]")
	if m.detailScroll != 1 {
		t.Errorf("detailScroll after ] should be 1, got %d", m.detailScroll)
	}

	// Scroll up with '['.
	m, _ = key(t, m, "[")
	if m.detailScroll != 0 {
		t.Errorf("detailScroll after [ should be 0, got %d", m.detailScroll)
	}

	// Scroll up at 0 should stay at 0.
	m, _ = key(t, m, "[")
	if m.detailScroll != 0 {
		t.Errorf("detailScroll should not go negative, got %d", m.detailScroll)
	}
}

func TestDetailPanel_ScrollOutOfBoundsStillRenders(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01OOB", Title: "oob scroll", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			Body:      "short body",
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	// Open panel and set scroll way beyond body length.
	m, _ = key(t, m, "enter")
	m.detailScroll = 999

	// detailPanelView should still render without panic.
	got := m.detailPanelView()
	if got == "" {
		t.Error("detailPanelView should still render even with scroll out of bounds")
	}
}

func TestDetailPanel_HelpLineShowsScrollKeys(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01HLP", Title: "help test", Status: "open",
			Schedule: expectSchedule(t, "today"), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*Model)

	m, _ = key(t, m, "enter")
	help := m.helpLine()
	if !strings.Contains(help, "scroll") {
		t.Errorf("help line when detail open should mention scroll, got: %s", help)
	}
}

// --- search overlay entry/exit plumbing (Task 3) ---------------------------

func TestSearch_SlashEntersSearchMode(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "fix login bug", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "write docs", Status: "done",
			Schedule: expectSchedule(t, schedule.Today), Position: 2000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "/")
	if m.mode != modeSearch {
		t.Fatalf("mode after '/' = %v, want modeSearch", m.mode)
	}
	if len(m.search.haystack) != 2 {
		t.Errorf("haystack size = %d, want 2 (open + done tasks)", len(m.search.haystack))
	}
	// Initial rank with empty query should return all docs (sorted by CreatedAt desc).
	if len(m.search.results) != 2 {
		t.Errorf("results size = %d, want 2 for empty query", len(m.search.results))
	}
	if m.search.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.search.cursor)
	}
}

func TestSearch_EscClosesSearchMode(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "fix login bug", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "/")
	if m.mode != modeSearch {
		t.Fatalf("precondition: mode = %v, want modeSearch", m.mode)
	}
	m, _ = key(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode after esc = %v, want modeNormal", m.mode)
	}
	if len(m.search.haystack) != 0 {
		t.Errorf("haystack should be cleared on close, got len=%d", len(m.search.haystack))
	}
	if len(m.search.results) != 0 {
		t.Errorf("results should be cleared on close, got len=%d", len(m.search.results))
	}
}

func TestSearch_SlashInGrabModeDoesNotChangeMode(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "first", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "second", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 2000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m.lists[0].Select(0)
	m, _ = key(t, m, "m")
	if m.mode != modeGrab {
		t.Fatalf("precondition: mode = %v, want modeGrab", m.mode)
	}
	m, _ = key(t, m, "/")
	if m.mode != modeGrab {
		t.Errorf("mode after '/' in grab = %v, want modeGrab (grab intact)", m.mode)
	}
}

// --- search overlay input/navigation behaviour (Task 4) --------------------

func TestSearch_TypingRerunsQueryAndChangesResults(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "fix login bug", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "write docs", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 2000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01C", Title: "refactor login", Status: "done",
			Schedule: expectSchedule(t, schedule.Today), Position: 500,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "/")
	if m.mode != modeSearch {
		t.Fatalf("precondition: mode = %v, want modeSearch", m.mode)
	}
	// Empty-query rank returns all 3 docs.
	if got := len(m.search.results); got != 3 {
		t.Fatalf("initial results = %d, want 3", got)
	}

	m = typeString(t, m, "login")
	if got := m.search.input.Value(); got != "login" {
		t.Errorf("input value = %q, want %q", got, "login")
	}
	// Only the two "login" tasks should match.
	if got := len(m.search.results); got != 2 {
		t.Errorf("results after typing %q = %d, want 2", "login", got)
	}
	// Confirm the matched IDs are the login-bearing tasks, not "write docs".
	for _, r := range m.search.results {
		id := m.search.haystack[r.docIdx].task.ID
		if id == "01B" {
			t.Errorf("result includes non-matching task %q", id)
		}
	}
}

func TestSearch_TypingRefinesAndClampsCursor(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "alpha", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "beta", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 2000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01C", Title: "gamma", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 3000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "/")
	// Move cursor to the last result, then type a query that produces fewer
	// results — the cursor must clamp down.
	m.search.cursor = 2
	m = typeString(t, m, "alp")
	if got := len(m.search.results); got != 1 {
		t.Fatalf("results after %q = %d, want 1", "alp", got)
	}
	if m.search.cursor != 0 {
		t.Errorf("cursor after narrowing = %d, want 0 (clamped)", m.search.cursor)
	}
}

// TestSearch_ClampSearchCursorNoOpWithNonZeroCursor covers the path where the
// cursor already points inside the (possibly-grown) result set: clampSearchCursor
// must leave a valid non-zero cursor untouched. A previous implementation could
// regress by always snapping back to zero when results change.
func TestSearch_ClampSearchCursorNoOpWithNonZeroCursor(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "alpha", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "beta", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 2000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01C", Title: "gamma", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 3000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "/")
	if got := len(m.search.results); got != 3 {
		t.Fatalf("precondition: results = %d, want 3", got)
	}
	// Park the cursor on the middle result (still in-range for any result set
	// that has >= 2 entries).
	m.search.cursor = 1
	// Directly exercise clampSearchCursor; it must leave the cursor at 1.
	m.clampSearchCursor()
	if m.search.cursor != 1 {
		t.Errorf("clampSearchCursor moved in-range cursor: got %d, want 1", m.search.cursor)
	}
	// Now grow/refresh the result set by typing a broad query that still
	// matches all docs. Cursor should still be preserved, not snapped to 0.
	m = typeString(t, m, "a")
	if len(m.search.results) < 2 {
		t.Fatalf("results after broad query = %d, want >= 2", len(m.search.results))
	}
	if m.search.cursor != 1 {
		t.Errorf("cursor after broadening query = %d, want 1 (no-op clamp)", m.search.cursor)
	}
}

func TestSearch_EscKeepsActiveTabAndListCursor(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "first", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "second", Status: "open",
			Schedule: expectSchedule(t, schedule.Tomorrow), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	// Move to the Tomorrow tab with the cursor anchored there.
	tomorrowIdx := findTabByLabel(t, m, "Tomorrow")
	m.activeTab = tomorrowIdx
	m.lists[tomorrowIdx].Select(0)

	m, _ = key(t, m, "/")
	if m.mode != modeSearch {
		t.Fatalf("precondition: mode = %v, want modeSearch", m.mode)
	}
	// Type something and move the search cursor around, then cancel.
	m = typeString(t, m, "first")
	m, _ = key(t, m, "esc")

	if m.mode != modeNormal {
		t.Errorf("mode after esc = %v, want modeNormal", m.mode)
	}
	if m.activeTab != tomorrowIdx {
		t.Errorf("activeTab after esc = %d, want %d (untouched)", m.activeTab, tomorrowIdx)
	}
	if got := m.lists[tomorrowIdx].Index(); got != 0 {
		t.Errorf("list cursor after esc = %d, want 0", got)
	}
}

func TestSearch_CursorDownUpClampsAtBoundaries(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "alpha", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:01Z"},
		model.Task{ID: "01B", Title: "beta", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 2000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:02Z"},
		model.Task{ID: "01C", Title: "gamma", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 3000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:03Z"},
	)
	m, _ = key(t, m, "/")
	if m.search.cursor != 0 {
		t.Fatalf("precondition: cursor = %d, want 0", m.search.cursor)
	}
	// Up at position 0 stays at 0 (no underflow).
	m, _ = key(t, m, "up")
	if m.search.cursor != 0 {
		t.Errorf("cursor after up at 0 = %d, want 0", m.search.cursor)
	}
	m, _ = key(t, m, "down")
	if m.search.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", m.search.cursor)
	}
	m, _ = key(t, m, "down")
	m, _ = key(t, m, "down") // at end already, should clamp
	if m.search.cursor != 2 {
		t.Errorf("cursor after down past end = %d, want 2 (clamped)", m.search.cursor)
	}
	m, _ = key(t, m, "up")
	if m.search.cursor != 1 {
		t.Errorf("cursor after up from end = %d, want 1", m.search.cursor)
	}
}

func TestSearch_CtrlNavAliasesMoveCursor(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "alpha", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:01Z"},
		model.Task{ID: "01B", Title: "beta", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 2000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:02Z"},
	)
	m, _ = key(t, m, "/")
	// ctrl+n should move cursor down.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	m = next.(*Model)
	if m.search.cursor != 1 {
		t.Errorf("cursor after ctrl+n = %d, want 1", m.search.cursor)
	}
	// ctrl+p should move cursor up.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = next.(*Model)
	if m.search.cursor != 0 {
		t.Errorf("cursor after ctrl+p = %d, want 0", m.search.cursor)
	}
}

func TestSearch_PgDnPgUpMovesByPage(t *testing.T) {
	var tasks []model.Task
	for i := 0; i < searchPageSize+5; i++ {
		tasks = append(tasks, model.Task{
			ID:        fmt.Sprintf("01%02d", i),
			Title:     fmt.Sprintf("task %02d", i),
			Status:    "open",
			Schedule:  expectSchedule(t, schedule.Today),
			Position:  float64((i + 1) * 1000),
			UpdatedAt: "2026-04-13T00:00:00Z",
			CreatedAt: fmt.Sprintf("2026-04-13T00:00:%02dZ", i),
		})
	}
	m := newTestModel(t, tasks...)
	m, _ = key(t, m, "/")
	// pgdown jumps by searchPageSize.
	m, _ = key(t, m, "pgdown")
	if m.search.cursor != searchPageSize {
		t.Errorf("cursor after pgdown = %d, want %d", m.search.cursor, searchPageSize)
	}
	// pgdown again clamps at end.
	m, _ = key(t, m, "pgdown")
	wantEnd := len(m.search.results) - 1
	if m.search.cursor != wantEnd {
		t.Errorf("cursor after second pgdown = %d, want %d (clamped)", m.search.cursor, wantEnd)
	}
	// pgup brings it back down by a page.
	m, _ = key(t, m, "pgup")
	want := wantEnd - searchPageSize
	if want < 0 {
		want = 0
	}
	if m.search.cursor != want {
		t.Errorf("cursor after pgup = %d, want %d", m.search.cursor, want)
	}
}

func TestSearch_CtrlCClosesLikeEsc(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "first", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m, _ = key(t, m, "/")
	if m.mode != modeSearch {
		t.Fatalf("precondition: mode = %v, want modeSearch", m.mode)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(*Model)
	if m.mode != modeNormal {
		t.Errorf("mode after ctrl+c = %v, want modeNormal", m.mode)
	}
	if len(m.search.haystack) != 0 {
		t.Errorf("haystack not cleared after ctrl+c, len = %d", len(m.search.haystack))
	}
}

func TestSearch_RecomputeLayoutSetsInputWidthInSearchMode(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "first", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(*Model)
	m, _ = key(t, m, "/")
	// recomputeLayout is called inside openSearch; input width should be set.
	wantW := 120 - searchInputReserve
	if got := m.search.input.Width; got != wantW {
		t.Errorf("search input width = %d, want %d", got, wantW)
	}

	// A resize while in search mode should update the width too.
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = next.(*Model)
	wantW = 80 - searchInputReserve
	if got := m.search.input.Width; got != wantW {
		t.Errorf("search input width after resize = %d, want %d", got, wantW)
	}
}

// --- search overlay commit / cross-tab focus (Task 5) ----------------------

func TestSearch_CommitScheduleViewFocusesTargetTab(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "today task", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "tomorrow special", Status: "open",
			Schedule: expectSchedule(t, schedule.Tomorrow), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:01Z"},
	)
	// Start from the Today tab.
	m.activeTab = findTabByLabel(t, m, "Today")

	m, _ = key(t, m, "/")
	m = typeString(t, m, "tomorrow special")
	if got := len(m.search.results); got == 0 {
		t.Fatalf("results empty after typing query")
	}
	// The first result should be the Tomorrow task (title match).
	firstDoc := m.search.haystack[m.search.results[0].docIdx].task
	if firstDoc.ID != "01B" {
		t.Fatalf("first result = %q, want 01B (tomorrow special)", firstDoc.ID)
	}
	m, _ = key(t, m, "enter")

	if m.mode != modeNormal {
		t.Errorf("mode after enter = %v, want modeNormal", m.mode)
	}
	tomorrowIdx := findTabByLabel(t, m, "Tomorrow")
	if m.activeTab != tomorrowIdx {
		t.Errorf("activeTab after commit = %d, want %d (Tomorrow)", m.activeTab, tomorrowIdx)
	}
	// The cursor should rest on the target task in the Tomorrow tab.
	items := m.lists[tomorrowIdx].Items()
	sel := m.lists[tomorrowIdx].Index()
	if sel < 0 || sel >= len(items) {
		t.Fatalf("list cursor %d out of range (items=%d)", sel, len(items))
	}
	selItem, ok := items[sel].(item)
	if !ok || selItem.task.ID != "01B" {
		t.Errorf("selected task = %+v, want ID 01B", selItem.task)
	}
}

func TestSearch_CommitDoneTaskSwitchesToDoneTab(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "open thing", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "finished artifact", Status: "done",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:01Z"},
	)
	m.activeTab = findTabByLabel(t, m, "Today")

	m, _ = key(t, m, "/")
	m = typeString(t, m, "finished")
	if got := len(m.search.results); got == 0 {
		t.Fatalf("no results for 'finished'")
	}
	m, _ = key(t, m, "enter")

	if m.mode != modeNormal {
		t.Errorf("mode after enter = %v, want modeNormal", m.mode)
	}
	doneIdx := findTabByLabel(t, m, "Done")
	if m.activeTab != doneIdx {
		t.Errorf("activeTab after committing done task = %d, want %d (Done)", m.activeTab, doneIdx)
	}
	items := m.lists[doneIdx].Items()
	sel := m.lists[doneIdx].Index()
	if sel < 0 || sel >= len(items) {
		t.Fatalf("list cursor %d out of range (items=%d)", sel, len(items))
	}
	selItem, ok := items[sel].(item)
	if !ok || selItem.task.ID != "01B" {
		t.Errorf("selected task = %+v, want ID 01B", selItem.task)
	}
}

func TestSearch_CommitTagViewFocusesCorrectTagTab(t *testing.T) {
	task1 := makeTask(t, "01WRK", "work refactor", schedule.Today, []string{"work"})
	task1.CreatedAt = "2026-04-13T00:00:00Z"
	task2 := makeTask(t, "01HOM", "home errand", schedule.Today, []string{"home"})
	task2.CreatedAt = "2026-04-13T00:00:01Z"
	m := newTestModel(t, task1, task2)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	// Start from the Active tab (index 0 by construction of tagTabs).
	m.activeTab = 0

	m, _ = key(t, m, "/")
	m = typeString(t, m, "home errand")
	if got := len(m.search.results); got == 0 {
		t.Fatalf("no results for 'home errand'")
	}
	m, _ = key(t, m, "enter")

	if m.mode != modeNormal {
		t.Errorf("mode after enter = %v, want modeNormal", m.mode)
	}
	homeIdx := -1
	for i, tt := range m.tagTabs {
		if tt.tag == "home" {
			homeIdx = i
			break
		}
	}
	if homeIdx < 0 {
		t.Fatalf("no home tag tab found")
	}
	if m.activeTab != homeIdx {
		t.Errorf("activeTab after commit = %d, want %d (home)", m.activeTab, homeIdx)
	}
	items := m.lists[homeIdx].Items()
	sel := m.lists[homeIdx].Index()
	if sel < 0 || sel >= len(items) {
		t.Fatalf("list cursor %d out of range (items=%d)", sel, len(items))
	}
	selItem, ok := items[sel].(item)
	if !ok || selItem.task.ID != "01HOM" {
		t.Errorf("selected task = %+v, want ID 01HOM", selItem.task)
	}
}

func TestSearch_CommitTagViewActiveTakesPriority(t *testing.T) {
	task := makeTask(t, "01ACT", "active workflow", schedule.Today,
		[]string{"work", model.ActiveTag})
	task.CreatedAt = "2026-04-13T00:00:00Z"
	m := newTestModel(t, task)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}
	// Start from a non-Active tab to force the jump.
	workIdx := -1
	for i, tt := range m.tagTabs {
		if tt.tag == "work" {
			workIdx = i
		}
	}
	if workIdx < 0 {
		t.Fatalf("no work tag tab found")
	}
	m.activeTab = workIdx

	m, _ = key(t, m, "/")
	m = typeString(t, m, "active workflow")
	m, _ = key(t, m, "enter")

	activeIdx := -1
	for i, tt := range m.tagTabs {
		if tt.isActive {
			activeIdx = i
		}
	}
	if activeIdx < 0 {
		t.Fatalf("no Active tag tab found")
	}
	if m.activeTab != activeIdx {
		t.Errorf("activeTab after commit = %d, want %d (Active preferred)", m.activeTab, activeIdx)
	}
}

func TestSearch_CommitTagViewUntaggedRoute(t *testing.T) {
	// One tagged task so tag tabs exist alongside the Untagged tab.
	task1 := makeTask(t, "01WRK", "work alpha", schedule.Today, []string{"work"})
	task1.CreatedAt = "2026-04-13T00:00:00Z"
	task2 := makeTask(t, "01UNT", "loose untagged task", schedule.Today, nil)
	task2.CreatedAt = "2026-04-13T00:00:01Z"
	m := newTestModel(t, task1, task2)
	if err := m.rebuildForTagView(); err != nil {
		t.Fatalf("rebuildForTagView: %v", err)
	}

	m.activeTab = 0 // Active tab
	m, _ = key(t, m, "/")
	m = typeString(t, m, "loose untagged")
	m, _ = key(t, m, "enter")

	untaggedIdx := -1
	for i, tt := range m.tagTabs {
		if tt.isUntagged {
			untaggedIdx = i
		}
	}
	if untaggedIdx < 0 {
		t.Fatalf("no Untagged tag tab found")
	}
	if m.activeTab != untaggedIdx {
		t.Errorf("activeTab after commit = %d, want %d (Untagged)", m.activeTab, untaggedIdx)
	}
}

func TestSearch_EnterWithEmptyResultsIsNoOp(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "hello", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	originalTab := m.activeTab
	m, _ = key(t, m, "/")
	m = typeString(t, m, "zzzzznomatch")
	if got := len(m.search.results); got != 0 {
		t.Fatalf("precondition: results should be empty, got %d", got)
	}
	m, _ = key(t, m, "enter")
	if m.mode != modeSearch {
		t.Errorf("mode after enter with empty results = %v, want modeSearch (no-op)", m.mode)
	}
	if m.activeTab != originalTab {
		t.Errorf("activeTab changed to %d on empty-result Enter, want %d", m.activeTab, originalTab)
	}
}

// --- renderSearch layout (Task 6) ------------------------------------------

func TestSearch_RenderSearchWideSplitPane(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "fix login bug", Body: "the preview body text", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "write docs", Status: "done",
			Schedule: expectSchedule(t, schedule.Today), Position: 2000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m.width = 120
	m.height = 40
	m.recomputeLayout()
	m, _ = key(t, m, "/")

	out := m.renderSearch()
	if out == "" {
		t.Fatal("renderSearch() returned empty string")
	}
	// Render output must fit within the terminal bounds — every line no wider
	// than m.width, and no more lines than m.height. Regression guard for the
	// preview border width/height not being accounted for in the split math.
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("renderSearch wide: line %d width = %d, want <= %d; line=%q", i, w, m.width, line)
		}
	}
	if lines := strings.Count(out, "\n") + 1; lines > m.height {
		t.Errorf("renderSearch wide: rendered %d lines, want <= %d", lines, m.height)
	}
	// Strip ANSI so we can substring-match the plain content.
	plain := ansi.Strip(out)

	if !strings.Contains(plain, ">") {
		t.Errorf("renderSearch output missing input prompt '>': %q", plain)
	}
	// The counter is rendered as "N/M" where N=visible and M=total.
	if !strings.Contains(plain, "2/2") {
		t.Errorf("renderSearch output missing counter '2/2': %q", plain)
	}
	// Results pane: at least the first task's title must appear (it is the
	// default-selected result).
	if !strings.Contains(plain, "fix login bug") {
		t.Errorf("renderSearch output missing first result title: %q", plain)
	}
	// Preview pane: the body of the selected task must be rendered.
	if !strings.Contains(plain, "the preview body text") {
		t.Errorf("renderSearch output missing preview body: %q", plain)
	}
	// Meta line: schedule bucket + status for the selected task, separated by
	// middot. "today" is the bucket for Schedule.Today.
	if !strings.Contains(plain, "today") || !strings.Contains(plain, "open") {
		t.Errorf("renderSearch output missing meta line (bucket/status): %q", plain)
	}
	if !strings.Contains(plain, "·") {
		t.Errorf("renderSearch output missing middot separator in meta line: %q", plain)
	}
	// Help hint: the in-search key set.
	for _, want := range []string{"enter", "esc", "filter"} {
		if !strings.Contains(plain, want) {
			t.Errorf("renderSearch output missing help hint %q: %q", want, plain)
		}
	}
}

func TestSearch_RenderSearchNarrowStacked(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "fix login bug", Body: "body", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m.width = 60
	m.height = 30
	m.recomputeLayout()
	m, _ = key(t, m, "/")

	out := m.renderSearch()
	if out == "" {
		t.Fatal("renderSearch() returned empty string in narrow layout")
	}
	// Stacked (narrow) path must also respect terminal bounds.
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("renderSearch narrow: line %d width = %d, want <= %d; line=%q", i, w, m.width, line)
		}
	}
	if lines := strings.Count(out, "\n") + 1; lines > m.height {
		t.Errorf("renderSearch narrow: rendered %d lines, want <= %d", lines, m.height)
	}
	if !strings.Contains(out, ">") {
		t.Errorf("renderSearch narrow output missing input prompt '>': %q", out)
	}
}

func TestSearch_RenderSearchEmptyResults(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "hello", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
	)
	m.width = 120
	m.height = 40
	m.recomputeLayout()
	m, _ = key(t, m, "/")
	m = typeString(t, m, "zzzzznomatch")
	out := m.renderSearch()
	if out == "" {
		t.Fatal("renderSearch() returned empty with empty results")
	}
	if !strings.Contains(out, "no matches") {
		t.Errorf("renderSearch empty-results output missing '(no matches)': %q", out)
	}
}

func TestSearch_HighlightMatchesBoldsMatches(t *testing.T) {
	// Reset style profile so ANSI output is deterministic regardless of TERM.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	plain := highlightMatches("hello", nil)
	if plain != "hello" {
		t.Errorf("highlightMatches with nil hits = %q, want %q", plain, "hello")
	}

	// For ASCII strings, byte offsets and rune indexes coincide, so [0, 2] hit
	// the 'h' and 'l' of "hello".
	styled := highlightMatches("hello", []int{0, 2})
	if styled == "hello" {
		t.Errorf("highlightMatches with hits should differ from plain; got %q", styled)
	}
	// Stripping the ANSI escapes should yield the original string.
	if got := ansi.Strip(styled); got != "hello" {
		t.Errorf("ansi.Strip(highlighted) = %q, want %q", got, "hello")
	}
}

func TestSearch_HighlightMatchesSkipsOutOfRange(t *testing.T) {
	// Out-of-range indices must not panic or corrupt output.
	got := highlightMatches("hi", []int{10, -1})
	if ansi.Strip(got) != "hi" {
		t.Errorf("highlightMatches with out-of-range hits = %q, want plain 'hi'", ansi.Strip(got))
	}
}

// TestSearch_HighlightMatchesMultibyte exercises the byte-offset contract of
// highlightMatches on a string with multi-byte runes. The earlier rune-index
// implementation mis-aligned or dropped highlights here because sahilm/fuzzy
// returns byte offsets. "café" has bytes: c=0, a=1, f=2, é=3..4.
func TestSearch_HighlightMatchesMultibyte(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	styled := highlightMatches("café", []int{3})
	if plain := ansi.Strip(styled); plain != "café" {
		t.Errorf("highlightMatches round-trip on multibyte = %q, want %q", plain, "café")
	}
	if styled == "café" {
		t.Errorf("highlightMatches should apply styling for valid multibyte hit, got unchanged %q", styled)
	}
	// A byte offset that lands inside the é rune (offset 4) must not corrupt
	// output — highlightMatches keys on rune-start byte offsets only.
	styledBad := highlightMatches("café", []int{4})
	if plain := ansi.Strip(styledBad); plain != "café" {
		t.Errorf("highlightMatches with non-rune-start offset corrupted output: %q", plain)
	}
}

func TestSearch_ResultsWindowKeepsCursorVisible(t *testing.T) {
	tests := []struct {
		name               string
		cursor, total, h   int
		wantStart, wantEnd int
	}{
		{"all fit", 0, 5, 10, 0, 5},
		{"cursor at top", 0, 20, 10, 0, 10},
		{"cursor centered", 10, 20, 10, 5, 15},
		{"cursor at bottom", 19, 20, 10, 10, 20},
		{"zero total", 0, 0, 10, 0, 0},
		{"zero height", 0, 5, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotS, gotE := resultsWindow(tt.cursor, tt.total, tt.h)
			if gotS != tt.wantStart || gotE != tt.wantEnd {
				t.Errorf("resultsWindow(%d,%d,%d) = (%d,%d), want (%d,%d)",
					tt.cursor, tt.total, tt.h, gotS, gotE, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

// TestSearchSplitWidths pins the boundary behavior of searchSplitWidths across
// the narrow-threshold edge (79/80/81) and verifies the wide-path invariant
// that the two pane widths plus the 2-col preview border sum to the total.
func TestSearchSplitWidths(t *testing.T) {
	tests := []struct {
		name        string
		total       int
		wantResults int
		wantPreview int
	}{
		// Below the narrow threshold: helper returns (total, total) per its
		// doc; the renderer is expected to branch to the stacked path instead.
		{"width 60 narrow path", 60, 60, 60},
		{"width 79 just below threshold", 79, 79, 79},
		// At/above the threshold: results = total*2/5 (clamped min 20),
		// preview = total - results - 2 (border, clamped min 20).
		{"width 80 boundary", 80, 32, 46},   // 80*2/5=32; 80-32-2=46
		{"width 81 just above", 81, 32, 47}, // 81*2/5=32; 81-32-2=47
		{"width 120 wide", 120, 48, 70},     // 120*2/5=48; 120-48-2=70
		{"width 200 very wide", 200, 80, 118},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotR, gotP := searchSplitWidths(tt.total)
			if gotR != tt.wantResults || gotP != tt.wantPreview {
				t.Errorf("searchSplitWidths(%d) = (%d,%d), want (%d,%d)",
					tt.total, gotR, gotP, tt.wantResults, tt.wantPreview)
			}
			// Wide-path invariant: results + preview + 2 (border) must equal
			// total so the horizontally-joined panes render at exactly total
			// columns. Skip for narrow (helper returns (total,total) stub).
			if tt.total >= 80 {
				if sum := gotR + gotP + 2; sum != tt.total {
					t.Errorf("wide invariant: results(%d)+preview(%d)+2 = %d, want %d",
						gotR, gotP, sum, tt.total)
				}
			}
		})
	}
}

// TestSearchBodyHeight pins the floor (3) and the wide-path invariant that
// total render height = bodyHeight + 5 (input+meta+help+border). Small heights
// (<8) hit the floor, which the compact renderer path handles separately.
func TestSearchBodyHeight(t *testing.T) {
	tests := []struct {
		name  string
		total int
		want  int
	}{
		{"tiny 1", 1, 3},     // floored
		{"tiny 3", 3, 3},     // floored
		{"tiny 7", 7, 3},     // floored (7-5=2, clamped to 3)
		{"boundary 8", 8, 3}, // 8-5=3
		{"boundary 9", 9, 4},
		{"typical 20", 20, 15},
		{"typical 40", 40, 35},
		{"tall 100", 100, 95},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchBodyHeight(tt.total)
			if got != tt.want {
				t.Errorf("searchBodyHeight(%d) = %d, want %d", tt.total, got, tt.want)
			}
			if got < 3 {
				t.Errorf("searchBodyHeight(%d) = %d, violated floor of 3", tt.total, got)
			}
		})
	}
}

// TestSearch_RenderSearchTinyTerminalFitsBounds guards against the overflow
// regression where the stacked layout rendered 10 rows on an 8-row terminal.
// At m.height < searchCompactMinHeight the renderer must drop preview/meta/help
// and emit at most m.height lines, each no wider than m.width.
func TestSearch_RenderSearchTinyTerminalFitsBounds(t *testing.T) {
	sizes := []struct {
		name string
		w, h int
	}{
		{"8x60 stacked-path-would-overflow", 60, 8},
		{"9x60 stacked-path-would-overflow", 60, 9},
		{"5x120 wide-path-would-overflow", 120, 5},
		{"3x80 extreme", 80, 3},
	}
	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t,
				model.Task{ID: "01A", Title: "alpha task", Body: "body text", Status: "open",
					Schedule: expectSchedule(t, schedule.Today), Position: 1000,
					UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
				model.Task{ID: "01B", Title: "beta task", Status: "open",
					Schedule: expectSchedule(t, schedule.Today), Position: 2000,
					UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:01Z"},
			)
			m.width = tc.w
			m.height = tc.h
			m.recomputeLayout()
			m, _ = key(t, m, "/")

			out := m.renderSearch()
			if out == "" {
				t.Fatal("renderSearch() returned empty string")
			}
			for i, line := range strings.Split(out, "\n") {
				if w := lipgloss.Width(line); w > m.width {
					t.Errorf("line %d width = %d, want <= %d; line=%q", i, w, m.width, line)
				}
			}
			if lines := strings.Count(out, "\n") + 1; lines > m.height {
				t.Errorf("rendered %d lines, want <= %d", lines, m.height)
			}
			// Input prompt must still be visible.
			if !strings.Contains(ansi.Strip(out), ">") {
				t.Errorf("compact render missing input prompt '>': %q", out)
			}
		})
	}
}

// TestSearch_CommitAfterAsyncAllTasksMutation simulates a taskSavedMsg arriving
// while the search overlay is open. The overlay's haystack snapshot must
// survive the reload, and committing the selection must still focus the task
// via the search-time snapshot (not the mutated allTasks). The focus target
// is the task as it exists *after* reload, via focusTaskByID.
func TestSearch_CommitAfterAsyncAllTasksMutation(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "alpha", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 1000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: "beta target", Status: "open",
			Schedule: expectSchedule(t, schedule.Today), Position: 2000,
			UpdatedAt: "2026-04-13T00:00:00Z", CreatedAt: "2026-04-13T00:00:01Z"},
	)
	m.activeTab = findTabByLabel(t, m, "Today")

	// Open search, narrow to the target, take snapshot.
	m, _ = key(t, m, "/")
	m = typeString(t, m, "beta")
	if len(m.search.results) == 0 {
		t.Fatalf("precondition: results empty after typing 'beta'")
	}
	snapshotSize := len(m.search.haystack)

	// Simulate an unrelated async mutation completing (e.g. a prior 'c' that
	// created a new task resolves while modeSearch is open). The taskSavedMsg
	// reloads allTasks and tab lists but must not touch the search haystack.
	id, err := model.NewID()
	if err != nil {
		t.Fatalf("model.NewID: %v", err)
	}
	added := model.Task{
		ID: id, Title: "gamma late addition", Status: "open",
		Schedule:  expectSchedule(t, schedule.Today),
		Position:  3000,
		CreatedAt: "2026-04-13T00:00:02Z",
		UpdatedAt: "2026-04-13T00:00:02Z",
	}
	if err := m.store.Create(added); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	next, _ := m.Update(taskSavedMsg{status: "Added: gamma", focusID: id})
	m = next.(*Model)

	// Search mode should still be open (taskSavedMsg does not close it).
	if m.mode != modeSearch {
		t.Errorf("mode after taskSavedMsg = %v, want modeSearch (still open)", m.mode)
	}
	// Haystack is a snapshot from openSearch and must not have grown.
	if got := len(m.search.haystack); got != snapshotSize {
		t.Errorf("haystack len after async save = %d, want %d (snapshot invariant)", got, snapshotSize)
	}
	// allTasks, on the other hand, reflects the newly-created task.
	if got := len(m.allTasks); got != 3 {
		t.Errorf("allTasks len after async save = %d, want 3", got)
	}

	// Commit the selection — Enter on the "beta" match must still focus the
	// target task via the stable haystack snapshot.
	m, _ = key(t, m, "enter")
	if m.mode != modeNormal {
		t.Errorf("mode after commit = %v, want modeNormal", m.mode)
	}
	todayIdx := findTabByLabel(t, m, "Today")
	if m.activeTab != todayIdx {
		t.Errorf("activeTab after commit = %d, want %d (Today)", m.activeTab, todayIdx)
	}
	items := m.lists[todayIdx].Items()
	sel := m.lists[todayIdx].Index()
	if sel < 0 || sel >= len(items) {
		t.Fatalf("list cursor %d out of range (items=%d)", sel, len(items))
	}
	selItem, ok := items[sel].(item)
	if !ok || selItem.task.ID != "01B" {
		t.Errorf("selected task after async-mutated commit = %+v, want ID 01B", selItem.task)
	}
}

// TestRenderListItem_LinkifiesURLInTitle confirms that a task whose title
// contains a URL renders the title wrapped in OSC 8 hyperlink escapes in the
// list row (after wrapText, before the bullet/indent prefix).
func TestRenderListItem_LinkifiesURLInTitle(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "visit https://example.com now", Status: "open",
			Schedule: "today", Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.lists[0].SetSize(80, 20)
	items := m.lists[0].Items()

	out := m.renderListItem(0, items[0], true)
	if !strings.Contains(out, "\x1b]8;;https://example.com") {
		t.Errorf("list row should contain OSC 8 opener for URL in title;\n rendered=%q", out)
	}
	if !strings.Contains(out, "\x1b]8;;\x1b\\") {
		t.Errorf("list row should contain OSC 8 closer;\n rendered=%q", out)
	}
}

// TestRenderListItem_WrappedTitleHasNoDanglingOpener confirms that when a
// long title is wrapped across lines by wrapText, the OSC 8 escape sequence
// is still balanced (no opener without a closer) because Linkify runs on
// each wrapped fragment.
func TestRenderListItem_WrappedTitleHasNoDanglingOpener(t *testing.T) {
	// Long title guaranteed to wrap at a narrow list width.
	longTitle := "this is a rather long title that will need to wrap across multiple lines visit https://example.com/some/path for details"
	m := newTestModel(t,
		model.Task{ID: "01A", Title: longTitle, Status: "open",
			Schedule: "today", Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Very narrow list width forces wrapText to split the title.
	m.lists[0].SetSize(30, 20)
	items := m.lists[0].Items()

	out := m.renderListItem(0, items[0], true)

	// Count openers and closers: must be balanced (one closer per opener).
	openers := strings.Count(out, "\x1b]8;;https://example.com")
	closers := strings.Count(out, "\x1b]8;;\x1b\\")
	if openers == 0 {
		t.Fatalf("expected at least one OSC 8 opener for URL in wrapped title;\n rendered=%q", out)
	}
	if closers < openers {
		t.Errorf("dangling OSC 8 opener detected: openers=%d closers=%d;\n rendered=%q",
			openers, closers, out)
	}
}

// TestRenderListItem_LongURLWithoutSpacesStaysLinkified confirms that a
// URL longer than the list column width — with no internal spaces —
// stays on a single line, is linkified in full, and that the OSC 8
// target is the ENTIRE URL (not a truncated prefix). Without the
// URL-aware wrap, wrapLine would hard-break the URL mid-string and the
// first fragment would be linkified with a broken target.
func TestRenderListItem_LongURLWithoutSpacesStaysLinkified(t *testing.T) {
	longURL := "https://example.com/some/very/long/path/to/a/resource/that/exceeds/the/column"
	m := newTestModel(t,
		model.Task{ID: "01A", Title: longURL, Status: "open",
			Schedule: "today", Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	// Narrow width: any reasonable hard-break would split the URL.
	m.lists[0].SetSize(30, 20)
	items := m.lists[0].Items()

	out := m.renderListItem(0, items[0], true)

	// The full URL must appear as an OSC 8 target exactly once.
	fullOpener := "\x1b]8;;" + longURL + "\x1b\\"
	if !strings.Contains(out, fullOpener) {
		t.Errorf("expected full URL as OSC 8 target;\n want opener = %q\n rendered = %q", fullOpener, out)
	}
	// And there must be exactly one opener — no secondary openers with
	// a truncated URL target.
	openers := strings.Count(out, "\x1b]8;;http")
	if openers != 1 {
		t.Errorf("expected exactly 1 OSC 8 opener for long URL, got %d;\n rendered = %q", openers, out)
	}
	// And one matching closer.
	closers := strings.Count(out, "\x1b]8;;\x1b\\")
	if closers != 1 {
		t.Errorf("expected exactly 1 OSC 8 closer for long URL, got %d;\n rendered = %q", closers, out)
	}
}

// TestRenderListItem_ActiveRowWithURLComposesStyleAndLink confirms that a
// row for an active task whose title contains a URL carries BOTH the SGR
// style from the active delegate AND the OSC 8 hyperlink escape — styling
// and linking compose cleanly.
func TestRenderListItem_ActiveRowWithURLComposesStyleAndLink(t *testing.T) {
	// Force color profile so ANSI codes survive in test output.
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := newTestModel(t,
		model.Task{ID: "01A", Title: "check https://example.com status", Status: "open",
			Schedule: "today", Position: 1000, Tags: []string{"active"},
			UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.lists[0].SetSize(80, 20)
	items := m.lists[0].Items()

	out := m.renderListItem(0, items[0], true)

	// OSC 8 opener must be present.
	if !strings.Contains(out, "\x1b]8;;https://example.com") {
		t.Errorf("active row should contain OSC 8 opener for URL;\n rendered=%q", out)
	}
	// SGR color from the active delegate (bright green for selected active row).
	brightGreenSeq := "38;2;73;222;128"
	if !strings.Contains(out, brightGreenSeq) {
		t.Errorf("active selected row should contain bright green SGR sequence %q;\n rendered=%q",
			brightGreenSeq, out)
	}
	// And a generic SGR escape prefix check — link + style must coexist.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("active row should contain at least one SGR escape;\n rendered=%q", out)
	}
}

// TestVlistItemHeight_MatchesRenderedLinesForLongURL is a regression test
// for the vlist / renderListItem height contract: vlist.itemHeight must
// mirror whatever wrap helper renderListItem uses so row-height prediction
// stays aligned with the rendered output. A title containing a URL wider
// than the column and with no internal spaces is the canonical case —
// wrapText would hard-break it into several rows while wrapTextPreservingURLs
// (which the renderer uses) keeps it on a single row.
func TestVlistItemHeight_MatchesRenderedLinesForLongURL(t *testing.T) {
	longURL := "https://example.com/some/very/long/path/to/a/resource/that/exceeds/the/column"
	m := newTestModel(t,
		model.Task{ID: "01A", Title: longURL, Status: "open",
			Schedule: "today", Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.lists[0].SetSize(30, 20)
	items := m.lists[0].Items()
	if len(items) == 0 {
		t.Fatalf("expected at least one list item; got 0")
	}

	predicted := m.lists[0].itemHeight(0)
	rendered := m.renderListItem(0, items[0], false)
	// renderListItem returns "<title>\n<desc>\n" — trailing newline produces
	// an empty final element in strings.Split, matching itemHeight's +2
	// (desc + blank separator).
	actual := len(strings.Split(rendered, "\n"))

	if predicted != actual {
		t.Errorf("vlist.itemHeight must match rendered line count;\n"+
			" predicted = %d\n actual    = %d\n rendered  = %q",
			predicted, actual, rendered)
	}

	// Sanity: with wrapText (the pre-fix helper), a 76-rune URL in a 28-rune
	// column would wrap to at least 3 rows. Assert we're actually exercising
	// the URL-aware path — predicted should be 3 (1 title row + 1 desc + 1
	// trailing blank), not more.
	if predicted != 3 {
		t.Errorf("URL-aware wrap should keep long URL on a single title row "+
			"(expected height = 1 title + 1 desc + 1 blank = 3); got %d", predicted)
	}
}
