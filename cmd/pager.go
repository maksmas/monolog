package cmd

import (
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/x/term"
)

// withPager calls fn, routing its output through a pager when w is a TTY.
//
// Pager selection: $PAGER env var if set; otherwise "less -FR".
// If w is not an *os.File or is not a TTY, fn is called with w directly —
// no subprocess is spawned. On any exec error the helper falls back to calling
// fn(w) directly so callers never see a crash.
func withPager(w io.Writer, fn func(io.Writer) error) error {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return fn(w)
	}

	pagerCmd := os.Getenv("PAGER")
	if pagerCmd == "" {
		pagerCmd = "less -FR"
	}

	parts := strings.Fields(pagerCmd)
	cmd := exec.Command(parts[0], parts[1:]...) //nolint:gosec
	cmd.Stdout = w
	cmd.Stderr = os.Stderr

	pw, err := cmd.StdinPipe()
	if err != nil {
		return fn(w)
	}

	if err := cmd.Start(); err != nil {
		return fn(w)
	}

	if err := fn(pw); err != nil {
		_ = pw.Close()
		_ = cmd.Wait()
		return err
	}

	_ = pw.Close()
	_ = cmd.Wait()
	return nil
}
