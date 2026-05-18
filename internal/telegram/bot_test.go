package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeBot is the in-memory Bot implementation used by every telegram-package
// test that needs to exercise handler code without a real Telegram API.
//
// It records every call into capped slices so tests can assert the exact
// sequence of outgoing messages, edits, callback-answers, and update polls.
// All fields are guarded by mu so tests can drive the bot from a goroutine
// (the Serve main loop) while the test body asserts on the recorded state.
//
// Behavior knobs:
//   - updates: a queue of slices returned by successive GetUpdates calls
//     (each call pops the front element); when empty, GetUpdates blocks
//     until updatesCh is closed or ctx is done. Tests that want a single
//     poll-and-return set the queue up front and let the loop drain.
//   - getUpdatesErr: when non-nil, the next GetUpdates call returns this
//     error instead of consuming the queue.
//   - sendErr / editErr / answerErr: corresponding error injection points
//     for the write-side methods.
//   - nextMsgID: incremented and returned by SendMessage so message IDs are
//     monotonically increasing and predictable across the test.
type fakeBot struct {
	mu sync.Mutex

	// inbound side
	updates       [][]Update
	getUpdatesErr error
	pollCount     int

	// outbound side
	sent      []sentMessage
	edits     []editedMessage
	answers   []answeredCallback
	nextMsgID int

	sendErr   error
	editErr   error
	answerErr error
}

// sentMessage records one SendMessage call. We capture chat ID, body, and
// the keyboard so assertions can pinpoint exactly what the handler emitted.
type sentMessage struct {
	ChatID   int64
	HTML     string
	Keyboard InlineKeyboard
	MsgID    int
}

// editedMessage records one EditMessage call — same shape as sentMessage
// plus the message ID being edited.
type editedMessage struct {
	ChatID   int64
	MsgID    int
	HTML     string
	Keyboard InlineKeyboard
}

// answeredCallback records one AnswerCallback call. The toast text is what
// the user sees as the small banner above the keyboard.
type answeredCallback struct {
	CallbackID string
	Toast      string
}

// GetUpdates returns the next queued batch and pops it from the queue. When
// the queue is empty it returns nil (zero-length slice) so the polling loop
// continues without an error.
func (f *fakeBot) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	f.mu.Lock()
	f.pollCount++
	err := f.getUpdatesErr
	var batch []Update
	if len(f.updates) > 0 {
		batch = f.updates[0]
		f.updates = f.updates[1:]
	}
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return batch, nil
}

// SendMessage records the call and returns a fresh, monotonically increasing
// message ID. The returned ID lets tests verify EditMessage references the
// correct prior message.
func (f *fakeBot) SendMessage(ctx context.Context, chatID int64, html string, kb InlineKeyboard) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return 0, f.sendErr
	}
	f.nextMsgID++
	f.sent = append(f.sent, sentMessage{ChatID: chatID, HTML: html, Keyboard: kb, MsgID: f.nextMsgID})
	return f.nextMsgID, nil
}

// EditMessage records the call.
func (f *fakeBot) EditMessage(ctx context.Context, chatID int64, msgID int, html string, kb InlineKeyboard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.editErr != nil {
		return f.editErr
	}
	f.edits = append(f.edits, editedMessage{ChatID: chatID, MsgID: msgID, HTML: html, Keyboard: kb})
	return nil
}

// AnswerCallback records the call.
func (f *fakeBot) AnswerCallback(ctx context.Context, callbackID, toast string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.answerErr != nil {
		return f.answerErr
	}
	f.answers = append(f.answers, answeredCallback{CallbackID: callbackID, Toast: toast})
	return nil
}

// Compile-time interface satisfaction check — if fakeBot ever drifts from
// the Bot interface this fails to compile, which is the cheapest possible
// regression alarm.
var _ Bot = (*fakeBot)(nil)

func TestFakeBotSendMessageAssignsIncrementingIDs(t *testing.T) {
	t.Parallel()
	b := &fakeBot{}
	ctx := context.Background()

	id1, err := b.SendMessage(ctx, 42, "hello", nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	id2, err := b.SendMessage(ctx, 42, "world", nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if id1 != 1 || id2 != 2 {
		t.Fatalf("expected monotonic IDs 1,2 got %d,%d", id1, id2)
	}
	if len(b.sent) != 2 {
		t.Fatalf("expected 2 recorded sends got %d", len(b.sent))
	}
	if b.sent[0].HTML != "hello" || b.sent[1].HTML != "world" {
		t.Fatalf("recorded HTML mismatch: %+v", b.sent)
	}
	if b.sent[0].ChatID != 42 {
		t.Fatalf("chat id mismatch: %d", b.sent[0].ChatID)
	}
}

func TestFakeBotSendMessagePropagatesErrorAndDoesNotRecord(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("send boom")
	b := &fakeBot{sendErr: wantErr}
	if _, err := b.SendMessage(context.Background(), 1, "x", nil); !errors.Is(err, wantErr) {
		t.Fatalf("expected send error to propagate, got %v", err)
	}
	if len(b.sent) != 0 {
		t.Fatalf("failed send should not record, got %d", len(b.sent))
	}
}

func TestFakeBotEditMessageRecordsCallAndPropagatesError(t *testing.T) {
	t.Parallel()
	b := &fakeBot{}
	if err := b.EditMessage(context.Background(), 7, 99, "edited", InlineKeyboard{{{Text: "x", CallbackData: "y:01ARZ3NDEKTSV4RRFFQ69G5FAV"}}}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(b.edits) != 1 {
		t.Fatalf("expected 1 edit got %d", len(b.edits))
	}
	e := b.edits[0]
	if e.ChatID != 7 || e.MsgID != 99 || e.HTML != "edited" {
		t.Fatalf("edit mismatch: %+v", e)
	}
	if len(e.Keyboard) != 1 || len(e.Keyboard[0]) != 1 {
		t.Fatalf("keyboard not preserved: %+v", e.Keyboard)
	}

	b.editErr = errors.New("edit boom")
	if err := b.EditMessage(context.Background(), 7, 99, "again", nil); err == nil {
		t.Fatal("expected edit error to propagate")
	}
	if len(b.edits) != 1 {
		t.Fatalf("failed edit should not record, still %d", len(b.edits))
	}
}

func TestFakeBotAnswerCallbackRecordsAndPropagatesError(t *testing.T) {
	t.Parallel()
	b := &fakeBot{}
	if err := b.AnswerCallback(context.Background(), "cb1", "toast"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(b.answers) != 1 || b.answers[0].CallbackID != "cb1" || b.answers[0].Toast != "toast" {
		t.Fatalf("answer mismatch: %+v", b.answers)
	}

	b.answerErr = errors.New("nope")
	if err := b.AnswerCallback(context.Background(), "cb2", ""); err == nil {
		t.Fatal("expected answer error to propagate")
	}
	if len(b.answers) != 1 {
		t.Fatalf("failed answer should not record, still %d", len(b.answers))
	}
}

func TestFakeBotGetUpdatesQueueDrains(t *testing.T) {
	t.Parallel()
	b := &fakeBot{
		updates: [][]Update{
			{{UpdateID: 1, Message: &Message{ChatID: 10, UserID: 100, Text: "hi"}}},
			{{UpdateID: 2, Callback: &CallbackQuery{ID: "cb", UserID: 100, Data: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"}}},
		},
	}
	ctx := context.Background()
	first, err := b.GetUpdates(ctx, 0, time.Second)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(first) != 1 || first[0].UpdateID != 1 || first[0].Message == nil {
		t.Fatalf("first batch mismatch: %+v", first)
	}
	second, err := b.GetUpdates(ctx, 2, time.Second)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(second) != 1 || second[0].UpdateID != 2 || second[0].Callback == nil {
		t.Fatalf("second batch mismatch: %+v", second)
	}
	empty, err := b.GetUpdates(ctx, 3, time.Second)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty batch after drain, got %+v", empty)
	}
	if b.pollCount != 3 {
		t.Fatalf("pollCount=%d want 3", b.pollCount)
	}
}

func TestFakeBotGetUpdatesPropagatesError(t *testing.T) {
	t.Parallel()
	b := &fakeBot{getUpdatesErr: errors.New("net down")}
	if _, err := b.GetUpdates(context.Background(), 0, time.Second); err == nil {
		t.Fatal("expected getUpdates error to propagate")
	}
}

func TestFakeBotGetUpdatesRespectsCancellation(t *testing.T) {
	t.Parallel()
	b := &fakeBot{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.GetUpdates(ctx, 0, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx.Canceled, got %v", err)
	}
}

func TestNewClientRejectsEmptyToken(t *testing.T) {
	t.Parallel()
	// We can't construct a real client in CI (no token, no network), so we
	// only exercise the fail-fast branch. The real-client construction is
	// covered by the manual smoke test on EC2.
	if _, err := NewClient(""); err == nil {
		t.Fatal("expected err for empty token")
	}
}

func TestToMarkupEmptyKeyboardSignalsOmit(t *testing.T) {
	t.Parallel()
	if _, ok := toMarkup(nil); ok {
		t.Fatal("nil keyboard should signal omit")
	}
	if _, ok := toMarkup(InlineKeyboard{}); ok {
		t.Fatal("empty keyboard should signal omit")
	}
	kb := InlineKeyboard{
		{
			{Text: "A", CallbackData: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
			{Text: "B", CallbackData: "view:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		},
	}
	markup, ok := toMarkup(kb)
	if !ok {
		t.Fatal("non-empty keyboard should signal include")
	}
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 2 {
		t.Fatalf("markup shape mismatch: %+v", markup)
	}
	row := markup.InlineKeyboard[0]
	if row[0].Text != "A" || row[1].Text != "B" {
		t.Fatalf("button labels mismatch: %+v", row)
	}
	if row[0].CallbackData == nil || *row[0].CallbackData != "done:01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("callback data 0 mismatch: %+v", row[0].CallbackData)
	}
	if row[1].CallbackData == nil || *row[1].CallbackData != "view:01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("callback data 1 mismatch: %+v", row[1].CallbackData)
	}
}

func TestToMarkupPreservesDistinctCallbackPointers(t *testing.T) {
	t.Parallel()
	// Regression guard: tgbotapi.InlineKeyboardButton.CallbackData is a *string,
	// so a naive loop that takes the address of the loop variable would have
	// every button point at the same final value. We re-bind by value inside
	// the loop body; this test confirms each pointer has its own backing string.
	kb := InlineKeyboard{
		{
			{Text: "1", CallbackData: "done:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
			{Text: "2", CallbackData: "view:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
			{Text: "3", CallbackData: "active:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		},
	}
	markup, _ := toMarkup(kb)
	got := make([]string, 0, 3)
	for _, btn := range markup.InlineKeyboard[0] {
		if btn.CallbackData == nil {
			t.Fatalf("nil callback data: %+v", btn)
		}
		got = append(got, *btn.CallbackData)
	}
	want := []string{
		"done:01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"view:01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"active:01ARZ3NDEKTSV4RRFFQ69G5FAV",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("callback %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestTranslateUpdateMessage(t *testing.T) {
	t.Parallel()
	// We exercise translateUpdate via a synthetic tgbotapi.Update value so we
	// can verify the chain ReplyToMessage → ReplyTo without standing up a real
	// API client. Imports tgbotapi locally to keep the test next to the
	// translation logic.
	u := makeMessageUpdate(t, 555, 7, 100, 1001, "hello", nil)
	out := translateUpdate(u)
	if out.UpdateID != 7 {
		t.Fatalf("update id: got %d want 7", out.UpdateID)
	}
	if out.Message == nil {
		t.Fatal("Message should be non-nil")
	}
	if out.Message.ChatID != 1001 || out.Message.UserID != 100 || out.Message.Text != "hello" || out.Message.MessageID != 555 {
		t.Fatalf("message fields mismatch: %+v", out.Message)
	}
	if out.Message.ReplyTo != nil {
		t.Fatalf("reply-to should be nil when no reply, got %+v", out.Message.ReplyTo)
	}
	if out.Callback != nil {
		t.Fatalf("callback should be nil for message update, got %+v", out.Callback)
	}
}

func TestTranslateUpdateMessageWithReply(t *testing.T) {
	t.Parallel()
	reply := &Message{MessageID: 100, ChatID: 1001, UserID: 200, Text: "original"}
	u := makeMessageUpdate(t, 101, 6, 100, 1001, "reply", reply)
	out := translateUpdate(u)
	if out.Message == nil || out.Message.ReplyTo == nil {
		t.Fatalf("reply-to chain not preserved: %+v", out.Message)
	}
	if out.Message.ReplyTo.Text != "original" || out.Message.ReplyTo.MessageID != 100 {
		t.Fatalf("reply-to fields mismatch: %+v", out.Message.ReplyTo)
	}
}

func TestTranslateUpdateCallback(t *testing.T) {
	t.Parallel()
	u := makeCallbackUpdate(8, "cbid", 100, 1001, 555, "done:01ARZ3NDEKTSV4RRFFQ69G5FAV")
	out := translateUpdate(u)
	if out.UpdateID != 8 {
		t.Fatalf("update id: got %d want 8", out.UpdateID)
	}
	if out.Callback == nil {
		t.Fatal("Callback should be non-nil")
	}
	if out.Callback.ID != "cbid" || out.Callback.UserID != 100 || out.Callback.ChatID != 1001 || out.Callback.MessageID != 555 || out.Callback.Data != "done:01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("callback fields mismatch: %+v", out.Callback)
	}
	if out.Message != nil {
		t.Fatalf("message should be nil for callback update, got %+v", out.Message)
	}
}

func TestTranslateUpdateSkippedKinds(t *testing.T) {
	t.Parallel()
	// An update with neither Message nor CallbackQuery (e.g. EditedMessage,
	// ChannelPost, InlineQuery) translates to one with both pointer fields
	// nil but the UpdateID preserved so the long-poll offset still advances.
	u := makeBareUpdate(99)
	out := translateUpdate(u)
	if out.UpdateID != 99 {
		t.Fatalf("update id: got %d want 99", out.UpdateID)
	}
	if out.Message != nil || out.Callback != nil {
		t.Fatalf("expected both fields nil, got msg=%+v cb=%+v", out.Message, out.Callback)
	}
}
