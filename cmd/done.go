package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/recurrence"
	"github.com/mmaksmas/monolog/internal/slack"
	"github.com/spf13/cobra"
)

// doneSlackUnsaveTimeout bounds the CLI unsave call so the `done` command
// cannot hang on a wedged Slack API. Matches the TUI's 5s timeout.
const doneSlackUnsaveTimeout = 5 * time.Second

// newDoneSlackClientFn constructs a Slack client for the CLI unsave path.
// Tests substitute this to point at an httptest server. Mirrors the
// newSlackSyncClientFn pattern used by slack-sync.
var newDoneSlackClientFn = func(token, workspace string) *slack.Client {
	return &slack.Client{Token: token, Workspace: workspace}
}

func newDoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done <id-prefix>",
		Short: "Mark a task as done",
		Long:  "Resolves the task by ID prefix and sets its status to done.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := args[0]

			s, repoPath, err := openStore()
			if err != nil {
				return err
			}

			task, err := s.Resolve(prefix)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			if task.Status == "done" {
				fmt.Fprintf(cmd.OutOrStdout(), "Already done: %s [%s]\n", task.Title, display.ShortID(task.ID))
				return nil
			}

			commitMsg, commitFiles, err := recurrence.CompleteAndSpawn(s, &task, time.Now(), cmd.ErrOrStderr(), config.DateFormat())
			if err != nil {
				return err
			}

			if err := git.AutoCommit(repoPath, commitMsg, commitFiles...); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Done: %s [%s]\n", task.Title, display.ShortID(task.ID))

			// Slack unsave is best-effort: completion already succeeded and
			// was committed. Any failure here is surfaced as a stderr
			// warning but never fails the command, so cron/automation does
			// not false-positive on a transient Slack outage.
			maybeSlackUnsave(cmd, task.Source, task.SourceID)

			return nil
		},
	}

	return cmd
}

// maybeSlackUnsave fires stars.remove against Slack if the completed task
// originated from Slack and a token is configured. Swallows and warns on any
// error — the caller has already succeeded at the actual done transition.
func maybeSlackUnsave(cmd *cobra.Command, source, sourceID string) {
	if source != "slack" || sourceID == "" {
		return
	}
	token, _, err := config.SlackToken()
	if err != nil || token == "" {
		// Silent when no token: user may have intentionally logged out; a
		// noisy warning here would be confusing.
		return
	}
	idx := strings.Index(sourceID, "/")
	if idx <= 0 || idx >= len(sourceID)-1 {
		fmt.Fprintf(cmd.ErrOrStderr(), "monolog: slack unsave skipped: malformed SourceID %q\n", sourceID)
		return
	}
	channel := sourceID[:idx]
	ts := sourceID[idx+1:]

	slackCfg := config.Slack()
	client := newDoneSlackClientFn(token, slackCfg.Workspace)

	ctx, cancel := context.WithTimeout(context.Background(), doneSlackUnsaveTimeout)
	defer cancel()

	if err := client.Unsave(ctx, channel, ts); err != nil {
		if errors.Is(err, slack.ErrMissingScope) {
			fmt.Fprintln(cmd.ErrOrStderr(), "monolog: slack unsave needs stars:write — run monolog slack-login")
			return
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "monolog: slack unsave failed: %v\n", err)
	}
}
