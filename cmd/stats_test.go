package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maksmas/monolog/internal/model"
)

// runStats runs `monolog stats` through runCLI and fails the test on error.
func runStats(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := runCLI(t, append([]string{"stats"}, args...)...)
	if err != nil {
		t.Fatalf("stats error = %v\nstderr: %s", err, errOut)
	}
	return out
}

func TestStatsCommand_Counts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Open today")
	addTask(t, "Open someday", "-s", "someday")
	addTask(t, "Finish me")
	for _, task := range readTasks(t, dir) {
		if task.Title == "Finish me" {
			if _, _, err := runCLI(t, "done", task.ID); err != nil {
				t.Fatalf("done failed: %v", err)
			}
		}
	}

	out := runStats(t)
	for _, want := range []string{
		"Total:                   3\n",
		"Open:                    2\n",
		"Done:                    1\n",
		"Avg time to complete:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output missing %q, got:\n%s", want, out)
		}
	}
	// Fresh tasks are under an hour old, which FormatDuration rounds up to 1h.
	if !strings.Contains(out, "Avg time open:           1h") {
		t.Errorf("expected avg open of 1h, got:\n%s", out)
	}
}

func TestStatsCommand_TagFilter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	addTask(t, "Work one", "-t", "work")
	addTask(t, "Work two", "-t", "work,urgent")
	addTask(t, "Home", "-t", "home")

	out := runStats(t, "--tag", "work")
	if !strings.Contains(out, "Total:                   2\n") {
		t.Errorf("--tag work should count 2 tasks, got:\n%s", out)
	}
	if !strings.Contains(out, "Done:                    0\n") {
		t.Errorf("--tag work should have 0 done, got:\n%s", out)
	}
}

func TestStatsCommand_EmptyBacklog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	out := runStats(t)
	if !strings.Contains(out, "Total:                   0\n") {
		t.Errorf("empty backlog should report 0 total, got:\n%s", out)
	}
	if !strings.Contains(out, "Avg time open:           -\n") || !strings.Contains(out, "Avg time to complete:    -\n") {
		t.Errorf("empty backlog should show '-' for averages, got:\n%s", out)
	}
}

func TestStatsCommand_RejectsArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	if _, _, err := runCLI(t, "stats", "extra"); err == nil {
		t.Fatal("stats with a positional argument should fail")
	}
}

func TestWriteStats_FormatsAverages(t *testing.T) {
	buf := new(bytes.Buffer)
	writeStats(buf, model.Stats{Total: 5, Open: 3, Done: 2, AvgDaysOpen: 3.5, AvgDaysToComplete: 21})
	out := buf.String()
	if !strings.Contains(out, "Avg time open:           3d\n") {
		t.Errorf("expected avg open 3d, got:\n%s", out)
	}
	if !strings.Contains(out, "Avg time to complete:    3w\n") {
		t.Errorf("expected avg complete 3w, got:\n%s", out)
	}
}
