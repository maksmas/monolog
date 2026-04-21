package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
)

// searchResultLimit caps the number of ranked results returned each keystroke.
// Users cannot realistically navigate past 200 entries, and a hard cap keeps
// rendering cheap regardless of backlog size.
const searchResultLimit = 200

// searchPageSize is the cursor-jump step for PgDn / PgUp inside the search
// overlay. It is intentionally a fixed constant (not derived from viewport
// height) so behavior does not depend on the renderer, which simplifies tests.
const searchPageSize = 10

// openSearch enters the fuzzy-search overlay. It snapshots the current
// in-memory task list (Model.allTasks, kept current by every mutation via
// reloadAllTasks) into searchDoc wrappers with parallel titles/bodies slices
// for the ranker, resets input/cursor state, and seeds the results with an
// empty-query rank so the list starts populated. Using the cached slice
// avoids a fresh disk scan on every "/" press.
func (m *Model) openSearch() {
	tasks := m.allTasks
	docs := make([]searchDoc, len(tasks))
	titles := make([]string, len(tasks))
	bodies := make([]string, len(tasks))
	for i, t := range tasks {
		docs[i] = searchDoc{task: t}
		titles[i] = t.Title
		bodies[i] = t.Body
	}
	m.mode = modeSearch
	m.search.haystack = docs
	m.search.titles = titles
	m.search.bodies = bodies
	m.search.cursor = 0
	m.search.input.SetValue("")
	m.search.input.Focus()
	m.search.results = rankSearch("", docs, titles, bodies, searchResultLimit)
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
	m.search.titles = nil
	m.search.bodies = nil
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
		m.search.results = rankSearch(m.search.input.Value(), m.search.haystack, m.search.titles, m.search.bodies, searchResultLimit)
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
// given task and then focuses it via focusTaskByID. In schedule view each
// task appears in exactly one tab's list, so focusTaskByID alone finds and
// selects it. In tag view a task with multiple tags appears in multiple tabs;
// tagTabForTask picks the preferred tab (active > first visible tag >
// untagged) so we land somewhere predictable rather than on whichever list
// focusTaskByID scans first.
func (m *Model) focusTaskAcrossTabs(task model.Task) {
	if m.viewMode == viewTag {
		if idx := m.tagTabForTask(task); idx >= 0 {
			m.activeTab = idx
		}
	}
	m.focusTaskByID(task.ID)
}

// tagTabForTask returns the tag-view tab index that should display the task.
// Priority: active tab (if the task carries the active tag), then the first
// matching visible-tag tab, then the untagged tab when the task has no
// visible tags or when no visible tag has a tab (buildTagTabs derives tabs
// only from open tasks, so a done task whose tags belonged to open tasks that
// are now gone would otherwise land on -1). Returns -1 only if neither a
// matching tag tab nor the untagged tab exists.
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
	// Fall through to the untagged tab both when the task has no visible tags
	// and when its tags have no corresponding visible tab — without this
	// fallback, Enter on a done-with-orphaned-tag task would silently no-op.
	for i, tt := range m.tagTabs {
		if tt.isUntagged {
			return i
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
func (m *Model) searchHelpHint() string {
	return m.renderHelpBar(
		[2]string{"type", "filter"},
		[2]string{"↑/↓", "move"},
		[2]string{"enter", "jump"},
		[2]string{"esc", "cancel"},
	)
}

// searchCompactMinHeight is the height below which the overlay switches to a
// compact layout (input + results only, no preview/meta/help). Below this, the
// full layout would overflow the terminal: the stacked path needs at least 10
// rows (3 results + 4 preview incl. border + 1 input + 1 meta + 1 help), and
// the wide path needs at least 8. Using the stacked path's minimum as a single
// threshold keeps the logic simple at the cost of a slightly-overzealous
// fallback for 8x80+ terminals — acceptable since those are rare.
const searchCompactMinHeight = 10

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
// When m.height < searchCompactMinHeight, the preview, meta, and help lines are
// dropped to keep the render within the terminal bounds. The renderer never
// mutates state — it reads from m.search and computes a string. Safe to call
// repeatedly per frame.
func (m *Model) renderSearch() string {
	// Input bar: `> ` + value with a right-aligned "visible/total" counter.
	total := len(m.search.haystack)
	visible := len(m.search.results)
	counter := m.styles.searchCountStyle.Render(fmt.Sprintf("%d/%d", visible, total))
	input := m.search.input.View()
	inputLine := joinInputAndCounter(input, counter, m.width)

	// Very short terminal: drop preview/meta/help and render only input +
	// results. This guarantees the overlay never exceeds m.height on tiny
	// windows (where the full layout's 2-row preview border + 3 other rows
	// already equals or exceeds m.height on its own).
	if m.height < searchCompactMinHeight {
		resultsH := m.height - 1 // reserve 1 row for the input line
		if resultsH < 1 {
			resultsH = 1
		}
		width := m.width
		if width < 10 {
			width = 10
		}
		resultsBody := m.renderSearchResults(width, resultsH)
		return lipgloss.JoinVertical(lipgloss.Left, inputLine, resultsBody)
	}

	// Results panel. searchBodyHeight accounts for input/meta/help AND the
	// preview's top+bottom border rows, so the preview is rendered with
	// height=bodyHeight (the border adds 2 rows on top). The wide path
	// additionally uses searchSplitWidths, which reserves 2 cols for the
	// preview border so the total width of the horizontally-joined panes
	// stays at m.width.
	bodyHeight := searchBodyHeight(m.height)

	var body string
	if m.width < searchNarrowThreshold {
		// Stacked layout: split bodyHeight between results (top) and preview
		// (bottom). Give results a little more room since navigation needs it.
		// The preview pane has a 2-row border, so it consumes 2 extra rows
		// beyond its content height — subtract those from the stacked preview
		// slice to keep the total render height at bodyHeight+2.
		stackedResultsH := bodyHeight * 2 / 3
		if stackedResultsH < 3 {
			stackedResultsH = 3
		}
		stackedPreviewH := bodyHeight - stackedResultsH
		if stackedPreviewH < 2 {
			stackedPreviewH = 2
		}
		// Preview border adds 2 cols outside its .Width(), so pass m.width-2
		// to keep the rendered row at exactly m.width. Results uses the same
		// width (no border) so both panes align.
		previewW := m.width - 2
		if previewW < 10 {
			previewW = 10
		}
		resultsBody := m.renderSearchResults(previewW, stackedResultsH)
		previewBody := m.renderSearchPreview(previewW, stackedPreviewH)
		body = lipgloss.JoinVertical(lipgloss.Left, resultsBody, previewBody)
	} else {
		// Wide path: results gets bodyHeight+2 rows so its height matches the
		// bordered preview (which renders at previewHeight+2 due to the border).
		resultsWidth, previewWidth := searchSplitWidths(m.width)
		resultsBody := m.renderSearchResults(resultsWidth, bodyHeight+2)
		previewBody := m.renderSearchPreview(previewWidth, bodyHeight)
		body = lipgloss.JoinHorizontal(lipgloss.Top, resultsBody, previewBody)
	}

	meta := m.renderSearchMeta()
	help := m.searchHelpHint()

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
// Results get ~40% of the width and preview gets the rest. The returned
// previewWidth is the content width passed to searchPreviewBorderStyle.Width,
// which adds 2 cols for the rounded border. searchSplitWidths therefore
// reserves 2 cols up-front so the horizontally-joined panes render at exactly
// `total` columns (resultsWidth + previewWidth + border == total).
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
	preview := total - results - 2 // 2 cols reserved for the preview border
	if preview < 20 {
		preview = 20
	}
	return results, preview
}

// searchBodyHeight carves out vertical space for the results+preview region.
// Reserves rows for input bar, meta line, help line, and the preview's 2-row
// rounded border (which renders outside the height passed to .Height()).
// The returned value is the content height to pass to renderSearchPreview;
// the results pane uses h+2 in the wide layout so both panes occupy the same
// number of rows.
func searchBodyHeight(total int) int {
	h := total - 5 // input + meta + help + preview border (top+bottom)
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
		placeholder := m.styles.searchPreviewDimStyle.Render("(no matches)")
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

	title := highlightMatches(task.Title, res.titleHit)
	// Truncate after highlighting to keep the byte offsets aligned with the
	// original title. We budget the styled prefix width out of the total.
	available := width - lipgloss.Width(prefix)
	if available < 1 {
		available = 1
	}
	title = truncateStyledTitle(title, available)

	row := prefix + title
	switch {
	case task.IsActive():
		row = m.styles.searchActiveStyle.Render(row)
	case task.Status == "done":
		row = m.styles.searchDoneStyle.Render(row)
	}
	if resultIdx == m.search.cursor {
		row = searchSelectedStyle.Render(row)
	}
	return row
}

// highlightMatches bolds the runes at the given byte offsets in s (the offsets
// sahilm/fuzzy returns in MatchedIndexes). Offsets outside the string or that
// do not start a rune are skipped defensively.
func highlightMatches(s string, hits []int) string {
	if len(hits) == 0 {
		return s
	}
	hitSet := make(map[int]struct{}, len(hits))
	for _, h := range hits {
		if h >= 0 && h < len(s) {
			hitSet[h] = struct{}{}
		}
	}
	if len(hitSet) == 0 {
		return s
	}
	// searchMatchStyle is theme-independent (bold only) so use a fixed style
	// rather than going through the model.
	matchStyle := lipgloss.NewStyle().Bold(true)
	var b strings.Builder
	// Walk the string by rune, keyed on the byte offset of each rune start,
	// so multi-byte highlight positions line up with their source rune.
	for i, r := range s {
		if _, ok := hitSet[i]; ok {
			b.WriteString(matchStyle.Render(string(r)))
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
	// Fallback when truncation is needed: strip ALL ANSI styling and truncate
	// the plain runes. This drops every highlight in the row (not just those
	// in the trimmed tail) because slicing a styled string mid-escape would
	// corrupt output. Acceptable tradeoff — only very long titles hit this
	// branch, and the preview pane still shows the full styled title.
	plain := ansi.Strip(s)
	return truncateTitle(plain, width)
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
		ph := m.styles.searchPreviewDimStyle.Render("(no task selected)")
		return m.styles.searchPreviewBorderStyle.Width(width).Height(height).Render(ph)
	}

	res := m.search.results[m.search.cursor]
	task := m.search.haystack[res.docIdx].task

	title := searchPreviewTitleStyle.Render(truncateTitle(task.Title, innerWidth))
	var body string
	if strings.TrimSpace(task.Body) == "" {
		body = m.styles.searchPreviewDimStyle.Render("(no body)")
	} else {
		body = lipgloss.NewStyle().Width(innerWidth).Render(task.Body)
	}

	content := title + "\n\n" + body
	return m.styles.searchPreviewBorderStyle.Width(width).Height(height).Render(content)
}

// renderSearchMeta formats the meta line: "schedule · status · created {date}".
// Empty when there is no selection.
func (m *Model) renderSearchMeta() string {
	if len(m.search.results) == 0 {
		return m.styles.searchMetaStyle.Render(" ")
	}
	res := m.search.results[m.search.cursor]
	task := m.search.haystack[res.docIdx].task
	bucket := schedule.Bucket(task.Schedule, time.Now())
	created := display.FormatRelDate(time.Now(), task.CreatedAt, config.DateFormat())
	parts := []string{bucket, task.Status}
	if created != "" {
		parts = append(parts, "created "+created)
	}
	return m.styles.searchMetaStyle.Render(strings.Join(parts, " · "))
}
