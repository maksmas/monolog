package model

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Task represents a single backlog item stored as a JSON file.
type Task struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	Source    string   `json:"source"`
	Status    string   `json:"status"`
	Position  float64  `json:"position"`
	Schedule  string   `json:"schedule"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	Tags      []string `json:"tags,omitempty"`
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

// NewID generates a new ULID string.
// It returns an error if the random source fails.
func NewID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ULID: %w", err)
	}
	return id.String(), nil
}
