package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/store"
)

// validSchedules contains the allowed named schedule values.
var validSchedules = map[string]bool{
	"today":    true,
	"tomorrow": true,
	"week":     true,
	"someday":  true,
}

// isoDateLayout matches ISO date format YYYY-MM-DD.
const isoDateLayout = "2006-01-02"

// isISODate checks whether s is a valid YYYY-MM-DD date string.
func isISODate(s string) bool {
	_, err := time.Parse(isoDateLayout, s)
	return err == nil
}

// validateSchedule checks that s is a valid schedule value:
// one of today, tomorrow, week, someday, or an ISO date (YYYY-MM-DD).
func validateSchedule(s string) error {
	if validSchedules[s] {
		return nil
	}
	if isISODate(s) {
		return nil
	}
	return fmt.Errorf("invalid schedule %q: must be today, tomorrow, week, someday, or ISO date (YYYY-MM-DD)", s)
}

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
