package telegram

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseInlineTags(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantCleaned string
		wantTags    []string
	}{
		{"empty", "", "", nil},
		{"no hashtags", "buy milk", "buy milk", nil},
		{"single hashtag end", "buy milk #shopping", "buy milk", []string{"shopping"}},
		{"single hashtag start", "#urgent fix login", "fix login", []string{"urgent"}},
		{"single hashtag middle", "fix #login bug", "fix bug", []string{"login"}},
		{"multiple hashtags", "fix login #work #urgent", "fix login", []string{"work", "urgent"}},
		{"duplicate hashtags deduped", "task #work #work #other", "task", []string{"work", "other"}},
		{"duplicate hashtags case insensitive", "task #Work #work #WORK", "task", []string{"work"}},
		{"hashtag only text", "#alone", "", []string{"alone"}},
		{"hashtags only multi", "#a #b #c", "", []string{"a", "b", "c"}},
		{"unicode in body survives", "купить молоко #shopping", "купить молоко", []string{"shopping"}},
		{"leading trailing whitespace", "   buy milk   ", "buy milk", nil},
		{"bare hash is not a tag", "issue # 42", "issue # 42", nil},
		{"hashtag with digits", "fix #bug123", "fix", []string{"bug123"}},
		{"hashtag with underscore", "task #my_tag", "task", []string{"my_tag"}},
		{"hashtag with dash", "task #my-tag", "task", []string{"my-tag"}},
		{"collapses whitespace gaps", "fix #a   #b   bug", "fix bug", []string{"a", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotCleaned, gotTags := ParseInlineTags(tc.in)
			if gotCleaned != tc.wantCleaned {
				t.Fatalf("ParseInlineTags(%q) cleaned=%q want %q", tc.in, gotCleaned, tc.wantCleaned)
			}
			if !reflect.DeepEqual(gotTags, tc.wantTags) {
				t.Fatalf("ParseInlineTags(%q) tags=%v want %v", tc.in, gotTags, tc.wantTags)
			}
		})
	}
}

func TestParseCapture(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantTitle string
		wantBody  string
		wantTags  []string
	}{
		{
			name:      "single line",
			in:        "buy milk",
			wantTitle: "buy milk",
			wantBody:  "",
			wantTags:  nil,
		},
		{
			name:      "single line with tags",
			in:        "buy milk #shopping #urgent",
			wantTitle: "buy milk",
			wantBody:  "",
			wantTags:  []string{"shopping", "urgent"},
		},
		{
			name:      "multi line",
			in:        "fix login bug\nthe form rejects valid emails",
			wantTitle: "fix login bug",
			wantBody:  "the form rejects valid emails",
			wantTags:  nil,
		},
		{
			name:      "multi line with title tags",
			in:        "fix login #work\nmore details here",
			wantTitle: "fix login",
			wantBody:  "more details here",
			wantTags:  []string{"work"},
		},
		{
			name:      "body hashtags survive untouched",
			in:        "do the thing\nremember to #note this in the body",
			wantTitle: "do the thing",
			wantBody:  "remember to #note this in the body",
			wantTags:  nil,
		},
		{
			name:      "leading newline empty title",
			in:        "\nonly body",
			wantTitle: "",
			wantBody:  "only body",
			wantTags:  nil,
		},
		{
			name:      "trailing newline empty body",
			in:        "title\n",
			wantTitle: "title",
			wantBody:  "",
			wantTags:  nil,
		},
		{
			name:      "body preserves leading whitespace",
			in:        "title\n  indented body",
			wantTitle: "title",
			wantBody:  "  indented body",
			wantTags:  nil,
		},
		{
			name: "multi paragraph body",
			in: "title #t1\nfirst paragraph\n\nsecond paragraph",
			wantTitle: "title",
			wantBody:  "first paragraph\n\nsecond paragraph",
			wantTags:  []string{"t1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotTitle, gotBody, gotTags := ParseCapture(tc.in)
			if gotTitle != tc.wantTitle {
				t.Fatalf("ParseCapture(%q) title=%q want %q", tc.in, gotTitle, tc.wantTitle)
			}
			if gotBody != tc.wantBody {
				t.Fatalf("ParseCapture(%q) body=%q want %q", tc.in, gotBody, tc.wantBody)
			}
			if !reflect.DeepEqual(gotTags, tc.wantTags) {
				t.Fatalf("ParseCapture(%q) tags=%v want %v", tc.in, gotTags, tc.wantTags)
			}
		})
	}
}

func TestParseCallback(t *testing.T) {
	const validULID = "01J5K7VC9RXMQ8NPZF2W3Y4ABC" // exactly 26 chars

	if len(validULID) != ulidLength {
		t.Fatalf("test fixture validULID is %d chars, want %d", len(validULID), ulidLength)
	}

	tests := []struct {
		name       string
		in         string
		wantAction string
		wantULID   string
		wantErr    bool
		errSubstr  string
	}{
		{
			name:       "done valid",
			in:         "done:" + validULID,
			wantAction: "done",
			wantULID:   validULID,
		},
		{
			name:       "active valid",
			in:         "active:" + validULID,
			wantAction: "active",
			wantULID:   validULID,
		},
		{
			name:       "view valid",
			in:         "view:" + validULID,
			wantAction: "view",
			wantULID:   validULID,
		},
		{
			name:       "collapse valid",
			in:         "collapse:" + validULID,
			wantAction: "collapse",
			wantULID:   validULID,
		},
		{
			name:      "no colon",
			in:        "doneXYZ",
			wantErr:   true,
			errSubstr: "missing colon",
		},
		{
			name:      "empty input",
			in:        "",
			wantErr:   true,
			errSubstr: "missing colon",
		},
		{
			name:      "unknown action",
			in:        "delete:" + validULID,
			wantErr:   true,
			errSubstr: "unknown action",
		},
		{
			name:      "empty action",
			in:        ":" + validULID,
			wantErr:   true,
			errSubstr: "unknown action",
		},
		{
			name:      "ulid too short",
			in:        "done:ABC",
			wantErr:   true,
			errSubstr: "invalid ulid length",
		},
		{
			name:      "ulid too long",
			in:        "done:" + validULID + "EXTRA",
			wantErr:   true,
			errSubstr: "invalid ulid length",
		},
		{
			name:      "ulid empty",
			in:        "done:",
			wantErr:   true,
			errSubstr: "invalid ulid length",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotAction, gotULID, err := ParseCallback(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCallback(%q) err=nil, want error containing %q", tc.in, tc.errSubstr)
				}
				if !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("ParseCallback(%q) err=%v, want substring %q", tc.in, err, tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCallback(%q) unexpected err=%v", tc.in, err)
			}
			if gotAction != tc.wantAction {
				t.Fatalf("ParseCallback(%q) action=%q want %q", tc.in, gotAction, tc.wantAction)
			}
			if gotULID != tc.wantULID {
				t.Fatalf("ParseCallback(%q) ulid=%q want %q", tc.in, gotULID, tc.wantULID)
			}
		})
	}
}
