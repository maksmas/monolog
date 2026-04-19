package display

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
)

// shortDate renders t in a compact form derived from layout:
//
//   - same-year as now: drops the year token entirely
//     (for the default "02-01-2006" this yields "DD-MM")
//   - cross-year: replaces the 4-digit year token "2006" with "06"
//     (for the default "02-01-2006" this yields "DD-MM-YY")
//
// Caveat: the short-form transform assumes the year is last (i.e. layouts
// like "02-01-2006"). For year-leading layouts such as "2006-01-02", the
// cross-year form "06-01-02" is ambiguous with a DD-MM-YY date. Only
// DD-MM-YYYY is supported today — revisit if a year-leading layout is
// added to the supported table.
func shortDate(now, t time.Time, layout string) string {
	if t.Year() != now.Year() {
		// Replace full year "2006" with short year "06".
		short := strings.Replace(layout, "2006", "06", 1)
		return t.Format(short)
	}
	// Same-year: drop the year token and any adjacent separator.
	sameYear := dropYearToken(layout)
	return t.Format(sameYear)
}

// dropYearToken returns layout with the "2006" year token (and its adjacent
// separator) removed. For the default "02-01-2006" this returns "02-01".
// For a leading-year layout like "2006-01-02" this returns "01-02".
//
// Assumes the year token is delimited from adjacent tokens by a single
// non-digit separator char (e.g. '-', '/', '.', ' '). Layouts with no
// separator around "2006" (e.g. "20060102") are not supported and would
// yield a malformed stub — not reachable from the config-allowed layouts
// today, but a caveat if the supported-layouts table grows.
//
// Edge cases:
//   - layout without "2006" is returned unchanged.
//   - layout equal to exactly "2006" (year-only) returns "". Nothing useful
//     can be rendered in same-year short form for a year-only layout, so
//     the empty string signals "no same-year shortening applicable" to
//     time.Format (which simply emits nothing).
func dropYearToken(layout string) string {
	const yearTok = "2006"
	idx := strings.Index(layout, yearTok)
	if idx < 0 {
		return layout
	}
	// Drop the year token and the separator immediately before it
	// (if any) — e.g. "02-01-2006" -> "02-01".
	if idx > 0 {
		return layout[:idx-1] + layout[idx+len(yearTok):]
	}
	// Year was at the start; drop it and the following separator (if any).
	rest := layout[len(yearTok):]
	if len(rest) > 1 {
		// Skip the single separator char following the year token
		// (e.g. "2006-01-02" -> "01-02").
		return rest[1:]
	}
	// No trailing separator/content after "2006" — nothing left to render.
	return ""
}

// FormatRelDate returns a compact representation of ts relative to now.
//
//	<=1 minute -> "now"              (symmetric: both past and future within 1m inclusive)
//	<1 hour    -> "Nm"               (e.g. "5m")
//	<1 day     -> "Nh"               (e.g. "3h", 23h59m -> "23h")
//	<7 days    -> "Nd"               (e.g. "2d", 6d23h -> "6d")
//	>=7 days   -> same-year short or cross-year short of layout
//	              (for the default "02-01-2006": "DD-MM" / "DD-MM-YY")
//
// Empty or unparseable ts -> "".
// Future timestamps within 1 minute tolerance (inclusive) -> "now"; any
// further-future timestamp skips the Nm/Nh/Nd tiers entirely (those are
// past-only) and routes straight to the compact date form derived from
// layout. This asymmetry is intentional: future relative values like "+3h"
// would misread as past in the column where this helper is used.
func FormatRelDate(now time.Time, ts string, layout string) string {
	if ts == "" {
		return ""
	}

	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}

	diff := now.Sub(t)

	// Future timestamp handling
	if diff < 0 {
		if diff >= -time.Minute {
			return "now"
		}
		return shortDate(now, t, layout)
	}

	switch {
	case diff <= time.Minute:
		return "now"
	case diff < time.Hour:
		return fmt.Sprintf("%dm", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh", int(diff.Hours()))
	case diff < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(diff.Hours()/24))
	default:
		return shortDate(now, t, layout)
	}
}

// FormatTaskDates returns the compact date column value for a task:
//
//	open: "<created>"
//	done: "<created>→<updated>"
//	both empty -> ""
//
// layout is the configured date format (Go layout); callers usually pass
// config.DateFormat().
func FormatTaskDates(now time.Time, t model.Task, layout string) string {
	created := FormatRelDate(now, t.CreatedAt, layout)
	if t.Status == "done" {
		updated := FormatRelDate(now, t.UpdatedAt, layout)
		if created == "" && updated == "" {
			return ""
		}
		return created + "→" + updated
	}
	return created
}
