package email

import (
	"strings"
	"testing"
	"time"
)

func TestCleanSubject(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Hello world", "Hello world"},
		{"single Re", "Re: Hello", "Hello"},
		{"single Fwd", "Fwd: Hello", "Hello"},
		{"single Fw", "Fw: Hello", "Hello"},
		{"single FW upper", "FW: Hello", "Hello"},
		{"chained", "Re: RE: Fwd: foo", "foo"},
		{"chained mixed case", "rE: fW: FWD: bar", "bar"},
		{"chained with extra spaces", "Re:  Fwd:   baz", "baz"},
		{"empty", "", "(no subject)"},
		{"whitespace only", "   \t  ", "(no subject)"},
		{"prefix only", "Re: ", "(no subject)"},
		{"chained prefix only", "Re: Fwd: ", "(no subject)"},
		{"trims surrounding ws", "  hello  ", "hello"},
		{"prefix-like-but-not", "Reminder: pay bills", "Reminder: pay bills"},
		// Note: subjectPrefixPattern is anchored at the start, so a "Re:"
		// in the middle of a subject is preserved verbatim.
		{"middle Re left intact", "Hello Re: world", "Hello Re: world"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanSubject(tc.in)
			if got != tc.want {
				t.Fatalf("cleanSubject(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseSender(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"name and addr", `Alice <alice@example.com>`, "Alice"},
		{"quoted name", `"Bob B." <bob@example.com>`, "Bob B."},
		{"bare addr", `<alice@example.com>`, "alice@example.com"},
		{"bare addr no angle", `alice@example.com`, "alice@example.com"},
		{"empty", ``, "unknown"},
		{"whitespace only", `   `, "unknown"},
		{"malformed", `not-an-email`, "unknown"},
		{"unclosed angle", `Alice <alice@example.com`, "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSender(tc.in)
			if got != tc.want {
				t.Fatalf("parseSender(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTruncateSnippet(t *testing.T) {
	long := strings.Repeat("a", 250)
	want201 := strings.Repeat("a", 200) + "…"

	exact := strings.Repeat("b", 200)
	short := "hello world"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   \n\t ", ""},
		{"short passes through", short, short},
		{"exactly 200 no suffix", exact, exact},
		{"over 200 truncated with ellipsis", long, want201},
		{"html entity decoded", "Tom &amp; Jerry", "Tom & Jerry"},
		{"html numeric entity", "it&#39;s here", "it's here"},
		{"trims surrounding ws", "  hi  ", "hi"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateSnippet(tc.in)
			if got != tc.want {
				t.Fatalf("truncateSnippet(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTruncateSnippetCountsRunesNotBytes(t *testing.T) {
	// Each emoji is 4 bytes but 1 rune. 199 emoji + extra letter must
	// pass through (200 runes ≤ 200), but 201 emoji must truncate.
	emoji := "🙂"
	if len([]rune(emoji)) != 1 {
		t.Fatalf("test fixture: expected 1 rune emoji")
	}
	at200 := strings.Repeat(emoji, 200)
	at201 := strings.Repeat(emoji, 201)

	if got := truncateSnippet(at200); got != at200 {
		t.Fatalf("at200: expected pass-through, got truncated len=%d", len([]rune(got)))
	}
	got := truncateSnippet(at201)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("at201: expected ellipsis suffix, got %q", got)
	}
	if len([]rune(got)) != 201 {
		// 200 emoji + 1 ellipsis = 201 runes.
		t.Fatalf("at201: expected 201 runes, got %d", len([]rune(got)))
	}
}

func TestToTaskBasic(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	msg := &Message{
		ID:      "msg-001",
		Subject: "Re: Hello",
		From:    "Alice <alice@example.com>",
		Snippet: "Some short snippet",
	}
	got, err := ToTask(msg, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Title != "Hello" {
		t.Fatalf("Title=%q want Hello", got.Title)
	}
	if got.Source != "gmail" {
		t.Fatalf("Source=%q want gmail", got.Source)
	}
	if got.SourceID != "msg-001" {
		t.Fatalf("SourceID=%q want msg-001", got.SourceID)
	}
	if got.Status != "open" {
		t.Fatalf("Status=%q want open", got.Status)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "email" {
		t.Fatalf("Tags=%v want [email]", got.Tags)
	}
	// Schedule today should be the ISO date for `now`.
	if got.Schedule != "2026-04-28" {
		t.Fatalf("Schedule=%q want 2026-04-28", got.Schedule)
	}
	// Body lines: From, URL, blank, snippet.
	wantBody := "From: Alice\nhttps://mail.google.com/mail/#all/msg-001\n\nSome short snippet"
	if got.Body != wantBody {
		t.Fatalf("Body=%q want %q", got.Body, wantBody)
	}
	// URL must omit /u/0/ so it works regardless of which Google account
	// is signed in.
	if strings.Contains(got.Body, "/u/0/") {
		t.Fatalf("Body should not contain /u/0/: %q", got.Body)
	}
	// CreatedAt and UpdatedAt are RFC3339 of `now`.
	wantTS := now.UTC().Format(time.RFC3339)
	if got.CreatedAt != wantTS || got.UpdatedAt != wantTS {
		t.Fatalf("CreatedAt=%q UpdatedAt=%q want %q", got.CreatedAt, got.UpdatedAt, wantTS)
	}
	// ID is non-empty (a fresh ULID).
	if got.ID == "" {
		t.Fatalf("ID is empty")
	}
}

func TestToTaskFreshULIDPerCall(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	msg := &Message{ID: "msg-x", Subject: "test", From: "a@example.com", Snippet: ""}
	a, err := ToTask(msg, now)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := ToTask(msg, now)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a.ID == b.ID {
		t.Fatalf("expected different ULIDs across calls; got %q twice", a.ID)
	}
}

func TestToTaskEmptySnippet(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	msg := &Message{
		ID:      "id-2",
		Subject: "topic",
		From:    "Bob <bob@example.com>",
		Snippet: "",
	}
	got, err := ToTask(msg, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// With an empty snippet there is no body text after the URL — only
	// "From:" + URL with NO trailing newlines.
	wantBody := "From: Bob\nhttps://mail.google.com/mail/#all/id-2"
	if got.Body != wantBody {
		t.Fatalf("Body=%q want %q", got.Body, wantBody)
	}
	if strings.HasSuffix(got.Body, "\n") {
		t.Fatalf("Body has trailing newline: %q", got.Body)
	}
}

func TestToTaskEmptySubject(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	got, err := ToTask(&Message{ID: "i", Subject: "", From: "a@example.com", Snippet: ""}, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Title != "(no subject)" {
		t.Fatalf("Title=%q want (no subject)", got.Title)
	}
}

func TestToTaskMalformedSender(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	got, err := ToTask(&Message{ID: "i", Subject: "x", From: "garbage", Snippet: "s"}, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.HasPrefix(got.Body, "From: unknown\n") {
		t.Fatalf("Body should start with 'From: unknown', got %q", got.Body)
	}
}

func TestToTaskTruncatesLongSnippet(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 250)
	got, err := ToTask(&Message{ID: "i", Subject: "x", From: "a@example.com", Snippet: long}, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.HasSuffix(got.Body, "…") {
		t.Fatalf("Body should end with ellipsis when snippet truncated: %q", got.Body[len(got.Body)-50:])
	}
	// Body = "From: a@example.com\nhttps://...\n\n" + 200 'x' + "…"
	parts := strings.SplitN(got.Body, "\n\n", 2)
	if len(parts) != 2 {
		t.Fatalf("Body missing snippet section: %q", got.Body)
	}
	snipPart := parts[1]
	wantSnipRunes := 201 // 200 x + 1 ellipsis
	if got := len([]rune(snipPart)); got != wantSnipRunes {
		t.Fatalf("snippet runes=%d want %d", got, wantSnipRunes)
	}
}

func TestToTaskShortSnippetNoEllipsis(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	got, err := ToTask(&Message{ID: "i", Subject: "x", From: "a@example.com", Snippet: "short"}, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strings.Contains(got.Body, "…") {
		t.Fatalf("Body should not contain ellipsis for short snippet: %q", got.Body)
	}
}

func TestToTaskHTMLEntities(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	got, err := ToTask(&Message{
		ID: "i", Subject: "x", From: "a@example.com",
		Snippet: "Tom &amp; Jerry &#39;s",
	}, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(got.Body, "Tom & Jerry 's") {
		t.Fatalf("Body should contain decoded entities, got %q", got.Body)
	}
}

func TestToTaskNilMessage(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	if _, err := ToTask(nil, now); err == nil {
		t.Fatalf("expected error for nil msg")
	}
}

func TestToTaskScheduleIsToday(t *testing.T) {
	// Run with a few different `now` values to confirm Schedule tracks
	// the injected time, not the wall clock.
	cases := []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026-01-01"},
		{time.Date(2030, 12, 31, 23, 59, 59, 0, time.UTC), "2030-12-31"},
	}
	for _, c := range cases {
		got, err := ToTask(&Message{ID: "i", Subject: "x", From: "a@example.com"}, c.now)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got.Schedule != c.want {
			t.Fatalf("Schedule=%q want %q", got.Schedule, c.want)
		}
	}
}
