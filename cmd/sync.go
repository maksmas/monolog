package cmd

import (
	"fmt"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync local changes with the remote repository",
		Long:  "Stages all changes, commits, pulls with rebase, and pushes to the remote. If no remote is configured, commits locally and warns.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := monologDir()

			// Step 1: Check for uncommitted changes and commit if needed
			hasChanges, err := git.HasChanges(repoPath)
			if err != nil {
				return fmt.Errorf("check changes: %w", err)
			}

			if hasChanges {
				if err := git.SyncCommit(repoPath); err != nil {
					return fmt.Errorf("commit: %w", err)
				}
			}

			// Step 2: Check for remote
			hasRemote, err := git.HasRemote(repoPath)
			if err != nil {
				return fmt.Errorf("check remote: %w", err)
			}

			if !hasRemote {
				fmt.Fprintln(cmd.OutOrStdout(), "no remote configured, skipping sync")
				return nil
			}

			// Step 3: Pull and push
			if err := git.PullRebase(repoPath); err != nil {
				return fmt.Errorf("pull: %w", err)
			}

			if err := git.Push(repoPath); err != nil {
				return fmt.Errorf("push: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Synced")
			return nil
		},
	}

	return cmd
}
