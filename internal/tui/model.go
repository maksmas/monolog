package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
)

// mode tracks whether the TUI is in normal navigation or inside a modal.
type mode int

const (
	modeNormal mode = iota
	modeGrab
	modeReschedule
	modeRetag
	modeAdd
	modeConfirmDelete
)

// reschedulePresets are the quick-pick bucket names shown in the reschedule
// modal. The index + 1 is the numeric shortcut key. They are resolved into
// concrete ISO dates via schedule.Parse before being written.
var reschedulePresets = []string{schedule.Today, schedule.Tomorrow, schedule.Week, schedule.Someday}

// tab describes one virtual schedule bucket shown in the TUI.
type tab struct {
	label  string
	bucket string // bucket name (today/tomorrow/week/someday); empty for Done
	status string // "open" for regular tabs, "done" for the Done tab
}

// defaultTabs are the tabs shown in display order. 1-5 shortcut keys index in.
var defaultTabs = []tab{
	{label: "Today", bucket: schedule.Today, status: "open"},
	{label: "Tomorrow", bucket: schedule.Tomorrow, status: "open"},
	{label: "Week", bucket: schedule.Week, status: "open"},
	{label: "Someday", bucket: schedule.Someday, status: "open"},
	{label: "Done", bucket: "", status: "done"},
}

// Model is the top-level Bubble Tea model for the TUI.
type Model struct {
	store    *store.Store
	repoPath string

	tabs      []tab
	activeTab int
	lists     []list.Model // one per tab

	width, height int

	// Modal state. All modals reuse a single text input; only one modal can
	// be open at a time.
	mode          mode
	input         textinput.Model
	modalTask     *model.Task // task the modal is acting on (nil for add)
	rescheduleSub int         // 0 = picker, 1 = custom date input

	// Grab-mode state. grabTask is a working copy whose Position is not
	// mutated until Enter drop; its current visual location is (activeTab,
	// grabIndex) within that tab's list items.
	grabTask  *model.Task
	grabIndex int

	statusMsg string // transient status line
	err       error  // sticky error; cleared on next successful action
}

// item wraps a model.Task for display in a bubbles/list.
// now is captured at reloadTab time. Dates won't refresh in a tab until the
// next mutation or tab switch triggers a reload — acceptable tradeoff for a
// personal CLI tool.
type item struct {
	task model.Task
	now  time.Time // render-time clock for compact date display
}

func (i item) Title() string { return i.task.Title }

func (i item) Description() string {
	parts := []string{display.ShortID(i.task.ID)}
	if i.task.Schedule != "" && schedule.Bucket(i.task.Schedule, i.now) != schedule.Today {
		parts = append(parts, i.task.Schedule)
	}
	if len(i.task.Tags) > 0 {
		parts = append(parts, "["+strings.Join(i.task.Tags, ", ")+"]")
	}
	if dates := display.FormatTaskDates(i.now, i.task); dates != "" {
		parts = append(parts, dates)
	}
	return strings.Join(parts, "  ")
}

func (i item) FilterValue() string { return i.task.Title }

// taskSavedMsg is dispatched back to Update after an async mutation completes.
// focusID, if non-empty, asks the handler to move the cursor to the task with
// that ID after reload — used by add so a new task is autofocused.
type taskSavedMsg struct {
	status  string
	err     error
	focusID string
}

// newModel constructs a Model and loads initial task data for each tab.
func newModel(s *store.Store, repoPath string) (*Model, error) {
	ti := textinput.New()
	ti.CharLimit = 512

	m := &Model{
		store:    s,
		repoPath: repoPath,
		tabs:     defaultTabs,
		lists:    make([]list.Model, len(defaultTabs)),
		input:    ti,
	}

	for i, t := range m.tabs {
		l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
		l.Title = t.label
		l.SetShowStatusBar(false)
		l.SetShowHelp(false)
		l.SetFilteringEnabled(false)
		l.DisableQuitKeybindings()
		m.lists[i] = l
	}

	if err := m.reloadAll(); err != nil {
		return nil, err
	}
	return m, nil
}

// reloadAll refreshes every tab from the store.
func (m *Model) reloadAll() error {
	for i := range m.tabs {
		if err := m.reloadTab(i); err != nil {
			return err
		}
	}
	return nil
}

// reloadTab refreshes a single tab's list from the store.
func (m *Model) reloadTab(idx int) error {
	t := m.tabs[idx]
	tasks, err := m.store.List(store.ListOptions{Status: t.status})
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	now := time.Now()
	// Filter to the tab's bucket. The Done tab (empty bucket) keeps every
	// done task, regardless of schedule.
	if t.bucket != "" {
		filtered := tasks[:0]
		for _, task := range tasks {
			if schedule.MatchesBucket(task.Schedule, t.bucket, now) {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
	}
	// Done tab: completion-time order (later UpdatedAt first), not position.
	if t.status == "done" {
		sort.SliceStable(tasks, func(i, j int) bool {
			return tasks[i].UpdatedAt > tasks[j].UpdatedAt
		})
	}
	items := make([]list.Item, len(tasks))
	for i, task := range tasks {
		items[i] = item{task: task, now: now}
	}
	m.lists[idx].SetItems(items)
	return nil
}

// selectedTask returns a pointer to the currently selected task in the
// active tab, or nil if the tab is empty.
func (m *Model) selectedTask() *model.Task {
	sel := m.lists[m.activeTab].SelectedItem()
	if sel == nil {
		return nil
	}
	it := sel.(item)
	return &it.task
}

// Init is the Bubble Tea Init hook.
func (m *Model) Init() tea.Cmd { return nil }

// Update routes a tea.Msg through the model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		listH := msg.Height - 4
		if listH < 3 {
			listH = 3
		}
		for i := range m.lists {
			m.lists[i].SetSize(msg.Width, listH)
		}
		return m, nil

	case taskSavedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.statusMsg = ""
			return m, nil
		}
		m.err = nil
		m.statusMsg = msg.status
		if err := m.reloadAll(); err != nil {
			m.err = err
		}
		if msg.focusID != "" {
			m.focusTaskByID(msg.focusID)
		}
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeNormal:
			return m.updateNormal(msg)
		case modeGrab:
			return m.updateGrab(msg)
		default:
			return m.updateModal(msg)
		}
	}
	return m, nil
}

// updateNormal handles keys when no modal is open.
func (m *Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.statusMsg = ""

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "left", "shift+tab":
		m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
		return m, nil
	case "right", "tab":
		m.activeTab = (m.activeTab + 1) % len(m.tabs)
		return m, nil
	case "1", "2", "3", "4", "5":
		n := int(msg.String()[0] - '1')
		if n < len(m.tabs) {
			m.activeTab = n
		}
		return m, nil

	case "d":
		return m, m.doneSelected()
	case "r":
		return m, m.openReschedule()
	case "t":
		return m, m.openRetag()
	case "a":
		return m, m.openAdd()
	case "x":
		return m, m.openConfirmDelete()
	case "m":
		m.openGrab()
		return m, nil
	case "e":
		return m, m.openEdit()
	case "s":
		m.statusMsg = "Syncing..."
		return m, m.syncCmd()
	}

	var cmd tea.Cmd
	m.lists[m.activeTab], cmd = m.lists[m.activeTab].Update(msg)
	return m, cmd
}

// updateModal routes keys based on the current modal.
func (m *Model) updateModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Esc always cancels a modal.
	if msg.Type == tea.KeyEsc {
		m.closeModal()
		return m, nil
	}

	switch m.mode {
	case modeReschedule:
		return m.updateReschedule(msg)
	case modeRetag:
		return m.updateRetag(msg)
	case modeAdd:
		return m.updateAdd(msg)
	case modeConfirmDelete:
		return m.updateConfirmDelete(msg)
	}
	return m, nil
}

// closeModal resets modal state back to normal.
func (m *Model) closeModal() {
	m.mode = modeNormal
	m.modalTask = nil
	m.rescheduleSub = 0
	m.input.Blur()
	m.input.SetValue("")
}

// --- done action -----------------------------------------------------------

func (m *Model) doneSelected() tea.Cmd {
	task := m.selectedTask()
	if task == nil {
		return nil
	}
	if task.Status == "done" {
		m.statusMsg = "already done"
		return nil
	}
	t := *task
	t.Status = "done"
	t.UpdatedAt = now()
	return m.saveCmd(t, fmt.Sprintf("done: %s", t.Title), fmt.Sprintf("Completed: %s", t.Title))
}

// --- reschedule modal ------------------------------------------------------

func (m *Model) openReschedule() tea.Cmd {
	task := m.selectedTask()
	if task == nil {
		m.statusMsg = "no task selected"
		return nil
	}
	t := *task
	m.modalTask = &t
	m.mode = modeReschedule
	m.rescheduleSub = 0
	return nil
}

func (m *Model) updateReschedule(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.rescheduleSub == 0 {
		// Picker step.
		switch msg.String() {
		case "1", "2", "3", "4":
			n := int(msg.String()[0] - '1')
			return m, m.applyReschedule(reschedulePresets[n])
		case "5":
			m.rescheduleSub = 1
			m.input.Placeholder = "YYYY-MM-DD"
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		}
		return m, nil
	}

	// Custom date input step.
	switch msg.Type {
	case tea.KeyEnter:
		date := strings.TrimSpace(m.input.Value())
		if !schedule.IsISODate(date) {
			m.err = fmt.Errorf("invalid date %q (want YYYY-MM-DD)", date)
			return m, nil
		}
		return m, m.applyReschedule(date)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) applyReschedule(sched string) tea.Cmd {
	if m.modalTask == nil {
		return nil
	}
	nowT := time.Now()
	scheduleDate, err := schedule.Parse(sched, nowT)
	if err != nil {
		m.err = err
		return nil
	}
	t := *m.modalTask
	oldBucket := schedule.Bucket(m.modalTask.Schedule, nowT)
	t.Schedule = scheduleDate
	t.UpdatedAt = now()
	newBucket := schedule.Bucket(scheduleDate, nowT)
	// Position it at the bottom of the new bucket if the bucket changed.
	if newBucket != oldBucket {
		others, err := bucketSiblings(m.store, t.Schedule, nowT)
		if err == nil {
			t.Position = ordering.NextPosition(others)
		}
	}
	m.closeModal()
	return m.saveCmd(t, fmt.Sprintf("reschedule: %s -> %s", t.Title, sched),
		fmt.Sprintf("Rescheduled: %s -> %s", t.Title, sched))
}

// bucketSiblings returns all open tasks that fall into the same bucket as the
// given schedule date.
func bucketSiblings(s *store.Store, sched string, now time.Time) ([]model.Task, error) {
	all, err := s.List(store.ListOptions{Status: "open"})
	if err != nil {
		return nil, err
	}
	bucket := schedule.Bucket(sched, now)
	out := all[:0]
	for _, t := range all {
		if schedule.MatchesBucket(t.Schedule, bucket, now) {
			out = append(out, t)
		}
	}
	return out, nil
}

// --- retag modal -----------------------------------------------------------

func (m *Model) openRetag() tea.Cmd {
	task := m.selectedTask()
	if task == nil {
		m.statusMsg = "no task selected"
		return nil
	}
	t := *task
	m.modalTask = &t
	m.mode = modeRetag
	m.input.Placeholder = "tag1, tag2"
	m.input.SetValue(strings.Join(t.Tags, ", "))
	m.input.CursorEnd()
	m.input.Focus()
	return textinput.Blink
}

func (m *Model) updateRetag(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEnter {
		if m.modalTask == nil {
			m.closeModal()
			return m, nil
		}
		t := *m.modalTask
		t.Tags = sanitizeTags(m.input.Value())
		t.UpdatedAt = now()
		m.closeModal()
		return m, m.saveCmd(t,
			fmt.Sprintf("edit: %s", t.Title),
			fmt.Sprintf("Retagged: %s", t.Title))
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// --- add modal -------------------------------------------------------------

func (m *Model) openAdd() tea.Cmd {
	m.mode = modeAdd
	m.input.Placeholder = "task title"
	m.input.SetValue("")
	m.input.Focus()
	return textinput.Blink
}

func (m *Model) updateAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEnter {
		title := strings.TrimSpace(m.input.Value())
		if title == "" {
			m.closeModal()
			return m, nil
		}
		m.closeModal()
		return m, m.createCmd(title)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// --- delete confirm --------------------------------------------------------

func (m *Model) openConfirmDelete() tea.Cmd {
	task := m.selectedTask()
	if task == nil {
		m.statusMsg = "no task selected"
		return nil
	}
	t := *task
	m.modalTask = &t
	m.mode = modeConfirmDelete
	return nil
}

func (m *Model) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "y" || msg.String() == "Y" {
		if m.modalTask == nil {
			m.closeModal()
			return m, nil
		}
		t := *m.modalTask
		m.closeModal()
		return m, m.deleteCmd(t)
	}
	// Anything else cancels.
	m.closeModal()
	return m, nil
}

// --- edit (shell out to $EDITOR) -------------------------------------------

// editableFields is the subset of a Task that's exposed to the user in the
// YAML edit buffer. ID, Status, Position, and timestamps are not shown.
type editableFields struct {
	Title    string   `yaml:"title"`
	Body     string   `yaml:"body,omitempty"`
	Schedule string   `yaml:"schedule"`
	Tags     []string `yaml:"tags,omitempty"`
}

// marshalTaskForEdit renders a task into the YAML shown in the editor.
func marshalTaskForEdit(t model.Task) ([]byte, error) {
	return yaml.Marshal(editableFields{
		Title:    t.Title,
		Body:     t.Body,
		Schedule: t.Schedule,
		Tags:     t.Tags,
	})
}

// applyEditedYAML parses the user's edited YAML and returns an updated task.
// Returns an error if the YAML is invalid, the title is empty, or the
// schedule is not parseable. Bucket-name schedules are converted to ISO
// dates so the on-disk file always stores a concrete date.
func applyEditedYAML(orig model.Task, data []byte, now time.Time) (model.Task, error) {
	var edit editableFields
	if err := yaml.Unmarshal(data, &edit); err != nil {
		return model.Task{}, fmt.Errorf("parse YAML: %w", err)
	}
	edit.Title = strings.TrimSpace(edit.Title)
	edit.Schedule = strings.TrimSpace(edit.Schedule)
	if edit.Title == "" {
		return model.Task{}, fmt.Errorf("title cannot be empty")
	}
	scheduleDate, err := schedule.Parse(edit.Schedule, now)
	if err != nil {
		return model.Task{}, err
	}
	out := orig
	out.Title = edit.Title
	out.Body = edit.Body
	out.Schedule = scheduleDate
	out.Tags = edit.Tags
	out.UpdatedAt = now.UTC().Format(time.RFC3339)
	return out, nil
}

// resolveEditor returns the editor command split into binary + args:
// $VISUAL, $EDITOR, or "vi". Splitting on whitespace lets users set
// flags like EDITOR="idea --wait" or EDITOR="code -w", which is required
// for editors whose launchers are otherwise non-blocking — without a
// wait flag, the temp file is deleted before the editor can read it.
func resolveEditor() []string {
	v := os.Getenv("VISUAL")
	if v == "" {
		v = os.Getenv("EDITOR")
	}
	if v == "" {
		v = "vi"
	}
	return strings.Fields(v)
}

// openEdit writes the selected task as YAML to a temp file and launches
// $EDITOR to edit it. On editor exit the file is re-parsed and the task
// updated + committed.
func (m *Model) openEdit() tea.Cmd {
	task := m.selectedTask()
	if task == nil {
		m.statusMsg = "no task selected"
		return nil
	}
	data, err := marshalTaskForEdit(*task)
	if err != nil {
		m.err = fmt.Errorf("marshal: %w", err)
		return nil
	}
	tmp, err := os.CreateTemp("", fmt.Sprintf("monolog-edit-%s-*.yaml", display.ShortID(task.ID)))
	if err != nil {
		m.err = fmt.Errorf("tempfile: %w", err)
		return nil
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		m.err = fmt.Errorf("write tempfile: %w", err)
		return nil
	}
	_ = tmp.Close()
	path := tmp.Name()

	origTask := *task
	repoPath := m.repoPath
	storeRef := m.store

	editor := resolveEditor()
	c := exec.Command(editor[0], append(editor[1:], path)...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return taskSavedMsg{err: fmt.Errorf("editor: %w", err)}
		}
		edited, err := os.ReadFile(path)
		if err != nil {
			return taskSavedMsg{err: fmt.Errorf("read edited: %w", err)}
		}
		updated, err := applyEditedYAML(origTask, edited, time.Now())
		if err != nil {
			return taskSavedMsg{err: err}
		}
		if err := storeRef.Update(updated); err != nil {
			return taskSavedMsg{err: fmt.Errorf("update: %w", err)}
		}
		if err := git.AutoCommit(repoPath, fmt.Sprintf("edit: %s", updated.Title), taskRelPath(updated.ID)); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		return taskSavedMsg{status: fmt.Sprintf("Edited: %s", updated.Title)}
	})
}

// --- grab mode -------------------------------------------------------------

// openGrab begins a grab on the selected task. No-op if nothing is selected.
func (m *Model) openGrab() {
	task := m.selectedTask()
	if task == nil {
		m.statusMsg = "no task selected"
		return
	}
	t := *task
	m.grabTask = &t
	m.grabIndex = m.lists[m.activeTab].Index()
	m.mode = modeGrab
}

// updateGrab handles key events while a task is grabbed.
func (m *Model) updateGrab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.grabTask == nil {
		m.mode = modeNormal
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		// Cancel: discard in-memory moves, reload from disk.
		m.mode = modeNormal
		m.grabTask = nil
		if err := m.reloadAll(); err != nil {
			m.err = err
		}
		return m, nil
	case tea.KeyEnter:
		return m, m.commitGrab()
	}

	switch msg.String() {
	case "up":
		if m.tabs[m.activeTab].status == "done" {
			return m, nil // reorder disabled in Done tab
		}
		m.moveGrabWithin(-1)
	case "down":
		if m.tabs[m.activeTab].status == "done" {
			return m, nil
		}
		m.moveGrabWithin(+1)
	case "left":
		m.moveGrabAcross(-1)
	case "right":
		m.moveGrabAcross(+1)
	case "g":
		if m.tabs[m.activeTab].status == "done" {
			return m, nil
		}
		m.moveGrabTo(0)
	case "G":
		if m.tabs[m.activeTab].status == "done" {
			return m, nil
		}
		items := m.lists[m.activeTab].Items()
		if len(items) > 0 {
			m.moveGrabTo(len(items) - 1)
		}
	}
	return m, nil
}

// moveGrabWithin swaps the grabbed task with its neighbor in the current tab.
// delta is -1 (up) or +1 (down). No-op at the boundary.
func (m *Model) moveGrabWithin(delta int) {
	items := m.lists[m.activeTab].Items()
	target := m.grabIndex + delta
	if target < 0 || target >= len(items) {
		return
	}
	items[m.grabIndex], items[target] = items[target], items[m.grabIndex]
	m.lists[m.activeTab].SetItems(items)
	m.grabIndex = target
	m.lists[m.activeTab].Select(m.grabIndex)
}

// moveGrabTo moves the grabbed task to a specific index in the current tab.
func (m *Model) moveGrabTo(idx int) {
	items := m.lists[m.activeTab].Items()
	if idx < 0 || idx >= len(items) || idx == m.grabIndex {
		return
	}
	grabbed := items[m.grabIndex]
	// Remove, then insert at target.
	items = append(items[:m.grabIndex], items[m.grabIndex+1:]...)
	// Re-insert — note len has decreased by 1.
	if idx > len(items) {
		idx = len(items)
	}
	items = append(items[:idx], append([]list.Item{grabbed}, items[idx:]...)...)
	m.lists[m.activeTab].SetItems(items)
	m.grabIndex = idx
	m.lists[m.activeTab].Select(idx)
}

// moveGrabAcross moves the grabbed task to the adjacent tab (delta ±1). The
// task is inserted at the bottom of the new tab's in-memory list. Wraps
// around the tab bar.
func (m *Model) moveGrabAcross(delta int) {
	if m.grabTask == nil {
		return
	}
	// Remove from current tab.
	items := m.lists[m.activeTab].Items()
	if m.grabIndex < 0 || m.grabIndex >= len(items) {
		return
	}
	grabbed := items[m.grabIndex]
	items = append(items[:m.grabIndex], items[m.grabIndex+1:]...)
	m.lists[m.activeTab].SetItems(items)

	// Advance active tab.
	m.activeTab = (m.activeTab + delta + len(m.tabs)) % len(m.tabs)

	// Insert at bottom of new tab.
	newItems := append(m.lists[m.activeTab].Items(), grabbed)
	m.lists[m.activeTab].SetItems(newItems)
	m.grabIndex = len(newItems) - 1
	m.lists[m.activeTab].Select(m.grabIndex)
}

// commitGrab persists the grabbed task's final schedule/status/position and
// exits grab mode. Runs ordering.Rebalance if the bucket's gaps got too tight.
func (m *Model) commitGrab() tea.Cmd {
	if m.grabTask == nil {
		return nil
	}
	targetTab := m.tabs[m.activeTab]
	t := *m.grabTask
	nowT := time.Now()

	// Apply the target tab's semantics. For bucket tabs, write the bucket's
	// canonical date (today/+1/+7/+365). The Done tab leaves Schedule alone.
	if targetTab.bucket != "" {
		// Only rewrite Schedule when the task isn't already in the target
		// bucket, otherwise dropping back into your current bucket would
		// reset a custom-set date (e.g. a 3-day-out task in the Week tab).
		if schedule.Bucket(t.Schedule, nowT) != targetTab.bucket {
			scheduleDate, err := schedule.Parse(targetTab.bucket, nowT)
			if err == nil {
				t.Schedule = scheduleDate
			}
		} else {
			// Lazy-migrate any legacy bucket string to ISO on this write.
			t.Schedule = schedule.Normalize(t.Schedule, nowT)
		}
	}
	t.Status = targetTab.status
	if t.Status == "" {
		t.Status = "open"
	}
	t.UpdatedAt = now()

	// Compute the new Position from the grabbed task's in-memory neighbors
	// within the destination tab. The Done tab has no meaningful ordering
	// (sorted by UpdatedAt), but we still keep a sane Position value.
	if targetTab.status != "done" {
		t.Position = m.computeGrabPosition(t.ID)
	}

	// Snapshot the destination bucket for a possible rebalance check. We do
	// this before the update call so we have the sibling positions on disk.
	repoPath := m.repoPath
	storeRef := m.store

	// Reset grab UI state now; a successful save will reload everything.
	m.mode = modeNormal
	m.grabTask = nil

	return func() tea.Msg {
		if err := storeRef.Update(t); err != nil {
			return taskSavedMsg{err: fmt.Errorf("update: %w", err)}
		}
		// Rebalance the destination bucket if adjacent gaps got too tight.
		if t.Status == "open" {
			siblings, err := bucketSiblings(storeRef, t.Schedule, nowT)
			if err == nil && ordering.NeedsRebalance(siblings) {
				rebalanced := ordering.Rebalance(siblings)
				for _, rt := range rebalanced {
					// Apply only if position changed (to avoid noisy writes).
					// Easiest: just update all.
					if err := storeRef.Update(rt); err != nil {
						return taskSavedMsg{err: fmt.Errorf("rebalance: %w", err)}
					}
				}
			}
		}
		if err := git.AutoCommit(repoPath, fmt.Sprintf("move: %s", t.Title), taskRelPath(t.ID)); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		return taskSavedMsg{status: fmt.Sprintf("Moved: %s", t.Title)}
	}
}

// computeGrabPosition derives a Position value for the grabbed task based on
// its neighbors in the current (destination) tab's in-memory list. The
// grabbed task is identified by id so we skip its own placeholder position.
func (m *Model) computeGrabPosition(grabbedID string) float64 {
	items := m.lists[m.activeTab].Items()

	// Collect neighbor positions, ignoring the grabbed item.
	var prev, next *model.Task
	var grabIdx int = -1
	for i, it := range items {
		task := it.(item).task
		if task.ID == grabbedID {
			grabIdx = i
			continue
		}
	}
	if grabIdx == -1 {
		// Shouldn't happen — grabbed task must be in the items.
		return ordering.DefaultSpacing
	}
	for i := grabIdx - 1; i >= 0; i-- {
		task := items[i].(item).task
		if task.ID != grabbedID {
			t := task
			prev = &t
			break
		}
	}
	for i := grabIdx + 1; i < len(items); i++ {
		task := items[i].(item).task
		if task.ID != grabbedID {
			t := task
			next = &t
			break
		}
	}

	switch {
	case prev == nil && next == nil:
		return ordering.DefaultSpacing
	case prev == nil:
		return next.Position / 2.0
	case next == nil:
		return prev.Position + ordering.DefaultSpacing
	default:
		return ordering.PositionBetween(prev.Position, next.Position)
	}
}

// --- command dispatchers ---------------------------------------------------

// saveCmd dispatches a store.Update + git.AutoCommit as a tea.Cmd.
func (m *Model) saveCmd(task model.Task, commitMsg, statusMsg string) tea.Cmd {
	return func() tea.Msg {
		if err := m.store.Update(task); err != nil {
			return taskSavedMsg{err: fmt.Errorf("update: %w", err)}
		}
		if err := git.AutoCommit(m.repoPath, commitMsg, taskRelPath(task.ID)); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		return taskSavedMsg{status: statusMsg}
	}
}

// createCmd creates a new task in the current tab's bucket at the bottom.
func (m *Model) createCmd(title string) tea.Cmd {
	// Capture active-tab bucket at dispatch time; if user changes tabs
	// while the cmd is in flight, we still place it in the intended bucket.
	t := m.tabs[m.activeTab]
	// "Add" on the Done tab doesn't really make sense; fall back to today.
	bucket := t.bucket
	if bucket == "" {
		bucket = schedule.Today
	}
	storeRef := m.store
	repoPath := m.repoPath
	return func() tea.Msg {
		nowT := time.Now()
		scheduleDate, err := schedule.Parse(bucket, nowT)
		if err != nil {
			return taskSavedMsg{err: fmt.Errorf("schedule: %w", err)}
		}
		existing, err := bucketSiblings(storeRef, scheduleDate, nowT)
		if err != nil {
			return taskSavedMsg{err: fmt.Errorf("list: %w", err)}
		}
		id, err := model.NewID()
		if err != nil {
			return taskSavedMsg{err: fmt.Errorf("id: %w", err)}
		}
		n := now()
		task := model.Task{
			ID:        id,
			Title:     title,
			Source:    "tui",
			Status:    "open",
			Position:  ordering.NextPosition(existing),
			Schedule:  scheduleDate,
			CreatedAt: n,
			UpdatedAt: n,
		}
		if err := storeRef.Create(task); err != nil {
			return taskSavedMsg{err: fmt.Errorf("create: %w", err)}
		}
		if err := git.AutoCommit(repoPath, fmt.Sprintf("add: %s", title), taskRelPath(id)); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		return taskSavedMsg{status: fmt.Sprintf("Added: %s", title), focusID: id}
	}
}

// focusTaskByID searches every tab for a task with the given ID. If found,
// the active tab switches to that tab and the list cursor moves to the item.
func (m *Model) focusTaskByID(id string) {
	for i := range m.lists {
		for j, li := range m.lists[i].Items() {
			if li.(item).task.ID == id {
				m.activeTab = i
				m.lists[i].Select(j)
				return
			}
		}
	}
}

// syncCmd runs a full sync (commit + pull + auto-resolve + push) in the
// background and surfaces the result in the status bar.
func (m *Model) syncCmd() tea.Cmd {
	repoPath := m.repoPath
	return func() tea.Msg {
		res, err := git.Sync(repoPath)
		if err != nil {
			return taskSavedMsg{err: err}
		}
		if !res.HasRemote {
			return taskSavedMsg{status: "No remote configured; committed locally"}
		}
		if res.Resolved > 0 {
			return taskSavedMsg{status: fmt.Sprintf("Synced (auto-resolved %d conflicts)", res.Resolved)}
		}
		return taskSavedMsg{status: "Synced"}
	}
}

// deleteCmd removes a task and commits the deletion.
func (m *Model) deleteCmd(task model.Task) tea.Cmd {
	return func() tea.Msg {
		if err := m.store.Delete(task.ID); err != nil {
			return taskSavedMsg{err: fmt.Errorf("delete: %w", err)}
		}
		if err := git.AutoCommit(m.repoPath, fmt.Sprintf("rm: %s", task.Title), taskRelPath(task.ID)); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		return taskSavedMsg{status: fmt.Sprintf("Deleted: %s", task.Title)}
	}
}

// --- View ------------------------------------------------------------------

func (m *Model) View() string {
	var tabBar []string
	for i, t := range m.tabs {
		if i == m.activeTab {
			tabBar = append(tabBar, activeTabStyle.Render(t.label))
		} else {
			tabBar = append(tabBar, tabStyle.Render(t.label))
		}
	}
	header := lipgloss.JoinHorizontal(lipgloss.Top, tabBar...)

	var body string
	if m.mode == modeNormal || m.mode == modeGrab {
		// Grab mode keeps the list view; the help bar signals the mode.
		body = m.lists[m.activeTab].View()
	} else {
		body = m.modalView()
	}

	help := m.helpLine()
	return lipgloss.JoinVertical(lipgloss.Left, header, body, help)
}

func (m *Model) helpLine() string {
	if m.err != nil {
		return statusStyle.Render("error: " + m.err.Error())
	}
	if m.statusMsg != "" {
		return statusStyle.Render(m.statusMsg)
	}
	switch m.mode {
	case modeNormal:
		return helpStyle.Render("←/→ tabs  1-5 jump  d done  e edit  r resched  t tag  a add  x del  m grab  s sync  q quit")
	case modeGrab:
		return helpStyle.Render("GRAB  ↑/↓ reorder  ←/→ move bucket  g/G top/bottom  enter drop  esc cancel")
	case modeReschedule:
		if m.rescheduleSub == 0 {
			return helpStyle.Render("1 today  2 tomorrow  3 week  4 someday  5 custom  esc cancel")
		}
		return helpStyle.Render("enter save  esc cancel")
	case modeRetag, modeAdd:
		return helpStyle.Render("enter save  esc cancel")
	case modeConfirmDelete:
		return helpStyle.Render("y confirm  anything else cancels")
	}
	return ""
}

func (m *Model) modalView() string {
	switch m.mode {
	case modeReschedule:
		if m.rescheduleSub == 0 {
			return modalBox("Reschedule:\n\n" +
				"  1  Today\n" +
				"  2  Tomorrow\n" +
				"  3  Week\n" +
				"  4  Someday\n" +
				"  5  Custom date...")
		}
		return modalBox("Custom date:\n\n" + m.input.View())
	case modeRetag:
		return modalBox("Tags (comma-separated):\n\n" + m.input.View())
	case modeAdd:
		t := m.tabs[m.activeTab]
		return modalBox(fmt.Sprintf("Add task to %s:\n\n%s", t.label, m.input.View()))
	case modeConfirmDelete:
		if m.modalTask == nil {
			return ""
		}
		return modalBox(fmt.Sprintf("Delete %q?\n\n(y = confirm, anything else cancels)", m.modalTask.Title))
	}
	return ""
}

// modalBox wraps content in a subtle bordered box.
func modalBox(content string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Render(content)
}

// --- helpers ---------------------------------------------------------------

func taskRelPath(id string) string {
	return filepath.Join(".monolog", "tasks", id+".json")
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// sanitizeTags splits a comma-separated string into tags, trimming and
// dropping empties. Duplicates preserved in input order (caller's choice).
func sanitizeTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	var tags []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			tags = append(tags, p)
		}
	}
	return tags
}

