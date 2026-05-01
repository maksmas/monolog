package email

import (
	"path/filepath"
	"time"
)

// ArchiveTimeout caps how long we wait for the Gmail archive call after a
// successful done. Kept short so a flaky network never makes either the
// CLI's `monolog done` or the TUI's `d` keypress hang.
const ArchiveTimeout = 5 * time.Second

// SyncTimeout caps how long one Gmail-sync run may take. Larger than
// ArchiveTimeout because a sync may issue many list+get calls back-to-back.
const SyncTimeout = 30 * time.Second

// TokenPathFor derives the on-disk token path from the client-secrets path.
// The token sits next to the client-secrets JSON in
// $XDG_CONFIG_HOME/monolog/, keeping both files outside the git-synced
// monolog repo so OAuth secrets are never accidentally committed across
// devices. Returns "" when clientSecretsPath is empty so callers can
// short-circuit unauthenticated flows.
//
// Takes a path string rather than config.EmailConfig so internal/email
// stays decoupled from internal/config (mirrors the rule applied in
// schedule/display/model).
func TokenPathFor(clientSecretsPath string) string {
	if clientSecretsPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(clientSecretsPath), "gmail_token.json")
}
