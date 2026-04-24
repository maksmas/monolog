package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/slack"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

// slackSyncTimeout bounds a single headless poll so cron jobs never hang on a
// wedged Slack API call. Thirty seconds comfortably covers full pagination
// under Tier 3 rate limits.
const slackSyncTimeout = 30 * time.Second

func newSlackSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slack-sync",
		Short: "Ingest Slack saved items into monolog (headless)",
		Long: `Poll Slack's saved-items API once and ingest any new messages as
monolog tasks. Suitable for cron — exits 0 even when there is nothing new.

Requires a token configured via "monolog slack-login" or the
MONOLOG_SLACK_TOKEN env var.`,
		Args: cobra.NoArgs,
		RunE: runSlackSync,
	}
	return cmd
}

// runSlackSync executes one poll-and-ingest cycle. Split out so tests can call
// it directly without cobra plumbing.
func runSlackSync(cmd *cobra.Command, args []string) error {
	s, _, err := openStore()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	token, _, err := config.SlackToken()
	if err != nil {
		return fmt.Errorf("read slack token: %w", err)
	}
	if token == "" {
		return fmt.Errorf("Slack not configured. Run monolog slack-login.")
	}

	slackCfg := config.Slack()
	// Presence of a token (env var or logged-in file) is sufficient to
	// proceed. The slack.enabled flag is only a toggle for the file-based
	// token maintained by slack-login / slack-logout; an env-var user who
	// never ran slack-login should still be able to sync. Note that
	// slack-logout clears the file token but does NOT unset env vars — a
	// user who wants a complete disconnect must unset MONOLOG_SLACK_TOKEN
	// in their shell as well.
	client := newSlackClientFn(token, slackCfg.Workspace)

	// Build the dedup cache by scanning on-disk tasks. Same pattern as
	// slack-status: walk every task, key by SourceID for anything with
	// Source=="slack". Ingest mutates the map in place after a successful
	// commit; we don't use that state post-run here (process exits) but
	// passing the seeded map is what makes dedup work.
	synced, err := buildSlackSyncedMap(s)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), slackSyncTimeout)
	defer cancel()

	items, err := client.ListSaved(ctx)
	if err != nil {
		return fmt.Errorf("slack list saved: %w", err)
	}

	opts := slack.Options{
		ChannelAsTag: slackCfg.ChannelAsTag,
		DateFormat:   config.DateFormat(),
		Stderr:       cmd.ErrOrStderr(),
	}
	newCount, err := slack.Ingest(s, items, synced, opts)
	if err != nil {
		return fmt.Errorf("slack ingest: %w", err)
	}

	if newCount == 0 {
		fmt.Fprintln(out, "No new items.")
		return nil
	}
	fmt.Fprintf(out, "Ingested %d new task(s).\n", newCount)
	return nil
}

// buildSlackSyncedMap scans all on-disk tasks and returns a map keyed by
// SourceID for every task with Source=="slack" and a non-empty SourceID.
// Same pattern as slack-status' ingested-count loop; shared here so a fresh
// map is produced per CLI invocation (the process doesn't persist state).
func buildSlackSyncedMap(s *store.Store) (map[string]bool, error) {
	tasks, err := s.List(store.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	synced := map[string]bool{}
	for _, t := range tasks {
		if t.Source == "slack" && t.SourceID != "" {
			synced[t.SourceID] = true
		}
	}
	return synced, nil
}
