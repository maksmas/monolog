package cmd

// End-to-end acceptance tests for the recurring-tasks feature. These
// automate the smoke-test scenarios from Task 8 of the plan
// docs/plans/20260417-recurring-tasks.md so the acceptance checks run
// as part of `go test ./...` rather than as manual terminal sessions.

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/schedule"
)

// TestAcceptance_AddRecurDoneSpawnsAndSingleCommit is the automated version
// of smoke test #1:
//
//	monolog add --recur monthly:1 "pay rent"
//	monolog done <id>
//	→ new task created with next-month's first as schedule,
//	→ body has "Spawned from",
//	→ old task's body has "Spawned follow-up",
//	→ git log -1 --name-only shows both files in one commit.
func TestAcceptance_AddRecurDoneSpawnsAndSingleCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// monolog add --recur monthly:1 "pay rent"
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"add", "pay rent", "--recur", "monthly:1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add --recur error = %v", err)
	}

	tasks := readTasks(t, dir)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task after add, got %d", len(tasks))
	}
	oldID := tasks[0].ID
	if tasks[0].Recurrence != "monthly:1" {
		t.Errorf("Recurrence: got %q, want %q", tasks[0].Recurrence, "monthly:1")
	}

	commitsBefore := countCommits(t, dir)

	// monolog done <id>
	doneCmd := NewRootCmd()
	doneBuf := new(bytes.Buffer)
	doneCmd.SetOut(doneBuf)
	doneCmd.SetErr(doneBuf)
	doneCmd.SetArgs([]string{"done", oldID[:8]})
	if err := doneCmd.Execute(); err != nil {
		t.Fatalf("done error = %v\noutput: %s", err, doneBuf.String())
	}

	// Exactly one new commit for the spawn.
	commitsAfter := countCommits(t, dir)
	if commitsAfter != commitsBefore+1 {
		t.Errorf("expected exactly 1 new commit after done, before=%d after=%d",
			commitsBefore, commitsAfter)
	}

	// Find spawned task.
	all := readTasks(t, dir)
	if len(all) != 2 {
		t.Fatalf("expected 2 tasks after done, got %d", len(all))
	}
	var old, spawn model.Task
	for _, task := range all {
		if task.ID == oldID {
			old = task
		} else {
			spawn = task
		}
	}
	if spawn.ID == "" {
		t.Fatal("spawned task not found")
	}

	// Spawn: scheduled for the 1st of some month, "Spawned from <old>" note.
	if !strings.HasSuffix(spawn.Schedule, "-01") {
		t.Errorf("spawn Schedule %q should end with -01 (first of month)", spawn.Schedule)
	}
	if !strings.Contains(spawn.Body, "Spawned from "+oldID) {
		t.Errorf("spawn Body missing 'Spawned from %s':\n%s", oldID, spawn.Body)
	}

	// Old task: "Spawned follow-up: <new> (scheduled <date>)" note.
	wantForward := "Spawned follow-up: " + spawn.ID + " (scheduled " + spawn.Schedule + ")"
	if !strings.Contains(old.Body, wantForward) {
		t.Errorf("old Body missing %q:\n%s", wantForward, old.Body)
	}

	// git log -1 --name-only must show both files in the same commit.
	out, err := exec.Command("git", "-C", dir, "show", "--name-only", "--pretty=format:", "HEAD").Output()
	if err != nil {
		t.Fatalf("git show failed: %v", err)
	}
	files := string(out)
	oldPath := filepath.Join(".monolog", "tasks", oldID+".json")
	newPath := filepath.Join(".monolog", "tasks", spawn.ID+".json")
	if !strings.Contains(files, oldPath) {
		t.Errorf("latest commit missing %s, got:\n%s", oldPath, files)
	}
	if !strings.Contains(files, newPath) {
		t.Errorf("latest commit missing %s, got:\n%s", newPath, files)
	}
}

// TestAcceptance_EditClearRecurPreventsSpawn is the automated version of
// smoke test #2: edit to clear (`monolog edit <id> --recur ""`), mark done,
// confirm no new spawn.
func TestAcceptance_EditClearRecurPreventsSpawn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Create a recurring task.
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"add", "chore", "--recur", "days:3"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add error = %v", err)
	}
	tasks := readTasks(t, dir)
	id := tasks[0].ID

	// Clear the recurrence via edit --recur "".
	editCmd := NewRootCmd()
	editCmd.SetOut(new(bytes.Buffer))
	editCmd.SetErr(new(bytes.Buffer))
	editCmd.SetArgs([]string{"edit", id[:8], "--recur", ""})
	if err := editCmd.Execute(); err != nil {
		t.Fatalf("edit --recur '' error = %v", err)
	}
	cleared, _ := getTaskByID(t, dir, id)
	if cleared.Recurrence != "" {
		t.Fatalf("Recurrence after clear: got %q, want empty", cleared.Recurrence)
	}

	// Mark done — no spawn should occur.
	doneCmd := NewRootCmd()
	doneCmd.SetOut(new(bytes.Buffer))
	doneCmd.SetErr(new(bytes.Buffer))
	doneCmd.SetArgs([]string{"done", id[:8]})
	if err := doneCmd.Execute(); err != nil {
		t.Fatalf("done error = %v", err)
	}

	afterDone := readTasks(t, dir)
	if len(afterDone) != 1 {
		t.Errorf("expected 1 task (no spawn after cleared recurrence), got %d", len(afterDone))
	}

	// Commit message should be the non-recurring form.
	msgOut, err := exec.Command("git", "-C", dir, "log", "-1", "--pretty=%s").Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	msg := strings.TrimSpace(string(msgOut))
	if strings.Contains(msg, "recurring") {
		t.Errorf("commit message should not mention 'recurring', got: %q", msg)
	}
}

// TestAcceptance_WorkdaysSpawnsNextWeekday is the automated version of
// smoke test #3 (modulo the TUI keystrokes — TUI input plumbing is
// covered separately by tests in internal/tui/model_test.go). It verifies
// that a task with workdays recurrence spawns an occurrence on a weekday.
func TestAcceptance_WorkdaysSpawnsNextWeekday(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Add a task with workdays recurrence via the CLI — this path is the
	// programmatic equivalent of the TUI add-modal workflow: the TUI ends
	// up calling store.Create with the same Recurrence field. Separate
	// TUI tests in internal/tui/model_test.go cover the input plumbing
	// (focus cycling, input validation, text entry).
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"add", "standup", "--recur", "workdays"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("add --recur workdays error = %v", err)
	}

	tasks := readTasks(t, dir)
	id := tasks[0].ID
	if tasks[0].Recurrence != "workdays" {
		t.Fatalf("Recurrence: got %q, want %q", tasks[0].Recurrence, "workdays")
	}

	// Task appears in the ls output (smoke test: "confirm it appears in list").
	lsCmd := NewRootCmd()
	lsBuf := new(bytes.Buffer)
	lsCmd.SetOut(lsBuf)
	lsCmd.SetErr(new(bytes.Buffer))
	lsCmd.SetArgs([]string{"ls"})
	if err := lsCmd.Execute(); err != nil {
		t.Fatalf("ls error = %v", err)
	}
	if !strings.Contains(lsBuf.String(), "standup") {
		t.Errorf("ls output missing 'standup':\n%s", lsBuf.String())
	}

	// Complete the task.
	doneCmd := NewRootCmd()
	doneCmd.SetOut(new(bytes.Buffer))
	doneCmd.SetErr(new(bytes.Buffer))
	doneCmd.SetArgs([]string{"done", id[:8]})
	if err := doneCmd.Execute(); err != nil {
		t.Fatalf("done error = %v", err)
	}

	// Spawn must have a schedule that is Mon-Fri (weekday 1..5 in Go's Weekday).
	all := readTasks(t, dir)
	var spawn model.Task
	for _, task := range all {
		if task.ID != id {
			spawn = task
			break
		}
	}
	if spawn.ID == "" {
		t.Fatal("spawn not found after workdays done")
	}
	// Parse schedule and check day-of-week.
	if len(spawn.Schedule) == 0 {
		t.Fatalf("spawn has empty schedule")
	}
	ts, err := time.Parse(schedule.IsoLayout, spawn.Schedule)
	if err != nil {
		t.Fatalf("parse schedule %q: %v", spawn.Schedule, err)
	}
	wd := ts.Weekday()
	// time.Weekday: Sunday=0, Monday=1, ..., Saturday=6. Mon-Fri = 1..5.
	if wd < 1 || wd > 5 {
		t.Errorf("workdays spawn landed on weekend: schedule=%s weekday=%s",
			spawn.Schedule, wd)
	}
	if spawn.Recurrence != "workdays" {
		t.Errorf("spawn Recurrence: got %q, want 'workdays'", spawn.Recurrence)
	}
}

// TestAcceptance_WeeklyAliasesCanonicalize is the automated version of
// smoke test #4: `--recur weekly:Monday` and `--recur weekly:1` both
// succeed and store as `weekly:mon`. Verified via the show command.
func TestAcceptance_WeeklyAliasesCanonicalize(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{name: "full_name", input: "weekly:Monday"},
		{name: "numeric", input: "weekly:1"},
		{name: "upper_short", input: "weekly:MON"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "monolog")
			initTestRepo(t, dir)

			// monolog add --recur <alias> "..."
			addCmd := NewRootCmd()
			addCmd.SetOut(new(bytes.Buffer))
			addCmd.SetErr(new(bytes.Buffer))
			addCmd.SetArgs([]string{"add", "weekly task", "--recur", tc.input})
			if err := addCmd.Execute(); err != nil {
				t.Fatalf("add --recur %s error = %v", tc.input, err)
			}

			tasks := readTasks(t, dir)
			if len(tasks) != 1 {
				t.Fatalf("expected 1 task, got %d", len(tasks))
			}
			id := tasks[0].ID

			// monolog show <id> — must display canonical weekly:mon.
			showCmd := NewRootCmd()
			showBuf := new(bytes.Buffer)
			showCmd.SetOut(showBuf)
			showCmd.SetErr(new(bytes.Buffer))
			showCmd.SetArgs([]string{"show", id[:8]})
			if err := showCmd.Execute(); err != nil {
				t.Fatalf("show error = %v", err)
			}
			output := showBuf.String()
			if !strings.Contains(output, "Recurrence: weekly:mon") {
				t.Errorf("show output missing canonical 'Recurrence: weekly:mon' for input %q, got:\n%s",
					tc.input, output)
			}
		})
	}
}

