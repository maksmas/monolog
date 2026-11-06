package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/maksmas/monolog/internal/config"
	"github.com/maksmas/monolog/internal/git"
	"github.com/maksmas/monolog/internal/launch"
	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/store"
)

// Full-length ULIDs so the prompt assertions cover the real ID shape.
const (
	launchIDA = "01JAAAAAAAAAAAAAAAAAAAAAAA"
	launchIDB = "01JBBBBBBBBBBBBBBBBBBBBBBB"
)

// launchRecorder records calls made through the runLaunch seam. Cmds run on
// the test goroutine, so it needs no locking.
type launchRecorder struct {
	calls     int
	argvs     [][]string
	dirs      []string
	deadlines []time.Duration // time left on ctx at call time; -1 if none
	err       error
}

func (r *launchRecorder) record(ctx context.Context, argv []string, dir string) error {
	r.calls++
	r.argvs = append(r.argvs, argv)
	r.dirs = append(r.dirs, dir)
	left := time.Duration(-1)
	if dl, ok := ctx.Deadline(); ok {
		left = time.Until(dl)
	}
	r.deadlines = append(r.deadlines, left)
	return r.err
}

func stubLaunch(t *testing.T, err error) *launchRecorder {
	t.Helper()
	rec := &launchRecorder{err: err}
	prev := runLaunch
	runLaunch = rec.record
	t.Cleanup(func() { runLaunch = prev })
	return rec
}

// testLaunchCfg maps the "proj" tag to dir with no default_dir.
func testLaunchCfg(dir string) config.LaunchConfig {
	return config.LaunchConfig{
		Command: []string{"launcher", "--cwd={dir}", "{prompt}"},
		Dirs:    map[string]string{"proj": dir},
	}
}

func commitCount(t *testing.T, repoPath string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", repoPath, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-list: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse commit count %q: %v", out, err)
	}
	return n
}

func mustGet(t *testing.T, m *Model, id string) model.Task {
	t.Helper()
	got, err := m.store.Get(id)
	if err != nil {
		t.Fatalf("store.Get(%s): %v", id, err)
	}
	return got
}

func TestLaunch_ActivatesInactiveTaskAndMovesToToday(t *testing.T) {
	rec := stubLaunch(t, nil)
	dir := t.TempDir()
	body := "monolog/internal/store/store.go:142\nsays \"hi\" `$(rm -rf ~)`"
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "fix the thing", Body: body, Status: "open", Schedule: "week",
			Position: 1000, Tags: []string{"claude", "proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)
	m, _ = key(t, m, "3") // Week tab
	before := commitCount(t, m.repoPath)
	undoBefore := len(m.undoStack)

	m, cmd := key(t, m, "o")
	if cmd == nil {
		t.Fatal("o returned nil cmd")
	}
	m = runCmd(t, m, cmd)

	if rec.calls != 1 {
		t.Fatalf("runLaunch calls = %d, want 1", rec.calls)
	}
	if rec.dirs[0] != dir {
		t.Errorf("dir = %q, want %q", rec.dirs[0], dir)
	}
	if left := rec.deadlines[0]; left <= 0 || left > launchTimeout {
		t.Errorf("launcher ctx time left = %v, want a deadline within %v", left, launchTimeout)
	}
	prompt := launch.BuildPrompt(launchIDA, "fix the thing", body)
	wantArgv := []string{"launcher", "--cwd=" + dir, prompt}
	if !reflect.DeepEqual(rec.argvs[0], wantArgv) {
		t.Errorf("argv = %q, want %q", rec.argvs[0], wantArgv)
	}
	for _, want := range []string{launchIDA, "fix the thing", body} {
		if !strings.Contains(rec.argvs[0][2], want) {
			t.Errorf("prompt element missing %q: %q", want, rec.argvs[0][2])
		}
	}

	got := mustGet(t, m, launchIDA)
	if !got.IsActive() {
		t.Error("task should be active after a successful launch")
	}
	if want := expectSchedule(t, "today"); got.Schedule != want {
		t.Errorf("Schedule = %q, want %q", got.Schedule, want)
	}
	if after := commitCount(t, m.repoPath); after != before+1 {
		t.Errorf("commits = %d, want %d", after, before+1)
	}
	if len(m.undoStack) != undoBefore+1 {
		t.Errorf("undo stack = %d, want %d", len(m.undoStack), undoBefore+1)
	}
	if want := "launched: fix the thing (moved to Today)"; m.statusMsg != want {
		t.Errorf("status = %q, want %q", m.statusMsg, want)
	}
}

func TestLaunch_AlreadyActiveTaskStaysActiveWithoutCommit(t *testing.T) {
	rec := stubLaunch(t, nil)
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "busy", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"active", "proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)
	before := commitCount(t, m.repoPath)

	m, cmd := key(t, m, "o")
	m = runCmd(t, m, cmd)

	if rec.calls != 1 {
		t.Fatalf("runLaunch calls = %d, want 1", rec.calls)
	}
	if !mustGet(t, m, launchIDA).IsActive() {
		t.Error("already-active task must stay active")
	}
	if after := commitCount(t, m.repoPath); after != before {
		t.Errorf("commits = %d, want %d (no new commit)", after, before)
	}
	if len(m.undoStack) != 0 {
		t.Errorf("undo stack = %v, want empty", m.undoStack)
	}
	if want := "launched: busy"; m.statusMsg != want {
		t.Errorf("status = %q, want %q", m.statusMsg, want)
	}
}

func TestLaunch_FailureLeavesTaskUnchanged(t *testing.T) {
	stubLaunch(t, errors.New("exit status 1: no listen_on"))
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "fix", Status: "open", Schedule: "week",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)
	m, _ = key(t, m, "3")
	before := commitCount(t, m.repoPath)

	m, cmd := key(t, m, "o")
	m = runCmd(t, m, cmd)

	if want := "launch failed: exit status 1: no listen_on"; m.statusMsg != want {
		t.Errorf("status = %q, want %q", m.statusMsg, want)
	}
	if m.err != nil {
		t.Errorf("m.err = %v, want nil (launch messages go through statusMsg)", m.err)
	}
	got := mustGet(t, m, launchIDA)
	if got.IsActive() {
		t.Error("failed launch must not activate the task")
	}
	if got.Schedule != "week" {
		t.Errorf("Schedule = %q, want unchanged %q", got.Schedule, "week")
	}
	if after := commitCount(t, m.repoPath); after != before {
		t.Errorf("commits = %d, want %d", after, before)
	}
}

func TestLaunch_TaskDeletedBeforeResult(t *testing.T) {
	stubLaunch(t, nil)
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "vanishing", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)

	m, cmd := key(t, m, "o")
	msg := cmd()
	if err := m.store.Delete(launchIDA); err != nil {
		t.Fatalf("delete: %v", err)
	}
	next, out := m.Update(msg)
	m = next.(*Model)

	if out != nil {
		t.Error("deleted task should produce no follow-up cmd")
	}
	if want := "launched: vanishing (task gone, not activated)"; m.statusMsg != want {
		t.Errorf("status = %q, want %q", m.statusMsg, want)
	}
}

func TestLaunch_DoneTaskLaunchesWithoutActivating(t *testing.T) {
	rec := stubLaunch(t, nil)
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "finished", Status: "done", Schedule: "today",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)
	m, _ = key(t, m, "6") // Done tab
	before := commitCount(t, m.repoPath)

	m, cmd := key(t, m, "o")
	if cmd == nil {
		t.Fatal("o on a done task returned nil cmd")
	}
	m = runCmd(t, m, cmd)

	if rec.calls != 1 {
		t.Fatalf("runLaunch calls = %d, want 1", rec.calls)
	}
	got := mustGet(t, m, launchIDA)
	if got.IsActive() || got.Status != "done" {
		t.Errorf("done task changed: active=%v status=%q", got.IsActive(), got.Status)
	}
	if after := commitCount(t, m.repoPath); after != before {
		t.Errorf("commits = %d, want %d", after, before)
	}
	if want := "launched: finished"; m.statusMsg != want {
		t.Errorf("status = %q, want %q", m.statusMsg, want)
	}
}

// The launch result names its task by ID, so moving the cursor while the
// launcher runs must not redirect the activation onto the new selection.
func TestLaunch_ActivatesLaunchedTaskNotNewSelection(t *testing.T) {
	stubLaunch(t, nil)
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "first", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: launchIDB, Title: "second", Status: "open", Schedule: "today",
			Position: 2000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)
	if sel := m.selectedTask(); sel == nil || sel.ID != launchIDA {
		t.Fatalf("initial selection = %v, want %s", sel, launchIDA)
	}

	m, cmd := key(t, m, "o")
	msg := cmd()
	m, _ = key(t, m, "j")
	if sel := m.selectedTask(); sel == nil || sel.ID != launchIDB {
		t.Fatalf("selection after j = %v, want %s", sel, launchIDB)
	}
	next, out := m.Update(msg)
	m = next.(*Model)
	if out == nil {
		t.Fatal("successful launch should return the activation cmd")
	}
	m = drive(t, m, out)

	if !mustGet(t, m, launchIDA).IsActive() {
		t.Error("launched task should be active")
	}
	if mustGet(t, m, launchIDB).IsActive() {
		t.Error("newly selected task must not be activated")
	}
	if sel := m.selectedTask(); sel == nil || sel.ID != launchIDB {
		t.Errorf("selection after activation = %v, want it left on %s", sel, launchIDB)
	}
}

// A launch that lands after the user switched tabs activates the task (and
// moves it to Today) without dragging the view back to Today.
func TestLaunch_DoesNotYankTabWhenSelectionMoved(t *testing.T) {
	stubLaunch(t, nil)
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "weekly", Status: "open", Schedule: "week",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
		model.Task{ID: launchIDB, Title: "monthly", Status: "open", Schedule: "month",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)
	m, _ = key(t, m, "3") // Week
	m, cmd := key(t, m, "o")
	msg := cmd()
	m, _ = key(t, m, "4") // Month
	tab := m.activeTab

	next, out := m.Update(msg)
	m = next.(*Model)
	if out == nil {
		t.Fatal("successful launch should return the activation cmd")
	}
	m = drive(t, m, out)

	if !mustGet(t, m, launchIDA).IsActive() {
		t.Error("launched task should be active")
	}
	if m.activeTab != tab {
		t.Errorf("activeTab = %d, want %d (no jump to Today)", m.activeTab, tab)
	}
	if sel := m.selectedTask(); sel == nil || sel.ID != launchIDB {
		t.Errorf("selection = %v, want %s", sel, launchIDB)
	}
	if want := "launched: weekly (moved to Today)"; m.statusMsg != want {
		t.Errorf("status = %q, want %q", m.statusMsg, want)
	}
}

func TestLaunch_UnreadableTaskReportsNotActivated(t *testing.T) {
	stubLaunch(t, nil)
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "corrupt", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)
	before := commitCount(t, m.repoPath)

	m, cmd := key(t, m, "o")
	msg := cmd()
	if err := os.WriteFile(filepath.Join(m.repoPath, taskRelPath(launchIDA)), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, out := m.Update(msg)
	m = next.(*Model)

	if out != nil {
		t.Error("unreadable task should produce no follow-up cmd")
	}
	if prefix := "launched: corrupt (not activated: "; !strings.HasPrefix(m.statusMsg, prefix) {
		t.Errorf("status = %q, want prefix %q", m.statusMsg, prefix)
	}
	if after := commitCount(t, m.repoPath); after != before {
		t.Errorf("commits = %d, want %d", after, before)
	}
}

func TestLaunch_MultiLineTitleFlattenedInStatus(t *testing.T) {
	rec := stubLaunch(t, nil)
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "first line\nsecond line", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)

	m, cmd := key(t, m, "o")
	m = runCmd(t, m, cmd)

	if want := "launched: " + flattenTitle("first line\nsecond line"); m.statusMsg != want {
		t.Errorf("status = %q, want %q", m.statusMsg, want)
	}
	if strings.Contains(m.statusMsg, "\n") {
		t.Errorf("status %q should be single-line", m.statusMsg)
	}
	if !strings.Contains(rec.argvs[0][2], "first line\nsecond line") {
		t.Errorf("prompt should keep the raw title: %q", rec.argvs[0][2])
	}
}

// Two `o` presses before either launcher returns open two tabs; the task
// ends up active once, with no error and at most one commit per activation.
func TestLaunch_DoublePress(t *testing.T) {
	rec := stubLaunch(t, nil)
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "twice", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)
	before := commitCount(t, m.repoPath)

	m, first := key(t, m, "o")
	m, second := key(t, m, "o")
	m = runCmd(t, m, first)
	m = runCmd(t, m, second)

	if rec.calls != 2 {
		t.Errorf("runLaunch calls = %d, want 2", rec.calls)
	}
	if !mustGet(t, m, launchIDA).IsActive() {
		t.Error("task should be active")
	}
	if m.err != nil {
		t.Errorf("m.err = %v, want nil", m.err)
	}
	if after := commitCount(t, m.repoPath); after < before+1 || after > before+2 {
		t.Errorf("commits = %d, want %d..%d", after, before+1, before+2)
	}
}

// A stale m.err from an earlier action renders ahead of statusMsg, so every
// launch outcome must clear it or its status is never seen.
func TestLaunch_ClearsStaleError(t *testing.T) {
	stubLaunch(t, nil)
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "task", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.width = 200
	m.err = errors.New("old reschedule error")

	m, _ = key(t, m, "o")

	if line := ansi.Strip(m.helpLine()); !strings.Contains(line, "launch not configured") {
		t.Errorf("help line = %q, want the launch status", line)
	}
}

func TestLaunch_ResolveFailureThenSuccessIsNotSticky(t *testing.T) {
	stubLaunch(t, nil)
	dir := t.TempDir()
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "nowhere", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"unmapped"}, UpdatedAt: "2026-04-13T00:00:00Z"},
		// Already active, so the launch sets only statusMsg: an activation's
		// taskSavedMsg would clear m.err and hide a regression here.
		model.Task{ID: launchIDB, Title: "somewhere", Status: "open", Schedule: "today",
			Position: 2000, Tags: []string{"active", "proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(dir)
	m.width = 200

	m, cmd := key(t, m, "o")
	if cmd != nil {
		t.Fatal("unresolvable dir should not launch")
	}
	if !strings.Contains(ansi.Strip(m.helpLine()), "no launch dir for tags [unmapped]") {
		t.Errorf("help line = %q, want the resolve error", ansi.Strip(m.helpLine()))
	}

	m, _ = key(t, m, "j")
	if sel := m.selectedTask(); sel == nil || sel.ID != launchIDB {
		t.Fatalf("selection after j = %v, want %s", sel, launchIDB)
	}
	m, cmd = key(t, m, "o")
	m = runCmd(t, m, cmd)

	if m.err != nil {
		t.Errorf("m.err = %v, want nil", m.err)
	}
	if line := ansi.Strip(m.helpLine()); !strings.Contains(line, "launched: somewhere") {
		t.Errorf("help line = %q, want launched status", line)
	}
}

func TestLaunch_RefusesWithoutRunning(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		cfg        config.LaunchConfig
		wantStatus string
	}{
		{"unconfigured", config.LaunchConfig{}, "launch not configured"},
		{"no matching tag and no default", config.LaunchConfig{
			Command: []string{"launcher"}, Dirs: map[string]string{"other": "/tmp"},
		}, "no launch dir for tags [proj]"},
		{"mapped dir missing", testLaunchCfg(missing), "launch dir not found: " + missing},
		{"mapped dir is a file", testLaunchCfg(file), "launch dir not found: " + file},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := stubLaunch(t, nil)
			m := newTestModel(t,
				model.Task{ID: launchIDA, Title: "task", Status: "open", Schedule: "today",
					Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
			)
			m.launchCfg = tc.cfg

			m, cmd := key(t, m, "o")
			if cmd != nil {
				t.Error("o returned a cmd, want nil")
			}
			if m.statusMsg != tc.wantStatus {
				t.Errorf("status = %q, want %q", m.statusMsg, tc.wantStatus)
			}
			if m.err != nil {
				t.Errorf("m.err = %v, want nil", m.err)
			}
			if rec.calls != 0 {
				t.Errorf("runLaunch calls = %d, want 0", rec.calls)
			}
		})
	}
}

func TestLaunch_NoSelectedTaskIsNoOp(t *testing.T) {
	rec := stubLaunch(t, nil)
	m := newTestModel(t)
	m.launchCfg = testLaunchCfg(t.TempDir())

	m, cmd := key(t, m, "o")
	if cmd != nil || m.statusMsg != "" || rec.calls != 0 {
		t.Errorf("cmd=%v status=%q calls=%d, want nil/empty/0", cmd != nil, m.statusMsg, rec.calls)
	}
}

func TestLaunch_DetailPanelRoutesOToNoteTextarea(t *testing.T) {
	rec := stubLaunch(t, nil)
	m := newTestModel(t,
		model.Task{ID: launchIDA, Title: "task", Status: "open", Schedule: "today",
			Position: 1000, Tags: []string{"proj"}, UpdatedAt: "2026-04-13T00:00:00Z"},
	)
	m.launchCfg = testLaunchCfg(t.TempDir())
	m, _ = key(t, m, "enter")
	if !m.detailOpen {
		t.Fatal("detail panel should be open")
	}

	m, _ = key(t, m, "o")

	if got := m.noteArea.Value(); got != "o" {
		t.Errorf("note textarea = %q, want %q", got, "o")
	}
	if rec.calls != 0 {
		t.Errorf("runLaunch calls = %d, want 0", rec.calls)
	}
}

// Pins the newModel snapshot: a launch block in config.json reaches
// m.launchCfg through config.Load + newModel, not through Options.
func TestNewModel_SnapshotsLaunchConfig(t *testing.T) {
	// Reset config package state so the kitty launch block does not leak.
	cleanDir := t.TempDir()
	t.Cleanup(func() { _ = config.Load(cleanDir) })
	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := git.Init(repoPath, ""); err != nil {
		t.Fatalf("git.Init: %v", err)
	}
	t.Setenv("MONOLOG_DIR", repoPath)
	cfgPath := filepath.Join(repoPath, ".monolog", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	raw["launch"] = map[string]any{
		"command":     []string{"kitty", "@", "launch", "--cwd={dir}", "claude", "{prompt}"},
		"dirs":        map[string]string{"proj": "/srv/proj"},
		"default_dir": "/srv",
	}
	out, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, out, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.Load(repoPath); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	s, err := store.New(filepath.Join(repoPath, ".monolog", "tasks"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	m, err := newModel(s, repoPath, Options{})
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}

	want := config.LaunchConfig{
		Command:    []string{"kitty", "@", "launch", "--cwd={dir}", "claude", "{prompt}"},
		Dirs:       map[string]string{"proj": "/srv/proj"},
		DefaultDir: "/srv",
	}
	if !reflect.DeepEqual(m.launchCfg, want) {
		t.Errorf("launchCfg = %+v, want %+v", m.launchCfg, want)
	}
	if !m.launchConfigured() {
		t.Error("launchConfigured() = false, want true")
	}
}

func TestHelpLine_ClaudeHintOnlyWhenLaunchConfigured(t *testing.T) {
	for _, view := range []struct {
		name string
		mode viewMode
	}{{"schedule view", viewSchedule}, {"tag view", viewTag}} {
		t.Run(view.name, func(t *testing.T) {
			m := newTestModel(t)
			m.width = 400
			m.viewMode = view.mode

			if h := ansi.Strip(m.helpLine()); strings.Contains(h, "claude") {
				t.Errorf("unconfigured help line has claude hint: %q", h)
			}

			m.launchCfg = testLaunchCfg(t.TempDir())
			h := ansi.Strip(m.helpLine())
			if !strings.Contains(h, "claude") {
				t.Errorf("configured help line missing claude hint: %q", h)
			}
			if !strings.Contains(ansi.Strip(h), "a active  o claude") {
				t.Errorf("claude hint should sit right after the active hint: %q", ansi.Strip(h))
			}

			// Narrow terminals drop hints from the right; o must survive.
			m.width = 100
			if h := ansi.Strip(m.helpLine()); !strings.Contains(h, "a active  o claude") {
				t.Errorf("claude hint dropped at width 100: %q", h)
			}
		})
	}
}

func TestHelpModalContent_ListsLaunchKey(t *testing.T) {
	m := newTestModel(t)
	if help := ansi.Strip(m.helpModalContent()); !strings.Contains(help, "open in Claude Code (and activate)") {
		t.Errorf("help overlay missing o line:\n%s", help)
	}
}
