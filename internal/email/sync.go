package email

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/maksmas/monolog/internal/git"
	"github.com/maksmas/monolog/internal/store"
)

// SyncOptions controls the behavior of one Sync run. Callers populate it from
// configuration values they have already read; the email package never imports
// internal/config.
type SyncOptions struct {
	// Label is the Gmail label to query. Empty label is rejected.
	Label string
	// MaxPerSync is a soft cap on how many new messages this run will
	// import. Remaining new messages are picked up on subsequent runs with
	// no error and no warning. Zero or negative disables the cap (all new
	// messages processed).
	MaxPerSync int
	// Now is the wall-clock anchor used for Schedule="today" and the
	// CreatedAt/UpdatedAt timestamps. Tests inject a fixed value here.
	Now time.Time
	// Writer receives non-fatal warnings (e.g. partial Store.Create
	// failures). Sync still commits successfully written tasks even when
	// some writes failed.
	Writer io.Writer
}

// SyncResult summarizes what one Sync run did. Created counts only tasks that
// made it to disk; Err is non-nil for fatal failures (list error, commit
// error). Per-task warnings are written to SyncOptions.Writer instead.
type SyncResult struct {
	Created int
	Err     error
}

// Sync imports new gmail-labeled messages as monolog tasks and commits them in
// a single batch.
//
// Algorithm:
//  1. ListLabeled to get all message IDs (newest-first per Gmail API order).
//  2. Build a dedup set from store.List (no Status filter → all open + done)
//     of SourceIDs where Source == "gmail".
//  3. Filter to new IDs, preserving Gmail's newest-first order.
//  4. Take first MaxPerSync (remaining new IDs are silently skipped — soft
//     cap; they'll be picked up on the next sync).
//  5. For each new id: Get → ToTask → Store.Create. Per-task failures warn
//     to Writer and continue; the run still commits whatever succeeded.
//  6. Single git.AutoCommit at the end if any tasks were created.
//
// A fatal ListLabeled failure aborts before any Store mutation. No commit is
// emitted when zero tasks are created.
func Sync(ctx context.Context, gmail Gmail, s *store.Store, repoPath string, opts SyncOptions) SyncResult {
	if gmail == nil {
		return SyncResult{Err: fmt.Errorf("email: nil gmail client")}
	}
	if s == nil {
		return SyncResult{Err: fmt.Errorf("email: nil store")}
	}
	if opts.Label == "" {
		return SyncResult{Err: fmt.Errorf("email: empty label")}
	}

	ids, err := gmail.ListLabeled(ctx, opts.Label)
	if err != nil {
		return SyncResult{Err: fmt.Errorf("list labeled: %w", err)}
	}

	// Build dedup set: SourceIDs of every gmail-sourced task, open or done.
	// store.List with a zero ListOptions returns all tasks regardless of
	// status, which is exactly what we want — completed-and-archived emails
	// must not re-import.
	existing, err := s.List(store.ListOptions{})
	if err != nil {
		return SyncResult{Err: fmt.Errorf("list existing tasks: %w", err)}
	}
	seen := make(map[string]struct{}, len(existing))
	for _, t := range existing {
		if t.Source == "gmail" && t.SourceID != "" {
			seen[t.SourceID] = struct{}{}
		}
	}

	// Filter to new IDs, preserving the API order.
	var fresh []string
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		fresh = append(fresh, id)
	}

	// Apply the soft cap. Anything past MaxPerSync is silently deferred to
	// the next run.
	if opts.MaxPerSync > 0 && len(fresh) > opts.MaxPerSync {
		fresh = fresh[:opts.MaxPerSync]
	}

	var files []string
	for _, id := range fresh {
		msg, err := gmail.Get(ctx, id)
		if err != nil {
			warn(opts.Writer, "email: get %s: %v", id, err)
			continue
		}
		task, err := ToTask(msg, opts.Now)
		if err != nil {
			warn(opts.Writer, "email: convert %s: %v", id, err)
			continue
		}
		if err := s.Create(task); err != nil {
			warn(opts.Writer, "email: create task for %s: %v", id, err)
			continue
		}
		files = append(files, filepath.Join(".monolog", "tasks", task.ID+".json"))
	}

	if len(files) == 0 {
		return SyncResult{Created: 0}
	}

	msg := fmt.Sprintf("email: imported %d task(s) (label=%s)", len(files), opts.Label)
	if err := git.AutoCommit(repoPath, msg, files...); err != nil {
		// The tasks are already on disk; surface the commit failure but
		// also report Created so the caller can flash an accurate count.
		return SyncResult{Created: len(files), Err: fmt.Errorf("auto-commit: %w", err)}
	}
	return SyncResult{Created: len(files)}
}

// warn writes a single line to w when w is non-nil. The trailing newline is
// added automatically; format strings should not include it.
func warn(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format+"\n", args...)
}
