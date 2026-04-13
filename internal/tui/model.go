package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/store"
)

// tab describes one schedule bucket shown in the TUI.
type tab struct {
	label    string
	schedule string // matches model.Task.Schedule; empty means "any"
	status   string // "open" for regular tabs, "done" for the Done tab
}

// tabs used in the UI, in display order. 1-5 shortcut keys index into this.
var defaultTabs = []tab{
	{label: "Today", schedule: "today", status: "open"},
	{label: "Tomorrow", schedule: "tomorrow", status: "open"},
	{label: "Week", schedule: "week", status: "open"},
	{label: "Someday", schedule: "someday", status: "open"},
	{label: "Done", schedule: "", status: "done"},
}

// Model is the top-level Bubble Tea model for the TUI.
type Model struct {
	store    *store.Store
	repoPath string

	tabs      []tab
	activeTab int
	lists     []list.Model // one per tab

	width, height int

	statusMsg string // transient bottom-line message; cleared on next keypress
	err       error  // sticky error surfaced in status bar
}

// item wraps a model.Task for display in a bubbles/list.
type item struct {
	task model.Task
}

func (i item) Title() string {
	return i.task.Title
}

func (i item) Description() string {
	parts := []string{display.ShortID(i.task.ID)}
	if i.task.Schedule != "" && i.task.Schedule != "today" {
		parts = append(parts, i.task.Schedule)
	}
	if len(i.task.Tags) > 0 {
		parts = append(parts, "["+strings.Join(i.task.Tags, ", ")+"]")
	}
	return strings.Join(parts, "  ")
}

func (i item) FilterValue() string { return i.task.Title }

// newModel constructs a Model and loads initial task data for each tab.
func newModel(s *store.Store, repoPath string) (*Model, error) {
	m := &Model{
		store:    s,
		repoPath: repoPath,
		tabs:     defaultTabs,
		lists:    make([]list.Model, len(defaultTabs)),
	}

	for i, t := range m.tabs {
		delegate := list.NewDefaultDelegate()
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

// reloadAll refreshes every tab from the store. Used on startup and after
// actions that may have changed tasks in multiple buckets.
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
	opts := store.ListOptions{Schedule: t.schedule, Status: t.status}
	tasks, err := m.store.List(opts)
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	// Done tab is sorted by completion (UpdatedAt) descending rather than
	// position, which has no meaning for completed tasks.
	if t.status == "done" {
		sort.SliceStable(tasks, func(i, j int) bool {
			return tasks[i].UpdatedAt > tasks[j].UpdatedAt
		})
	}
	items := make([]list.Item, len(tasks))
	for i, task := range tasks {
		items[i] = item{task: task}
	}
	m.lists[idx].SetItems(items)
	return nil
}

// Init is the Bubble Tea Init hook. No background work yet; the constructor
// already loaded initial data.
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update routes a tea.Msg through the model, producing state transitions and
// potentially a follow-up tea.Cmd.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve rows for tab bar (1), borders/padding (2), help bar (1).
		listH := msg.Height - 4
		if listH < 3 {
			listH = 3
		}
		for i := range m.lists {
			m.lists[i].SetSize(msg.Width, listH)
		}
		return m, nil

	case tea.KeyMsg:
		// Clear transient status on any keypress.
		m.statusMsg = ""

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "left", "h", "shift+tab":
			m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
			return m, nil
		case "right", "l", "tab":
			m.activeTab = (m.activeTab + 1) % len(m.tabs)
			return m, nil
		case "1", "2", "3", "4", "5":
			n := int(msg.String()[0] - '1')
			if n < len(m.tabs) {
				m.activeTab = n
			}
			return m, nil
		}
	}

	// Delegate remaining input to the active tab's list.
	var cmd tea.Cmd
	m.lists[m.activeTab], cmd = m.lists[m.activeTab].Update(msg)
	return m, cmd
}

// View renders the current frame.
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

	body := m.lists[m.activeTab].View()

	help := helpStyle.Render("←/→ tabs  1-5 jump  q quit")
	if m.err != nil {
		help = statusStyle.Render("error: " + m.err.Error())
	} else if m.statusMsg != "" {
		help = statusStyle.Render(m.statusMsg)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, help)
}
