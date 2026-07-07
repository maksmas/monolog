package telegram

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/maksmas/monolog/internal/model"
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

const testDateFormat = "02-01-2006"

// testNow is a fixed reference time used by formatting tests so that
// schedule-bucket boundaries and relative-date renderings are
// deterministic regardless of the wall clock at test time.
//
//	2026-05-18 12:00 UTC → "today"
//	2026-05-19 → "tomorrow"
//	2026-05-25 → within "week" window
//	2026-06-25 → within "month" window
//	2027-01-01 → "someday"
func testNow() time.Time {
	return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
}

func TestHTMLEscape(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"hello", "hello"},
		{"a & b", "a &amp; b"},
		{"<script>", "&lt;script&gt;"},
		{"\"quoted\"", "&#34;quoted&#34;"},
		{"a < b > c & d", "a &lt; b &gt; c &amp; d"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := htmlEscape(tc.in); got != tc.want {
				t.Fatalf("htmlEscape(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPrefixID(t *testing.T) {
	if got := prefixID("01J5K7VC9RXMQ8NPZF2W3Y4ABC"); got != "01J5K" {
		t.Fatalf("prefixID full ULID = %q, want %q", got, "01J5K")
	}
	if got := prefixID("ABC"); got != "ABC" {
		t.Fatalf("prefixID short = %q, want %q", got, "ABC")
	}
	if got := prefixID(""); got != "" {
		t.Fatalf("prefixID empty = %q, want %q", got, "")
	}
	// exactly prefixLength chars → unchanged
	if got := prefixID("01J5K"); got != "01J5K" {
		t.Fatalf("prefixID exact = %q, want %q", got, "01J5K")
	}
}

func TestFormatTaskRow(t *testing.T) {
	const id = "01J5K7VC9RXMQ8NPZF2W3Y4ABC"
	tests := []struct {
		name string
		task model.Task
		want string
	}{
		{
			name: "title only no decorations",
			task: model.Task{ID: id, Title: "buy milk"},
			want: "<code>01J5K</code>  buy milk",
		},
		{
			name: "title with HTML metacharacters",
			task: model.Task{ID: id, Title: "<broken> & sad"},
			want: "<code>01J5K</code>  &lt;broken&gt; &amp; sad",
		},
		{
			name: "with tags only",
			task: model.Task{ID: id, Title: "fix login", Tags: []string{"work", "urgent"}},
			want: "<code>01J5K</code>  fix login\n<i>work, urgent</i>",
		},
		{
			name: "active tag filtered out but star marker shown",
			task: model.Task{ID: id, Title: "fix login", Tags: []string{"work", model.ActiveTag}},
			want: "⭐ <code>01J5K</code>  fix login\n<i>work</i>",
		},
		{
			name: "only active tag = star marker no <i> line",
			task: model.Task{ID: id, Title: "fix login", Tags: []string{model.ActiveTag}},
			want: "⭐ <code>01J5K</code>  fix login",
		},
		{
			name: "with recur marker only",
			task: model.Task{ID: id, Title: "weekly review", Recurrence: "weekly:mon"},
			want: "<code>01J5K</code>  weekly review\n<i>↻</i>",
		},
		{
			name: "with notes badge only",
			task: model.Task{ID: id, Title: "long task", NoteCount: 3},
			want: "<code>01J5K</code>  long task\n<i>[3]</i>",
		},
		{
			name: "tags + recur + notes combined",
			task: model.Task{ID: id, Title: "weekly", Tags: []string{"work"}, Recurrence: "weekly:mon", NoteCount: 2},
			want: "<code>01J5K</code>  weekly\n<i>work  ·  ↻ [2]</i>",
		},
		{
			name: "recur + notes no tags",
			task: model.Task{ID: id, Title: "weekly", Recurrence: "weekly:mon", NoteCount: 2},
			want: "<code>01J5K</code>  weekly\n<i>↻ [2]</i>",
		},
		{
			name: "tags + notes no recur",
			task: model.Task{ID: id, Title: "task", Tags: []string{"work"}, NoteCount: 1},
			want: "<code>01J5K</code>  task\n<i>work  ·  [1]</i>",
		},
		{
			name: "tags with HTML metachars escaped",
			task: model.Task{ID: id, Title: "task", Tags: []string{"a&b"}},
			want: "<code>01J5K</code>  task\n<i>a&amp;b</i>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatTaskRow(tc.task)
			if got != tc.want {
				t.Fatalf("FormatTaskRow:\n got:  %q\n want: %q", got, tc.want)
			}
		})
	}
}

// TestFormatTaskRowActiveToggleChangesRendering is the regression guard for
// the active-toggle EditMessage bug: a task with no other tags, no recur,
// and no notes used to render identically before and after toggling active
// because the active tag was filtered out of the visible row. Telegram
// rejects identical EditMessage payloads with "message is not modified",
// so the inline message would silently fail to refresh after the user tap.
//
// The fix prepends a visible ⭐ marker when t.IsActive() is true. This test
// locks in the contract: for the same task, the active-on and active-off
// renderings MUST differ — otherwise no future change to the row layout
// can silently re-introduce the bug.
func TestFormatTaskRowActiveToggleChangesRendering(t *testing.T) {
	const id = "01J5K7VC9RXMQ8NPZF2W3Y4ABC"
	// Plainest possible task: title only, no tags, no recur, no notes.
	// This is the common case that triggered the original bug.
	inactive := model.Task{ID: id, Title: "buy milk"}
	active := model.Task{ID: id, Title: "buy milk", Tags: []string{model.ActiveTag}}

	gotInactive := FormatTaskRow(inactive)
	gotActive := FormatTaskRow(active)
	if gotInactive == gotActive {
		t.Fatalf("active toggle produced identical rendering — Telegram would reject EditMessage as 'message is not modified'\n inactive: %q\n active:   %q",
			gotInactive, gotActive)
	}
}

func TestFormatDetailView(t *testing.T) {
	const id = "01J5K7VC9RXMQ8NPZF2W3Y4ABC"
	now := testNow()
	createdAt := now.Add(-3 * 24 * time.Hour).Format(time.RFC3339) // "3d" ago

	t.Run("minimal task no decorations", func(t *testing.T) {
		task := model.Task{
			ID:        id,
			Title:     "buy milk",
			Schedule:  "2026-05-18", // == today
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		// schedule line: today
		if !strings.Contains(got, "Schedule: 18-05-2026 (today)") {
			t.Fatalf("expected Schedule line with bucket; got:\n%s", got)
		}
		if !strings.Contains(got, "Created: 3d") {
			t.Fatalf("expected Created: 3d; got:\n%s", got)
		}
		if strings.Contains(got, "Tags:") {
			t.Fatalf("expected no Tags line; got:\n%s", got)
		}
		if strings.Contains(got, "Recur:") {
			t.Fatalf("expected no Recur line; got:\n%s", got)
		}
		if strings.Contains(got, "Notes:") {
			t.Fatalf("expected no Notes line; got:\n%s", got)
		}
		if !strings.HasPrefix(got, "<code>01J5K</code>  buy milk\n") {
			t.Fatalf("expected ULID+title header; got:\n%s", got)
		}
	})

	t.Run("escapes HTML metacharacters", func(t *testing.T) {
		task := model.Task{
			ID:        id,
			Title:     "<title> & stuff",
			Body:      "body with <html> & chars",
			Schedule:  "2026-05-18",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if !strings.Contains(got, "&lt;title&gt; &amp; stuff") {
			t.Fatalf("title not escaped; got:\n%s", got)
		}
		if !strings.Contains(got, "body with &lt;html&gt; &amp; chars") {
			t.Fatalf("body not escaped; got:\n%s", got)
		}
	})

	t.Run("with tags excludes active", func(t *testing.T) {
		task := model.Task{
			ID:        id,
			Title:     "task",
			Schedule:  "2026-05-18",
			Tags:      []string{"work", model.ActiveTag, "urgent"},
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if !strings.Contains(got, "Tags: work, urgent") {
			t.Fatalf("expected Tags: work, urgent; got:\n%s", got)
		}
	})

	t.Run("with recurrence", func(t *testing.T) {
		task := model.Task{
			ID:         id,
			Title:      "weekly review",
			Schedule:   "2026-05-25",
			Recurrence: "weekly:mon",
			CreatedAt:  createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if !strings.Contains(got, "Recur: weekly:mon") {
			t.Fatalf("expected Recur line; got:\n%s", got)
		}
	})

	t.Run("with note count", func(t *testing.T) {
		task := model.Task{
			ID:        id,
			Title:     "task",
			Schedule:  "2026-05-18",
			NoteCount: 5,
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if !strings.Contains(got, "Notes: 5") {
			t.Fatalf("expected Notes: 5; got:\n%s", got)
		}
	})

	t.Run("body preserved with newlines", func(t *testing.T) {
		task := model.Task{
			ID:        id,
			Title:     "task",
			Body:      "line one\nline two\n\nline four",
			Schedule:  "2026-05-18",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if !strings.Contains(got, "line one\nline two\n\nline four") {
			t.Fatalf("body newlines not preserved; got:\n%s", got)
		}
	})

	t.Run("body shorter than cap stays whole", func(t *testing.T) {
		task := model.Task{
			ID:        id,
			Title:     "task",
			Body:      "short body",
			Schedule:  "2026-05-18",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if strings.Contains(got, truncationMarker) {
			t.Fatalf("short body should not be truncated; got:\n%s", got)
		}
		if !strings.HasSuffix(got, "short body") {
			t.Fatalf("body missing from output; got:\n%s", got)
		}
	})

	t.Run("body longer than cap truncated with marker", func(t *testing.T) {
		// generate a body that, when escaped, comfortably exceeds the
		// telegramMaxMessage budget after the header.
		body := strings.Repeat("a", telegramMaxMessage+200)
		task := model.Task{
			ID:        id,
			Title:     "task",
			Body:      body,
			Schedule:  "2026-05-18",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if len(got) > telegramMaxMessage {
			t.Fatalf("output exceeds Telegram cap: len=%d", len(got))
		}
		if !strings.HasSuffix(got, truncationMarker) {
			t.Fatalf("expected truncation marker at end; got tail %q", got[len(got)-50:])
		}
	})

	t.Run("truncation never splits HTML entity", func(t *testing.T) {
		// Body is a long run of `&` (escaped to `&amp;`). A naive byte
		// truncation at byte budget would often land *inside* a `&amp;`
		// entity (e.g. `&am` at the cut), which Telegram rejects with
		// "Bad Request: can't parse entities". Verify the output ends
		// cleanly with the marker, and contains no partial `&...` entity
		// before the marker.
		body := strings.Repeat("&", telegramMaxMessage+200)
		task := model.Task{
			ID:        id,
			Title:     "task",
			Body:      body,
			Schedule:  "2026-05-18",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if len(got) > telegramMaxMessage {
			t.Fatalf("output exceeds cap: len=%d", len(got))
		}
		if !strings.HasSuffix(got, truncationMarker) {
			t.Fatalf("expected truncation marker; got tail %q", got[len(got)-50:])
		}
		// Walk backward from just before the marker: the last `&` we
		// see must be followed by `amp;` (a complete entity), otherwise
		// we landed inside one.
		beforeMarker := strings.TrimSuffix(got, truncationMarker)
		if i := strings.LastIndexByte(beforeMarker, '&'); i >= 0 {
			tail := beforeMarker[i:]
			if !strings.HasPrefix(tail, "&amp;") {
				t.Fatalf("truncation landed inside HTML entity; tail = %q", tail)
			}
		}
	})

	t.Run("truncation respects multi-byte UTF-8 rune boundary", func(t *testing.T) {
		// Russian text averages 2 bytes/rune (Cyrillic). A naive byte cut
		// inside a multi-byte rune produces invalid UTF-8 — Telegram
		// accepts the bytes but the rendered chat shows replacement chars.
		// Verify the output is valid UTF-8 after truncation.
		body := strings.Repeat("Ы", telegramMaxMessage/2+100) // Ы is 2 bytes
		task := model.Task{
			ID:        id,
			Title:     "task",
			Body:      body,
			Schedule:  "2026-05-18",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if len(got) > telegramMaxMessage {
			t.Fatalf("output exceeds cap: len=%d", len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("truncated output is not valid UTF-8")
		}
		if !strings.HasSuffix(got, truncationMarker) {
			t.Fatalf("expected truncation marker; got tail %q", got[len(got)-50:])
		}
	})

	t.Run("truncation with mixed entities and runes is valid", func(t *testing.T) {
		// Repeating chunk produces both `&amp;` entities (from `&`) and
		// multi-byte Cyrillic runes — exactly the worst case for a naive
		// byte truncation.
		chunk := "Ы&Ы&"
		body := strings.Repeat(chunk, telegramMaxMessage/len(chunk)+50)
		task := model.Task{
			ID:        id,
			Title:     "task",
			Body:      body,
			Schedule:  "2026-05-18",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if len(got) > telegramMaxMessage {
			t.Fatalf("output exceeds cap: len=%d", len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("truncated output is not valid UTF-8")
		}
		if !strings.HasSuffix(got, truncationMarker) {
			t.Fatalf("expected truncation marker")
		}
	})

	t.Run("header alone exceeds cap is safely truncated", func(t *testing.T) {
		// Pathological case: title alone bigger than the Telegram 4096-
		// byte cap. The fallback used to be a raw byte slice that could
		// land mid-rune or mid-`&amp;` entity. safeTruncate must produce
		// valid UTF-8 and not split an entity.
		title := strings.Repeat("Ы", 3000) // ~6000 bytes of Cyrillic
		task := model.Task{
			ID:        id,
			Title:     title,
			Schedule:  "2026-05-18",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if len(got) > telegramMaxMessage {
			t.Fatalf("oversized-header output exceeds cap: len=%d", len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("oversized-header output is not valid UTF-8")
		}
	})

	t.Run("header with HTML entities exceeds cap respects entity boundaries", func(t *testing.T) {
		// Title is a long run of `&` which expands to `&amp;` after
		// htmlEscape. A naive byte cut would land inside `&am...`.
		// safeTruncate must back up to before the partial entity.
		title := strings.Repeat("&", 5000) // 5000 → 25000 bytes after escape
		task := model.Task{
			ID:        id,
			Title:     title,
			Schedule:  "2026-05-18",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if len(got) > telegramMaxMessage {
			t.Fatalf("output exceeds cap: len=%d", len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("output not valid UTF-8")
		}
		// The last `&` we see must start a complete `&amp;` — anything
		// else means the cut landed inside an entity.
		if i := strings.LastIndexByte(got, '&'); i >= 0 {
			tail := got[i:]
			if !strings.HasPrefix(tail, "&amp;") {
				t.Fatalf("header truncation split HTML entity; tail = %q", tail)
			}
		}
	})

	t.Run("schedule today no bucket parentheses when stored is bucket name", func(t *testing.T) {
		// legacy task that still has the literal "someday" bucket name as
		// its schedule value. The bucket and stored value are equal so
		// no parenthetical is appended.
		task := model.Task{
			ID:        id,
			Title:     "later",
			Schedule:  "someday",
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if !strings.Contains(got, "Schedule: someday\n") {
			t.Fatalf("expected legacy bucket without parens; got:\n%s", got)
		}
	})

	t.Run("schedule week bucket parens", func(t *testing.T) {
		task := model.Task{
			ID:        id,
			Title:     "weekly thing",
			Schedule:  "2026-05-25", // 7 days from now → week
			CreatedAt: createdAt,
		}
		got := formatDetailViewAt(task, testDateFormat, now)
		if !strings.Contains(got, "Schedule: 25-05-2026 (week)") {
			t.Fatalf("expected (week) bucket label; got:\n%s", got)
		}
	})
}

func TestBuildSummaryKeyboard(t *testing.T) {
	const id = "01J5K7VC9RXMQ8NPZF2W3Y4ABC"
	kb := BuildSummaryKeyboard(id)
	if len(kb) != 1 {
		t.Fatalf("expected 1 row, got %d", len(kb))
	}
	row := kb[0]
	if len(row) != 3 {
		t.Fatalf("expected 3 buttons, got %d", len(row))
	}
	wantData := []string{"done:" + id, "active:" + id, "view:" + id}
	wantText := []string{"✅ Done", "⭐ Active", "📄 Details"}
	for i, b := range row {
		if b.CallbackData != wantData[i] {
			t.Errorf("button %d data=%q want %q", i, b.CallbackData, wantData[i])
		}
		if b.Text != wantText[i] {
			t.Errorf("button %d text=%q want %q", i, b.Text, wantText[i])
		}
		// ParseCallback should round-trip the data we generate.
		action, gotID, err := ParseCallback(b.CallbackData)
		if err != nil {
			t.Errorf("ParseCallback(%q) err=%v", b.CallbackData, err)
		}
		if gotID != id {
			t.Errorf("ParseCallback(%q) ulid=%q want %q", b.CallbackData, gotID, id)
		}
		if action == "" {
			t.Errorf("ParseCallback(%q) action empty", b.CallbackData)
		}
	}
}

func TestBuildDetailKeyboard(t *testing.T) {
	const id = "01J5K7VC9RXMQ8NPZF2W3Y4ABC"
	kb := BuildDetailKeyboard(id)
	if len(kb) != 1 {
		t.Fatalf("expected 1 row, got %d", len(kb))
	}
	row := kb[0]
	if len(row) != 3 {
		t.Fatalf("expected 3 buttons, got %d", len(row))
	}
	wantData := []string{"collapse:" + id, "done:" + id, "active:" + id}
	wantText := []string{"⬆ Collapse", "✅ Done", "⭐ Active"}
	for i, b := range row {
		if b.CallbackData != wantData[i] {
			t.Errorf("button %d data=%q want %q", i, b.CallbackData, wantData[i])
		}
		if b.Text != wantText[i] {
			t.Errorf("button %d text=%q want %q", i, b.Text, wantText[i])
		}
	}
}

func TestFormatDoneRow(t *testing.T) {
	const id = "01J5K7VC9RXMQ8NPZF2W3Y4ABC"
	t.Run("non-recurring", func(t *testing.T) {
		got := FormatDoneRow(model.Task{ID: id, Title: "buy milk"}, "")
		want := "✅ <s><code>01J5K</code>  buy milk</s>"
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("recurring with next date", func(t *testing.T) {
		got := FormatDoneRow(model.Task{ID: id, Title: "weekly review"}, "25-04-2026")
		want := "✅ <s><code>01J5K</code>  weekly review</s>\n↻ next: 25-04-2026"
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("escapes HTML in title", func(t *testing.T) {
		got := FormatDoneRow(model.Task{ID: id, Title: "<x> & y"}, "")
		want := "✅ <s><code>01J5K</code>  &lt;x&gt; &amp; y</s>"
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
}

func TestFormatEmptyBucket(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{"Today", "<b>Today</b> — nothing 🎉"},
		{"Week", "<b>Week</b> — nothing 🎉"},
		{"<x>", "<b>&lt;x&gt;</b> — nothing 🎉"},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			if got := FormatEmptyBucket(tc.label); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFormatScheduleLine(t *testing.T) {
	now := testNow()
	tests := []struct {
		name   string
		stored string
		want   string
	}{
		{"today", "2026-05-18", "18-05-2026 (today)"},
		{"tomorrow", "2026-05-19", "19-05-2026 (tomorrow)"},
		{"week", "2026-05-25", "25-05-2026 (week)"},
		{"month", "2026-06-15", "15-06-2026 (month)"},
		{"someday", "2027-01-01", "01-01-2027 (someday)"},
		{"legacy bucket name", "someday", "someday"},
		{"past date = today bucket", "2026-05-10", "10-05-2026 (today)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatScheduleLine(tc.stored, now, testDateFormat)
			if got != tc.want {
				t.Fatalf("formatScheduleLine(%q): got %q want %q", tc.stored, got, tc.want)
			}
		})
	}
}
