package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var remote string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new monolog repository",
		Long:  "Creates a new monolog repository with git, task storage directory, and config file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := monologDir()

			if err := git.Init(repoPath, remote); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Initialized monolog repo at %s\n", repoPath)
			if remote != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Remote origin set to %s\n", remote)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&remote, "remote", "", "Git remote URL to add as origin")

	return cmd
}

// monologDir returns the path to the monolog data directory.
// It uses MONOLOG_DIR env var if set, otherwise defaults to ~/.monolog/.
func monologDir() string {
	if dir := os.Getenv("MONOLOG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".monolog")
	}
	return filepath.Join(home, ".monolog")
}
