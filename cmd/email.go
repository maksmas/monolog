package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/email"
	"github.com/spf13/cobra"
)

// emailClientFactory builds a Gmail client + token loader for the email
// subcommands. Tests swap this var with a fake to exercise the cobra wiring
// without touching the network or the user's filesystem.
//
// The factory returns a Gmail client and the token's expiry (used by
// `email status`) — the expiry is derived from the persisted token, not
// from a network call, so tests can construct it directly.
var emailClientFactory = realEmailClientFactory

// realEmailClientFactory is the production implementation of
// emailClientFactory. It loads the persisted token, wraps it with a
// refreshing http.Client, and constructs a Gmail client backed by the
// real *gmail.Service.
func realEmailClientFactory(ctx context.Context, ec config.EmailConfig) (email.Gmail, time.Time, error) {
	tok, err := email.LoadToken(tokenPathFor(ec))
	if err != nil {
		return nil, time.Time{}, err
	}
	httpClient, err := email.HTTPClient(ctx, ec.ClientSecretsPath, tokenPathFor(ec))
	if err != nil {
		return nil, time.Time{}, err
	}
	g, err := email.NewClient(ctx, httpClient)
	if err != nil {
		return nil, time.Time{}, err
	}
	return g, tok.Expiry, nil
}

// tokenPathFor derives the on-disk token path from the email config. The
// token sits next to the client-secrets JSON in $XDG_CONFIG_HOME/monolog/,
// keeping both files outside the git-synced monolog repo so OAuth secrets
// are never accidentally committed across devices.
func tokenPathFor(ec config.EmailConfig) string {
	if ec.ClientSecretsPath == "" {
		return ""
	}
	// Place gmail_token.json next to gmail_credentials.json. The defaults
	// resolved by config.Email() already point at $XDG_CONFIG_HOME/monolog/
	// so this naturally lands in the right place.
	return filepath.Join(filepath.Dir(ec.ClientSecretsPath), "gmail_token.json")
}

func newEmailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "email",
		Short: "Gmail integration: import labeled emails as tasks",
		Long:  "Import emails labeled with the configured Gmail label as monolog tasks, and archive them in Gmail when the task is completed. Run 'monolog email auth' once to authorize, then 'monolog email sync' to import.",
	}

	cmd.AddCommand(newEmailSyncCmd())
	cmd.AddCommand(newEmailAuthCmd())
	cmd.AddCommand(newEmailStatusCmd())
	return cmd
}

// newEmailSyncCmd implements `monolog email sync` — fetch labeled messages
// from Gmail and import them as monolog tasks in a single batch commit.
func newEmailSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Import new labeled Gmail messages as tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, repoPath, err := openStore()
			if err != nil {
				return err
			}
			ec := config.Email()
			if !ec.Enabled {
				return fmt.Errorf("email integration is disabled — run 'monolog email auth' to enable")
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			g, _, err := emailClientFactory(ctx, ec)
			if err != nil {
				return err
			}

			res := email.Sync(ctx, g, s, repoPath, email.SyncOptions{
				Label:      ec.Label,
				MaxPerSync: ec.MaxPerSync,
				Now:        time.Now(),
				Writer:     cmd.ErrOrStderr(),
			})
			if res.Err != nil {
				return res.Err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created %d task(s)\n", res.Created)
			return nil
		},
	}
}

// newEmailAuthCmd implements `monolog email auth` — run the interactive
// browser-redirect OAuth flow once to obtain a refresh token and persist it
// to disk, then flip enabled=true so subsequent runs pick up the feature.
func newEmailAuthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Run the OAuth flow to authorize Gmail access",
		Long:  "Opens a browser window to complete Google OAuth consent. Saves the refresh token under $XDG_CONFIG_HOME/monolog/gmail_token.json and enables email integration in config.json.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ec := config.Email()
			tokPath := tokenPathFor(ec)

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			if err := emailAuthorize(ctx, ec.ClientSecretsPath, tokPath); err != nil {
				return err
			}

			// Persist enabled=true so the user doesn't have to hand-edit
			// config.json after the OAuth flow.
			repoPath := monologDir()
			ec.Enabled = true
			if err := config.SaveEmail(repoPath, ec); err != nil {
				// The token is saved already; surface the warning but treat
				// this as non-fatal so the user can still 'email sync'.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not enable email block in config.json: %v\n", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "authorized; token saved to %s\n", tokPath)
			return nil
		},
	}
}

// emailAuthorize is a swappable seam pointing at email.Authorize so tests
// can exercise the auth command's surrounding wiring without launching a
// browser. The real interactive flow is exercised manually in Task 12.
var emailAuthorize = email.Authorize

// newEmailStatusCmd implements `monolog email status` — print a one-shot
// summary of auth state and configured options. Does NOT show "last sync
// time"; the user can use `git log` for that history.
func newEmailStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show email integration status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ec := config.Email()
			out := cmd.OutOrStdout()

			// Auth state — we only inspect the token file directly here so
			// we don't burn an HTTP refresh just to render status.
			tokPath := tokenPathFor(ec)
			tok, err := email.LoadToken(tokPath)
			switch {
			case err == nil:
				fmt.Fprintf(out, "auth: token loaded, expires %s\n", tok.Expiry.UTC().Format(time.RFC3339))
			case email.IsAuthMissing(err):
				fmt.Fprintln(out, "auth: not authorized — run 'monolog email auth'")
			default:
				fmt.Fprintf(out, "auth: error reading token: %v\n", err)
			}

			fmt.Fprintf(out, "enabled: %t\n", ec.Enabled)
			fmt.Fprintf(out, "label: %s\n", ec.Label)
			fmt.Fprintf(out, "interval: %s\n", ec.SyncInterval)
			fmt.Fprintf(out, "max_per_sync: %d\n", ec.MaxPerSync)
			fmt.Fprintf(out, "client_secrets_path: %s\n", ec.ClientSecretsPath)
			fmt.Fprintf(out, "token_path: %s\n", tokPath)
			return nil
		},
	}
}

