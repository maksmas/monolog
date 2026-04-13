package cmd

import (
	"github.com/spf13/cobra"

	"github.com/mmaksmas/monolog/internal/tui"
)

// Version is set at build time or defaults to "dev".
var Version = "dev"

// runTUI is the hook invoked when `monolog` is called with no subcommand. It
// opens the store and launches the interactive TUI. Exposed as a var so tests
// can stub it and avoid spinning up a terminal.
var runTUI = func() error {
	s, repoPath, err := openStore()
	if err != nil {
		return err
	}
	return tui.Run(s, repoPath)
}

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "monolog",
		Short:   "A CLI personal backlog tool",
		Long:    "Monolog is a CLI tool that provides a unified personal backlog.\nTasks are stored as individual JSON files in a git repo for conflict-free cross-device sync.\nRun with no arguments to open the interactive TUI.",
		Version: Version,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI()
		},
	}

	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newAddCmd())
	rootCmd.AddCommand(newLsCmd())
	rootCmd.AddCommand(newDoneCmd())
	rootCmd.AddCommand(newRmCmd())
	rootCmd.AddCommand(newEditCmd())
	rootCmd.AddCommand(newMvCmd())
	rootCmd.AddCommand(newBumpCmd())
	rootCmd.AddCommand(newLogCmd())
	rootCmd.AddCommand(newSyncCmd())

	return rootCmd
}

func Execute() error {
	return NewRootCmd().Execute()
}
