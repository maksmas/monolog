package schedule

import (
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
		Someday:  "2027-04-13",
	}
	for in, want := range cases {
		got, err := Parse(in, fixedNow)
		if err != nil {
			t.Fatalf("Parse(%q): unexpected error %v", in, err)
		}
		if got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParse_ISOPassThrough(t *testing.T) {
	got, err := Parse("2026-05-01", fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2026-05-01" {
		t.Errorf("Parse(ISO) = %q, want 2026-05-01", got)
	}
}

func TestParse_Invalid(t *testing.T) {
	for _, s := range []string{"garbage", "yesterday", "2026-13-01", ""} {
		if _, err := Parse(s, fixedNow); err == nil {
			t.Errorf("Parse(%q): expected error, got nil", s)
		}
	}
}

func TestBucket_FromISODate(t *testing.T) {
	cases := []struct {
		schedule string
		want     string
	}{
		{"2020-01-01", Today},   // far past
		{"2026-04-12", Today},   // yesterday
		{"2026-04-13", Today},   // today
		{"2026-04-14", Tomorrow},
		{"2026-04-15", Week},    // today+2
		{"2026-04-20", Week},    // today+7 boundary
		{"2026-04-21", Someday}, // today+8
		{"2027-04-13", Someday},
	}
	for _, tc := range cases {
		if got := Bucket(tc.schedule, fixedNow); got != tc.want {
			t.Errorf("Bucket(%q) = %q, want %q", tc.schedule, got, tc.want)
		}
	}
}

func TestBucket_LegacyStrings(t *testing.T) {
	for _, b := range []string{Today, Tomorrow, Week, Someday} {
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
