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
		Long:  "Stages all changes, commits, pulls with rebase (auto-resolving conflicts by picking the task version with the later UpdatedAt), and pushes. If no remote is configured, commits locally and warns.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := monologDir()
			res, err := git.Sync(repoPath)
			if err != nil {
				return err
			}
			if !res.HasRemote {
				fmt.Fprintln(cmd.OutOrStdout(), "no remote configured, skipping sync")
				return nil
			}
			if res.Resolved > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Synced (auto-resolved %d conflicts)\n", res.Resolved)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Synced")
			}
			return nil
		},
	}
	return cmd
}
