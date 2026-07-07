package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/display"
	"github.com/maksmas/monolog/internal/email"
	"github.com/maksmas/monolog/internal/git"
	"github.com/maksmas/monolog/internal/recurrence"
	"github.com/spf13/cobra"
)

// archiveFn is a swappable seam invoked after a successful `done` on a
// gmail-sourced task to remove the INBOX label in Gmail. Tests replace this
// with a recording fake; production wiring uses realArchive.
//
// The function takes the Gmail message ID (Task.SourceID) and the email
// configuration so the caller can build the Gmail client. Returning nil means
// the archive succeeded; any error is treated as NON-FATAL by the caller (the
// task stays done, the error is logged to stderr).
var archiveFn = realArchive

// realArchive constructs an authenticated Gmail client from the persisted
// OAuth token and removes the INBOX label from the given message. The
// email.ArchiveTimeout context caps how long this can hang on flaky network.
func realArchive(sourceID string, ec config.EmailConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), email.ArchiveTimeout)
	defer cancel()

	tokenPath := email.TokenPathFor(ec.ClientSecretsPath)
	httpClient, err := email.HTTPClient(ctx, ec.ClientSecretsPath, tokenPath)
	if err != nil {
		return err
	}
	g, err := email.NewClient(ctx, httpClient)
	if err != nil {
		return err
	}
	return g.ArchiveLabel(ctx, sourceID)
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

			// Archive in Gmail when the completed task came from a gmail
			// import and email integration is enabled. Failures are
			// non-fatal — we already exited 0 from the user's perspective.
			ec := config.Email()
			if ec.Enabled && task.Source == "gmail" && task.SourceID != "" {
				if err := archiveFn(task.SourceID, ec); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "archive failed: %v\n", err)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "email archived")
				}
			}
			return nil
		},
	}

	return cmd
}
