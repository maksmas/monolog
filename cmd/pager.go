package cmd

import (
	"io"
	"os"
	"os/exec"

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

	cmd := exec.Command("sh", "-c", pagerCmd) //nolint:gosec
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
		_ = pw.Close()    // flush remaining input to pager so it can exit cleanly
		_ = cmd.Wait()   // pager exit code (e.g. user quit) is not an application error
		return err
	}

	_ = pw.Close()  // signal EOF to the pager so it can exit cleanly
	_ = cmd.Wait()  // pager exit code (e.g. user quit) is not an application error
	return nil
}
