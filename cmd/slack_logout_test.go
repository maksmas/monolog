package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSlackTokenFile plants a token file inside an initialized monolog repo.
// Returns the absolute path of the file it created so tests can assert on it.
func writeSlackTokenFile(t *testing.T, dir, token string) string {
	t.Helper()
	path := filepath.Join(dir, ".monolog", "slack_token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return path
}

func TestSlackLogout_RemovesTokenAndDisables(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Simulate a prior slack-login: write a token file and set enabled=true in
	// config.json. We reach into the on-disk config directly rather than going
	// through slack-login so the test doesn't depend on httptest.
	tokenPath := writeSlackTokenFile(t, dir, "xoxp-prior")
	configPath := filepath.Join(dir, ".monolog", "config.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	cfg["slack"] = map[string]any{
		"enabled":   true,
		"workspace": "myteam",
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-logout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("slack-logout error: %v\nerr: %s", err, errBuf.String())
	}

	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Errorf("token file should be removed; stat err = %v", err)
	}

	// Config should now have enabled=false but preserve workspace.
	raw, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	var after struct {
		Slack struct {
			Enabled   bool   `json:"enabled"`
			Workspace string `json:"workspace"`
		} `json:"slack"`
	}
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatalf("unmarshal config after: %v\n%s", err, raw)
	}
	if after.Slack.Enabled {
		t.Errorf("slack.enabled should be false after logout, got true")
	}
	if after.Slack.Workspace != "myteam" {
		t.Errorf("slack.workspace should be preserved, got %q", after.Slack.Workspace)
	}

	if got := outBuf.String(); !strings.Contains(got, "Slack disconnected") {
		t.Errorf("expected 'Slack disconnected.' in output, got:\n%s", got)
	}
}

func TestSlackLogout_NoTokenAlreadyDisconnected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Capture config.json before the command so we can verify it is untouched.
	configPath := filepath.Join(dir, ".monolog", "config.json")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-logout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("slack-logout error: %v\nerr: %s", err, errBuf.String())
	}

	if got := outBuf.String(); !strings.Contains(got, "Already disconnected") {
		t.Errorf("expected 'Already disconnected.' in output, got:\n%s", got)
	}

	// Config should be untouched — we never called SetSlackEnabled.
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("config.json mutated by slack-logout on already-disconnected repo\nbefore: %s\nafter: %s", before, after)
	}
}
