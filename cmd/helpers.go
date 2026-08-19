package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/store"
)

// loadConfig resolves the monolog data directory and applies
// <dir>/.monolog/config.json to the in-memory config package, returning the
// resolved path. Errors are non-fatal — a missing or malformed config
// silently leaves the package defaults in place.
//
// Split out of openStore so read-only commands (the `status` subcommands)
// can pick up persisted settings without paying for the store handle. The
// config package keeps its values in package-level state that only Load
// populates, so a command that skips this reports built-in defaults rather
// than what the user actually configured.
func loadConfig() string {
	repoPath := monologDir()
	_ = config.Load(repoPath)
	return repoPath
}

// openStore resolves the monolog data directory, loads user config, and opens
// the task store. Returns the store, the repo path (for git operations), and
// any error.
func openStore() (*store.Store, string, error) {
	repoPath := loadConfig()
	tasksDir := filepath.Join(repoPath, ".monolog", "tasks")
	s, err := store.New(tasksDir)
	if err != nil {
		return nil, "", fmt.Errorf("open store: %w", err)
	}
	return s, repoPath, nil
}
