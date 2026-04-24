package slack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// loadFixture reads a JSON fixture from testdata/ and returns its contents.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return b
}

// mockHandler serves Slack API responses from an in-memory map of
// method (e.g. "auth.test") -> response body. It also counts hits per method
// so tests can verify caching.
type mockHandler struct {
	mu        sync.Mutex
	bodies    map[string][]byte
	hits      map[string]int
	requests  map[string][]string // form bodies seen per method
	sequences map[string][][]byte // per-method queue of responses (consumed in order)
	status    int
	headers   http.Header
}

func newMockHandler() *mockHandler {
	return &mockHandler{
		bodies:    map[string][]byte{},
		hits:      map[string]int{},
		requests:  map[string][]string{},
		sequences: map[string][][]byte{},
		status:    http.StatusOK,
		headers:   http.Header{},
	}
}

func (m *mockHandler) setBody(method string, body []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bodies[method] = body
}

// queueSequence stores an ordered list of bodies for a method; each call
// consumes one entry. Once exhausted, falls back to bodies[method].
func (m *mockHandler) queueSequence(method string, bodies ...[]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sequences[method] = append(m.sequences[method], bodies...)
}

// methodAndForm pulls the method name out of the URL and the form body.
func (m *mockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/")
	body, _ := io.ReadAll(r.Body)

	m.mu.Lock()
	m.hits[method]++
	m.requests[method] = append(m.requests[method], string(body))

	var resp []byte
	if seq, ok := m.sequences[method]; ok && len(seq) > 0 {
		resp = seq[0]
		m.sequences[method] = seq[1:]
	} else {
		resp = m.bodies[method]
	}
	status := m.status
	extraHeaders := m.headers.Clone()
	m.mu.Unlock()

	for k, vs := range extraHeaders {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if len(resp) > 0 {
		_, _ = w.Write(resp)
	}
}

func (m *mockHandler) hitCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hits[method]
}

func (m *mockHandler) formFor(method string, idx int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if reqs := m.requests[method]; idx < len(reqs) {
		return reqs[idx]
	}
	return ""
}

// ctxWithTimeout gives tests a bounded context so a hang in the client
// manifests as a test timeout rather than a global harness hang.
func ctxWithTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestAuthTest_ExtractsWorkspaceSubdomain(t *testing.T) {
	h := newMockHandler()
	h.setBody("auth.test", loadFixture(t, "auth_test_ok.json"))
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	ws, err := c.AuthTest(ctxWithTimeout(t))
	if err != nil {
		t.Fatalf("AuthTest: %v", err)
	}
	if ws != "myteam" {
		t.Fatalf("workspace = %q, want myteam", ws)
	}
}

func TestAuthTest_InvalidAuthReturnsMissingScope(t *testing.T) {
	h := newMockHandler()
	h.setBody("auth.test", loadFixture(t, "auth_test_invalid.json"))
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-bad", BaseURL: server.URL}
	_, err := c.AuthTest(ctxWithTimeout(t))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrMissingScope) {
		t.Fatalf("err = %v, want errors.Is(ErrMissingScope)", err)
	}
}

func TestAuthTest_EmptyTokenRejected(t *testing.T) {
	c := &Client{Token: ""}
	_, err := c.AuthTest(ctxWithTimeout(t))
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestListSaved_Paginates(t *testing.T) {
	h := newMockHandler()
	// stars.list returns page1 then page2 in sequence.
	h.queueSequence("stars.list",
		loadFixture(t, "stars_list_page1.json"),
		loadFixture(t, "stars_list_page2.json"),
	)
	h.setBody("conversations.info", loadFixture(t, "conversations_info_c0123.json"))
	h.setBody("users.info", loadFixture(t, "users_info_u0456.json"))
	// dynamic per-call for conversations.info / users.info:
	h.queueSequence("conversations.info",
		loadFixture(t, "conversations_info_c0123.json"),
		loadFixture(t, "conversations_info_c0999.json"),
	)
	h.queueSequence("users.info",
		loadFixture(t, "users_info_u0456.json"),
		loadFixture(t, "users_info_u0789.json"),
	)
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	items, err := c.ListSaved(ctxWithTimeout(t))
	if err != nil {
		t.Fatalf("ListSaved: %v", err)
	}
	// page1 had 3 raw items: 2 messages + 1 file (filtered). page2 adds 1
	// message. Total: 3 messages.
	if got := len(items); got != 3 {
		t.Fatalf("got %d items, want 3", got)
	}
	if h.hitCount("stars.list") != 2 {
		t.Fatalf("stars.list hit %d times, want 2", h.hitCount("stars.list"))
	}
}

func TestListSaved_FiltersNonMessageTypes(t *testing.T) {
	// Single-page fixture: 2 message items + 1 file item that must be filtered.
	singlePage := []byte(`{
  "ok": true,
  "items": [
    {"type":"message","channel":"C0123ABC","message":{"ts":"1.1","text":"a","user":"U0456"}},
    {"type":"file","file":{"id":"F1","name":"foo.png"}},
    {"type":"message","channel":"C0123ABC","message":{"ts":"2.2","text":"b","user":"U0789"}}
  ],
  "response_metadata":{"next_cursor":""}
}`)
	h := newMockHandler()
	h.setBody("stars.list", singlePage)
	h.setBody("conversations.info", loadFixture(t, "conversations_info_c0123.json"))
	h.queueSequence("users.info",
		loadFixture(t, "users_info_u0456.json"),
		loadFixture(t, "users_info_u0789.json"),
	)
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	items, err := c.ListSaved(ctxWithTimeout(t))
	if err != nil {
		t.Fatalf("ListSaved: %v", err)
	}
	// 2 message items survive; the file is filtered.
	if got := len(items); got != 2 {
		t.Fatalf("got %d items, want 2 (file type filtered)", got)
	}
	for _, it := range items {
		if it.Channel == "" {
			t.Fatalf("message item has empty Channel: %+v", it)
		}
		if it.TS == "" {
			t.Fatalf("message item has empty TS: %+v", it)
		}
	}
}

func TestListSaved_CachesResolveCalls(t *testing.T) {
	// Build an in-repeated stars.list page where the same channel+user
	// appears 3 times. Expect 1 conversations.info and 1 users.info call.
	h := newMockHandler()
	repeated := []byte(`{
  "ok": true,
  "items": [
    {"type":"message","channel":"C0123ABC","message":{"ts":"1.1","text":"a","user":"U0456"}},
    {"type":"message","channel":"C0123ABC","message":{"ts":"2.2","text":"b","user":"U0456"}},
    {"type":"message","channel":"C0123ABC","message":{"ts":"3.3","text":"c","user":"U0456"}}
  ],
  "response_metadata":{"next_cursor":""}
}`)
	h.setBody("stars.list", repeated)
	h.setBody("conversations.info", loadFixture(t, "conversations_info_c0123.json"))
	h.setBody("users.info", loadFixture(t, "users_info_u0456.json"))
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	items, err := c.ListSaved(ctxWithTimeout(t))
	if err != nil {
		t.Fatalf("ListSaved: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if got := h.hitCount("conversations.info"); got != 1 {
		t.Fatalf("conversations.info hit %d times, want 1 (cached)", got)
	}
	if got := h.hitCount("users.info"); got != 1 {
		t.Fatalf("users.info hit %d times, want 1 (cached)", got)
	}
	for _, it := range items {
		if it.ChannelName != "general" {
			t.Fatalf("channel name = %q, want general", it.ChannelName)
		}
		if it.AuthorName != "alice" {
			t.Fatalf("author name = %q, want alice", it.AuthorName)
		}
	}
}

func TestListSaved_ConstructsPermalinkWhenMissing(t *testing.T) {
	h := newMockHandler()
	h.setBody("stars.list", loadFixture(t, "stars_list_no_permalink.json"))
	h.setBody("conversations.info", loadFixture(t, "conversations_info_c0123.json"))
	h.setBody("users.info", loadFixture(t, "users_info_u0456.json"))
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL, Workspace: "myteam"}
	items, err := c.ListSaved(ctxWithTimeout(t))
	if err != nil {
		t.Fatalf("ListSaved: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	want := "https://myteam.slack.com/archives/C0123ABC/p1712400000000500"
	if items[0].Permalink != want {
		t.Fatalf("permalink = %q, want %q", items[0].Permalink, want)
	}
}

func TestListSaved_MidPaginationErrorReturnsPartial(t *testing.T) {
	h := newMockHandler()
	// First page succeeds with a cursor, second page returns an error.
	h.queueSequence("stars.list",
		loadFixture(t, "stars_list_page1.json"),
		loadFixture(t, "error_unknown.json"),
	)
	h.setBody("conversations.info", loadFixture(t, "conversations_info_c0123.json"))
	h.queueSequence("users.info",
		loadFixture(t, "users_info_u0456.json"),
		loadFixture(t, "users_info_u0789.json"),
	)
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	items, err := c.ListSaved(ctxWithTimeout(t))
	if err == nil {
		t.Fatal("expected error from second page")
	}
	// Partial items (2 message items from page 1) are still returned.
	if got := len(items); got != 2 {
		t.Fatalf("got %d partial items, want 2", got)
	}
}

func TestListSaved_MissingChannelFallsBackToID(t *testing.T) {
	h := newMockHandler()
	h.setBody("stars.list", loadFixture(t, "stars_list_page2.json"))
	h.setBody("conversations.info", loadFixture(t, "conversations_info_not_found.json"))
	h.setBody("users.info", loadFixture(t, "users_info_u0456.json"))
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	items, err := c.ListSaved(ctxWithTimeout(t))
	if err != nil {
		t.Fatalf("ListSaved: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].ChannelName != "C0999XYZ" {
		t.Fatalf("channel name = %q, want fallback C0999XYZ", items[0].ChannelName)
	}
}

func TestListSaved_MissingUserFallsBackToID(t *testing.T) {
	h := newMockHandler()
	h.setBody("stars.list", loadFixture(t, "stars_list_page2.json"))
	h.setBody("conversations.info", loadFixture(t, "conversations_info_c0999.json"))
	h.setBody("users.info", loadFixture(t, "users_info_not_found.json"))
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	items, err := c.ListSaved(ctxWithTimeout(t))
	if err != nil {
		t.Fatalf("ListSaved: %v", err)
	}
	if items[0].AuthorName != "U0456" {
		t.Fatalf("author name = %q, want fallback U0456", items[0].AuthorName)
	}
}

func TestUnsave_SendsCorrectForm(t *testing.T) {
	h := newMockHandler()
	h.setBody("stars.remove", loadFixture(t, "stars_remove_ok.json"))
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	err := c.Unsave(ctxWithTimeout(t), "C0123ABC", "1712345678.000100")
	if err != nil {
		t.Fatalf("Unsave: %v", err)
	}
	form := h.formFor("stars.remove", 0)
	if !strings.Contains(form, "channel=C0123ABC") {
		t.Fatalf("form body missing channel: %q", form)
	}
	// Match the `&` delimiter so this assertion is not satisfied by the
	// old, buggy `channel_timestamp=...` name (which contains `timestamp=`
	// as a substring). Also explicitly reject the old name.
	if !strings.Contains(form, "&timestamp=1712345678.000100") {
		t.Fatalf("form body missing timestamp: %q", form)
	}
	if strings.Contains(form, "channel_timestamp") {
		t.Fatalf("form body must not use legacy channel_timestamp name: %q", form)
	}
}

func TestUnsave_TreatsIdempotentErrorsAsSuccess(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"not_starred", loadFixture(t, "stars_remove_not_starred.json")},
		{"already_unstarred", []byte(`{"ok":false,"error":"already_unstarred"}`)},
		{"message_not_found", []byte(`{"ok":false,"error":"message_not_found"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMockHandler()
			h.setBody("stars.remove", tc.body)
			server := httptest.NewServer(h)
			defer server.Close()

			c := &Client{Token: "xoxp-test", BaseURL: server.URL}
			err := c.Unsave(ctxWithTimeout(t), "C0123", "1.1")
			if err != nil {
				t.Fatalf("Unsave(%s) err = %v, want nil", tc.name, err)
			}
		})
	}
}

func TestUnsave_ReturnsMissingScopeSentinel(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"missing_scope", loadFixture(t, "stars_remove_missing_scope.json")},
		{"invalid_auth", []byte(`{"ok":false,"error":"invalid_auth"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMockHandler()
			h.setBody("stars.remove", tc.body)
			server := httptest.NewServer(h)
			defer server.Close()

			c := &Client{Token: "xoxp-test", BaseURL: server.URL}
			err := c.Unsave(ctxWithTimeout(t), "C0123", "1.1")
			if err == nil {
				t.Fatalf("Unsave(%s) err = nil, want non-nil", tc.name)
			}
			if !errors.Is(err, ErrMissingScope) {
				t.Fatalf("Unsave(%s) err = %v, want errors.Is(ErrMissingScope)", tc.name, err)
			}
		})
	}
}

func TestUnsave_UnknownErrorReturnsDescriptive(t *testing.T) {
	h := newMockHandler()
	h.setBody("stars.remove", loadFixture(t, "error_unknown.json"))
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	err := c.Unsave(ctxWithTimeout(t), "C0123", "1.1")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrMissingScope) {
		t.Fatalf("err %v should not match ErrMissingScope", err)
	}
	if !strings.Contains(err.Error(), "ratelimited") {
		t.Fatalf("err = %v, want to contain Slack error text", err)
	}
}

func TestPostForm_RateLimitReturnsRetryAfter(t *testing.T) {
	// Serve 429 with Retry-After header.
	h := newMockHandler()
	h.mu.Lock()
	h.status = http.StatusTooManyRequests
	h.headers.Set("Retry-After", "30")
	h.bodies["auth.test"] = []byte(`{"ok":false,"error":"ratelimited"}`)
	h.mu.Unlock()
	server := httptest.NewServer(h)
	defer server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL}
	_, err := c.AuthTest(ctxWithTimeout(t))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v, want rate limited", err)
	}
	if !strings.Contains(err.Error(), "30") {
		t.Fatalf("err = %v, want retry-after 30s", err)
	}
}

func TestPostForm_NetworkErrorWrapped(t *testing.T) {
	// Point at a closed server to force a dial failure.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	c := &Client{Token: "xoxp-test", BaseURL: server.URL, HTTP: &http.Client{Timeout: 500 * time.Millisecond}}
	_, err := c.AuthTest(ctxWithTimeout(t))
	if err == nil {
		t.Fatal("expected network error")
	}
	if !strings.Contains(err.Error(), "slack auth.test") {
		t.Fatalf("err = %v, want method-prefixed", err)
	}
}

func TestSubdomainFromURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://myteam.slack.com/", "myteam"},
		{"https://myteam.slack.com", "myteam"},
		{"https://foo.enterprise.slack.com/", "foo"},
		{"https://slack.com/", ""},
		{"", ""},
		{"not a url at all %%%", ""},
	}
	for _, tc := range cases {
		got := subdomainFromURL(tc.in)
		if got != tc.want {
			t.Errorf("subdomainFromURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPermalinkFor(t *testing.T) {
	got := permalinkFor("myteam", "C0123", "1712345678.000100")
	want := "https://myteam.slack.com/archives/C0123/p1712345678000100"
	if got != want {
		t.Errorf("permalinkFor = %q, want %q", got, want)
	}
	if permalinkFor("", "C", "1.1") != "" {
		t.Error("permalinkFor with empty workspace should return empty")
	}
}

func TestPostForm_BearerAuthHeaderSent(t *testing.T) {
	var gotAuth string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true,"url":"https://myteam.slack.com/","team":"t"}`)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	c := &Client{Token: "xoxp-abc", BaseURL: server.URL}
	_, _ = c.AuthTest(ctxWithTimeout(t))
	if gotAuth != "Bearer xoxp-abc" {
		t.Fatalf("Authorization header = %q, want Bearer xoxp-abc", gotAuth)
	}
}
