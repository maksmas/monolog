package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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

// addField tracks which input has focus in the add modal.
type addField int

const (
	addFocusTitle addField = iota
	addFocusTags
)

// activePanelChromeWidth is the number of non-title characters per line in the
// active panel: border (2) + padding (2) + ShortID (8) + separator (2) = 14.
const activePanelChromeWidth = 14

// reschedulePresets are the quick-pick bucket names shown in the reschedule
// modal. The index + 1 is the numeric shortcut key. They are resolved into
// concrete ISO dates via schedule.Parse before being written.
var reschedulePresets = []string{schedule.Today, schedule.Tomorrow, schedule.Week, schedule.Month, schedule.Someday}

// tab describes one virtual schedule bucket shown in the TUI.
type tab struct {
	label  string
	bucket string // bucket name (today/tomorrow/week/month/someday); empty for Done
	status string // "open" for regular tabs, "done" for the Done tab
}

// defaultTabs are the tabs shown in display order. 1-6 shortcut keys index in.
var defaultTabs = []tab{
	{label: "Today", bucket: schedule.Today, status: "open"},
	{label: "Tomorrow", bucket: schedule.Tomorrow, status: "open"},
	{label: "Week", bucket: schedule.Week, status: "open"},
	{label: "Month", bucket: schedule.Month, status: "open"},
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

	// Add-modal state: second text input for tags and focus tracker.
	tagInput  textinput.Model
	addFocus  addField
	knownTags []string // cached known tags, populated when add modal opens

	// Grab-mode state. grabTask is a working copy whose Position is not
	// mutated until Enter drop; its current visual location is (activeTab,
	// grabIndex) within that tab's list items.
	grabTask  *model.Task
	grabIndex int

	// pendingAction holds an action to dispatch after a successful
	// taskSavedMsg (e.g. open editor after commitGrab). Cleared on
	// error or modal close.
	pendingAction func() tea.Cmd

	// Active tasks panel: tasks that carry the "active" tag, shown in a
	// persistent panel above the tab bar.
	activeTasks []model.Task

	// itemHeight is the delegate height for list items: 2 when all titles
	// in the active tab fit in one line, 3 when any title requires wrapping.
	itemHeight int

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
	if vt := display.VisibleTags(i.task.Tags); len(vt) > 0 {
		parts = append(parts, "["+strings.Join(vt, ", ")+"]")
	}
	if dates := display.FormatTaskDates(i.now, i.task); dates != "" {
		parts = append(parts, dates)
	}
	return strings.Join(parts, "  ")
}

func (i item) FilterValue() string { return i.task.Title }

// itemDelegate wraps list.DefaultDelegate to apply context-dependent styling.
// A pointer to Model lets Render consult the current mode and task state.
// Render decision tree (first match wins):
//   - grab: in modeGrab the selected row uses orange grabStyles to signal it's being moved.
//   - active: tasks with the "active" tag use green activeStyles (both selected and unselected).
//   - base: all other tasks use the default delegate styles.
type itemDelegate struct {
	base         list.DefaultDelegate
	grabStyles   list.DefaultItemStyles
	activeStyles list.DefaultItemStyles
	m            *Model
}

func newItemDelegate(m *Model) *itemDelegate {
	base := list.NewDefaultDelegate()
	grab := list.NewDefaultItemStyles()
	// Distinct from default pink selection: orange/yellow reads as "held".
	grabColor := lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#FFB454"}
	grab.SelectedTitle = grab.SelectedTitle.
		Foreground(grabColor).
		BorderForeground(grabColor).
		Bold(true)
	grab.SelectedDesc = grab.SelectedDesc.
		Foreground(grabColor).
		BorderForeground(grabColor)

	// Green styling for active tasks (persistent "currently working on" state).
	active := list.NewDefaultItemStyles()
	activeColor := lipgloss.AdaptiveColor{Light: "#16A34A", Dark: "#22C55E"}
	active.NormalTitle = active.NormalTitle.Foreground(activeColor)
	active.NormalDesc = active.NormalDesc.Foreground(activeColor)
	active.SelectedTitle = active.SelectedTitle.
		Foreground(activeColor).
		BorderForeground(activeColor)
	active.SelectedDesc = active.SelectedDesc.
		Foreground(activeColor).
		BorderForeground(activeColor)

	return &itemDelegate{base: base, grabStyles: grab, activeStyles: active, m: m}
}

func (d *itemDelegate) Height() int {
	if d.m.itemHeight > 0 {
		return d.m.itemHeight
	}
	return d.base.Height()
}
func (d *itemDelegate) Spacing() int { return d.base.Spacing() }
func (d *itemDelegate) Update(msg tea.Msg, lm *list.Model) tea.Cmd {
	return d.base.Update(msg, lm)
}

func (d *itemDelegate) Render(w io.Writer, lm list.Model, index int, it list.Item) {
	i, ok := it.(item)
	if !ok || lm.Width() <= 0 {
		return
	}

	// Pick the style set based on mode and task state.
	var s *list.DefaultItemStyles
	switch {
	case d.m.mode == modeGrab && index == lm.Index():
		s = &d.grabStyles
	case i.task.IsActive():
		s = &d.activeStyles
	default:
		s = &d.base.Styles
	}

	isSelected := index == lm.Index()

	// Available text width (left padding is 2 for both normal and selected).
	textWidth := lm.Width() - s.NormalTitle.GetPaddingLeft() - s.NormalTitle.GetPaddingRight()

	// Wrap title across available lines (height − 1 reserved for description).
	titleLines := wrapText(i.Title(), textWidth)
	maxTitleLines := d.Height() - 1
	if maxTitleLines < 1 {
		maxTitleLines = 1
	}
	if len(titleLines) > maxTitleLines {
		titleLines = titleLines[:maxTitleLines]
		// Add ellipsis to signal truncation.
		last := titleLines[len(titleLines)-1]
		runes := []rune(last)
		if len(runes) >= textWidth {
			titleLines[len(titleLines)-1] = string(runes[:textWidth-1]) + "…"
		} else {
			titleLines[len(titleLines)-1] = last + "…"
		}
	}
	title := strings.Join(titleLines, "\n")

	desc := i.Description()
	desc = truncateTitle(desc, textWidth)

	// Apply styles.
	if isSelected {
		title = s.SelectedTitle.Render(title)
		desc = s.SelectedDesc.Render(desc)
	} else {
		title = s.NormalTitle.Render(title)
		desc = s.NormalDesc.Render(desc)
	}

	fmt.Fprintf(w, "%s\n%s", title, desc)
}

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

	tagTi := textinput.New()
	tagTi.Placeholder = "tag1, tag2"
	tagTi.CharLimit = 512

	m := &Model{
		store:    s,
		repoPath: repoPath,
		tabs:     defaultTabs,
		lists:    make([]list.Model, len(defaultTabs)),
		input:    ti,
		tagInput: tagTi,
	}

	delegate := newItemDelegate(m)
	for i, t := range m.tabs {
		l := list.New(nil, delegate, 0, 0)
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

// reloadAll refreshes every tab from the store and the active-tasks panel.
func (m *Model) reloadAll() error {
	for i := range m.tabs {
		if err := m.reloadTab(i); err != nil {
			return err
		}
	}
	if err := m.reloadActive(); err != nil {
		return err
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

// reloadActive refreshes the activeTasks slice from the store.
func (m *Model) reloadActive() error {
	tasks, err := m.store.List(store.ListOptions{Tag: model.ActiveTag, Status: "open"})
	if err != nil {
		return fmt.Errorf("list active tasks: %w", err)
	}
	// Sort by position for stable display order.
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Position < tasks[j].Position
	})
	m.activeTasks = tasks
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
		m.recomputeLayout()
		return m, nil

	case taskSavedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.statusMsg = ""
			m.pendingAction = nil
			return m, nil
		}
		m.err = nil
		m.statusMsg = msg.status
		if err := m.reloadAll(); err != nil {
			m.err = err
		}
		m.recomputeLayout()
		if msg.focusID != "" {
			m.focusTaskByID(msg.focusID)
		}
		if action := m.pendingAction; action != nil {
			m.pendingAction = nil
			return m, action()
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
		m.recomputeLayout()
		return m, nil
	case "right", "tab":
		m.activeTab = (m.activeTab + 1) % len(m.tabs)
		m.recomputeLayout()
		return m, nil
	case "1", "2", "3", "4", "5", "6":
		n := int(msg.String()[0] - '1')
		if n < len(m.tabs) {
			m.activeTab = n
			m.recomputeLayout()
		}
		return m, nil

	case "d":
		return m, m.doneSelected()
	case "r":
		return m, m.openReschedule()
	case "t":
		return m, m.openRetag()
	case "c":
		return m, m.openAdd()
	case "x":
		return m, m.openConfirmDelete()
	case "m":
		m.openGrab()
		return m, nil
	case "a":
		return m, m.toggleActive()
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
	m.pendingAction = nil
	m.input.Blur()
	m.input.SetValue("")
	m.tagInput.Blur()
	m.tagInput.SetValue("")
	m.addFocus = addFocusTitle
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
	t.SetActive(false)
	t.UpdatedAt = now()
	return m.saveCmd(t, fmt.Sprintf("done: %s", t.Title), fmt.Sprintf("Completed: %s", t.Title))
}

// --- active toggle ---------------------------------------------------------

func (m *Model) toggleActive() tea.Cmd {
	task := m.selectedTask()
	if task == nil {
		return nil
	}
	t := *task
	t.SetActive(!t.IsActive())
	t.UpdatedAt = now()
	label := "activated"
	if !t.IsActive() {
		label = "deactivated"
	}
	return m.saveCmd(t, fmt.Sprintf("edit: %s", t.Title), fmt.Sprintf("%s: %s", label, t.Title))
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
		case "1", "2", "3", "4", "5":
			n := int(msg.String()[0] - '1')
			return m, m.applyReschedule(reschedulePresets[n])
		case "6":
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
	// Filter out the reserved active tag so the user only sees their own tags.
	// Active state is preserved separately via wasActive/SetActive in updateRetag.
	m.input.SetValue(strings.Join(display.VisibleTags(t.Tags), ", "))
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
		wasActive := t.IsActive()
		t.Tags = model.SanitizeTags(m.input.Value())
		t.SetActive(wasActive)
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
	m.tagInput.SetValue("")
	m.tagInput.Blur()
	m.addFocus = addFocusTitle
	// Cache known tags from all loaded list items for instant auto-tag on ":".
	var allTasks []model.Task
	for i := range m.lists {
		for _, li := range m.lists[i].Items() {
			allTasks = append(allTasks, li.(item).task)
		}
	}
	m.knownTags = model.CollectTags(allTasks)
	return textinput.Blink
}

func (m *Model) updateAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Tab toggles focus between title and tags — intercept before the
	// textinput swallows it.
	if msg.Type == tea.KeyTab {
		if m.addFocus == addFocusTitle {
			m.addFocus = addFocusTags
			m.input.Blur()
			m.tagInput.Focus()
		} else {
			m.addFocus = addFocusTitle
			m.tagInput.Blur()
			m.input.Focus()
		}
		return m, textinput.Blink
	}

	if msg.Type == tea.KeyEnter {
		title := strings.TrimSpace(m.input.Value())
		if title == "" {
			m.closeModal()
			return m, nil
		}
		tags := model.SanitizeTags(m.tagInput.Value())
		m.closeModal()
		return m, m.createCmd(title, tags)
	}

	// Route remaining keys to the focused input only.
	var cmd tea.Cmd
	if m.addFocus == addFocusTags {
		m.tagInput, cmd = m.tagInput.Update(msg)
	} else {
		m.input, cmd = m.input.Update(msg)
		// After updating the title input, check if a known tag prefix was typed.
		// Auto-populate the tags field on ":" so the user gets instant feedback.
		if autoTag := model.ParseTitleTag(m.input.Value(), m.knownTags); autoTag != "" {
			existing := m.tagInput.Value()
			if !tagFieldContains(existing, autoTag) {
				if existing == "" {
					m.tagInput.SetValue(autoTag)
				} else {
					m.tagInput.SetValue(existing + ", " + autoTag)
				}
			}
		}
	}
	return m, cmd
}

// tagFieldContains reports whether the comma-separated tag field text already
// contains the given tag (trimming whitespace around each entry).
func tagFieldContains(field, tag string) bool {
	for _, part := range strings.Split(field, ",") {
		if strings.TrimSpace(part) == tag {
			return true
		}
	}
	return false
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
// The reserved "active" tag is filtered out so the user only sees their own
// tags; active state is preserved separately in applyEditedYAML.
func marshalTaskForEdit(t model.Task) ([]byte, error) {
	return yaml.Marshal(editableFields{
		Title:    t.Title,
		Body:     t.Body,
		Schedule: t.Schedule,
		Tags:     display.VisibleTags(t.Tags),
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
	wasActive := orig.IsActive()
	out.Title = edit.Title
	out.Body = edit.Body
	out.Schedule = scheduleDate
	out.Tags = edit.Tags
	out.SetActive(wasActive)
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

	// Action keys: commit grab first, then dispatch the action after reload.
	case "e":
		m.pendingAction = func() tea.Cmd { return m.openEdit() }
		return m, m.commitGrab()
	case "r":
		m.pendingAction = func() tea.Cmd { return m.openReschedule() }
		return m, m.commitGrab()
	case "t":
		m.pendingAction = func() tea.Cmd { return m.openRetag() }
		return m, m.commitGrab()
	case "d":
		m.pendingAction = func() tea.Cmd { return m.doneSelected() }
		return m, m.commitGrab()
	case "a":
		m.pendingAction = func() tea.Cmd { return m.toggleActive() }
		return m, m.commitGrab()
	case "c":
		m.pendingAction = func() tea.Cmd { return m.openAdd() }
		return m, m.commitGrab()
	case "x":
		m.pendingAction = func() tea.Cmd { return m.openConfirmDelete() }
		return m, m.commitGrab()
	case "s":
		m.pendingAction = func() tea.Cmd {
			m.statusMsg = "Syncing..."
			return m.syncCmd()
		}
		return m, m.commitGrab()
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
	if t.Status == "done" {
		t.SetActive(false)
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
		return taskSavedMsg{status: fmt.Sprintf("Moved: %s", t.Title), focusID: t.ID}
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
func (m *Model) createCmd(title string, tags []string) tea.Cmd {
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

		// Load all tasks once — derive bucket siblings (for position) and
		// known tags (for auto-tag) from the same scan, avoiding a second
		// full directory read.
		allTasks, err := storeRef.List(store.ListOptions{})
		if err != nil {
			return taskSavedMsg{err: fmt.Errorf("list: %w", err)}
		}
		bkt := schedule.Bucket(scheduleDate, nowT)
		var siblings []model.Task
		for _, tk := range allTasks {
			if tk.Status == "open" && schedule.MatchesBucket(tk.Schedule, bkt, nowT) {
				siblings = append(siblings, tk)
			}
		}

		// Auto-tag from title prefix using already-loaded tasks
		tags = model.AutoTag(title, model.CollectTags(allTasks), tags)

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
			Position:  ordering.NextPosition(siblings),
			Schedule:  scheduleDate,
			Tags:      tags,
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

// --- active panel ----------------------------------------------------------

// activePanelView renders a bordered panel listing each active task. Returns
// an empty string when there are no active tasks (auto-hide).
func (m *Model) activePanelView() string {
	if len(m.activeTasks) == 0 {
		return ""
	}
	titleWidth := m.width - activePanelChromeWidth
	var lines []string
	for _, t := range m.activeTasks {
		titleLines := wrapText(t.Title, titleWidth)
		if len(titleLines) > 2 {
			titleLines = titleLines[:2]
			last := titleLines[1]
			runes := []rune(last)
			if len(runes) >= titleWidth {
				titleLines[1] = string(runes[:titleWidth-1]) + "…"
			} else {
				titleLines[1] = last + "…"
			}
		}
		prefix := display.ShortID(t.ID) + "  "
		indent := strings.Repeat(" ", utf8.RuneCountInString(prefix))
		for i, line := range titleLines {
			if i == 0 {
				lines = append(lines, prefix+line)
			} else {
				lines = append(lines, indent+line)
			}
		}
	}
	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("28")).
		Padding(0, 1).
		Render(content)
}

// activePanelHeight returns the rendered line count of the active panel. Returns
// 0 when there are no active tasks (panel hidden). Mirrors the wrapping logic
// in activePanelView so the two never drift apart.
// Structure: 1 top border + content lines + 1 bottom border.
func (m *Model) activePanelHeight() int {
	if len(m.activeTasks) == 0 {
		return 0
	}
	titleWidth := m.width - activePanelChromeWidth
	if titleWidth <= 0 {
		return len(m.activeTasks) + 2
	}
	contentLines := 0
	for _, t := range m.activeTasks {
		n := len(wrapText(t.Title, titleWidth))
		if n > 2 {
			n = 2
		}
		contentLines += n
	}
	return contentLines + 2
}

// recomputeLayout recalculates list sizes based on the current terminal
// dimensions and active panel height. Called from WindowSizeMsg and after
// any mutation that might change the panel (taskSavedMsg).
func (m *Model) recomputeLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	m.itemHeight = m.computeItemHeight()
	listH := m.height - 4 - m.activePanelHeight()
	if listH < 3 {
		listH = 3
	}
	for i := range m.lists {
		m.lists[i].SetSize(m.width, listH)
	}
}

// computeItemHeight returns 3 if any title in the active tab exceeds the
// available text width (needs wrapping), otherwise 2.
func (m *Model) computeItemHeight() int {
	if m.width <= 0 {
		return 2
	}
	// Text width matches the delegate Render calculation: list width minus
	// NormalTitle left padding (2).
	textWidth := m.width - 2
	if textWidth <= 0 {
		return 2
	}
	for _, it := range m.lists[m.activeTab].Items() {
		if task, ok := it.(item); ok {
			if utf8.RuneCountInString(task.Title()) > textWidth {
				return 3
			}
		}
	}
	return 2
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

	parts := []string{}
	if panel := m.activePanelView(); panel != "" {
		parts = append(parts, panel)
	}
	parts = append(parts, header, body, help)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
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
		return helpStyle.Render("←/→ tabs  1-6 jump  d done  e edit  r resched  t tag  c add  x del  m grab  a active  s sync  q quit")
	case modeGrab:
		return helpStyle.Render("GRAB  ↑/↓ reorder  ←/→ move bucket  g/G top/bottom  enter drop  esc cancel")
	case modeReschedule:
		if m.rescheduleSub == 0 {
			return helpStyle.Render("1 today  2 tomorrow  3 week  4 month  5 someday  6 custom  esc cancel")
		}
		return helpStyle.Render("enter save  esc cancel")
	case modeRetag:
		return helpStyle.Render("enter save  esc cancel")
	case modeAdd:
		return helpStyle.Render("tab switch field  enter save  esc cancel")
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
				"  4  Month\n" +
				"  5  Someday\n" +
				"  6  Custom date...")
		}
		return modalBox("Custom date:\n\n" + m.input.View())
	case modeRetag:
		return modalBox("Tags (comma-separated):\n\n" + m.input.View())
	case modeAdd:
		t := m.tabs[m.activeTab]
		return modalBox(fmt.Sprintf("Add task to %s:\n\nTitle: %s\nTags:  %s", t.label, m.input.View(), m.tagInput.View()))
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

// wrapText breaks s into lines of at most width runes, splitting at word
// boundaries when possible. Falls back to hard-breaking mid-word when a
// single word exceeds the width. Returns at least one line.
func wrapText(s string, width int) []string {
	if width <= 0 || utf8.RuneCountInString(s) <= width {
		return []string{s}
	}
	runes := []rune(s)
	var lines []string
	start := 0
	for start < len(runes) {
		end := start + width
		if end >= len(runes) {
			lines = append(lines, string(runes[start:]))
			break
		}
		// Look backwards for a space to break at.
		cut := end
		for cut > start && runes[cut] != ' ' {
			cut--
		}
		if cut == start {
			// No space found — hard-break at width.
			lines = append(lines, string(runes[start:end]))
			start = end
		} else {
			lines = append(lines, string(runes[start:cut]))
			start = cut + 1 // skip the space
		}
	}
	return lines
}

// truncateTitle shortens s to width runes, appending "…" if truncated.
// If width <= 0 (e.g., terminal size not yet known), returns s unchanged.
func truncateTitle(s string, width int) string {
	if width <= 0 {
		return s
	}
	n := utf8.RuneCountInString(s)
	if n <= width {
		return s
	}
	runes := []rune(s)
	return string(runes[:width-1]) + "…"
}
