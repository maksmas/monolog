package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/spf13/cobra"
)

func newSlackLogoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slack-logout",
		Short: "Disconnect monolog from Slack",
		Long: `Remove the stored Slack user OAuth token and disable polling.

Deletes $MONOLOG_DIR/.monolog/slack_token if present and sets
slack.enabled=false in config.json. Does not revoke the token on Slack's
side; use the Slack app management UI for that.`,
		Args: cobra.NoArgs,
		RunE: runSlackLogout,
	}
	return cmd
}

// runSlackLogout executes the logout flow. Split out so tests can invoke it
// directly without cobra plumbing.
func runSlackLogout(cmd *cobra.Command, args []string) error {
	repoPath := monologDir()
	// Load so in-process state stays consistent after we flip enabled=false.
	_ = config.Load(repoPath)

	out := cmd.OutOrStdout()

	tokenPath := filepath.Join(repoPath, ".monolog", "slack_token")
	_, statErr := os.Stat(tokenPath)
	switch {
	case statErr == nil:
		if err := os.Remove(tokenPath); err != nil {
			return fmt.Errorf("remove token file: %w", err)
		}
	case os.IsNotExist(statErr):
		fmt.Fprintln(out, "Already disconnected.")
		return nil
	default:
		return fmt.Errorf("stat token file: %w", statErr)
	}

	if err := config.SetSlackEnabled(repoPath, false); err != nil {
		return fmt.Errorf("disable slack integration: %w", err)
	}

	fmt.Fprintln(out, "Slack disconnected.")
	// If MONOLOG_SLACK_TOKEN is set in the environment, the env value still
	// wins over the now-missing on-disk token file: `slack-sync` would still
	// hit Slack, and a future `slack-login` would re-enable polling. Surface
	// a warning so the user can unset the var if they really want to
	// disconnect entirely.
	if os.Getenv("MONOLOG_SLACK_TOKEN") != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: MONOLOG_SLACK_TOKEN is still set in the environment — unset it for a full disconnect.")
	}
	return nil
}
