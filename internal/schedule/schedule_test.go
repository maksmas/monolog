package schedule

import (
	"errors"
	"testing"
	"time"
)

// fixedNow is a deterministic clock used across tests.
var fixedNow = time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

func TestIsISODate(t *testing.T) {
	cases := map[string]bool{
		"2026-04-13": true,
		"2026-12-31": true,
		"2026-13-01": false,
		"today":      false,
		"":           false,
		"2026/04/13": false,
	}
	for s, want := range cases {
		if got := IsISODate(s); got != want {
			t.Errorf("IsISODate(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestParse_Buckets(t *testing.T) {
	cases := map[string]string{
		Today:    "2026-04-13",
		Tomorrow: "2026-04-14",
		Week:     "2026-04-20",
		Month:    "2026-05-14",
		Someday:  "2027-04-13",
	}
	for in, want := range cases {
		got, err := Parse(in, fixedNow, "02-01-2006")
		if err != nil {
			t.Fatalf("Parse(%q): unexpected error %v", in, err)
		}
		if got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParse_DDMMYYYY(t *testing.T) {
	got, err := Parse("15-04-2026", fixedNow, "02-01-2006")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2026-04-15" {
		t.Errorf("Parse(DD-MM-YYYY) = %q, want 2026-04-15", got)
	}
}

func TestParse_AlternativeLayout(t *testing.T) {
	// Verify the layout parameter is actually wired through and not ignored
	// by exercising a format that would be invalid under the default.
	got, err := Parse("04/15/2026", fixedNow, "01/02/2006")
	if err != nil {
		t.Fatalf("unexpected error with alternative layout: %v", err)
	}
	if got != "2026-04-15" {
		t.Errorf("Parse(MM/DD/YYYY) = %q, want 2026-04-15", got)
	}

	// And the same input under the default layout must be rejected.
	if _, err := Parse("04/15/2026", fixedNow, "02-01-2006"); err == nil {
		t.Errorf("Parse(04/15/2026) under DD-MM-YYYY: expected error, got nil")
	}
}

func TestParse_LegacyISOStillAccepted(t *testing.T) {
	// After the layout flip, legacy ISO input from pre-existing scripts must
	// still silently succeed and round-trip unchanged.
	got, err := Parse("2026-05-01", fixedNow, "02-01-2006")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2026-05-01" {
		t.Errorf("Parse(legacy ISO) = %q, want 2026-05-01", got)
	}
}

func TestParse_Invalid(t *testing.T) {
	for _, s := range []string{"garbage", "yesterday", "2026-13-01", ""} {
		_, err := Parse(s, fixedNow, "02-01-2006")
		if err == nil {
			t.Errorf("Parse(%q): expected error, got nil", s)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) error = %v, want errors.Is(ErrInvalid)", s, err)
		}
	}
}

func TestFormatDisplay(t *testing.T) {
	cases := []struct {
		name   string
		iso    string
		layout string
		want   string
	}{
		{name: "DDMMYYYY", iso: "2026-04-15", layout: "02-01-2006", want: "15-04-2026"},
		{name: "alt_MMDDYYYY", iso: "2026-04-15", layout: "01/02/2006", want: "04/15/2026"},
		{name: "empty_passthrough", iso: "", layout: "02-01-2006", want: ""},
		{name: "bucket_passthrough", iso: "today", layout: "02-01-2006", want: "today"},
		// Guard: bucket names must pass through under any layout, not just
		// the default DD-MM-YYYY. Without this, a future FormatDisplay that
		// happens to special-case the default layout could silently mangle
		// bucket strings when the user switches formats.
		{name: "bucket_passthrough_alt_layout", iso: "today", layout: "2006-01-02", want: "today"},
		{name: "junk_passthrough", iso: "not-a-date", layout: "02-01-2006", want: "not-a-date"},
		{name: "empty_layout_passthrough", iso: "2026-04-15", layout: "", want: "2026-04-15"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDisplay(tc.iso, tc.layout); got != tc.want {
				t.Errorf("FormatDisplay(%q, %q) = %q, want %q", tc.iso, tc.layout, got, tc.want)
			}
		})
	}
}

func TestBucket_FromISODate(t *testing.T) {
	cases := []struct {
		schedule string
		want     string
	}{
		{"2020-01-01", Today}, // far past
		{"2026-04-12", Today}, // yesterday
		{"2026-04-13", Today}, // today
		{"2026-04-14", Tomorrow},
		{"2026-04-15", Week},    // today+2
		{"2026-04-20", Week},    // today+7 boundary
		{"2026-04-21", Month},   // today+8
		{"2026-05-14", Month},   // today+31 boundary
		{"2026-05-15", Someday}, // today+32
		{"2027-04-13", Someday},
	}
	for _, tc := range cases {
		if got := Bucket(tc.schedule, fixedNow); got != tc.want {
			t.Errorf("Bucket(%q) = %q, want %q", tc.schedule, got, tc.want)
		}
	}
}

func TestBucket_LegacyStrings(t *testing.T) {
	for _, b := range []string{Today, Tomorrow, Week, Month, Someday} {
		if got := Bucket(b, fixedNow); got != b {
			t.Errorf("Bucket(%q) = %q, want %q", b, got, b)
		}
	}
}

func TestBucket_Unrecognized(t *testing.T) {
	for _, s := range []string{"", "garbage", "2026/04/13"} {
		if got := Bucket(s, fixedNow); got != Today {
			t.Errorf("Bucket(%q) = %q, want %q (default)", s, got, Today)
		}
	}
}

func TestMatchesBucket(t *testing.T) {
	if !MatchesBucket("2026-04-14", Tomorrow, fixedNow) {
		t.Error("ISO date today+1 should match tomorrow bucket")
	}
	if MatchesBucket("2026-04-14", Today, fixedNow) {
		t.Error("ISO date today+1 should not match today bucket")
	}
	if !MatchesBucket(Tomorrow, Tomorrow, fixedNow) {
		t.Error("legacy string tomorrow should match tomorrow bucket")
	}
}

func TestNormalize_LegacyStrings(t *testing.T) {
	cases := map[string]string{
		Today:    "2026-04-13",
		Tomorrow: "2026-04-14",
		Week:     "2026-04-20",
		Month:    "2026-05-14",
		Someday:  "2027-04-13",
	}
	for in, want := range cases {
		if got := Normalize(in, fixedNow); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalize_ISOPassThrough(t *testing.T) {
	if got := Normalize("2026-05-01", fixedNow); got != "2026-05-01" {
		t.Errorf("Normalize(ISO) = %q, want 2026-05-01", got)
	}
}

func TestNormalize_UnrecognizedPassThrough(t *testing.T) {
	if got := Normalize("garbage", fixedNow); got != "garbage" {
		t.Errorf("Normalize(garbage) = %q, want garbage (pass-through)", got)
	}
}

func TestParseRelative(t *testing.T) {
	// fixedNow = 2026-04-13
	cases := []struct {
		input   string
		wantISO string
		wantOK  bool
	}{
		// days
		{"3d", "2026-04-16", true},
		{"1d", "2026-04-14", true},
		{"0d", "2026-04-13", true}, // N=0 is valid: today
		{"10D", "2026-04-23", true}, // uppercase D
		// weeks
		{"2w", "2026-04-27", true},
		{"1w", "2026-04-20", true},
		{"2W", "2026-04-27", true}, // uppercase W
		// months
		{"1m", "2026-05-13", true},
		{"3m", "2026-07-13", true},
		{"1M", "2026-05-13", true}, // uppercase M
		// non-matching inputs — too many digits (>4) → no match
		{"99999d", "", false},
		// non-matching inputs → false
		{"", "", false},
		{"3x", "", false},  // unsupported suffix
		{"day", "", false}, // full word, not shorthand
		{"week", "", false},
		{"d3", "", false},  // suffix before digit
		{"-1d", "", false}, // negative number
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, ok := ParseRelative(tc.input, fixedNow)
			if ok != tc.wantOK {
				t.Errorf("ParseRelative(%q) ok=%v, want %v", tc.input, ok, tc.wantOK)
			}
			if ok && got != tc.wantISO {
				t.Errorf("ParseRelative(%q) = %q, want %q", tc.input, got, tc.wantISO)
			}
		})
	}
}

func TestParseRelative_MonthOverflow(t *testing.T) {
	// Go's time.AddDate does NOT clamp month-end: Jan 31 + 1 month overflows
	// into March (Mar 3 for non-leap year, Mar 2 for leap year). This test
	// documents the actual stdlib behaviour so any future change is explicit.
	jan31 := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	got, ok := ParseRelative("1m", jan31)
	if !ok {
		t.Fatal("ParseRelative(1m) unexpectedly returned false")
	}
	// Jan 31 + 1 month = Feb 31 → Go normalises to Mar 3 (2026 is not leap).
	if got != "2026-03-03" {
		t.Errorf("ParseRelative(1m, Jan 31 2026) = %q, want 2026-03-03 (stdlib AddDate overflow)", got)
	}

	// Jan 31 2024 + 1 month = Feb 31 → Go normalises to Mar 2 (Feb has 29 days in leap year, so day 31 – 29 = day 2 of March).
	jan31Leap := time.Date(2024, 1, 31, 12, 0, 0, 0, time.UTC)
	got, ok = ParseRelative("1m", jan31Leap)
	if !ok {
		t.Fatal("ParseRelative(1m, leap) unexpectedly returned false")
	}
	if got != "2024-03-02" {
		t.Errorf("ParseRelative(1m, Jan 31 2024) = %q, want 2024-03-02 (stdlib AddDate overflow, leap)", got)
	}
}
