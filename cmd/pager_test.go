package cmd

import (
	"bytes"
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
