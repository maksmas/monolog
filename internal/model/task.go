package model

import (
	"crypto/rand"
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

// NewID generates a new ULID string.
func NewID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
