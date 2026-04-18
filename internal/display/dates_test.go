package display

import (
	"testing"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
)

// ddmmyyyy is the default user-facing layout (DD-MM-YYYY).
const ddmmyyyy = "02-01-2006"

// yyyymmdd is the alternative layout used to prove the layout parameter is
// wired through rather than ignored.
const yyyymmdd = "2006-01-02"

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

		// now boundary includes exactly 1m (symmetric with future)
		{"1m ago", now.Add(-1 * time.Minute).Format(time.RFC3339), "now"},

		// minutes: (1m, 1h)
		{"61s ago (first non-now)", now.Add(-61 * time.Second).Format(time.RFC3339), "1m"},
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

		// date format: >=7 days (DD-MM for same year, DD-MM-YY for cross-year)
		{"7d ago (boundary)", now.Add(-7 * 24 * time.Hour).Format(time.RFC3339), "06-04"},
		{"older same year", time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC).Format(time.RFC3339), "28-03"},
		{"older different year", time.Date(2025, 3, 28, 10, 0, 0, 0, time.UTC).Format(time.RFC3339), "28-03-25"},

		// future: within 1 min tolerance -> "now"
		{"future 30s", now.Add(30 * time.Second).Format(time.RFC3339), "now"},
		{"future exactly 1m", now.Add(1 * time.Minute).Format(time.RFC3339), "now"},

		// future: beyond 1 min -> DD-MM (same year) or DD-MM-YY (cross year)
		{"future 61s (first non-now)", now.Add(61 * time.Second).Format(time.RFC3339), "13-04"},
		{"future >1m same year", now.Add(2 * time.Minute).Format(time.RFC3339), "13-04"},
		{"future >1m different year", time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), "15-01-27"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatRelDate(now, tt.ts, ddmmyyyy)
			if got != tt.want {
				t.Errorf("FormatRelDate(now, %q, %q) = %q, want %q", tt.ts, ddmmyyyy, got, tt.want)
			}
		})
	}
}

// TestFormatRelDate_AlternativeLayout confirms the layout parameter is
// actually wired through — rendering under YYYY-MM-DD must produce the ISO
// form, not the default DD-MM-YYYY.
func TestFormatRelDate_AlternativeLayout(t *testing.T) {
	now := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ts   string
		want string
	}{
		// Same-year: drop year token from layout "2006-01-02" -> "01-02"
		// so "older same year" becomes "03-28" (MM-DD).
		{"older same year", time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC).Format(time.RFC3339), "03-28"},
		// Cross-year: replace "2006" with "06" -> "06-01-02", so
		// "older different year" renders as "25-03-28" (YY-MM-DD).
		{"older different year", time.Date(2025, 3, 28, 10, 0, 0, 0, time.UTC).Format(time.RFC3339), "25-03-28"},
		// Relative (non-date) branches still bypass layout.
		{"2d ago", now.Add(-2 * 24 * time.Hour).Format(time.RFC3339), "2d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatRelDate(now, tt.ts, yyyymmdd)
			if got != tt.want {
				t.Errorf("FormatRelDate(now, %q, %q) = %q, want %q", tt.ts, yyyymmdd, got, tt.want)
			}
		})
	}
}

func TestFormatTaskDates(t *testing.T) {
	now := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		task model.Task
		want string
	}{
		{
			name: "open task with recent CreatedAt",
			task: model.Task{
				Status:    "open",
				CreatedAt: now.Add(-2 * 24 * time.Hour).Format(time.RFC3339),
			},
			want: "2d",
		},
		{
			name: "done task with both timestamps",
			task: model.Task{
				Status:    "done",
				CreatedAt: now.Add(-5 * 24 * time.Hour).Format(time.RFC3339),
				UpdatedAt: now.Add(-1 * time.Hour).Format(time.RFC3339),
			},
			want: "5d\u2192" + "1h",
		},
		{
			name: "both timestamps empty on open task",
			task: model.Task{
				Status: "open",
			},
			want: "",
		},
		{
			name: "done task with both timestamps empty",
			task: model.Task{
				Status: "done",
			},
			want: "",
		},
		{
			name: "done task with only CreatedAt empty",
			task: model.Task{
				Status:    "done",
				UpdatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
			},
			want: "\u2192" + "30m",
		},
		{
			name: "done task with only UpdatedAt empty",
			task: model.Task{
				Status:    "done",
				CreatedAt: now.Add(-3 * 24 * time.Hour).Format(time.RFC3339),
			},
			want: "3d\u2192",
		},
		{
			name: "open task with old CreatedAt shows date",
			task: model.Task{
				Status:    "open",
				CreatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
			},
			want: "01-03",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatTaskDates(now, tt.task, ddmmyyyy)
			if got != tt.want {
				t.Errorf("FormatTaskDates() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDropYearToken covers the helper's layout transforms directly so the
// subtle slice arithmetic is guarded against edge cases (leading year, no
// separator, missing token, year-only layout).
func TestDropYearToken(t *testing.T) {
	cases := []struct {
		name   string
		layout string
		want   string
	}{
		{name: "year_trailing_dash", layout: "02-01-2006", want: "02-01"},
		{name: "year_leading_dash", layout: "2006-01-02", want: "01-02"},
		{name: "year_trailing_slash", layout: "01/02/2006", want: "01/02"},
		{name: "year_leading_slash", layout: "2006/01/02", want: "01/02"},
		{name: "no_year_token", layout: "01-02", want: "01-02"},
		{name: "year_only_layout", layout: "2006", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dropYearToken(tc.layout); got != tc.want {
				t.Errorf("dropYearToken(%q) = %q, want %q", tc.layout, got, tc.want)
			}
		})
	}
}

// TestShortDate_EdgeLayouts pins the behavior of shortDate on boundary
// layouts not reachable via the config-allowed table today, so if a new
// supported layout ever exposes these shapes the regression shows up here
// rather than in user-facing output.
//
//   - year-only layout ("2006"): cross-year falls through strings.Replace and
//     yields the short year "06"; same-year drops to "" via dropYearToken.
//   - literal "2006" token outside the year position: strings.Replace targets
//     only the first occurrence, which is the year in practice.
func TestShortDate_EdgeLayouts(t *testing.T) {
	now := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	crossYear := time.Date(2025, 3, 28, 10, 0, 0, 0, time.UTC)
	sameYear := time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		t      time.Time
		layout string
		want   string
	}{
		{name: "year_only_cross_year", t: crossYear, layout: "2006", want: "25"},
		{name: "year_only_same_year", t: sameYear, layout: "2006", want: ""},
		// A layout containing "2006" twice only has the first instance
		// rewritten by strings.Replace; pin that so a future refactor that
		// switches to ReplaceAll is caught.
		{name: "double_year_token_cross_year", t: crossYear, layout: "2006-2006", want: "25-2025"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortDate(now, tc.t, tc.layout); got != tc.want {
				t.Errorf("shortDate(now, %v, %q) = %q, want %q", tc.t, tc.layout, got, tc.want)
			}
		})
	}
}

// TestFormatTaskDates_AlternativeLayout proves the layout parameter flows
// through FormatTaskDates to FormatRelDate.
func TestFormatTaskDates_AlternativeLayout(t *testing.T) {
	now := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	task := model.Task{
		Status:    "open",
		CreatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
	got := FormatTaskDates(now, task, yyyymmdd)
	want := "03-01"
	if got != want {
		t.Errorf("FormatTaskDates(layout=%q) = %q, want %q", yyyymmdd, got, want)
	}
}
