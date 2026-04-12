package model

import (
	"crypto/rand"
	"fmt"
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
// It returns an error if the random source fails.
func NewID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ULID: %w", err)
	}
	return id.String(), nil
}
