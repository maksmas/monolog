package cmd

import (
	"bytes"
	"testing"
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

func TestRootCommandNoArgs(t *testing.T) {
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("expected no error running root with no args, got %v", err)
	}
}
