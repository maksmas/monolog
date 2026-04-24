package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/config"
	"github.com/mmaksmas/monolog/internal/git"
	"github.com/mmaksmas/monolog/internal/slack"
	"github.com/spf13/cobra"
)

// slackAppURL is the Slack app-creation page opened by the wizard.
const slackAppURL = "https://api.slack.com/apps?new_app=1"

// openBrowser opens the given URL in the user's default browser. Overridden
// by tests via openBrowserFn to avoid spawning an actual browser. Best-effort:
// failures are logged to the caller-provided writer but never fatal.
var openBrowserFn = defaultOpenBrowser

// readToken reads the user-pasted OAuth token from the given reader. Split
// into a package-level var so tests can substitute a canned input without
// needing to touch stdin.
var readTokenFn = defaultReadToken

// newSlackClientFn constructs a Slack client for any CLI Slack path
// (slack-login, slack-sync, done-triggered unsave). Consolidates what used to
// be three near-identical factory vars. Tests replace this to point at an
// httptest server. workspace is used for permalink reconstruction and is
// unused by login (which hasn't discovered the workspace yet).
var newSlackClientFn = func(token, workspace string) *slack.Client {
	return &slack.Client{Token: token, Workspace: workspace}
}

// defaultOpenBrowser dispatches to the platform-appropriate URL opener. Any
// error is returned unchanged; the wizard just prints the URL on failure.
func defaultOpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform %q", runtime.GOOS)
	}
	return cmd.Start()
}

// defaultReadToken reads a single line from r, trims whitespace, and returns
// the token. Echoes to the terminal — we deliberately do not pull in
// golang.org/x/term to keep the dependency footprint small.
func defaultReadToken(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	// Slack tokens are ~60 chars but other identity providers can grow;
	// bump the buffer to comfortably handle anything a user might paste.
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", nil
	}
	return strings.TrimSpace(scanner.Text()), nil
}

func newSlackLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slack-login",
		Short: "Connect monolog to a Slack workspace",
		Long: `Interactive wizard that connects monolog to a Slack workspace.

The wizard walks through creating a Slack app, installing it to the
workspace, and pasting the resulting User OAuth Token. The token is stored
under $MONOLOG_DIR/.monolog/slack_token (git-ignored) with mode 0600.`,
		Args: cobra.NoArgs,
		RunE: runSlackLogin,
	}
	return cmd
}

// runSlackLogin executes the login wizard. Split out from newSlackLoginCmd so
// tests can exercise it directly without cobra plumbing.
func runSlackLogin(cmd *cobra.Command, args []string) error {
	repoPath := monologDir()
	// Load so SlackToken() / Slack() see file defaults, though login itself
	// does not read them.
	_ = config.Load(repoPath)

	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	fmt.Fprintln(out, "monolog Slack connection wizard")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  1. Create a new Slack app at:")
	fmt.Fprintln(out, "       "+slackAppURL)
	fmt.Fprintln(out, "  2. Under OAuth & Permissions, add these User Token Scopes:")
	fmt.Fprintln(out, "       stars:read  stars:write")
	fmt.Fprintln(out, "  3. Install the app to your workspace.")
	fmt.Fprintln(out, "  4. Copy the User OAuth Token (starts with xoxp-).")
	fmt.Fprintln(out)

	// Best-effort browser launch. Print the URL again on failure so the user
	// can always copy-paste manually.
	if err := openBrowserFn(slackAppURL); err != nil {
		fmt.Fprintf(errOut, "(could not open browser automatically: %v)\n", err)
		fmt.Fprintln(out, "Open this URL in your browser: "+slackAppURL)
	}

	fmt.Fprintln(out, "(token will be visible in this terminal — close scrollback afterward if that's a concern)")
	fmt.Fprint(out, "Paste User OAuth Token (xoxp-...): ")

	token, err := readTokenFn(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("read token: %w", err)
	}
	if token == "" {
		return fmt.Errorf("no token provided")
	}

	// Validate the token against Slack before we write anything to disk.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := newSlackClientFn(token, "")
	workspace, err := client.AuthTest(ctx)
	if err != nil {
		return fmt.Errorf("verify token: %w", err)
	}
	if workspace == "" {
		return fmt.Errorf("Slack did not return a workspace subdomain — cannot connect")
	}

	// Persist the token under $MONOLOG_DIR/.monolog/slack_token (0600).
	tokenPath := filepath.Join(repoPath, ".monolog", "slack_token")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o755); err != nil {
		return fmt.Errorf("create .monolog dir: %w", err)
	}
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}

	// Upgrade older repos that pre-date the Slack integration's gitignore
	// addition. A repo initialized by the current `init` already has the
	// entry; EnsureGitignoreEntry is a no-op in that case.
	if err := git.EnsureGitignoreEntry(repoPath, "slack_token"); err != nil {
		return fmt.Errorf("update .gitignore: %w", err)
	}

	// Persist workspace + enabled=true in a single write rather than two
	// separate SetSlack* calls (each doing its own read-modify-write).
	if err := config.SetSlackConnection(repoPath, workspace, true); err != nil {
		return fmt.Errorf("save slack connection: %w", err)
	}

	fmt.Fprintf(out, "Connected to %s.slack.com. Polling will start next time you open the TUI.\n", workspace)
	return nil
}
