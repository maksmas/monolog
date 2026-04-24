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
	s, repoPath, err := openStore()
	if err != nil {
		return err
	}
	// openStore calls config.Load; keep repoPath to stay symmetric if we ever
	// need it below.
	_ = repoPath

	out := cmd.OutOrStdout()
	slackCfg := config.Slack()

	// Token source is derived from SlackToken: env > file > none. Token value
	// itself is never printed; we only care about the source here.
	_, source, tokErr := config.SlackToken()
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
	fmt.Fprintf(out, "Ingested so far: %d\n", ingested)
	return nil
}
