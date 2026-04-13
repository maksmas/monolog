// Package schedule converts between bucket names and ISO dates and computes
// which virtual bucket a stored schedule date falls into.
//
// Tasks store schedule as an ISO date (YYYY-MM-DD). The four bucket names
// (today, tomorrow, week, someday) exist only as input shorthand and as
// display-time predicates over schedule vs. today's date. Legacy bucket
// strings written by older versions are tolerated by Bucket and Normalize so
// that on-disk data migrates lazily as tasks are touched.
package schedule

import (
	"fmt"
	"time"
)

// IsoLayout is the on-disk schedule date format.
const IsoLayout = "2006-01-02"

// Bucket names. These are valid Parse inputs and possible Bucket outputs.
const (
	Today    = "today"
	Tomorrow = "tomorrow"
	Week     = "week"
	Someday  = "someday"
)

// IsBucket reports whether s is one of the four bucket names.
func IsBucket(s string) bool {
	switch s {
	case Today, Tomorrow, Week, Someday:
		return true
	}
	return false
}

// IsISODate reports whether s parses as YYYY-MM-DD.
func IsISODate(s string) bool {
	_, err := time.Parse(IsoLayout, s)
	return err == nil
}

// todayDate returns now's UTC date at midnight.
func todayDate(now time.Time) time.Time {
	n := now.UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// Parse turns user input (a bucket name or ISO date) into an ISO date string.
// Bucket names are resolved relative to now: today, today+1, today+7, today+365.
func Parse(input string, now time.Time) (string, error) {
	today := todayDate(now)
	switch input {
	case Today:
		return today.Format(IsoLayout), nil
	case Tomorrow:
		return today.AddDate(0, 0, 1).Format(IsoLayout), nil
	case Week:
		return today.AddDate(0, 0, 7).Format(IsoLayout), nil
	case Someday:
		return today.AddDate(0, 0, 365).Format(IsoLayout), nil
	}
	if IsISODate(input) {
		return input, nil
	}
	return "", fmt.Errorf("invalid schedule %q: must be today, tomorrow, week, someday, or ISO date (YYYY-MM-DD)", input)
}

// Bucket returns the virtual bucket (today/tomorrow/week/someday) that the
// stored schedule falls into. Accepts ISO dates and legacy bucket strings.
// Anything unrecognized (empty, malformed) is treated as today so the task
// remains visible in the default list.
func Bucket(schedule string, now time.Time) string {
	if IsBucket(schedule) {
		return schedule
	}
	d, err := time.Parse(IsoLayout, schedule)
	if err != nil {
		return Today
	}
	today := todayDate(now)
	plus1 := today.AddDate(0, 0, 1)
	plus7 := today.AddDate(0, 0, 7)
	switch {
	case !d.After(today):
		return Today
	case d.Equal(plus1):
		return Tomorrow
	case d.After(plus1) && !d.After(plus7):
		return Week
	default:
		return Someday
	}
}

// MatchesBucket reports whether a stored schedule belongs to the given bucket.
func MatchesBucket(schedule, bucket string, now time.Time) bool {
	return Bucket(schedule, now) == bucket
}

// Normalize converts a legacy bucket string to its ISO equivalent. ISO dates
// pass through unchanged. Unrecognized values pass through unchanged so the
// caller can decide what to do.
func Normalize(schedule string, now time.Time) string {
	if IsBucket(schedule) {
		s, _ := Parse(schedule, now)
		return s
	}
	return schedule
}
