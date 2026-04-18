package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/recurrence"
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
	modeHelp
	modeSearch
)

// addField tracks which input has focus in the add modal.
type addField int

const (
	addFocusTitle addField = iota
	addFocusTags
	addFocusRecur
)

// viewMode selects between schedule-based tabs and tag-based tabs.
type viewMode int

const (
	viewSchedule viewMode = iota
	viewTag
)

// tagTab describes one tab in tag view mode.
type tagTab struct {
	label      string // display label (tag name, or "Active", or "Untagged")
	tag        string // tag to filter by; empty string means "untagged"
	isActive   bool   // true for the active tab (filters by model.ActiveTag)
	isUntagged bool   // true for the special untagged tab
}

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

// searchState holds the per-modal state for the fuzzy-search overlay.
// haystack is captured once at openSearch time along with parallel titles/
// bodies slices that sahilm/fuzzy consumes, so the ranker does not re-allocate
// on every keystroke. results is re-ranked on every keystroke. cursor is the
// currently-highlighted index into results.
type searchState struct {
	input    textinput.Model
	haystack []searchDoc
	titles   []string
	bodies   []string
	results  []searchResult
	cursor   int
}

// searchInputReserve is the column budget taken out of the total width for the
// search input bar's prompt and right-aligned counter. A single constant keeps
// recomputeLayout and renderSearch in sync.
const searchInputReserve = 14

// Model is the top-level Bubble Tea model for the TUI.
type Model struct {
	store    *store.Store
	repoPath string

	tabs      []tab
	activeTab int
	lists     []*vlist // one per tab

	// Style sets for list item rendering.
	baseStyles   list.DefaultItemStyles
	grabStyles   list.DefaultItemStyles
	activeStyles list.DefaultItemStyles

	// View mode: schedule (default) or tag.
	viewMode viewMode
	tagTabs  []tagTab // populated when viewMode == viewTag

	width, height int

	// Modal state. All modals reuse a single text input; only one modal can
	// be open at a time.
	mode          mode
	input         textinput.Model
	modalTask     *model.Task // task the modal is acting on (nil for add)
	rescheduleSub int         // 0 = picker, 1 = custom date input

	// Add-modal state. The title uses a textarea so Alt+Enter can insert a
	// newline while Enter submits; tags remain a single-line textinput.
	titleArea  textarea.Model
	tagInput   textinput.Model
	recurInput textinput.Model
	addFocus   addField
	knownTags  []string // cached known tags, populated when add modal opens

	// Grab-mode state. grabTask is a working copy whose Position is not
	// mutated until Enter drop; its current visual location is (activeTab,
	// grabIndex) within that tab's list items.
	grabTask  *model.Task
	grabIndex int

	// pendingAction holds an action to dispatch after a successful
	// taskSavedMsg (e.g. open editor after commitGrab). Cleared on
	// error or modal close.
	pendingAction func() tea.Cmd

	// Detail panel state. The detail panel is a right-side panel showing full
	// task info and notes with an inline textarea for adding new notes.
	// detailOpen is independent of mode — it's a panel toggle, not a modal.
	detailOpen   bool
	detailScroll int
	noteArea     textarea.Model

	// Tag autocomplete state. Only one modal is open at a time, so a single
	// pair of fields is shared between add and retag modals.
	suggestions   []string // current filtered tag suggestions (max 5)
	suggestionIdx int      // selected suggestion index; -1 = none selected

	// Active tasks panel: tasks that carry the "active" tag, shown in a
	// persistent panel above the tab bar.
	activeTasks []model.Task

	// allTasks is the full unfiltered task list, refreshed after every mutation.
	// Used for computing global stats.
	allTasks []model.Task
	stats    model.Stats

	// search holds the fuzzy-search overlay state (modeSearch).
	search searchState

	statusMsg string // transient status line
	err       error  // sticky error; cleared on next successful action
}

// item wraps a model.Task for display in a bubbles/list.
// now is captured at reloadTab time. Dates won't refresh in a tab until the
// next mutation or tab switch triggers a reload — acceptable tradeoff for a
// personal CLI tool.
//
// When isSeparator is true, the item represents a bucket heading (e.g.
// "Today", "Week") rather than a real task. The task field's Title holds the
// bucket label; all other task fields are zero-valued. Separators are rendered
// as dimmed, non-selectable dividers by the delegate.
type item struct {
	task        model.Task
	now         time.Time // render-time clock for compact date display
	isSeparator bool      // true for bucket heading items in tag view
}

// newSeparatorItem creates a separator item with the given bucket label.
func newSeparatorItem(label string) item {
	return item{
		task:        model.Task{Title: label},
		isSeparator: true,
	}
}

func (i item) Title() string { return i.task.Title }

func (i item) Description() string {
	parts := []string{display.ShortID(i.task.ID)}
	if i.task.NoteCount > 0 {
		parts = append(parts, fmt.Sprintf("[%d]", i.task.NoteCount))
	}
	if i.task.Schedule != "" && schedule.Bucket(i.task.Schedule, i.now) != schedule.Today {
		parts = append(parts, i.task.Schedule)
	}
	if vt := display.VisibleTags(i.task.Tags); len(vt) > 0 {
		parts = append(parts, "["+strings.Join(vt, ", ")+"]")
	}
	if dates := display.FormatTaskDates(i.now, i.task, config.DateFormat()); dates != "" {
		parts = append(parts, dates)
	}
	return strings.Join(parts, "  ")
}

func (i item) FilterValue() string { return i.task.Title }

// initStyles sets up the three style sets used for list item rendering.
// Styles carry only foreground colors (no padding or borders) because bullet
// prefixes handle indentation and selection indication.
func initStyles() (base, grab, active list.DefaultItemStyles) {
	normalFg := lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"}
	selectedFg := lipgloss.AdaptiveColor{Light: "#EE6FF8", Dark: "#EE6FF8"}
	dimFg := lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"}

	base.NormalTitle = lipgloss.NewStyle().Foreground(normalFg)
	base.SelectedTitle = lipgloss.NewStyle().Foreground(selectedFg)
	base.NormalDesc = lipgloss.NewStyle().Foreground(dimFg)
	base.SelectedDesc = lipgloss.NewStyle().Foreground(selectedFg)

	// Distinct from default pink selection: orange/yellow reads as "held".
	grab = base
	grabColor := lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#FFB454"}
	grab.SelectedTitle = lipgloss.NewStyle().Foreground(grabColor).Bold(true)
	grab.SelectedDesc = lipgloss.NewStyle().Foreground(grabColor)

	// Green styling for active tasks (persistent "currently working on" state).
	active = base
	activeColor := lipgloss.AdaptiveColor{Light: "#16A34A", Dark: "#22C55E"}
	activeSelectedColor := lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	active.NormalTitle = lipgloss.NewStyle().Foreground(activeColor)
	active.NormalDesc = lipgloss.NewStyle().Foreground(activeColor)
	active.SelectedTitle = lipgloss.NewStyle().Foreground(activeSelectedColor).Bold(true)
	active.SelectedDesc = lipgloss.NewStyle().Foreground(activeSelectedColor)

	return
}

// renderListItem renders a single list item (regular or separator) as a
// fully styled multi-line string. Used as the callback for vlist.Render.
func (m *Model) renderListItem(index int, it list.Item, selected bool) string {
	i, ok := it.(item)
	if !ok {
		return ""
	}
	if i.isSeparator {
		label := "── " + i.task.Title + " ──"
		return separatorStyle.Render(label)
	}

	// Pick the style set based on mode and task state.
	var s *list.DefaultItemStyles
	switch {
	case m.mode == modeGrab && selected:
		s = &m.grabStyles
	case i.task.IsActive():
		s = &m.activeStyles
	default:
		s = &m.baseStyles
	}

	// Text width after bullet prefix ("▸ " / "· " = 2 chars).
	textWidth := m.lists[m.activeTab].Width() - 2

	titleLines := wrapText(i.Title(), textWidth)

	// Prepend bullet on first line, indent continuation lines.
	bullet := "· "
	if selected {
		bullet = "▸ "
	}
	const indent = "  "
	for j := range titleLines {
		if j == 0 {
			titleLines[j] = bullet + titleLines[j]
		} else {
			titleLines[j] = indent + titleLines[j]
		}
	}
	title := strings.Join(titleLines, "\n")

	desc := i.Description()
	desc = indent + truncateTitle(desc, textWidth)

	if selected {
		title = s.SelectedTitle.Render(title)
		desc = s.SelectedDesc.Render(desc)
	} else {
		title = s.NormalTitle.Render(title)
		desc = s.NormalDesc.Render(desc)
	}

	return title + "\n" + desc + "\n"
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
func newModel(s *store.Store, repoPath string, opts Options) (*Model, error) {
	ti := textinput.New()
	ti.CharLimit = 512
	// Drop the default "> " prompt — our modals use explicit "Title:" / "Tags:"
	// labels, and the default prompt throws off width math for the fixed-size
	// modal box.
	ti.Prompt = ""

	tagTi := textinput.New()
	tagTi.Placeholder = "tag1, tag2"
	tagTi.CharLimit = 512
	tagTi.Prompt = ""

	recurTi := textinput.New()
	recurTi.Placeholder = "e.g. monthly:1"
	recurTi.CharLimit = 64
	recurTi.Prompt = ""

	searchTi := textinput.New()
	searchTi.Placeholder = "search title or body"
	searchTi.CharLimit = 512
	searchTi.Prompt = "> "

	ta := textarea.New()
	ta.Placeholder = "task title"
	ta.CharLimit = 512
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	// Rebind newline insertion from Enter to Alt+Enter. Plain Enter is handled
	// one level up in updateAdd() where it submits the task.
	ta.KeyMap.InsertNewline.SetKeys("alt+enter")
	ta.SetHeight(1)
	// Flatten the textarea's own cursor-line background so the modal looks like
	// a regular one-line input until the user presses Alt+Enter.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()

	// Note textarea for the detail panel. Alt+Enter inserts newlines; plain
	// Enter is handled one level up where it submits the note.
	noteTA := textarea.New()
	noteTA.Placeholder = "add a note..."
	noteTA.CharLimit = 4096
	noteTA.ShowLineNumbers = false
	noteTA.Prompt = ""
	noteTA.KeyMap.InsertNewline.SetKeys("alt+enter")
	noteTA.SetHeight(3)
	noteTA.FocusedStyle.CursorLine = lipgloss.NewStyle()
	noteTA.FocusedStyle.Base = lipgloss.NewStyle()
	noteTA.BlurredStyle.CursorLine = lipgloss.NewStyle()
	noteTA.BlurredStyle.Base = lipgloss.NewStyle()

	m := &Model{
		store:      s,
		repoPath:   repoPath,
		tabs:       defaultTabs,
		input:      ti,
		tagInput:   tagTi,
		recurInput: recurTi,
		titleArea:  ta,
		noteArea:   noteTA,
		search:     searchState{input: searchTi},
	}
	m.baseStyles, m.grabStyles, m.activeStyles = initStyles()

	m.lists = m.initLists()

	if err := m.reloadAll(); err != nil {
		return nil, err
	}

	// If the caller requested tag view at launch, switch now.
	if opts.StartInTagView {
		if err := m.rebuildForTagView(); err != nil {
			return nil, err
		}
	}

	return m, nil
}

// initLists creates a fresh slice of vlist widgets, one per tab.
func (m *Model) initLists() []*vlist {
	lists := make([]*vlist, len(m.tabs))
	for i := range m.tabs {
		lists[i] = &vlist{}
	}
	return lists
}

// reloadAll refreshes every tab from the store and the active-tasks panel.
// In tag view, it first rebuilds the tag tab list from the current task set
// to reflect tag additions/removals since the last reload.
func (m *Model) reloadAll() error {
	if m.viewMode == viewTag {
		if err := m.refreshTagTabs(); err != nil {
			return err
		}
	}
	for i := range m.tabs {
		if err := m.reloadTab(i); err != nil {
			return err
		}
	}
	if err := m.reloadActive(); err != nil {
		return err
	}
	if err := m.reloadAllTasks(); err != nil {
		return err
	}
	return nil
}

// refreshTagTabs rescans tasks and rebuilds tagTabs/tabs/lists if the set of
// tags has changed. Called from reloadAll in tag view mode.
func (m *Model) refreshTagTabs() error {
	allTasks, err := m.store.List(store.ListOptions{})
	if err != nil {
		return fmt.Errorf("list tasks for tag refresh: %w", err)
	}

	newTagTabs := buildTagTabs(allTasks)

	// Only rebuild list widgets if the tab count changed.
	if len(newTagTabs) != len(m.tagTabs) {
		m.tagTabs = newTagTabs
		m.tabs = make([]tab, len(m.tagTabs))
		for i, tt := range m.tagTabs {
			m.tabs[i] = tab{label: tt.label, bucket: "", status: "open"}
		}
		if m.activeTab >= len(m.tabs) {
			m.activeTab = 0
		}
		m.lists = m.initLists()
	} else {
		// Tab count unchanged — just update labels in case tag names shifted.
		m.tagTabs = newTagTabs
		for i, tt := range m.tagTabs {
			m.tabs[i] = tab{label: tt.label, bucket: "", status: "open"}
		}
	}
	return nil
}

// reloadTab refreshes a single tab's list from the store. In tag view mode
// it delegates to reloadTagTab; in schedule view it uses the original
// bucket-based filtering.
func (m *Model) reloadTab(idx int) error {
	if m.viewMode == viewTag {
		return m.reloadTagTab(idx)
	}
	return m.reloadScheduleTab(idx)
}

// reloadScheduleTab is the original schedule-view reload: filter tasks by
// bucket and sort by position (or UpdatedAt for the Done tab).
func (m *Model) reloadScheduleTab(idx int) error {
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

// reloadTagTab loads tasks for a single tag tab. Tasks are filtered by the
// tab's tag, grouped by schedule bucket, and each group is preceded by a
// separator item. Within each bucket, tasks are sorted by position.
func (m *Model) reloadTagTab(idx int) error {
	if idx < 0 || idx >= len(m.tagTabs) {
		return fmt.Errorf("tag tab index %d out of range", idx)
	}
	tt := m.tagTabs[idx]
	nowT := time.Now()

	// Load all tasks (open + done) — we filter by tag in-process.
	allTasks, err := m.store.List(store.ListOptions{})
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}

	// Filter tasks by this tab's tag criterion, splitting open and done.
	var openFiltered, doneFiltered []model.Task
	for _, task := range allTasks {
		match := false
		switch {
		case tt.isActive:
			match = task.IsActive()
		case tt.isUntagged:
			match = len(display.VisibleTags(task.Tags)) == 0
		default:
			match = taskHasTag(task, tt.tag)
		}
		if !match {
			continue
		}
		if task.Status == "done" {
			doneFiltered = append(doneFiltered, task)
		} else {
			openFiltered = append(openFiltered, task)
		}
	}

	// Group open tasks by bucket, preserving reschedulePresets order.
	bucketTasks := make(map[string][]model.Task)
	for _, task := range openFiltered {
		bkt := schedule.Bucket(task.Schedule, nowT)
		bucketTasks[bkt] = append(bucketTasks[bkt], task)
	}

	// Sort within each bucket by position.
	for bkt := range bucketTasks {
		sort.SliceStable(bucketTasks[bkt], func(i, j int) bool {
			return bucketTasks[bkt][i].Position < bucketTasks[bkt][j].Position
		})
	}

	// Build items: for each bucket that has tasks, insert a separator + tasks.
	var items []list.Item
	for _, bkt := range reschedulePresets {
		tasks := bucketTasks[bkt]
		if len(tasks) == 0 {
			continue
		}
		label := bucketDisplayName(bkt)
		items = append(items, newSeparatorItem(label))
		for _, task := range tasks {
			items = append(items, item{task: task, now: nowT})
		}
	}

	// Append done tasks at the bottom, sorted by completion time (newest first).
	if len(doneFiltered) > 0 {
		sort.SliceStable(doneFiltered, func(i, j int) bool {
			return doneFiltered[i].UpdatedAt > doneFiltered[j].UpdatedAt
		})
		items = append(items, newSeparatorItem("Done"))
		for _, task := range doneFiltered {
			items = append(items, item{task: task, now: nowT})
		}
	}

	m.lists[idx].SetItems(items)
	return nil
}

// taskHasTag reports whether a task has the given tag (case-sensitive).
func taskHasTag(task model.Task, tag string) bool {
	for _, t := range task.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// bucketLabels maps schedule bucket constants to their display labels.
// Used by both bucketDisplayName and bucketNameFromLabel.
var bucketLabels = map[string]string{
	schedule.Today:    "Today",
	schedule.Tomorrow: "Tomorrow",
	schedule.Week:     "Week",
	schedule.Month:    "Month",
	schedule.Someday:  "Someday",
}

// labelBuckets is the reverse mapping of bucketLabels.
var labelBuckets = func() map[string]string {
	m := make(map[string]string, len(bucketLabels))
	for k, v := range bucketLabels {
		m[v] = k
	}
	return m
}()

// bucketDisplayName returns a human-friendly label for a schedule bucket.
func bucketDisplayName(bucket string) string {
	if label, ok := bucketLabels[bucket]; ok {
		return label
	}
	return bucket
}

// findBucketAbove scans the items in the current tab list backwards from
// m.grabIndex to find the nearest separator item. Returns the bucket name
// (e.g. "today", "week") derived from the separator label, or empty string
// if no separator is found above the grab position.
func (m *Model) findBucketAbove() string {
	items := m.lists[m.activeTab].Items()
	for i := m.grabIndex - 1; i >= 0; i-- {
		it, ok := items[i].(item)
		if ok && it.isSeparator {
			return bucketNameFromLabel(it.task.Title)
		}
	}
	return ""
}

// bucketAtCursor scans backwards from the current list cursor to find the
// nearest separator item. Returns the bucket name (e.g. "today", "week") or
// empty string if no separator is found. Used to determine schedule context
// when creating tasks in tag view.
func (m *Model) bucketAtCursor() string {
	items := m.lists[m.activeTab].Items()
	for i := m.lists[m.activeTab].Index(); i >= 0; i-- {
		it, ok := items[i].(item)
		if ok && it.isSeparator {
			return bucketNameFromLabel(it.task.Title)
		}
	}
	return ""
}

// bucketNameFromLabel returns the schedule bucket constant for a display label.
// Returns the label lowered if no match (fallback; should not happen with
// standard bucket labels).
func bucketNameFromLabel(label string) string {
	if bucket, ok := labelBuckets[label]; ok {
		return bucket
	}
	return strings.ToLower(label)
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

// buildTagTabs builds the tagTab slice for tag view mode from the given tasks.
// Ordering: Active first, then alphabetically sorted tags, then Untagged last.
// The Active and Untagged tabs are always present, even when empty.
// Only tags that have at least one open task generate a tab (done tasks still
// appear inside those tabs via reloadTagTab).
func buildTagTabs(tasks []model.Task) []tagTab {
	// Derive tabs only from open tasks so tags with only done tasks are hidden.
	var open []model.Task
	for _, t := range tasks {
		if t.Status != "done" {
			open = append(open, t)
		}
	}
	tags := model.CollectTags(open) // ActiveTag excluded
	// Re-sort case-insensitively so "jean" < "mlog" < "Warsaw 26".
	sort.Slice(tags, func(i, j int) bool {
		return strings.ToLower(tags[i]) < strings.ToLower(tags[j])
	})

	tabs := make([]tagTab, 0, len(tags)+2)

	// Always prepend the Active tab.
	tabs = append(tabs, tagTab{
		label:    "Active",
		tag:      model.ActiveTag,
		isActive: true,
	})

	// Alphabetically sorted tags in the middle.
	for _, tag := range tags {
		tabs = append(tabs, tagTab{
			label: tag,
			tag:   tag,
		})
	}

	// Always append the Untagged tab.
	tabs = append(tabs, tagTab{
		label:      "Untagged",
		tag:        "",
		isUntagged: true,
	})

	return tabs
}

// reloadAllTasks fetches every task from the store and recomputes global stats.
// Called from reloadAll so stats stay in sync after every mutation.
func (m *Model) reloadAllTasks() error {
	tasks, err := m.store.List(store.ListOptions{})
	if err != nil {
		return fmt.Errorf("list all tasks: %w", err)
	}
	m.allTasks = tasks
	m.stats = model.ComputeStats(tasks, time.Now())
	return nil
}

// rebuildForTagView switches the model to tag view mode. It scans all tasks,
// builds tag tabs, recreates list widgets, and reloads data.
func (m *Model) rebuildForTagView() error {
	// Load all tasks (open + done) to derive tags.
	allTasks, err := m.store.List(store.ListOptions{})
	if err != nil {
		return fmt.Errorf("list tasks for tag view: %w", err)
	}

	m.viewMode = viewTag
	m.tagTabs = buildTagTabs(allTasks)

	// Convert tagTabs to generic tabs.
	m.tabs = make([]tab, len(m.tagTabs))
	for i, tt := range m.tagTabs {
		m.tabs[i] = tab{label: tt.label, bucket: "", status: "open"}
	}

	// Clamp activeTab to the new tab count.
	if m.activeTab >= len(m.tabs) {
		m.activeTab = 0
	}

	m.lists = m.initLists()
	if err := m.reloadAll(); err != nil {
		return err
	}
	// After reload, the cursor may rest on a separator at index 0. Skip
	// forward so the first selectable item is highlighted.
	m.skipSeparator(0)
	return nil
}

// rebuildForScheduleView switches the model back to schedule view mode.
// Restores default tabs, recreates list widgets, and reloads data.
func (m *Model) rebuildForScheduleView() error {
	m.viewMode = viewSchedule
	m.tagTabs = nil
	m.tabs = defaultTabs

	// Clamp activeTab to the new tab count.
	if m.activeTab >= len(m.tabs) {
		m.activeTab = 0
	}

	m.lists = m.initLists()
	return m.reloadAll()
}

// toggleViewMode switches between schedule view and tag view, preserving
// the cursor on the currently selected task (if it exists in the new view).
func (m *Model) toggleViewMode() tea.Cmd {
	// Remember the currently selected task ID for refocusing after the switch.
	var focusID string
	if task := m.selectedTask(); task != nil {
		focusID = task.ID
	}

	var err error
	if m.viewMode == viewSchedule {
		err = m.rebuildForTagView()
	} else {
		err = m.rebuildForScheduleView()
	}
	if err != nil {
		m.err = err
		return nil
	}

	m.recomputeLayout()

	// Try to refocus the previously selected task in the new view.
	if focusID != "" {
		m.focusTaskByID(focusID)
	}
	return nil
}

// selectedTask returns a pointer to the currently selected task in the
// active tab, or nil if the tab is empty or the selected item is a
// separator (bucket heading).
func (m *Model) selectedTask() *model.Task {
	sel := m.lists[m.activeTab].SelectedItem()
	if sel == nil {
		return nil
	}
	it := sel.(item)
	if it.isSeparator {
		return nil
	}
	return &it.task
}

// skipSeparator moves the cursor off a separator item. dir indicates the
// preferred direction (+1 down, -1 up); 0 defaults to +1.
func (m *Model) skipSeparator(dir int) {
	if dir == 0 {
		dir = 1
	}
	m.lists[m.activeTab].SkipSeparator(dir)
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
		// After reload/focus, the cursor may rest on a separator in tag view.
		if m.viewMode == viewTag {
			m.skipSeparator(0)
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
		case modeHelp:
			m.mode = modeNormal
			return m, nil
		case modeSearch:
			return m.updateSearch(msg)
		default:
			return m.updateModal(msg)
		}
	}
	return m, nil
}

// updateNormal handles keys when no modal is open.
func (m *Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.statusMsg = ""

	// When the detail panel is open, the note textarea owns the keyboard so
	// notes can start with any letter (including action-key shortcuts like
	// d/q/t). Esc closes the panel; Enter submits; Alt+Enter inserts a
	// newline. Non-rune keys (arrows, Home/End, PgUp/PgDn, Tab) fall through
	// for task/tab navigation. [/] still scroll the detail body.
	if m.detailOpen {
		switch msg.Type {
		case tea.KeyEsc:
			m.closeDetailPanel()
			return m, nil
		case tea.KeyEnter:
			if msg.Alt {
				var cmd tea.Cmd
				m.noteArea, cmd = m.noteArea.Update(msg)
				return m, cmd
			}
			return m, m.submitNote()
		}

		if msg.Type == tea.KeyRunes {
			switch string(msg.Runes) {
			case "[", "]":
				// Fall through for body scroll.
			default:
				var cmd tea.Cmd
				m.noteArea, cmd = m.noteArea.Update(msg)
				return m, cmd
			}
		}
		if msg.Type == tea.KeySpace || msg.Type == tea.KeyBackspace {
			var cmd tea.Cmd
			m.noteArea, cmd = m.noteArea.Update(msg)
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "left", "shift+tab":
		m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
		m.recomputeLayout()
		if m.viewMode == viewTag {
			m.skipSeparator(0)
		}
		m.detailScroll = 0
		return m, nil
	case "right", "tab":
		m.activeTab = (m.activeTab + 1) % len(m.tabs)
		m.recomputeLayout()
		if m.viewMode == viewTag {
			m.skipSeparator(0)
		}
		m.detailScroll = 0
		return m, nil
	case "1", "2", "3", "4", "5", "6":
		n := int(msg.String()[0] - '1')
		if n < len(m.tabs) {
			m.activeTab = n
			m.recomputeLayout()
			if m.viewMode == viewTag {
				m.skipSeparator(0)
			}
			m.detailScroll = 0
		}
		return m, nil

	case "enter":
		if !m.detailOpen {
			m.openDetailPanel()
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
	case "v":
		return m, m.toggleViewMode()
	case "h":
		m.mode = modeHelp
		return m, nil
	case "/":
		m.openSearch()
		return m, nil
	}

	// Cursor navigation — handled by vlist directly.
	vl := m.lists[m.activeTab]
	switch msg.String() {
	case "j", "down":
		vl.CursorDown()
		m.detailScroll = 0 // reset scroll when navigating tasks
	case "k", "up":
		vl.CursorUp()
		m.detailScroll = 0 // reset scroll when navigating tasks
	case "pgdown":
		vl.PageDown()
		m.detailScroll = 0
	case "pgup":
		vl.PageUp()
		m.detailScroll = 0
	case "g", "home":
		vl.GoToStart()
		m.detailScroll = 0
	case "G", "end":
		vl.GoToEnd()
		m.detailScroll = 0
	case "]":
		// Scroll detail panel body down.
		if m.detailOpen {
			m.detailScroll++
		}
	case "[":
		// Scroll detail panel body up.
		if m.detailOpen && m.detailScroll > 0 {
			m.detailScroll--
		}
	}
	return m, nil
}

// openDetailPanel opens the detail panel and focuses the note textarea.
func (m *Model) openDetailPanel() {
	task := m.selectedTask()
	if task == nil {
		return
	}
	m.detailOpen = true
	m.detailScroll = 0
	m.noteArea.Reset()
	m.noteArea.Focus()
	m.recomputeLayout()
}

// closeDetailPanel closes the detail panel and returns focus to the list.
func (m *Model) closeDetailPanel() {
	m.detailOpen = false
	m.detailScroll = 0
	m.noteArea.Blur()
	m.noteArea.Reset()
	m.recomputeLayout()
}

// updateModal routes keys based on the current modal.
func (m *Model) updateModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Esc: when tag suggestions are visible, clear them instead of closing the
	// modal. Let the modal handler decide.
	if msg.Type == tea.KeyEsc {
		if len(m.suggestions) > 0 && (m.mode == modeAdd || m.mode == modeRetag) {
			m.suggestions = nil
			m.suggestionIdx = -1
			return m, nil
		}
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
	m.recurInput.Blur()
	m.recurInput.SetValue("")
	m.titleArea.Blur()
	m.titleArea.Reset()
	m.titleArea.SetHeight(1)
	m.addFocus = addFocusTitle
	m.suggestions = nil
	m.suggestionIdx = -1
}

// --- note submission -------------------------------------------------------

// submitNote appends the noteArea's text to the selected task's Body, saves,
// and auto-commits. NoteCount is recalculated from Body inside Store.Update,
// so callers no longer touch it. Returns nil (no-op) when the textarea is
// empty or no task is selected.
func (m *Model) submitNote() tea.Cmd {
	text := strings.TrimSpace(m.noteArea.Value())
	if text == "" {
		return nil
	}
	task := m.selectedTask()
	if task == nil {
		return nil
	}
	t := *task
	nowT := time.Now()
	t.Body = model.AppendNote(t.Body, text, nowT, config.DateFormat())
	t.UpdatedAt = nowT.UTC().Format(time.RFC3339)
	flat := flattenTitle(t.Title)

	// Clear and reset the textarea immediately so the UI feels responsive.
	m.noteArea.Reset()

	return m.saveCmd(t, fmt.Sprintf("note: %s", flat), fmt.Sprintf("Note added: %s", flat))
}

// --- search overlay --------------------------------------------------------
//
// openSearch / closeSearch / updateSearch / renderSearch live in search.go so
// the mode logic stays close to the ranker.

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
	flat := flattenTitle(t.Title)
	storeRef := m.store
	repoPath := m.repoPath
	return func() tea.Msg {
		// warnBuf collects any recurrence warnings surfaced through the
		// status bar (stderr has no natural home in the TUI).
		var warnBuf strings.Builder
		commitMsg, commitFiles, err := recurrence.CompleteAndSpawn(storeRef, &t, time.Now(), &warnBuf, config.DateFormat())
		if err != nil {
			return taskSavedMsg{err: fmt.Errorf("done: %w", err)}
		}
		if err := git.AutoCommit(repoPath, commitMsg, commitFiles...); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		status := fmt.Sprintf("Completed: %s", flat)
		if w := strings.TrimSpace(warnBuf.String()); w != "" {
			status = fmt.Sprintf("Completed: %s (%s)", flat, w)
		} else if len(commitFiles) > 1 {
			// Spawn happened — surface the next-occurrence date in the
			// status line so the user sees the recurrence worked.
			status = fmt.Sprintf("Completed: %s (next occurrence spawned)", flat)
		}
		return taskSavedMsg{status: status}
	}
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
	flat := flattenTitle(t.Title)
	return m.saveCmd(t, fmt.Sprintf("edit: %s", flat), fmt.Sprintf("%s: %s", label, flat))
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
			m.input.Width = m.modalInnerWidth() - 1
			m.input.Placeholder = config.DateFormatLabel()
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		}
		return m, nil
	}

	// Custom date input step. schedule.Parse accepts bucket names, the
	// configured format, and legacy ISO input (silent backward compat) —
	// we rely on it for validation and normalization rather than IsISODate
	// so the modal automatically follows the configured format.
	switch msg.Type {
	case tea.KeyEnter:
		date := strings.TrimSpace(m.input.Value())
		if _, err := schedule.Parse(date, time.Now(), config.DateFormat()); err != nil {
			m.err = fmt.Errorf("invalid date %q (want %s)", date, config.DateFormatLabel())
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
	scheduleDate, err := schedule.Parse(sched, nowT, config.DateFormat())
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
	flat := flattenTitle(t.Title)
	return m.saveCmd(t, fmt.Sprintf("reschedule: %s -> %s", flat, sched),
		fmt.Sprintf("Rescheduled: %s -> %s", flat, sched))
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
	// Subtract 1 for the trailing cursor space that textinput appends.
	m.input.Width = m.modalInnerWidth() - 1
	m.input.Placeholder = "tag1, tag2"
	// Filter out the reserved active tag so the user only sees their own tags.
	// Active state is preserved separately via wasActive/SetActive in updateRetag.
	m.input.SetValue(strings.Join(display.VisibleTags(t.Tags), ", "))
	m.input.CursorEnd()
	m.input.Focus()
	// Cache known tags for autocomplete suggestions.
	m.knownTags = model.CollectTags(m.allTasks)
	m.suggestions = nil
	m.suggestionIdx = -1
	return textinput.Blink
}

func (m *Model) updateRetag(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Delegate Up/Down/Tab/Enter to shared suggestion navigation.
	if m.handleSuggestionNav(msg, &m.input) {
		return m, nil
	}

	// Enter: submit retag (suggestion case already handled above).
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
		flat := flattenTitle(t.Title)
		return m, m.saveCmd(t,
			fmt.Sprintf("edit: %s", flat),
			fmt.Sprintf("Retagged: %s", flat))
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Refresh suggestions after every keystroke in the tag field.
	m.refreshSuggestions(m.input.Value())
	return m, cmd
}

// --- add modal -------------------------------------------------------------

func (m *Model) openAdd() tea.Cmd {
	m.mode = modeAdd
	// "Title: ", "Tags:  ", and "Recur: " labels are each 7 chars; the
	// textinput also renders a trailing cursor space (1 char beyond its
	// configured Width), so subtract 8 total.
	inputW := m.modalInnerWidth() - 8
	m.titleArea.SetWidth(inputW)
	m.titleArea.SetHeight(1)
	m.tagInput.Width = inputW
	m.recurInput.Width = inputW
	m.titleArea.Reset()
	m.titleArea.Focus()
	// In tag view, prepopulate the tag field with the current tab's tag.
	if m.viewMode == viewTag {
		tt := m.tagTabs[m.activeTab]
		if !tt.isActive && !tt.isUntagged {
			m.tagInput.SetValue(tt.tag)
		} else {
			m.tagInput.SetValue("")
		}
	} else {
		m.tagInput.SetValue("")
	}
	m.tagInput.Blur()
	m.recurInput.SetValue("")
	m.recurInput.Blur()
	m.addFocus = addFocusTitle
	// Cache known tags from all loaded list items for instant auto-tag on ":".
	m.knownTags = model.CollectTags(m.allTasks)
	m.suggestions = nil
	m.suggestionIdx = -1
	return textinput.Blink
}

func (m *Model) updateAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	tagsFocused := m.addFocus == addFocusTags
	recurFocused := m.addFocus == addFocusRecur

	// Delegate suggestion navigation to shared helper when tag field is focused.
	if tagsFocused && m.handleSuggestionNav(msg, &m.tagInput) {
		return m, nil
	}

	// Tab: cycle focus title -> tags -> recur -> title.
	if msg.Type == tea.KeyTab {
		switch m.addFocus {
		case addFocusTitle:
			m.addFocus = addFocusTags
			m.titleArea.Blur()
			m.tagInput.Focus()
			m.recurInput.Blur()
		case addFocusTags:
			m.addFocus = addFocusRecur
			m.tagInput.Blur()
			m.recurInput.Focus()
			m.titleArea.Blur()
			// Clear suggestions when leaving the tag field — they are not
			// interactable from the recur field.
			m.suggestions = nil
			m.suggestionIdx = -1
		default: // addFocusRecur
			m.addFocus = addFocusTitle
			m.recurInput.Blur()
			m.tagInput.Blur()
			m.titleArea.Focus()
			m.suggestions = nil
			m.suggestionIdx = -1
		}
		return m, textinput.Blink
	}

	// Enter: submit (plain Enter) or insert newline (Alt+Enter).
	// Suggestion acceptance is handled by handleSuggestionNav above.
	if msg.Type == tea.KeyEnter && !msg.Alt {
		title := sanitizeTitle(m.titleArea.Value())
		if title == "" {
			m.closeModal()
			return m, nil
		}
		recur, err := recurrence.Canonicalize(strings.TrimSpace(m.recurInput.Value()))
		if err != nil {
			m.err = fmt.Errorf("recurrence: %w", err)
			return m, nil
		}
		tags := model.SanitizeTags(m.tagInput.Value())
		m.closeModal()
		return m, m.createCmd(title, tags, recur)
	}

	// Route remaining keys to the focused input only.
	var cmd tea.Cmd
	if recurFocused {
		m.recurInput, cmd = m.recurInput.Update(msg)
	} else if tagsFocused {
		m.tagInput, cmd = m.tagInput.Update(msg)
		// Refresh suggestions after every keystroke in the tag field.
		m.refreshSuggestions(m.tagInput.Value())
	} else {
		// Pre-grow the textarea height before the update so the viewport
		// never needs to scroll. Without this, Update()'s internal
		// repositionView sees a too-small viewport and scrolls the top
		// line out of view. After the update we trim to the exact count.
		m.titleArea.SetHeight(textareaVisualHeight(m.titleArea) + 1)
		m.titleArea, cmd = m.titleArea.Update(msg)
		m.titleArea.SetHeight(textareaVisualHeight(m.titleArea) + 1)
		// After updating the title input, check if a known tag prefix was typed.
		// Auto-populate the tags field on ":" so the user gets instant feedback.
		if autoTag := model.ParseTitleTag(m.titleArea.Value(), m.knownTags); autoTag != "" {
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

// handleSuggestionNav handles Up/Down/Tab/Enter keys when suggestions are
// visible. It returns true when the key was consumed (caller should return
// early).
func (m *Model) handleSuggestionNav(msg tea.KeyMsg, ti *textinput.Model) bool {
	hasSuggestions := len(m.suggestions) > 0

	// Up/Down navigate suggestions.
	if hasSuggestions && (msg.Type == tea.KeyUp || msg.Type == tea.KeyDown) {
		if msg.Type == tea.KeyUp {
			if m.suggestionIdx > 0 {
				m.suggestionIdx--
			}
		} else {
			if m.suggestionIdx < len(m.suggestions)-1 {
				m.suggestionIdx++
			}
		}
		return true
	}

	// Tab/Enter: accept suggestion if visible with a selection.
	if (msg.Type == tea.KeyTab || msg.Type == tea.KeyEnter) && hasSuggestions && m.suggestionIdx >= 0 {
		m.acceptSuggestion(ti)
		return true
	}

	return false
}

// refreshSuggestions updates m.suggestions from knownTags based on the current
// tag field value. Resets suggestionIdx to 0 if there are results, -1 otherwise.
func (m *Model) refreshSuggestions(fieldValue string) {
	m.suggestions = model.FilterTags(m.knownTags, fieldValue)
	if len(m.suggestions) > 0 {
		m.suggestionIdx = 0
	} else {
		m.suggestionIdx = -1
	}
}

// acceptSuggestion replaces the current fragment in the given textinput with
// the selected suggestion, appending ", " so the user can continue typing.
func (m *Model) acceptSuggestion(ti *textinput.Model) {
	if m.suggestionIdx < 0 || m.suggestionIdx >= len(m.suggestions) {
		return
	}
	selected := m.suggestions[m.suggestionIdx]
	val := ti.Value()
	// Find the last comma to locate the fragment being replaced.
	lastComma := strings.LastIndex(val, ",")
	var prefix string
	if lastComma >= 0 {
		prefix = val[:lastComma+1] + " "
	}
	ti.SetValue(prefix + selected + ", ")
	ti.CursorEnd()
	// Recompute suggestions (fragment is now empty after ", " so they'll clear).
	m.refreshSuggestions(ti.Value())
}

// renderSuggestions returns a string with suggestion lines to embed in a modal
// view. Returns "" when there are no suggestions. Each suggestion is indented
// 7 chars to align with the tag value; the highlighted item is prefixed with
// "> " and styled bold, others are prefixed with "  " and styled dim.
func (m *Model) renderSuggestions() string {
	if len(m.suggestions) == 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range m.suggestions {
		b.WriteByte('\n')
		if i == m.suggestionIdx {
			b.WriteString("       " + suggestionBoldStyle.Render("> "+s))
		} else {
			b.WriteString("       " + suggestionDimStyle.Render("  "+s))
		}
	}
	return b.String()
}

// sanitizeTitle trims surrounding whitespace but preserves interior newlines
// inserted via Alt+Enter. Each line is individually trimmed and blank lines
// are dropped so stray empty lines don't bloat the stored title.
func sanitizeTitle(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
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
	Title      string   `yaml:"title"`
	Body       string   `yaml:"body,omitempty"`
	Schedule   string   `yaml:"schedule"`
	Tags       []string `yaml:"tags,omitempty"`
	Recurrence string   `yaml:"recurrence,omitempty"`
}

// marshalTaskForEdit renders a task into the YAML shown in the editor.
// The reserved "active" tag is filtered out so the user only sees their own
// tags; active state is preserved separately in applyEditedYAML. The
// schedule field is rendered through FormatDisplay so the user sees
// dates in the configured format (default DD-MM-YYYY); applyEditedYAML
// round-trips it back to the stored ISO format via schedule.Parse.
func marshalTaskForEdit(t model.Task) ([]byte, error) {
	return yaml.Marshal(editableFields{
		Title:      t.Title,
		Body:       t.Body,
		Schedule:   schedule.FormatDisplay(t.Schedule, config.DateFormat()),
		Tags:       display.VisibleTags(t.Tags),
		Recurrence: t.Recurrence,
	})
}

// applyEditedYAML parses the user's edited YAML and returns an updated task.
// Returns an error if the YAML is invalid, the title is empty, the schedule
// is not parseable, or the recurrence rule is not parseable. Bucket-name
// schedules are converted to ISO dates so the on-disk file always stores a
// concrete date; the recurrence rule is canonicalized (e.g. weekly:Monday ->
// weekly:mon) so edit round-trips always produce a normalized value.
func applyEditedYAML(orig model.Task, data []byte, now time.Time) (model.Task, error) {
	var edit editableFields
	if err := yaml.Unmarshal(data, &edit); err != nil {
		return model.Task{}, fmt.Errorf("parse YAML: %w", err)
	}
	edit.Title = strings.TrimSpace(edit.Title)
	edit.Schedule = strings.TrimSpace(edit.Schedule)
	edit.Recurrence = strings.TrimSpace(edit.Recurrence)
	if edit.Title == "" {
		return model.Task{}, fmt.Errorf("title cannot be empty")
	}
	scheduleDate, err := schedule.Parse(edit.Schedule, now, config.DateFormat())
	if err != nil {
		return model.Task{}, err
	}
	recurCanonical, err := recurrence.Canonicalize(edit.Recurrence)
	if err != nil {
		return model.Task{}, err
	}
	out := orig
	wasActive := orig.IsActive()
	out.Title = edit.Title
	out.Body = edit.Body
	out.Schedule = scheduleDate
	out.Tags = edit.Tags
	out.Recurrence = recurCanonical
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
		flat := flattenTitle(updated.Title)
		if err := git.AutoCommit(repoPath, fmt.Sprintf("edit: %s", flat), taskRelPath(updated.ID)); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		return taskSavedMsg{status: fmt.Sprintf("Edited: %s", flat)}
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
		if m.viewMode == viewTag {
			return m, nil // no cross-tab movement in tag view
		}
		m.moveGrabAcross(-1)
	case "right":
		if m.viewMode == viewTag {
			return m, nil // no cross-tab movement in tag view
		}
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
// delta is -1 (up) or +1 (down). No-op at the boundary. In tag view, separator
// items are skipped — the grabbed task jumps over them to the next non-separator
// item in the same direction.
func (m *Model) moveGrabWithin(delta int) {
	items := m.lists[m.activeTab].Items()
	target := m.grabIndex + delta
	if target < 0 || target >= len(items) {
		return
	}

	// In tag view, skip over separator items.
	if m.viewMode == viewTag {
		if it, ok := items[target].(item); ok && it.isSeparator {
			// Jump past the separator to the next non-separator item.
			target += delta
			if target < 0 || target >= len(items) {
				return
			}
			// If the next item is also a separator (shouldn't happen normally),
			// give up rather than continuing indefinitely.
			if it2, ok := items[target].(item); ok && it2.isSeparator {
				return
			}
			// Remove grabbed item from its current position and insert at target.
			grabbed := items[m.grabIndex]
			items = append(items[:m.grabIndex], items[m.grabIndex+1:]...)
			// Adjust target after removal.
			if target > m.grabIndex {
				target--
			}
			items = append(items[:target], append([]list.Item{grabbed}, items[target:]...)...)
			m.lists[m.activeTab].SetItems(items)
			m.grabIndex = target
			m.lists[m.activeTab].Select(m.grabIndex)
			return
		}
	}

	items[m.grabIndex], items[target] = items[target], items[m.grabIndex]
	m.lists[m.activeTab].SetItems(items)
	m.grabIndex = target
	m.lists[m.activeTab].Select(m.grabIndex)
}

// moveGrabTo moves the grabbed task to a specific index in the current tab.
// In tag view, the target index is adjusted to skip separator items.
func (m *Model) moveGrabTo(idx int) {
	items := m.lists[m.activeTab].Items()
	if idx < 0 || idx >= len(items) || idx == m.grabIndex {
		return
	}

	// In tag view, skip separator items at the target. Scan forward for
	// g (top) and backward for G (bottom).
	if m.viewMode == viewTag {
		if idx < m.grabIndex {
			// Moving toward the top: skip separators forward.
			for idx < len(items) {
				if it, ok := items[idx].(item); ok && it.isSeparator {
					idx++
				} else {
					break
				}
			}
		} else {
			// Moving toward the bottom: skip separators backward.
			for idx >= 0 {
				if it, ok := items[idx].(item); ok && it.isSeparator {
					idx--
				} else {
					break
				}
			}
		}
		if idx < 0 || idx >= len(items) || idx == m.grabIndex {
			return
		}
	}

	grabbed := items[m.grabIndex]
	// Remove, then insert at target.
	items = append(items[:m.grabIndex], items[m.grabIndex+1:]...)
	// NOTE: when grabIndex < idx, the removal shifts items after grabIndex
	// down by one, so `idx` effectively refers to one position past the
	// original item at that index. For the current callers (g→idx=0 and
	// G→idx=len-1) this is benign: g always has idx < grabIndex, and G
	// relies on the clamp below to land at the very end.
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

	if m.viewMode == viewTag {
		// In tag view, derive the schedule and status from the separator above.
		bucket := m.findBucketAbove()
		if bucket == "done" {
			// Moved into the Done section.
			t.Status = "done"
			t.SetActive(false)
		} else {
			t.Status = "open"
			if bucket != "" {
				if schedule.Bucket(t.Schedule, nowT) != bucket {
					scheduleDate, err := schedule.Parse(bucket, nowT, config.DateFormat())
					if err == nil {
						t.Schedule = scheduleDate
					}
				} else {
					t.Schedule = schedule.Normalize(t.Schedule, nowT)
				}
			}
		}
	} else {
		// Schedule view: apply the target tab's semantics. For bucket tabs,
		// write the bucket's canonical date. The Done tab leaves Schedule alone.
		if targetTab.bucket != "" {
			// Only rewrite Schedule when the task isn't already in the target
			// bucket, otherwise dropping back into your current bucket would
			// reset a custom-set date (e.g. a 3-day-out task in the Week tab).
			if schedule.Bucket(t.Schedule, nowT) != targetTab.bucket {
				scheduleDate, err := schedule.Parse(targetTab.bucket, nowT, config.DateFormat())
				if err == nil {
					t.Schedule = scheduleDate
				}
			} else {
				// Lazy-migrate any legacy bucket string to ISO on this write.
				t.Schedule = schedule.Normalize(t.Schedule, nowT)
			}
		}
	}
	// In tag view the status was already set from the separator above.
	// In schedule view derive it from the target tab.
	if m.viewMode != viewTag {
		t.Status = targetTab.status
		if t.Status == "" {
			t.Status = "open"
		}
		if t.Status == "done" {
			t.SetActive(false)
		}
	}
	if t.Status == "open" {
		t.CompletedAt = ""
	}
	t.UpdatedAt = now()

	// Compute the new Position from the grabbed task's in-memory neighbors
	// within the destination tab. Done tasks have no meaningful ordering
	// (sorted by UpdatedAt), but we still keep a sane Position value.
	if t.Status != "done" {
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
		flat := flattenTitle(t.Title)
		if err := git.AutoCommit(repoPath, fmt.Sprintf("move: %s", flat), taskRelPath(t.ID)); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		return taskSavedMsg{status: fmt.Sprintf("Moved: %s", flat), focusID: t.ID}
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
		it := items[i].(item)
		if it.isSeparator {
			break // stop at bucket boundary
		}
		if it.task.ID == grabbedID {
			continue
		}
		t := it.task
		prev = &t
		break
	}
	for i := grabIdx + 1; i < len(items); i++ {
		it := items[i].(item)
		if it.isSeparator {
			break // stop at bucket boundary
		}
		if it.task.ID == grabbedID {
			continue
		}
		t := it.task
		next = &t
		break
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
// recur is an optional recurrence rule (empty = non-recurring); callers are
// expected to have already validated it via recurrence.Parse.
func (m *Model) createCmd(title string, tags []string, recur string) tea.Cmd {
	// Capture bucket at dispatch time; if user changes tabs while the cmd
	// is in flight, we still place it in the intended bucket.
	var bucket string
	if m.viewMode == viewTag {
		// In tag view, derive bucket from the separator above the cursor.
		bucket = m.bucketAtCursor()
		if bucket == "" || bucket == "done" {
			bucket = schedule.Today
		}
	} else {
		bucket = m.tabs[m.activeTab].bucket
		// "Add" on the Done tab doesn't really make sense; fall back to today.
		if bucket == "" {
			bucket = schedule.Today
		}
	}
	storeRef := m.store
	repoPath := m.repoPath
	return func() tea.Msg {
		nowT := time.Now()
		scheduleDate, err := schedule.Parse(bucket, nowT, config.DateFormat())
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
			ID:         id,
			Title:      title,
			Source:     "tui",
			Status:     "open",
			Position:   ordering.NextPosition(siblings),
			Schedule:   scheduleDate,
			Tags:       tags,
			Recurrence: recur,
			CreatedAt:  n,
			UpdatedAt:  n,
		}
		if err := storeRef.Create(task); err != nil {
			return taskSavedMsg{err: fmt.Errorf("create: %w", err)}
		}
		flat := flattenTitle(title)
		if err := git.AutoCommit(repoPath, fmt.Sprintf("add: %s", flat), taskRelPath(id)); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		return taskSavedMsg{status: fmt.Sprintf("Added: %s", flat), focusID: id}
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
		flat := flattenTitle(task.Title)
		if err := git.AutoCommit(m.repoPath, fmt.Sprintf("rm: %s", flat), taskRelPath(task.ID)); err != nil {
			return taskSavedMsg{err: fmt.Errorf("commit: %w", err)}
		}
		return taskSavedMsg{status: fmt.Sprintf("Deleted: %s", flat)}
	}
}

// --- active panel ----------------------------------------------------------

// activePanelView renders a bordered panel listing each active task. Returns
// an empty string when there are no active tasks (auto-hide) or when in tag
// view mode (active tasks have their own tab there).
func (m *Model) activePanelView() string {
	if m.viewMode == viewTag || len(m.activeTasks) == 0 {
		return ""
	}
	titleWidth := m.width - activePanelChromeWidth
	var lines []string
	for _, t := range m.activeTasks {
		titleLines := wrapText(t.Title, titleWidth)
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
// 0 when there are no active tasks (panel hidden) or in tag view mode. Mirrors
// the wrapping logic in activePanelView so the two never drift apart.
// Structure: 1 top border + content lines + 1 bottom border.
func (m *Model) activePanelHeight() int {
	if m.viewMode == viewTag || len(m.activeTasks) == 0 {
		return 0
	}
	titleWidth := m.width - activePanelChromeWidth
	if titleWidth <= 0 {
		return len(m.activeTasks) + 2
	}
	contentLines := 0
	for _, t := range m.activeTasks {
		contentLines += len(wrapText(t.Title, titleWidth))
	}
	return contentLines + 2
}

// statsBarHeight always returns 1. The stats bar is always shown.
func (m *Model) statsBarHeight() int { return 1 }

// statsBarView renders a compact single-line summary of task statistics.
// Overall stats and tab-specific stats are separated by a "|" divider.
// Schedule view: "45 tasks  32 open  13 done  ~4d open  ~12d done  |  8 in tab"
// Tag view (non-Active tab): "…  |  5 tag-done  8 in tab"
// Tag view (Active tab): "…  |  8 in tab"
func (m *Model) statsBarView() string {
	s := m.stats

	// Overall stats: totals and averages.
	overall := []string{
		fmt.Sprintf("%d tasks", s.Total),
		fmt.Sprintf("%d open", s.Open),
		fmt.Sprintf("%d done", s.Done),
	}
	if s.AvgDaysOpen > 0 {
		overall = append(overall, "~"+model.FormatDuration(s.AvgDaysOpen)+" open")
	}
	if s.AvgDaysToComplete > 0 {
		overall = append(overall, "~"+model.FormatDuration(s.AvgDaysToComplete)+" done")
	}

	// Tab-specific stats: tag-done (tag view, non-Active tabs) and in-tab count.
	var tabParts []string
	if m.viewMode == viewTag && m.activeTab < len(m.tagTabs) {
		tt := m.tagTabs[m.activeTab]
		if !tt.isActive {
			tagDone := 0
			for _, t := range m.allTasks {
				if t.Status != "done" {
					continue
				}
				switch {
				case tt.isUntagged:
					if len(display.VisibleTags(t.Tags)) == 0 {
						tagDone++
					}
				default:
					for _, tag := range t.Tags {
						if tag == tt.tag {
							tagDone++
							break
						}
					}
				}
			}
			tabParts = append(tabParts, fmt.Sprintf("%d tag-done", tagDone))
		}
	}

	// Count non-separator items in the current tab list.
	tabCount := 0
	if m.activeTab < len(m.lists) {
		for _, it := range m.lists[m.activeTab].Items() {
			if i, ok := it.(item); ok && !i.isSeparator {
				tabCount++
			}
		}
	}
	tabParts = append(tabParts, fmt.Sprintf("%d in tab", tabCount))

	line := strings.Join(overall, "  ") + "  |  " + strings.Join(tabParts, "  ")
	return helpTextStyle.Padding(0, 1).Render(line)
}

// detailPanelWidth returns the width allocated to the detail panel when it is
// open. Returns 0 when the panel is closed. Uses ~45% of terminal width.
func (m *Model) detailPanelWidth() int {
	if !m.detailOpen {
		return 0
	}
	w := m.width * 45 / 100
	if w < 30 {
		w = 30
	}
	if w > m.width-20 {
		w = m.width - 20
	}
	if w < 0 {
		w = 0
	}
	return w
}

// listWidth returns the width allocated to the task list. When the detail panel
// is open, the list is narrowed to make room.
func (m *Model) listWidth() int {
	return m.width - m.detailPanelWidth()
}

// detailPanelInnerWidth returns the usable content width inside the detail
// panel, accounting for border (2) and padding (4). Returns at least 10.
func (m *Model) detailPanelInnerWidth() int {
	iw := m.detailPanelWidth() - 6
	if iw < 10 {
		iw = 10
	}
	return iw
}

// recomputeLayout recalculates list sizes based on the current terminal
// dimensions and active panel height. Called from WindowSizeMsg and after
// any mutation that might change the panel (taskSavedMsg).
func (m *Model) recomputeLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	listH := m.height - 4 - m.activePanelHeight() - m.statsBarHeight()
	if listH < 3 {
		listH = 3
	}
	lw := m.listWidth()
	for i := range m.lists {
		m.lists[i].SetSize(lw, listH)
	}
	// Resize the note textarea to fit inside the detail panel.
	if m.detailOpen {
		m.noteArea.SetWidth(m.detailPanelInnerWidth())
	}
	// Keep textinput widths in sync when the terminal is resized while a modal
	// is open, so the fixed-width border doesn't reflow on the next render.
	// See openAdd / openRetag for the -8 / -1 explanations (label + cursor).
	switch m.mode {
	case modeAdd:
		inputW := m.modalInnerWidth() - 8
		m.titleArea.SetWidth(inputW)
		m.tagInput.Width = inputW
		m.recurInput.Width = inputW
	case modeRetag, modeReschedule:
		m.input.Width = m.modalInnerWidth() - 1
	case modeSearch:
		// Leave room for the "> " prompt (2) plus a few columns for the
		// right-aligned count counter added by renderSearch in Task 6.
		w := m.width - searchInputReserve
		if w < 10 {
			w = 10
		}
		m.search.input.Width = w
	}
}

// --- View ------------------------------------------------------------------

// detailPanelView renders the right-side detail panel for the currently
// selected task. Returns an empty string when the panel is closed or no task
// is selected.
//
// Layout (top to bottom, all within a bordered box):
//   - Title (bold)
//   - Schedule
//   - Tags (if any)
//   - Created date (and Completed date if done)
//   - Blank line
//   - Body text (scrollable via detailScroll)
//   - Separator line
//   - Note textarea
func (m *Model) detailPanelView() string {
	if !m.detailOpen {
		return ""
	}
	task := m.selectedTask()
	if task == nil {
		return ""
	}

	pw := m.detailPanelWidth()
	iw := m.detailPanelInnerWidth()

	now := time.Now()

	// --- header section ---
	titleStyle := lipgloss.NewStyle().Bold(true)
	var header []string
	header = append(header, titleStyle.Render(truncateTitle(task.Title, iw)))

	bucket := schedule.Bucket(task.Schedule, now)
	displayDate := schedule.FormatDisplay(task.Schedule, config.DateFormat())
	if bucket != task.Schedule {
		header = append(header, fmt.Sprintf("Schedule: %s (%s)", bucket, displayDate))
	} else {
		header = append(header, "Schedule: "+displayDate)
	}

	if vt := display.VisibleTags(task.Tags); len(vt) > 0 {
		header = append(header, "Tags: "+strings.Join(vt, ", "))
	}

	layout := config.DateFormat()
	created := display.FormatRelDate(now, task.CreatedAt, layout)
	if created != "" {
		dateLine := "Created: " + created
		if task.CompletedAt != "" {
			completed := display.FormatRelDate(now, task.CompletedAt, layout)
			if completed != "" {
				dateLine += "  Completed: " + completed
			}
		}
		header = append(header, dateLine)
	}

	// --- body section (scrollable) ---
	// Calculate how many lines we have for the body area.
	// Total panel height = list height (from vlist) + 2 for tab bar line + 1 for help line.
	// The body gets whatever is left after header, separator, and textarea.
	listH := 0
	if len(m.lists) > 0 {
		listH = m.lists[0].height
	}
	// Panel content area height matches the list height (tab bar and help line
	// offset the border cost).
	panelContentH := listH
	if panelContentH < 5 {
		panelContentH = 5
	}
	headerLines := len(header)
	noteAreaH := m.noteArea.Height() + 1                 // +1 for the separator line above
	bodyH := panelContentH - headerLines - 1 - noteAreaH // -1 for blank line between header and body
	if bodyH < 1 {
		bodyH = 1
	}

	var bodyLines []string
	if task.Body != "" {
		bodyLines = wrapText(task.Body, iw)
	}

	// Clamp scroll offset to valid range so we never wrap to top.
	if m.detailScroll >= len(bodyLines) {
		m.detailScroll = max(0, len(bodyLines)-1)
	}
	// Apply scroll offset.
	if m.detailScroll > 0 && m.detailScroll < len(bodyLines) {
		bodyLines = bodyLines[m.detailScroll:]
	}

	// Truncate to fit available body height.
	if len(bodyLines) > bodyH {
		bodyLines = bodyLines[:bodyH]
	}

	// --- assemble panel content ---
	var parts []string
	parts = append(parts, strings.Join(header, "\n"))
	parts = append(parts, "") // blank line between header and body
	if len(bodyLines) > 0 {
		parts = append(parts, strings.Join(bodyLines, "\n"))
	}
	sepLine := strings.Repeat("─", iw)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	parts = append(parts, dimStyle.Render(sepLine))
	parts = append(parts, m.noteArea.View())

	content := strings.Join(parts, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(0, 2).
		Width(pw - 2). // subtract border columns
		Render(content)
}

func (m *Model) View() string {
	// Search overlay owns the full screen: render and return before computing
	// anything specific to the normal-mode layout (tab bar, panels, etc).
	if m.mode == modeSearch {
		return m.renderSearch()
	}

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
		body = m.lists[m.activeTab].Render(m.renderListItem)
	} else {
		body = m.modalView()
	}

	// When the detail panel is open in normal/grab mode, join the list body
	// and detail panel side-by-side horizontally. Skip when a modal is active
	// to avoid layout conflicts.
	if m.mode == modeNormal || m.mode == modeGrab {
		if detail := m.detailPanelView(); detail != "" {
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, detail)
		}
	}

	help := m.helpLine()

	parts := []string{}
	if panel := m.activePanelView(); panel != "" {
		parts = append(parts, panel)
	}
	parts = append(parts, m.statsBarView(), header, body, help)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderHelpBar composes a styled status-bar line from key/desc pairs.
// Each key is rendered bold+light-red; descriptions and separators are dim grey.
// Pass an empty key ("") to render the desc as unstyled label (e.g., "GRAB").
// Pass an empty desc ("") to render the key alone (e.g., "+d/e/r/t/a/c/x/s").
func renderHelpBar(pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		key, desc := p[0], p[1]
		switch {
		case key == "":
			parts = append(parts, helpTextStyle.Render(desc))
		case desc == "":
			parts = append(parts, helpKeyStyle.Render(key))
		default:
			parts = append(parts, helpKeyStyle.Render(key)+helpTextStyle.Render(" "+desc))
		}
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(
		strings.Join(parts, helpTextStyle.Render("  ")),
	)
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
		if m.detailOpen {
			return renderHelpBar(
				[2]string{"esc", "close"},
				[2]string{"enter", "submit"},
				[2]string{"alt+enter", "newline"},
				[2]string{"[/]", "scroll"},
			)
		}
		if m.viewMode == viewTag {
			return renderHelpBar(
				[2]string{"←/→", "tabs"},
				[2]string{"enter", "notes"},
				[2]string{"d", "done"},
				[2]string{"e", "edit"},
				[2]string{"r", "date"},
				[2]string{"t", "tag"},
				[2]string{"c", "create"},
				[2]string{"x", "del"},
				[2]string{"m", "grab"},
				[2]string{"a", "active"},
				[2]string{"/", "search"},
				[2]string{"v", "schedule"},
				[2]string{"s", "sync"},
				[2]string{"h", "help"},
				[2]string{"q", "quit"},
			)
		}
		return renderHelpBar(
			[2]string{"←/→", "tabs"},
			[2]string{"1-6", "jump"},
			[2]string{"enter", "notes"},
			[2]string{"d", "done"},
			[2]string{"e", "edit"},
			[2]string{"r", "date"},
			[2]string{"t", "tag"},
			[2]string{"c", "create"},
			[2]string{"x", "del"},
			[2]string{"m", "grab"},
			[2]string{"a", "active"},
			[2]string{"/", "search"},
			[2]string{"v", "tags"},
			[2]string{"s", "sync"},
			[2]string{"h", "help"},
			[2]string{"q", "quit"},
		)
	case modeHelp:
		return helpStyle.Render("press any key to close")
	case modeGrab:
		if m.viewMode == viewTag {
			return renderHelpBar(
				[2]string{"", "GRAB"},
				[2]string{"↑/↓", "reorder"},
				[2]string{"g/G", "top/bottom"},
				[2]string{"enter", "drop"},
				[2]string{"esc", "cancel"},
				[2]string{"+d/e/r/t/a/c/x/s", ""},
			)
		}
		return renderHelpBar(
			[2]string{"", "GRAB"},
			[2]string{"↑/↓", "reorder"},
			[2]string{"←/→", "bucket"},
			[2]string{"g/G", "top/bottom"},
			[2]string{"enter", "drop"},
			[2]string{"esc", "cancel"},
			[2]string{"+d/e/r/t/a/c/x/s", ""},
		)
	case modeReschedule:
		if m.rescheduleSub == 0 {
			return renderHelpBar(
				[2]string{"1", "today"},
				[2]string{"2", "tomorrow"},
				[2]string{"3", "week"},
				[2]string{"4", "month"},
				[2]string{"5", "someday"},
				[2]string{"6", "custom"},
				[2]string{"esc", "cancel"},
			)
		}
		return renderHelpBar(
			[2]string{"enter", "save"},
			[2]string{"esc", "cancel"},
		)
	case modeRetag:
		return renderHelpBar(
			[2]string{"enter", "save"},
			[2]string{"esc", "cancel"},
		)
	case modeAdd:
		return renderHelpBar(
			[2]string{"tab", "switch field"},
			[2]string{"enter", "save"},
			[2]string{"esc", "cancel"},
		)
	case modeConfirmDelete:
		return renderHelpBar(
			[2]string{"y", "confirm"},
			[2]string{"", "anything else cancels"},
		)
	}
	return ""
}

func helpModalContent() string {
	k := helpKeyStyle.Render
	return "Keybindings:\n\n" +
		"  " + k("←/→") + "  navigate tabs\n" +
		"  " + k("1-6") + "  jump to tab\n" +
		"  " + k("d") + "    mark done\n" +
		"  " + k("e") + "    edit task\n" +
		"  " + k("r") + "    reschedule\n" +
		"  " + k("t") + "    retag\n" +
		"  " + k("c") + "    create task\n" +
		"  " + k("x") + "    delete\n" +
		"  " + k("m") + "    grab / reorder\n" +
		"  " + k("a") + "    toggle active\n" +
		"  " + k("/") + "    search\n" +
		"  " + k("v") + "    toggle view\n" +
		"  " + k("↵") + "    notes panel\n" +
		"  " + k("s") + "    sync\n" +
		"  " + k("h") + "    this help\n" +
		"  " + k("q") + "    quit\n\n" +
		"Search:\n\n" +
		"  " + k("/") + "              open fuzzy search\n" +
		"  " + k("↑/↓") + "            move selection\n" +
		"  " + k("ctrl+j/k") + "       move selection\n" +
		"  " + k("ctrl+n/p") + "       move selection\n" +
		"  " + k("pgdn/pgup") + "      page selection\n" +
		"  " + k("enter") + "          jump to task\n" +
		"  " + k("esc") + "            cancel"
}

func (m *Model) modalView() string {
	iw := m.modalInnerWidth()
	switch m.mode {
	case modeHelp:
		return modalBox(helpModalContent(), iw)
	case modeReschedule:
		if m.rescheduleSub == 0 {
			return modalBox("Reschedule:\n\n"+
				"  1  Today\n"+
				"  2  Tomorrow\n"+
				"  3  Week\n"+
				"  4  Month\n"+
				"  5  Someday\n"+
				"  6  Custom date...", iw)
		}
		return modalBox("Custom date:\n\n"+m.input.View(), iw)
	case modeRetag:
		sugLines := m.renderSuggestions()
		return modalBox("Tags (comma-separated):\n\n"+m.input.View()+sugLines, iw)
	case modeAdd:
		t := m.tabs[m.activeTab]
		// The textarea's View() spans titleArea.Height() lines; align the "Title:"
		// label with its first line so taller boxes still read naturally.
		titleView := indentContinuation(m.titleArea.View(), "       ")
		sugLines := m.renderSuggestions()
		return modalBox(fmt.Sprintf("Add task to %s:\n\nTitle: %s\nTags:  %s%s\nRecur: %s\n\n(Alt+Enter = newline, Enter = save)",
			t.label, titleView, m.tagInput.View(), sugLines, m.recurInput.View()), iw)
	case modeConfirmDelete:
		if m.modalTask == nil {
			return ""
		}
		return modalBox(fmt.Sprintf("Delete %q?\n\n(y = confirm, anything else cancels)", m.modalTask.Title), iw)
	}
	return ""
}

// modalBoxWidth returns the total outer width of the modal box (borders
// included), capped at 60 columns and adapted to the terminal width.
func (m *Model) modalBoxWidth() int {
	w := m.width - 4
	if w > 60 {
		w = 60
	}
	if w < 42 {
		w = 42
	}
	return w
}

// modalInnerWidth returns the content-area width inside the modal box
// (total width minus 2 border columns and 4 padding columns).
func (m *Model) modalInnerWidth() int {
	return m.modalBoxWidth() - 6
}

// modalBox wraps content in a subtle bordered box of fixed width so the
// border never shifts as the user types.
//
// innerWidth is the usable content-area width (where text wraps). lipgloss's
// .Width() actually wraps at `Width - leftPadding - rightPadding`, so we pass
// innerWidth+4 to get an effective wrap point of innerWidth.
func modalBox(content string, innerWidth int) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Width(innerWidth + 4).
		Render(content)
}

// --- helpers ---------------------------------------------------------------

// textareaVisualHeight returns the number of visual (display) lines the
// textarea content occupies, accounting for soft-wrapping. This differs from
// textarea.LineCount() which only counts logical lines delimited by newlines.
func textareaVisualHeight(ta textarea.Model) int {
	w := ta.Width()
	if w <= 0 {
		return max(1, ta.LineCount())
	}
	val := ta.Value()
	if val == "" {
		return 1
	}
	total := 0
	for _, line := range strings.Split(val, "\n") {
		lw := lipgloss.Width(line)
		if lw <= w {
			total++
		} else {
			total += (lw + w - 1) / w
		}
	}
	return max(1, total)
}

// indentContinuation prepends prefix to every line of s after the first. Used
// to align multi-line text under a leading label like "Title: ".
func indentContinuation(s, prefix string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 1 {
		return s
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func taskRelPath(id string) string {
	return filepath.Join(".monolog", "tasks", id+".json")
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// wrapText breaks s into lines of at most width runes, splitting at word
// boundaries when possible. Falls back to hard-breaking mid-word when a
// single word exceeds the width. Explicit '\n' characters (from multi-line
// titles) always start a new line. Returns at least one line.
func wrapText(s string, width int) []string {
	if !strings.Contains(s, "\n") {
		return wrapLine(s, width)
	}
	var lines []string
	for _, ln := range strings.Split(s, "\n") {
		lines = append(lines, wrapLine(ln, width)...)
	}
	return lines
}

// wrapLine width-wraps a single line (no embedded newlines).
func wrapLine(s string, width int) []string {
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

// flattenTitle replaces any newlines in a title with spaces so it fits on a
// single line. Used for git commit subjects and one-line status messages.
func flattenTitle(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	// Collapse the runs of spaces that blank lines would have produced.
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
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
