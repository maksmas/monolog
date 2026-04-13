package model

import (
	"testing"
)

func TestTask_IsActive(t *testing.T) {
	tests := []struct {
		name   string
		tags   []string
		expect bool
	}{
		{"nil tags", nil, false},
		{"empty tags", []string{}, false},
		{"active tag present", []string{ActiveTag}, true},
		{"active among others", []string{"work", ActiveTag, "urgent"}, true},
		{"active absent among others", []string{"work", "urgent"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := Task{Tags: tt.tags}
			if got := task.IsActive(); got != tt.expect {
				t.Errorf("IsActive() = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestTask_SetActive(t *testing.T) {
	t.Run("add when absent", func(t *testing.T) {
		task := Task{Tags: []string{"work"}}
		task.SetActive(true)
		if !task.IsActive() {
			t.Fatal("expected task to be active after SetActive(true)")
		}
		// other tags preserved
		if len(task.Tags) != 2 {
			t.Fatalf("expected 2 tags, got %d: %v", len(task.Tags), task.Tags)
		}
		if task.Tags[0] != "work" {
			t.Errorf("expected first tag to remain 'work', got %q", task.Tags[0])
		}
	})

	t.Run("add when already present (idempotent)", func(t *testing.T) {
		task := Task{Tags: []string{"work", ActiveTag}}
		task.SetActive(true)
		if !task.IsActive() {
			t.Fatal("expected task to be active")
		}
		// should not duplicate
		count := 0
		for _, tag := range task.Tags {
			if tag == ActiveTag {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected exactly 1 active tag, found %d in %v", count, task.Tags)
		}
	})

	t.Run("remove when present", func(t *testing.T) {
		task := Task{Tags: []string{"work", ActiveTag, "urgent"}}
		task.SetActive(false)
		if task.IsActive() {
			t.Fatal("expected task to not be active after SetActive(false)")
		}
		// other tags preserved in order
		if len(task.Tags) != 2 {
			t.Fatalf("expected 2 tags, got %d: %v", len(task.Tags), task.Tags)
		}
		if task.Tags[0] != "work" || task.Tags[1] != "urgent" {
			t.Errorf("expected [work urgent], got %v", task.Tags)
		}
	})

	t.Run("remove when absent (no-op)", func(t *testing.T) {
		task := Task{Tags: []string{"work", "urgent"}}
		task.SetActive(false)
		if task.IsActive() {
			t.Fatal("expected task to not be active")
		}
		if len(task.Tags) != 2 {
			t.Fatalf("expected 2 tags, got %d: %v", len(task.Tags), task.Tags)
		}
	})

	t.Run("add to nil tags", func(t *testing.T) {
		task := Task{}
		task.SetActive(true)
		if !task.IsActive() {
			t.Fatal("expected task to be active")
		}
		if len(task.Tags) != 1 || task.Tags[0] != ActiveTag {
			t.Errorf("expected [active], got %v", task.Tags)
		}
	})

	t.Run("order preservation of other tags", func(t *testing.T) {
		task := Task{Tags: []string{"alpha", "beta", "gamma"}}
		task.SetActive(true)
		// active is appended
		if task.Tags[0] != "alpha" || task.Tags[1] != "beta" || task.Tags[2] != "gamma" {
			t.Errorf("order not preserved: %v", task.Tags)
		}
		task.SetActive(false)
		if task.Tags[0] != "alpha" || task.Tags[1] != "beta" || task.Tags[2] != "gamma" {
			t.Errorf("order not preserved after removal: %v", task.Tags)
		}
	})
}
