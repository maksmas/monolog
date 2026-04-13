package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mmaksmas/monolog/internal/store"
)

// sanitizeTags splits a comma-separated string into tags, trimming whitespace
// and filtering out empty strings.
func sanitizeTags(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	var tags []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			tags = append(tags, p)
		}
	}
	return tags
}

// openStore resolves the monolog data directory and opens the task store.
// Returns the store, the repo path (for git operations), and any error.
func openStore() (*store.Store, string, error) {
	repoPath := monologDir()
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	s, err := store.New(tasksDir)
	if err != nil {
		return nil, "", fmt.Errorf("open store: %w", err)
	}
	return s, repoPath, nil
}
