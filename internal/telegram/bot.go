package telegram

import (
	"context"
	"fmt"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Update is the neutral DTO emitted by Bot.GetUpdates. It carries either a
// Message (plain text or reply) or a CallbackQuery (button tap) — at most one
// of the two pointer fields is non-nil per Update. The dispatcher in
// internal/telegram/handler.go branches on which field is populated.
//
// Updates the bot does not care about (channel posts, edited messages, inline
// queries, etc.) are translated to an Update with both pointer fields nil; the
// caller skips them. We surface the UpdateID even on skipped updates so the
// long-polling offset still advances and the same update isn't redelivered.
type Update struct {
	UpdateID int64
	Message  *Message
	Callback *CallbackQuery
}

// Message is the neutral DTO for a Telegram message. We strip everything we
// don't use (entities, dates, forwarded-from metadata, media payloads, etc.)
// so handler code and tests have a minimal surface to reason about.
//
// ReplyTo is non-nil only when the user composed this message by tapping
// "reply" on a previous message from the bot — that signals the note-append
// path. We deliberately do not recurse into ReplyTo.ReplyTo because Telegram
// truncates the chain to one level anyway (see tgbotapi.Message.ReplyToMessage
// docs); a single optional pointer is sufficient.
type Message struct {
	ChatID    int64
	MessageID int
	UserID    int64
	Text      string
	ReplyTo   *Message
}

// CallbackQuery is the neutral DTO for an inline-keyboard button tap. Data
// carries the `<action>:<ulid>` payload encoded by BuildSummaryKeyboard /
// BuildDetailKeyboard and decoded by ParseCallback.
//
// MessageID identifies the message under the tapped button so EditMessage
// calls can replace it in place (the summary↔detail toggle and the post-Done
// strike-through both rely on this). The ID field is the callback-query ID
// itself, distinct from MessageID — answering with AnswerCallback uses ID.
type CallbackQuery struct {
	ID        string
	UserID    int64
	ChatID    int64
	MessageID int
	Data      string
}

// Bot is the small interface the handler depends on. Production wires it to
// realBot wrapping *tgbotapi.BotAPI; tests pass a recording fake. The
// interface stays as narrow as possible: four methods covering the entire
// telegram-side need (poll updates, send a message, edit a message, answer a
// callback). Anything beyond that — media uploads, chat-administration calls,
// inline queries — is out of scope for this bot.
type Bot interface {
	// GetUpdates fetches new updates since offset, blocking up to timeout for
	// long-polling. The implementation must honor ctx.Done() and return
	// ctx.Err() if cancellation occurs mid-poll, leaving the polling loop a
	// clean shutdown path.
	GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error)
	// SendMessage sends an HTML-formatted message to chatID with an optional
	// inline keyboard (pass an empty InlineKeyboard for no buttons). Returns
	// the assigned Telegram message ID so callers can correlate later edits.
	SendMessage(ctx context.Context, chatID int64, html string, kb InlineKeyboard) (msgID int, err error)
	// EditMessage edits the text and inline keyboard of a previously sent
	// message. Used to swap summary↔detail views and to apply the
	// strike-through after a Done callback. Pass an empty InlineKeyboard to
	// remove all buttons.
	EditMessage(ctx context.Context, chatID int64, msgID int, html string, kb InlineKeyboard) error
	// AnswerCallback answers a callback query, stopping the loading spinner on
	// the user's button. The optional toast text (max ~200 chars) is shown as
	// a small banner; pass "" for a silent answer.
	AnswerCallback(ctx context.Context, callbackID string, toast string) error
}

// realBot is the production Bot implementation backed by tgbotapi.BotAPI.
// Constructed via NewClient; tests never instantiate this directly.
type realBot struct {
	api *tgbotapi.BotAPI
}

// NewClient constructs a production Bot. The empty-token check fails fast so
// a misconfigured systemd unit (missing EnvironmentFile, for example) crashes
// at startup rather than silently long-polling against an invalid token and
// spinning on 401 errors. The error message intentionally avoids echoing the
// token (even when empty) so we never accidentally log a partial secret.
func NewClient(token string) (Bot, error) {
	if token == "" {
		return nil, fmt.Errorf("telegram: empty token")
	}
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram: new bot api: %w", err)
	}
	return &realBot{api: api}, nil
}

// GetUpdates wraps tgbotapi.BotAPI.GetUpdates with context cancellation. The
// underlying library has no ctx-aware variant, so we run the call in a
// goroutine and select on ctx.Done() — on cancellation we invoke
// StopReceivingUpdates so the HTTP long-poll connection is torn down, and
// return ctx.Err(). The goroutine is allowed to finish naturally; its result
// is discarded.
func (b *realBot) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	cfg := tgbotapi.NewUpdate(int(offset))
	// tgbotapi expects seconds; round-trip via int(time.Duration / time.Second)
	// keeps fractional sub-second values from being silently dropped to 0.
	cfg.Timeout = int(timeout / time.Second)
	if cfg.Timeout <= 0 {
		// Telegram's getUpdates rejects negative timeouts and treats 0 as
		// short-poll; clamp to a safe minimum so a misconfigured tiny timeout
		// doesn't hammer the API.
		cfg.Timeout = 1
	}

	type result struct {
		updates []tgbotapi.Update
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		u, err := b.api.GetUpdates(cfg)
		resCh <- result{updates: u, err: err}
	}()

	select {
	case <-ctx.Done():
		// Tear down the long-poll HTTP connection so the goroutine returns.
		b.api.StopReceivingUpdates()
		return nil, ctx.Err()
	case r := <-resCh:
		if r.err != nil {
			return nil, fmt.Errorf("telegram: get updates: %w", r.err)
		}
		out := make([]Update, 0, len(r.updates))
		for _, raw := range r.updates {
			out = append(out, translateUpdate(raw))
		}
		return out, nil
	}
}

// translateUpdate converts a tgbotapi.Update into our neutral Update DTO.
// Updates we don't care about (edited messages, channel posts, inline
// queries) translate to an Update with both Message and Callback nil — the
// UpdateID is preserved so the long-poll offset advances.
func translateUpdate(u tgbotapi.Update) Update {
	out := Update{UpdateID: int64(u.UpdateID)}
	if m := u.Message; m != nil {
		out.Message = translateMessage(m)
	}
	if cq := u.CallbackQuery; cq != nil {
		out.Callback = translateCallbackQuery(cq)
	}
	return out
}

// translateMessage converts a tgbotapi.Message into our neutral Message DTO.
// The fields we care about are the chat ID, message ID, sender user ID, the
// text, and (optionally) the parent message a user replied to.
func translateMessage(m *tgbotapi.Message) *Message {
	if m == nil {
		return nil
	}
	out := &Message{
		MessageID: m.MessageID,
		Text:      m.Text,
	}
	if m.Chat != nil {
		out.ChatID = m.Chat.ID
	}
	if m.From != nil {
		out.UserID = m.From.ID
	}
	if m.ReplyToMessage != nil {
		out.ReplyTo = translateMessage(m.ReplyToMessage)
	}
	return out
}

// translateCallbackQuery converts a tgbotapi.CallbackQuery into our neutral
// CallbackQuery DTO. ChatID and MessageID come from the embedded Message
// (which is always present for inline-keyboard callbacks attached to bot
// messages, the only case we trigger).
func translateCallbackQuery(cq *tgbotapi.CallbackQuery) *CallbackQuery {
	if cq == nil {
		return nil
	}
	out := &CallbackQuery{
		ID:   cq.ID,
		Data: cq.Data,
	}
	if cq.From != nil {
		out.UserID = cq.From.ID
	}
	if cq.Message != nil {
		out.MessageID = cq.Message.MessageID
		if cq.Message.Chat != nil {
			out.ChatID = cq.Message.Chat.ID
		}
	}
	return out
}

// SendMessage sends an HTML-formatted message to chatID and returns the
// assigned Telegram message ID. The HTML parse mode is fixed — we don't
// expose a knob because every render path in this package emits HTML.
func (b *realBot) SendMessage(ctx context.Context, chatID int64, html string, kb InlineKeyboard) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	msg := tgbotapi.NewMessage(chatID, html)
	msg.ParseMode = "HTML"
	if markup, ok := toMarkup(kb); ok {
		msg.ReplyMarkup = markup
	}
	sent, err := b.api.Send(msg)
	if err != nil {
		return 0, fmt.Errorf("telegram: send message: %w", err)
	}
	return sent.MessageID, nil
}

// EditMessage edits the text and inline keyboard of an existing message. An
// empty keyboard is sent as an empty markup so all buttons are removed
// (Telegram's edit endpoint replaces the markup wholesale).
func (b *realBot) EditMessage(ctx context.Context, chatID int64, msgID int, html string, kb InlineKeyboard) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	markup, _ := toMarkup(kb)
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, html, markup)
	edit.ParseMode = "HTML"
	if _, err := b.api.Send(edit); err != nil {
		return fmt.Errorf("telegram: edit message: %w", err)
	}
	return nil
}

// AnswerCallback answers a callback query, stopping the user-facing loading
// spinner. The toast text is optional — pass "" for a silent answer.
func (b *realBot) AnswerCallback(ctx context.Context, callbackID, toast string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	cb := tgbotapi.NewCallback(callbackID, toast)
	if _, err := b.api.Request(cb); err != nil {
		return fmt.Errorf("telegram: answer callback: %w", err)
	}
	return nil
}

// toMarkup translates our neutral InlineKeyboard into the tgbotapi-flavored
// InlineKeyboardMarkup. Returns (zero, false) for an empty keyboard so the
// caller can skip setting ReplyMarkup entirely on send (sending an empty
// markup attaches no buttons but still includes the field; for SendMessage we
// want to omit the field, for EditMessage we send an empty markup to clear).
func toMarkup(kb InlineKeyboard) (tgbotapi.InlineKeyboardMarkup, bool) {
	if len(kb) == 0 {
		return tgbotapi.InlineKeyboardMarkup{}, false
	}
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(kb))
	for _, row := range kb {
		cells := make([]tgbotapi.InlineKeyboardButton, 0, len(row))
		for _, btn := range row {
			data := btn.CallbackData
			cells = append(cells, tgbotapi.InlineKeyboardButton{
				Text:         btn.Text,
				CallbackData: &data,
			})
		}
		rows = append(rows, cells)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...), true
}
