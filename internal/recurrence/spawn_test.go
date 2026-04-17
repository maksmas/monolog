package recurrence

import (
	"reflect"
	"testing"

	"github.com/mmaksmas/monolog/internal/model"
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
