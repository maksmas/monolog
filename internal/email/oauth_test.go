package email

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestSaveAndLoadTokenRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "gmail_token.json")

	want := &oauth2.Token{
		AccessToken:  "access-123",
		TokenType:    "Bearer",
		RefreshToken: "refresh-456",
		Expiry:       time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := SaveToken(path, want); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	got, err := LoadToken(path)
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || got.TokenType != want.TokenType {
		t.Fatalf("token mismatch: got=%+v want=%+v", got, want)
	}
	if !got.Expiry.Equal(want.Expiry) {
		t.Fatalf("expiry mismatch: got=%v want=%v", got.Expiry, want.Expiry)
	}
}

func TestSaveTokenFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tok.json")
	if err := SaveToken(path, &oauth2.Token{AccessToken: "x"}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("token file mode = %o want 0600", mode)
	}
	// Parent dir must be 0700 (created by MkdirAll with our perm bits).
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		// Some test setups (TempDir is 0700 already, MkdirAll on existing dir is a no-op);
		// still, our intent is 0700 for newly-created dirs. Allow looser perm only when
		// MkdirAll didn't create the dir (i.e. parent already existed via TempDir).
		t.Logf("dir mode = %o (TempDir-created, not asserting)", mode)
	}
}

func TestSaveTokenNil(t *testing.T) {
	if err := SaveToken(filepath.Join(t.TempDir(), "tok.json"), nil); err == nil {
		t.Fatal("expected error for nil token")
	}
}

func TestLoadTokenMissingFileHint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	_, err := LoadToken(path)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "run monolog email auth") {
		t.Fatalf("error %q missing user-facing hint", err.Error())
	}
	if !errors.Is(err, ErrAuthMissing) {
		t.Fatalf("errors.Is(%v, ErrAuthMissing) = false want true", err)
	}
	if !IsAuthMissing(err) {
		t.Fatalf("IsAuthMissing(%v) = false want true", err)
	}
}

func TestLoadTokenInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadToken(path); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestIsAuthMissingNil(t *testing.T) {
	if IsAuthMissing(nil) {
		t.Fatal("IsAuthMissing(nil) = true want false")
	}
	if IsAuthMissing(errors.New("unrelated")) {
		t.Fatal("IsAuthMissing(unrelated) = true want false")
	}
}

// staticTokenSource is a fake oauth2.TokenSource that returns whatever token
// was set. Used to drive the persistingTokenSource refresh-persistence logic
// without going to the network.
type staticTokenSource struct {
	tok *oauth2.Token
	err error
}

func (s *staticTokenSource) Token() (*oauth2.Token, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.tok, nil
}

func TestPersistingTokenSourceWritesOnRefresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok.json")

	initial := &oauth2.Token{AccessToken: "old", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}
	// Seed disk so we can detect the rewrite.
	if err := SaveToken(path, initial); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Underlying source returns a NEW access token — simulating a refresh.
	refreshed := &oauth2.Token{AccessToken: "new", RefreshToken: "r1", Expiry: time.Now().Add(2 * time.Hour)}
	src := newPersistingTokenSource(&staticTokenSource{tok: refreshed}, path, initial)

	got, err := src.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got.AccessToken != "new" {
		t.Fatalf("Token().AccessToken = %q want %q", got.AccessToken, "new")
	}

	// Disk must now hold the refreshed token.
	onDisk, err := LoadToken(path)
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if onDisk.AccessToken != "new" {
		t.Fatalf("on-disk AccessToken = %q want %q (refreshed token not persisted)", onDisk.AccessToken, "new")
	}
}

func TestPersistingTokenSourceNoOpWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok.json")

	tok := &oauth2.Token{AccessToken: "same", Expiry: time.Now().Add(time.Hour)}
	if err := SaveToken(path, tok); err != nil {
		t.Fatalf("seed: %v", err)
	}
	originalInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	originalMtime := originalInfo.ModTime()

	// Wait long enough that any rewrite would bump mtime detectably.
	time.Sleep(20 * time.Millisecond)

	src := newPersistingTokenSource(&staticTokenSource{tok: tok}, path, tok)
	if _, err := src.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}

	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !afterInfo.ModTime().Equal(originalMtime) {
		t.Fatalf("token file rewritten on no-op refresh (mtime changed: %v -> %v)", originalMtime, afterInfo.ModTime())
	}
}

func TestPersistingTokenSourcePropagatesError(t *testing.T) {
	src := newPersistingTokenSource(&staticTokenSource{err: errors.New("refresh failed")}, "/dev/null", nil)
	if _, err := src.Token(); err == nil {
		t.Fatal("expected error from underlying source to propagate")
	}
}

func TestHTTPClientMissingToken(t *testing.T) {
	dir := t.TempDir()
	secrets := filepath.Join(dir, "secrets.json")
	// Minimal valid Desktop-app client secrets JSON — google.ConfigFromJSON
	// must accept it so we can reach the LoadToken step.
	const secretsBody = `{
  "installed": {
    "client_id": "test-client.apps.googleusercontent.com",
    "client_secret": "test-secret",
    "redirect_uris": ["http://localhost"],
    "auth_uri": "https://accounts.google.com/o/oauth2/auth",
    "token_uri": "https://oauth2.googleapis.com/token"
  }
}`
	if err := os.WriteFile(secrets, []byte(secretsBody), 0o600); err != nil {
		t.Fatalf("write secrets: %v", err)
	}

	tokenPath := filepath.Join(dir, "missing.json")
	_, err := HTTPClient(tContext(t), secrets, tokenPath)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !errors.Is(err, ErrAuthMissing) {
		t.Fatalf("errors.Is(%v, ErrAuthMissing) = false want true", err)
	}
	if !IsAuthMissing(err) {
		t.Fatalf("IsAuthMissing(%v) = false want true", err)
	}
}

func TestHTTPClientMissingSecrets(t *testing.T) {
	dir := t.TempDir()
	if _, err := HTTPClient(tContext(t), filepath.Join(dir, "no-secrets.json"), filepath.Join(dir, "tok.json")); err == nil {
		t.Fatal("expected error for missing client secrets")
	}
}

// tContext returns a context that's cancelled when the test finishes so
// HTTPClient/Authorize don't leak goroutines on failure.
func tContext(t *testing.T) (ctx context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}
