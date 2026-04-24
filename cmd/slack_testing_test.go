package cmd

import (
	"testing"

	"github.com/mmaksmas/monolog/internal/slack"
)

// installSlackClient overrides the shared newSlackClientFn factory so CLI
// Slack paths hit the test httptest server at serverURL instead of live
// Slack. Returns a restore closure callers defer to undo the override.
//
// Shared across slack_sync_test.go and task_commands_test.go (done). Not
// used by slack_login_test.go: that file also overrides openBrowserFn and
// readTokenFn alongside the client factory, so it keeps its own combined
// stub helper.
func installSlackClient(t *testing.T, serverURL string) func() {
	t.Helper()
	orig := newSlackClientFn
	newSlackClientFn = func(token, workspace string) *slack.Client {
		return &slack.Client{Token: token, Workspace: workspace, BaseURL: serverURL}
	}
	return func() { newSlackClientFn = orig }
}
