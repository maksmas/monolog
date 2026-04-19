package config

import (
	"regexp"
	"testing"
)

// setDateFormatForTest swaps the package-level dateFormat for the duration
// of a test and returns a restore function. The caller is responsible for
// calling the restore function (typically via t.Cleanup). If layout is not
// already present in supported, the caller may pass a non-nil entry to
// register it for the duration of the test as well — pass nil to leave
// supported unchanged.
func setDateFormatForTest(layout string, entry *formatEntry) func() {
	prevFormat := dateFormat
	var hadPrev bool
	var prevEntry formatEntry
	if entry != nil {
		prevEntry, hadPrev = supported[layout]
		supported[layout] = *entry
	}
	dateFormat = layout
	return func() {
		dateFormat = prevFormat
		if entry != nil {
			if hadPrev {
				supported[layout] = prevEntry
			} else {
				delete(supported, layout)
			}
		}
	}
}

func TestDefaultAccessors(t *testing.T) {
	if got, want := DateFormat(), "02-01-2006"; got != want {
		t.Errorf("DateFormat() = %q, want %q", got, want)
	}
	if got, want := DateFormatLabel(), "DD-MM-YYYY"; got != want {
		t.Errorf("DateFormatLabel() = %q, want %q", got, want)
	}
	if got, want := DateRegex(), `\d{2}-\d{2}-\d{4}`; got != want {
		t.Errorf("DateRegex() = %q, want %q", got, want)
	}
}

func TestDateRegexCompiles(t *testing.T) {
	re, err := regexp.Compile(DateRegex())
	if err != nil {
		t.Fatalf("DateRegex() did not compile: %v", err)
	}
	if !re.MatchString("18-04-2026") {
		t.Errorf("DateRegex() did not match %q", "18-04-2026")
	}
	if re.MatchString("2026-04-18") {
		t.Errorf("DateRegex() unexpectedly matched ISO %q", "2026-04-18")
	}
}

func TestAccessorsRespectAlternativeLayout(t *testing.T) {
	restore := setDateFormatForTest("2006-01-02", &formatEntry{
		Label: "YYYY-MM-DD",
		Regex: `\d{4}-\d{2}-\d{2}`,
	})
	t.Cleanup(restore)

	if got, want := DateFormat(), "2006-01-02"; got != want {
		t.Errorf("DateFormat() = %q, want %q", got, want)
	}
	if got, want := DateFormatLabel(), "YYYY-MM-DD"; got != want {
		t.Errorf("DateFormatLabel() = %q, want %q", got, want)
	}
	if got, want := DateRegex(), `\d{4}-\d{2}-\d{2}`; got != want {
		t.Errorf("DateRegex() = %q, want %q", got, want)
	}
}

func TestAccessorsRestoredAfterTest(t *testing.T) {
	// Self-contained verification that setDateFormatForTest's returned
	// restore func actually restores the prior format and removes any
	// temporarily registered supported entry. Previously this test relied
	// on the implicit ordering of a sibling test's cleanup running first,
	// which would have silently become meaningless under t.Run reordering
	// or sharding.
	before := dateFormat
	_, hadPrev := supported["2006-01-02"]

	restore := setDateFormatForTest("2006-01-02", &formatEntry{
		Label: "YYYY-MM-DD",
		Regex: `\d{4}-\d{2}-\d{2}`,
	})
	if got := DateFormat(); got != "2006-01-02" {
		t.Fatalf("DateFormat() during override = %q, want %q", got, "2006-01-02")
	}

	restore()

	if got := DateFormat(); got != before {
		t.Errorf("DateFormat() after restore = %q, want %q", got, before)
	}
	if _, present := supported["2006-01-02"]; present != hadPrev {
		t.Errorf("restore did not roll back supported[%q] (present=%v, hadPrev=%v)",
			"2006-01-02", present, hadPrev)
	}
}

func TestUnknownLayoutPanics(t *testing.T) {
	restore := setDateFormatForTest("bogus-layout", nil)
	t.Cleanup(restore)

	assertPanics(t, "DateFormat", func() { _ = DateFormat() })
	assertPanics(t, "DateFormatLabel", func() { _ = DateFormatLabel() })
	assertPanics(t, "DateRegex", func() { _ = DateRegex() })
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("%s did not panic for unsupported layout", name)
		}
	}()
	fn()
}
