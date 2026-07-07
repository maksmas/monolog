package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/email"
	"github.com/maksmas/monolog/internal/store"
)

// emailSyncResult is the bubbletea message dispatched when an email-sync
// goroutine finishes. created is the number of new tasks imported in this
// run; err is non-nil for a fatal failure (auth missing, list error, commit
// error). Per-task warnings are emitted to a sink writer set up by
// emailSyncCmd and never appear here.
type emailSyncResult struct {
	created int
	err     error
}

// emailNoOpMsg is returned by emailSyncCmd when the feature is disabled. It
// is silently ignored by Update so callers can always batch the cmd into a
// tea.Batch without a conditional.
type emailNoOpMsg struct{}

// emailTickMsg is dispatched by emailTickCmd after the configured sync
// interval elapses. The Update handler responds by re-arming the ticker AND
// dispatching another email sync — a self-rescheduling loop that runs as
// long as the TUI is alive and email is enabled.
type emailTickMsg struct{}

// archiveResult is dispatched by archiveEmailCmd after an archive call to
// Gmail completes. err is non-nil when the call failed; the Update handler
// flashes either "email archived" or "archive failed: <err>" but does NOT
// roll back the underlying done — archive is always non-fatal.
type archiveResult struct {
	err error
}

// archiveEmailCmd removes the INBOX label from the given Gmail message ID
// in the background, with email.ArchiveTimeout as the context deadline.
// Returns archiveResult so the Update handler can flash status. Returns
// nil when email is disabled or sourceID is empty so callers can dispatch
// unconditionally.
func (m *Model) archiveEmailCmd(sourceID string) tea.Cmd {
	if !m.emailEnabled || sourceID == "" {
		return nil
	}
	return func() tea.Msg {
		ec := config.Email()
		ctx, cancel := context.WithTimeout(context.Background(), email.ArchiveTimeout)
		defer cancel()
		g, err := emailClientBuilder(ctx, ec)
		if err != nil {
			return archiveResult{err: err}
		}
		if err := g.ArchiveLabel(ctx, sourceID); err != nil {
			return archiveResult{err: err}
		}
		return archiveResult{}
	}
}

// emailClientBuilder constructs a Gmail client from the on-disk OAuth state.
// Tests swap this seam to inject a fake; the production implementation
// resolves token + http client + gmail.Service the same way cmd/email does.
//
// The seam takes the EmailConfig snapshot the TUI captured at startup so it
// is keyed off the same paths the user configured (token next to client
// secrets) without re-reading config.json.
var emailClientBuilder = realEmailClientBuilder

// realEmailClientBuilder loads the persisted token, wraps it in an
// auto-refreshing http.Client, and returns a Gmail client. Returns an error
// if the token is missing — the caller surfaces a "run monolog email auth"
// hint via the wrapped error message.
func realEmailClientBuilder(ctx context.Context, ec config.EmailConfig) (email.Gmail, error) {
	tokenPath := email.TokenPathFor(ec.ClientSecretsPath)
	if _, err := email.LoadToken(tokenPath); err != nil {
		return nil, err
	}
	httpClient, err := email.HTTPClient(ctx, ec.ClientSecretsPath, tokenPath)
	if err != nil {
		return nil, err
	}
	return email.NewClient(ctx, httpClient)
}

// emailSyncCmd runs an email sync in the background and returns the result
// as a tea.Msg. When email integration is disabled the cmd returns a
// no-op message immediately so callers can batch it unconditionally.
//
// The store and repoPath are captured at call time. The cmd is safe to
// dispatch even while another email sync is in flight — the Update handler
// flips emailSyncing back to false on every result so a stuck "syncing"
// state cannot persist.
func (m *Model) emailSyncCmd() tea.Cmd {
	if !m.emailEnabled {
		return func() tea.Msg { return emailNoOpMsg{} }
	}
	repoPath := m.repoPath
	storeRef := m.store
	label := m.emailLabel
	maxPerSync := m.emailMaxPerSync
	return func() tea.Msg {
		// Re-read config inside the goroutine so the seam can consult the
		// freshest paths; the snapshot on Model only carries enabled/label
		// /interval and not the underlying file paths used by the builder.
		ec := config.Email()
		ctx, cancel := context.WithTimeout(context.Background(), email.SyncTimeout)
		defer cancel()
		g, err := emailClientBuilder(ctx, ec)
		if err != nil {
			return emailSyncResult{err: err}
		}
		res := runEmailSync(ctx, g, storeRef, repoPath, label, maxPerSync)
		return emailSyncResult{created: res.Created, err: res.Err}
	}
}

// runEmailSync is a thin wrapper over email.Sync that exists so tests can
// inject a fixed Now without requiring a clock seam on Model. Production
// callers pass time.Now().
var runEmailSync = func(ctx context.Context, g email.Gmail, s *store.Store, repoPath, label string, maxPerSync int) email.SyncResult {
	return email.Sync(ctx, g, s, repoPath, email.SyncOptions{
		Label:      label,
		MaxPerSync: maxPerSync,
		Now:        time.Now(),
		// Per-task warnings are dropped — the TUI surfaces fatal errors via
		// the status bar instead. A future enhancement could route these
		// into a structured log file.
		Writer: nil,
	})
}

// emailTickCmd returns a tea.Cmd that fires emailTickMsg after the given
// interval. It returns nil when interval <= 0 so callers can include the
// result in a tea.Batch unconditionally — tea.Batch silently drops nil
// entries. The Update handler is what re-arms the next tick.
func (m *Model) emailTickCmd(interval time.Duration) tea.Cmd {
	if !m.emailEnabled || interval <= 0 {
		return nil
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return emailTickMsg{} })
}
