package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// helpOutput runs rootCmd --help for the given subcommand and returns the
// combined stdout/stderr output.
func helpOutput(t *testing.T, args ...string) string {
	t.Helper()
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("help command %v failed: %v\noutput: %s", args, err, buf.String())
	}
	return buf.String()
}

func TestAddHelp_RecurFlagMentionsAllGrammarForms(t *testing.T) {
	out := helpOutput(t, "add", "--help")

	// All four canonical grammar forms must appear in the help string.
	for _, form := range []string{"monthly:N", "weekly:<day>", "workdays", "days:N"} {
		if !strings.Contains(out, form) {
			t.Errorf("add --help missing grammar form %q; got:\n%s", form, out)
		}
	}

	// At least one concrete example should be present to demonstrate usage.
	if !strings.Contains(out, "e.g.") {
		t.Errorf("add --help should contain an examples marker (e.g.); got:\n%s", out)
	}
	if !strings.Contains(out, "weekly:mon") {
		t.Errorf("add --help should contain a concrete example like 'weekly:mon'; got:\n%s", out)
	}
}

func TestEditHelp_RecurFlagMentionsAllGrammarFormsAndClearNote(t *testing.T) {
	out := helpOutput(t, "edit", "--help")

	for _, form := range []string{"monthly:N", "weekly:<day>", "workdays", "days:N"} {
		if !strings.Contains(out, form) {
			t.Errorf("edit --help missing grammar form %q; got:\n%s", form, out)
		}
	}

	// The edit help string must preserve the explicit clear note.
	if !strings.Contains(out, `pass "" to clear`) {
		t.Errorf(`edit --help should preserve 'pass "" to clear' note; got:\n%s`, out)
	}

	if !strings.Contains(out, "e.g.") {
		t.Errorf("edit --help should contain an examples marker (e.g.); got:\n%s", out)
	}
}
