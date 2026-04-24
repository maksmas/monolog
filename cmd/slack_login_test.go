package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmaksmas/monolog/internal/slack"
)

// slackLoginStubs installs test doubles for the browser opener, token reader,
// and slack client factory. The returned restore closure reverts all three
// package-level overrides; tests defer it to keep other tests isolated.
func slackLoginStubs(t *testing.T, token string, server *httptest.Server) func() {
	t.Helper()

	origOpen := openBrowserFn
	origRead := readTokenFn
	origFactory := newSlackClientFn

	openBrowserFn = func(string) error { return nil }
	readTokenFn = func(_ io.Reader) (string, error) { return token, nil }
	newSlackClientFn = func(tok, ws string) *slack.Client {
		if server == nil {
			return &slack.Client{Token: tok, Workspace: ws}
		}
		return &slack.Client{Token: tok, Workspace: ws, BaseURL: server.URL}
	}

	return func() {
		openBrowserFn = origOpen
		readTokenFn = origRead
		newSlackClientFn = origFactory
	}
}

// slackAuthTestHandler serves a canned auth.test response. url is the full
// workspace URL (e.g. "https://myteam.slack.com/"); when ok is false, the body
// is {"ok":false,"error":"invalid_auth"} instead.
func slackAuthTestHandler(t *testing.T, ok bool, url string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"url":"` + url + `","team":"My Team"}`))
	})
	return httptest.NewServer(mux)
}

func TestSlackLogin_HappyPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	server := slackAuthTestHandler(t, true, "https://myteam.slack.com/")
	defer server.Close()

	restore := slackLoginStubs(t, "xoxp-fake-token", server)
	defer restore()

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-login"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("slack-login error: %v\nout: %s\nerr: %s", err, outBuf.String(), errBuf.String())
	}

	// Token file is written with the pasted token.
	tokenPath := filepath.Join(dir, ".monolog", "slack_token")
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	// Check mode 0600 (ignore file-type bits).
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("token file mode = %v, want 0600", mode)
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "xoxp-fake-token" {
		t.Errorf("token file contents = %q, want %q", got, "xoxp-fake-token")
	}

	// .gitignore includes slack_token.
	gitignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), "slack_token") {
		t.Errorf(".gitignore missing slack_token entry, got:\n%s", gitignore)
	}

	// Config records workspace + enabled=true.
	configPath := filepath.Join(dir, ".monolog", "config.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg struct {
		Slack struct {
			Enabled   bool   `json:"enabled"`
			Workspace string `json:"workspace"`
		} `json:"slack"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v\n%s", err, raw)
	}
	if cfg.Slack.Workspace != "myteam" {
		t.Errorf("slack.workspace = %q, want %q", cfg.Slack.Workspace, "myteam")
	}
	if !cfg.Slack.Enabled {
		t.Errorf("slack.enabled = false, want true")
	}

	// Success message references the subdomain.
	if out := outBuf.String(); !strings.Contains(out, "myteam.slack.com") {
		t.Errorf("success message missing subdomain, got:\n%s", out)
	}
}

func TestSlackLogin_EmptyToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// No server needed — we fail before contacting Slack.
	restore := slackLoginStubs(t, "", nil)
	defer restore()

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-login"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error on empty token, got nil (out=%s)", outBuf.String())
	}

	// No token file created.
	tokenPath := filepath.Join(dir, ".monolog", "slack_token")
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Errorf("token file should not exist; stat err = %v", err)
	}

	// Config should not have a slack block (init wrote a default config that
	// has no slack key; assert it was not mutated to add one).
	raw, err := os.ReadFile(filepath.Join(dir, ".monolog", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), `"slack"`) {
		t.Errorf("config should not contain slack block after failed login, got:\n%s", raw)
	}
}

func TestSlackLogin_InvalidAuth(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	server := slackAuthTestHandler(t, false, "")
	defer server.Close()

	restore := slackLoginStubs(t, "xoxp-bad", server)
	defer restore()

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"slack-login"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error on invalid auth, got nil (out=%s)", outBuf.String())
	}

	// No token file created.
	tokenPath := filepath.Join(dir, ".monolog", "slack_token")
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Errorf("token file should not exist; stat err = %v", err)
	}

	// Config unchanged (no slack block).
	raw, err := os.ReadFile(filepath.Join(dir, ".monolog", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), `"slack"`) {
		t.Errorf("config should not have slack block after invalid_auth, got:\n%s", raw)
	}
}

func TestSlackLogin_UpgradesMissingGitignoreEntry(t *testing.T) {
	// Simulate an older repo: one whose .gitignore does NOT contain
	// slack_token. Verify slack-login appends it.
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Overwrite the gitignore to remove the slack_token line.
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("# monolog gitignore\n"), 0o644); err != nil {
		t.Fatalf("rewrite .gitignore: %v", err)
	}

	server := slackAuthTestHandler(t, true, "https://acme.slack.com/")
	defer server.Close()

	restore := slackLoginStubs(t, "xoxp-real", server)
	defer restore()

	rootCmd := NewRootCmd()
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"slack-login"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("slack-login error: %v", err)
	}

	raw, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	if !strings.Contains(string(raw), "slack_token") {
		t.Errorf(".gitignore should contain slack_token after upgrade, got:\n%s", raw)
	}
}
