package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
)

// writeTaskFile plants a task JSON file directly in the tasks dir. Used by
// status tests to seed the store without going through the store package.
func writeTaskFile(t *testing.T, dir string, task model.Task) {
	t.Helper()
	tasksDir := filepath.Join(dir, ".monolog", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	p := filepath.Join(tasksDir, task.ID+".json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write task file: %v", err)
	}
}

// writeSlackConfigBlock rewrites config.json to include the given slack
// values, preserving any existing non-slack keys.
func writeSlackConfigBlock(t *testing.T, dir string, block map[string]any) {
	t.Helper()
	p := filepath.Join(dir, ".monolog", "config.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	cfg["slack"] = block
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(p, append(out, '\n'), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestSlackStatus_EnabledWithTokenFileAndTasks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Config says enabled=true, workspace=myteam.
	writeSlackConfigBlock(t, dir, map[string]any{
		"enabled":   true,
		"workspace": "myteam",
	})
	// Token file planted — source should resolve to "file".
	writeSlackTokenFile(t, dir, "xoxp-real")
	// Ensure env var is not interfering (test-local).
	t.Setenv("MONOLOG_SLACK_TOKEN", "")

	// Seed 2 slack-sourced tasks and 1 non-slack task.
	now := time.Now().UTC().Format(time.RFC3339)
	writeTaskFile(t, dir, model.Task{
		ID: "01HTESTSLACKTASK1AAAAAAAAA", Title: "from slack 1", Status: "open",
		Source: "slack", SourceID: "C1/1", Schedule: "today", Position: 1000,
		CreatedAt: now, UpdatedAt: now,
	})
	writeTaskFile(t, dir, model.Task{
		ID: "01HTESTSLACKTASK2AAAAAAAAA", Title: "from slack 2", Status: "open",
		Source: "slack", SourceID: "C1/2", Schedule: "today", Position: 2000,
		CreatedAt: now, UpdatedAt: now,
	})
	writeTaskFile(t, dir, model.Task{
		ID: "01HTESTMANUALAAAAAAAAAAAA", Title: "manual", Status: "open",
		Source: "tui", Schedule: "today", Position: 3000,
		CreatedAt: now, UpdatedAt: now,
	})

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("slack-status error: %v\nerr: %s", err, errBuf.String())
	}

	got := outBuf.String()
	wantSubstrings := []string{
		"Workspace:",
		"myteam",
		"Token source:",
		"file",
		"Enabled:",
		"true",
		"Ingested so far:",
		"2",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("status output missing %q; got:\n%s", want, got)
		}
	}
}

func TestSlackStatus_DisabledWithNoToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	// No slack config block, no token file, no env.
	t.Setenv("MONOLOG_SLACK_TOKEN", "")

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("slack-status error: %v\nerr: %s", err, errBuf.String())
	}

	got := outBuf.String()
	// Default workspace is empty → shown as "(not set)".
	for _, want := range []string{"(not set)", "none", "false", "Ingested so far: 0"} {
		if !strings.Contains(got, want) {
			t.Errorf("status output missing %q; got:\n%s", want, got)
		}
	}
}

func TestSlackStatus_EnvTokenWins(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// File token planted AND env var set — env should win.
	writeSlackTokenFile(t, dir, "xoxp-file")
	t.Setenv("MONOLOG_SLACK_TOKEN", "xoxp-env")
	writeSlackConfigBlock(t, dir, map[string]any{
		"enabled":   true,
		"workspace": "acme",
	})

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"slack-status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("slack-status error: %v", err)
	}

	got := outBuf.String()
	if !strings.Contains(got, "Token source:    env") {
		t.Errorf("expected env-sourced token, got:\n%s", got)
	}
}
