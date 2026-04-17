package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
)

// vlist is a variable-height scrollable list. Each item renders at its natural
// height (number of display lines) rather than a fixed delegate height.
type vlist struct {
	items   []list.Item
	cursor  int
	yOffset int // scroll position in lines from the top
	width   int
	height  int
}

// --- data access -----------------------------------------------------------

func (vl *vlist) Items() []list.Item { return vl.items }
func (vl *vlist) Index() int         { return vl.cursor }
func (vl *vlist) Width() int         { return vl.width }
func (vl *vlist) SetSize(w, h int)   { vl.width = w; vl.height = h }

func (vl *vlist) SetItems(items []list.Item) {
	vl.items = items
	if vl.cursor >= len(items) {
		vl.cursor = max(0, len(items)-1)
	}
}

func (vl *vlist) Select(i int) {
	if len(vl.items) == 0 {
		vl.cursor = 0
		return
	}
	vl.cursor = clamp(i, 0, len(vl.items)-1)
}

func (vl *vlist) SelectedItem() list.Item {
	if vl.cursor >= 0 && vl.cursor < len(vl.items) {
		return vl.items[vl.cursor]
	}
	return nil
}

// --- height helpers --------------------------------------------------------

// isSep reports whether the item at index i is a separator.
func (vl *vlist) isSep(i int) bool {
	if i < 0 || i >= len(vl.items) {
		return false
	}
	it, ok := vl.items[i].(item)
	return ok && it.isSeparator
}

// itemHeight returns the display-line count for the item at index i.
// Separators occupy 1 line; regular items occupy wrapped-title lines + 1 desc
// + 1 blank separator line.
func (vl *vlist) itemHeight(i int) int {
	it, ok := vl.items[i].(item)
	if !ok || it.isSeparator {
		return 1
	}
	tw := vl.width - 2 // matches NormalTitle left padding
	if tw <= 0 {
		return 3
	}
	return len(wrapText(it.Title(), tw)) + 2 // +1 desc, +1 blank line
}

// ensureVisible adjusts yOffset so the cursor item is fully within the
// viewport.
func (vl *vlist) ensureVisible() {
	if len(vl.items) == 0 || vl.height <= 0 {
		return
	}
	top, h := vl.cursorSpan()
	if top < vl.yOffset {
		vl.yOffset = top
	}
	if top+h > vl.yOffset+vl.height {
		vl.yOffset = top + h - vl.height
	}
	vl.clampScroll()
}

// cursorSpan returns the line offset and height of the cursor item.
func (vl *vlist) cursorSpan() (top, height int) {
	for i := 0; i < vl.cursor && i < len(vl.items); i++ {
		top += vl.itemHeight(i)
	}
	if vl.cursor >= 0 && vl.cursor < len(vl.items) {
		height = vl.itemHeight(vl.cursor)
	}
	return
}

func (vl *vlist) clampScroll() {
	total := 0
	for i := range vl.items {
		total += vl.itemHeight(i)
	}
	maxOff := total - vl.height
	if maxOff < 0 {
		maxOff = 0
	}
	if vl.yOffset > maxOff {
		vl.yOffset = maxOff
	}
	if vl.yOffset < 0 {
		vl.yOffset = 0
	}
}

// --- cursor navigation -----------------------------------------------------

func (vl *vlist) CursorUp() {
	for i := vl.cursor - 1; i >= 0; i-- {
		if !vl.isSep(i) {
			vl.cursor = i
			vl.ensureVisible()
			return
		}
	}
}

func (vl *vlist) CursorDown() {
	for i := vl.cursor + 1; i < len(vl.items); i++ {
		if !vl.isSep(i) {
			vl.cursor = i
			vl.ensureVisible()
			return
		}
	}
}

func (vl *vlist) GoToStart() {
	for i := 0; i < len(vl.items); i++ {
		if !vl.isSep(i) {
			vl.cursor = i
			vl.ensureVisible()
			return
		}
	}
}

func (vl *vlist) GoToEnd() {
	for i := len(vl.items) - 1; i >= 0; i-- {
		if !vl.isSep(i) {
			vl.cursor = i
			vl.ensureVisible()
			return
		}
	}
}

func (vl *vlist) PageUp() {
	target := vl.height
	moved := 0
	last := vl.cursor
	for i := vl.cursor - 1; i >= 0; i-- {
		moved += vl.itemHeight(i)
		if !vl.isSep(i) {
			last = i
		}
		if moved >= target {
			break
		}
	}
	vl.cursor = last
	vl.ensureVisible()
}

func (vl *vlist) PageDown() {
	target := vl.height
	moved := 0
	last := vl.cursor
	for i := vl.cursor + 1; i < len(vl.items); i++ {
		moved += vl.itemHeight(i)
		if !vl.isSep(i) {
			last = i
		}
		if moved >= target {
			break
		}
	}
	vl.cursor = last
	vl.ensureVisible()
}

// SkipSeparator moves the cursor past a separator in the given direction.
// Used after SetItems or Select when the cursor may have landed on a separator.
func (vl *vlist) SkipSeparator(dir int) {
	if !vl.isSep(vl.cursor) {
		return
	}
	// Try the requested direction first.
	for next := vl.cursor + dir; next >= 0 && next < len(vl.items); next += dir {
		if !vl.isSep(next) {
			vl.cursor = next
			return
		}
	}
	// Fall back to the opposite direction.
	for next := vl.cursor - dir; next >= 0 && next < len(vl.items); next -= dir {
		if !vl.isSep(next) {
			vl.cursor = next
			return
		}
	}
}

// --- rendering -------------------------------------------------------------

// Render renders the visible portion of the list. renderFn is called for each
// visible item and must return the fully styled content for that item.
func (vl *vlist) Render(renderFn func(index int, it list.Item, selected bool) string) string {
	if len(vl.items) == 0 || vl.height <= 0 {
		return strings.Repeat("\n", max(0, vl.height-1))
	}
	vl.ensureVisible()

	var out []string
	pos := 0
	for i, it := range vl.items {
		h := vl.itemHeight(i)
		if pos >= vl.yOffset+vl.height {
			break
		}
		if pos+h > vl.yOffset {
			content := renderFn(i, it, i == vl.cursor)
			for j, ln := range strings.Split(content, "\n") {
				abs := pos + j
				if abs >= vl.yOffset && abs < vl.yOffset+vl.height {
					out = append(out, ln)
				}
			}
		}
		pos += h
	}
	for len(out) < vl.height {
		out = append(out, "")
	}
	return strings.Join(out[:vl.height], "\n")
}

// --- helpers ---------------------------------------------------------------

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
