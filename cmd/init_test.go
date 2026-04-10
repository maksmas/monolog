package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInitCommand_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	monologDir := filepath.Join(dir, "test-monolog")
	t.Setenv("MONOLOG_DIR", monologDir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("init command error = %v\noutput: %s", err, buf.String())
	}

	// Verify directory structure was created
	if _, err := os.Stat(filepath.Join(monologDir, ".git")); os.IsNotExist(err) {
		t.Error(".git should exist")
	}
	if _, err := os.Stat(filepath.Join(monologDir, ".monolog", "tasks")); os.IsNotExist(err) {
		t.Error(".monolog/tasks should exist")
	}
	if _, err := os.Stat(filepath.Join(monologDir, ".monolog", "config.json")); os.IsNotExist(err) {
		t.Error(".monolog/config.json should exist")
	}
	if _, err := os.Stat(filepath.Join(monologDir, ".gitignore")); os.IsNotExist(err) {
		t.Error(".gitignore should exist")
	}
}

func TestInitCommand_WithRemote(t *testing.T) {
	// Create a bare repo to act as the remote
	remoteDir := t.TempDir()
	bareRepo := filepath.Join(remoteDir, "remote.git")
	cmd := exec.Command("git", "init", "--bare", bareRepo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create bare repo: %v\n%s", err, out)
	}

	dir := t.TempDir()
	monologDir := filepath.Join(dir, "test-monolog")
	t.Setenv("MONOLOG_DIR", monologDir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--remote", bareRepo})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("init --remote command error = %v\noutput: %s", err, buf.String())
	}

	// Verify remote is configured
	gitCmd := exec.Command("git", "-C", monologDir, "remote", "get-url", "origin")
	out, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git remote get-url failed: %v", err)
	}
	if got := string(out[:len(out)-1]); got != bareRepo {
		t.Errorf("remote = %q, want %q", got, bareRepo)
	}
}

func TestInitCommand_AlreadyInitialized(t *testing.T) {
	dir := t.TempDir()
	monologDir := filepath.Join(dir, "test-monolog")
	t.Setenv("MONOLOG_DIR", monologDir)

	// First init
	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"init"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first init error = %v", err)
	}

	// Second init should error
	rootCmd2 := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"init"})
	err := rootCmd2.Execute()
	if err == nil {
		t.Error("expected error on second init, got nil")
	}
}

func TestInitCommand_SuccessMessage(t *testing.T) {
	dir := t.TempDir()
	monologDir := filepath.Join(dir, "test-monolog")
	t.Setenv("MONOLOG_DIR", monologDir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("init command error = %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Error("expected success message, got empty output")
	}
}
