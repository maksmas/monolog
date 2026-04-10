package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

func newMvCmd() *cobra.Command {
	var (
		top    bool
		bottom bool
		before string
		after  string
	)

	cmd := &cobra.Command{
		Use:   "mv <id-prefix>",
		Short: "Move a task to a new position",
		Long:  "Reorders a task within its schedule group using --top, --bottom, --before, or --after.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate exactly one position flag
			flagCount := 0
			if top {
				flagCount++
			}
			if bottom {
				flagCount++
			}
			if before != "" {
				flagCount++
			}
			if after != "" {
				flagCount++
			}
			if flagCount == 0 {
				return fmt.Errorf("exactly one of --top, --bottom, --before, or --after is required")
			}
			if flagCount > 1 {
				return fmt.Errorf("exactly one of --top, --bottom, --before, or --after is required")
			}

			prefix := args[0]
			repoPath := monologDir()
			tasksDir := filepath.Join(repoPath, ".monolog", "tasks")

			s, err := store.New(tasksDir)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}

			task, err := s.GetByPrefix(prefix)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			// Get all open tasks in the same schedule group, sorted by position.
			// Exclude the task being moved.
			groupTasks, err := s.List(store.ListOptions{Schedule: task.Schedule, Status: "open"})
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}
			var others []model.Task
			for _, gt := range groupTasks {
				if gt.ID != task.ID {
					others = append(others, gt)
				}
			}
			// others is already sorted by position (store.List sorts)

			var newPos float64
			var posLabel string

			switch {
			case top:
				newPos = ordering.PositionTop(others)
				posLabel = "top"

			case bottom:
				newPos = ordering.NextPosition(others)
				posLabel = "bottom"

			case before != "":
				target, err := s.GetByPrefix(before)
				if err != nil {
					return fmt.Errorf("resolve target task: %w", err)
				}
				targetIdx := -1
				for i, o := range others {
					if o.ID == target.ID {
						targetIdx = i
						break
					}
				}
				if targetIdx == -1 {
					return fmt.Errorf("target task is not in the same schedule group")
				}
				if targetIdx == 0 {
					newPos = ordering.PositionTop(others)
				} else {
					newPos = ordering.PositionBetween(others[targetIdx-1].Position, others[targetIdx].Position)
				}
				posLabel = fmt.Sprintf("before %s", target.ID[:8])

			case after != "":
				target, err := s.GetByPrefix(after)
				if err != nil {
					return fmt.Errorf("resolve target task: %w", err)
				}
				targetIdx := -1
				for i, o := range others {
					if o.ID == target.ID {
						targetIdx = i
						break
					}
				}
				if targetIdx == -1 {
					return fmt.Errorf("target task is not in the same schedule group")
				}
				if targetIdx == len(others)-1 {
					newPos = ordering.NextPosition(others)
				} else {
					newPos = ordering.PositionBetween(others[targetIdx].Position, others[targetIdx+1].Position)
				}
				posLabel = fmt.Sprintf("after %s", target.ID[:8])
			}

			task.Position = newPos
			task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

			if err := s.Update(task); err != nil {
				return fmt.Errorf("update task: %w", err)
			}

			// Collect files to commit
			commitFiles := []string{filepath.Join(".monolog", "tasks", task.ID+".json")}

			// Check if rebalance is needed
			allGroup, err := s.List(store.ListOptions{Schedule: task.Schedule, Status: "open"})
			if err != nil {
				return fmt.Errorf("list tasks for rebalance check: %w", err)
			}
			if ordering.NeedsRebalance(allGroup) {
				rebalanced := ordering.Rebalance(allGroup)
				for _, rt := range rebalanced {
					rt.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
					if err := s.Update(rt); err != nil {
						return fmt.Errorf("rebalance update: %w", err)
					}
					commitFiles = append(commitFiles, filepath.Join(".monolog", "tasks", rt.ID+".json"))
				}
			}

			// Deduplicate commit files
			seen := make(map[string]bool)
			var uniqueFiles []string
			for _, f := range commitFiles {
				if !seen[f] {
					seen[f] = true
					uniqueFiles = append(uniqueFiles, f)
				}
			}

			if err := git.AutoCommit(repoPath, fmt.Sprintf("mv: %s to %s", task.Title, posLabel), uniqueFiles...); err != nil {
				return fmt.Errorf("auto-commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Moved: %s to %s [%s]\n", task.Title, posLabel, task.ID[:8])
			return nil
		},
	}

	cmd.Flags().BoolVar(&top, "top", false, "Move to top of schedule group")
	cmd.Flags().BoolVar(&bottom, "bottom", false, "Move to bottom of schedule group")
	cmd.Flags().StringVar(&before, "before", "", "Move before this task (id-prefix)")
	cmd.Flags().StringVar(&after, "after", "", "Move after this task (id-prefix)")

	return cmd
}
