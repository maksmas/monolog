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
	"regexp"
	"strings"
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
