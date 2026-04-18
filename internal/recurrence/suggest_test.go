package recurrence

import (
	"strings"
	"testing"
)

func TestGrammarHint_Content(t *testing.T) {
	// Guard against accidental edits that diverge from the grammar
	// surfaced by Parse.
	want := "monthly:N | weekly:<day> | workdays | days:N"
	if GrammarHint != want {
		t.Fatalf("GrammarHint = %q, want %q", GrammarHint, want)
	}
}

func TestSuggest(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "empty returns nil",
			in:   "",
			want: nil,
		},
		{
			name: "m matches monthly template",
			in:   "m",
			want: []string{"monthly:N"},
		},
		{
			name: "w matches weekly and workdays in order",
			in:   "w",
			want: []string{"weekly:", "workdays"},
		},
		{
			name: "week narrows to weekly only",
			in:   "week",
			want: []string{"weekly:"},
		},
		{
			name: "weekly: returns all seven weekdays but capped at 5",
			in:   "weekly:",
			want: []string{"weekly:mon", "weekly:tue", "weekly:wed", "weekly:thu", "weekly:fri"},
		},
		{
			name: "weekly:t narrows to tue and thu",
			in:   "weekly:t",
			want: []string{"weekly:tue", "weekly:thu"},
		},
		{
			name: "weekly:MON is case-insensitive and matches mon",
			in:   "weekly:MON",
			want: []string{"weekly:mon"},
		},
		{
			name: "monthly:1 still returns template (user not finished)",
			in:   "monthly:1",
			want: []string{"monthly:N"},
		},
		{
			name: "days: returns template",
			in:   "days:",
			want: []string{"days:N"},
		},
		{
			name: "days:7 still returns template",
			in:   "days:7",
			want: []string{"days:N"},
		},
		{
			name: "workdays prefix matches itself",
			in:   "workd",
			want: []string{"workdays"},
		},
		{
			name: "unknown prefix returns nil",
			in:   "xyz",
			want: nil,
		},
		{
			name: "unknown kind with colon returns nil",
			in:   "yearly:",
			want: nil,
		},
		{
			name: "weekly: with extra colon past the weekday returns nil",
			// Fragment after the first colon ("mon:extra") is longer than
			// any canonical weeklyCandidates entry, so prefix-match fails
			// and the slice is empty even though kind "weekly" is known.
			in:   "weekly:mon:extra",
			want: nil,
		},
		{
			name: "uppercase input case-insensitive at top-level",
			in:   "MON",
			want: []string{"monthly:N"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Suggest(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("Suggest(%q) = %v (len=%d), want %v (len=%d)",
					tc.in, got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Suggest(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSuggest_CapAndOrderingForWeekly(t *testing.T) {
	// Explicit guard for the 5-result cap and deterministic ordering.
	got := Suggest("weekly:")
	if len(got) != 5 {
		t.Fatalf("Suggest(\"weekly:\") returned %d items, want exactly 5 (cap)", len(got))
	}
	wantOrder := []string{"weekly:mon", "weekly:tue", "weekly:wed", "weekly:thu", "weekly:fri"}
	for i, w := range wantOrder {
		if got[i] != w {
			t.Errorf("Suggest(\"weekly:\")[%d] = %q, want %q", i, got[i], w)
		}
	}
	// Ensure sat and sun do not leak into the top 5.
	for _, s := range got {
		if s == "weekly:sat" || s == "weekly:sun" {
			t.Errorf("unexpected %q in capped 5-result slice: %v", s, got)
		}
	}
}

func TestSuggest_AcceptedFormsParse(t *testing.T) {
	// Any candidate that does not contain "N", "<", or ":" (a literal
	// final form) must parse cleanly via Parse — this keeps Suggest in
	// sync with the parser for fully-formed completions.
	for _, s := range weeklyCandidates {
		if _, err := Parse(s); err != nil {
			t.Errorf("weekly candidate %q failed Parse: %v", s, err)
		}
	}
	// workdays must parse.
	if _, err := Parse("workdays"); err != nil {
		t.Errorf("workdays failed Parse: %v", err)
	}
	// Template forms like monthly:N and days:N intentionally do NOT
	// parse (user must replace N); guard that assumption is still true so
	// the hint-accept flow stays honest.
	for _, tpl := range []string{"monthly:N", "days:N"} {
		if _, err := Parse(tpl); err == nil {
			t.Errorf("template %q unexpectedly parsed (should require user to replace N)", tpl)
		}
	}
	// Sanity: the top-level "weekly:" partial should also fail to parse.
	if _, err := Parse("weekly:"); err == nil {
		t.Error("partial \"weekly:\" unexpectedly parsed")
	}
}

func TestSuggest_NoAllocForEmpty(t *testing.T) {
	// Cheap behavioral guard: empty input returns a nil slice rather than
	// an empty-but-allocated one. Matches FilterTags convention.
	got := Suggest("")
	if got != nil {
		t.Errorf("Suggest(\"\") = %v, want nil slice", got)
	}
}

func TestSuggest_CandidatesMatchGrammarHint(t *testing.T) {
	// Loose structural check: every top-level candidate's "kind" (prefix
	// up to ':' if any) appears in GrammarHint. Guards against drift.
	for _, c := range topLevelCandidates {
		kind := c
		if idx := strings.Index(c, ":"); idx >= 0 {
			kind = c[:idx]
		}
		if !strings.Contains(GrammarHint, kind) {
			t.Errorf("top-level candidate %q (kind %q) not present in GrammarHint %q", c, kind, GrammarHint)
		}
	}
}
