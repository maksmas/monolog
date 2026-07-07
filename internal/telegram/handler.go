package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/ordering"
	"github.com/maksmas/monolog/internal/recurrence"
	"github.com/maksmas/monolog/internal/schedule"
	"github.com/maksmas/monolog/internal/store"
)

// readOnlyMessage is the user-facing reply sent when a write request hits the
// bot while the read-only flag is set. It mirrors the wording from the plan's
// Technical Details section so the user sees consistent language whether the
// flag was tripped by a capture, a Done callback, or any future write path.
const readOnlyMessage = "⚠️ sync conflict, change not saved — resolve on laptop"

// Handler owns the per-process state for serving Telegram updates. A single
// Handler instance is constructed by Serve and shared across the update
// loop's goroutine sequencing — there is no need to instantiate it from
// outside the package, so the constructor is exported but the type's fields
// remain unexported.
//
// Concurrency model: `mu` serializes git subprocess invocations and
// in-memory task mutations across goroutines — both the dispatch
// goroutine (this Handler's Handle calls) and the background pull-
// ticker. Telegram bot HTTP I/O happens OUTSIDE the lock so a slow or
// hung Telegram round-trip cannot block the pull-ticker or other write
// handlers. The mutex covers:
//   - The pull-ticker's git.PullRebase (held in pullOnce).
//   - The write handlers' read-modify-write + git AutoCommit + git Sync
//     (only — bot.SendMessage / EditMessage / AnswerCallback are issued
//     after the lock is released).
//   - The browse / read paths' store.List (so they don't observe a
//     partially-rewritten tasks directory mid-rebase).
//
// The motivation is that git itself serializes on .git/index.lock at the
// OS level: two processes hitting the index simultaneously fail with
// "Unable to create '.git/index.lock'". A single sync.Mutex around every
// store + git call sidesteps that entire class of bug. Holding the lock
// during a pull also makes the read-modify-write done by callback
// handlers atomic — re-reading the task under the lock observes any
// laptop-side edits the pull just replayed. Keeping bot HTTP I/O outside
// the lock prevents Telegram-side latency from starving the pull-ticker.
type Handler struct {
	bot        Bot
	store      *store.Store
	repoPath   string
	cfg        TelegramConfig
	dateFormat string
	now        func() time.Time
	writer     io.Writer // non-fatal warnings (e.g. spawn warnings from CompleteAndSpawn)

	mu       sync.Mutex  // serializes write paths
	readOnly atomic.Bool // set when a git.Sync conflict needs manual resolution
}

// TelegramConfig is the value-type view of internal/config.TelegramConfig
// the package depends on. Defining it here lets internal/telegram remain
// independent of internal/config (per package contract) — callers translate
// from config.TelegramConfig to telegram.TelegramConfig at the cmd layer.
type TelegramConfig struct {
	AllowedUserIDs []int64
	PullInterval   time.Duration
	BrowseLimit    int
}

// NewHandler constructs a Handler. The `now` parameter is optional — a nil
// value falls back to time.Now so tests can inject a deterministic clock
// while production code uses the wall clock. Mirrors the pattern used by
// internal/email's swappable seams.
//
// The constructor does not validate fields beyond required pointers — the
// Serve entry point owns the option-validation responsibility, and Handler
// is internal to the package.
func NewHandler(bot Bot, s *store.Store, repoPath string, cfg TelegramConfig, dateFormat string, now func() time.Time) *Handler {
	if now == nil {
		now = time.Now
	}
	return &Handler{
		bot:        bot,
		store:      s,
		repoPath:   repoPath,
		cfg:        cfg,
		dateFormat: dateFormat,
		now:        now,
		writer:     io.Discard,
	}
}

// SetWriter installs the destination for non-fatal warnings (spawn warnings
// from recurrence.CompleteAndSpawn, etc.). Serve calls this from
// ServeOptions.Writer so warnings reach the systemd journal instead of being
// silently discarded. nil falls back to io.Discard.
func (h *Handler) SetWriter(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	h.writer = w
}

// IsReadOnly reports whether the bot is currently rejecting writes due to a
// prior git.Sync conflict. Browse commands consult this flag to prepend a
// banner; write paths use it to short-circuit before mutating the store.
func (h *Handler) IsReadOnly() bool { return h.readOnly.Load() }

// ClearReadOnly clears the read-only flag — called by the pull-ticker
// goroutine in Serve after a successful background PullRebase, since a
// clean pull means the remote and local diverged commits have merged.
func (h *Handler) ClearReadOnly() { h.readOnly.Store(false) }

// SetReadOnly is exposed for tests so they can force the flag without
// having to drive a real git.Sync failure. Production code only sets the
// flag through commitAndSync.
func (h *Handler) SetReadOnly(v bool) { h.readOnly.Store(v) }

// isAllowed returns true when userID appears in the configured allow-list.
// An empty allow-list rejects everyone — the documented "no one allowed"
// semantics. We do NOT treat empty allow-list as "anyone allowed" because
// that would silently open the bot to the public on a misconfigured deploy.
func (h *Handler) isAllowed(userID int64) bool {
	for _, id := range h.cfg.AllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// Handle dispatches a single Update. Updates from non-allow-listed users
// are silently dropped (return nil) — the bot intentionally does NOT reply
// to unknown users so it doesn't reveal its own existence to drive-by
// queries. Bare updates (translated as both Message and Callback nil, e.g.
// edited messages or channel posts) are also dropped silently.
//
// Routing:
//   - Callback present → handleCallback (implemented in task 7)
//   - Message present + ReplyTo set → handleNoteReply (task 8)
//   - Message present + Text begins with '/' → handleSlash (task 6)
//   - Message present otherwise → handleCapture
//
// All sub-handlers return error; Handle propagates the error up to the
// Serve loop, which logs but does not abort the polling loop (one bad
// update should not take the bot down).
func (h *Handler) Handle(ctx context.Context, u Update) error {
	if u.Callback != nil {
		if !h.isAllowed(u.Callback.UserID) {
			// Always answer the callback (silently) so the loading spinner
			// stops on the user's button — Telegram requires it within ~15s
			// or the button spins forever. We use an empty toast so the
			// rejected user gets no visible signal about the bot's existence;
			// from their phone it just looks like the button "did nothing".
			_ = h.bot.AnswerCallback(ctx, u.Callback.ID, "")
			return nil
		}
		return h.handleCallback(ctx, u.Callback)
	}
	if u.Message == nil {
		return nil
	}
	if !h.isAllowed(u.Message.UserID) {
		return nil
	}
	if u.Message.ReplyTo != nil {
		return h.handleNoteReply(ctx, u.Message)
	}
	if len(u.Message.Text) > 0 && u.Message.Text[0] == '/' {
		return h.handleSlash(ctx, u.Message)
	}
	return h.handleCapture(ctx, u.Message)
}

// readOnlyBanner is the header message prepended to browse output when the
// bot is currently rejecting writes. The wording matches readOnlyMessage's
// vocabulary ("sync conflict pending") so a user who saw a write fail sees
// the same language while browsing.
const readOnlyBanner = "⚠️ <i>read-only — sync conflict pending</i>"

// bucketLabel returns the human-readable display label for a bucket name,
// shown as the bold title on the empty-bucket message ("Today — nothing 🎉").
// The mapping mirrors the bucket constants in internal/schedule so users see
// a familiar word regardless of whether the underlying value is an ISO date
// or a legacy bucket string.
func bucketLabel(bucket string) string {
	switch bucket {
	case schedule.Today:
		return "Today"
	case schedule.Tomorrow:
		return "Tomorrow"
	case schedule.Week:
		return "Week"
	case schedule.Month:
		return "Month"
	case schedule.Someday:
		return "Someday"
	default:
		return bucket
	}
}

// handleSlash dispatches a slash command. The command word is the first
// whitespace-delimited token with its leading '/' stripped and lowercased,
// so `/Today` and `/today extra args` both route to the today branch. The
// trailing arguments (if any) are ignored — none of the browse commands
// today take arguments. Unknown commands reply with a one-line hint
// pointing at /help.
func (h *Handler) handleSlash(ctx context.Context, m *Message) error {
	text := strings.TrimSpace(m.Text)
	// Strip the leading '/' that the caller already filtered on, then take
	// the first whitespace-delimited token as the command word. We use
	// strings.Fields so any Unicode whitespace (incl. \n / \r) splits the
	// token — `/today\nextra` must route the same as `/today extra`.
	cmd := strings.TrimPrefix(text, "/")
	if fields := strings.Fields(cmd); len(fields) > 0 {
		cmd = fields[0]
	}
	cmd = strings.ToLower(cmd)

	switch cmd {
	case "today":
		return h.handleBrowse(ctx, m, schedule.Today)
	case "week":
		return h.handleBrowse(ctx, m, schedule.Week)
	case "active":
		return h.handleActive(ctx, m)
	case "all":
		return h.handleAll(ctx, m)
	case "help", "start":
		return h.handleHelp(ctx, m)
	default:
		_, err := h.bot.SendMessage(ctx, m.ChatID, "unknown command — try /help", nil)
		return err
	}
}

// listLocked is a thin helper that takes h.mu around store.List. The lock
// keeps the read coherent with the background pull-ticker — without it a
// reader could land on a partial JSON file mid-rebase and bubble a
// `json.Unmarshal` error up to the user as "list failed". We release the
// lock before sending so the slow bot.SendMessage calls don't block the
// pull-ticker.
func (h *Handler) listLocked(opts store.ListOptions) ([]model.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.store.List(opts)
}

// handleBrowse renders all open tasks whose stored schedule falls into the
// given bucket. The bucket filter is applied post-List because store.List
// only supports Status + Tag filtering; schedule semantics (today / week)
// depend on now and on the virtual-bucket rules in internal/schedule, so
// we keep them out of the store and apply them here.
//
// Output: one message per task (summary row + summary keyboard). When no
// task matches, a single FormatEmptyBucket message is sent so the user
// always sees a reply. When readOnly is set, a banner message is prepended.
func (h *Handler) handleBrowse(ctx context.Context, m *Message, bucket string) error {
	all, err := h.listLocked(store.ListOptions{Status: "open"})
	if err != nil {
		_, sendErr := h.bot.SendMessage(ctx, m.ChatID, "internal error: list failed", nil)
		return errors.Join(fmt.Errorf("store.List: %w", err), sendErr)
	}
	now := h.now()
	matched := make([]model.Task, 0, len(all))
	for _, t := range all {
		if schedule.MatchesBucket(t.Schedule, bucket, now) {
			matched = append(matched, t)
		}
	}
	return h.sendBrowseResults(ctx, m, matched, bucketLabel(bucket), 0)
}

// handleActive renders all open tasks bearing the reserved "active" tag.
// Unlike the bucket-based browse helpers, we let the store filter by tag
// directly (store.ListOptions.Tag) since tag filtering is already a
// first-class store concept.
func (h *Handler) handleActive(ctx context.Context, m *Message) error {
	tasks, err := h.listLocked(store.ListOptions{Status: "open", Tag: model.ActiveTag})
	if err != nil {
		_, sendErr := h.bot.SendMessage(ctx, m.ChatID, "internal error: list failed", nil)
		return errors.Join(fmt.Errorf("store.List: %w", err), sendErr)
	}
	return h.sendBrowseResults(ctx, m, tasks, "Active", 0)
}

// handleAll renders every open task, regardless of schedule or tag. The
// list is capped at cfg.BrowseLimit; when the underlying list exceeds the
// cap, a `+N more — open laptop` footer is sent after the per-task rows
// so the user knows there is more content waiting on the laptop.
//
// The cap is applied *before* sending any per-task message so we can show
// the right footer count without a second list traversal. A non-positive
// cap is treated as "no cap" — this is defensive: the config layer
// silently clamps non-positive values to defaults, but we want this
// helper to behave sanely if a test or future caller passes a bogus
// value directly into the Handler.
func (h *Handler) handleAll(ctx context.Context, m *Message) error {
	tasks, err := h.listLocked(store.ListOptions{Status: "open"})
	if err != nil {
		_, sendErr := h.bot.SendMessage(ctx, m.ChatID, "internal error: list failed", nil)
		return errors.Join(fmt.Errorf("store.List: %w", err), sendErr)
	}
	overflow := 0
	limit := h.cfg.BrowseLimit
	if limit > 0 && len(tasks) > limit {
		overflow = len(tasks) - limit
		tasks = tasks[:limit]
	}
	return h.sendBrowseResults(ctx, m, tasks, "All", overflow)
}

// sendBrowseResults is the common output path shared by all browse
// helpers. It emits, in order:
//  1. The read-only banner (if the bot's readOnly flag is set).
//  2. One message per task (summary row + summary keyboard), OR a single
//     empty-bucket "nothing 🎉" message when there are no tasks.
//  3. A `+N more — open laptop` footer when overflow > 0.
//
// Returning the first send error short-circuits the rest of the loop —
// further sends would likely fail too, and the Serve loop already logs
// the error.
func (h *Handler) sendBrowseResults(ctx context.Context, m *Message, tasks []model.Task, label string, overflow int) error {
	if h.readOnly.Load() {
		if _, err := h.bot.SendMessage(ctx, m.ChatID, readOnlyBanner, nil); err != nil {
			return fmt.Errorf("send banner: %w", err)
		}
	}
	if len(tasks) == 0 {
		if _, err := h.bot.SendMessage(ctx, m.ChatID, FormatEmptyBucket(label), nil); err != nil {
			return fmt.Errorf("send empty: %w", err)
		}
		return nil
	}
	for _, t := range tasks {
		row := FormatTaskRow(t)
		kb := BuildSummaryKeyboard(t.ID)
		if _, err := h.bot.SendMessage(ctx, m.ChatID, row, kb); err != nil {
			return fmt.Errorf("send row: %w", err)
		}
	}
	if overflow > 0 {
		footer := fmt.Sprintf("<i>+%d more — open laptop</i>", overflow)
		if _, err := h.bot.SendMessage(ctx, m.ChatID, footer, nil); err != nil {
			return fmt.Errorf("send footer: %w", err)
		}
	}
	return nil
}

// handleCapture creates a new task from a free-text message. The flow is:
//  1. Refuse to mutate when the readOnly flag is set; reply with the
//     conflict message and return nil (the user can keep talking; nothing
//     is lost on Telegram's side).
//  2. Parse hashtags out of the title line via ParseCapture; the body is
//     left untouched so multi-line capture preserves intent.
//  3. Resolve today's schedule via schedule.Parse(schedule.Today).
//  4. Generate a fresh ULID, compute the next position from the current
//     list of all tasks (matching cmd/add.go's behavior), and apply the
//     auto-tag rule from the title prefix against the existing known tags.
//  5. Create + commit + sync. On any write-side error reply with a short
//     status and return the error so the Serve loop can log it.
func (h *Handler) handleCapture(ctx context.Context, m *Message) error {
	if h.readOnly.Load() {
		_, err := h.bot.SendMessage(ctx, m.ChatID, readOnlyMessage, nil)
		return err
	}

	title, body, inlineTags := ParseCapture(m.Text)
	if title == "" {
		// Stripping hashtags from a hashtag-only message leaves no title;
		// rather than create an empty task, reply with a hint so the user
		// knows what to do. The hint is intentionally terse to keep the
		// phone UI clean.
		_, err := h.bot.SendMessage(ctx, m.ChatID, "empty title — send some text to capture", nil)
		return err
	}

	// captureResult carries the data needed to issue the bot reply AFTER
	// the lock is released. We compute everything under the lock — store
	// writes, git, the rendered HTML — then ship it out unlocked so a
	// slow Telegram round-trip does not block the pull-ticker.
	type captureResult struct {
		html    string
		kb      InlineKeyboard
		err     error // returned by handleCapture to the caller
		sendErr error // joined into err after the bot send fires (rare path)
	}
	var res captureResult

	func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		now := h.now()
		scheduleDate, err := schedule.Parse(schedule.Today, now, h.dateFormat)
		if err != nil {
			res.html = "internal error: schedule parse failed"
			res.err = err
			return
		}

		id, err := model.NewID()
		if err != nil {
			res.html = "internal error: id generation failed"
			res.err = err
			return
		}

		existing, err := h.store.List(store.ListOptions{})
		if err != nil {
			res.html = "internal error: list failed"
			res.err = err
			return
		}

		nowStr := now.UTC().Format(time.RFC3339)
		task := model.Task{
			ID:        id,
			Title:     title,
			Body:      body,
			Source:    "telegram",
			Status:    "open",
			Position:  ordering.NextPosition(existing),
			Schedule:  scheduleDate,
			CreatedAt: nowStr,
			UpdatedAt: nowStr,
			Tags:      inlineTags,
		}
		// Apply the same auto-tag rule used by cmd/add.go so the `tagname: ...`
		// prefix gets folded into Tags only when tagname is already known.
		task.Tags = model.AutoTag(title, model.CollectTags(existing), task.Tags)

		if err := h.store.Create(task); err != nil {
			res.html = "internal error: store create failed"
			res.err = fmt.Errorf("store.Create: %w", err)
			return
		}

		taskRel := taskRelPath(task.ID)
		commitMsg := fmt.Sprintf("add: %s", task.Title)
		if syncErr := h.commitAndSync(commitMsg, taskRel); syncErr != nil {
			// commitAndSync has already set readOnly. The task is on
			// disk locally and will be rebased on the next clean pull;
			// tell the user the write was deferred.
			res.html = readOnlyMessage
			res.err = syncErr
			return
		}

		// Refresh the task in case store.Create normalized fields (e.g.
		// NoteCount). For a fresh capture this is mostly a no-op, but we
		// want the rendered summary to reflect whatever the store wrote.
		if stored, err := h.store.Get(task.ID); err == nil {
			task = stored
		}

		res.html = FormatTaskRow(task)
		res.kb = BuildSummaryKeyboard(task.ID)
	}()

	// Bot HTTP call happens OUTSIDE the lock so a slow Telegram round-
	// trip never blocks the pull-ticker or other write paths.
	_, sendErr := h.bot.SendMessage(ctx, m.ChatID, res.html, res.kb)
	if res.err != nil {
		// Internal/sync error already captured under the lock; combine
		// with any send error so the caller logs the full chain.
		if sendErr != nil {
			return errors.Join(res.err, sendErr)
		}
		return res.err
	}
	if sendErr != nil {
		return fmt.Errorf("send summary: %w", sendErr)
	}
	return nil
}

// handleCallback dispatches inline-keyboard button taps to the right
// action handler. Parse errors and unknown ULIDs surface as user-visible
// toasts so a stale message (e.g. one rendered before the laptop deleted
// the task) doesn't silently swallow taps. All branches answer the
// callback exactly once — the loading spinner must always stop.
//
// Routing:
//   - "done:<ULID>"     → handleCallbackDone (write path)
//   - "active:<ULID>"   → handleCallbackActive (write path)
//   - "view:<ULID>"     → handleCallbackView (read path)
//   - "collapse:<ULID>" → handleCallbackCollapse (read path)
//
// View / Collapse intentionally do NOT consult the readOnly flag — they
// only read the store; surfacing the conflict on a pure read would
// confuse the user more than help them.
func (h *Handler) handleCallback(ctx context.Context, cq *CallbackQuery) error {
	action, ulid, parseErr := ParseCallback(cq.Data)
	if parseErr != nil {
		return h.bot.AnswerCallback(ctx, cq.ID, "invalid")
	}

	// Resolve the task once up-front; every branch needs it. A missing
	// ULID is converted to a friendly toast — the message itself is left
	// alone since the user may want to read the strike-through state.
	// Resolve under the lock so the directory scan does not observe a
	// partially-rewritten tasks dir mid-pull-rebase. Write handlers will
	// re-read the task under the lock anyway, so the value returned here
	// is only authoritative for the unlocked read-only View / Collapse
	// branches — which is exactly what we want (consistent snapshot).
	h.mu.Lock()
	task, err := h.store.Resolve(ulid)
	h.mu.Unlock()
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return h.bot.AnswerCallback(ctx, cq.ID, "task not found")
		}
		// Other store errors (I/O, parse) are surfaced as a generic toast.
		// We deliberately do NOT echo the underlying message — that text
		// can leak file-system layout or include arbitrary tag content.
		_ = h.bot.AnswerCallback(ctx, cq.ID, "internal error")
		return fmt.Errorf("store.Resolve %q: %w", ulid, err)
	}

	switch action {
	case "done":
		return h.handleCallbackDone(ctx, cq, task)
	case "active":
		return h.handleCallbackActive(ctx, cq, task)
	case "view":
		return h.handleCallbackView(ctx, cq, task)
	case "collapse":
		return h.handleCallbackCollapse(ctx, cq, task)
	default:
		// ParseCallback already validated the action set; this branch is a
		// defensive fall-through in case the dispatch list ever drifts
		// from the parser's allow-list.
		return h.bot.AnswerCallback(ctx, cq.ID, "invalid")
	}
}

// handleCallbackDone marks the task as done (via recurrence.CompleteAndSpawn
// so recurring tasks get their next occurrence spawned), commits the change
// plus any spawn in a single git commit, edits the original message to a
// strike-through summary, and answers the callback. The strike-through row
// includes a `↻ next: <date>` line when CompleteAndSpawn produced a spawn.
//
// readOnly path: when the bot is rejecting writes the message is left as-is
// and the user sees a toast pointing at the conflict resolution — same
// language as the capture path.
//
// Already-done path: a second tap on a stale message gets a "already done"
// toast and no further work. The store is unchanged.
//
// Concurrency: the task value resolved in handleCallback is read OUTSIDE the
// lock — to make the read-modify-write atomic against a concurrent pull,
// we re-read the task inside the lock before mutating. Otherwise a
// background pull-rebase between Resolve and Update could land laptop-side
// edits to title/tags/schedule that we'd silently clobber on write.
func (h *Handler) handleCallbackDone(ctx context.Context, cq *CallbackQuery, task model.Task) error {
	if h.readOnly.Load() {
		return h.bot.AnswerCallback(ctx, cq.ID, "sync conflict — resolve on laptop")
	}
	// NOTE: there is intentionally NO pre-lock `task.Status == "done"`
	// short-circuit here. The `task` value passed in by handleCallback
	// was resolved before the lock and can be stale relative to a pull-
	// ticker that just landed a laptop-side done. The in-lock re-read
	// below is the only trustworthy view of `Status`.

	// doneOutcome captures what should happen AFTER the lock is released.
	// `toast` is the AnswerCallback text (always sent). `editRow`
	// non-empty means EditMessage runs with that body; empty means the
	// message is left as-is (already-done / read-only paths). `err` is
	// returned to the caller for logging.
	type doneOutcome struct {
		toast   string
		editRow string
		err     error
	}
	var out doneOutcome

	func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		// Re-check readOnly under the lock so a concurrent failure flip
		// doesn't race past the unlocked check above.
		if h.readOnly.Load() {
			out.toast = "sync conflict — resolve on laptop"
			return
		}

		// Re-read the task under the lock so we observe any laptop-side
		// edits that landed via the pull-ticker between Resolve and now.
		fresh, err := h.store.Get(task.ID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				out.toast = "task not found"
				return
			}
			out.toast = "internal error"
			out.err = fmt.Errorf("store.Get %q: %w", task.ID, err)
			return
		}
		task = fresh
		if task.Status == "done" {
			// A concurrent CLI/TUI done landed before we acquired the lock.
			out.toast = "already done"
			return
		}

		// Capture spawn warnings to a side buffer so they can surface on
		// stderr after the user-facing path completes; we deliberately don't
		// surface them in Telegram (the user can't act on them from phone).
		var warn bytes.Buffer
		commitMsg, commitFiles, err := recurrence.CompleteAndSpawn(h.store, &task, h.now(), &warn, h.dateFormat)
		if err != nil {
			out.toast = "internal error"
			out.err = fmt.Errorf("CompleteAndSpawn: %w", err)
			return
		}
		if warn.Len() > 0 {
			fmt.Fprint(h.writer, warn.String())
		}

		if syncErr := h.commitAndSync(commitMsg, commitFiles...); syncErr != nil {
			out.toast = "sync conflict — resolve on laptop"
			out.err = syncErr
			return
		}

		// Derive the next-occurrence date string for the strike-through
		// row from the spawn-side file path (when present). commitFiles
		// is ordered: [oldTaskFile, optional newTaskFile]. A two-element
		// slice indicates a spawn happened.
		var nextDate string
		if len(commitFiles) > 1 {
			newID := strings.TrimSuffix(filepath.Base(commitFiles[1]), ".json")
			if spawned, gErr := h.store.Get(newID); gErr == nil {
				nextDate = schedule.FormatDisplay(spawned.Schedule, h.dateFormat)
			}
		}

		out.editRow = FormatDoneRow(task, nextDate)
	}()

	// Bot HTTP calls happen OUTSIDE the lock so a slow Telegram round-
	// trip never blocks the pull-ticker or other write paths.
	var editErr error
	if out.editRow != "" {
		editErr = h.bot.EditMessage(ctx, cq.ChatID, cq.MessageID, out.editRow, nil)
	}
	// Always answer the callback so Telegram stops the loading spinner —
	// otherwise an EditMessage failure leaves the button spinning until
	// Telegram's ~15s timeout. The answer is silent on the happy path and
	// on EditMessage failure alike (the user already sees the strike-
	// through state via the next browse, or none of these messages
	// mattered).
	answerErr := h.bot.AnswerCallback(ctx, cq.ID, out.toast)
	if out.err != nil {
		return out.err
	}
	if editErr != nil {
		return fmt.Errorf("edit done message: %w", editErr)
	}
	return answerErr
}

// handleCallbackActive toggles the reserved active tag on the task,
// commits the change, and edits the message to reflect the new tag
// state. The button label stays "Active" in both states — the user can
// tell which way the toggle went from the inline tag list, and a
// stateful label would make taps from older messages confusing.
//
// readOnly path mirrors handleCallbackDone: no mutation, toast points
// at the conflict, message stays as-is.
//
// Concurrency: as in handleCallbackDone, we re-read the task inside the
// lock so a concurrent pull-rebase doesn't let us write back stale
// in-memory state on top of laptop-side edits.
func (h *Handler) handleCallbackActive(ctx context.Context, cq *CallbackQuery, task model.Task) error {
	if h.readOnly.Load() {
		return h.bot.AnswerCallback(ctx, cq.ID, "sync conflict — resolve on laptop")
	}

	// activeOutcome mirrors doneOutcome: compute everything that needs
	// the lock, then issue bot HTTP calls unlocked. editRow/editKB are
	// non-zero only on the happy toggle path; empty editRow means leave
	// the message alone (read-only, not-found, already-done, error).
	type activeOutcome struct {
		toast   string
		editRow string
		editKB  InlineKeyboard
		err     error
	}
	var out activeOutcome

	func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		if h.readOnly.Load() {
			out.toast = "sync conflict — resolve on laptop"
			return
		}

		// Re-read the task under the lock so the toggle is applied to
		// the latest on-disk state, not the stale value resolved before
		// the lock.
		fresh, err := h.store.Get(task.ID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				out.toast = "task not found"
				return
			}
			out.toast = "internal error"
			out.err = fmt.Errorf("store.Get %q: %w", task.ID, err)
			return
		}
		task = fresh
		if task.Status == "done" {
			// A concurrent CLI/TUI done landed before we acquired the
			// lock. Toggling active on a done task would re-open it
			// implicitly via SetActive's tag write; bail out and tell
			// the user instead.
			out.toast = "already done"
			return
		}

		wasActive := task.IsActive()
		task.SetActive(!wasActive)
		task.UpdatedAt = h.now().UTC().Format(time.RFC3339)

		if err := h.store.Update(task); err != nil {
			out.toast = "internal error"
			out.err = fmt.Errorf("store.Update: %w", err)
			return
		}

		verb := "active"
		if wasActive {
			verb = "inactive"
		}
		commitMsg := fmt.Sprintf("%s: %s", verb, task.Title)
		if syncErr := h.commitAndSync(commitMsg, taskRelPath(task.ID)); syncErr != nil {
			out.toast = "sync conflict — resolve on laptop"
			out.err = syncErr
			return
		}

		// Re-read the task so the rendered summary reflects whatever
		// the store normalized (NoteCount, tag ordering, etc.).
		if refreshed, err := h.store.Get(task.ID); err == nil {
			task = refreshed
		}

		// Toast confirms the new state for users on slow networks — the
		// inline row also updates (via the ⭐ marker), but the toast
		// gives immediate feedback before the edit round-trip completes.
		if wasActive {
			out.toast = "active off"
		} else {
			out.toast = "active on"
		}
		out.editRow = FormatTaskRow(task)
		out.editKB = BuildSummaryKeyboard(task.ID)
	}()

	// Bot HTTP calls happen OUTSIDE the lock so a slow Telegram round-
	// trip never blocks the pull-ticker or other write paths.
	var editErr error
	if out.editRow != "" {
		editErr = h.bot.EditMessage(ctx, cq.ChatID, cq.MessageID, out.editRow, out.editKB)
	}
	answerErr := h.bot.AnswerCallback(ctx, cq.ID, out.toast)
	if out.err != nil {
		return out.err
	}
	if editErr != nil {
		return fmt.Errorf("edit active message: %w", editErr)
	}
	return answerErr
}

// handleCallbackView expands a summary message into the full detail
// view. The expansion is performed entirely via EditMessage so the
// original message ID is reused — no second message clutters the chat,
// and Collapse swaps back to the summary on the same ID.
func (h *Handler) handleCallbackView(ctx context.Context, cq *CallbackQuery, task model.Task) error {
	body := FormatDetailView(task, h.dateFormat)
	kb := BuildDetailKeyboard(task.ID)
	editErr := h.bot.EditMessage(ctx, cq.ChatID, cq.MessageID, body, kb)
	// Always answer the callback so the spinner stops even on edit failure.
	answerErr := h.bot.AnswerCallback(ctx, cq.ID, "")
	if editErr != nil {
		return fmt.Errorf("edit detail view: %w", editErr)
	}
	return answerErr
}

// handleCallbackCollapse inverts handleCallbackView — the detail message
// is replaced in place with the compact summary row + summary keyboard.
// We re-read the task in case anything mutated between view and collapse
// (e.g. an external `monolog edit` ran on the laptop during the gap).
func (h *Handler) handleCallbackCollapse(ctx context.Context, cq *CallbackQuery, task model.Task) error {
	row := FormatTaskRow(task)
	kb := BuildSummaryKeyboard(task.ID)
	editErr := h.bot.EditMessage(ctx, cq.ChatID, cq.MessageID, row, kb)
	answerErr := h.bot.AnswerCallback(ctx, cq.ID, "")
	if editErr != nil {
		return fmt.Errorf("edit collapse message: %w", editErr)
	}
	return answerErr
}

// helpText is the static HTML cheatsheet returned for /help and /start.
// It mirrors the user-facing surface implemented by the Telegram package
// today: free-text capture (with #hashtag and tagname: prefix), the four
// browse commands, the inline keyboard buttons, and the reply-to-note flow.
// The wording is deliberately terse so the message stays under one screen
// on a phone — discovery in detail still lives in the laptop CLI's `help`.
const helpText = `<b>monolog bot</b>
Send any free text to <b>capture</b> a task.
  • Add <code>#hashtag</code> anywhere on the first line to tag.
  • Start with <code>tagname:</code> to auto-apply an existing tag.

<b>Browse</b>
  /today    — today's bucket
  /week     — the week bucket
  /active   — tasks tagged active
  /all      — every open task

<b>Buttons under each task</b>
  ✅ Done — complete (recurring tasks spawn next occurrence)
  ⭐ Active — toggle the active tag
  📄 Details — expand to full body

<b>Notes</b>
  Reply to any task message to append a timestamped note.`

// handleHelp sends the static help cheatsheet. /start is routed here too
// so the very first interaction a new chat sees mirrors the on-demand
// /help reply — no separate "welcome" text to maintain. The handler does
// NOT touch the store and is safe in read-only mode.
func (h *Handler) handleHelp(ctx context.Context, m *Message) error {
	_, err := h.bot.SendMessage(ctx, m.ChatID, helpText, nil)
	return err
}

// noteReplyMissingPrefix is the user-facing message sent when a reply
// does not start with a recognisable task token. The hint points the
// user at the prefix shape rendered in summary rows.
const noteReplyMissingPrefix = "could not find a task ID in the replied-to message — reply to a task summary"

// noteReplySuccess is the confirmation sent after a note has been
// appended and committed. Kept short for phone-screen reading; the
// updated note count surfaces on the next browse via the [N] badge.
const noteReplySuccess = "📝 note added"

// handleNoteReply appends a timestamped note to an existing task. The
// resolution rule is:
//
//  1. Extract the first whitespace-bounded token from m.ReplyTo.Text.
//     The summary rows we send always begin with the 5-char ULID prefix
//     (rendered inside <code>...</code>); Telegram strips the HTML in
//     replies, so the plain text begins with the prefix itself.
//  2. Pass that token to store.Resolve so ambiguous prefixes / initials
//     matches behave exactly like the CLI's lookup. Resolution errors
//     are surfaced to the user verbatim (HTML-escaped) so they know
//     what went wrong without us hand-rolling a separate error catalog.
//  3. On success build the new body via model.AppendNote, store.Update,
//     commit + sync, and reply with a short confirmation.
//
// readOnly path mirrors the other write handlers — the reply is rejected
// with the conflict message and the store is untouched.
func (h *Handler) handleNoteReply(ctx context.Context, m *Message) error {
	if h.readOnly.Load() {
		_, err := h.bot.SendMessage(ctx, m.ChatID, readOnlyMessage, nil)
		return err
	}
	if m.ReplyTo == nil {
		// Defensive: Handle's routing has already checked this, but a
		// future caller could invoke handleNoteReply directly.
		return nil
	}

	prefix := firstToken(m.ReplyTo.Text)
	if prefix == "" {
		_, err := h.bot.SendMessage(ctx, m.ChatID, noteReplyMissingPrefix, nil)
		return err
	}

	// noteOutcome carries the data needed for the post-lock bot reply.
	// `html` is the single message body to send; `err` is returned to
	// the caller. A `resolveErr` is propagated as a non-fatal — the
	// user is told what went wrong but Handle does NOT bubble it as
	// an error (matches the pre-refactor behavior).
	type noteOutcome struct {
		html       string
		err        error
		resolveErr error // for errors.Join when the bot send fails
	}
	var out noteOutcome

	func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		if h.readOnly.Load() {
			out.html = readOnlyMessage
			return
		}

		// Resolve under the lock — that way the resolution (which
		// scans the directory) and the subsequent AppendNote/Update
		// see the same on-disk state. A pre-lock Resolve followed by
		// a post-lock Update would risk clobbering a body that the
		// pull-ticker rewrote in between.
		task, err := h.store.Resolve(prefix)
		if err != nil {
			// Bubble the resolve error verbatim — its wording already
			// covers the not-found and ambiguous cases. HTML-escape
			// because the ambiguous case includes user-controlled
			// task titles.
			out.html = "could not resolve task: " + htmlEscape(err.Error())
			out.resolveErr = err
			return
		}

		now := h.now()
		task.Body = model.AppendNote(task.Body, m.Text, now, h.dateFormat)
		task.UpdatedAt = now.UTC().Format(time.RFC3339)

		if err := h.store.Update(task); err != nil {
			out.html = "internal error: store update failed"
			out.err = fmt.Errorf("store.Update: %w", err)
			return
		}

		if syncErr := h.commitAndSync(fmt.Sprintf("note: %s", task.Title), taskRelPath(task.ID)); syncErr != nil {
			out.html = readOnlyMessage
			out.err = syncErr
			return
		}

		out.html = noteReplySuccess
	}()

	// Bot HTTP call happens OUTSIDE the lock so a slow Telegram round-
	// trip never blocks the pull-ticker or other write paths.
	_, sendErr := h.bot.SendMessage(ctx, m.ChatID, out.html, nil)
	if out.err != nil {
		if sendErr != nil {
			return errors.Join(out.err, sendErr)
		}
		return out.err
	}
	if out.resolveErr != nil {
		// Pre-refactor behavior: resolve failures are surfaced to the
		// user but not bubbled as an error from Handle. A send-side
		// failure on top of resolve still gets joined for logging.
		if sendErr != nil {
			return errors.Join(out.resolveErr, sendErr)
		}
		return nil
	}
	if sendErr != nil {
		return fmt.Errorf("send note ack: %w", sendErr)
	}
	return nil
}

// firstToken returns the first whitespace-bounded substring of text, or
// the empty string when text is all-whitespace. Used by handleNoteReply
// to peel the ULID prefix off the start of a replied-to summary row.
// strings.Fields handles every Unicode whitespace category (space, tab,
// newline, CR, NBSP, …) which matches what Telegram clients may insert.
func firstToken(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

