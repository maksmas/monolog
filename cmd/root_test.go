package cmd

import (
	"bytes"
	"testing"

	"github.com/mmaksmas/monolog/internal/tui"
)

func TestRootCommandHelp(t *testing.T) {
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"--help"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("expected help output, got empty string")
	}

	// Check that key help text elements are present
	if !bytes.Contains([]byte(output), []byte("Monolog")) {
		t.Error("help output should contain 'Monolog'")
	}
	if !bytes.Contains([]byte(output), []byte("personal backlog")) {
		t.Error("help output should contain 'personal backlog'")
	}
}

func TestRootCommandVersion(t *testing.T) {
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"--version"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("dev")) {
		t.Errorf("version output should contain 'dev', got: %s", output)
	}
}

func TestRootCommandNoArgs_InvokesTUI(t *testing.T) {
	// Stub the TUI hook so the test doesn't try to open a real terminal.
	called := false
	orig := runTUI
	runTUI = func(opts tui.Options) error { called = true; return nil }
	defer func() { runTUI = orig }()

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("root with no args should succeed, got %v", err)
	}
	if !called {
		t.Error("runTUI was not invoked by no-arg root command")
	}
}

func TestRootCommand_TagsFlagPassesOption(t *testing.T) {
	var gotOpts tui.Options
	orig := runTUI
	runTUI = func(opts tui.Options) error { gotOpts = opts; return nil }
	defer func() { runTUI = orig }()

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"--tags"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("--tags should succeed, got %v", err)
	}
	if !gotOpts.StartInTagView {
		t.Error("--tags flag should set StartInTagView=true")
	}
}

func TestRootCommand_TagsShortFlagPassesOption(t *testing.T) {
	var gotOpts tui.Options
	orig := runTUI
	runTUI = func(opts tui.Options) error { gotOpts = opts; return nil }
	defer func() { runTUI = orig }()

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"-T"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("-T should succeed, got %v", err)
	}
	if !gotOpts.StartInTagView {
		t.Error("-T flag should set StartInTagView=true")
	}
}

func TestRootCommand_NoTagsFlagDefaultsFalse(t *testing.T) {
	var gotOpts tui.Options
	orig := runTUI
	runTUI = func(opts tui.Options) error { gotOpts = opts; return nil }
	defer func() { runTUI = orig }()

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetArgs([]string{})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("no-args should succeed, got %v", err)
	}
	if gotOpts.StartInTagView {
		t.Error("without --tags flag, StartInTagView should be false")
	}
}

func TestRootCommandSubcommandsStillWork(t *testing.T) {
	// With Args: NoArgs + RunE on root, subcommands should still dispatch
	// without invoking the TUI.
	called := false
	orig := runTUI
	runTUI = func(opts tui.Options) error { called = true; return nil }
	defer func() { runTUI = orig }()

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("--help should succeed, got %v", err)
	}
	if called {
		t.Error("runTUI should not be invoked when a subcommand runs")
	}
}
