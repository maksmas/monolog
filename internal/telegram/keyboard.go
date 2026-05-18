package telegram

// InlineButton is a single inline-keyboard button shown below a Telegram
// message. The Text is the user-visible label; CallbackData is the
// `<action>:<ulid>` payload (decoded by ParseCallback) that Telegram sends
// back as a CallbackQuery when the user taps the button.
//
// Telegram's inline-keyboard callback_data field is capped at 64 bytes — a
// 26-char ULID plus an 8-char action verb and the colon separator stays
// well under the cap, so no truncation is necessary.
type InlineButton struct {
	Text         string
	CallbackData string
}

// InlineKeyboard is a 2D slice of InlineButton values: the outer slice is
// rows, the inner slice is the buttons in that row (left-to-right). An
// empty InlineKeyboard signals "no buttons" to the rendering layer, which
// then sends the message without a `reply_markup` field.
type InlineKeyboard [][]InlineButton
