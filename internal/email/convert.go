package email

import (
	"fmt"
	"html"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
)

// snippetMaxLen is the hard cap on the snippet portion of the body. When a
// snippet is longer than this, ToTask appends a "…" suffix to signal the
// truncation; shorter snippets are passed through verbatim with no suffix.
const snippetMaxLen = 200

// truncatedSuffix is the character appended to a truncated snippet. The
// ellipsis is a single Unicode codepoint so it adds one rune (three bytes
// in UTF-8) without inflating the visible width.
const truncatedSuffix = "…"

// subjectPrefixPattern strips chained reply / forward prefixes from a
// subject. Matches one-or-more of "Re: ", "Fwd: ", "Fw: ", "FW: " etc.,
// case-insensitive, with arbitrary intervening whitespace. Anchored at the
// start so only the leading run is removed.
var subjectPrefixPattern = regexp.MustCompile(`(?i)^((re|fwd?|fw):\s*)+`)

// cleanSubject returns the subject with leading reply/forward prefixes
// stripped and surrounding whitespace trimmed. An empty (or whitespace-only)
// result is rendered as "(no subject)" so the title is never blank.
func cleanSubject(subject string) string {
	s := strings.TrimSpace(subject)
	s = subjectPrefixPattern.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no subject)"
	}
	return s
}

// parseSender extracts a human-friendly name from a From: header. Falls back
// to the bare address when no display name is set, and to "unknown" when
// the header cannot be parsed at all.
func parseSender(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return "unknown"
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return "unknown"
	}
	if name := strings.TrimSpace(addr.Name); name != "" {
		return name
	}
	if addr.Address != "" {
		return addr.Address
	}
	return "unknown"
}

// truncateSnippet HTML-unescapes the snippet, trims surrounding whitespace,
// and hard-caps the result at snippetMaxLen runes. When truncation occurs
// the ellipsis suffix is appended; otherwise the snippet is returned as-is
// (no suffix).
func truncateSnippet(snippet string) string {
	s := html.UnescapeString(snippet)
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Count runes, not bytes — a 200-character cap means "200 visible
	// characters" not "200 bytes".
	runes := []rune(s)
	if len(runes) <= snippetMaxLen {
		return s
	}
	return string(runes[:snippetMaxLen]) + truncatedSuffix
}

// buildBody assembles the task body with a "From:" line, the Gmail web URL
// for the message, and (optionally) the snippet. The URL omits "/u/0/" so
// it works regardless of which Google account is currently signed in —
// Gmail redirects to the right account based on the message ID.
//
// When the snippet is empty, the body ends after the URL line — no trailing
// blank lines are emitted. With a snippet, a single blank line separates
// the URL from the snippet body.
func buildBody(sender, msgID, snippet string) string {
	header := "From: " + sender + "\nhttps://mail.google.com/mail/#all/" + msgID
	if snippet == "" {
		return header
	}
	return header + "\n\n" + snippet
}

// ToTask converts a Gmail Message into a model.Task ready for Store.Create.
// The conversion is pure: same inputs (including `now`) produce the same
// title/body/schedule, but the ID is freshly generated each call (ULIDs
// embed a timestamp + random suffix so two calls with the same `now` still
// differ).
//
// Failure modes are absorbed into sensible defaults rather than returning
// errors:
//   - empty / whitespace-only subject after prefix stripping → "(no subject)"
//   - malformed From: header → "unknown"
//   - empty snippet → body has only the From: + URL lines (no extra blank
//     lines)
//
// The only error path is ULID generation (model.NewID), which only fails if
// crypto/rand is broken — we surface that error so the caller can decide
// whether to skip-and-warn or abort.
func ToTask(msg *Message, now time.Time) (model.Task, error) {
	if msg == nil {
		return model.Task{}, fmt.Errorf("email: nil message")
	}
	id, err := model.NewID()
	if err != nil {
		return model.Task{}, err
	}
	// schedule.Parse with the "today" bucket name does not depend on the
	// configured user-facing layout, so passing "" is correct here — we
	// stay decoupled from internal/config (per the package contract).
	sched, err := schedule.Parse(schedule.Today, now, "")
	if err != nil {
		// schedule.Parse cannot fail on a bucket name input, but guard
		// defensively rather than panic in case the package contract
		// changes.
		return model.Task{}, fmt.Errorf("email: schedule today: %w", err)
	}
	nowStr := now.UTC().Format(time.RFC3339)
	return model.Task{
		ID:        id,
		Title:     cleanSubject(msg.Subject),
		Body:      buildBody(parseSender(msg.From), msg.ID, truncateSnippet(msg.Snippet)),
		Source:    "gmail",
		SourceID:  msg.ID,
		Status:    "open",
		Schedule:  sched,
		CreatedAt: nowStr,
		UpdatedAt: nowStr,
		Tags:      []string{"email"},
	}, nil
}
