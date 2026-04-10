package cmd

import (
	"github.com/spf13/cobra"
)

// Version is set at build time or defaults to "dev".
var Version = "dev"

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "monolog",
		Short:   "A CLI personal backlog tool",
		Long:    "Monolog is a CLI tool that provides a unified personal backlog.\nTasks are stored as individual JSON files in a git repo for conflict-free cross-device sync.",
		Version: Version,
	}

	rootCmd.AddCommand(newInitCmd())

	return rootCmd
}

func Execute() error {
	return NewRootCmd().Execute()
}
