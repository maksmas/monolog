package cmd

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/telegram"
)

// fakeTelegramBot is a minimal Bot stand-in for cmd-level tests. The cobra
// wiring tests never call any method on it directly — telegramServeFunc is
// stubbed too — so the methods only need to satisfy the interface.
type fakeTelegramBot struct{}

func (fakeTelegramBot) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]telegram.Update, error) {
	return nil, nil
}
func (fakeTelegramBot) SendMessage(ctx context.Context, chatID int64, html string, kb telegram.InlineKeyboard) (int, error) {
	return 0, nil
}
func (fakeTelegramBot) EditMessage(ctx context.Context, chatID int64, msgID int, html string, kb telegram.InlineKeyboard) error {
	return nil
}
func (fakeTelegramBot) AnswerCallback(ctx context.Context, callbackID, toast string) error {
	return nil
}

var _ telegram.Bot = fakeTelegramBot{}

// stubTelegramClientFactory replaces the package-level telegramClientFactory
// with one returning the supplied fake. Cleanup restores the original. The
// recorded token (when non-nil) lets the caller assert on the value the
// command resolved.
func stubTelegramClientFactory(t *testing.T, gotToken *string, retErr error) {
	t.Helper()
	prev := telegramClientFactory
	telegramClientFactory = func(token string) (telegram.Bot, error) {
		if gotToken != nil {
			*gotToken = token
		}
		if retErr != nil {
			return nil, retErr
		}
		return fakeTelegramBot{}, nil
	}
	t.Cleanup(func() { telegramClientFactory = prev })
}

// stubTelegramServe replaces telegramServeFunc with a recording fake that
// captures the ServeOptions it was called with and returns retErr. Tests
// use this to verify the cobra wiring threads config / token / signal
// handling correctly without paying for the actual long-poll loop.
func stubTelegramServe(t *testing.T, gotOpts *telegram.ServeOptions, retErr error) {
	t.Helper()
	prev := telegramServeFunc
	telegramServeFunc = func(ctx context.Context, opts telegram.ServeOptions) error {
		if gotOpts != nil {
			*gotOpts = opts
		}
		return retErr
	}
	t.Cleanup(func() { telegramServeFunc = prev })
}

// stubTelegramSignalNotifyContext replaces the signal-handling wrapper with
// one returning a pre-cancelled context, so `serve` exits immediately when
// telegramServeFunc respects ctx.Err(). For tests that stub the serve func
// entirely (most of them) this just makes the assertion path simpler — the
// stub doesn't have to inspect ctx.
func stubTelegramSignalNotifyContextCancelled(t *testing.T) {
	t.Helper()
	prev := telegramSignalNotifyContext
	telegramSignalNotifyContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}
	t.Cleanup(func() { telegramSignalNotifyContext = prev })
}

// enableTelegramConfig writes config.json with the telegram block enabled,
// then reloads config so subsequent calls see it. Returns nothing — callers
// only care about the side effect on config + disk.
func enableTelegramConfig(t *testing.T, monologDir string) {
	t.Helper()
	if err := config.SaveTelegram(monologDir, config.TelegramConfig{
		Enabled:        true,
		AllowedUserIDs: []int64{42},
		PullInterval:   30 * time.Second,
		BrowseLimit:    20,
	}); err != nil {
		t.Fatalf("SaveTelegram: %v", err)
	}
	if err := config.Load(monologDir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
}

// --- telegram serve ---

func TestTelegramServeCommand_DisabledIsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	// initTestRepo did NOT enable the telegram block.
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "serve"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when telegram integration disabled, got nil")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled', got: %v", err)
	}
}

func TestTelegramServeCommand_MissingTokenIsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableTelegramConfig(t, dir)

	// Explicitly clear the env var; the flag is also unset.
	t.Setenv(telegramTokenEnvVar, "")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "serve"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when token unset, got nil")
	}
	if !strings.Contains(err.Error(), "telegram token required") {
		t.Errorf("error should mention 'telegram token required', got: %v", err)
	}
	if !strings.Contains(err.Error(), telegramTokenEnvVar) {
		t.Errorf("error should mention env var %s, got: %v", telegramTokenEnvVar, err)
	}
}

func TestTelegramServeCommand_FlagOverridesEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableTelegramConfig(t, dir)

	// Env var would otherwise be picked up; flag should win.
	t.Setenv(telegramTokenEnvVar, "env-token")

	var gotToken string
	stubTelegramClientFactory(t, &gotToken, nil)
	stubTelegramServe(t, nil, nil)
	stubTelegramSignalNotifyContextCancelled(t)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "serve", "--token", "flag-token"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("telegram serve error = %v\noutput: %s", err, buf.String())
	}

	if gotToken != "flag-token" {
		t.Errorf("client factory got token %q, want %q", gotToken, "flag-token")
	}
}

func TestTelegramServeCommand_AcceptsEnvWhenFlagUnset(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableTelegramConfig(t, dir)

	t.Setenv(telegramTokenEnvVar, "env-token")

	var gotToken string
	stubTelegramClientFactory(t, &gotToken, nil)

	var gotOpts telegram.ServeOptions
	stubTelegramServe(t, &gotOpts, nil)
	stubTelegramSignalNotifyContextCancelled(t)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "serve"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("telegram serve error = %v\noutput: %s", err, buf.String())
	}

	if gotToken != "env-token" {
		t.Errorf("client factory got token %q, want %q", gotToken, "env-token")
	}
	if gotOpts.RepoPath != dir {
		t.Errorf("ServeOptions.RepoPath = %q, want %q", gotOpts.RepoPath, dir)
	}
	if gotOpts.Store == nil {
		t.Error("ServeOptions.Store is nil")
	}
	if gotOpts.Bot == nil {
		t.Error("ServeOptions.Bot is nil")
	}
	// telegram.TelegramConfig has no Enabled flag (the cmd layer already
	// gated on config.Telegram().Enabled before reaching Serve). Verify the
	// other fields propagated from the saved config block instead.
	if gotOpts.Cfg.PullInterval != 30*time.Second {
		t.Errorf("ServeOptions.Cfg.PullInterval = %v, want 30s", gotOpts.Cfg.PullInterval)
	}
	if gotOpts.Cfg.BrowseLimit != 20 {
		t.Errorf("ServeOptions.Cfg.BrowseLimit = %d, want 20", gotOpts.Cfg.BrowseLimit)
	}
	if len(gotOpts.Cfg.AllowedUserIDs) != 1 || gotOpts.Cfg.AllowedUserIDs[0] != 42 {
		t.Errorf("ServeOptions.Cfg.AllowedUserIDs = %v, want [42]", gotOpts.Cfg.AllowedUserIDs)
	}
	// DateFormat must be the exact value config.DateFormat() returned at
	// startup so the bot's user-facing date rendering matches the laptop's.
	if want := config.DateFormat(); gotOpts.DateFormat != want {
		t.Errorf("ServeOptions.DateFormat = %q, want %q", gotOpts.DateFormat, want)
	}
	if gotOpts.Writer == nil {
		t.Error("ServeOptions.Writer is nil; expected cmd.ErrOrStderr() to be threaded through")
	}
}

func TestTelegramServeCommand_AcceptsFlagWhenEnvUnset(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableTelegramConfig(t, dir)

	// Env var explicitly unset (set to empty); only the flag carries the token.
	t.Setenv(telegramTokenEnvVar, "")

	var gotToken string
	stubTelegramClientFactory(t, &gotToken, nil)
	stubTelegramServe(t, nil, nil)
	stubTelegramSignalNotifyContextCancelled(t)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "serve", "-t", "flag-only"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("telegram serve error = %v\noutput: %s", err, buf.String())
	}

	if gotToken != "flag-only" {
		t.Errorf("client factory got token %q, want %q", gotToken, "flag-only")
	}
}

func TestTelegramServeCommand_PropagatesClientFactoryError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableTelegramConfig(t, dir)

	t.Setenv(telegramTokenEnvVar, "env-token")

	stubTelegramClientFactory(t, nil, errors.New("bot api unreachable"))
	stubTelegramSignalNotifyContextCancelled(t)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "serve"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error from client factory, got nil")
	}
	if !strings.Contains(err.Error(), "bot api unreachable") {
		t.Errorf("error should wrap factory error, got: %v", err)
	}
}

func TestTelegramServeCommand_PropagatesServeError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableTelegramConfig(t, dir)

	t.Setenv(telegramTokenEnvVar, "env-token")

	stubTelegramClientFactory(t, nil, nil)
	stubTelegramServe(t, nil, errors.New("serve crashed"))
	stubTelegramSignalNotifyContextCancelled(t)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "serve"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error from Serve, got nil")
	}
	if !strings.Contains(err.Error(), "serve crashed") {
		t.Errorf("error should propagate Serve error, got: %v", err)
	}
}

// --- telegram status ---

func TestTelegramStatusCommand_EnabledShowsFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	enableTelegramConfig(t, dir)

	t.Setenv(telegramTokenEnvVar, "secret-token")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "status"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("telegram status error = %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	want := []string{
		"enabled: true",
		"allowed_user_ids: [42]",
		"pull_interval: 30s",
		"browse_limit: 20",
		telegramTokenEnvVar + " (set)",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("expected %q in status output, got:\n%s", w, out)
		}
	}
	if strings.Contains(out, "secret-token") {
		t.Errorf("status output must NOT contain the token VALUE, got:\n%s", out)
	}
}

func TestTelegramStatusCommand_DisabledStillRenders(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)
	if err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	t.Setenv(telegramTokenEnvVar, "")

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "status"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("telegram status error = %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "enabled: false") {
		t.Errorf("expected 'enabled: false', got:\n%s", out)
	}
	if !strings.Contains(out, telegramTokenEnvVar+" (unset)") {
		t.Errorf("expected token env reported as (unset), got:\n%s", out)
	}
}

// --- registration sanity ---

func TestTelegramCmdHasSubcommands(t *testing.T) {
	root := NewRootCmd()
	var telegramCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Use == "telegram" {
			telegramCmd = c
			break
		}
	}
	if telegramCmd == nil {
		t.Fatal("'telegram' command not registered on root")
	}
	wantSubs := map[string]bool{"serve": false, "status": false}
	for _, sub := range telegramCmd.Commands() {
		if _, ok := wantSubs[sub.Use]; ok {
			wantSubs[sub.Use] = true
		}
	}
	for name, found := range wantSubs {
		if !found {
			t.Errorf("expected 'telegram %s' subcommand to be registered", name)
		}
	}
}

// TestTelegramStatusCommand_LoadsConfigFromDisk guards a regression where
// `telegram status` printed built-in defaults instead of the user's
// config.json. Only config.Load populates the config package's state, and it
// is reached solely through cmd helpers; status skips openStore, so it must
// call loadConfig itself or it reports enabled=false for a configured user.
//
// Ordering matters: SaveTelegram writes the file AND assigns telegramCfg as a
// side effect, so the in-memory state must be reset to defaults AFTER the
// save. Otherwise the command reads the value SaveTelegram left behind and
// the test passes whether or not it ever touches disk.
func TestTelegramStatusCommand_LoadsConfigFromDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "monolog")
	initTestRepo(t, dir)

	reset := filepath.Join(t.TempDir(), "reset")
	initTestRepo(t, reset) // a repo whose config.json has no telegram block

	if err := config.SaveTelegram(dir, config.TelegramConfig{
		Enabled:        true,
		AllowedUserIDs: []int64{99},
		PullInterval:   45 * time.Second,
		BrowseLimit:    7,
	}); err != nil {
		t.Fatalf("SaveTelegram: %v", err)
	}

	// Drop the in-memory state back to defaults, then point the command at
	// the configured repo. Only a disk read can now produce the values.
	if err := config.Load(reset); err != nil {
		t.Fatalf("config.Load(reset): %v", err)
	}
	if config.Telegram().Enabled {
		t.Fatal("precondition: in-memory telegram config should be disabled after reset")
	}
	t.Setenv("MONOLOG_DIR", dir)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"telegram", "status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("telegram status error = %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	for _, w := range []string{"enabled: true", "allowed_user_ids: [99]", "pull_interval: 45s", "browse_limit: 7"} {
		if !strings.Contains(out, w) {
			t.Errorf("expected %q in status output (config.json not loaded?), got:\n%s", w, out)
		}
	}
}
