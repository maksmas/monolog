package telegram

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maksmas/monolog/internal/git"
	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/store"
)

// twoCloneFixture builds the real deployment topology in miniature: one bare
// remote, a "laptop" clone where a mutation commits and auto-pushes, and a
// "bot" clone where the Telegram handler serves commands. Both clones drive the
// real git binary, so the push and the pull in between are production code
// paths rather than seams.
func twoCloneFixture(t *testing.T) (laptop, botRepo string, botStore *store.Store) {
	t.Helper()
	dir := t.TempDir()

	bare := filepath.Join(dir, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	laptop = filepath.Join(dir, "laptop")
	if err := git.Init(laptop, bare); err != nil {
		t.Fatalf("git.Init(laptop): %v", err)
	}

	// Point the bare repo's HEAD at the branch Init just pushed, so the bot's
	// `git clone` checks it out instead of landing on an unborn branch.
	branchOut, err := exec.Command("git", "-C", laptop, "branch", "--show-current").Output()
	if err != nil {
		t.Fatalf("branch --show-current: %v", err)
	}
	branch := strings.TrimSpace(string(branchOut))
	if out, symErr := exec.Command("git", "-C", bare, "symbolic-ref", "HEAD", "refs/heads/"+branch).CombinedOutput(); symErr != nil {
		t.Fatalf("symbolic-ref: %v\n%s", symErr, out)
	}

	botRepo = filepath.Join(dir, "bot")
	if out, cloneErr := exec.Command("git", "clone", bare, botRepo).CombinedOutput(); cloneErr != nil {
		t.Fatalf("git clone bot: %v\n%s", cloneErr, out)
	}

	botStore, err = store.New(filepath.Join(botRepo, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("store.New(bot): %v", err)
	}
	return laptop, botRepo, botStore
}

// mutateAndAutoPush reproduces exactly what a TUI mutation does on the laptop:
// write the task through the store, commit it with AutoCommitSHA, then hand the
// commit to git.AutoPush. The TUI's own wiring from taskSavedMsg to
// runAutoPush is pinned separately in internal/tui; this is the git half.
func mutateAndAutoPush(t *testing.T, repoPath, taskID, title string) {
	t.Helper()
	s, err := store.New(filepath.Join(repoPath, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("store.New(laptop): %v", err)
	}
	rfc := handlerTestNow.Format(time.RFC3339)
	if createErr := s.Create(model.Task{
		ID:        taskID,
		Title:     title,
		Status:    "open",
		Position:  1000,
		Schedule:  handlerTestNow.Format("2006-01-02"),
		CreatedAt: rfc,
		UpdatedAt: rfc,
	}); createErr != nil {
		t.Fatalf("laptop store.Create: %v", createErr)
	}
	if _, commitErr := git.AutoCommitSHA(repoPath, "add: "+title, taskRelPath(taskID)); commitErr != nil {
		t.Fatalf("laptop AutoCommitSHA: %v", commitErr)
	}
	res, err := git.AutoPush(repoPath, git.DefaultPushTimeout)
	if err != nil {
		t.Fatalf("laptop AutoPush: %v", err)
	}
	if !res.Pushed {
		t.Fatalf("laptop AutoPush did not push: %+v", res)
	}
}

// TestEndToEnd_LaptopMutationReachesBotBrowseWithoutTheTicker is the plan's
// end-to-end latency claim, proved rather than reasoned about: a task created
// on the laptop is visible to a Telegram browse command without any pull-ticker
// tick ever firing.
//
// Every link runs for real — store write, AutoCommitSHA, git.AutoPush over a
// bare remote, the handler's freshness-gated pull (the production
// git.PullRebase, restored over this package's stub pullFunc), and the browse
// reply built from the bot clone's store. Serve is deliberately never called,
// so runPullTicker does not exist in this test: anything the bot sees arrived
// through the per-command gate alone.
//
// The middle assertion pins the honest bound. Inside commandPullMaxAge the gate
// deliberately serves local state, so the worst-case visibility delay is 5s
// (the gate), not cfg.PullInterval (30s, the ticker) and not "never" (the bug
// this plan fixes).
func TestEndToEnd_LaptopMutationReachesBotBrowseWithoutTheTicker(t *testing.T) {
	// The real pull, not the package-wide test stub: the point of this test is
	// that a genuine `git pull --rebase` lands the laptop's pushed commit.
	withPullFunc(t, git.PullRebase)

	laptop, botRepo, botStore := twoCloneFixture(t)

	clk := newMutableClock(handlerTestNow)
	bot := &fakeBot{}
	h := NewHandler(bot, botStore, botRepo, TelegramConfig{
		AllowedUserIDs: []int64{100},
		// Long enough that a tick could not fire even if a ticker existed —
		// and no ticker exists, because Serve is never called.
		PullInterval: time.Hour,
		BrowseLimit:  20,
	}, "02-01-2006", clk.now)

	browse := func() string {
		t.Helper()
		bot.sent = nil
		if err := h.Handle(context.Background(), Update{
			UpdateID: 1,
			Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
		}); err != nil {
			t.Fatalf("Handle /today: %v", err)
		}
		var b strings.Builder
		for _, m := range bot.sent {
			b.WriteString(m.HTML)
			b.WriteString("\n")
		}
		return b.String()
	}

	const taskID = "01ARZ3NDEKTSV4RRFFQ69G5FB1"
	const title = "filed on the laptop"

	// 1. Cold gate: the bot pulls for real and finds an empty backlog.
	if got := browse(); !strings.Contains(got, "nothing") {
		t.Fatalf("expected an empty-bucket reply before the laptop files anything, got %q", got)
	}

	// 2. The laptop files a task and auto-pushes it to the shared remote.
	mutateAndAutoPush(t, laptop, taskID, title)

	// 3. Still inside commandPullMaxAge: the gate serves local state on
	//    purpose, so the task is not visible yet. This is the documented
	//    trade-off that keeps a burst of taps to one fetch.
	if got := browse(); strings.Contains(got, title) {
		t.Fatalf("gate should have served local state inside commandPullMaxAge, got %q", got)
	}

	// 4. Past the gate, the next command pulls and the task appears — with no
	//    ticker involved anywhere in this test.
	clk.advance(commandPullMaxAge + time.Second)
	got := browse()
	if !strings.Contains(got, title) {
		t.Fatalf("expected the laptop's auto-pushed task in the browse reply, got %q", got)
	}

	// The task really is on the bot's disk, not merely in a rendered string.
	if _, err := botStore.Get(taskID); err != nil {
		t.Fatalf("task %s should exist in the bot clone's store after the pull: %v", taskID, err)
	}
}

// TestEndToEnd_BotSeesLaptopTaskOnFirstCommandAfterStartup covers the other
// real-world shape: the bot process starts, the laptop files something, and the
// user's very first command must show it. lastPull is zero on a fresh Handler,
// so the gate treats it as stale and fetches immediately — no ticker wait.
func TestEndToEnd_BotSeesLaptopTaskOnFirstCommandAfterStartup(t *testing.T) {
	withPullFunc(t, git.PullRebase)

	laptop, botRepo, botStore := twoCloneFixture(t)

	const taskID = "01ARZ3NDEKTSV4RRFFQ69G5FB2"
	const title = "captured while the bot was idle"
	mutateAndAutoPush(t, laptop, taskID, title)

	bot := &fakeBot{}
	h := NewHandler(bot, botStore, botRepo, TelegramConfig{
		AllowedUserIDs: []int64{100},
		PullInterval:   time.Hour,
		BrowseLimit:    20,
	}, "02-01-2006", func() time.Time { return handlerTestNow })

	if err := h.Handle(context.Background(), Update{
		UpdateID: 1,
		Message:  &Message{ChatID: 5, UserID: 100, Text: "/today"},
	}); err != nil {
		t.Fatalf("Handle /today: %v", err)
	}

	var b strings.Builder
	for _, m := range bot.sent {
		b.WriteString(m.HTML)
		b.WriteString("\n")
	}
	if !strings.Contains(b.String(), title) {
		t.Fatalf("the first command after startup must show the laptop's task, got %q", b.String())
	}
}
