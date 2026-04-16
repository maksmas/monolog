package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
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
// Ctrl+C cancels; Enter commits; arrow / ctrl-nav keys move the cursor;
// everything else is delegated to the text input, and if the query changes
// the results are re-ranked.
func (m *Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.closeSearch()
		return m, nil
	case tea.KeyEnter:
		m.commitSearch()
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

// commitSearch accepts the currently-highlighted search result, closes the
// overlay, and focuses the corresponding task on whichever tab it lives in.
// With no results it is a no-op (Enter on an empty list does nothing).
// closeSearch is called first so mode returns to normal before any tab
// switch / cursor move, matching how the rest of the TUI dispatches after
// leaving a modal.
func (m *Model) commitSearch() {
	if len(m.search.results) == 0 {
		return
	}
	idx := m.search.cursor
	if idx < 0 || idx >= len(m.search.results) {
		return
	}
	docIdx := m.search.results[idx].docIdx
	if docIdx < 0 || docIdx >= len(m.search.haystack) {
		return
	}
	task := m.search.haystack[docIdx].task
	m.closeSearch()
	m.focusTaskAcrossTabs(task)
}

// focusTaskAcrossTabs switches activeTab to whichever tab currently owns the
// given task and then focuses it via focusTaskByID. In schedule view the tab
// is derived from task.Status (Done tab) or the schedule bucket; in tag view
// the active tab is preferred when the task carries the active tag, otherwise
// the first visible tag that has a matching tagTab, otherwise the Untagged
// tab. If no matching tab can be found the activeTab is left untouched and
// focusTaskByID is still attempted — on failure it is simply a no-op.
func (m *Model) focusTaskAcrossTabs(task model.Task) {
	if idx := m.tabForTask(task); idx >= 0 {
		m.activeTab = idx
	}
	m.focusTaskByID(task.ID)
}

// tabForTask returns the tab index that should show the given task in the
// current viewMode, or -1 if no tab matches. It does not mutate state.
func (m *Model) tabForTask(task model.Task) int {
	if m.viewMode == viewTag {
		return m.tagTabForTask(task)
	}
	return m.scheduleTabForTask(task)
}

// scheduleTabForTask returns the schedule-view tab index for a task:
// the Done tab when the task is done, otherwise whichever tab matches its
// classified schedule bucket. Returns -1 if no tab matches (e.g. a custom
// tab layout without a Done tab).
func (m *Model) scheduleTabForTask(task model.Task) int {
	if task.Status == "done" {
		for i, t := range m.tabs {
			if t.status == "done" {
				return i
			}
		}
		return -1
	}
	bkt := schedule.Bucket(task.Schedule, time.Now())
	for i, t := range m.tabs {
		if t.status == "open" && t.bucket == bkt {
			return i
		}
	}
	return -1
}

// tagTabForTask returns the tag-view tab index that should display the task.
// Priority: active tab (if the task carries the active tag), then the first
// matching visible-tag tab, then the untagged tab when the task has no
// visible tags. Returns -1 if no tab matches.
func (m *Model) tagTabForTask(task model.Task) int {
	if task.IsActive() {
		for i, tt := range m.tagTabs {
			if tt.isActive {
				return i
			}
		}
	}
	visible := display.VisibleTags(task.Tags)
	for _, tag := range visible {
		for i, tt := range m.tagTabs {
			if !tt.isActive && !tt.isUntagged && tt.tag == tag {
				return i
			}
		}
	}
	if len(visible) == 0 {
		for i, tt := range m.tagTabs {
			if tt.isUntagged {
				return i
			}
		}
	}
	return -1
}

// --- rendering -------------------------------------------------------------

// searchNarrowThreshold is the terminal width below which the split pane
// (results │ preview) collapses to a stacked layout. 80 columns is the
// historical "narrow terminal" cutoff and matches what most users mean by
// "small window".
const searchNarrowThreshold = 80

// searchHelpHint describes the in-search key set shown on the footer line.
// Kept as a small function so tests can assert its content without depending
// on rendering output.
func searchHelpHint() string {
	return renderHelpBar(
		[2]string{"type", "filter"},
		[2]string{"↑/↓", "move"},
		[2]string{"enter", "jump"},
		[2]string{"esc", "cancel"},
	)
}

// renderSearch builds the full-screen fuzzy search overlay. Layout:
//
//	input bar (1 line, counter on the right)
//	┌ results list ─┐ ┌ preview ─┐
//	│               │ │          │
//	└───────────────┘ └──────────┘
//	meta line (1 line: schedule · status · created)
//	help line (1 line)
//
// When m.width < searchNarrowThreshold, results and preview stack vertically.
// The renderer never mutates state — it reads from m.search and computes a
// string. Safe to call repeatedly per frame.
func (m *Model) renderSearch() string {
	// Input bar: `> ` + value with a right-aligned "visible/total" counter.
	total := len(m.search.haystack)
	visible := len(m.search.results)
	counter := searchCountStyle.Render(fmt.Sprintf("%d/%d", visible, total))
	input := m.search.input.View()
	inputLine := joinInputAndCounter(input, counter, m.width)

	// Results panel.
	resultsWidth, previewWidth := searchSplitWidths(m.width)
	bodyHeight := searchBodyHeight(m.height)

	resultsBody := m.renderSearchResults(resultsWidth, bodyHeight)
	previewBody := m.renderSearchPreview(previewWidth, bodyHeight)

	var body string
	if m.width < searchNarrowThreshold {
		// Stacked layout: split bodyHeight between results (top) and preview
		// (bottom). Give results a little more room since navigation needs it.
		stackedResultsH := bodyHeight * 2 / 3
		if stackedResultsH < 3 {
			stackedResultsH = 3
		}
		stackedPreviewH := bodyHeight - stackedResultsH
		if stackedPreviewH < 2 {
			stackedPreviewH = 2
		}
		resultsBody = m.renderSearchResults(m.width, stackedResultsH)
		previewBody = m.renderSearchPreview(m.width, stackedPreviewH)
		body = lipgloss.JoinVertical(lipgloss.Left, resultsBody, previewBody)
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, resultsBody, previewBody)
	}

	meta := m.renderSearchMeta()
	help := searchHelpHint()

	return lipgloss.JoinVertical(lipgloss.Left, inputLine, body, meta, help)
}

// joinInputAndCounter right-aligns the counter after the input. If the
// terminal width can't accommodate the counter, it is dropped rather than
// wrapped onto a second line — keeping the input bar a single row matters for
// layout stability.
func joinInputAndCounter(input, counter string, totalWidth int) string {
	iw := lipgloss.Width(input)
	cw := lipgloss.Width(counter)
	if totalWidth <= 0 || iw+cw+1 > totalWidth {
		return input
	}
	pad := totalWidth - iw - cw
	if pad < 1 {
		pad = 1
	}
	return input + strings.Repeat(" ", pad) + counter
}

// searchSplitWidths returns (resultsWidth, previewWidth) for the wide layout.
// Results get ~40% of the width and preview gets the rest.
func searchSplitWidths(total int) (int, int) {
	if total < searchNarrowThreshold {
		// Caller is expected to switch to stacked layout; return same defaults
		// here so unit tests of this helper remain stable.
		return total, total
	}
	results := total * 2 / 5
	if results < 20 {
		results = 20
	}
	preview := total - results
	if preview < 20 {
		preview = 20
	}
	return results, preview
}

// searchBodyHeight carves out vertical space for the results+preview region.
// Reserves rows for input bar, meta line, and help line.
func searchBodyHeight(total int) int {
	h := total - 3 // input + meta + help
	if h < 3 {
		h = 3
	}
	return h
}

// renderSearchResults formats the visible result rows inside a bordered box.
// The returned string has a stable height (rendered with lipgloss .Height()).
func (m *Model) renderSearchResults(width, height int) string {
	innerWidth := width - 2 // padding
	if innerWidth < 10 {
		innerWidth = 10
	}

	var lines []string
	if len(m.search.results) == 0 {
		placeholder := searchPreviewDimStyle.Render("(no matches)")
		lines = append(lines, placeholder)
	} else {
		// Determine which slice of results to render so the cursor stays visible.
		start, end := resultsWindow(m.search.cursor, len(m.search.results), height)
		for i := start; i < end; i++ {
			lines = append(lines, m.renderSearchResultRow(i, innerWidth))
		}
	}

	body := strings.Join(lines, "\n")
	return searchResultsStyle.Width(width).Height(height).Render(body)
}

// resultsWindow picks a [start, end) slice of results to render so the cursor
// stays inside the visible region. Simple scroll-to-cursor logic — no
// animation, no smooth scrolling.
func resultsWindow(cursor, total, height int) (int, int) {
	if height <= 0 || total == 0 {
		return 0, 0
	}
	if total <= height {
		return 0, total
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > total {
		end = total
		start = end - height
	}
	return start, end
}

// renderSearchResultRow formats a single result row: optional "✓" prefix for
// done tasks, bolded matched runes, and a reverse-video wash when selected.
func (m *Model) renderSearchResultRow(resultIdx, width int) string {
	res := m.search.results[resultIdx]
	doc := m.search.haystack[res.docIdx]
	task := doc.task

	prefix := "  "
	if task.Status == "done" {
		prefix = "✓ "
	}

	title := highlightRunes(task.Title, res.titleHit)
	// Truncate after highlighting to keep the rune indices aligned with the
	// original title. We budget the styled prefix width out of the total.
	available := width - lipgloss.Width(prefix)
	if available < 1 {
		available = 1
	}
	title = truncateStyledTitle(title, available)

	row := prefix + title
	switch {
	case task.IsActive():
		row = searchActiveStyle.Render(row)
	case task.Status == "done":
		row = searchDoneStyle.Render(row)
	}
	if resultIdx == m.search.cursor {
		row = searchSelectedStyle.Render(row)
	}
	return row
}

// highlightRunes bolds the runes at the given indices in s. Indices out of
// range are skipped defensively.
func highlightRunes(s string, hits []int) string {
	if len(hits) == 0 {
		return s
	}
	runes := []rune(s)
	hitSet := make(map[int]struct{}, len(hits))
	for _, h := range hits {
		if h >= 0 && h < len(runes) {
			hitSet[h] = struct{}{}
		}
	}
	if len(hitSet) == 0 {
		return s
	}
	var b strings.Builder
	for i, r := range runes {
		if _, ok := hitSet[i]; ok {
			b.WriteString(searchMatchStyle.Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// truncateStyledTitle trims a possibly-styled string to at most width display
// columns, appending "…" when truncated. Uses lipgloss.Width for measurement
// so ANSI escape sequences are not counted.
func truncateStyledTitle(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	// Best-effort: strip styling by re-constructing from the plain runes. We
	// lose highlights in the truncated portion, which is an acceptable
	// tradeoff since only very long titles hit this branch.
	plain := stripANSI(s)
	return truncateTitle(plain, width)
}

// stripANSI removes CSI escape sequences from s. The search overlay only uses
// simple SGR sequences (ESC [ ... m) and this covers them all.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == 0x1b { // ESC
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// renderSearchPreview renders the body of the currently-selected task inside a
// bordered pane. Shows "(no body)" when the task has no body; empty results
// shows a placeholder.
func (m *Model) renderSearchPreview(width, height int) string {
	innerWidth := width - 4 // border (2) + padding (2)
	if innerWidth < 10 {
		innerWidth = 10
	}
	innerHeight := height - 2 // border top/bottom
	if innerHeight < 1 {
		innerHeight = 1
	}

	if len(m.search.results) == 0 {
		ph := searchPreviewDimStyle.Render("(no task selected)")
		return searchPreviewBorderStyle.Width(width).Height(height).Render(ph)
	}

	res := m.search.results[m.search.cursor]
	task := m.search.haystack[res.docIdx].task

	title := searchPreviewTitleStyle.Render(truncateTitle(task.Title, innerWidth))
	var body string
	if strings.TrimSpace(task.Body) == "" {
		body = searchPreviewDimStyle.Render("(no body)")
	} else {
		body = lipgloss.NewStyle().Width(innerWidth).Render(task.Body)
	}

	content := title + "\n\n" + body
	return searchPreviewBorderStyle.Width(width).Height(height).Render(content)
}

// renderSearchMeta formats the meta line: "schedule · status · created {date}".
// Empty when there is no selection.
func (m *Model) renderSearchMeta() string {
	if len(m.search.results) == 0 {
		return searchMetaStyle.Render(" ")
	}
	res := m.search.results[m.search.cursor]
	task := m.search.haystack[res.docIdx].task
	bucket := schedule.Bucket(task.Schedule, time.Now())
	created := display.FormatRelDate(time.Now(), task.CreatedAt)
	parts := []string{bucket, task.Status}
	if created != "" {
		parts = append(parts, "created "+created)
	}
	return searchMetaStyle.Render(strings.Join(parts, " · "))
}
