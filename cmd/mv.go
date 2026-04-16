package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmaksmas/monolog/internal/display"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
	"github.com/spf13/cobra"
)

// findTargetIndex looks up the target task in the others slice and returns its index.
// Returns an error if the target is not in the same schedule bucket (distinguishing
// done targets from different-bucket targets).
func findTargetIndex(s *store.Store, prefix string, others []model.Task, taskBucket string) (model.Task, int, error) {
	target, err := s.Resolve(prefix)
	if err != nil {
		return model.Task{}, -1, fmt.Errorf("resolve target task: %w", err)
	}
	for i, o := range others {
		if o.ID == target.ID {
			return target, i, nil
		}
	}
	if target.Status == "done" {
		return model.Task{}, -1, fmt.Errorf("target task is done, not in an open schedule group")
	}
	targetBucket := schedule.Bucket(target.Schedule, time.Now())
	return model.Task{}, -1, fmt.Errorf("target task is in schedule bucket %q, not %q", targetBucket, taskBucket)
}

// calcPosition computes the new position and label for a move operation.
func calcPosition(s *store.Store, others []model.Task, taskBucket string, top, bottom bool, before, after string) (float64, string, error) {
	switch {
	case top:
		return ordering.PositionTop(others), "top", nil

	case bottom:
		return ordering.NextPosition(others), "bottom", nil

	case before != "":
		target, idx, err := findTargetIndex(s, before, others, taskBucket)
		if err != nil {
			return 0, "", err
		}
		var pos float64
		if idx == 0 {
			pos = ordering.PositionTop(others)
		} else {
			pos = ordering.PositionBetween(others[idx-1].Position, others[idx].Position)
		}
		return pos, fmt.Sprintf("before %s", display.ShortID(target.ID)), nil

	default: // after != ""
		target, idx, err := findTargetIndex(s, after, others, taskBucket)
		if err != nil {
			return 0, "", err
		}
		var pos float64
		if idx == len(others)-1 {
			pos = ordering.NextPosition(others)
		} else {
			pos = ordering.PositionBetween(others[idx].Position, others[idx+1].Position)
		}
		return pos, fmt.Sprintf("after %s", display.ShortID(target.ID)), nil
	}
}

// bucketGroup returns all open tasks in the same virtual bucket as task.
func bucketGroup(s *store.Store, task model.Task, now time.Time) ([]model.Task, error) {
	all, err := s.List(store.ListOptions{Status: "open"})
	if err != nil {
		return nil, err
	}
	taskBucket := schedule.Bucket(task.Schedule, now)
	out := all[:0]
	for _, t := range all {
		if schedule.Bucket(t.Schedule, now) == taskBucket {
			out = append(out, t)
		}
	}
	return out, nil
}

// rebalanceAndCommit checks if rebalancing is needed, performs it, and commits all changes.
func rebalanceAndCommit(s *store.Store, repoPath string, task model.Task, posLabel string) error {
	commitFiles := []string{filepath.Join(".monolog", "tasks", task.ID+".json")}

	allGroup, err := bucketGroup(s, task, time.Now())
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

	return git.AutoCommit(repoPath, fmt.Sprintf("mv: %s to %s", task.Title, posLabel), uniqueFiles...)
}

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

			s, repoPath, err := openStore()
			if err != nil {
				return err
			}

			task, err := s.Resolve(prefix)
			if err != nil {
				return fmt.Errorf("resolve task: %w", err)
			}

			now := time.Now()
			// Lazy-migrate any legacy bucket string to ISO before computing
			// the bucket so the on-disk file is normalized after this write.
			task.Schedule = schedule.Normalize(task.Schedule, now)
			taskBucket := schedule.Bucket(task.Schedule, now)

			// Get all open tasks in the same schedule bucket, sorted by position.
			// Exclude the task being moved.
			groupTasks, err := bucketGroup(s, task, now)
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}
			var others []model.Task
			for _, gt := range groupTasks {
				if gt.ID != task.ID {
					others = append(others, gt)
				}
			}

			newPos, posLabel, err := calcPosition(s, others, taskBucket, top, bottom, before, after)
			if err != nil {
				return err
			}

			if newPos == task.Position {
				fmt.Fprintf(cmd.OutOrStdout(), "Already at %s: %s [%s]\n", posLabel, task.Title, display.ShortID(task.ID))
				return nil
			}

			task.Position = newPos
			task.UpdatedAt = now.UTC().Format(time.RFC3339)

			if err := s.Update(task); err != nil {
				return fmt.Errorf("update task: %w", err)
			}

			if err := rebalanceAndCommit(s, repoPath, task, posLabel); err != nil {
				return fmt.Errorf("rebalance/commit: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Moved: %s to %s [%s]\n", task.Title, posLabel, display.ShortID(task.ID))
			return nil
		},
	}

	cmd.Flags().BoolVar(&top, "top", false, "Move to top of schedule group")
	cmd.Flags().BoolVar(&bottom, "bottom", false, "Move to bottom of schedule group")
	cmd.Flags().StringVar(&before, "before", "", "Move before this task (id-prefix)")
	cmd.Flags().StringVar(&after, "after", "", "Move after this task (id-prefix)")

	return cmd
}
