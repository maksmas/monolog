package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/spf13/cobra"
)

func newNoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note <identifier> <text>",
		Short: "Add a note to a task",
		Long:  "Resolves the task by ID prefix or title initials and appends a timestamped note.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			identifier := args[0]
			text := strings.TrimSpace(args[1])
			if text == "" {
				return fmt.Errorf("note text cannot be empty")
			}

			s, repoPath, err := openStore()
			if err != nil {
				return err
			}

			task, err := s.Resolve(identifier)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			now := time.Now()
			task.Body = model.AppendNote(task.Body, text, now)
			task.NoteCount++
			task.UpdatedAt = now.UTC().Format(time.RFC3339)

			if err := s.Update(task); err != nil {
				return fmt.Errorf("update task: %w", err)
			}

			taskFile := filepath.Join(".monolog", "tasks", task.ID+".json")
			if err := git.AutoCommit(repoPath, fmt.Sprintf("note: %s", task.Title), taskFile); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Note added: %s [%s]\n", task.Title, display.ShortID(task.ID))
			return nil
		},
	}

	return cmd
}
