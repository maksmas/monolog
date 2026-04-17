package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
)

// seedRecurringTask seeds a task with the given recurrence on disk and
// returns its ID. The task is created via the add command (so it lives in
// git), then patched with the Recurrence field and (optionally) extra tags.
func seedRecurringTask(t *testing.T, dir, title, recurrence string, extraTags []string) string {
	t.Helper()
	id := addTestTask(t, dir, title)
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatalf("seed task %s not found", id)
	}
	task.Recurrence = recurrence
	if len(extraTags) > 0 {
		task.Tags = append(task.Tags, extraTags...)
	}
	path := filepath.Join(dir, ".monolog", "tasks", id+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal patched seed: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write patched seed: %v", err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", filepath.Join(".monolog", "tasks", id+".json")).CombinedOutput(); err != nil {
		t.Fatalf("git add patched seed: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "seed recurrence: "+title).CombinedOutput(); err != nil {
		t.Fatalf("git commit patched seed: %v\n%s", err, out)
	}
	return id
}

func TestDone_Recurring_SpawnsNewTask(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := seedRecurringTask(t, dir, "Pay rent", "monthly:1", nil)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"done", id[:8]})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command error = %v\noutput: %s", err, buf.String())
	}

	// Old task: done, still has Recurrence.
	old, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("old task not found after done")
	}
	if old.Status != "done" {
		t.Errorf("old Status: got %q, want done", old.Status)
	}
	if old.Recurrence != "monthly:1" {
		t.Errorf("old Recurrence: got %q, want %q", old.Recurrence, "monthly:1")
	}

	// There should be exactly one other open task with same title — the spawn.
	tasks := readTasks(t, dir)
	var spawn model.Task
	spawnCount := 0
	for _, task := range tasks {
		if task.ID == id {
			continue
		}
		if task.Title == "Pay rent" {
			spawn = task
			spawnCount++
		}
	}
	if spawnCount != 1 {
		t.Fatalf("expected 1 spawned task, got %d", spawnCount)
	}

	if spawn.ID == id {
		t.Errorf("spawn should have a fresh ID, got %q == old %q", spawn.ID, id)
	}
	if spawn.Status != "open" {
		t.Errorf("spawn Status: got %q, want open", spawn.Status)
	}
	if spawn.Recurrence != "monthly:1" {
		t.Errorf("spawn Recurrence: got %q, want %q", spawn.Recurrence, "monthly:1")
	}
	// Schedule should parse as a valid ISO date.
	if _, err := time.Parse("2006-01-02", spawn.Schedule); err != nil {
		t.Errorf("spawn Schedule %q is not an ISO date: %v", spawn.Schedule, err)
	}
	// Body should contain the spawned-from note.
	if !strings.Contains(spawn.Body, "Spawned from "+id) {
		t.Errorf("spawn Body missing 'Spawned from %s' note:\n%s", id, spawn.Body)
	}
	// Tags should not include active.
	for _, tag := range spawn.Tags {
		if tag == model.ActiveTag {
			t.Errorf("spawn tags should not include active, got %v", spawn.Tags)
		}
	}
}

func TestDone_NonRecurring_NoSpawn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := addTestTask(t, dir, "One-shot task")

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v", err)
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d (non-recurring should not spawn)", len(tasks))
	}
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task missing")
	}
	if task.Status != "done" {
		t.Errorf("Status: got %q, want done", task.Status)
	}
}

func TestDone_InvalidRecurrence_SkipsSpawnWithWarning(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := seedRecurringTask(t, dir, "Broken recur", "bogus", nil)

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done command should succeed even with invalid recurrence, got err=%v\nstderr: %s", err, errBuf.String())
	}

	// Stderr should carry a warning.
	stderr := errBuf.String()
	if !strings.Contains(stderr, "recurrence") || !strings.Contains(stderr, "invalid") {
		t.Errorf("expected warning about invalid recurrence on stderr, got: %q", stderr)
	}

	// Old task still marked done.
	task, ok := getTaskByID(t, dir, id)
	if !ok {
		t.Fatal("task missing")
	}
	if task.Status != "done" {
		t.Errorf("Status: got %q, want done", task.Status)
	}

	// No spawn — only the original task exists.
	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Errorf("expected 1 task (no spawn on invalid recurrence), got %d", len(tasks))
	}
}

func TestDone_Recurring_BidirectionalNotes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := seedRecurringTask(t, dir, "Note cross-ref", "days:3", nil)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v", err)
	}

	// Find spawn.
	tasks := readTasks(t, dir)
	var spawn model.Task
	for _, task := range tasks {
		if task.ID != id && task.Title == "Note cross-ref" {
			spawn = task
			break
		}
	}
	if spawn.ID == "" {
		t.Fatal("spawn not found")
	}

	// Reload old task.
	old, _ := getTaskByID(t, dir, id)

	// Old task body should end with the spawned-follow-up note.
	wantForward := "Spawned follow-up: " + spawn.ID + " (scheduled " + spawn.Schedule + ")"
	if !strings.Contains(old.Body, wantForward) {
		t.Errorf("old Body missing forward note %q:\n%s", wantForward, old.Body)
	}

	// New task body should contain the spawned-from note.
	wantBack := "Spawned from " + id
	if !strings.Contains(spawn.Body, wantBack) {
		t.Errorf("spawn Body missing back note %q:\n%s", wantBack, spawn.Body)
	}
}

func TestDone_Recurring_StripsActiveOnSpawn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Seed task with extra tags including active.
	id := seedRecurringTask(t, dir, "Active recur", "weekly:mon", []string{"work", model.ActiveTag})

	// Sanity check: the seed has the active tag.
	seeded, _ := getTaskByID(t, dir, id)
	if !seeded.IsActive() {
		t.Fatal("precondition: seeded task should be active")
	}

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v", err)
	}

	// Find spawn.
	tasks := readTasks(t, dir)
	var spawn model.Task
	for _, task := range tasks {
		if task.ID != id && task.Title == "Active recur" {
			spawn = task
			break
		}
	}
	if spawn.ID == "" {
		t.Fatal("spawn not found")
	}
	if spawn.IsActive() {
		t.Errorf("spawn should not be active, got tags %v", spawn.Tags)
	}
	// Non-active tag should be preserved.
	hasWork := false
	for _, tag := range spawn.Tags {
		if tag == "work" {
			hasWork = true
		}
	}
	if !hasWork {
		t.Errorf("spawn should preserve non-active tags, got %v", spawn.Tags)
	}

	// The old task should also have active cleared (existing behavior).
	old, _ := getTaskByID(t, dir, id)
	if old.IsActive() {
		t.Errorf("old task should not be active after done, got tags %v", old.Tags)
	}
}

func TestDone_Recurring_SingleCommitIncludesBothFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := seedRecurringTask(t, dir, "One commit", "workdays", nil)

	// Count commits before.
	commitsBefore := countCommits(t, dir)

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v", err)
	}

	// Exactly one new commit.
	commitsAfter := countCommits(t, dir)
	if commitsAfter != commitsBefore+1 {
		t.Errorf("expected exactly one new commit, got before=%d after=%d", commitsBefore, commitsAfter)
	}

	// Find spawn.
	tasks := readTasks(t, dir)
	var spawnID string
	for _, task := range tasks {
		if task.ID != id && task.Title == "One commit" {
			spawnID = task.ID
			break
		}
	}
	if spawnID == "" {
		t.Fatal("spawn not found")
	}

	// The latest commit should touch both task files.
	gitCmd := exec.Command("git", "-C", dir, "show", "--name-only", "--pretty=format:", "HEAD")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git show failed: %v", err)
	}
	files := string(out)
	oldFile := filepath.Join(".monolog", "tasks", id+".json")
	newFile := filepath.Join(".monolog", "tasks", spawnID+".json")
	if !strings.Contains(files, oldFile) {
		t.Errorf("latest commit should include %s, got:\n%s", oldFile, files)
	}
	if !strings.Contains(files, newFile) {
		t.Errorf("latest commit should include %s, got:\n%s", newFile, files)
	}

	// Commit message should note the recurring spawn.
	msgCmd := exec.Command("git", "-C", dir, "log", "-1", "--pretty=%s")
	msgOut, err := msgCmd.Output()
	if err != nil {
		t.Fatalf("git log --pretty failed: %v", err)
	}
	msg := strings.TrimSpace(string(msgOut))
	if !strings.HasPrefix(msg, "done: One commit") || !strings.Contains(msg, "(recurring, next ") {
		t.Errorf("commit message missing recurring marker: %q", msg)
	}
}

func TestDone_Recurring_NoteCountRecalculated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	id := seedRecurringTask(t, dir, "Note count test", "days:1", nil)

	// Baseline: no notes yet.
	seeded, _ := getTaskByID(t, dir, id)
	if seeded.NoteCount != 0 {
		t.Fatalf("seed NoteCount: got %d, want 0", seeded.NoteCount)
	}

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"done", id[:8]})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("done error = %v", err)
	}

	// Old task: back-reference note added → NoteCount == 1.
	old, _ := getTaskByID(t, dir, id)
	if old.NoteCount != 1 {
		t.Errorf("old NoteCount: got %d, want 1 (one spawned-follow-up note)", old.NoteCount)
	}

	// Spawn: spawned-from note added → NoteCount == 1.
	tasks := readTasks(t, dir)
	var spawn model.Task
	for _, task := range tasks {
		if task.ID != id && task.Title == "Note count test" {
			spawn = task
			break
		}
	}
	if spawn.ID == "" {
		t.Fatal("spawn not found")
	}
	if spawn.NoteCount != 1 {
		t.Errorf("spawn NoteCount: got %d, want 1 (one spawned-from note)", spawn.NoteCount)
	}
}

// countCommits counts the number of commits on HEAD in the given repo.
func countCommits(t *testing.T, dir string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-list failed: %v", err)
	}
	s := strings.TrimSpace(string(out))
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("parse commit count %q: %v", s, err)
	}
	return n
}
