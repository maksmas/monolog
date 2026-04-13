package display

import (
	"fmt"
	"time"
)

// FormatRelDate returns a compact representation of ts relative to now.
//
//	<1 minute  -> "now"
//	<1 hour    -> "Nm"        (e.g. "5m")
//	<1 day     -> "Nh"        (e.g. "3h", 23h59m -> "23h")
//	<7 days    -> "Nd"        (e.g. "2d", 6d23h -> "6d")
//	>=7 days   -> "MM-DD"     (same year) or "YY-MM-DD" (different year)
//
// Empty or unparseable ts -> "".
// Future timestamps within 1 minute tolerance -> "now"; further future -> "MM-DD".
func FormatRelDate(now time.Time, ts string) string {
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
		// Further future: show as date
		if t.Year() != now.Year() {
			return fmt.Sprintf("%02d-%02d-%02d", t.Year()%100, t.Month(), t.Day())
		}
		return fmt.Sprintf("%02d-%02d", t.Month(), t.Day())
	}

	switch {
	case diff < time.Minute:
		return "now"
	case diff < time.Hour:
		return fmt.Sprintf("%dm", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh", int(diff.Hours()))
	case diff < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(diff.Hours()/24))
	default:
		if t.Year() != now.Year() {
			return fmt.Sprintf("%02d-%02d-%02d", t.Year()%100, t.Month(), t.Day())
		}
		return fmt.Sprintf("%02d-%02d", t.Month(), t.Day())
	}
}
