package cmd

import (
	"errors"
	"fmt"

	"github.com/maksmas/monolog/internal/git"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync local changes with the remote repository",
		Long:  "Stages all changes, commits, pulls with rebase (auto-resolving conflicts by picking the task version with the later UpdatedAt), and pushes. If no remote is configured, commits locally and warns.",
		Args:  cobra.NoArgs,
		// A sync that fails did so because of the repo or the network, never
		// because of how the command was typed (it takes no arguments and no
		// flags). Cobra's default is to print the full usage block under the
		// error, which buries git's diagnosis in help text.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := monologDir()
			res, err := git.Sync(repoPath)
			if err != nil {
				// Collapsed to one line for the same reason: git's "hint:"
				// block advises running by hand what sync just tried.
				return errors.New(git.ShortError(err))
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
