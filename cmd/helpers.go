package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/mmaksmas/monolog/internal/store"
)

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
