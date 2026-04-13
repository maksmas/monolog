package ordering

import (
	"testing"

	"github.com/mmaksmas/monolog/internal/model"
)

func makeTasks(positions ...float64) []model.Task {
	tasks := make([]model.Task, len(positions))
	for i, p := range positions {
		tasks[i] = model.Task{
			ID:       "TASK" + string(rune('A'+i)),
			Position: p,
		}
	}
	return tasks
}

// --- NextPosition tests ---

func TestNextPosition_EmptyList(t *testing.T) {
	pos := NextPosition(nil)
	if pos != DefaultSpacing {
		t.Errorf("NextPosition(nil) = %v, want %v", pos, DefaultSpacing)
	}
}

func TestNextPosition_SingleItem(t *testing.T) {
	tasks := makeTasks(1000)
	pos := NextPosition(tasks)
	if pos != 2000 {
		t.Errorf("NextPosition([1000]) = %v, want 2000", pos)
	}
}

func TestNextPosition_MultipleItems(t *testing.T) {
	tasks := makeTasks(1000, 2000, 3000)
	pos := NextPosition(tasks)
	if pos != 4000 {
		t.Errorf("NextPosition([1000,2000,3000]) = %v, want 4000", pos)
	}
}

func TestNextPosition_UnsortedInput(t *testing.T) {
	tasks := makeTasks(3000, 1000, 5000, 2000)
	pos := NextPosition(tasks)
	if pos != 6000 {
		t.Errorf("NextPosition([3000,1000,5000,2000]) = %v, want 6000", pos)
	}
}

// --- PositionBetween tests ---

func TestPositionBetween_Normal(t *testing.T) {
	pos := PositionBetween(1000, 2000)
	if pos != 1500 {
		t.Errorf("PositionBetween(1000, 2000) = %v, want 1500", pos)
	}
}

func TestPositionBetween_TightGap(t *testing.T) {
	pos := PositionBetween(1000, 1002)
	if pos != 1001 {
		t.Errorf("PositionBetween(1000, 1002) = %v, want 1001", pos)
	}
}

func TestPositionBetween_LargeGap(t *testing.T) {
	pos := PositionBetween(0, 10000)
	if pos != 5000 {
		t.Errorf("PositionBetween(0, 10000) = %v, want 5000", pos)
	}
}

// --- PositionTop tests ---

func TestPositionTop_EmptyList(t *testing.T) {
	pos := PositionTop(nil)
	if pos != DefaultSpacing {
		t.Errorf("PositionTop(nil) = %v, want %v", pos, DefaultSpacing)
	}
}

func TestPositionTop_SingleItem(t *testing.T) {
	tasks := makeTasks(1000)
	pos := PositionTop(tasks)
	if pos >= 1000 {
		t.Errorf("PositionTop([1000]) = %v, want < 1000", pos)
	}
}

func TestPositionTop_MultipleItems(t *testing.T) {
	tasks := makeTasks(1000, 2000, 3000)
	pos := PositionTop(tasks)
	if pos >= 1000 {
		t.Errorf("PositionTop([1000,2000,3000]) = %v, want < 1000", pos)
	}
}

func TestPositionTop_UnsortedInput(t *testing.T) {
	tasks := makeTasks(3000, 1000, 5000, 2000)
	pos := PositionTop(tasks)
	if pos >= 1000 {
		t.Errorf("PositionTop([3000,1000,5000,2000]) = %v, want < 1000", pos)
	}
}

// --- NeedsRebalance tests ---

func TestNeedsRebalance_WellSpaced(t *testing.T) {
	tasks := makeTasks(1000, 2000, 3000)
	if NeedsRebalance(tasks) {
		t.Error("NeedsRebalance should be false for well-spaced tasks")
	}
}

func TestNeedsRebalance_TightGap(t *testing.T) {
	tasks := makeTasks(1000, 1000.5, 2000)
	if !NeedsRebalance(tasks) {
		t.Error("NeedsRebalance should be true when gap < 1.0")
	}
}

func TestNeedsRebalance_Empty(t *testing.T) {
	if NeedsRebalance(nil) {
		t.Error("NeedsRebalance should be false for empty list")
	}
}

func TestNeedsRebalance_SingleItem(t *testing.T) {
	tasks := makeTasks(1000)
	if NeedsRebalance(tasks) {
		t.Error("NeedsRebalance should be false for single item")
	}
}

// --- Rebalance tests ---

func TestRebalance_TightGaps(t *testing.T) {
	tasks := makeTasks(1000, 1000.3, 1000.6)
	rebalanced := Rebalance(tasks)
	if len(rebalanced) != 3 {
		t.Fatalf("Rebalance returned %d tasks, want 3", len(rebalanced))
	}
	expected := []float64{1000, 2000, 3000}
	for i, task := range rebalanced {
		if task.Position != expected[i] {
			t.Errorf("rebalanced[%d].Position = %v, want %v", i, task.Position, expected[i])
		}
	}
}

func TestRebalance_AlreadyBalanced(t *testing.T) {
	tasks := makeTasks(1000, 2000, 3000)
	rebalanced := Rebalance(tasks)
	expected := []float64{1000, 2000, 3000}
	for i, task := range rebalanced {
		if task.Position != expected[i] {
			t.Errorf("rebalanced[%d].Position = %v, want %v", i, task.Position, expected[i])
		}
	}
}

func TestRebalance_UnsortedInput(t *testing.T) {
	tasks := makeTasks(5000, 1000, 3000)
	rebalanced := Rebalance(tasks)
	// Should sort by position first, then rebalance
	if len(rebalanced) != 3 {
		t.Fatalf("Rebalance returned %d tasks, want 3", len(rebalanced))
	}
	// After sorting: 1000, 3000, 5000 → rebalanced to 1000, 2000, 3000
	expected := []float64{1000, 2000, 3000}
	for i, task := range rebalanced {
		if task.Position != expected[i] {
			t.Errorf("rebalanced[%d].Position = %v, want %v", i, task.Position, expected[i])
		}
	}
}

func TestRebalance_PreservesTaskData(t *testing.T) {
	tasks := []model.Task{
		{ID: "A", Title: "First", Position: 100},
		{ID: "B", Title: "Second", Position: 100.5},
	}
	rebalanced := Rebalance(tasks)
	if rebalanced[0].ID != "A" || rebalanced[0].Title != "First" {
		t.Error("Rebalance should preserve task data for first task")
	}
	if rebalanced[1].ID != "B" || rebalanced[1].Title != "Second" {
		t.Error("Rebalance should preserve task data for second task")
	}
}

func TestRebalance_Empty(t *testing.T) {
	rebalanced := Rebalance(nil)
	if len(rebalanced) != 0 {
		t.Errorf("Rebalance(nil) returned %d tasks, want 0", len(rebalanced))
	}
}

func TestRebalance_SingleItem(t *testing.T) {
	tasks := makeTasks(42)
	rebalanced := Rebalance(tasks)
	if len(rebalanced) != 1 {
		t.Fatalf("Rebalance returned %d tasks, want 1", len(rebalanced))
	}
	if rebalanced[0].Position != DefaultSpacing {
		t.Errorf("Rebalance single item position = %v, want %v", rebalanced[0].Position, DefaultSpacing)
	}
}
