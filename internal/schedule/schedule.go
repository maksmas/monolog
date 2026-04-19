// Package schedule converts between bucket names and ISO dates and computes
// which virtual bucket a stored schedule date falls into.
//
// Storage vs. user-facing format split:
//   - Storage: Task.Schedule on disk is always ISO (YYYY-MM-DD / IsoLayout).
//     This format is fixed and never changes.
//   - User-facing: the canonical input layout and display format are
//     configurable via internal/config (default DD-MM-YYYY). Parse accepts
//     the caller-provided layout as an explicit parameter so this package
//     does not depend on internal/config. FormatDisplay renders a stored
//     ISO date into a caller-chosen layout for user-facing output.
//
// Parse tolerates legacy ISO input silently (in addition to the configured
// layout) so pre-existing scripts keep working after a format switch.
//
// The five bucket names (today, tomorrow, week, month, someday) exist only
// as input shorthand and as display-time predicates over schedule vs.
// today's date. Legacy bucket strings written by older versions are
// tolerated by Bucket and Normalize so that on-disk data migrates lazily as
// tasks are touched.
package schedule

import (
	"errors"
	"fmt"
	"time"
)

// IsoLayout is the on-disk schedule date format. It serves two roles:
//   - canonical storage format for Task.Schedule in JSON files on disk;
//   - silent legacy-fallback input parser inside Parse, so scripts or
//     user input still using ISO continue to work after the user-facing
//     format is switched via internal/config.
const IsoLayout = "2006-01-02"

// Bucket names. These are valid Parse inputs and possible Bucket outputs.
const (
	Today    = "today"
	Tomorrow = "tomorrow"
	Week     = "week"
	Month    = "month"
	Someday  = "someday"
)

// IsBucket reports whether s is one of the five bucket names.
func IsBucket(s string) bool {
	switch s {
	case Today, Tomorrow, Week, Month, Someday:
		return true
	}
	return false
}

// IsISODate reports whether s parses as the on-disk storage format
// (YYYY-MM-DD / IsoLayout). This is a storage-format validation helper,
// not a user-input validator — callers that need to accept the user-facing
// layout (configurable via internal/config) must use Parse instead, which
// accepts both the configured layout and legacy ISO input. Normalize,
// Bucket, and FormatDisplay do not route through IsISODate; they call
// time.Parse(IsoLayout, ...) directly. Kept exported as a stable API entry
// point for external callers and test coverage, even though the in-tree
// production call sites have all migrated to schedule.Parse.
func IsISODate(s string) bool {
	_, err := time.Parse(IsoLayout, s)
	return err == nil
}

// todayDate returns now's UTC date at midnight.
func todayDate(now time.Time) time.Time {
	n := now.UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// ErrInvalid is the sentinel error returned by Parse when the input is
// neither a bucket name, a date in the passed layout, nor a legacy ISO
// date. Callers wrap it into their own user-facing message so they can
// reference the currently configured format label (via config.DateFormatLabel)
// without dragging a config dependency into this package.
var ErrInvalid = errors.New("invalid schedule")

// Parse turns user input into an ISO date string. Accepts, in order:
//
//  1. one of the five bucket names (today, tomorrow, week, month, someday),
//     resolved relative to now: today, today+1, today+7, today+31, today+365;
//  2. a date in the passed layout (the caller typically passes
//     config.DateFormat());
//  3. a legacy ISO date (silent backward compat so pre-existing scripts
//     continue to work after a format switch).
//
// layout == "" means "ISO-only": the configured-layout attempt is skipped
// and only bucket names and legacy ISO input are accepted (used internally
// by Normalize where the layout is irrelevant to bucket resolution).
//
// On failure it returns ErrInvalid wrapped with the offending input, so
// callers can use errors.Is and format the user-facing message themselves.
func Parse(input string, now time.Time, layout string) (string, error) {
	today := todayDate(now)
	switch input {
	case Today:
		return today.Format(IsoLayout), nil
	case Tomorrow:
		return today.AddDate(0, 0, 1).Format(IsoLayout), nil
	case Week:
		return today.AddDate(0, 0, 7).Format(IsoLayout), nil
	case Month:
		return today.AddDate(0, 0, 31).Format(IsoLayout), nil
	case Someday:
		return today.AddDate(0, 0, 365).Format(IsoLayout), nil
	}
	// Try the configured layout first. If the caller's layout happens to
	// equal IsoLayout, the subsequent legacy-ISO branch covers the same
	// input, so we skip the duplicate attempt. If the configured layout
	// parse fails, the legacy-ISO branch still gives the input a chance.
	if layout != "" && layout != IsoLayout {
		if t, err := time.Parse(layout, input); err == nil {
			return t.Format(IsoLayout), nil
		}
	}
	if t, err := time.Parse(IsoLayout, input); err == nil {
		return t.Format(IsoLayout), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalid, input)
}

// FormatDisplay renders an ISO date string in the given layout for display.
// Empty input, bucket names, or anything else not parseable as an ISO date
// passes through unchanged so callers don't need to guard; this keeps the
// helper safe to call on raw task.Schedule values which may still hold a
// legacy bucket string.
func FormatDisplay(iso, layout string) string {
	t, err := time.Parse(IsoLayout, iso)
	if err != nil {
		return iso
	}
	if layout == "" {
		return iso
	}
	return t.Format(layout)
}

// Bucket returns the virtual bucket (today/tomorrow/week/month/someday) that
// the stored schedule falls into. Accepts ISO dates and legacy bucket
// strings. Anything unrecognized (empty, malformed) is treated as today so
// the task remains visible in the default list.
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
	plus31 := today.AddDate(0, 0, 31)
	switch {
	case !d.After(today):
		return Today
	case d.Equal(plus1):
		return Tomorrow
	case d.After(plus1) && !d.After(plus7):
		return Week
	case d.After(plus7) && !d.After(plus31):
		return Month
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
		// Bucket names are resolved by Parse before the layout branch runs,
		// so we pass "" to make the irrelevance of the layout explicit.
		s, _ := Parse(schedule, now, "")
		return s
	}
	return schedule
}
