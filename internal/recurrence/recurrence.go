// Package recurrence parses and evaluates recurring-task rules.
//
// Four grammar forms are supported:
//   - monthly:N        (N=1..31, clamped to month-end for short months)
//   - weekly:<day>     (<day>: mon|tue|wed|thu|fri|sat|sun, full names like
//                       monday|tuesday|..., or numeric 1..7 ISO-8601 Mon=1.
//                       Case-insensitive on input; canonical three-letter
//                       lowercase form emitted by Rule.String().)
//   - workdays         (exact literal, case-insensitive on input)
//   - days:N           (N>=1, every N days from completion)
//
// Parse validates the grammar and returns a Rule whose Next method returns
// the first matching date strictly after the given completion date (date-only
// comparison; time-of-day is ignored — completion is rounded to midnight UTC).
// Empty string parses to (nil, nil) — callers treat nil Rule as "not recurring".
package recurrence

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Rule is a parsed recurrence spec. Implementations are immutable and safe
// to share.
type Rule interface {
	// Next returns the date (midnight UTC) of the next occurrence strictly
	// after completedAt. Time-of-day on completedAt is ignored.
	Next(completedAt time.Time) time.Time
	// String returns the canonical grammar form of the rule. Round-trips
	// through Parse land on the same canonical spelling regardless of input
	// alias (e.g. Parse("weekly:Monday").String() == "weekly:mon").
	String() string
}

// Parse validates the grammar and returns a Rule. Empty string returns
// (nil, nil) — callers treat nil Rule as "not recurring".
func Parse(s string) (Rule, error) {
	if s == "" {
		return nil, nil
	}
	lower := strings.ToLower(s)

	// workdays (no colon)
	if lower == "workdays" {
		return workdaysRule{}, nil
	}

	// everything else uses "kind:arg"
	idx := strings.Index(lower, ":")
	if idx < 0 {
		return nil, fmt.Errorf("invalid recurrence %q: unknown rule", s)
	}
	kind := lower[:idx]
	arg := lower[idx+1:]
	if arg == "" {
		return nil, fmt.Errorf("invalid recurrence %q: missing argument", s)
	}
	// Reject extra colons.
	if strings.Contains(arg, ":") {
		return nil, fmt.Errorf("invalid recurrence %q: extra colon", s)
	}

	switch kind {
	case "monthly":
		n, err := strconv.Atoi(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid recurrence %q: monthly day must be an integer", s)
		}
		if n < 1 || n > 31 {
			return nil, fmt.Errorf("invalid recurrence %q: monthly day must be 1..31", s)
		}
		return monthlyRule{day: n}, nil
	case "weekly":
		wd, ok := parseWeekday(arg)
		if !ok {
			return nil, fmt.Errorf("invalid recurrence %q: unknown weekday %q", s, arg)
		}
		return weeklyRule{weekday: wd}, nil
	case "days":
		n, err := strconv.Atoi(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid recurrence %q: days count must be an integer", s)
		}
		if n < 1 {
			return nil, fmt.Errorf("invalid recurrence %q: days count must be >= 1", s)
		}
		return daysRule{n: n}, nil
	default:
		return nil, fmt.Errorf("invalid recurrence %q: unknown rule %q", s, kind)
	}
}

// parseWeekday normalizes a lowercased weekday token. Returns the parsed
// time.Weekday and true on success.
func parseWeekday(arg string) (time.Weekday, bool) {
	switch arg {
	case "mon", "monday", "1":
		return time.Monday, true
	case "tue", "tuesday", "2":
		return time.Tuesday, true
	case "wed", "wednesday", "3":
		return time.Wednesday, true
	case "thu", "thursday", "4":
		return time.Thursday, true
	case "fri", "friday", "5":
		return time.Friday, true
	case "sat", "saturday", "6":
		return time.Saturday, true
	case "sun", "sunday", "7":
		return time.Sunday, true
	}
	return 0, false
}

// weekdayShort returns the three-letter lowercase canonical name.
func weekdayShort(wd time.Weekday) string {
	switch wd {
	case time.Monday:
		return "mon"
	case time.Tuesday:
		return "tue"
	case time.Wednesday:
		return "wed"
	case time.Thursday:
		return "thu"
	case time.Friday:
		return "fri"
	case time.Saturday:
		return "sat"
	case time.Sunday:
		return "sun"
	}
	return ""
}

// midnightUTC rounds t to midnight UTC on the same calendar date as its UTC
// representation.
func midnightUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// daysInMonth returns the number of days in the given (year, month).
func daysInMonth(year int, month time.Month) int {
	// Day 0 of month+1 == last day of month.
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// ---- monthlyRule ----

type monthlyRule struct {
	day int // 1..31
}

func (r monthlyRule) String() string {
	return fmt.Sprintf("monthly:%d", r.day)
}

func (r monthlyRule) Next(completedAt time.Time) time.Time {
	today := midnightUTC(completedAt)
	y, m, _ := today.Date()

	// Try this month's anchor day first.
	candidate := monthlyAnchor(y, m, r.day)
	if candidate.After(today) {
		return candidate
	}
	// Advance to next month.
	m2 := m + 1
	y2 := y
	if m2 > 12 {
		m2 = 1
		y2 = y + 1
	}
	return monthlyAnchor(y2, m2, r.day)
}

// monthlyAnchor returns the day-th of (y, m), clamped to the last day of the
// month when day exceeds days-in-month.
func monthlyAnchor(y int, m time.Month, day int) time.Time {
	last := daysInMonth(y, m)
	d := day
	if d > last {
		d = last
	}
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ---- weeklyRule ----

type weeklyRule struct {
	weekday time.Weekday
}

func (r weeklyRule) String() string {
	return "weekly:" + weekdayShort(r.weekday)
}

func (r weeklyRule) Next(completedAt time.Time) time.Time {
	today := midnightUTC(completedAt)
	diff := int(r.weekday - today.Weekday())
	if diff <= 0 {
		diff += 7
	}
	return today.AddDate(0, 0, diff)
}

// ---- workdaysRule ----

type workdaysRule struct{}

func (workdaysRule) String() string { return "workdays" }

func (workdaysRule) Next(completedAt time.Time) time.Time {
	today := midnightUTC(completedAt)
	for i := 1; i <= 7; i++ {
		cand := today.AddDate(0, 0, i)
		wd := cand.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			return cand
		}
	}
	// Unreachable: at least one weekday lies within any 7-day span.
	return today
}

// ---- daysRule ----

type daysRule struct {
	n int // >= 1
}

func (r daysRule) String() string {
	return fmt.Sprintf("days:%d", r.n)
}

func (r daysRule) Next(completedAt time.Time) time.Time {
	return midnightUTC(completedAt).AddDate(0, 0, r.n)
}
