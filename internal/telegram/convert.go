// Package telegram implements the Telegram bot integration for monolog.
//
// The package owns the bot/conversion/handler/sync stack and is consumed by
// cmd/telegram.go. It mirrors internal/email in shape: a neutral Bot
// interface for testability, pure conversion helpers, and a Sync-style
// orchestrator that takes its options by value.
//
// Package contract: internal/telegram MUST NOT import internal/config —
// configuration values flow in via config.Telegram() read once by callers
// (cmd/telegram.go and tests) and passed by value to telegram.Serve. This
// keeps the package easy to unit-test with arbitrary settings and prevents
// import cycles with future config additions.
package telegram

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maksmas/monolog/internal/display"
	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/schedule"
)

// inlineTagPattern matches a #hashtag token. Tag characters are
// case-insensitive ASCII letters, digits, underscore, and dash — the same
// alphabet allowed by Twitter/Mastodon style hashtags. The pattern requires
// at least one body character after `#` so a bare `#` in a sentence is not
// treated as a tag.
//
// The match runs anywhere in the text (no anchors), so users can put
// hashtags at the start, middle, or end of the title line. Surrounding
// whitespace is collapsed by ParseInlineTags after extraction.
var inlineTagPattern = regexp.MustCompile(`#([A-Za-z0-9_-]+)`)

// whitespaceRun matches one or more whitespace characters so we can collapse
// any gaps left behind after stripping the hashtag tokens.
var whitespaceRun = regexp.MustCompile(`\s+`)

// ParseInlineTags extracts #hashtag tokens from text and returns the text
// with those tokens removed plus a deduped lowercase tag list.
//
// Tag tokens are matched by inlineTagPattern, which accepts ASCII letters,
// digits, underscores, and dashes after the `#`. The leading `#` is NOT
// part of the returned tag value (the caller stores tags without it).
//
// Duplicates are deduped case-insensitively while preserving the order of
// first appearance. The returned tag values are lowercased so that
// `#Work` and `#work` collapse to the same store-side tag.
//
// Whitespace cleanup: after stripping the tokens, the result runs through
// whitespaceRun to collapse any runs of spaces left behind, then
// strings.TrimSpace removes leading/trailing whitespace. A text composed
// entirely of hashtags returns "".
func ParseInlineTags(text string) (cleaned string, tags []string) {
	matches := inlineTagPattern.FindAllStringSubmatch(text, -1)
	if len(matches) > 0 {
		seen := make(map[string]struct{}, len(matches))
		for _, m := range matches {
			tag := strings.ToLower(m[1])
			if _, ok := seen[tag]; ok {
				continue
			}
			seen[tag] = struct{}{}
			tags = append(tags, tag)
		}
	}
	cleaned = inlineTagPattern.ReplaceAllString(text, "")
	cleaned = whitespaceRun.ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, tags
}

// ParseCapture splits Telegram message text into (title, body, tags).
//
// Splitting rule: the text is split on the first newline. The portion
// before the newline (or the full text if no newline is present) is the
// title-candidate; the portion after is the body. The body is passed
// through verbatim — leading/trailing whitespace and any hashtags inside
// the body survive untouched. Only the title-candidate is fed through
// ParseInlineTags, so #hashtags only become tags when they appear on the
// first line.
//
// The returned title has hashtag tokens stripped and internal whitespace
// collapsed. The body is returned as-is so multi-line capture preserves
// the user's intent (URL lists, code snippets, etc.).
//
// model.ParseTitleTag handling (the `tagname: ...` prefix auto-tag rule)
// is intentionally NOT applied here — it runs inside store.Create at write
// time so the same logic governs every capture path (CLI, TUI, Telegram).
// This keeps the inline-hashtag extraction separate from the prefix rule.
func ParseCapture(text string) (title, body string, tags []string) {
	titlePart := text
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		titlePart = text[:idx]
		body = text[idx+1:]
	}
	title, tags = ParseInlineTags(titlePart)
	return title, body, tags
}

// callbackActions enumerates the valid action verbs for inline-keyboard
// callbacks. Callback data is encoded as `<action>:<ulid>` and decoded by
// ParseCallback. Keeping the set small and explicit lets the dispatcher
// switch on the returned action without a separate validation pass.
var callbackActions = map[string]struct{}{
	"done":     {},
	"active":   {},
	"view":     {},
	"collapse": {},
}

// ulidLength is the canonical 26-character length of a Crockford-base32
// ULID. ParseCallback enforces this shape check so malformed callback
// payloads are rejected before they reach the store layer (which would
// also reject them, but at the cost of a directory scan).
const ulidLength = 26

// ParseCallback decodes the `<action>:<ulid>` payload sent with inline
// keyboard buttons.
//
// Returns the action verb and the ULID on success. The action must be one
// of done / active / view / collapse; the ULID must be exactly 26 chars
// (shape check only — actual existence is verified by store.Resolve).
//
// Errors:
//   - missing colon → "missing colon"
//   - unknown action verb → "unknown action: <verb>"
//   - ULID length != 26 → "invalid ulid length: <n>"
//
// The error messages are deliberately terse — the handler maps them to a
// generic "invalid" toast for the user, but the wrapped text helps when
// debugging from logs.
func ParseCallback(data string) (action, ulid string, err error) {
	idx := strings.IndexByte(data, ':')
	if idx < 0 {
		return "", "", fmt.Errorf("missing colon")
	}
	action = data[:idx]
	ulid = data[idx+1:]
	if _, ok := callbackActions[action]; !ok {
		return "", "", fmt.Errorf("unknown action: %q", action)
	}
	if len(ulid) != ulidLength {
		return "", "", fmt.Errorf("invalid ulid length: %d", len(ulid))
	}
	return action, ulid, nil
}

// prefixLength is the number of leading ULID characters shown in
// Telegram-rendered task rows. Chosen to match the user's typical CLI
// usage where 5 characters is the most common prefix entered for
// store.Resolve and keeps the inline code chip narrow on phone screens.
const prefixLength = 5

// telegramMaxMessage is the hard cap Telegram enforces on a single
// outgoing message (4096 UTF-8 bytes). FormatDetailView truncates the
// body so the rendered HTML stays under this cap with a small safety
// margin.
const telegramMaxMessage = 4096

// truncationMarker is appended to the body when FormatDetailView has to
// trim oversized content. The wording asks the user to look on the
// laptop for the full text, consistent with the "+N more" footer used
// by `/all` browse output.
const truncationMarker = "… (open laptop for full body)"

// htmlEscape wraps html.EscapeString so call sites read uniformly across
// the package. The Telegram Bot API HTML parse mode escapes the same
// metacharacters as the html.EscapeString function: `<`, `>`, `&`, `'`,
// `"` — keeping our renderer aligned with Telegram's parser prevents
// the API from rejecting messages with `Bad Request: can't parse entities`.
func htmlEscape(s string) string {
	return html.EscapeString(s)
}

// prefixID returns the first prefixLength chars of id (or the whole id
// when it is shorter). Matches the shape of display.ShortID but with
// the Telegram-specific width and no allocation surprises when id is
// already short.
func prefixID(id string) string {
	if len(id) <= prefixLength {
		return id
	}
	return id[:prefixLength]
}

// FormatTaskRow renders a compact HTML summary row for a task as shown
// in browse output and after a capture. The layout is:
//
//	{⭐ if active}<code>{prefix5}</code>  {escaped-title}
//	<i>{tag-list}  ·  {recur-marker}{notes-badge}</i>
//
// The leading ⭐ marker is prepended when t.IsActive() so the row
// rendering visibly changes between the active-on and active-off states
// even when the task has no other tags / no recur / no notes. Without
// this marker, toggling active on a plainest-case task produces an
// EditMessage payload identical to the previous render — Telegram then
// rejects the edit with "message is not modified" and the user's tap
// appears to do nothing. See TestFormatTaskRowActiveToggleChangesRendering.
//
// The second `<i>` line is omitted entirely when the task has no tags,
// no recurrence rule, and no notes — so simple captures render as a
// single line. The reserved `active` tag is filtered out via
// display.VisibleTags so it never appears in the user-visible tag list
// (the active state is reflected via the ⭐ marker + button instead).
//
// The row carries no date column today — FormatDetailView is where
// dateFormat matters. If a future revision adds a relative-time chip
// here, take dateFormat as a parameter at that point.
func FormatTaskRow(t model.Task) string {
	var b strings.Builder
	if t.IsActive() {
		b.WriteString("⭐ ")
	}
	b.WriteString("<code>")
	b.WriteString(htmlEscape(prefixID(t.ID)))
	b.WriteString("</code>  ")
	b.WriteString(htmlEscape(t.Title))

	visibleTags := display.VisibleTags(t.Tags)
	hasRecur := t.Recurrence != ""
	hasNotes := t.NoteCount > 0
	if len(visibleTags) == 0 && !hasRecur && !hasNotes {
		return b.String()
	}
	b.WriteString("\n<i>")
	var parts []string
	if len(visibleTags) > 0 {
		parts = append(parts, htmlEscape(strings.Join(visibleTags, ", ")))
	}
	var markers []string
	if hasRecur {
		markers = append(markers, "↻")
	}
	if hasNotes {
		markers = append(markers, fmt.Sprintf("[%d]", t.NoteCount))
	}
	if len(markers) > 0 {
		parts = append(parts, strings.Join(markers, " "))
	}
	b.WriteString(strings.Join(parts, "  ·  "))
	b.WriteString("</i>")
	return b.String()
}

// formatScheduleLine returns the "Schedule:" body for FormatDetailView.
// The result is either `{display-date} ({bucket})` when the stored
// schedule falls into a virtual bucket (today/tomorrow/week/month/
// someday), or just `{display-date}` when the stored ISO date is the
// same as its bucket name (e.g. someday legacy tasks).
//
// The bucket label is appended in parentheses so the user sees both
// "when" (the date) and "where it sorts" (the bucket) without having to
// cross-reference. Mirrors the TUI detail-panel rule.
func formatScheduleLine(stored string, now time.Time, layout string) string {
	displayDate := schedule.FormatDisplay(stored, layout)
	bucket := schedule.Bucket(stored, now)
	if bucket != stored {
		return fmt.Sprintf("%s (%s)", displayDate, bucket)
	}
	return displayDate
}

// FormatDetailView renders the full-detail HTML view shown after a
// `view:<ULID>` callback. Layout mirrors the TUI's detailPanelView:
//
//	<code>{prefix5}</code>  {escaped-title}
//	Schedule: {date} ({bucket})
//	Tags: tag1, tag2
//	Recur: <rule>
//	Created: {rel-date}
//	Notes: N
//
//	<full body with notes, HTML-escaped, newlines preserved>
//
// Conditional lines (Tags / Recur / Notes) are omitted entirely when
// the underlying value is empty/zero. The body is truncated to keep
// the whole message under Telegram's 4096-byte cap, appending
// truncationMarker on overflow.
//
// HTML escaping is applied to every user-supplied substring before it
// reaches the template so titles like `<broken & sad>` render safely.
// Newlines inside the body are preserved — Telegram renders them
// literally in HTML parse mode, which gives notes their familiar
// paragraph layout.
func FormatDetailView(t model.Task, dateFormat string) string {
	now := time.Now()
	return formatDetailViewAt(t, dateFormat, now)
}

// formatDetailViewAt is the testable form of FormatDetailView with an
// injectable "now" — exported helpers should not take time arguments
// (they would leak through too many call sites), but the tests need
// deterministic schedule/relative-date output. Keeping this private
// keeps the public surface narrow.
func formatDetailViewAt(t model.Task, dateFormat string, now time.Time) string {
	var b strings.Builder
	b.WriteString("<code>")
	b.WriteString(htmlEscape(prefixID(t.ID)))
	b.WriteString("</code>  ")
	b.WriteString(htmlEscape(t.Title))
	b.WriteString("\n")

	b.WriteString("Schedule: ")
	b.WriteString(htmlEscape(formatScheduleLine(t.Schedule, now, dateFormat)))
	b.WriteString("\n")

	if visibleTags := display.VisibleTags(t.Tags); len(visibleTags) > 0 {
		b.WriteString("Tags: ")
		b.WriteString(htmlEscape(strings.Join(visibleTags, ", ")))
		b.WriteString("\n")
	}
	if t.Recurrence != "" {
		b.WriteString("Recur: ")
		b.WriteString(htmlEscape(t.Recurrence))
		b.WriteString("\n")
	}
	if created := display.FormatRelDate(now, t.CreatedAt, dateFormat); created != "" {
		b.WriteString("Created: ")
		b.WriteString(htmlEscape(created))
		b.WriteString("\n")
	}
	if t.NoteCount > 0 {
		b.WriteString(fmt.Sprintf("Notes: %d\n", t.NoteCount))
	}

	header := b.String()
	if t.Body == "" {
		// Strip the trailing newline so the empty-body case doesn't
		// leave a dangling blank line at the bottom of the message.
		out := strings.TrimRight(header, "\n")
		// Even with no body, a pathological title can push the header
		// past Telegram's 4096-byte cap. safeTruncate respects UTF-8
		// rune boundaries and HTML entity boundaries so the message
		// still parses cleanly on Telegram's side.
		if len(out) > telegramMaxMessage {
			return safeTruncate(out, telegramMaxMessage)
		}
		return out
	}

	bodyEscaped := htmlEscape(t.Body)
	separator := "\n"
	// Available budget for the body fragment = max - header - separator
	// - the truncation marker (which we may append). We use the marker
	// length unconditionally as a safe upper bound for the budget so we
	// don't have to re-check after appending.
	budget := telegramMaxMessage - len(header) - len(separator) - len(truncationMarker)
	if budget < 0 {
		// Header alone exceeds the cap (pathological — would only
		// happen with an absurdly long title). Use safeTruncate so the
		// cut respects UTF-8 rune boundaries AND HTML entity boundaries;
		// a raw byte slice could land mid-rune or mid-`&amp;` and make
		// Telegram reject the message with "can't parse entities".
		if len(header) > telegramMaxMessage {
			return safeTruncate(header, telegramMaxMessage)
		}
		return strings.TrimRight(header, "\n")
	}
	if len(bodyEscaped) <= budget+len(truncationMarker) {
		// Body fits without truncation; we can use the full slack
		// because we never have to append the marker.
		return header + separator + bodyEscaped
	}
	return header + separator + safeTruncate(bodyEscaped, budget) + truncationMarker
}

// safeTruncate slices s to at most maxBytes bytes without producing an
// invalid UTF-8 sequence or splitting a Telegram HTML entity (`&amp;`,
// `&lt;`, `&gt;`, `&#34;`, `&#39;`). Telegram's HTML parser rejects the
// entire message with `Bad Request: can't parse entities` if the cut
// lands inside an entity, so we walk back to the last safe boundary.
//
// Algorithm: byte-slice to maxBytes, then back up over any incomplete
// UTF-8 rune at the tail (utf8.DecodeLastRuneInString → RuneError when
// truncated), and back up further if the tail is inside an HTML entity
// (a `&` followed by characters but no closing `;` within the tail).
func safeTruncate(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	// Back up past any incomplete UTF-8 multibyte rune so we never emit
	// invalid UTF-8.
	for cut > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:cut])
		if r == utf8.RuneError && size == 1 {
			cut--
			continue
		}
		break
	}
	// Back up past any open `&...` entity that lacks its closing `;`
	// within the truncated portion. We scan backward from `cut` for a
	// `&`; if we find one before a `;` (or before a non-entity char like
	// whitespace), drop everything from that `&` onward.
	for i := cut - 1; i >= 0 && cut-i <= 8; i-- {
		c := s[i]
		if c == ';' {
			break // entity already closed → safe
		}
		if c == '&' {
			cut = i // truncate before the partial entity
			break
		}
	}
	return s[:cut]
}

// BuildSummaryKeyboard returns the inline keyboard shown beneath a
// task summary row: a single row with [Done] [Active] [Details] buttons.
// The callback payloads encode the task's full ULID via ParseCallback's
// reverse so the dispatch layer can resolve the task without keeping any
// bot-side state. The label texts use emoji so the buttons read clearly
// at a phone-screen glance.
func BuildSummaryKeyboard(taskID string) InlineKeyboard {
	return InlineKeyboard{
		{
			{Text: "✅ Done", CallbackData: "done:" + taskID},
			{Text: "⭐ Active", CallbackData: "active:" + taskID},
			{Text: "📄 Details", CallbackData: "view:" + taskID},
		},
	}
}

// BuildDetailKeyboard returns the inline keyboard shown beneath the
// expanded detail view: a single row with [Done] [Active] [Collapse].
//
// Button order deliberately MIRRORS BuildSummaryKeyboard: Done and Active
// keep their exact slots and Collapse takes over the slot Details held.
// Expanding a task edits the message in place, so any reordering here
// moves the two write buttons under the user's thumb mid-tap — tapping
// Details and then Done would hit whatever slid into Done's old position.
// Details is intentionally omitted — the user is already in the detail
// view, so a second "Details" button would be a no-op.
func BuildDetailKeyboard(taskID string) InlineKeyboard {
	return InlineKeyboard{
		{
			{Text: "✅ Done", CallbackData: "done:" + taskID},
			{Text: "⭐ Active", CallbackData: "active:" + taskID},
			{Text: "⬆ Collapse", CallbackData: "collapse:" + taskID},
		},
	}
}

// FormatDoneRow renders the post-completion message that replaces a
// task's summary row after the user taps the Done button. Layout:
//
//	✅ <s><code>{prefix5}</code>  {escaped-title}</s>
//	↻ next: {nextDate}        (only when nextDate != "")
//
// The strike-through is applied to the prefix + title so the visual
// difference between "open" and "done" is immediately legible. The
// optional next-date line is only added when CompleteAndSpawn produced
// a follow-up task; for non-recurring tasks the second line is omitted.
// The caller passes the pre-formatted date string in the configured
// layout — this function does not touch dateFormat — so the rendering
// stays deterministic across timezone-edge cases.
func FormatDoneRow(t model.Task, nextDate string) string {
	row := fmt.Sprintf("✅ <s><code>%s</code>  %s</s>",
		htmlEscape(prefixID(t.ID)),
		htmlEscape(t.Title),
	)
	if nextDate == "" {
		return row
	}
	return row + "\n↻ next: " + htmlEscape(nextDate)
}

// FormatEmptyBucket returns the single-line "nothing 🎉" message sent
// when a browse command finds no matching tasks. The label is the
// human-readable bucket name (e.g. "Today", "Week") and is shown in
// bold so the empty-state message visually parallels the per-task rows
// it would otherwise replace.
func FormatEmptyBucket(label string) string {
	return fmt.Sprintf("<b>%s</b> — nothing 🎉", htmlEscape(label))
}

