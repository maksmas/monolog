package tui

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
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
	d := newItemDelegate(m)
	items := m.lists[0].Items()

	// Render the active item (index 0 = selected row) and the normal item
	// (index 1 = unselected row).
	var activeBuf, normalBuf bytes.Buffer
	d.Render(&activeBuf, m.lists[0], 0, items[0])
	d.Render(&normalBuf, m.lists[0], 1, items[1])

	activeOut := activeBuf.String()
	normalOut := normalBuf.String()

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

func TestActivePanel_TruncatesLongTitles(t *testing.T) {
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
	// The panel must contain the ellipsis character and must NOT contain the full title.
	if !strings.Contains(panel, "\u2026") {
		t.Error("panel should contain ellipsis character '\u2026' for truncated title")
	}
	if strings.Contains(panel, longTitle) {
		t.Error("panel should NOT contain the full 200-char title")
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

// --- itemHeight tests --------------------------------------------------------

func TestComputeItemHeight_DefaultWhenShortTitles(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "short", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)
	if m.itemHeight != 2 {
		t.Errorf("itemHeight = %d, want 2 for short titles", m.itemHeight)
	}
}

func TestComputeItemHeight_BumpsWhenLongTitle(t *testing.T) {
	longTitle := strings.Repeat("x", 100)
	m := newTestModel(t,
		model.Task{ID: "01A", Title: longTitle, Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = next.(*Model)
	// 60 - 2 (padding) = 58 text width; 100 chars > 58, so height should be 3.
	if m.itemHeight != 3 {
		t.Errorf("itemHeight = %d, want 3 for long title", m.itemHeight)
	}
}

func TestItemHeightChangesOnTabSwitch(t *testing.T) {
	longTitle := strings.Repeat("word ", 20) // 100 chars
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "short", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: "01B", Title: longTitle, Status: "open", Schedule: "tomorrow",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = next.(*Model)

	// Today tab: short title → height 2.
	if m.itemHeight != 2 {
		t.Errorf("Today tab: itemHeight = %d, want 2", m.itemHeight)
	}
	// Switch to Tomorrow tab: long title → height 3.
	m, _ = key(t, m, "right")
	if m.itemHeight != 3 {
		t.Errorf("Tomorrow tab: itemHeight = %d, want 3", m.itemHeight)
	}
	// Switch back to Today: height should return to 2.
	m, _ = key(t, m, "left")
	if m.itemHeight != 2 {
		t.Errorf("Today tab after return: itemHeight = %d, want 2", m.itemHeight)
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
	m, _ = key(t, m, "tab")   // switch to tags field
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
	m, _ = key(t, m, "tab") // focus title
	m = typeString(t, m, "jean: ")
	got := m.tagInput.Value()
	// Should still be just "jean", not "jean, jean" or "jean,jean".
	if got != "jean" {
		t.Errorf("tagInput = %q, want %q (no duplicate)", got, "jean")
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
	delegate := newItemDelegate(m)

	sep := newSeparatorItem("Week")
	lm := m.lists[0]
	lm.SetItems([]list.Item{sep})
	lm.SetSize(60, 10)

	var buf bytes.Buffer
	delegate.Render(&buf, lm, 0, sep)
	out := buf.String()
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
	sched, err := schedule.Parse(bucket, time.Now())
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
	schedListH := m.lists[0].Height()

	// Toggle to tag view.
	m, _ = key(t, m, "v")

	// Active panel hidden in tag view.
	if m.activePanelHeight() != 0 {
		t.Error("activePanelHeight should be 0 in tag view")
	}

	// List should be taller in tag view (recovered the panel space).
	tagListH := m.lists[0].Height()
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
		Schedule: expectSchedule(t, schedule.Today),
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
		Position: 1000,
		Schedule: expectSchedule(t, schedule.Today),
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
		Schedule: expectSchedule(t, schedule.Today),
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
		Schedule: expectSchedule(t, schedule.Today),
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
		Schedule: expectSchedule(t, schedule.Today),
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
		Schedule: expectSchedule(t, schedule.Today),
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
		Schedule: expectSchedule(t, schedule.Today),
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	task2 := model.Task{
		ID: "01SKP2", Title: "week task", Status: "open",
		Position: 1000, Tags: []string{"test"},
		Schedule: expectSchedule(t, schedule.Week),
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
	m.skipSeparator(0)
	if m.lists[m.activeTab].Index() != 1 {
		t.Errorf("cursor after skipSeparator on non-separator = %d, want 1", m.lists[m.activeTab].Index())
	}

	// Manually place cursor on separator at index 2 (simulating a down move from 1).
	m.lists[m.activeTab].Select(2)
	m.skipSeparator(1) // previous was 1, so direction is down
	if m.lists[m.activeTab].Index() != 3 {
		t.Errorf("cursor after skipping separator downward = %d, want 3", m.lists[m.activeTab].Index())
	}

	// Place cursor on separator at index 2 (simulating an up move from 3).
	m.lists[m.activeTab].Select(2)
	m.skipSeparator(3) // previous was 3, so direction is up
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
		Tags: []string{"work"}, Position: 1000,
		UpdatedAt: "2026-04-13T00:00:00Z",
	}
	task2 := model.Task{
		ID: "01TSS2", Title: "hobby task", Status: "open",
		Schedule: expectSchedule(t, schedule.Today),
		Tags: []string{"hobby"}, Position: 1000,
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
		Tags: []string{"work"}, Position: 1000,
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
	cmd := m.createCmd("new task from tag view", nil)
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

func TestComputeItemHeight_BumpsOnMultilineTitle(t *testing.T) {
	m := newTestModel(t,
		model.Task{ID: "01A", Title: "line1\nline2", Status: "open", Schedule: "today",
			Position: 1000, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(*Model)
	// 80 - 2 padding = 78 text width; title is short but has a newline -> height 3.
	if m.itemHeight != 3 {
		t.Errorf("itemHeight = %d, want 3 (newline in title)", m.itemHeight)
	}
}
