package slack

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
	"github.com/mmaksmas/monolog/internal/ordering"
	"github.com/mmaksmas/monolog/internal/schedule"
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
	body := buildBody(item, opts)

	tags := []string{"slack"}
	if opts.ChannelAsTag && item.ChannelName != "" {
		tags = append(tags, item.ChannelName)
	}

	// Schedule is always "today" for Slack imports. We resolve it via
	// schedule.Parse so the stored value is ISO (the caller's DateFormat
	// doesn't apply — on-disk schedule is always ISO).
	scheduleDate, _ := schedule.Parse(schedule.Today, now, opts.DateFormat)

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
// the permalink. The display time is rendered in the caller-provided date
// format to match the rest of mlog.
func buildBody(item SavedItem, opts Options) string {
	displayTime := nowFrom(opts).Format(opts.DateFormat)

	var b strings.Builder
	b.WriteString(item.Text)
	b.WriteString("\n\n— @")
	b.WriteString(item.AuthorName)
	b.WriteString(" in #")
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

// isDMChannel reports whether a Slack channel ID represents a DM, group DM,
// or multi-party DM. These channel types have confusing tag semantics (no
// channel name; author-as-channel-name is misleading) so v1 skips them.
func isDMChannel(channelID string) bool {
	if channelID == "" {
		return false
	}
	// D-prefixed: direct message between two users.
	// G-prefixed: legacy group DM.
	// Multi-party DMs use an "mpdm-" prefix in older payloads; newer ones
	// use a "G" id. The "mpdm-" check catches both shapes.
	if strings.HasPrefix(channelID, "D") || strings.HasPrefix(channelID, "G") {
		return true
	}
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
