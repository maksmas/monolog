package email

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gmailapi "google.golang.org/api/gmail/v1"
)

// gmailScope is the smallest OAuth scope that allows listing messages by
// label, reading metadata + snippet, and removing the INBOX label (archive).
// We deliberately avoid the broader gmail scopes (no send, no full-mailbox).
const gmailScope = gmailapi.GmailModifyScope

// authMissingHint is the user-facing error suffix surfaced whenever a token
// is missing or unrecoverable. The wording matches the CLI command name.
const authMissingHint = "run monolog email auth"

// LoadToken reads an OAuth2 token previously persisted by SaveToken. A
// missing file is reported with a wrapped error containing the user-facing
// hint to run `monolog email auth`.
func LoadToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("email: token not found at %s — %s", path, authMissingHint)
		}
		return nil, fmt.Errorf("email: read token %s: %w", path, err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("email: parse token %s: %w", path, err)
	}
	return &tok, nil
}

// SaveToken writes an OAuth2 token as 0600 JSON, creating the parent
// directory (mode 0700) if missing. Used both by the initial Authorize flow
// and by the auto-refresh wrapper that persists refreshed tokens back to
// disk so subsequent processes don't have to re-authorize.
func SaveToken(path string, tok *oauth2.Token) error {
	if tok == nil {
		return fmt.Errorf("email: nil token")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("email: mkdir token dir: %w", err)
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("email: marshal token: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("email: write token %s: %w", path, err)
	}
	return nil
}

// loadOAuthConfig reads a Google OAuth client-secrets JSON (Desktop app
// type) and returns an *oauth2.Config configured for the gmail.modify scope.
func loadOAuthConfig(clientSecretsPath string) (*oauth2.Config, error) {
	data, err := os.ReadFile(clientSecretsPath)
	if err != nil {
		return nil, fmt.Errorf("email: read client secrets %s: %w", clientSecretsPath, err)
	}
	cfg, err := google.ConfigFromJSON(data, gmailScope)
	if err != nil {
		return nil, fmt.Errorf("email: parse client secrets: %w", err)
	}
	return cfg, nil
}

// browserOpener is a swappable command-runner used by Authorize so the
// browser-launch step (the only side-effect we cannot meaningfully unit test
// without a real browser) doesn't get in the way of test runs that exercise
// surrounding logic.
var browserOpener = openBrowser

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// Authorize runs the local-redirect OAuth flow:
//
//  1. Read the client secrets JSON (Desktop-app credentials downloaded from
//     Google Cloud Console).
//  2. Listen on 127.0.0.1:<random-port> for the redirect callback.
//  3. Open the user's browser to the consent URL; if browser-launch fails,
//     print the URL so the user can copy/paste it.
//  4. Exchange the auth code for a token and persist it to tokenPath.
//
// The flow is interactive and exercised manually in Task 12 — it has no
// unit test of its own, but its component pieces (LoadToken, SaveToken,
// the refresh-persistence wrapper) are tested.
func Authorize(ctx context.Context, clientSecretsPath, tokenPath string) error {
	cfg, err := loadOAuthConfig(clientSecretsPath)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("email: open callback listener: %w", err)
	}
	defer listener.Close()
	cfg.RedirectURL = fmt.Sprintf("http://%s/callback", listener.Addr().String())

	// CSRF protection — require the state value to come back unchanged.
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return fmt.Errorf("email: rand state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)

	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	type result struct {
		code string
		err  error
	}
	resultCh := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("state"); got != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			resultCh <- result{err: fmt.Errorf("state mismatch")}
			return
		}
		if e := q.Get("error"); e != "" {
			http.Error(w, e, http.StatusBadRequest)
			resultCh <- result{err: fmt.Errorf("oauth error: %s", e)}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			resultCh <- result{err: fmt.Errorf("missing code")}
			return
		}
		_, _ = w.Write([]byte("<html><body><h2>monolog: authorization complete — you can close this tab.</h2></body></html>"))
		resultCh <- result{code: code}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := browserOpener(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "monolog: open this URL in your browser:\n%s\n", authURL)
	} else {
		fmt.Fprintf(os.Stderr, "monolog: opening browser for OAuth consent…\nIf nothing happens, visit:\n%s\n", authURL)
	}

	var code string
	select {
	case <-ctx.Done():
		return fmt.Errorf("email: authorize cancelled: %w", ctx.Err())
	case res := <-resultCh:
		if res.err != nil {
			return fmt.Errorf("email: authorize: %w", res.err)
		}
		code = res.code
	}

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("email: exchange code: %w", err)
	}
	if err := SaveToken(tokenPath, tok); err != nil {
		return err
	}
	return nil
}

// persistingTokenSource is a TokenSource decorator that writes refreshed
// tokens back to disk so subsequent processes pick up the new access token
// without re-authorizing. It compares AccessToken values (cheap and
// definitive) to detect whether the underlying source rotated the token.
type persistingTokenSource struct {
	base oauth2.TokenSource
	path string

	mu      sync.Mutex
	lastTok *oauth2.Token
}

func newPersistingTokenSource(base oauth2.TokenSource, path string, initial *oauth2.Token) *persistingTokenSource {
	return &persistingTokenSource{base: base, path: path, lastTok: initial}
}

func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := p.base.Token()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastTok == nil || tok.AccessToken != p.lastTok.AccessToken {
		// Best-effort persist; surface the error but still return the
		// fresh token so the in-flight request can succeed even if disk
		// is wedged. The next call will retry the write.
		if saveErr := SaveToken(p.path, tok); saveErr != nil {
			return tok, fmt.Errorf("email: persist refreshed token: %w", saveErr)
		}
		p.lastTok = tok
	}
	return tok, nil
}

// HTTPClient returns an *http.Client that authenticates Gmail API requests
// using the persisted token, refreshing it transparently when it expires
// and writing the refreshed value back to tokenPath.
func HTTPClient(ctx context.Context, clientSecretsPath, tokenPath string) (*http.Client, error) {
	cfg, err := loadOAuthConfig(clientSecretsPath)
	if err != nil {
		return nil, err
	}
	tok, err := LoadToken(tokenPath)
	if err != nil {
		return nil, err
	}
	base := cfg.TokenSource(ctx, tok)
	wrapped := newPersistingTokenSource(base, tokenPath, tok)
	return oauth2.NewClient(ctx, wrapped), nil
}

// IsAuthMissing reports whether an error from LoadToken/HTTPClient is the
// "no token on disk" case. cmd/email status uses this to render a friendly
// message rather than the wrapped error string.
func IsAuthMissing(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), authMissingHint)
}
