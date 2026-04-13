package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/store"
)

// newTestModel returns a Model with a temp store pre-populated with the given
// tasks. Tasks are placed in their declared schedule buckets.
func newTestModel(t *testing.T, tasks ...model.Task) *Model {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	for _, task := range tasks {
		if err := s.Create(task); err != nil {
			t.Fatalf("store.Create: %v", err)
		}
	}
	m, err := newModel(s, dir)
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	return m
}

// key sends a key event to the model and returns the updated model.
func key(t *testing.T, m *Model, s string) *Model {
	t.Helper()
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	// Map arrow/name keys that can't be represented as runes.
	switch s {
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	}
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

	// Today tab: expect the two open "today" tasks.
	if got := len(m.lists[0].Items()); got != 2 {
		t.Errorf("Today tab items = %d, want 2", got)
	}
	// Tomorrow tab: 1 task.
	if got := len(m.lists[1].Items()); got != 1 {
		t.Errorf("Tomorrow tab items = %d, want 1", got)
	}
	// Done tab: 1 task (status=done, schedule irrelevant).
	if got := len(m.lists[4].Items()); got != 1 {
		t.Errorf("Done tab items = %d, want 1", got)
	}
}

func TestSkeleton_RightArrowAdvancesTab(t *testing.T) {
	m := newTestModel(t)
	if m.activeTab != 0 {
		t.Fatalf("initial tab = %d, want 0", m.activeTab)
	}
	m = key(t, m, "right")
	if m.activeTab != 1 {
		t.Errorf("after right: tab = %d, want 1", m.activeTab)
	}
}

func TestSkeleton_LeftArrowWrapsAround(t *testing.T) {
	m := newTestModel(t)
	m = key(t, m, "left")
	if m.activeTab != len(defaultTabs)-1 {
		t.Errorf("after left from 0: tab = %d, want %d (wrap to Done)",
			m.activeTab, len(defaultTabs)-1)
	}
}

func TestSkeleton_NumberKeysJump(t *testing.T) {
	m := newTestModel(t)
	m = key(t, m, "3")
	if m.activeTab != 2 {
		t.Errorf("after '3': tab = %d, want 2 (Week)", m.activeTab)
	}
	m = key(t, m, "5")
	if m.activeTab != 4 {
		t.Errorf("after '5': tab = %d, want 4 (Done)", m.activeTab)
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
	if len(items) != 3 {
		t.Fatalf("Done tab items = %d, want 3", len(items))
	}
	first := items[0].(item).task
	if first.Title != "newest done" {
		t.Errorf("Done[0] = %q, want %q (newest UpdatedAt first)", first.Title, "newest done")
	}
	last := items[2].(item).task
	if last.Title != "oldest done" {
		t.Errorf("Done[2] = %q, want %q", last.Title, "oldest done")
	}
}

func TestSkeleton_QuitReturnsQuitCmd(t *testing.T) {
	m := newTestModel(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q should return tea.Quit cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("q cmd produced %T, want tea.QuitMsg", msg)
	}
}
