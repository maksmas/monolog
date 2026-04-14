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

func TestCollectTags(t *testing.T) {
	tests := []struct {
		name  string
		tasks []Task
		want  []string
	}{
		{"empty slice", nil, nil},
		{"tasks with no tags", []Task{{Title: "a"}, {Title: "b"}}, nil},
		{"single task single tag", []Task{{Tags: []string{"work"}}}, []string{"work"}},
		{"sorted output", []Task{{Tags: []string{"zebra"}}, {Tags: []string{"alpha"}}}, []string{"alpha", "zebra"}},
		{"deduplicates across tasks", []Task{
			{Tags: []string{"work", "urgent"}},
			{Tags: []string{"work", "personal"}},
		}, []string{"personal", "urgent", "work"}},
		{"excludes tasks with empty tags", []Task{
			{Tags: nil},
			{Tags: []string{}},
			{Tags: []string{"solo"}},
		}, []string{"solo"}},
		{"multiple tags per task", []Task{
			{Tags: []string{"a", "c", "b"}},
		}, []string{"a", "b", "c"}},
		{"excludes active tag", []Task{
			{Tags: []string{"work", ActiveTag, "personal"}},
		}, []string{"personal", "work"}},
		{"only active tag yields nil", []Task{
			{Tags: []string{ActiveTag}},
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CollectTags(tc.tasks)
			if len(got) != len(tc.want) {
				t.Fatalf("CollectTags() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("CollectTags()[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseTitleTag(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		knownTags []string
		want      string
	}{
		// basic matching
		{"matching known tag", "jean: create integration", []string{"jean", "work"}, "jean"},
		{"unknown tag returns empty", "jean: create integration", []string{"work", "personal"}, ""},

		// no colon in title
		{"no colon in title", "create integration", []string{"jean"}, ""},

		// edge cases
		{"empty title", "", []string{"jean"}, ""},
		{"colon at start", ": some text", []string{""}, ""},
		{"no space after colon", "jean:nospace", []string{"jean"}, ""},
		{"tag with spaces not matched", "hello world: text", []string{"hello world"}, ""},
		{"tag containing colon not matched", "a:b: text", []string{"a:b"}, ""},

		// edge case: ActiveTag passed directly (CollectTags normally excludes it)
		{"active tag matches if in knownTags", "active: do something", []string{"active", "work"}, "active"},

		// additional edge cases
		{"empty known tags", "jean: do thing", nil, ""},
		{"empty known tags slice", "jean: do thing", []string{}, ""},
		{"multiple spaces after colon", "jean:  extra spaces", []string{"jean"}, "jean"},
		{"tab after colon", "jean:\tdo thing", []string{"jean"}, "jean"},
		{"only colon and space", ": ", []string{""}, ""},
		{"candidate with trailing space", "jean : do thing", []string{"jean"}, ""},
		{"case sensitive match", "Jean: do thing", []string{"jean"}, ""},
		{"case sensitive match exact", "Jean: do thing", []string{"Jean"}, "Jean"},
		{"first colon used", "a: b: c", []string{"a"}, "a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseTitleTag(tc.title, tc.knownTags)
			if got != tc.want {
				t.Errorf("ParseTitleTag(%q, %v) = %q, want %q", tc.title, tc.knownTags, got, tc.want)
			}
		})
	}
}

func TestAutoTag(t *testing.T) {
	tests := []struct {
		name         string
		title        string
		knownTags    []string
		existingTags []string
		want         []string
	}{
		{"adds matching tag", "jean: do thing", []string{"jean"}, nil, []string{"jean"}},
		{"no match returns existing", "unknown: do thing", []string{"jean"}, []string{"work"}, []string{"work"}},
		{"no duplicate when already present", "jean: do thing", []string{"jean"}, []string{"jean"}, []string{"jean"}},
		{"appends to existing tags", "jean: do thing", []string{"jean"}, []string{"work"}, []string{"work", "jean"}},
		{"nil existing tags", "jean: do thing", []string{"jean"}, nil, []string{"jean"}},
		{"empty title", "", []string{"jean"}, []string{"work"}, []string{"work"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AutoTag(tc.title, tc.knownTags, tc.existingTags)
			if len(got) != len(tc.want) {
				t.Fatalf("AutoTag(%q, %v, %v) = %v, want %v", tc.title, tc.knownTags, tc.existingTags, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("AutoTag()[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSanitizeTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty string", "", nil},
		{"whitespace only", "   ", nil},
		{"single tag", "one", []string{"one"}},
		{"comma separated", "one, two", []string{"one", "two"}},
		{"extra whitespace and empty parts", " a , , b ", []string{"a", "b"}},
		{"strips reserved active tag", "active, work", []string{"work"}},
		{"only active tag", "active", nil},
		{"active tag among many", "foo, active, bar", []string{"foo", "bar"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeTags(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("SanitizeTags(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("SanitizeTags(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}
