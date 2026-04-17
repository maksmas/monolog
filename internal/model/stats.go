package model

import (
	"fmt"
	"time"
)

// Stats holds aggregated metrics computed from a slice of tasks.
type Stats struct {
	Total             int
	Open              int
	Done              int
	AvgDaysOpen       float64 // mean age in days of open tasks; 0 when no open tasks
	AvgDaysToComplete float64 // mean days from creation to completion for done tasks
	// with CompletedAt set; 0 when no such tasks exist
}

// ComputeStats derives Stats from a slice of tasks relative to now.
// Tasks with an unparseable CreatedAt are skipped for avg calculations.
func ComputeStats(tasks []Task, now time.Time) Stats {
	var s Stats
	var sumDaysOpen float64
	var countOpen int
	var sumDaysDone float64
	var countDone int

	for _, t := range tasks {
		s.Total++
		if t.Status == "done" {
			s.Done++
			if t.CompletedAt != "" && t.CreatedAt != "" {
				created, err1 := time.Parse(time.RFC3339, t.CreatedAt)
				completed, err2 := time.Parse(time.RFC3339, t.CompletedAt)
				if err1 == nil && err2 == nil && !completed.Before(created) {
					sumDaysDone += completed.Sub(created).Hours() / 24
					countDone++
				}
			}
		} else {
			s.Open++
			if t.CreatedAt != "" {
				created, err := time.Parse(time.RFC3339, t.CreatedAt)
				if err == nil {
					age := now.Sub(created).Hours() / 24
					if age < 0 {
						age = 0
					}
					sumDaysOpen += age
					countOpen++
				}
			}
		}
	}

	if countOpen > 0 {
		s.AvgDaysOpen = sumDaysOpen / float64(countOpen)
	}
	if countDone > 0 {
		s.AvgDaysToComplete = sumDaysDone / float64(countDone)
	}
	return s
}

// FormatDuration formats a duration in days as a terse human-readable string.
// Values less than 1 day are expressed in hours; 14 days or more are expressed
// in weeks.
func FormatDuration(days float64) string {
	if days < 0 {
		days = 0
	}
	if days < 1 {
		hours := int(days * 24)
		if hours < 1 {
			hours = 1
		}
		return fmt.Sprintf("%dh", hours)
	}
	if days < 14 {
		return fmt.Sprintf("%dd", int(days))
	}
	weeks := int(days / 7)
	return fmt.Sprintf("%dw", weeks)
}
