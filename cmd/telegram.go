package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/telegram"
)

// telegramTokenEnvVar names the environment variable holding the bot token.
// Centralizing the name lets tests reference it without relying on a magic
// string and matches the documented systemd EnvironmentFile contract.
const telegramTokenEnvVar = "MONOLOG_TELEGRAM_TOKEN"

// telegramClientFactory builds a telegram.Bot from a bot token. Tests swap
// this seam with a fake constructor so `monolog telegram serve` can be
// exercised without touching the Telegram API. Mirrors the
// emailClientFactory pattern in cmd/email.go.
//
// The default value points directly at telegram.NewClient — that function
// already performs the empty-token guard and constructs the tgbotapi-backed
// *realBot, so the wrapper indirection adds no value.
var telegramClientFactory = telegram.NewClient

// telegramServeFunc is the swappable handle for telegram.Serve. Tests stub
// this so the cobra wiring can be exercised without spawning the long-poll
// loop (which would otherwise block until the test ctx expires).
var telegramServeFunc = telegram.Serve

// telegramSignalNotifyContext is the swappable wrapper around
// signal.NotifyContext. Tests can override this to inject a pre-cancelled
// context so the serve command exits immediately without delivering a real
// signal.
var telegramSignalNotifyContext = func(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
}

func newTelegramCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Telegram bot integration: long-poll for capture and browse from mobile",
		Long: "Run a long-running Telegram bot that lets the configured Telegram users capture, browse, and complete tasks from their phone. " +
			"The bot is intended to run on an always-on host (e.g. an EC2 t4g.nano) holding its own clone of the tasks git repo. " +
			"Configure via the 'telegram' block in <MONOLOG_DIR>/.monolog/config.json (see docs/plans/completed/20260518-telegram-bot.md).",
	}

	cmd.AddCommand(newTelegramServeCmd())
	cmd.AddCommand(newTelegramStatusCmd())
	return cmd
}

// newTelegramServeCmd implements `monolog telegram serve` — open the store,
// resolve the bot token, install a SIGINT/SIGTERM handler, and run the
// long-polling Serve loop until the signal is delivered.
//
// Token precedence: the --token flag wins when non-empty; otherwise the
// MONOLOG_TELEGRAM_TOKEN environment variable is used. We deliberately
// document the env-var path as the recommended deployment knob because
// values passed via --token are visible to anyone with `ps aux` on the
// host — fine for ad-hoc local runs, problematic for a shared system.
func newTelegramServeCmd() *cobra.Command {
	var tokenFlag string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Telegram bot long-poll loop",
		Long: "Run the Telegram bot long-poll loop. The token is read from --token if set; " +
			"otherwise the MONOLOG_TELEGRAM_TOKEN environment variable is used.\n\n" +
			"For systemd / long-running deployments, prefer the environment variable (e.g. via EnvironmentFile=) " +
			"because '--token <value>' is visible in 'ps aux'. The flag form is intended for ad-hoc local runs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, repoPath, err := openStore()
			if err != nil {
				return err
			}
			tc := config.Telegram()
			if !tc.Enabled {
				return fmt.Errorf("telegram integration is disabled — edit config.json to enable")
			}

			token := tokenFlag
			if token == "" {
				token = os.Getenv(telegramTokenEnvVar)
			}
			if token == "" {
				return fmt.Errorf("telegram token required: pass --token or set %s", telegramTokenEnvVar)
			}

			bot, err := telegramClientFactory(token)
			if err != nil {
				return err
			}

			ctx, stop := telegramSignalNotifyContext(cmd.Context())
			defer stop()

			return telegramServeFunc(ctx, telegram.ServeOptions{
				RepoPath:   repoPath,
				Bot:        bot,
				Store:      s,
				Cfg:        toTelegramPackageConfig(tc),
				DateFormat: config.DateFormat(),
				Writer:     cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVarP(&tokenFlag, "token", "t", "", "Telegram bot token (overrides "+telegramTokenEnvVar+"); prefer the env var for systemd")
	return cmd
}

// newTelegramStatusCmd implements `monolog telegram status` — print a
// summary of the configured options plus whether the bot token env var is
// set. The token VALUE is never printed (status output is safe to share /
// log; the secret stays in the environment).
func newTelegramStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Telegram bot integration status",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Mirrors `monolog email status`: load config without going
			// through openStore. Status is a read-only "print what you
			// would observe" command — we don't need the store handle
			// openStore builds, but we DO need loadConfig, because the
			// config package reports built-in defaults until Load runs.
			loadConfig()
			tc := config.Telegram()
			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "enabled: %t\n", tc.Enabled)
			fmt.Fprintf(out, "allowed_user_ids: %v\n", tc.AllowedUserIDs)
			fmt.Fprintf(out, "pull_interval: %s\n", tc.PullInterval)
			fmt.Fprintf(out, "browse_limit: %d\n", tc.BrowseLimit)

			tokenLabel := "unset"
			if os.Getenv(telegramTokenEnvVar) != "" {
				tokenLabel = "set"
			}
			fmt.Fprintf(out, "token_env: %s (%s)\n", telegramTokenEnvVar, tokenLabel)
			return nil
		},
	}
}

// toTelegramPackageConfig translates the user-facing config.TelegramConfig
// (which carries the `Enabled` knob the cmd layer consults) into the
// runtime-only telegram.TelegramConfig the Serve loop expects (which omits
// `Enabled` because by the time Serve runs the feature is unambiguously on).
//
// Keeping the two types separate preserves the contract documented in
// CLAUDE.md: internal/telegram MUST NOT import internal/config; values flow
// through the cmd layer by value.
func toTelegramPackageConfig(tc config.TelegramConfig) telegram.TelegramConfig {
	return telegram.TelegramConfig{
		AllowedUserIDs: tc.AllowedUserIDs,
		PullInterval:   tc.PullInterval,
		BrowseLimit:    tc.BrowseLimit,
	}
}
