package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/email"
)

// fakeEmailGmail is a recording stub used by every cmd-level email test.
type fakeEmailGmail struct {
	listIDs    []string
	listErr    error
	messages   map[string]*email.Message
	getErr     error
	archiveErr error
	archived   []string
}

func (f *fakeEmailGmail) ListLabeled(ctx context.Context, label string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]string, len(f.listIDs))
	copy(out, f.listIDs)
	return out, nil
}

func (f *fakeEmailGmail) Get(ctx context.Context, id string) (*email.Message, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	m, ok := f.messages[id]
	if !ok {
		return nil, fmt.Errorf("not found: %s", id)
	}
	cp := *m
	return &cp, nil
}

func (f *fakeEmailGmail) ArchiveLabel(ctx context.Context, id string) error {
	if f.archiveErr != nil {
		return f.archiveErr
	}
	f.archived = append(f.archived, id)
	return nil
}

var _ email.Gmail = (*fakeEmailGmail)(nil)

// stubEmailFactory replaces the package-level emailClientFactory with one
// returning the supplied fake. The cleanup restores the original.
func stubEmailFactory(t *testing.T, g email.Gmail, expiry time.Time, err error) {
	t.Helper()
	prev := emailClientFactory
	emailClientFactory = func(ctx context.Context, ec config.EmailConfig) (email.Gmail, time.Time, error) {
		if err != nil {
			return nil, time.Time{}, err
		}
		return g, expiry, nil
	}
	t.Cleanup(func() { emailClientFactory = prev })
}

// stubEmailAuthorize replaces email.Authorize's seam with a fake that
// records the args it was called with and returns the supplied error.
func stubEmailAuthorize(t *testing.T, gotPaths *[2]string, retErr error) {
	t.Helper()
	prev := emailAuthorize
	emailAuthorize = func(ctx context.Context, clientSecretsPath, tokenPath string) error {
		gotPaths[0] = clientSecretsPath
		gotPaths[1] = tokenPath
		return retErr
	}
	t.Cleanup(func() { emailAuthorize = prev })
}

// enableEmailConfig writes config.json with the email block enabled and
// reloads config so subsequent calls see it. Returns the token path which
// callers can populate with a fake token.
func enableEmailConfig(t *testing.T, monologDir string) (clientSecretsPath, tokenPath string) {
	t.Helper()
	// Direct $XDG_CONFIG_HOME at a sandbox so the default
	// gmail_credentials.json path lands somewhere safe and writable.
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	clientSecretsPath = filepath.Join(xdg, "monolog", "gmail_credentials.json")
	tokenPath = filepath.Join(xdg, "monolog", "gmail_token.json")

	if err := config.SaveEmail(monologDir, config.EmailConfig{
		Enabled:           true,
		Label:             "monolog",
		SyncInterval:      5 * time.Minute,
		MaxPerSync:        100,
		ClientSecretsPath: clientSecretsPath,
	}); err != nil {
		t.Fatalf("SaveEmail: %v", err)
	}
	if err := config.Load(monologDir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return clientSecretsPath, tokenPath
}

// writeFakeToken drops a JSON-encoded oauth2 token at path with the given
// expiry. Used by status tests to simulate an authorized state.
func writeFakeToken(t *testing.T, path string, expiry time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	tok := &oauth2.Token{
		AccessToken:  "fake-access",
		RefreshToken: "fake-refresh",
		TokenType:    "Bearer",
		Expiry:       expiry,
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
}

// --- email sync ---

func TestEmailSyncCommand_Success(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableEmailConfig(t, dir)

	fake := &fakeEmailGmail{
		listIDs: []string{"msg1", "msg2"},
		messages: map[string]*email.Message{
			"msg1": {ID: "msg1", Subject: "Hello", From: "Alice <a@example.com>", Snippet: "snip1"},
			"msg2": {ID: "msg2", Subject: "World", From: "Bob <b@example.com>", Snippet: "snip2"},
		},
	}
	stubEmailFactory(t, fake, time.Now().Add(time.Hour), nil)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"email", "sync"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("email sync error = %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "created 2 task(s)") {
		t.Errorf("expected 'created 2 task(s)' in output, got: %s", out)
	}

	// Verify the tasks landed and got committed.
	tasks := readTasks(t, dir)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks on disk, got %d", len(tasks))
	}
	gotSrc := map[string]bool{}
	for _, tk := range tasks {
		if tk.Source != "gmail" {
			t.Errorf("Source = %q, want gmail", tk.Source)
		}
		gotSrc[tk.SourceID] = true
	}
	for _, want := range []string{"msg1", "msg2"} {
		if !gotSrc[want] {
			t.Errorf("missing SourceID %q", want)
		}
	}

	gitCmd := exec.Command("git", "-C", dir, "log", "--oneline", "-1")
	logOut, err := gitCmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(string(logOut), "email: imported 2 task(s)") {
		t.Errorf("unexpected commit subject: %s", string(logOut))
	}
}

func TestEmailSyncCommand_NoTokenError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableEmailConfig(t, dir)

	// Factory returns a wrapped auth-missing error.
	authErr := fmt.Errorf("email: token not found at /tmp/x — run monolog email auth")
	stubEmailFactory(t, nil, time.Time{}, authErr)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"email", "sync"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when token missing, got nil")
	}
	if !strings.Contains(err.Error(), "run monolog email auth") {
		t.Errorf("error should hint 'run monolog email auth', got: %v", err)
	}
}

func TestEmailSyncCommand_DisabledIsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// initTestRepo sets MONOLOG_DIR but doesn't enable email — config has
	// no email block, so Email().Enabled is false.
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"email", "sync"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when email integration disabled, got nil")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled', got: %v", err)
	}
}

func TestEmailSyncCommand_PropagatesSyncError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableEmailConfig(t, dir)

	fake := &fakeEmailGmail{listErr: errors.New("api down")}
	stubEmailFactory(t, fake, time.Now().Add(time.Hour), nil)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"email", "sync"})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when ListLabeled fails, got nil")
	}
}

// --- email status ---

func TestEmailStatusCommand_Authorized(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	_, tokPath := enableEmailConfig(t, dir)

	expiry := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	writeFakeToken(t, tokPath, expiry)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"email", "status"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("email status error = %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "auth: token loaded") {
		t.Errorf("expected 'auth: token loaded' in output, got: %s", out)
	}
	if !strings.Contains(out, "2026-05-01T12:00:00Z") {
		t.Errorf("expected expiry timestamp in output, got: %s", out)
	}
	if !strings.Contains(out, "enabled: true") {
		t.Errorf("expected 'enabled: true' in output, got: %s", out)
	}
	if !strings.Contains(out, "label: monolog") {
		t.Errorf("expected 'label: monolog' in output, got: %s", out)
	}
	if !strings.Contains(out, "interval: 5m") {
		t.Errorf("expected interval line in output, got: %s", out)
	}
	if !strings.Contains(out, "max_per_sync: 100") {
		t.Errorf("expected max_per_sync in output, got: %s", out)
	}
}

func TestEmailStatusCommand_Unauthorized(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableEmailConfig(t, dir)
	// No token file exists at this path — status should print the hint.

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"email", "status"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("email status error = %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "not authorized") {
		t.Errorf("expected 'not authorized' in output, got: %s", out)
	}
	if !strings.Contains(out, "monolog email auth") {
		t.Errorf("expected hint 'monolog email auth' in output, got: %s", out)
	}
}

func TestEmailStatusCommand_DisabledStillRenders(t *testing.T) {
	// Even when email is disabled (no email block in config), status should
	// still print without error.
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"email", "status"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("email status error = %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "enabled: false") {
		t.Errorf("expected 'enabled: false' in output, got: %s", out)
	}
}

// --- email auth ---

func TestEmailAuthCommand_SuccessFlipsEnabledAndPrintsPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// Direct $XDG_CONFIG_HOME at a temp dir so the default secrets path
	// lands somewhere safe.
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	var gotPaths [2]string
	stubEmailAuthorize(t, &gotPaths, nil)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"email", "auth"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("email auth error = %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "authorized; token saved to") {
		t.Errorf("expected success message, got: %s", out)
	}

	// Authorize must have been called with the secrets+token paths.
	wantSecrets := filepath.Join(xdg, "monolog", "gmail_credentials.json")
	wantToken := filepath.Join(xdg, "monolog", "gmail_token.json")
	if gotPaths[0] != wantSecrets {
		t.Errorf("Authorize secrets path = %q, want %q", gotPaths[0], wantSecrets)
	}
	if gotPaths[1] != wantToken {
		t.Errorf("Authorize token path = %q, want %q", gotPaths[1], wantToken)
	}

	// After auth, the email block should be enabled in config.json.
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load after auth: %v", err)
	}
	if !config.Email().Enabled {
		t.Error("expected Email().Enabled = true after auth")
	}
}

func TestEmailAuthCommand_PropagatesAuthorizeError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	var gotPaths [2]string
	stubEmailAuthorize(t, &gotPaths, errors.New("oauth: user denied"))

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"email", "auth"})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when Authorize returns an error, got nil")
	}

	// On Authorize failure, enabled MUST NOT be flipped on.
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if config.Email().Enabled {
		t.Error("Email().Enabled should remain false after Authorize failure")
	}
}

// --- helper: email.TokenPathFor ---

func TestTokenPathFor(t *testing.T) {
	cases := []struct {
		name string
		in   config.EmailConfig
		want string
	}{
		{
			name: "default secrets path next to xdg",
			in:   config.EmailConfig{ClientSecretsPath: "/home/u/.config/monolog/gmail_credentials.json"},
			want: filepath.Join("/home/u/.config/monolog", "gmail_token.json"),
		},
		{
			name: "custom secrets path",
			in:   config.EmailConfig{ClientSecretsPath: "/etc/monolog/secret.json"},
			want: filepath.Join("/etc/monolog", "gmail_token.json"),
		},
		{
			name: "empty secrets path",
			in:   config.EmailConfig{ClientSecretsPath: ""},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := email.TokenPathFor(tc.in.ClientSecretsPath)
			if got != tc.want {
				t.Errorf("email.TokenPathFor(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// --- emailCmd registration sanity ---

func TestEmailCmdHasSubcommands(t *testing.T) {
	root := NewRootCmd()
	var emailCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Use == "email" {
			emailCmd = c
			break
		}
	}
	if emailCmd == nil {
		t.Fatal("'email' command not registered on root")
	}
	wantSubs := map[string]bool{"sync": false, "auth": false, "status": false}
	for _, sub := range emailCmd.Commands() {
		if _, ok := wantSubs[sub.Use]; ok {
			wantSubs[sub.Use] = true
		}
	}
	for name, found := range wantSubs {
		if !found {
			t.Errorf("expected 'email %s' subcommand to be registered", name)
		}
	}
}
