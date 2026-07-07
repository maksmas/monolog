package cmd

import (
	"bytes"
	"testing"
)

// TestVersionDefault asserts the package-default Version is non-empty and is
// "dev" until overridden at build time via ldflags.
func TestVersionDefault(t *testing.T) {
	if Version == "" {
		t.Fatal("Version should be non-empty")
	}
	if Version != "dev" {
		t.Errorf("default Version should be %q, got %q", "dev", Version)
	}

	if got := NewRootCmd().Version; got != Version {
		t.Errorf("NewRootCmd().Version = %q, want %q", got, Version)
	}
}

// TestVersionFlagOutput asserts that invoking the root command with --version
// renders output containing the current Version string.
func TestVersionFlagOutput(t *testing.T) {
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"--version"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("--version should succeed, got %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("expected version output, got empty string")
	}
	if !bytes.Contains(buf.Bytes(), []byte(Version)) {
		t.Errorf("version output should contain %q, got: %s", Version, output)
	}
}

// TestVersionOverrideInjection simulates a release build's ldflags override
// (-X 'github.com/maksmas/monolog/cmd.Version=vX.Y.Z') by mutating the Version
// package var, then asserts NewRootCmd() reads it dynamically and --version
// reflects the injected value. This fails if the command hardcodes a literal
// (e.g. Version: "dev") instead of Version: Version.
func TestVersionOverrideInjection(t *testing.T) {
	const sentinel = "v9.9.9-test"
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = sentinel

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"--version"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("--version should succeed, got %v", err)
	}

	if !bytes.Contains(buf.Bytes(), []byte(sentinel)) {
		t.Errorf("version output should contain injected %q, got: %s", sentinel, buf.String())
	}
}
