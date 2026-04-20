package cmd

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// TestWithPager_NoOpWhenNotFile verifies that when w is a *bytes.Buffer (not
// an *os.File), withPager calls fn directly with the same writer — no pager
// subprocess is launched.
func TestWithPager_NoOpWhenNotFile(t *testing.T) {
	var buf bytes.Buffer
	called := false
	var gotWriter io.Writer

	err := withPager(&buf, func(w io.Writer) error {
		called = true
		gotWriter = w
		_, werr := w.Write([]byte("hello"))
		return werr
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("fn was not called")
	}
	if gotWriter != &buf {
		t.Errorf("fn received a different writer; want the original *bytes.Buffer")
	}
	if buf.String() != "hello" {
		t.Errorf("buf = %q; want %q", buf.String(), "hello")
	}
}

// TestWithPager_PropagatesFnError verifies that when fn returns a non-nil error
// and w is not a TTY (bytes.Buffer), withPager propagates that exact error.
func TestWithPager_PropagatesFnError(t *testing.T) {
	var buf bytes.Buffer
	sentinel := errors.New("fn error sentinel")

	err := withPager(&buf, func(w io.Writer) error {
		return sentinel
	})

	if !errors.Is(err, sentinel) {
		t.Errorf("withPager should propagate fn's error; got %v, want %v", err, sentinel)
	}
}
