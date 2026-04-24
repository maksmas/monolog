package slack

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/store"
)

// titleMaxRunes is the rune-length cap on task titles derived from Slack
// message text. Titles longer than this are truncated at titleMaxRunes-1
// with a trailing ellipsis rune so the total character count stays at the
// cap.
const titleMaxRunes = 80

// emptyTitlePlaceholder is used when a Slack message's text is entirely
// empty or whitespace (e.g. file-only messages that somehow slipped
// through). Prevents blank-title tasks from landing in the TUI.
const emptyTitlePlaceholder = "(empty message)"

// Options controls ingest behavior. Values are plumbed from config and the
// caller; tests inject Now to control timestamps.
type Options struct {
	// ChannelAsTag controls whether the Slack channel name is added as a
	// second tag alongside "slack". When false, only the "slack" tag is
	// added.
	ChannelAsTag bool
	// DateFormat is the Go time layout used to render the displayTime line
	// in the task body (e.g. "02-01-2006"). Callers pass config.DateFormat().
	DateFormat string
	// Now is the current-time provider. Defaults to time.Now when nil.
	// Tests inject a fixed time to make output deterministic.
	Now func() time.Time
	// Stderr receives per-item diagnostic messages (e.g. DM-skip notices).
	// When nil, os.Stderr is used. Tests inject a bytes.Buffer.
	Stderr io.Writer
}

// BuildTask produces the Task struct for a single Slack saved item. It is
// pure: the ID and Position fields are left zero; the caller (Ingest) assigns
// those. CreatedAt and UpdatedAt are populated from opts.Now(), and Schedule
// is always the "today" bucket since Slack saves represent fresh intake.
func BuildTask(item SavedItem, opts Options) model.Task {
	now := nowFrom(opts)
	nowStr := now.UTC().Format(time.RFC3339)

	title := buildTitle(item.Text)
	// Display time prefers the Slack message timestamp (item.TS) over ingest
	// time — a bookmark saved weeks ago should read with its original date,
	// not the current wall clock. Falls back to now on parse failure so the
	// attribution line is always populated.
	displayTime := messageDisplayTime(item.TS, now, opts.DateFormat)
	body := buildBody(item, displayTime)

	tags := []string{"slack"}
	if opts.ChannelAsTag && item.ChannelName != "" {
		tags = append(tags, item.ChannelName)
	}

	// Schedule is always "today" for Slack imports. On-disk schedule is
	// always ISO, so we format directly without going through schedule.Parse.
	scheduleDate := now.Format("2006-01-02")

	return model.Task{
		Title:     title,
		Body:      body,
		Source:    "slack",
		SourceID:  item.Channel + "/" + item.TS,
		Status:    "open",
		Schedule:  scheduleDate,
		Tags:      tags,
		CreatedAt: nowStr,
		UpdatedAt: nowStr,
	}
}

// messageDisplayTime parses a Slack ts ("1712345678.000100") into a formatted
// date string using dateFormat. Falls back to fallback formatted with
// dateFormat on any parse failure so callers always get a non-empty value.
func messageDisplayTime(ts string, fallback time.Time, dateFormat string) string {
	if ts == "" {
		return fallback.Format(dateFormat)
	}
	// Slack ts is a float "seconds.subseconds" — ParseFloat handles both the
	// integer (whole seconds) and the subsecond parts; we only care about
	// seconds for a date string.
	secs, err := strconv.ParseFloat(ts, 64)
	if err != nil || secs <= 0 {
		return fallback.Format(dateFormat)
	}
	return time.Unix(int64(secs), 0).UTC().Format(dateFormat)
}

// buildTitle derives the task title from the first non-blank line of text,
// truncating to titleMaxRunes with a trailing ellipsis when needed. Rune-safe
// across the entire pipeline so multi-byte emoji never get cut mid-codepoint.
func buildTitle(text string) string {
	// Pick the first non-blank line. An entirely blank message falls back
	// to the placeholder.
	var first string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			first = strings.TrimSpace(line)
			break
		}
	}
	if first == "" {
		return emptyTitlePlaceholder
	}

	runes := []rune(first)
	if len(runes) <= titleMaxRunes {
		return first
	}
	// Reserve the final rune slot for the ellipsis so the total count
	// stays at titleMaxRunes.
	return string(runes[:titleMaxRunes-1]) + "…"
}

// buildBody composes the task body: the raw Slack text (verbatim, no
// markup decoding), a blank line, an attribution line, a blank line, and
// the permalink. displayTime is rendered by the caller (already formatted
// with the configured layout) so BuildTask can source it from the Slack
// message ts rather than ingest time.
func buildBody(item SavedItem, displayTime string) string {
	var b strings.Builder
	b.WriteString(item.Text)
	b.WriteString("\n\n— ")
	// Drop the "@" entirely when AuthorName is empty (bot / deleted-user
	// tombstones) so the attribution reads "— in #channel, date" instead of
	// a dangling "— @ in …".
	if item.AuthorName != "" {
		b.WriteString("@")
		b.WriteString(item.AuthorName)
		b.WriteString(" ")
	}
	b.WriteString("in #")
	b.WriteString(item.ChannelName)
	b.WriteString(", ")
	b.WriteString(displayTime)
	b.WriteString("\n\n")
	b.WriteString(item.Permalink)
	return b.String()
}

// nowFrom returns opts.Now() when set, falling back to time.Now.
func nowFrom(opts Options) time.Time {
	if opts.Now != nil {
		return opts.Now()
	}
	return time.Now()
}

// ParseSourceID splits a Task.SourceID in the "channel/ts" shape used by the
// Slack ingest into its components. Returns ok=false when the input is empty,
// lacks a slash, or has an empty channel/ts part. Shared by CLI done and TUI
// doneSelected to keep the parse rule in one place.
func ParseSourceID(s string) (channel, ts string, ok bool) {
	if s == "" {
		return "", "", false
	}
	idx := strings.Index(s, "/")
	if idx <= 0 || idx >= len(s)-1 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

// isDMChannel reports whether a Slack channel ID represents a DM or
// multi-party DM. These channel types have confusing tag semantics (no
// channel name; author-as-channel-name is misleading) so v1 skips them.
//
// Note: we intentionally do NOT check for the "G" prefix. Although legacy
// group DMs used a "G" id, Slack also uses "G" as the prefix for PRIVATE
// channels, which are a legitimate ingest target. Modern multi-party DMs use
// the "mpdm-" prefix — that is the reliable way to detect them by ID.
// Legacy G-prefixed group DMs (rare in modern workspaces) are accepted
// through ingest; the worst-case outcome is a bookmark whose channel name
// renders oddly, which the user can clean up via edit/rm.
func isDMChannel(channelID string) bool {
	if channelID == "" {
		return false
	}
	// D-prefixed: direct message between two users.
	if strings.HasPrefix(channelID, "D") {
		return true
	}
	// "mpdm-" prefix covers multi-party DMs in the modern id shape.
	if strings.HasPrefix(channelID, "mpdm-") {
		return true
	}
	return false
}

// Ingest writes previously-unseen Slack saved items as new tasks in a single
// batched git commit. Items already present in synced (keyed by
// "channel/ts") are skipped, as are DM / group-DM items.
//
// Returns (newCount, nil) on success where newCount is the number of tasks
// committed. synced is updated in place to reflect newly-ingested keys only
// after the commit succeeds. On commit failure, synced is left untouched and
// the error is surfaced.
//
// Empty input is a no-op and returns (0, nil) with no commit.
func Ingest(s *store.Store, items []SavedItem, synced map[string]bool, opts Options) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	// Position base: compute once, increment per ingested task.
	existing, err := s.List(store.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("list tasks for positioning: %w", err)
	}
	basePos := ordering.NextPosition(existing)

	newTasks := make([]model.Task, 0, len(items))
	newKeys := make([]string, 0, len(items))

	for _, item := range items {
		key := item.Channel + "/" + item.TS
		if synced[key] {
			continue
		}
		if isDMChannel(item.Channel) {
			fmt.Fprintf(stderr, "slack: skipping DM bookmark %s\n", item.TS)
			continue
		}

		id, err := model.NewID()
		if err != nil {
			return 0, fmt.Errorf("generate ID: %w", err)
		}

		task := BuildTask(item, opts)
		task.ID = id
		task.Position = basePos + float64(len(newTasks))*ordering.DefaultSpacing

		newTasks = append(newTasks, task)
		newKeys = append(newKeys, key)
	}

	if len(newTasks) == 0 {
		return 0, nil
	}

	msg := fmt.Sprintf("slack: ingest %d items", len(newTasks))
	if err := s.CreateBatch(newTasks, msg); err != nil {
		return 0, err
	}

	for _, key := range newKeys {
		synced[key] = true
	}
	return len(newTasks), nil
}
