package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/store"
)

// openStore resolves the monolog data directory, loads user config, and opens
// the task store. Returns the store, the repo path (for git operations), and
// any error.
func openStore() (*store.Store, string, error) {
	repoPath := monologDir()
	// Load config.json so date format and theme are applied for this session.
	// Errors are non-fatal — missing or malformed config silently uses defaults.
	_ = config.Load(repoPath)
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	s, err := store.New(tasksDir)
	if err != nil {
		return nil, "", fmt.Errorf("open store: %w", err)
	}
	return s, repoPath, nil
}
