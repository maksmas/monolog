package recurrence

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/store"
)

func TestTagsWithoutActive(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil", in: nil, want: nil},
		{name: "empty", in: []string{}, want: nil},
		{name: "only_active", in: []string{model.ActiveTag}, want: nil},
		{name: "active_and_other", in: []string{model.ActiveTag, "work"}, want: []string{"work"}},
		{name: "other_and_active", in: []string{"work", model.ActiveTag}, want: []string{"work"}},
		{name: "all_non_active", in: []string{"work", "home"}, want: []string{"work", "home"}},
		{name: "multiple_active_somehow", in: []string{model.ActiveTag, "x", model.ActiveTag, "y"}, want: []string{"x", "y"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tagsWithoutActive(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("tagsWithoutActive(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestCompleteAndSpawn_UsesConfiguredDateFormat verifies that the
// dateFormat parameter drives every user-facing date rendering on the
// spawn path: the commit message's "next" date, the forward-reference
// note on the old task, and the back-reference note separator in the new
// task's body. Store-level ISO Schedule is unaffected.
func TestCompleteAndSpawn_UsesConfiguredDateFormat(t *testing.T) {
	cases := []struct {
		name       string
		dateFormat string
		// wantCommitDateFn renders the expected formatted date given the
		// spawn's ISO schedule so the assertion stays driven by the
		// computed next-occurrence rather than hardcoding wall-clock
		// behavior.
		wantCommitDateFn func(iso string) string
		// wantSepRegex is a substring search fragment to look for in the
		// old task's body after completion, proving the note separator
		// was written using the supplied layout.
		wantSepRegexDD bool // true = expect DD-MM-YYYY separator
		wantSepRegexUS bool // true = expect MM/DD/YYYY separator
	}{
		{
			name:       "DD-MM-YYYY (default)",
			dateFormat: "02-01-2006",
			wantCommitDateFn: func(iso string) string {
				tt, _ := time.Parse("2006-01-02", iso)
				return tt.Format("02-01-2006")
			},
			wantSepRegexDD: true,
		},
		{
			name:       "MM/DD/YYYY (alternative)",
			dateFormat: "01/02/2006",
			wantCommitDateFn: func(iso string) string {
				tt, _ := time.Parse("2006-01-02", iso)
				return tt.Format("01/02/2006")
			},
			wantSepRegexUS: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tasksDir := filepath.Join(dir, "tasks")
			s, err := store.New(tasksDir)
			if err != nil {
				t.Fatalf("store.New: %v", err)
			}

			id, err := model.NewID()
			if err != nil {
				t.Fatalf("model.NewID: %v", err)
			}
			task := model.Task{
				ID:         id,
				Title:      "Chore",
				Status:     "open",
				Schedule:   "2026-04-18",
				Recurrence: "days:3",
				Position:   1000,
				CreatedAt:  "2026-04-18T00:00:00Z",
				UpdatedAt:  "2026-04-18T00:00:00Z",
			}
			if err := s.Create(task); err != nil {
				t.Fatalf("store.Create: %v", err)
			}

			now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
			var warn bytes.Buffer
			commitMsg, commitFiles, err := CompleteAndSpawn(s, &task, now, &warn, tc.dateFormat)
			if err != nil {
				t.Fatalf("CompleteAndSpawn: %v (stderr: %s)", err, warn.String())
			}
			if len(commitFiles) != 2 {
				t.Fatalf("expected 2 commit files (old + spawn), got %d", len(commitFiles))
			}

			// Find the spawn task on disk (it's the non-old file in commitFiles).
			var spawnID string
			oldFile := filepath.Join(".monolog", "tasks", task.ID+".json")
			for _, f := range commitFiles {
				if f == oldFile {
					continue
				}
				spawnID = strings.TrimSuffix(filepath.Base(f), ".json")
			}
			if spawnID == "" {
				t.Fatalf("spawn file not found in commitFiles: %v", commitFiles)
			}
			spawn, err := s.Get(spawnID)
			if err != nil {
				t.Fatalf("store.Get spawn: %v", err)
			}

			// The stored Schedule on the spawn MUST be ISO regardless of
			// the display format.
			if _, err := time.Parse("2006-01-02", spawn.Schedule); err != nil {
				t.Errorf("spawn.Schedule %q must parse as ISO, got err: %v", spawn.Schedule, err)
			}

			wantDate := tc.wantCommitDateFn(spawn.Schedule)
			wantCommit := "done: Chore (recurring, next " + wantDate + ")"
			if commitMsg != wantCommit {
				t.Errorf("commitMsg:\n got  %q\n want %q", commitMsg, wantCommit)
			}

			// Forward-reference note on the old task uses display format.
			reloadedOld, err := s.Get(task.ID)
			if err != nil {
				t.Fatalf("store.Get old: %v", err)
			}
			wantForward := "Spawned follow-up: " + spawnID + " (scheduled " + wantDate + ")"
			if !strings.Contains(reloadedOld.Body, wantForward) {
				t.Errorf("old body missing %q:\n%s", wantForward, reloadedOld.Body)
			}

			// The note separator on the old body should start with
			// "--- " and the date rendered in the configured layout.
			if tc.wantSepRegexDD && !strings.Contains(reloadedOld.Body, "--- 18-04-2026 ") {
				t.Errorf("old body missing DD-MM-YYYY separator, got:\n%s", reloadedOld.Body)
			}
			if tc.wantSepRegexUS && !strings.Contains(reloadedOld.Body, "--- 04/18/2026 ") {
				t.Errorf("old body missing MM/DD/YYYY separator, got:\n%s", reloadedOld.Body)
			}

			// The spawn body's "Spawned from" note separator should also
			// use the configured layout.
			if tc.wantSepRegexDD && !strings.Contains(spawn.Body, "--- 18-04-2026 ") {
				t.Errorf("spawn body missing DD-MM-YYYY separator, got:\n%s", spawn.Body)
			}
			if tc.wantSepRegexUS && !strings.Contains(spawn.Body, "--- 04/18/2026 ") {
				t.Errorf("spawn body missing MM/DD/YYYY separator, got:\n%s", spawn.Body)
			}
		})
	}
}

// TestTagsWithoutActive_ReturnsFreshSlice verifies that mutating the output
// does not affect the input. The spawn flow relies on this so that
// subsequent SetActive/reslice work on the spawn doesn't corrupt the old
// task's Tags.
func TestTagsWithoutActive_ReturnsFreshSlice(t *testing.T) {
	in := []string{"work", "home"}
	got := tagsWithoutActive(in)
	if len(got) == 0 {
		t.Fatalf("unexpected empty result for %v", in)
	}
	got[0] = "MUTATED"
	if in[0] == "MUTATED" {
		t.Errorf("tagsWithoutActive should return a fresh slice; input was mutated: %v", in)
	}
}
