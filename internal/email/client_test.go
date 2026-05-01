package email

import (
	"context"
	"errors"
	"testing"
)

// fakeGmail is the in-memory Gmail implementation used by every email-package
// test. It implements the Gmail interface so tests can verify call-site
// behavior without touching the network.
type fakeGmail struct {
	listIDs   []string
	listErr   error
	listCalls int

	messages map[string]*Message
	getErr   error
	getCalls int

	archived    []string
	archiveErr  error
	archiveCall int
}

func (f *fakeGmail) ListLabeled(ctx context.Context, label string) ([]string, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	// Return a copy so callers can't mutate fixtures by accident.
	out := make([]string, len(f.listIDs))
	copy(out, f.listIDs)
	return out, nil
}

func (f *fakeGmail) Get(ctx context.Context, id string) (*Message, error) {
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	m, ok := f.messages[id]
	if !ok {
		return nil, errors.New("not found: " + id)
	}
	cp := *m
	return &cp, nil
}

func (f *fakeGmail) ArchiveLabel(ctx context.Context, id string) error {
	f.archiveCall++
	if f.archiveErr != nil {
		return f.archiveErr
	}
	f.archived = append(f.archived, id)
	return nil
}

// Compile-time interface satisfaction check — if fakeGmail ever drifts from
// the Gmail interface this fails to compile, which is the cheapest possible
// regression alarm.
var _ Gmail = (*fakeGmail)(nil)

func TestFakeGmailListLabeled(t *testing.T) {
	tests := []struct {
		name    string
		fake    *fakeGmail
		label   string
		want    []string
		wantErr bool
	}{
		{
			name:  "empty",
			fake:  &fakeGmail{},
			label: "monolog",
			want:  nil,
		},
		{
			name:  "preserves order",
			fake:  &fakeGmail{listIDs: []string{"a", "b", "c"}},
			label: "monolog",
			want:  []string{"a", "b", "c"},
		},
		{
			name:    "propagates error",
			fake:    &fakeGmail{listErr: errors.New("boom")},
			label:   "monolog",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fake.ListLabeled(context.Background(), tc.label)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !equal(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if tc.fake.listCalls != 1 {
				t.Fatalf("listCalls=%d want 1", tc.fake.listCalls)
			}
		})
	}
}

func TestFakeGmailGet(t *testing.T) {
	f := &fakeGmail{
		messages: map[string]*Message{
			"id1": {ID: "id1", Subject: "hello", From: "a@example.com", Snippet: "snip"},
		},
	}
	got, err := f.Get(context.Background(), "id1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.ID != "id1" || got.Subject != "hello" || got.From != "a@example.com" || got.Snippet != "snip" {
		t.Fatalf("unexpected msg: %+v", got)
	}
	// Mutating the returned value must not affect the fake's storage.
	got.Subject = "mutated"
	again, _ := f.Get(context.Background(), "id1")
	if again.Subject != "hello" {
		t.Fatalf("fake storage mutated: %q", again.Subject)
	}

	if _, err := f.Get(context.Background(), "missing"); err == nil {
		t.Fatalf("expected err for missing id")
	}

	f.getErr = errors.New("api down")
	if _, err := f.Get(context.Background(), "id1"); err == nil {
		t.Fatalf("expected getErr to propagate")
	}
}

func TestFakeGmailArchiveLabel(t *testing.T) {
	f := &fakeGmail{}
	if err := f.ArchiveLabel(context.Background(), "id1"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if err := f.ArchiveLabel(context.Background(), "id2"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !equal(f.archived, []string{"id1", "id2"}) {
		t.Fatalf("archived=%v want [id1 id2]", f.archived)
	}

	f.archiveErr = errors.New("nope")
	if err := f.ArchiveLabel(context.Background(), "id3"); err == nil {
		t.Fatalf("expected archiveErr to propagate")
	}
	// On error, the id must not be appended.
	if len(f.archived) != 2 {
		t.Fatalf("archived grew on error: %v", f.archived)
	}
}

func TestNewClientNilHTTPClient(t *testing.T) {
	// NewClient with a nil http.Client should fail-fast rather than crash
	// later inside the Gmail SDK. The real success path needs OAuth and is
	// covered by the manual smoke test.
	if _, err := NewClient(context.Background(), nil); err == nil {
		t.Fatal("expected err for nil http client")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
