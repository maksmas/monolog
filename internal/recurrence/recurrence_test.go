package recurrence

import (
	"testing"
	"time"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("mustDate(%q): %v", s, err)
	}
	return d
}

func TestParse_EmptyReturnsNil(t *testing.T) {
	r, err := Parse("")
	if err != nil {
		t.Fatalf("Parse(\"\") error: %v", err)
	}
	if r != nil {
		t.Fatalf("Parse(\"\") = %v, want nil", r)
	}
}

func TestParse_Valid(t *testing.T) {
	cases := []struct {
		in       string
		wantStr  string
	}{
		{"monthly:1", "monthly:1"},
		{"monthly:15", "monthly:15"},
		{"monthly:31", "monthly:31"},
		{"weekly:mon", "weekly:mon"},
		{"weekly:sun", "weekly:sun"},
		{"workdays", "workdays"},
		{"days:1", "days:1"},
		{"days:7", "days:7"},
		{"days:365", "days:365"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			r, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.in, err)
			}
			if r == nil {
				t.Fatalf("Parse(%q) returned nil rule", tc.in)
			}
			if r.String() != tc.wantStr {
				t.Fatalf("Parse(%q).String() = %q, want %q", tc.in, r.String(), tc.wantStr)
			}
		})
	}
}

func TestParse_Invalid(t *testing.T) {
	cases := []string{
		"monthly:0",
		"monthly:32",
		"monthly:-1",
		"monthly:abc",
		"monthly:",
		"days:0",
		"days:-1",
		"days:abc",
		"days:",
		"weekly:xyz",
		"weekly:monda",
		"weekly:0",
		"weekly:8",
		"weekly:",
		"unknown",
		"unknown:1",
		"monthly",
		"weekly",
		"days",
		"monthly:1:2",
		"weekly:mon:tue",
		"workdays:1",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			r, err := Parse(tc)
			if err == nil {
				t.Fatalf("Parse(%q) = %v, want error", tc, r)
			}
		})
	}
}

func TestParse_WeeklyAliases(t *testing.T) {
	cases := []struct {
		aliases []string
		want    string
		weekday time.Weekday
	}{
		{[]string{"mon", "MON", "Mon", "monday", "MONDAY", "Monday", "1"}, "weekly:mon", time.Monday},
		{[]string{"tue", "TUE", "Tue", "tuesday", "TUESDAY", "Tuesday", "2"}, "weekly:tue", time.Tuesday},
		{[]string{"wed", "Wed", "wednesday", "Wednesday", "3"}, "weekly:wed", time.Wednesday},
		{[]string{"thu", "Thu", "thursday", "Thursday", "4"}, "weekly:thu", time.Thursday},
		{[]string{"fri", "Fri", "friday", "Friday", "5"}, "weekly:fri", time.Friday},
		{[]string{"sat", "Sat", "saturday", "Saturday", "6"}, "weekly:sat", time.Saturday},
		{[]string{"sun", "Sun", "sunday", "Sunday", "7"}, "weekly:sun", time.Sunday},
	}
	for _, tc := range cases {
		for _, alias := range tc.aliases {
			input := "weekly:" + alias
			t.Run(input, func(t *testing.T) {
				r, err := Parse(input)
				if err != nil {
					t.Fatalf("Parse(%q) error: %v", input, err)
				}
				if r.String() != tc.want {
					t.Fatalf("Parse(%q).String() = %q, want %q", input, r.String(), tc.want)
				}
				wr, ok := r.(weeklyRule)
				if !ok {
					t.Fatalf("Parse(%q) returned %T, want weeklyRule", input, r)
				}
				if wr.weekday != tc.weekday {
					t.Fatalf("Parse(%q) weekday = %v, want %v", input, wr.weekday, tc.weekday)
				}
			})
		}
	}
}

func TestParse_WorkdaysAliases(t *testing.T) {
	for _, in := range []string{"workdays", "Workdays", "WORKDAYS", "WorkDays"} {
		t.Run(in, func(t *testing.T) {
			r, err := Parse(in)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", in, err)
			}
			if r.String() != "workdays" {
				t.Fatalf("Parse(%q).String() = %q, want %q", in, r.String(), "workdays")
			}
		})
	}
}

func TestParse_WeeklyInvalidAliases(t *testing.T) {
	for _, in := range []string{"weekly:0", "weekly:8", "weekly:xyz", "weekly:monda", "weekly:"} {
		t.Run(in, func(t *testing.T) {
			if _, err := Parse(in); err == nil {
				t.Fatalf("Parse(%q) accepted, want error", in)
			}
		})
	}
}

func TestMonthlyNext(t *testing.T) {
	cases := []struct {
		name     string
		rule     string
		from     string
		want     string
	}{
		{"normal mid-month to same-anchor next month", "monthly:15", "2026-04-10", "2026-04-15"},
		{"completion on anchor date jumps to next month", "monthly:1", "2026-04-01", "2026-05-01"},
		{"after anchor rolls to next month", "monthly:1", "2026-04-02", "2026-05-01"},
		{"Feb 28 clamp from January 15, non-leap", "monthly:31", "2027-01-15", "2027-01-31"},
		{"Feb 28 clamp from February 15, non-leap", "monthly:31", "2027-02-15", "2027-02-28"},
		{"Feb 28 completion rolls to Mar 31", "monthly:31", "2027-02-28", "2027-03-31"},
		{"Feb 29 leap year", "monthly:31", "2028-02-15", "2028-02-29"},
		{"December to January wrap", "monthly:1", "2026-12-15", "2027-01-01"},
		{"clamp to April 30", "monthly:31", "2026-04-15", "2026-04-30"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(tc.rule)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.rule, err)
			}
			got := r.Next(mustDate(t, tc.from)).Format("2006-01-02")
			if got != tc.want {
				t.Fatalf("Next(%s) = %s, want %s", tc.from, got, tc.want)
			}
		})
	}
}

func TestWeeklyNext(t *testing.T) {
	cases := []struct {
		name string
		rule string
		from string
		want string
	}{
		// 2026-04-13 is a Monday.
		{"same weekday goes to next week", "weekly:mon", "2026-04-13", "2026-04-20"},
		{"mid-week to upcoming target", "weekly:fri", "2026-04-13", "2026-04-17"},
		{"Saturday to following Monday", "weekly:mon", "2026-04-18", "2026-04-20"},
		{"Sunday to following Monday", "weekly:mon", "2026-04-19", "2026-04-20"},
		{"Friday to following Monday", "weekly:mon", "2026-04-17", "2026-04-20"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(tc.rule)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.rule, err)
			}
			got := r.Next(mustDate(t, tc.from)).Format("2006-01-02")
			if got != tc.want {
				t.Fatalf("Next(%s) = %s, want %s", tc.from, got, tc.want)
			}
		})
	}
}

func TestWorkdaysNext(t *testing.T) {
	cases := []struct {
		name string
		from string
		want string
	}{
		// 2026-04-13 is Monday.
		{"Monday to Tuesday", "2026-04-13", "2026-04-14"},
		{"Tuesday to Wednesday", "2026-04-14", "2026-04-15"},
		{"Wednesday to Thursday", "2026-04-15", "2026-04-16"},
		{"Thursday to Friday", "2026-04-16", "2026-04-17"},
		{"Friday to Monday", "2026-04-17", "2026-04-20"},
		{"Saturday to Monday", "2026-04-18", "2026-04-20"},
		{"Sunday to Monday", "2026-04-19", "2026-04-20"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse("workdays")
			if err != nil {
				t.Fatalf("Parse(workdays): %v", err)
			}
			got := r.Next(mustDate(t, tc.from)).Format("2006-01-02")
			if got != tc.want {
				t.Fatalf("Next(%s) = %s, want %s", tc.from, got, tc.want)
			}
		})
	}
}

func TestDaysNext(t *testing.T) {
	cases := []struct {
		rule string
		from string
		want string
	}{
		{"days:1", "2026-04-13", "2026-04-14"},
		{"days:7", "2026-04-13", "2026-04-20"},
		{"days:30", "2026-04-13", "2026-05-13"},
		{"days:365", "2026-04-13", "2027-04-13"},
	}
	for _, tc := range cases {
		t.Run(tc.rule+"_from_"+tc.from, func(t *testing.T) {
			r, err := Parse(tc.rule)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.rule, err)
			}
			got := r.Next(mustDate(t, tc.from)).Format("2006-01-02")
			if got != tc.want {
				t.Fatalf("Next(%s) = %s, want %s", tc.from, got, tc.want)
			}
		})
	}
}

// TestNext_WesternTimezoneNoOffByOne guards against the off-by-one bug
// where a completion late in the evening in a negative-offset zone would
// fall on the next UTC date and cause Next() to return a date one too far.
// The user's local day must win, not the UTC day.
func TestNext_WesternTimezoneNoOffByOne(t *testing.T) {
	pdt := time.FixedZone("PDT", -7*3600)
	// 2026-04-30 23:00 PDT == 2026-05-01 06:00 UTC. With UTC-rounded
	// midnight the old code treated "today" as May 1 and returned May 2.
	// With zone-preserving midnight we anchor on April 30 and return May 1.
	completed := time.Date(2026, 4, 30, 23, 0, 0, 0, pdt)

	r, err := Parse("days:1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := r.Next(completed).Format("2006-01-02")
	if got != "2026-05-01" {
		t.Fatalf("days:1 Next from 2026-04-30 23:00 PDT = %s, want 2026-05-01", got)
	}

	// Also verify monthly and workdays don't skip under the same conditions.
	// 2026-04-30 is a Thursday, so workdays should yield Friday May 1.
	rw, err := Parse("workdays")
	if err != nil {
		t.Fatalf("Parse workdays: %v", err)
	}
	gotWd := rw.Next(completed).Format("2006-01-02")
	if gotWd != "2026-05-01" {
		t.Fatalf("workdays Next from 2026-04-30 23:00 PDT = %s, want 2026-05-01", gotWd)
	}

	// monthly:1 from April 30 evening local must land on May 1 local, not
	// May 1 then June 1 due to UTC already being May 1.
	rm, err := Parse("monthly:1")
	if err != nil {
		t.Fatalf("Parse monthly:1: %v", err)
	}
	gotM := rm.Next(completed).Format("2006-01-02")
	if gotM != "2026-05-01" {
		t.Fatalf("monthly:1 Next from 2026-04-30 23:00 PDT = %s, want 2026-05-01", gotM)
	}
}

func TestNext_IgnoresTimeOfDay(t *testing.T) {
	r, err := Parse("days:1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// 23:59 UTC on 2026-04-13 — Next should still anchor to 04-13 midnight
	// and return 04-14 midnight.
	completed := time.Date(2026, 4, 13, 23, 59, 0, 0, time.UTC)
	got := r.Next(completed).Format("2006-01-02")
	if got != "2026-04-14" {
		t.Fatalf("Next = %s, want 2026-04-14", got)
	}
}
