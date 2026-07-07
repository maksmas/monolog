package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testDebounce = 50 * time.Millisecond

// drainWithin waits up to d for one signal on ch and returns true if it
// arrived. Returns false on timeout.
func drainWithin(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

func TestWatcher_DetectsExternalCreate(t *testing.T) {
	dir := t.TempDir()
	w, err := newTaskWatcher(dir, testDebounce)
	if err != nil {
		t.Fatalf("newTaskWatcher: %v", err)
	}
	if w == nil {
		t.Fatal("newTaskWatcher returned nil watcher without error")
	}
	t.Cleanup(func() { _ = w.Stop() })

	// Give fsnotify a beat to register before writing.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !drainWithin(w.ch, 500*time.Millisecond) {
		t.Fatal("expected a signal after creating a file")
	}
}

func TestWatcher_DebouncesBurst(t *testing.T) {
	dir := t.TempDir()
	w, err := newTaskWatcher(dir, testDebounce)
	if err != nil {
		t.Fatalf("newTaskWatcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop() })
	time.Sleep(10 * time.Millisecond)

	// Fire three writes within the debounce window.
	for i := 0; i < 3; i++ {
		name := filepath.Join(dir, []string{"a.json", "b.json", "c.json"}[i])
		if err := os.WriteFile(name, []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !drainWithin(w.ch, 500*time.Millisecond) {
		t.Fatal("expected one signal after burst")
	}
	// No second signal should arrive within the next debounce window — the
	// burst is fully coalesced.
	if drainWithin(w.ch, 3*testDebounce) {
		t.Fatal("did not expect a second signal — burst should coalesce to one")
	}
}

func TestWatcher_StopReturnsCleanly(t *testing.T) {
	dir := t.TempDir()
	w, err := newTaskWatcher(dir, testDebounce)
	if err != nil {
		t.Fatalf("newTaskWatcher: %v", err)
	}
	if err := w.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Channel should close so a read returns the zero value with !ok.
	select {
	case _, ok := <-w.ch:
		if ok {
			t.Fatal("expected channel to be closed after Stop")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("channel did not close after Stop")
	}
	// Second Stop is a no-op.
	if err := w.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestWatcher_NoWatchEnv(t *testing.T) {
	t.Setenv("MONOLOG_NO_WATCH", "1")
	dir := t.TempDir()
	w, err := newTaskWatcher(dir, testDebounce)
	if err != nil {
		t.Fatalf("newTaskWatcher: %v", err)
	}
	if w != nil {
		_ = w.Stop()
		t.Fatal("expected nil watcher when MONOLOG_NO_WATCH=1")
	}
}

func TestWatcher_BadDirReturnsError(t *testing.T) {
	w, err := newTaskWatcher(filepath.Join(t.TempDir(), "does-not-exist"), testDebounce)
	if err == nil {
		_ = w.Stop()
		t.Fatal("expected error for missing dir")
	}
	if w != nil {
		_ = w.Stop()
		t.Fatal("expected nil watcher on error")
	}
}
