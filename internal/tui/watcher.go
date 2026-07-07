package tui

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// taskWatcher observes the task-files directory for changes made outside the
// running TUI process (Raycast captures, a second terminal, an external
// `git pull`) and emits debounced signals on its channel so the TUI can
// reload. Construction errors are surfaced to the caller; runtime errors
// from the underlying fsnotify watcher are non-fatal — the goroutine logs
// them once to stderr and stops.
type taskWatcher struct {
	ch     chan struct{}
	stopFn func() error
	once   sync.Once
}

// newTaskWatcher starts a fsnotify watch on tasksDir and returns a
// taskWatcher whose channel receives one struct{} value per debounced burst
// of filesystem events. debounce is the coalescing window — events arriving
// within this duration of each other collapse to a single signal.
//
// Returns (nil, nil) when MONOLOG_NO_WATCH=1 is set so callers can opt out
// without a code change. Returns (nil, err) when fsnotify cannot start or
// cannot watch the directory; the TUI should keep running without
// auto-refresh on this error rather than refusing to launch.
func newTaskWatcher(tasksDir string, debounce time.Duration) (*taskWatcher, error) {
	if os.Getenv("MONOLOG_NO_WATCH") == "1" {
		return nil, nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(tasksDir); err != nil {
		_ = w.Close()
		return nil, err
	}
	tw := &taskWatcher{
		ch:     make(chan struct{}, 1),
		stopFn: w.Close,
	}
	go tw.loop(w, debounce)
	return tw, nil
}

// loop reads events off the fsnotify watcher and forwards a coalesced
// signal once per debounce window. The send is non-blocking — if the
// consumer hasn't drained the previous signal yet, the new burst is
// effectively merged into the next read.
func (w *taskWatcher) loop(fw *fsnotify.Watcher, debounce time.Duration) {
	defer close(w.ch)
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case ev, ok := <-fw.Events:
			if !ok {
				return
			}
			// Chmod-only events are noise (e.g. tools touching attributes
			// without changing content). Filter to write/create/remove/rename.
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(debounce)
				timerC = timer.C
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounce)
			}
		case <-timerC:
			timer = nil
			timerC = nil
			select {
			case w.ch <- struct{}{}:
			default:
			}
		case err, ok := <-fw.Errors:
			if !ok {
				return
			}
			if err != nil && !errors.Is(err, fsnotify.ErrEventOverflow) {
				// One-time stderr note then keep going — the watcher is
				// best-effort.
				logWatcherError(err)
			}
		}
	}
}

// Stop terminates the watcher and the goroutine. Safe to call more than
// once; subsequent calls are no-ops.
func (w *taskWatcher) Stop() error {
	if w == nil {
		return nil
	}
	var err error
	w.once.Do(func() {
		if w.stopFn != nil {
			err = w.stopFn()
		}
	})
	return err
}

// logWatcherError is a seam tests can swap to capture stderr output.
var logWatcherError = func(err error) {
	// Stderr is the same surface used by theme bootstrap warnings —
	// keeps the TUI quiet on stdout while still surfacing the issue.
	_, _ = os.Stderr.WriteString("monolog: watcher: " + err.Error() + "\n")
}
