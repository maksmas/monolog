package cmd

import (
	"fmt"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newSlackStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slack-status",
		Short: "Show Slack integration status",
		Long: `Print workspace, token source, enabled flag, and the number of
tasks ingested from Slack so far.`,
		Args: cobra.NoArgs,
		RunE: runSlackStatus,
	}
	return cmd
}

// runSlackStatus prints the current Slack integration state. Does not contact
// Slack — purely a local snapshot.
func runSlackStatus(cmd *cobra.Command, args []string) error {
	s, _, err := openStore()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	slackCfg := config.Slack()

	// Token source is derived from SlackToken: env > file > none. Token value
	// itself is never printed; we only care about the source here.
	token, source, tokErr := config.SlackToken()
	if tokErr != nil {
		return fmt.Errorf("read slack token: %w", tokErr)
	}
	if source == "" {
		source = "none"
	}

	workspace := slackCfg.Workspace
	if workspace == "" {
		workspace = "(not set)"
	}

	// Active reflects whether Slack polling/syncing will actually run. Token
	// presence from any source is sufficient — the slack.enabled flag is
	// only a toggle for the file-based token. An env-var user bypasses the
	// enabled flag by design, so show them as active even when the flag is
	// false.
	active := token != ""

	// Count on-disk tasks sourced from Slack. Iterates the whole store; fine
	// for a personal backlog size. We do not filter by status — completed
	// slack tasks still count toward "ingested so far".
	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	var ingested int
	for _, t := range tasks {
		if t.Source == "slack" {
			ingested++
		}
	}

	fmt.Fprintf(out, "Workspace:       %s\n", workspace)
	fmt.Fprintf(out, "Token source:    %s\n", source)
	fmt.Fprintf(out, "Enabled:         %t\n", slackCfg.Enabled)
	fmt.Fprintf(out, "Active:          %t\n", active)
	fmt.Fprintf(out, "Ingested so far: %d\n", ingested)
	return nil
}
