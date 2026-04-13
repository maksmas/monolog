package display

import (
	"testing"
	"time"
)

func TestFormatRelDate(t *testing.T) {
	now := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ts   string
		want string
	}{
		// empty / malformed
		{"empty string", "", ""},
		{"malformed string", "not-a-date", ""},

		// now: [0, 1m)
		{"0s ago", now.Format(time.RFC3339), "now"},
		{"30s ago", now.Add(-30 * time.Second).Format(time.RFC3339), "now"},
		{"59s ago", now.Add(-59 * time.Second).Format(time.RFC3339), "now"},

		// minutes: [1m, 1h)
		{"1m ago", now.Add(-1 * time.Minute).Format(time.RFC3339), "1m"},
		{"5m ago", now.Add(-5 * time.Minute).Format(time.RFC3339), "5m"},
		{"59m ago", now.Add(-59 * time.Minute).Format(time.RFC3339), "59m"},

		// hours: [1h, 24h)
		{"1h ago", now.Add(-1 * time.Hour).Format(time.RFC3339), "1h"},
		{"3h ago", now.Add(-3 * time.Hour).Format(time.RFC3339), "3h"},
		{"23h59m ago", now.Add(-23*time.Hour - 59*time.Minute).Format(time.RFC3339), "23h"},

		// days: [24h, 7*24h)
		{"24h ago (boundary)", now.Add(-24 * time.Hour).Format(time.RFC3339), "1d"},
		{"2d ago", now.Add(-2 * 24 * time.Hour).Format(time.RFC3339), "2d"},
		{"6d ago", now.Add(-6 * 24 * time.Hour).Format(time.RFC3339), "6d"},
		{"6d23h59m ago", now.Add(-6*24*time.Hour - 23*time.Hour - 59*time.Minute).Format(time.RFC3339), "6d"},

		// date format: >=7 days
		{"7d ago (boundary)", now.Add(-7 * 24 * time.Hour).Format(time.RFC3339), "04-06"},
		{"older same year", time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC).Format(time.RFC3339), "03-28"},
		{"older different year", time.Date(2025, 3, 28, 10, 0, 0, 0, time.UTC).Format(time.RFC3339), "25-03-28"},

		// future: within 1 min tolerance -> "now"
		{"future 30s", now.Add(30 * time.Second).Format(time.RFC3339), "now"},
		{"future exactly 1m", now.Add(1 * time.Minute).Format(time.RFC3339), "now"},

		// future: beyond 1 min -> MM-DD
		{"future >1m same year", now.Add(2 * time.Minute).Format(time.RFC3339), "04-13"},
		{"future >1m different year", time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), "27-01-15"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatRelDate(now, tt.ts)
			if got != tt.want {
				t.Errorf("FormatRelDate(now, %q) = %q, want %q", tt.ts, got, tt.want)
			}
		})
	}
}
