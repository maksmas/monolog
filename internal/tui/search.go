package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mmaksmas/monolog/internal/store"
)

// searchResultLimit caps the number of ranked results returned each keystroke.
// Users cannot realistically navigate past 200 entries, and a hard cap keeps
// rendering cheap regardless of backlog size.
const searchResultLimit = 200

// searchPageSize is the cursor-jump step for PgDn / PgUp inside the search
// overlay. It is intentionally a fixed constant (not derived from viewport
// height) so behavior does not depend on the renderer, which simplifies tests.
const searchPageSize = 10

// openSearch enters the fuzzy-search overlay. It captures a one-shot snapshot
// of every task (open + done) with precomputed lowercased title/body strings,
// resets input/cursor state, and seeds the results with an empty-query rank
// so the list starts populated.
func (m *Model) openSearch() {
	tasks, err := m.store.List(store.ListOptions{})
	if err != nil {
		m.err = err
		return
	}
	docs := make([]searchDoc, len(tasks))
	for i, t := range tasks {
		docs[i] = searchDoc{
			task:    t,
			titleLC: strings.ToLower(t.Title),
			bodyLC:  strings.ToLower(t.Body),
		}
	}
	m.mode = modeSearch
	m.search.haystack = docs
	m.search.cursor = 0
	m.search.input.SetValue("")
	m.search.input.Focus()
	m.search.results = rankSearch("", docs, searchResultLimit)
	m.recomputeLayout()
}

// closeSearch leaves the fuzzy-search overlay and returns to normal mode. It
// releases the haystack/results slices so they can be garbage-collected and
// leaves activeTab / list cursor untouched so Esc is a true no-op.
func (m *Model) closeSearch() {
	m.mode = modeNormal
	m.search.input.Blur()
	m.search.input.SetValue("")
	m.search.haystack = nil
	m.search.results = nil
	m.search.cursor = 0
}

// updateSearch routes key messages while the search overlay is open. Esc /
// Ctrl+C cancels; Enter commits (handled in Task 5); arrow / ctrl-nav keys
// move the cursor; everything else is delegated to the text input, and if
// the query changes the results are re-ranked.
func (m *Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.closeSearch()
		return m, nil
	case tea.KeyEnter:
		// commitSearch is implemented in Task 5; until then, Enter is a no-op.
		return m, nil
	}

	switch msg.String() {
	case "down", "ctrl+j", "ctrl+n":
		m.moveSearchCursor(+1)
		return m, nil
	case "up", "ctrl+k", "ctrl+p":
		m.moveSearchCursor(-1)
		return m, nil
	case "pgdown":
		m.moveSearchCursor(+searchPageSize)
		return m, nil
	case "pgup":
		m.moveSearchCursor(-searchPageSize)
		return m, nil
	}

	// Anything else is treated as input editing. Capture the value before and
	// after to decide whether re-ranking is needed.
	before := m.search.input.Value()
	var cmd tea.Cmd
	m.search.input, cmd = m.search.input.Update(msg)
	if m.search.input.Value() != before {
		m.search.results = rankSearch(m.search.input.Value(), m.search.haystack, searchResultLimit)
		m.clampSearchCursor()
	}
	return m, cmd
}

// moveSearchCursor shifts the result cursor by delta and clamps to the valid
// range. With an empty result set the cursor stays at 0.
func (m *Model) moveSearchCursor(delta int) {
	if len(m.search.results) == 0 {
		m.search.cursor = 0
		return
	}
	c := m.search.cursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(m.search.results) {
		c = len(m.search.results) - 1
	}
	m.search.cursor = c
}

// clampSearchCursor pins the cursor into [0, len(results)) after re-ranking.
// A shorter result set must not leave the cursor dangling past the end.
func (m *Model) clampSearchCursor() {
	if len(m.search.results) == 0 {
		m.search.cursor = 0
		return
	}
	if m.search.cursor >= len(m.search.results) {
		m.search.cursor = len(m.search.results) - 1
	}
	if m.search.cursor < 0 {
		m.search.cursor = 0
	}
}
