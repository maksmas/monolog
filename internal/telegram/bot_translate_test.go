package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// makeMessageUpdate synthesizes a tgbotapi.Update containing a Message —
// used by translate-tests so we can exercise translateUpdate without
// touching the network or a real BotAPI client. The signature mirrors the
// shape we get back from Telegram (chat → user → text), plus an optional
// reply-to pointer to test the recursive translation.
func makeMessageUpdate(t *testing.T, msgID int, updateID int, userID, chatID int64, text string, replyTo *Message) tgbotapi.Update {
	t.Helper()
	m := &tgbotapi.Message{
		MessageID: msgID,
		Text:      text,
		Chat:      &tgbotapi.Chat{ID: chatID},
		From:      &tgbotapi.User{ID: userID},
	}
	if replyTo != nil {
		m.ReplyToMessage = &tgbotapi.Message{
			MessageID: replyTo.MessageID,
			Text:      replyTo.Text,
			Chat:      &tgbotapi.Chat{ID: replyTo.ChatID},
			From:      &tgbotapi.User{ID: replyTo.UserID},
		}
	}
	return tgbotapi.Update{UpdateID: updateID, Message: m}
}

// makeCallbackUpdate synthesizes a tgbotapi.Update containing a CallbackQuery
// attached to a bot message. Mirrors the shape we get on inline-keyboard
// button taps: callback ID, sender user ID, the bot-message reference, and
// the callback payload.
func makeCallbackUpdate(updateID int, callbackID string, userID, chatID int64, msgID int, data string) tgbotapi.Update {
	cq := &tgbotapi.CallbackQuery{
		ID:   callbackID,
		Data: data,
		From: &tgbotapi.User{ID: userID},
		Message: &tgbotapi.Message{
			MessageID: msgID,
			Chat:      &tgbotapi.Chat{ID: chatID},
		},
	}
	return tgbotapi.Update{UpdateID: updateID, CallbackQuery: cq}
}

// makeBareUpdate synthesizes an Update with neither Message nor CallbackQuery
// (e.g. EditedMessage, ChannelPost, InlineQuery) so we can verify the
// translation preserves UpdateID but leaves the union fields nil.
func makeBareUpdate(updateID int) tgbotapi.Update {
	return tgbotapi.Update{UpdateID: updateID}
}
