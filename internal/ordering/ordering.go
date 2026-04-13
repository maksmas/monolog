package ordering

import (
	"sort"

	"github.com/mmaksmas/monolog/internal/model"
)

// DefaultSpacing is the gap between positions when creating or rebalancing tasks.
const DefaultSpacing = 1000.0

// RebalanceThreshold is the minimum gap between adjacent positions before
// a rebalance is triggered.
const RebalanceThreshold = 1.0

// NextPosition returns the position for a new task appended after all existing
// tasks. If the list is empty, it returns DefaultSpacing.
func NextPosition(tasks []model.Task) float64 {
	if len(tasks) == 0 {
		return DefaultSpacing
	}
	max := tasks[0].Position
	for _, t := range tasks[1:] {
		if t.Position > max {
			max = t.Position
		}
	}
	return max + DefaultSpacing
}

// PositionBetween returns the midpoint between positions a and b.
// Caller must ensure a < b.
func PositionBetween(a, b float64) float64 {
	return (a + b) / 2.0
}

// PositionTop returns a position before the lowest-positioned task in the list.
// If the list is empty, it returns DefaultSpacing.
func PositionTop(tasks []model.Task) float64 {
	if len(tasks) == 0 {
		return DefaultSpacing
	}
	min := tasks[0].Position
	for _, t := range tasks[1:] {
		if t.Position < min {
			min = t.Position
		}
	}
	return min / 2.0
}

// NeedsRebalance reports whether any adjacent pair of tasks (sorted by position)
// has a gap smaller than RebalanceThreshold.
func NeedsRebalance(tasks []model.Task) bool {
	if len(tasks) < 2 {
		return false
	}
	sorted := make([]model.Task, len(tasks))
	copy(sorted, tasks)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Position < sorted[j].Position
	})
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Position-sorted[i-1].Position < RebalanceThreshold {
			return true
		}
	}
	return false
}

// Rebalance sorts tasks by position and redistributes them with even spacing
// (DefaultSpacing apart), starting at DefaultSpacing. It returns a new slice
// with updated positions; the original slice is not modified.
func Rebalance(tasks []model.Task) []model.Task {
	if len(tasks) == 0 {
		return nil
	}
	result := make([]model.Task, len(tasks))
	copy(result, tasks)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Position < result[j].Position
	})
	for i := range result {
		result[i].Position = DefaultSpacing * float64(i+1)
	}
	return result
}
