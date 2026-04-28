package model

import (
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Task represents a single backlog item stored as a JSON file.
type Task struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Body        string   `json:"body,omitempty"`
	Source      string   `json:"source"`
	SourceID    string   `json:"source_id,omitempty"`
	Status      string   `json:"status"`
	Position    float64  `json:"position"`
	Schedule    string   `json:"schedule"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	CompletedAt string   `json:"completed_at,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Recurrence  string   `json:"recurrence,omitempty"`
	NoteCount   int      `json:"note_count,omitempty"`
}

// ActiveTag is the reserved tag name used to mark a task as currently being worked on.
const ActiveTag = "active"

// IsActive reports whether the task is marked as active.
func (t Task) IsActive() bool {
	for _, tag := range t.Tags {
		if tag == ActiveTag {
			return true
		}
	}
	return false
}

// SetActive adds or removes the ActiveTag from the task's tags.
// Adding is idempotent (no duplicate), and removing preserves the order of other tags.
func (t *Task) SetActive(on bool) {
	if on {
		if !t.IsActive() {
			t.Tags = append(t.Tags, ActiveTag)
		}
		return
	}
	// remove
	out := t.Tags[:0]
	for _, tag := range t.Tags {
		if tag != ActiveTag {
			out = append(out, tag)
		}
	}
	t.Tags = out
}

// SanitizeTags splits a comma-separated string into tags, trimming whitespace,
// filtering out empty strings, and stripping the reserved "active" tag so
// users cannot bypass the dedicated active toggle.
func SanitizeTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	var tags []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && p != ActiveTag {
			tags = append(tags, p)
		}
	}
	return tags
}

// CollectTags extracts a sorted, deduplicated list of tag strings from the given tasks.
// The reserved ActiveTag is excluded from the result.
func CollectTags(tasks []Task) []string {
	seen := make(map[string]struct{})
	for _, t := range tasks {
		for _, tag := range t.Tags {
			if tag != ActiveTag {
				seen[tag] = struct{}{}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

// ParseTitleTag checks if the title starts with a "tag: ..." pattern where the
// tag is a known tag. It returns the matching tag name, or an empty string if
// no match is found. Only single-word tags match (no spaces or colons in the
// candidate). At least one whitespace character must follow the colon.
// Callers are expected to pass knownTags from CollectTags, which already
// excludes ActiveTag.
func ParseTitleTag(title string, knownTags []string) string {
	idx := strings.IndexByte(title, ':')
	if idx <= 0 {
		return ""
	}
	candidate := title[:idx]
	// reject candidates containing spaces (multi-word prefixes)
	if strings.ContainsAny(candidate, " \t") {
		return ""
	}
	// require at least one whitespace character after the colon
	rest := title[idx+1:]
	if len(rest) == 0 || (rest[0] != ' ' && rest[0] != '\t') {
		return ""
	}
	// look up candidate in known tags
	for _, tag := range knownTags {
		if tag == candidate {
			return candidate
		}
	}
	return ""
}

// AutoTag checks if the title matches a known tag prefix and merges the
// auto-tag into the existing tag slice, avoiding duplicates. It returns the
// (possibly extended) tag slice.
func AutoTag(title string, knownTags []string, existingTags []string) []string {
	autoTag := ParseTitleTag(title, knownTags)
	if autoTag == "" {
		return existingTags
	}
	for _, t := range existingTags {
		if t == autoTag {
			return existingTags
		}
	}
	return append(existingTags, autoTag)
}

// Initials returns the lowercase first letters of each whitespace-separated
// word in the title. For example, "Fix login bug" → "flb".
func Initials(title string) string {
	words := strings.Fields(title)
	var b strings.Builder
	for _, w := range words {
		for _, r := range w {
			b.WriteRune(r)
			break
		}
	}
	return strings.ToLower(b.String())
}

// maxSuggestions is the maximum number of tag suggestions returned by FilterTags.
const maxSuggestions = 5

// FilterTags returns tag suggestions for the current input field value.
// It extracts the fragment after the last comma, filters known tags by
// case-insensitive prefix match, excludes tags already present in the field,
// and caps the result at maxSuggestions entries.
func FilterTags(known []string, field string) []string {
	// Split the field by commas to get already-entered tags and the current fragment.
	parts := strings.Split(field, ",")
	fragment := strings.TrimSpace(parts[len(parts)-1])
	if fragment == "" {
		return nil
	}

	// Collect already-entered tags (everything before the last comma).
	entered := make(map[string]struct{})
	for _, p := range parts[:len(parts)-1] {
		tag := strings.TrimSpace(p)
		if tag != "" {
			entered[strings.ToLower(tag)] = struct{}{}
		}
	}

	fragmentLower := strings.ToLower(fragment)
	var result []string
	for _, tag := range known {
		tagLower := strings.ToLower(tag)
		if _, already := entered[tagLower]; already {
			continue
		}
		if strings.HasPrefix(tagLower, fragmentLower) {
			result = append(result, tag)
			if len(result) >= maxSuggestions {
				break
			}
		}
	}
	return result
}

// NewID generates a new ULID string.
// It returns an error if the random source fails.
func NewID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ULID: %w", err)
	}
	return id.String(), nil
}
