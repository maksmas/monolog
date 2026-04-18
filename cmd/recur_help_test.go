package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// helpOutput runs rootCmd --help for the given subcommand and returns stdout.
// stderr is captured separately and any unexpected content there fails the
// test — help output should go to stdout only.
func helpOutput(t *testing.T, args ...string) string {
	t.Helper()
	rootCmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("help command %v failed: %v\nstdout: %s\nstderr: %s",
			args, err, stdout.String(), stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("help command %v wrote unexpected stderr: %s", args, stderr.String())
	}
	return stdout.String()
}

// grammarFormOrder is the documented canonical order (matches
// recurrence.GrammarHint). Any reordering here must also update the
// GrammarHint constant and every user-visible help/hint surface.
var grammarFormOrder = []string{"monthly:N", "weekly:<day>", "workdays", "days:N"}

// assertGrammarFormsInOrder fails the test unless every form in
// grammarFormOrder appears in out AND they appear in that order (non-
// overlapping, left-to-right). Pins the documented grammar ordering so
// help text cannot silently drift out of sync with GrammarHint.
func assertGrammarFormsInOrder(t *testing.T, label, out string) {
	t.Helper()
	cursor := 0
	for _, form := range grammarFormOrder {
		idx := strings.Index(out[cursor:], form)
		if idx < 0 {
			t.Errorf("%s: grammar form %q not found at or after position %d; got:\n%s",
				label, form, cursor, out)
			return
		}
		cursor += idx + len(form)
	}
}

func TestAddHelp_RecurFlagMentionsAllGrammarForms(t *testing.T) {
	out := helpOutput(t, "add", "--help")

	// All four canonical grammar forms must appear in the help string...
	for _, form := range grammarFormOrder {
		if !strings.Contains(out, form) {
			t.Errorf("add --help missing grammar form %q; got:\n%s", form, out)
		}
	}
	// ...and in the documented order matching recurrence.GrammarHint.
	assertGrammarFormsInOrder(t, "add --help", out)

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

	for _, form := range grammarFormOrder {
		if !strings.Contains(out, form) {
			t.Errorf("edit --help missing grammar form %q; got:\n%s", form, out)
		}
	}
	assertGrammarFormsInOrder(t, "edit --help", out)

	// The edit help string must preserve the explicit clear note.
	if !strings.Contains(out, `pass "" to clear`) {
		t.Errorf(`edit --help should preserve 'pass "" to clear' note; got:\n%s`, out)
	}

	if !strings.Contains(out, "e.g.") {
		t.Errorf("edit --help should contain an examples marker (e.g.); got:\n%s", out)
	}
}
