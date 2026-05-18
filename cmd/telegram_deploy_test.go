package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDeployArtifactsExist is a low-cost sanity check that the deployment
// artifacts referenced from docs/deploy/README.md and the project plan are
// actually checked in. If someone renames or moves one of these files the
// documentation breaks silently — this test surfaces that quickly.
//
// We resolve the repo root from this test file's own path (runtime.Caller)
// so the test works regardless of where `go test` is invoked from.
func TestDeployArtifactsExist(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate test file path")
	}
	// thisFile is .../monolog/cmd/telegram_deploy_test.go — repo root is two
	// levels up.
	repoRoot := filepath.Dir(filepath.Dir(thisFile))

	want := []string{
		"Makefile",
		filepath.Join("docs", "deploy", "monolog-bot.service"),
		filepath.Join("docs", "deploy", "env.example"),
		filepath.Join("docs", "deploy", "README.md"),
	}

	for _, rel := range want {
		full := filepath.Join(repoRoot, rel)
		info, err := os.Stat(full)
		if err != nil {
			t.Errorf("deploy artifact %q missing: %v", rel, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("deploy artifact %q is a directory, expected a file", rel)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("deploy artifact %q is empty", rel)
		}
	}
}
