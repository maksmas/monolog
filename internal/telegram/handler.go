package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/schedule"
	"github.com/mmaksmas/monolog/internal/store"
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
// Concurrency model: the update loop in Serve dispatches Handle calls
// serially, but the design still serializes write paths through `mu` so
// later parallelisations (or a future websocket path) can't race on the
// store + git workflow. Read paths (browse, view, collapse) do not take the
// mutex — they only read the store and the read-only flag.
type Handler struct {
	bot        Bot
	store      *store.Store
	repoPath   string
	cfg        TelegramConfig
	dateFormat string
	now        func() time.Time

	mu       sync.Mutex   // serializes write paths
	readOnly atomic.Bool  // set when a git.Sync conflict needs manual resolution
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
	}
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
			return nil
		}
		// Callback dispatcher is implemented in task 7; for now we
		// answer with an empty toast so the user's spinner stops.
		return h.bot.AnswerCallback(ctx, u.Callback.ID, "")
	}
	if u.Message == nil {
		return nil
	}
	if !h.isAllowed(u.Message.UserID) {
		return nil
	}
	if u.Message.ReplyTo != nil {
		// Reply-to-note handler is implemented in task 8; until then we
		// fall through to ignoring the reply so the user sees no spurious
		// behavior on the message path that lands here.
		return nil
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
	// the first whitespace-delimited token as the command word.
	cmd := strings.TrimPrefix(text, "/")
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		cmd = cmd[:i]
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
		// Help / start handlers land in task 8; until then we reply with a
		// terse note so users hitting these commands get *some* feedback.
		_, err := h.bot.SendMessage(ctx, m.ChatID, "help command coming soon — try /today, /week, /active, /all", nil)
		return err
	default:
		_, err := h.bot.SendMessage(ctx, m.ChatID, "unknown command — try /help", nil)
		return err
	}
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
	all, err := h.store.List(store.ListOptions{Status: "open"})
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
	tasks, err := h.store.List(store.ListOptions{Status: "open", Tag: model.ActiveTag})
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
	tasks, err := h.store.List(store.ListOptions{Status: "open"})
	if err != nil {
		_, sendErr := h.bot.SendMessage(ctx, m.ChatID, "internal error: list failed", nil)
		return errors.Join(fmt.Errorf("store.List: %w", err), sendErr)
	}
	overflow := 0
	cap := h.cfg.BrowseLimit
	if cap > 0 && len(tasks) > cap {
		overflow = len(tasks) - cap
		tasks = tasks[:cap]
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
		row := FormatTaskRow(t, h.dateFormat)
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

	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.now()
	scheduleDate, err := schedule.Parse(schedule.Today, now, h.dateFormat)
	if err != nil {
		_, sendErr := h.bot.SendMessage(ctx, m.ChatID, "internal error: schedule parse failed", nil)
		return errors.Join(err, sendErr)
	}

	id, err := model.NewID()
	if err != nil {
		_, sendErr := h.bot.SendMessage(ctx, m.ChatID, "internal error: id generation failed", nil)
		return errors.Join(err, sendErr)
	}

	existing, err := h.store.List(store.ListOptions{})
	if err != nil {
		_, sendErr := h.bot.SendMessage(ctx, m.ChatID, "internal error: list failed", nil)
		return errors.Join(err, sendErr)
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
		_, sendErr := h.bot.SendMessage(ctx, m.ChatID, "internal error: store create failed", nil)
		return errors.Join(fmt.Errorf("store.Create: %w", err), sendErr)
	}

	taskRel := taskRelPath(task.ID)
	commitMsg := fmt.Sprintf("add: %s", task.Title)
	if syncErr := h.commitAndSync(commitMsg, taskRel); syncErr != nil {
		// commitAndSync has already set readOnly. The task is on disk
		// locally and will be rebased on the next clean pull; tell the
		// user the write was deferred.
		_, sendErr := h.bot.SendMessage(ctx, m.ChatID, readOnlyMessage, nil)
		if sendErr != nil {
			return errors.Join(syncErr, sendErr)
		}
		return syncErr
	}

	// Refresh the task in case store.Create normalized fields (e.g.
	// NoteCount). For a fresh capture this is mostly a no-op, but we
	// want the rendered summary to reflect whatever the store wrote.
	stored, err := h.store.Get(task.ID)
	if err == nil {
		task = stored
	}

	row := FormatTaskRow(task, h.dateFormat)
	kb := BuildSummaryKeyboard(task.ID)
	if _, err := h.bot.SendMessage(ctx, m.ChatID, row, kb); err != nil {
		return fmt.Errorf("send summary: %w", err)
	}
	return nil
}
