package model

import (
	"testing"
	"time"
)

func TestComputeStats_Empty(t *testing.T) {
	s := ComputeStats(nil, time.Now())
	if s.Total != 0 || s.Open != 0 || s.Done != 0 {
		t.Errorf("empty slice: got %+v, want zero", s)
	}
	if s.AvgDaysOpen != 0 || s.AvgDaysToComplete != 0 {
		t.Errorf("empty slice: avg fields should be 0, got %+v", s)
	}
}

func TestComputeStats_OnlyOpen(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	tasks := []Task{
		{Status: "open", CreatedAt: "2026-04-11T12:00:00Z"}, // 4 days ago
		{Status: "open", CreatedAt: "2026-04-13T12:00:00Z"}, // 2 days ago
	}
	s := ComputeStats(tasks, now)
	if s.Total != 2 {
		t.Errorf("Total = %d, want 2", s.Total)
	}
	if s.Open != 2 {
		t.Errorf("Open = %d, want 2", s.Open)
	}
	if s.Done != 0 {
		t.Errorf("Done = %d, want 0", s.Done)
	}
	// avg = (4+2)/2 = 3 days
	if s.AvgDaysOpen != 3.0 {
		t.Errorf("AvgDaysOpen = %v, want 3.0", s.AvgDaysOpen)
	}
	if s.AvgDaysToComplete != 0 {
		t.Errorf("AvgDaysToComplete = %v, want 0", s.AvgDaysToComplete)
	}
}

func TestComputeStats_OnlyDone(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	tasks := []Task{
		{
			Status:      "done",
			CreatedAt:   "2026-04-01T00:00:00Z",
			CompletedAt: "2026-04-11T00:00:00Z", // 10 days to complete
		},
		{
			Status:      "done",
			CreatedAt:   "2026-04-05T00:00:00Z",
			CompletedAt: "2026-04-07T00:00:00Z", // 2 days to complete
		},
	}
	s := ComputeStats(tasks, now)
	if s.Total != 2 {
		t.Errorf("Total = %d, want 2", s.Total)
	}
	if s.Open != 0 {
		t.Errorf("Open = %d, want 0", s.Open)
	}
	if s.Done != 2 {
		t.Errorf("Done = %d, want 2", s.Done)
	}
	if s.AvgDaysOpen != 0 {
		t.Errorf("AvgDaysOpen = %v, want 0", s.AvgDaysOpen)
	}
	// avg = (10+2)/2 = 6
	if s.AvgDaysToComplete != 6.0 {
		t.Errorf("AvgDaysToComplete = %v, want 6.0", s.AvgDaysToComplete)
	}
}

func TestComputeStats_Mixed(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	tasks := []Task{
		{Status: "open", CreatedAt: "2026-04-10T12:00:00Z"},  // 5 days open
		{Status: "open", CreatedAt: "2026-04-13T12:00:00Z"},  // 2 days open
		{Status: "done", CreatedAt: "2026-04-01T00:00:00Z", CompletedAt: "2026-04-05T00:00:00Z"}, // 4 days
		{Status: "done", CreatedAt: "2026-04-01T00:00:00Z"},  // done but no CompletedAt — excluded from avg
	}
	s := ComputeStats(tasks, now)
	if s.Total != 4 {
		t.Errorf("Total = %d, want 4", s.Total)
	}
	if s.Open != 2 {
		t.Errorf("Open = %d, want 2", s.Open)
	}
	if s.Done != 2 {
		t.Errorf("Done = %d, want 2", s.Done)
	}
	// avg open = (5+2)/2 = 3.5
	if s.AvgDaysOpen != 3.5 {
		t.Errorf("AvgDaysOpen = %v, want 3.5", s.AvgDaysOpen)
	}
	// only one done task has CompletedAt
	if s.AvgDaysToComplete != 4.0 {
		t.Errorf("AvgDaysToComplete = %v, want 4.0", s.AvgDaysToComplete)
	}
}

func TestComputeStats_DoneWithoutCompletedAt(t *testing.T) {
	now := time.Now()
	tasks := []Task{
		{Status: "done", CreatedAt: "2026-04-01T00:00:00Z"}, // no CompletedAt
	}
	s := ComputeStats(tasks, now)
	if s.AvgDaysToComplete != 0 {
		t.Errorf("AvgDaysToComplete should be 0 when no CompletedAt set, got %v", s.AvgDaysToComplete)
	}
}

func TestComputeStats_OpenWithBadCreatedAt(t *testing.T) {
	now := time.Now()
	tasks := []Task{
		{Status: "open", CreatedAt: "not-a-date"},
		{Status: "open", CreatedAt: ""},
	}
	s := ComputeStats(tasks, now)
	if s.Open != 2 {
		t.Errorf("Open = %d, want 2", s.Open)
	}
	// skipped from avg due to bad parse
	if s.AvgDaysOpen != 0 {
		t.Errorf("AvgDaysOpen = %v, want 0 (bad CreatedAt skipped)", s.AvgDaysOpen)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		days float64
		want string
	}{
		{0, "1h"},
		{0.04, "1h"},  // ~1h
		{0.25, "6h"},
		{0.5, "12h"},
		{0.99, "23h"},
		{1.0, "1d"},
		{1.5, "1d"},
		{13.0, "13d"},
		{13.9, "13d"},
		{14.0, "2w"},
		{21.0, "3w"},
		{100.0, "14w"},
	}
	for _, tt := range tests {
		got := FormatDuration(tt.days)
		if got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.days, got, tt.want)
		}
	}
}

func TestFormatDuration_Negative(t *testing.T) {
	// Negative values treated as 0.
	got := FormatDuration(-5)
	if got != "1h" {
		t.Errorf("FormatDuration(-5) = %q, want %q", got, "1h")
	}
}
